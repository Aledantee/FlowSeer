package lex

import (
	"runtime"
	"testing"
)

// allocationCeiling and allocationFloor bound what lexing one input may
// allocate, as bytes per input byte plus a fixed allowance for the
// result header and the smallest token slice.
//
// The ceiling is deliberately far above what the lexer actually spends,
// because the property under test is that the cost stays linear in the
// input rather than that it hits a particular constant. A regression
// that made the scan quadratic, retained a copy of the source per token,
// or grew a slice one element at a time would blow past this by orders
// of magnitude; a change that merely made a token a few bytes wider
// would not, and should not fail a fuzz run.
const (
	allocationCeiling = 1024
	allocationFloor   = 64 << 10
)

// FuzzLex asserts the lexer's totality contract on arbitrary bytes.
//
// The Go fuzzing engine catches the panic and the hang on its own. The
// three properties it cannot see are checked here: allocation stays
// bounded against the input length, the tokens and their implied gaps
// tile the input exactly, and lexing the same bytes twice yields the
// same tokens. Position totality is the invariant every later pass
// depends on when it reconstructs a declaration's source text, and
// nothing but a whole-input check can establish it.
//
// Both comment-termination modes are exercised on every input, since
// they are separate paths through the scanner and only one of them can
// report an unterminated comment.
func FuzzLex(f *testing.F) {
	f.Add([]byte("sysDescr OBJECT-TYPE\n  SYNTAX OCTET STRING\n  ::= { system 1 }\n"))
	f.Add([]byte("\xef\xbb\xbfFOO-MIB DEFINITIONS ::= BEGIN\nEND\n"))
	f.Add([]byte("-- comment -- trailing\r\n---------\r\n"))
	f.Add([]byte("'0F0'H '1010'B '10'"))
	f.Add([]byte("\"unterminated"))
	f.Add([]byte("a -- never closed"))
	f.Add([]byte("1..4 sysDescr.0 foo- foo--bar"))
	f.Add([]byte("\"Z\xfcrich\"\x00\x0b\x0c\x1f\x7f\xff"))

	f.Fuzz(func(t *testing.T, data []byte) {
		for _, mode := range []CommentMode{CommentEndOfLine, CommentPaired} {
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			r := Lex(data, Options{File: "fuzz.mib", Comments: mode})
			runtime.ReadMemStats(&after)

			spent := after.TotalAlloc - before.TotalAlloc
			budget := uint64(len(data))*allocationCeiling + allocationFloor
			if spent > budget {
				t.Fatalf("mode %d: lexing %d bytes allocated %d, want at most %d", mode, len(data), spent, budget)
			}

			wantTiling(t, data, r)

			again := Lex(data, Options{File: "fuzz.mib", Comments: mode})
			if len(again.Tokens) != len(r.Tokens) {
				t.Fatalf("mode %d: relexing gave %d tokens, want %d", mode, len(again.Tokens), len(r.Tokens))
			}
			for i := range r.Tokens {
				if again.Tokens[i] != r.Tokens[i] {
					t.Fatalf("mode %d: token %d relexed as %+v, want %+v", mode, i, again.Tokens[i], r.Tokens[i])
				}
			}

			if len(again.Diagnostics) != len(r.Diagnostics) {
				t.Fatalf("mode %d: relexing gave %d diagnostics, want %d", mode, len(again.Diagnostics), len(r.Diagnostics))
			}
			for i := range r.Diagnostics {
				if again.Diagnostics[i].Code() != r.Diagnostics[i].Code() ||
					again.Diagnostics[i].Position() != r.Diagnostics[i].Position() {
					t.Fatalf("mode %d: diagnostic %d relexed differently", mode, i)
				}
			}

			// Reading every token's text must also be total: an offset that
			// slipped outside the source would panic here rather than
			// silently produce a wrong span.
			for _, tok := range r.Tokens {
				_ = r.Text(tok)
				_ = r.StringValue(tok)
			}
		}
	})
}
