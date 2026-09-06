// Package secretguard checks Go source for exported struct fields that
// hold credential material in a raw string or []byte instead of a
// secret.Value. It scans hand-written source under src/; dot-directories,
// testdata, and the nested src/edge/netpen module are not scanned.
package secretguard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// secretNames are the lowercased field-name fragments that mark a field
// as carrying credential material.
var secretNames = []string{
	"password",
	"passphrase",
	"secret",
	"privatekey",
	"credential",
}

// rawTypes are the type expressions a secret-named field may not have.
// A secret.Value renders redacted; these render what they hold.
var rawTypes = []string{
	"string",
	"[]byte",
	"*string",
	"*[]byte",
}

// exemptDirs are directory paths relative to the scan root that the walk
// skips. netpen is a separate module and cannot import the carrier.
var exemptDirs = []string{
	filepath.Join("edge", "netpen"),
}

// rawField is one exported field that carries credential material in a
// type that renders it.
type rawField struct {
	path     string
	typeName string
	field    string
}

func (f rawField) String() string {
	return f.path + ": " + f.typeName + "." + f.field
}

// scanRawSecretFields walks root and reports every raw secret field it
// finds, with paths relative to root.
func scanRawSecretFields(root string) ([]rawField, error) {
	var found []rawField

	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			name := d.Name()
			if name != "." && (strings.HasPrefix(name, ".") || name == "testdata") {
				return filepath.SkipDir
			}

			rel, _ := filepath.Rel(root, path)
			for _, exempt := range exemptDirs {
				if rel == exempt || strings.HasPrefix(rel, exempt+string(filepath.Separator)) {
					return filepath.SkipDir
				}
			}

			return nil
		}

		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}

		rel, _ := filepath.Rel(root, path)
		found = append(found, rawFieldsIn(f, rel)...)

		return nil
	})
	return found, err
}

// rawFieldsIn reports the raw secret fields of the struct types declared
// at package level in f, including a struct type nested inside one of
// them. A package-level declaration is the shape another package can
// construct, which is what makes it worth a declaration-site rule; the
// walk stops there rather than following values into function bodies.
func rawFieldsIn(f *ast.File, rel string) []rawField {
	var found []rawField

	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}

		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}

			ast.Inspect(typeSpec.Type, func(n ast.Node) bool {
				st, ok := n.(*ast.StructType)
				if !ok {
					return true
				}

				for _, field := range st.Fields.List {
					if !isRawType(field.Type) {
						continue
					}
					for _, name := range field.Names {
						if name.IsExported() && isSecretName(name.Name) {
							found = append(found, rawField{rel, typeSpec.Name.Name, name.Name})
						}
					}
				}

				return true
			})
		}
	}

	return found
}

func isSecretName(name string) bool {
	lower := strings.ToLower(name)
	for _, fragment := range secretNames {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

func isRawType(expr ast.Expr) bool {
	return slices.Contains(rawTypes, types.ExprString(expr))
}

func TestScanRawSecretFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		want   []string
	}{
		{
			name:   "raw string field",
			source: "package p\ntype O struct {\n\tPassword string\n}\n",
			want:   []string{"a.go: O.Password"},
		},
		{
			name:   "raw byte slice field",
			source: "package p\ntype O struct {\n\tPrivateKeyPEM []byte\n}\n",
			want:   []string{"a.go: O.PrivateKeyPEM"},
		},
		{
			name:   "pointer to a raw type",
			source: "package p\ntype O struct {\n\tAuthPassword *string\n}\n",
			want:   []string{"a.go: O.AuthPassword"},
		},
		{
			name:   "nested struct type",
			source: "package p\ntype O struct {\n\tAuth struct {\n\t\tPassphrase string\n\t}\n}\n",
			want:   []string{"a.go: O.Passphrase"},
		},
		{
			name:   "several fields in one struct",
			source: "package p\ntype O struct {\n\tAuthPassphrase, PrivPassphrase string\n}\n",
			want:   []string{"a.go: O.AuthPassphrase", "a.go: O.PrivPassphrase"},
		},
		{
			name:   "carrier type",
			source: "package p\nimport \"go.aledante.io/FlowSeer/src/common/secret\"\ntype O struct {\n\tPassword secret.Value\n}\n",
		},
		{
			name:   "unexported field",
			source: "package p\ntype O struct {\n\tpassword string\n}\n",
		},
		{
			name:   "public material",
			source: "package p\ntype O struct {\n\tCACertPEM []byte\n\tHostKeySHA256 string\n}\n",
		},
		{
			name:   "struct declared inside a function",
			source: "package p\nfunc f() { _ = struct{ Password string }{Password: \"x\"} }\n",
		},
		{
			name:   "secret-named local variable",
			source: "package p\nfunc f() { password := \"x\"; _ = password }\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "a.go"), []byte(tc.source), 0o600); err != nil {
				t.Fatalf("writing fixture: %v", err)
			}

			found, err := scanRawSecretFields(root)
			if err != nil {
				t.Fatalf("scanning %s: %v", root, err)
			}

			var got []string
			for _, f := range found {
				got = append(got, f.String())
			}
			if strings.Join(got, "; ") != strings.Join(tc.want, "; ") {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
