package errs

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var (
	testCodePrivDecrypt = NewCode("errstest/priv-decrypt")
	testCodeAuthFail    = NewCode("errstest/auth-fail")
)

// Covers AE1: a peer error reconstructed from a code — no shared identity
// with the local sentinel — still matches it.
func TestIsMatchesByCode(t *testing.T) {
	sentinel := New().Code(testCodePrivDecrypt).Msg("privacy decryption failed")
	decoded := New().Code(testCodePrivDecrypt).Msg("peer said privacy decryption failed")

	if !errors.Is(decoded, sentinel) {
		t.Error("errors.Is on equal codes = false, want true")
	}
	if errors.Is(decoded, New().Code(testCodeAuthFail).Msg("other")) {
		t.Error("errors.Is on differing codes = true, want false")
	}
}

func TestIsReflexive(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "coded", err: New().Code(testCodeAuthFail).Msg("coded")},
		{name: "uncoded", err: Msg("uncoded")},
		{name: "wrapped", err: Wrap(Msg("inner"), "outer")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.err, tc.err) {
				t.Error("errors.Is(e, e) = false, want true")
			}
		})
	}
}

func TestIsWithNilOperands(t *testing.T) {
	err := New().Code(testCodeAuthFail).Msg("coded")

	if errors.Is(err, nil) {
		t.Error("errors.Is(e, nil) = true, want false")
	}
	if errors.Is(nil, err) {
		t.Error("errors.Is(nil, e) = true, want false")
	}
	if err.(*Error).Is(nil) {
		t.Error("Is(nil) = true, want false")
	}
}

// uncomparableError carries a map, so == on it panics — the samber/oops #95
// bug class. It must not reach a comparison.
type uncomparableError struct {
	fields map[string]string
}

func (e uncomparableError) Error() string { return "uncomparable" }

func TestIsWithUncomparableCauses(t *testing.T) {
	uncomparable := uncomparableError{fields: map[string]string{"k": "v"}}
	err := New().Code(testCodePrivDecrypt).Cause(uncomparable).Msg("outer")

	if !errors.Is(err, New().Code(testCodePrivDecrypt).Msg("peer")) {
		t.Error("code match failed through an uncomparable cause")
	}
	if errors.Is(err, Msg("unrelated")) {
		t.Error("errors.Is matched an unrelated sentinel")
	}
}

func TestCodedErrorDoesNotMatchUncodedSentinel(t *testing.T) {
	sentinel := Msg("unrelated sentinel")
	err := New().Code(testCodeAuthFail).Msg("coded")

	if errors.Is(err, sentinel) {
		t.Error("coded error matched an uncoded sentinel it does not wrap")
	}
}

func TestCodeOfSearchesChain(t *testing.T) {
	inner := New().Code(testCodePrivDecrypt).Msg("inner")
	outer := Wrap(inner, "outer")

	got, ok := CodeOf(outer)
	if !ok || got != testCodePrivDecrypt {
		t.Errorf("CodeOf(outer) = %q, %v, want %q, true", got, ok, testCodePrivDecrypt)
	}

	if _, ok := CodeOf(Msg("uncoded")); ok {
		t.Error("CodeOf on an uncoded error reported a code")
	}
	if _, ok := CodeOf(nil); ok {
		t.Error("CodeOf(nil) reported a code")
	}
}

// The outermost code wins, so a wrapper can restate identity for a boundary.
func TestCodeOfPrefersOutermost(t *testing.T) {
	inner := New().Code(testCodePrivDecrypt).Msg("inner")
	outer := From(inner).Code(testCodeAuthFail).Msg("outer")

	if got, _ := CodeOf(outer); got != testCodeAuthFail {
		t.Errorf("CodeOf = %q, want the outermost code %q", got, testCodeAuthFail)
	}
}

func TestNewCodePanics(t *testing.T) {
	tests := []struct {
		name string
		code string
	}{
		{name: "duplicate", code: string(testCodePrivDecrypt)},
		{name: "no slash", code: "privdecrypt"},
		{name: "empty package segment", code: "/priv-decrypt"},
		{name: "empty name segment", code: "errstest/"},
		{name: "empty", code: ""},
		{name: "uppercase", code: "errstest/PrivDecrypt"},
		{name: "leading dash", code: "errstest/-priv"},
		{name: "extra segment", code: "errstest/priv/decrypt"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("NewCode(%q) did not panic", tc.code)
				}
			}()

			NewCode(tc.code)
		})
	}
}

func TestCodesEnumeratesRegistry(t *testing.T) {
	codes := Codes()

	if !slices.Contains(codes, testCodePrivDecrypt) {
		t.Errorf("Codes() = %v, want it to contain %q", codes, testCodePrivDecrypt)
	}
	if !slices.IsSorted(codes) {
		t.Error("Codes() is not sorted")
	}

	seen := make(map[Code]struct{}, len(codes))
	for _, c := range codes {
		if _, dup := seen[c]; dup {
			t.Errorf("Codes() contains %q twice", c)
		}
		seen[c] = struct{}{}
	}
}

// The registry only sees codes linked into the running binary, so the
// repo-wide gate is a source scan: every NewCode literal in non-test code
// must be well formed and unique.
func TestDeclaredCodesAreUniqueRepoWide(t *testing.T) {
	root := repoRoot(t)
	declared := make(map[string]string)

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name != "." && (strings.HasPrefix(name, ".") || name == "testdata") {
				return fs.SkipDir
			}

			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		for _, decl := range newCodeLiterals(t, path) {
			if prev, dup := declared[decl.code]; dup {
				t.Errorf("error code %q declared in both %s and %s", decl.code, prev, decl.pos)
			}
			if err := validateCode(decl.code); err != nil {
				t.Errorf("%s: %v", decl.pos, err)
			}

			declared[decl.code] = decl.pos
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
}

type codeDecl struct {
	code string
	pos  string
}

// newCodeLiterals returns the NewCode declarations in one file. A NewCode
// call with a non-literal argument fails the test: the gate cannot verify
// what it cannot read.
func newCodeLiterals(t *testing.T, path string) []codeDecl {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	var decls []codeDecl

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isNewCode(call.Fun) {
			return true
		}

		pos := fset.Position(call.Pos()).String()
		if len(call.Args) != 1 {
			t.Errorf("%s: NewCode takes one argument", pos)
			return true
		}

		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			t.Errorf("%s: NewCode argument must be a string literal so the uniqueness gate can read it", pos)
			return true
		}

		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Errorf("%s: unreadable NewCode literal %s", pos, lit.Value)
			return true
		}

		decls = append(decls, codeDecl{code: value, pos: pos})

		return true
	})

	return decls
}

func isNewCode(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name == "NewCode"
	case *ast.SelectorExpr:
		pkg, ok := f.X.(*ast.Ident)
		return ok && pkg.Name == "errs" && f.Sel.Name == "NewCode"
	default:
		return false
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting working directory: %v", err)
	}

	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(data), "module go.aledante.io/FlowSeer\n") {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found above the errs package")
		}

		dir = parent
	}
}
