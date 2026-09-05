package smi

import (
	"os"
	"path/filepath"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/smi/internal/frame"
	"go.aledante.io/FlowSeer/src/protocol/smi/internal/lex"
)

// A module name comes out of a file nobody here wrote and is joined with
// a search path to find the file it names, so a name that climbs out of
// the search path must resolve to nothing. filepath.Join cleans the
// climb away instead of refusing it, which is why the containment check
// is written out rather than left to Join.
func TestFindModuleStaysUnderTheSearchPath(t *testing.T) {
	root := t.TempDir()
	search := filepath.Join(root, "mibs")
	if err := os.MkdirAll(search, 0o700); err != nil {
		t.Fatalf("creating %s: %v", search, err)
	}

	inside := filepath.Join(search, "GOOD-MIB")
	if err := os.WriteFile(inside, []byte("x"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", inside, err)
	}
	if err := os.WriteFile(filepath.Join(root, "SECRET-MIB"), []byte("x"), 0o600); err != nil {
		t.Fatalf("writing SECRET-MIB: %v", err)
	}

	if got, ok := FindModule("GOOD-MIB", []string{search}); !ok || got != inside {
		t.Errorf("FindModule(GOOD-MIB) = %q, %v; want %q, true", got, ok, inside)
	}

	for _, name := range []string{
		"../SECRET-MIB",
		"../../" + filepath.Base(root) + "/SECRET-MIB",
		filepath.Join("..", "SECRET-MIB"),
	} {
		if got, ok := FindModule(name, []string{search}); ok {
			t.Errorf("FindModule(%q) reached %q outside the search path", name, got)
		}
	}
}

// The token after FROM is a module name only when it is spelled like
// one. Anything else in that slot is a malformed IMPORTS list, and
// taking its text on trust would hand a search-path join whatever the
// file happened to say.
func TestImportsRejectsANonNameAfterFrom(t *testing.T) {
	src := []byte(`IMPORTS sym FROM "../SECRET-MIB";`)
	res := lex.Lex(src, lex.Options{File: "test.mib"})

	fm := frame.Module{
		Name: "TEST-MIB",
		Frames: []frame.Frame{{
			Kind:   frame.KindImports,
			Span:   frame.Span{Start: 0, End: int32(len(src))},
			Tokens: res.Tokens,
		}},
	}

	got := imports(fm, res)
	if len(got) != 0 {
		t.Errorf("got %v, want no imports: the FROM target is a quoted string", got)
	}
}
