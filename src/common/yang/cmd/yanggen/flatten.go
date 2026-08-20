package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/openconfig/goyang/pkg/yang"
)

// flatten.go merges submodules into their parent module at the
// statement level before goyang sees them. goyang's typedef and
// grouping dictionaries are keyed per (sub)module node, so a typedef
// defined in a submodule is invisible to modules importing the parent
// (the IOS-XE native tree hits this: Cisco-IOS-XE-eem referencing
// ios:logging-level-type, defined in the Cisco-IOS-XE-logging
// submodule). RFC 7950 §5.1 makes submodule definitions part of the
// module, so inlining every included submodule's body into the parent
// is semantics-preserving — provided the prefix scopes agree, which
// [flattenModule] verifies and otherwise rejects loudly.

// conflictError marks a module whose submodules cannot be inlined
// without changing meaning (sibling prefix conflicts, belongs-to
// prefix mismatch). The loader falls back to goyang's stock include
// handling for these modules — which is correct except for the
// cross-submodule typedef visibility gap flattening exists to fix.
type conflictError struct {
	module string
	issue  string
}

// Error returns the conflict diagnostic.
func (e *conflictError) Error() string {
	return fmt.Sprintf("module %q cannot be flattened: %s", e.module, e.issue)
}

// rawSource is one raw-parsed .yang file: its root statement, whether
// it is a module or submodule, and its discovery record.
type rawSource struct {
	stmt *yang.Statement
	kind string // "module" or "submodule"
	file SourceFile
}

// rawParse parses every non-skipped source with goyang's generic
// statement parser and indexes by the declared name, verifying it
// matches the filename-derived name so the lockfile's hash mapping
// stays honest.
func rawParse(vendor string, files map[string]SourceFile, skip map[string]Skip) (map[string]*rawSource, error) {
	out := make(map[string]*rawSource, len(files))
	for _, name := range sortedKeys(files) {
		if _, skipped := skip[name]; skipped {
			continue
		}
		f := files[name]
		data, err := os.ReadFile(f.Path)
		if err != nil {
			return nil, &LoadError{Vendor: vendor, Module: name, Issue: "read source", wrapped: err}
		}
		stmts, err := yang.Parse(string(data), f.Path)
		if err != nil {
			return nil, &LoadError{Vendor: vendor, Module: name, Issue: "parse", wrapped: err}
		}
		var root *yang.Statement
		for _, s := range stmts {
			if s.Keyword == "module" || s.Keyword == "submodule" {
				if root != nil {
					return nil, &LoadError{Vendor: vendor, Module: name, Issue: "file declares more than one (sub)module"}
				}
				root = s
			}
		}
		if root == nil {
			return nil, &LoadError{Vendor: vendor, Module: name, Issue: "file declares no module or submodule"}
		}
		if root.Argument != name {
			return nil, &LoadError{
				Vendor: vendor, Module: name,
				Issue: fmt.Sprintf("file declares %s %q; the filename says %q — rename one so hashes track the right source", root.Keyword, root.Argument, name),
			}
		}
		out[name] = &rawSource{stmt: root, kind: root.Keyword, file: f}
	}
	return out, nil
}

// flattenModule renders the module named name with every included
// submodule's body inlined (recursively), submodule imports hoisted
// to module level, and belongs-to/include/yang-version bookkeeping
// dropped. Returns the merged YANG text and the names of all inlined
// submodules.
func flattenModule(vendor, name string, sources map[string]*rawSource) (text string, included []string, err error) {
	root := sources[name]

	// prefixes maps prefix -> imported module, seeded from the parent,
	// to detect sibling-submodule prefix conflicts during hoisting.
	prefixes := make(map[string]string)
	var parentPrefix string
	for _, s := range root.stmt.SubStatements() {
		switch s.Keyword {
		case "import":
			if p := findSub(s, "prefix"); p != nil {
				prefixes[p.Argument] = s.Argument
			}
		case "prefix":
			parentPrefix = s.Argument
		}
	}

	var (
		imports []*yang.Statement // hoisted submodule imports, in encounter order
		bodies  []*yang.Statement // inlined submodule body statements
		visited = make(map[string]struct{})
	)

	var inline func(subName string) error
	inline = func(subName string) error {
		if _, done := visited[subName]; done {
			return nil
		}
		visited[subName] = struct{}{}
		sub, ok := sources[subName]
		if !ok {
			return &LoadError{
				Vendor: vendor, Module: name,
				Issue: fmt.Sprintf("includes %q, which is not present under the vendor's paths (or is skipped)", subName),
			}
		}
		if sub.kind != "submodule" {
			return &LoadError{
				Vendor: vendor, Module: name,
				Issue: fmt.Sprintf("includes %q, which is a module, not a submodule", subName),
			}
		}
		included = append(included, subName)

		for _, s := range sub.stmt.SubStatements() {
			switch s.Keyword {
			case "yang-version", "revision", "organization", "contact", "description", "reference":
				// The parent's own header and meta statements win; a
				// submodule's revision history and boilerplate are
				// not part of the merged module.
			case "belongs-to":
				if p := findSub(s, "prefix"); p != nil && parentPrefix != "" && p.Argument != parentPrefix {
					return &conflictError{
						module: name,
						issue:  fmt.Sprintf("submodule %q belongs-to prefix %q differs from the module prefix %q", subName, p.Argument, parentPrefix),
					}
				}
			case "include":
				if err := inline(s.Argument); err != nil {
					return err
				}
			case "import":
				p := findSub(s, "prefix")
				if p == nil {
					return &LoadError{
						Vendor: vendor, Module: name,
						Issue: fmt.Sprintf("submodule %q has an import without a prefix", subName),
					}
				}
				if prev, seen := prefixes[p.Argument]; seen {
					if prev != s.Argument {
						return &conflictError{
							module: name,
							issue:  fmt.Sprintf("submodule %q imports %q as prefix %q, already bound to %q", subName, s.Argument, p.Argument, prev),
						}
					}
					// Same module, same prefix — already hoisted.
					continue
				}
				prefixes[p.Argument] = s.Argument
				imports = append(imports, s)
			default:
				bodies = append(bodies, s)
			}
		}
		return nil
	}

	// Resolve the include graph first so the hoisted-import list is
	// complete before serialization begins.
	parentStmts := root.stmt.SubStatements()
	for _, s := range parentStmts {
		if s.Keyword == "include" {
			if err := inline(s.Argument); err != nil {
				return "", nil, err
			}
		}
	}

	// RFC 7950's grammar puts linkage statements (import/include)
	// before the module body, so hoisted imports are spliced in right
	// after the parent's own linkage section.
	linkageEnd := 0
	for i, s := range parentStmts {
		switch s.Keyword {
		case "yang-version", "namespace", "prefix", "import", "include":
			linkageEnd = i + 1
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s {\n", root.stmt.Keyword, quoteYANG(root.stmt.Argument))
	for _, s := range parentStmts[:linkageEnd] {
		if s.Keyword != "include" {
			writeStatement(&b, s, "  ")
		}
	}
	for _, s := range imports {
		writeStatement(&b, s, "  ")
	}
	for _, s := range parentStmts[linkageEnd:] {
		if s.Keyword != "include" {
			writeStatement(&b, s, "  ")
		}
	}
	for _, s := range bodies {
		writeStatement(&b, s, "  ")
	}
	b.WriteString("}\n")
	return b.String(), included, nil
}

// writeStatement serializes one statement tree as YANG text.
// goyang's Statement.Write mis-quotes multi-line arguments (the first
// line is emitted unescaped), so the flattener owns serialization:
// every argument is one double-quoted string with `\`, `"`, newline,
// and tab escaped per RFC 7950 §6.1.3.
func writeStatement(b *strings.Builder, s *yang.Statement, indent string) {
	b.WriteString(indent)
	b.WriteString(s.Keyword)
	if s.HasArgument {
		b.WriteByte(' ')
		b.WriteString(quoteYANG(s.Argument))
	}
	subs := s.SubStatements()
	if len(subs) == 0 {
		b.WriteString(";\n")
		return
	}
	b.WriteString(" {\n")
	for _, sub := range subs {
		writeStatement(b, sub, indent+"  ")
	}
	b.WriteString(indent)
	b.WriteString("}\n")
}

// quoteYANG renders s as a YANG double-quoted string.
func quoteYANG(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// parseOriginal feeds one raw source's on-disk text to goyang
// unchanged.
func parseOriginal(ms *yang.Modules, vendor, name string, src *rawSource) error {
	data, err := os.ReadFile(src.file.Path)
	if err != nil {
		return &LoadError{Vendor: vendor, Module: name, Issue: "read source", wrapped: err}
	}
	if err := ms.Parse(string(data), name+".yang"); err != nil {
		return &LoadError{Vendor: vendor, Module: name, Issue: "parse", wrapped: err}
	}
	return nil
}

// includeClosure collects the names of every submodule name includes,
// transitively, for the closure graph when a module is not flattened.
func includeClosure(name string, sources map[string]*rawSource) []string {
	var out []string
	visited := map[string]struct{}{}
	var visit func(n string)
	visit = func(n string) {
		src, ok := sources[n]
		if !ok {
			return
		}
		for _, s := range src.stmt.SubStatements() {
			if s.Keyword != "include" {
				continue
			}
			if _, done := visited[s.Argument]; done {
				continue
			}
			visited[s.Argument] = struct{}{}
			out = append(out, s.Argument)
			visit(s.Argument)
		}
	}
	visit(name)
	return out
}

// findSub returns the first sub-statement of s with the given
// keyword, or nil.
func findSub(s *yang.Statement, keyword string) *yang.Statement {
	for _, sub := range s.SubStatements() {
		if sub.Keyword == keyword {
			return sub
		}
	}
	return nil
}
