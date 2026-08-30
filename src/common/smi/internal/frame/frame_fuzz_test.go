package frame

import (
	"runtime"
	"testing"
)

// allocationCeiling and allocationFloor bound what framing one input may
// allocate, as bytes per input byte plus a fixed allowance for the token
// slice and the smallest frame slice.
//
// The ceiling is far above what the framer spends, because the property
// under test is that the cost stays linear in the input rather than that
// it hits a particular constant. A scan that restarted from the top of a
// declaration, or a head test that materialized a string per token,
// would blow past this by orders of magnitude; a frame growing a field
// would not, and should not fail a fuzz run. The budget covers two
// readings of the source, since a file that frames badly under the
// end-of-line rule is read a second time under the paired one.
const (
	allocationCeiling = 4096
	allocationFloor   = 256 << 10
)

// FuzzCut asserts the framer's totality contract on arbitrary bytes.
//
// Framing is the recovery boundary every later pass leans on, so the
// properties that matter are structural rather than semantic: the run
// terminates, it allocates in proportion to its input, the frames tile
// their modules, and the same bytes frame the same way twice. The fuzz
// engine catches the panic and the hang; the rest is checked here.
func FuzzCut(f *testing.F) {
	f.Add([]byte("TEST-MIB DEFINITIONS ::= BEGIN\nsysUpTime OBJECT-TYPE ::= { system 3 }\nEND\n"))
	f.Add([]byte("A DEFINITIONS ::= BEGIN\norg OBJECT IDENTIFIER ::= { iso 3 }\nFoo ::= OBJECT IDENTIFIER\nEND\n"))
	f.Add([]byte("A DEFINITIONS ::= BEGIN\nlinkDown TRAP-TYPE ENTERPRISE snmp VARIABLES { ifIndex } ::= 2\nEND\n"))
	f.Add([]byte("A DEFINITIONS ::= BEGIN\nB MACRO ::= BEGIN \"END\" -- END\nEND\nEND\n"))
	f.Add([]byte("A DEFINITIONS ::= BEGIN\nIMPORTS x FROM Y MODULE-COMPLIANCE FROM Z;\nEND\n"))
	f.Add([]byte("A DEFINITIONS ::= BEGIN\nf OBJECT-TYPE DEFVAL { { a, b } } ::= { x 1 }\nEND\nEND\ntrailing\n"))
	f.Add([]byte("A DEFINITIONS ::= BEGIN\nEND\nB DEFINITIONS ::= BEGIN\nEND\n"))
	f.Add([]byte("no module header at all"))
	f.Add([]byte("A DEFINITIONS ::= BEGIN\nx OBJECT-TYPE DESCRIPTION \"unterminated\nEND\n"))
	f.Add([]byte("A DEFINITIONS ::= BEGIN\nT ::= {{{{{{{{{{\nEND\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		file := Cut(data, Options{File: "fuzz.mib"})
		runtime.ReadMemStats(&after)

		spent := after.TotalAlloc - before.TotalAlloc
		budget := uint64(len(data))*allocationCeiling + allocationFloor
		if spent > budget {
			t.Fatalf("framing %d bytes allocated %d, want at most %d", len(data), spent, budget)
		}

		if err := checkTiling(len(data), file); err != nil {
			t.Fatal(err)
		}

		// Reading a frame's text must be total too: a span that slipped
		// outside the source would panic here rather than silently
		// describe the wrong declaration.
		for _, fr := range file.Frames() {
			_ = file.Text(fr.Span)
		}

		again := Cut(data, Options{File: "fuzz.mib"})
		if again.Comments != file.Comments {
			t.Fatalf("recut chose comment mode %v, want %v", again.Comments, file.Comments)
		}
		if len(again.Modules) != len(file.Modules) {
			t.Fatalf("recut gave %d modules, want %d", len(again.Modules), len(file.Modules))
		}
		for i := range file.Modules {
			got, want := again.Modules[i], file.Modules[i]
			if got.Name != want.Name || got.Span != want.Span || len(got.Frames) != len(want.Frames) {
				t.Fatalf("module %d recut as %q %v with %d frames, want %q %v with %d",
					i, got.Name, got.Span, len(got.Frames), want.Name, want.Span, len(want.Frames))
			}
			for j := range want.Frames {
				if got.Frames[j].Kind != want.Frames[j].Kind || got.Frames[j].Span != want.Frames[j].Span {
					t.Fatalf("module %d frame %d recut differently", i, j)
				}
			}
		}
	})
}
