package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/openconfig/goyang/pkg/yang"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// SourceFile is one .yang file discovered under a vendor's paths: the
// module (or submodule) name derived from its filename, the on-disk
// path, and the SHA-256 of its bytes.
type SourceFile struct {
	Name   string // module or submodule name (filename, revision suffix stripped)
	Path   string
	SHA256 string
}

// LoadedModule is one module resolved for generation: its goyang
// parse node, its resolved [yang.Entry] tree, its newest revision
// date, and the closure hash covering every source file its output
// depends on (KTD7).
type LoadedModule struct {
	Vendor      string
	Name        string
	Package     string // derived or overridden Go package name
	Revision    string // newest revision date, "" if the module declares none
	Module      *yang.Module
	Entry       *yang.Entry
	SourceSHA   string
	ClosureSHA  string
	ClosureSize int // number of source files in the closure, self included
}

// VendorSet is one vendor's fully-loaded tree: emitted modules in
// deterministic name order, plus the skip bookkeeping -verify reports.
type VendorSet struct {
	Vendor  string
	Modules []*LoadedModule
	Skipped []Skip // the config's skip entries, all verified to exist on disk
}

// LoadError reports a structured load failure, mirroring
// [ConfigError]'s shape.
type LoadError struct {
	Vendor  string
	Module  string
	Issue   string
	wrapped error
}

// Error returns a one-line diagnostic. Module and the wrapped cause
// are interpolated when set.
func (e *LoadError) Error() string {
	msg := fmt.Sprintf("load vendor %s: %s", e.Vendor, e.Issue)
	if e.Module != "" {
		msg = fmt.Sprintf("load vendor %s: module %q: %s", e.Vendor, e.Module, e.Issue)
	}
	if e.wrapped != nil {
		msg += ": " + e.wrapped.Error()
	}
	return msg
}

// Unwrap returns the wrapped cause.
func (e *LoadError) Unwrap() error { return e.wrapped }

// LoadVendors loads every vendor in the config, in config order.
func LoadVendors(cfg *Config) ([]*VendorSet, error) {
	out := make([]*VendorSet, 0, len(cfg.Vendors))
	for i := range cfg.Vendors {
		vs, err := LoadVendor(&cfg.Vendors[i])
		if err != nil {
			return nil, err
		}
		out = append(out, vs)
	}
	return out, nil
}

// LoadVendor discovers, parses, and resolves one vendor's tree:
// every .yang file under the vendor's paths is parsed, the skip-list
// subtracted, goyang resolves the schema (groupings, augments,
// deviations), and each surviving module gets its Entry tree and
// KTD7 closure hash.
func LoadVendor(v *Vendor) (*VendorSet, error) {
	files, err := discoverSources(v)
	if err != nil {
		return nil, err
	}

	skip := make(map[string]Skip, len(v.Skip))
	var skipped []Skip
	for _, s := range v.Skip {
		if s.Module != "" {
			if _, ok := files[s.Module]; !ok {
				return nil, &LoadError{
					Vendor: v.Name, Module: s.Module,
					Issue: "skip entry names a module not present under the vendor's paths — remove the stale skip",
				}
			}
			skip[s.Module] = s
			skipped = append(skipped, s)
			continue
		}
		matchedAny := false
		for _, name := range sortedKeys(files) {
			if ok, _ := path.Match(s.Pattern, name); !ok {
				continue
			}
			matchedAny = true
			if _, dup := skip[name]; dup {
				continue
			}
			resolved := Skip{Module: name, Reason: s.Reason}
			skip[name] = resolved
			skipped = append(skipped, resolved)
		}
		if !matchedAny {
			return nil, &LoadError{
				Vendor: v.Name,
				Issue:  fmt.Sprintf("skip pattern %q matches no module under the vendor's paths — remove the stale skip", s.Pattern),
			}
		}
	}

	// Raw-parse everything first, then hand goyang each module with
	// its submodules inlined (flatten.go). No search path is
	// registered: every source is parsed explicitly, so an import of
	// a skipped or missing module fails loudly instead of goyang
	// quietly re-reading a file the skip-list excluded.
	raw, err := rawParse(v.Name, files, skip)
	if err != nil {
		return nil, err
	}

	// Flatten first, parse second: a submodule whose parent was
	// flattened must NOT be parsed standalone — goyang only links the
	// include graph of modules, so an orphaned submodule reaches
	// Process's ToEntry pass with nil include links and panics.
	// Standalone submodule parses happen only for the include
	// closures of modules the flattener had to leave alone.
	type parsedModule struct {
		name string
		text string // "" means parse the original file text
	}
	var (
		toParse  []parsedModule
		needSub  = make(map[string]struct{})
		includes = make(map[string][]string)
	)
	for _, name := range sortedKeys(raw) {
		if raw[name].kind != "module" {
			continue
		}
		text, inlined, err := flattenModule(v.Name, name, raw)
		var conflict *conflictError
		switch {
		case errors.As(err, &conflict):
			// Inlining would change meaning — keep the original text
			// and goyang's own include handling for this module.
			includes[name] = includeClosure(name, raw)
			for _, sub := range includes[name] {
				needSub[sub] = struct{}{}
			}
			toParse = append(toParse, parsedModule{name: name})
			continue
		case err != nil:
			return nil, err
		}
		includes[name] = inlined
		toParse = append(toParse, parsedModule{name: name, text: text})
	}

	ms := yang.NewModules()
	for _, name := range sortedKeys(raw) {
		if raw[name].kind != "submodule" {
			continue
		}
		if _, needed := needSub[name]; !needed {
			continue
		}
		if err := parseOriginal(ms, v.Name, name, raw[name]); err != nil {
			return nil, err
		}
	}
	for _, pm := range toParse {
		if pm.text == "" {
			if err := parseOriginal(ms, v.Name, pm.name, raw[pm.name]); err != nil {
				return nil, err
			}
			continue
		}
		if err := ms.Parse(pm.text, pm.name+".yang"); err != nil {
			return nil, &LoadError{Vendor: v.Name, Module: pm.name, Issue: "parse (submodules inlined)", wrapped: err}
		}
	}

	if procErrs := ms.Process(); len(procErrs) > 0 {
		return nil, &LoadError{
			Vendor: v.Name,
			Issue:  fmt.Sprintf("schema resolution reported %d error(s), first: %v", len(procErrs), firstError(procErrs)),
			wrapped: errs.New().
				Attr("error_count", len(procErrs)).
				Msgf("%v", procErrs),
		}
	}

	graph := buildClosureGraph(ms, files, skip, includes)

	overrides := make(map[string]string, len(v.PackageOverrides))
	for _, o := range v.PackageOverrides {
		if _, ok := files[o.Module]; !ok {
			return nil, &LoadError{
				Vendor: v.Name, Module: o.Module,
				Issue: "package override names a module not present under the vendor's paths",
			}
		}
		overrides[o.Module] = o.Package
	}

	set := &VendorSet{Vendor: v.Name, Skipped: skipped}
	seenPkg := make(map[string]string) // package name -> module that claimed it
	for _, name := range sortedKeys(raw) {
		// Submodules are inlined into their parents and never emitted
		// standalone.
		if raw[name].kind != "module" {
			continue
		}
		mod, ok := ms.Modules[name]
		if !ok {
			return nil, &LoadError{
				Vendor: v.Name, Module: name,
				Issue: "module parsed but not registered under its own name",
			}
		}
		entry := yang.ToEntry(mod)
		if entry == nil {
			return nil, &LoadError{Vendor: v.Name, Module: name, Issue: "resolving the entry tree returned nil"}
		}

		pkg := overrides[name]
		if pkg == "" {
			pkg = derivePackageName(name)
		}
		if prev, dup := seenPkg[pkg]; dup {
			return nil, &LoadError{
				Vendor: v.Name, Module: name,
				Issue: fmt.Sprintf("package name %q collides with module %q — add a package_overrides entry", pkg, prev),
			}
		}
		seenPkg[pkg] = name

		closureSHA, closureSize := graph.closureHash(name)
		set.Modules = append(set.Modules, &LoadedModule{
			Vendor:      v.Name,
			Name:        name,
			Package:     pkg,
			Revision:    newestRevision(mod),
			Module:      mod,
			Entry:       entry,
			SourceSHA:   files[name].SHA256,
			ClosureSHA:  closureSHA,
			ClosureSize: closureSize,
		})
	}
	return set, nil
}

// discoverSources walks the vendor's paths collecting every .yang
// file, hashing its bytes, and deriving its module name from the
// filename (any "@<revision>" suffix stripped) — the vendored-tree
// naming convention; a mismatch against the module statement inside
// surfaces during load. Path order is precedence: when the same
// module name appears under two listed roots, the earlier root wins —
// a vendor's own tree, listed first, shadows any shared supplement
// directory. A duplicate within one root is an error: a single tree
// must have one source of truth per module.
func discoverSources(v *Vendor) (map[string]SourceFile, error) {
	files := make(map[string]SourceFile)
	fileRoot := make(map[string]string)
	for _, root := range v.Paths {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".yang") {
				return nil
			}
			name := strings.TrimSuffix(d.Name(), ".yang")
			if at := strings.IndexByte(name, '@'); at >= 0 {
				name = name[:at]
			}
			if prev, dup := files[name]; dup {
				if fileRoot[name] == root {
					return &LoadError{
						Vendor: v.Name, Module: name,
						Issue: fmt.Sprintf("module appears twice under %s: %s and %s", root, prev.Path, path),
					}
				}
				// Earlier root shadows this copy.
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			files[name] = SourceFile{Name: name, Path: path, SHA256: hex.EncodeToString(sum[:])}
			fileRoot[name] = root
			return nil
		})
		if err != nil {
			var le *LoadError
			if errors.As(err, &le) {
				return nil, err
			}
			return nil, &LoadError{Vendor: v.Name, Issue: fmt.Sprintf("walk %s", root), wrapped: err}
		}
	}
	if len(files) == 0 {
		return nil, &LoadError{Vendor: v.Name, Issue: "no .yang files found under the vendor's paths"}
	}
	return files, nil
}

// closureGraph holds the dependency edges KTD7's closure hash walks:
// forward import/include edges, and reverse augment/deviation edges
// (a module that augments A changes A's output without appearing in
// A's imports).
type closureGraph struct {
	files   map[string]SourceFile
	forward map[string][]string // module -> imports + includes
	reverse map[string][]string // module -> modules augmenting/deviating it
}

// buildClosureGraph derives the graph from the parsed module set and
// the flattener's include map (flattened modules carry no include
// statements of their own, and submodule imports/augments/deviations
// were hoisted into the parent). Only names present in the vendor's
// own file set become edges; goyang built-ins and cross-vendor names
// (none exist today) are ignored.
func buildClosureGraph(ms *yang.Modules, files map[string]SourceFile, skip map[string]Skip, includes map[string][]string) *closureGraph {
	g := &closureGraph{
		files:   files,
		forward: make(map[string][]string),
		reverse: make(map[string][]string),
	}
	addEdge := func(edges map[string][]string, from, to string) {
		if from == to {
			return
		}
		if _, known := files[to]; !known {
			return
		}
		if !slices.Contains(edges[from], to) {
			edges[from] = append(edges[from], to)
		}
	}

	for name, mod := range ms.Modules {
		if strings.Contains(name, "@") {
			continue
		}
		if _, skipped := skip[name]; skipped {
			continue
		}
		for _, imp := range mod.Import {
			addEdge(g.forward, name, imp.Name)
		}
		for _, sub := range includes[name] {
			addEdge(g.forward, name, sub)
		}
		for _, aug := range mod.Augment {
			if target := targetModule(mod, aug.Name); target != "" {
				addEdge(g.reverse, target, name)
			}
		}
		for _, dev := range mod.Deviation {
			if target := targetModule(mod, dev.Name); target != "" {
				addEdge(g.reverse, target, name)
			}
		}
	}
	return g
}

// targetModule resolves the module owning the first segment of an
// augment/deviation target path, via the augmenting module's import
// prefixes. An unprefixed or self-prefixed path targets the module
// itself and yields "".
func targetModule(mod *yang.Module, path string) string {
	seg := strings.TrimPrefix(path, "/")
	if i := strings.IndexByte(seg, '/'); i >= 0 {
		seg = seg[:i]
	}
	prefix, _, ok := strings.Cut(seg, ":")
	if !ok {
		return ""
	}
	if mod.Prefix != nil && prefix == mod.Prefix.Name {
		return ""
	}
	for _, imp := range mod.Import {
		if imp.Prefix != nil && imp.Prefix.Name == prefix {
			return imp.Name
		}
	}
	return ""
}

// closureHash walks the module's dependency closure — transitive
// forward edges, plus the reverse-edge contributors of every reached
// node and their forward closures — and hashes the sorted
// (name, source hash) pairs. Any source change anywhere in the
// closure changes the hash, which is exactly the KTD7 invalidation
// rule.
func (g *closureGraph) closureHash(name string) (sum string, size int) {
	visited := make(map[string]struct{})
	var visit func(n string)
	visit = func(n string) {
		if _, done := visited[n]; done {
			return
		}
		if _, known := g.files[n]; !known {
			return
		}
		visited[n] = struct{}{}
		for _, dep := range g.forward[n] {
			visit(dep)
		}
		for _, contributor := range g.reverse[n] {
			visit(contributor)
		}
	}
	visit(name)

	members := make([]string, 0, len(visited))
	for n := range visited {
		members = append(members, n)
	}
	sort.Strings(members)

	h := sha256.New()
	for _, n := range members {
		fmt.Fprintf(h, "%s:%s\n", n, g.files[n].SHA256)
	}
	return hex.EncodeToString(h.Sum(nil)), len(members)
}

// derivePackageName mangles a module name into a deterministic Go
// package name: lowercase, non-alphanumerics dropped, a leading "z"
// prepended if the result would start with a digit.
func derivePackageName(module string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(module) {
		if ('a' <= r && r <= 'z') || ('0' <= r && r <= '9') {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if s == "" || s[0] >= '0' && s[0] <= '9' {
		s = "z" + s
	}
	return s
}

// newestRevision returns the lexically greatest revision date, which
// for ISO dates is the newest. Modules without a revision yield "".
func newestRevision(mod *yang.Module) string {
	newest := ""
	for _, r := range mod.Revision {
		if r.Name > newest {
			newest = r.Name
		}
	}
	return newest
}

// sortedKeys returns m's keys in sorted order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// firstError returns the lexically first error string from errsIn so
// the headline diagnostic is deterministic.
func firstError(errsIn []error) error {
	if len(errsIn) == 0 {
		return nil
	}
	first := errsIn[0]
	for _, e := range errsIn[1:] {
		if e.Error() < first.Error() {
			first = e
		}
	}
	return first
}
