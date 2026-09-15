package diag_test

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/smi/internal/catalog"
)

// modulePath is the root module's path. Every package the registry names
// lives in it; the nested bench and differential modules are walked too,
// because MustRaise is exported and a caller may sit anywhere.
const modulePath = "go.aledante.io/FlowSeer"

const (
	diagDir = "src/protocol/smi/internal/diag"
	smiDir  = "src/protocol/smi"
)

// raiser is one function whose panic a call site inherits: MustRaise
// itself, or a variadic forwarder that passes a runtime code and a spread
// argument list through to it. A forwarder hides its arguments from any
// AST pass, so the scan resolves the forwarder's own callers instead,
// which is why the registry has to name each one and say where in its
// parameter list the code and the variadic arguments sit.
//
// The registry lives here rather than beside the forwarders because a
// production-side registry would have a test as its only caller. A
// forwarder nobody registers is not silently skipped: its body spreads a
// variadic into a raiser, which the scan reports as unreadable.
type raiser struct {
	dir      string // repo-relative directory of the declaring package
	pkgPath  string // import path, empty for an unexported method
	name     string
	method   bool
	codeArg  int    // index into the call's arguments, receiver excluded
	variadic int    // index of the first variadic argument
	proof    string // name of the forwarding test in dir; empty for diag.MustRaise, which forwards nothing
}

const (
	raiseProof     = "TestRaiseForwardsCodeAndArgsUnchanged"
	mustRaiseProof = "TestMustRaiseForwardsCodeAndArgsUnchanged"
)

var raisers = []raiser{
	{dir: diagDir, pkgPath: modulePath + "/" + diagDir, name: "MustRaise", codeArg: 1, variadic: 2},
	{dir: smiDir, pkgPath: modulePath + "/" + smiDir, name: "MustRaise", codeArg: 1, variadic: 2, proof: mustRaiseProof},
	{dir: "src/protocol/smi/internal/lex", name: "raise", method: true, codeArg: 1, variadic: 2, proof: raiseProof},
	{dir: "src/protocol/smi/internal/parse", name: "raise", method: true, codeArg: 1, variadic: 2, proof: raiseProof},
	{dir: "src/protocol/smi/internal/frame", name: "raise", method: true, codeArg: 1, variadic: 2, proof: raiseProof},
	{dir: smiDir, name: "raise", method: true, codeArg: 2, variadic: 3, proof: raiseProof},
}

// source is one parsed file the scan reasons about.
type source struct {
	path string // repo-relative, for findings
	dir  string // repo-relative directory, how a package is identified
	fset *token.FileSet
	file *ast.File
}

// TestMustRaiseCallsResolveToTheirCatalogRow is the proof MustRaise's doc
// comment cites for its two panics: every first-party call that reaches
// it names a cataloged code and passes the number of arguments that
// code's row declares, so neither panic is reachable from committed
// source.
//
// It scans the whole repository, not this module alone. MustRaise is
// exported, so a caller can sit in the nested bench and differential
// modules that a root `go test ./...` never builds; parsing by path is
// what lets one test in the root module see them.
func TestMustRaiseCallsResolveToTheirCatalogRow(t *testing.T) {
	root := repoRoot(t)
	sources := parseTree(t, root)
	tests := parseForwarderTests(t, root)
	codes := generatedCodes(t, root)
	arity := catalogArity()

	for _, finding := range scanCalls(sources, codes, arity) {
		t.Error(finding)
	}
	for _, finding := range scanCoverage(sources, tests) {
		t.Error(finding)
	}
}

// TestScanCallsReportsUnresolvableAndMismatchedCalls drives the scan over
// synthetic sources, so each way a call can fail is provable without a
// real violation in the tree. The first case is the arity mismatch the
// gate exists for; the rest are the ways the scan fails closed rather
// than skipping what it cannot read.
func TestScanCallsReportsUnresolvableAndMismatchedCalls(t *testing.T) {
	codes := map[string]string{
		"ErrCodeHyphenSeparator": "smi/hyphen-separator",
		"ErrCodeLimitExceeded":   "smi/limit-exceeded",
		"ErrCodeRetired":         "smi/retired",
	}
	arity := map[string]int{
		"smi/hyphen-separator": 1,
		"smi/limit-exceeded":   2,
	}

	// Every fixture in package parse reaches the codes through this import,
	// so the scan resolves the qualifier rather than the spelling.
	const parseHead = "package parse\n\nimport \"go.aledante.io/FlowSeer/src/protocol/smi/internal/diag\"\n"

	tests := []struct {
		name string
		dir  string
		src  string
		want string
	}{
		{
			name: "arity mismatch through a forwarder",
			dir:  "src/protocol/smi/internal/parse",
			src: parseHead + `
func (p *parser) grade() {
	p.raise(0, diag.ErrCodeHyphenSeparator, diag.ArgInt(1), diag.ArgInt(2), diag.ArgInt(3))
}`,
			want: `takes 1 argument`,
		},
		{
			name: "generated code the catalog no longer carries",
			dir:  "src/protocol/smi/internal/parse",
			src: parseHead + `
func (p *parser) grade() {
	p.raise(0, diag.ErrCodeRetired)
}`,
			want: `is not a cataloged diagnostic code`,
		},
		{
			name: "identifier the generated table does not declare",
			dir:  "src/protocol/smi/internal/parse",
			src: parseHead + `
func (p *parser) grade() {
	p.raise(0, diag.ErrCodeNoSuchThing)
}`,
			want: `is not a generated diagnostic code`,
		},
		{
			name: "code is not a constant identifier",
			dir:  "src/protocol/smi/internal/parse",
			src: parseHead + `
func (p *parser) grade(code errs.Code) {
	p.raise(0, code)
}`,
			want: `code argument is not a generated ErrCode constant`,
		},
		{
			name: "unregistered forwarder spreads its arguments",
			dir:  "src/protocol/smi/internal/parse",
			src: parseHead + `
func (p *parser) note(code errs.Code, args ...diag.Arg) {
	p.raise(0, code, args...)
}`,
			want: `spreads a variadic`,
		},
		{
			name: "registered forwarder may spread; its package's runtime test proves the pass-through",
			dir:  "src/protocol/smi/internal/parse",
			src: parseHead + `
func (p *parser) raise(offset int32, code errs.Code, args ...diag.Arg) {
	diag.MustRaise(diag.Position{}, code, args...)
}`,
		},
		{
			name: "registered forwarder whose parameters sit elsewhere than the registry says",
			dir:  "src/protocol/smi/internal/parse",
			src: parseHead + `
func (p *parser) raise(code errs.Code, offset int32, args ...diag.Arg) {
	diag.MustRaise(diag.Position{}, code, args...)
}`,
			want: `registered with its code at parameter 1`,
		},
		{
			name: "arity mismatch through a forwarder reached by a selector chain",
			dir:  "src/protocol/smi/internal/parse",
			src: parseHead + `
func (r *reader) grade() {
	r.p.raise(0, diag.ErrCodeHyphenSeparator, diag.ArgInt(1), diag.ArgInt(2))
}`,
			want: `takes 1 argument`,
		},
		{
			name: "method value the scan cannot follow",
			dir:  "src/protocol/smi/internal/parse",
			src: parseHead + `
func (p *parser) grade() {
	f := p.raise
	f(0, diag.ErrCodeHyphenSeparator)
}`,
			want: `used as a value`,
		},
		{
			name: "method expression passes the receiver as an argument",
			dir:  "src/protocol/smi/internal/parse",
			src: parseHead + `
func grade(p *parser) {
	(*parser).raise(p, 0, diag.ErrCodeHyphenSeparator, diag.ArgInt(1))
}`,
			want: `code argument is not a generated ErrCode constant`,
		},
		{
			name: "parenthesized callee",
			dir:  "src/protocol/smi/internal/parse",
			src: parseHead + `
func (p *parser) grade() {
	(diag.MustRaise)(diag.Position{}, diag.ErrCodeLimitExceeded, diag.ArgInt(1))
}`,
			want: `takes 2 arguments`,
		},
		{
			name: "cross-package call through the import path",
			dir:  "src/services/mibs",
			src: `package mibs

import "go.aledante.io/FlowSeer/src/protocol/smi"

func report() {
	smi.MustRaise(smi.Position{}, smi.ErrCodeLimitExceeded, smi.ArgInt(1))
}`,
			want: `takes 2 arguments`,
		},
		{
			name: "function value the scan cannot follow",
			dir:  "src/services/mibs",
			src: `package mibs

import "go.aledante.io/FlowSeer/src/protocol/smi"

func report() {
	f := smi.MustRaise
	_ = f
}`,
			want: `used as a value`,
		},
		{
			name: "dot import reaches MustRaise unqualified",
			dir:  "src/services/mibs",
			src: `package mibs

import . "go.aledante.io/FlowSeer/src/protocol/smi"

func report() {
	MustRaise(Position{}, ErrCodeLimitExceeded, ArgInt(1))
}`,
			want: `takes 2 arguments`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			findings := scanCalls([]source{parseSource(t, tc.dir, tc.src)}, codes, arity)

			if tc.want == "" {
				if len(findings) != 0 {
					t.Fatalf("got findings %v, want none", findings)
				}

				return
			}

			if len(findings) != 1 {
				t.Fatalf("got %d findings %v, want exactly one mentioning %q", len(findings), findings, tc.want)
			}
			if !strings.Contains(findings[0], tc.want) {
				t.Errorf("finding = %q, want it to mention %q", findings[0], tc.want)
			}
		})
	}
}

// TestScanCoverageReportsWhatItNeverSaw pins the ways a clean run can mean
// nothing: a walk that opened no file, one that opened files but never
// reached a package holding a forwarder, whose callers are then
// unscanned, and one whose forwarders are all declared but whose spread
// exemption rests on a forwarding test nobody wrote.
func TestScanCoverageReportsWhatItNeverSaw(t *testing.T) {
	declared := declaredRaiserFixtures(t)
	proofs := forwardingTestFixtures(t)

	tests := []struct {
		name    string
		sources []source
		tests   []source
		want    []string // each must appear in some finding; empty means no finding at all
		wantOne bool     // exactly one finding, so a missing test is not buried under others
	}{
		{
			name: "no sources",
			want: []string{"checked nothing"},
		},
		{
			name:    "one forwarder declared, five raisers unseen",
			sources: declared[3:4],
			tests:   proofs,
			want:    []string{diagDir, smiDir, "internal/lex", "internal/frame"},
		},
		{
			name:    "every raiser declared and every forwarding test present",
			sources: declared,
			tests:   proofs,
		},
		{
			name:    "every raiser declared, one forwarding test missing",
			sources: declared,
			tests:   slices.Concat(proofs[:2], proofs[3:]),
			want:    []string{"internal/parse's " + raiseProof, "spread exemption for raise is unproven"},
			wantOne: true,
		},
		{
			name:    "forwarding test named in the wrong package",
			sources: declared,
			tests:   slices.Concat(proofs[:2], proofs[3:], []source{parseSource(t, "src/protocol/smi/internal/lex", "package lex\nfunc "+raiseProof+"(t *testing.T) {}\n")}),
			want:    []string{"internal/parse's " + raiseProof},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			findings := scanCoverage(tc.sources, tc.tests)

			if len(tc.want) == 0 {
				if len(findings) != 0 {
					t.Fatalf("got findings %v, want none", findings)
				}

				return
			}
			if len(findings) == 0 {
				t.Fatalf("got no findings, want ones mentioning %q", tc.want)
			}
			for _, want := range tc.want {
				if !strings.Contains(strings.Join(findings, "\n"), want) {
					t.Errorf("findings %v do not mention %q", findings, want)
				}
			}
			if tc.wantOne && len(findings) != 1 {
				t.Errorf("got %d findings %v, want exactly one", len(findings), findings)
			}
		})
	}
}

// declaredRaiserFixtures returns one fixture per registered raiser, in
// registry order, declaring it with the parameters the registry records.
func declaredRaiserFixtures(t *testing.T) []source {
	t.Helper()

	sources := make([]source, 0, len(raisers))
	for _, r := range raisers {
		pkg := r.dir[strings.LastIndex(r.dir, "/")+1:]
		recv := ""
		if r.method {
			recv = "(x *receiver) "
		}
		params := make([]string, r.variadic+1)
		for i := range params {
			params[i] = "p" + strconv.Itoa(i) + " int"
		}
		params[r.codeArg] = "code errs.Code"
		params[r.variadic] = "args ...diag.Arg"

		sources = append(sources, parseSource(t, r.dir, "package "+pkg+"\nfunc "+recv+r.name+"("+strings.Join(params, ", ")+") {}\n"))
	}

	return sources
}

// forwardingTestFixtures returns one fixture per registered forwarder, in
// registry order with diag.MustRaise's slot left out, declaring the
// forwarding test the registry names for it.
func forwardingTestFixtures(t *testing.T) []source {
	t.Helper()

	var sources []source
	for _, r := range raisers {
		if r.proof == "" {
			continue
		}
		pkg := r.dir[strings.LastIndex(r.dir, "/")+1:]

		sources = append(sources, parseSource(t, r.dir, "package "+pkg+"\nfunc "+r.proof+"(t *testing.T) {}\n"))
	}

	return sources
}

// scanCalls reports every call reaching a raiser that it cannot resolve
// to a catalog row, or that disagrees with the row's arity.
func scanCalls(sources []source, codes map[string]string, arity map[string]int) []string {
	var findings []string

	for _, s := range sources {
		qualifiers := importQualifiers(s.file)

		// Each declaration is walked under the function that encloses it,
		// because a raiser's own body forwards the variadic it was handed
		// and no AST pass can read that. The scan resolves such a
		// forwarder's callers instead, so a spread anywhere else is an
		// unregistered forwarder.
		//
		// A raiser named anywhere but as a call's callee is a value the
		// scan cannot follow to its eventual call, so it is a finding. The
		// walk visits a call before its callee and a selector before its
		// field name, which is what lets both be recognized when reached.
		for _, decl := range s.file.Decls {
			enclosing, _ := decl.(*ast.FuncDecl)
			callees := make(map[ast.Expr]bool)
			skip := make(map[*ast.Ident]bool)
			if enclosing != nil {
				skip[enclosing.Name] = true
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CallExpr:
					fun := ast.Unparen(n.Fun)
					callees[fun] = true
					if r, ok := calledRaiser(fun, s.dir, qualifiers); ok {
						findings = append(findings, callFinding(s, n, r, enclosing, qualifiers, codes, arity)...)
					}
				case *ast.SelectorExpr:
					skip[n.Sel] = true
					findings = append(findings, valueFinding(s, n, callees, qualifiers)...)
				case *ast.Ident:
					if !skip[n] {
						findings = append(findings, valueFinding(s, n, callees, qualifiers)...)
					}
				}

				return true
			})
		}
	}

	return findings
}

// valueFinding reports expr when it names a raiser without calling it.
func valueFinding(s source, expr ast.Expr, callees map[ast.Expr]bool, qualifiers map[string]string) []string {
	if callees[expr] {
		return nil
	}
	r, ok := calledRaiser(expr, s.dir, qualifiers)
	if !ok {
		return nil
	}

	return []string{s.path + ":" + strconv.Itoa(s.fset.Position(expr.Pos()).Line) + ": " +
		r.name + " is used as a value; the scan cannot follow it to the call it eventually makes"}
}

// callFinding reports how one call to a raiser fails to resolve, if it
// does.
func callFinding(
	s source,
	call *ast.CallExpr,
	r raiser,
	enclosing *ast.FuncDecl,
	qualifiers map[string]string,
	codes map[string]string,
	arity map[string]int,
) []string {
	where := s.path + ":" + strconv.Itoa(s.fset.Position(call.Pos()).Line)
	finding := func(text string) []string { return []string{where + ": " + text} }

	if call.Ellipsis != token.NoPos {
		return spreadFinding(finding, r, enclosing, s.dir)
	}
	if len(call.Args) <= r.codeArg {
		return finding("call to " + r.name + " has no code argument")
	}

	ident, ok := codeIdent(call.Args[r.codeArg], s.dir, qualifiers)
	if !ok {
		return finding("code argument is not a generated ErrCode constant, so the arity cannot be checked")
	}
	code, ok := codes[ident]
	if !ok {
		return finding(ident + " is not a generated diagnostic code")
	}
	want, ok := arity[code]
	if !ok {
		return finding(strconv.Quote(code) + " is not a cataloged diagnostic code")
	}

	if got := len(call.Args) - r.variadic; got != want {
		return finding(strconv.Quote(code) + " takes " + plural(want, "argument") + ", given " + strconv.Itoa(got))
	}

	return nil
}

// spreadFinding reports a call that spreads a variadic into r from
// anywhere but the body of a registered forwarder. A spread inside a
// registered forwarder is exempt whatever the body does with code and
// args before the spread, because this scan does not decide that the
// forwarder passes them through: it cannot, and two syntactic
// approximations of the property have already been wrong. Each
// forwarder's own package proves it by execution instead, calling the
// forwarder once per catalog row with that row's code and its arity's
// worth of arguments and asserting the diagnostic that comes out is the
// one MustRaise builds from them: the test each registry entry names in
// its proof field, which scanCoverage requires to be declared in that
// package. What this scan can still decide about a forwarder is whether
// the registry's parameter indices match its declaration, which
// forwardedParams checks, since a caller resolved at the wrong index is
// checked against the wrong row and no runtime test would notice.
func spreadFinding(finding func(string) []string, r raiser, enclosing *ast.FuncDecl, dir string) []string {
	if enclosing == nil {
		return finding("package-level code spreads a variadic into " + r.name +
			", so no scan can read the argument count; register it as a forwarder or pass a fixed list")
	}
	own := declaredRaiser(enclosing, dir)
	if own < 0 {
		return finding(enclosing.Name.Name + " spreads a variadic into " + r.name +
			", so no scan can read the argument count; register it as a forwarder or pass a fixed list")
	}
	if !forwardedParams(enclosing, raisers[own]) {
		return finding(enclosing.Name.Name + " is registered with its code at parameter " +
			strconv.Itoa(raisers[own].codeArg) + " and its variadic at parameter " +
			strconv.Itoa(raisers[own].variadic) + ", but its declaration has no errs.Code and ... there")
	}

	return nil
}

// forwardedParams reports whether fn declares an errs.Code parameter and
// a `...` parameter at the indices the registry records for r. This is
// the one place the registry's indices meet the real declaration.
func forwardedParams(fn *ast.FuncDecl, r raiser) bool {
	var types []ast.Expr
	for _, field := range fn.Type.Params.List {
		for range max(len(field.Names), 1) {
			types = append(types, field.Type)
		}
	}
	if r.codeArg >= len(types) || r.variadic >= len(types) {
		return false
	}
	if sel, ok := types[r.codeArg].(*ast.SelectorExpr); !ok || !isIdent(sel.X, "errs") || sel.Sel.Name != "Code" {
		return false
	}
	_, ok := types[r.variadic].(*ast.Ellipsis)

	return ok
}

func isIdent(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)

	return ok && ident.Name == name
}

// scanCoverage reports what the walk never opened. Resolution failing
// closed covers calls the scan read; this covers the ones it did not,
// since a file nobody parses raises no finding of any kind.
//
// It also requires each forwarder's forwarding test to be declared in
// tests, the parsed test files of the forwarders' packages, because the
// spread exemption in spreadFinding rests on that test running: delete
// or rename it and nothing else would notice. The check is by name
// only. A test whose body no longer proves anything passes it, and
// stays a review catch.
func scanCoverage(sources, tests []source) []string {
	if len(sources) == 0 {
		return []string{"scanned no source files; the gate checked nothing"}
	}

	seen := make([]bool, len(raisers))
	for _, s := range sources {
		for _, decl := range s.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if i := declaredRaiser(fn, s.dir); i >= 0 {
				seen[i] = true
			}
		}
	}

	proven := make([]bool, len(raisers))
	for _, s := range tests {
		for _, decl := range s.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			for i, r := range raisers {
				if r.proof != "" && r.dir == s.dir && r.proof == fn.Name.Name {
					proven[i] = true
				}
			}
		}
	}

	var findings []string
	for i, r := range raisers {
		if !seen[i] {
			findings = append(findings, "never saw "+r.dir+"."+r.name+
				" declared, so its callers went unscanned")
		}
		if r.proof != "" && !proven[i] {
			findings = append(findings, "never saw "+r.dir+"'s "+r.proof+
				", so the spread exemption for "+r.name+" is unproven")
		}
	}

	return findings
}

// calledRaiser reports which raiser expr names, if any. A bare identifier
// is the package's own function, or a dot-imported one. A selector is
// either a qualified package function, when its left side is an import
// qualifier, or a method. A method is matched by name within its own
// package's directory whatever its receiver expression looks like,
// because the receiver's type is not in reach of an AST pass and a
// selector chain such as r.p.raise is as much a call as p.raise;
// over-matching there makes the scan stricter, never laxer.
func calledRaiser(expr ast.Expr, dir string, qualifiers map[string]string) (raiser, bool) {
	switch fun := expr.(type) {
	case *ast.Ident:
		for _, r := range raisers {
			if r.method || r.name != fun.Name {
				continue
			}
			if r.dir == dir || qualifiers["."] == r.pkgPath {
				return r, true
			}
		}
	case *ast.SelectorExpr:
		var path string
		imported := false
		if x, ok := fun.X.(*ast.Ident); ok {
			path, imported = qualifiers[x.Name]
		}

		for _, r := range raisers {
			if r.name != fun.Sel.Name {
				continue
			}
			if imported && !r.method && r.pkgPath == path {
				return r, true
			}
			if !imported && r.method && r.dir == dir {
				return r, true
			}
		}
	}

	return raiser{}, false
}

// declaredRaiser returns the index of the raiser fn declares, or -1.
func declaredRaiser(fn *ast.FuncDecl, dir string) int {
	for i, r := range raisers {
		if r.dir != dir || r.name != fn.Name.Name {
			continue
		}
		if r.method == (fn.Recv != nil) {
			return i
		}
	}

	return -1
}

// codeIdent returns the name of the ErrCode constant expr names. A
// selector resolves only through a qualifier for package diag or package
// smi, so another package's same-named constant is not mistaken for one;
// a bare identifier resolves inside those two packages and behind a dot
// import of either.
func codeIdent(expr ast.Expr, dir string, qualifiers map[string]string) (string, bool) {
	switch e := expr.(type) {
	case *ast.Ident:
		switch {
		case dir == diagDir, dir == smiDir,
			qualifiers["."] == modulePath+"/"+diagDir, qualifiers["."] == modulePath+"/"+smiDir:
			return e.Name, strings.HasPrefix(e.Name, "ErrCode")
		}

		return "", false
	case *ast.SelectorExpr:
		x, ok := e.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		switch qualifiers[x.Name] {
		case modulePath + "/" + diagDir, modulePath + "/" + smiDir:
			return e.Sel.Name, strings.HasPrefix(e.Sel.Name, "ErrCode")
		}
	}

	return "", false
}

// importQualifiers maps each local name a file can reach a package
// through onto that package's import path, so the scan follows the path
// rather than trusting how the package name happens to be spelled.
func importQualifiers(file *ast.File) map[string]string {
	qualifiers := make(map[string]string, len(file.Imports))
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}

		name := path[strings.LastIndex(path, "/")+1:]
		if spec.Name != nil {
			name = spec.Name.Name
		}

		qualifiers[name] = path
	}

	return qualifiers
}

// generatedCodes maps every generated ErrCode identifier onto its code
// string. Package diag declares the codes; package smi re-exports each as
// an alias, which is the one hop a call site in package smi resolves
// through.
func generatedCodes(t *testing.T, root string) map[string]string {
	t.Helper()

	codes := make(map[string]string)
	for name, spec := range assignments(t, filepath.Join(root, diagDir, "zz_generated_codes.go")) {
		call, ok := spec.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			continue
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatalf("unreadable code literal for %s: %s", name, lit.Value)
		}

		codes[name] = value
	}

	if len(codes) == 0 {
		t.Fatal("read no codes from the generated table; the scan would resolve nothing")
	}

	for name, spec := range assignments(t, filepath.Join(root, smiDir, "zz_generated_codes.go")) {
		sel, ok := spec.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		code, ok := codes[sel.Sel.Name]
		if !ok {
			t.Errorf("package smi re-exports %s, which package diag does not declare", name)

			continue
		}
		codes[name] = code
	}

	return codes
}

// assignments returns the single-name, single-value var specs of a
// generated code file, keyed by the name.
func assignments(t *testing.T, path string) map[string]ast.Expr {
	t.Helper()

	fset := token.NewFileSet()
	file, err := goparser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	out := make(map[string]ast.Expr)
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, s := range gen.Specs {
			spec, ok := s.(*ast.ValueSpec)
			if !ok || len(spec.Names) != 1 || len(spec.Values) != 1 {
				continue
			}

			out[spec.Names[0].Name] = spec.Values[0]
		}
	}

	return out
}

// catalogArity reads the live table rather than parsing its source, so
// the scan inherits catalog.Validate's guarantee that a row's arity
// agrees with the verb count of its format string.
func catalogArity() map[string]int {
	rows := catalog.Entries()
	arity := make(map[string]int, len(rows))
	for _, row := range rows {
		arity[row.Code] = row.Arity
	}

	return arity
}

// parseTree parses every non-test Go file in the repository outside a
// directory named testdata. testdata holds fixtures standing in for
// foreign code, and a test that calls MustRaise wrongly is asserting the
// panic.
func parseTree(t *testing.T, root string) []source {
	t.Helper()

	var sources []source
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if name == "testdata" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}

			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}

		s, err := parseFile(root, path)
		if err != nil {
			return err
		}
		sources = append(sources, s)

		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	return sources
}

// parseForwarderTests parses the test files of every package that
// declares a forwarder, which is where scanCoverage looks for each
// forwarding test.
func parseForwarderTests(t *testing.T, root string) []source {
	t.Helper()

	var sources []source
	dirs := make(map[string]bool)
	for _, r := range raisers {
		if r.proof == "" || dirs[r.dir] {
			continue
		}
		dirs[r.dir] = true

		paths, err := filepath.Glob(filepath.Join(root, r.dir, "*_test.go"))
		if err != nil {
			t.Fatalf("listing tests in %s: %v", r.dir, err)
		}
		for _, path := range paths {
			s, err := parseFile(root, path)
			if err != nil {
				t.Fatalf("parsing %s: %v", path, err)
			}
			sources = append(sources, s)
		}
	}

	return sources
}

// parseFile parses one Go file into a source keyed by its path relative
// to root.
func parseFile(root, path string) (source, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return source{}, err
	}

	fset := token.NewFileSet()
	file, err := goparser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return source{}, err
	}

	return source{
		path: filepath.ToSlash(rel),
		dir:  filepath.ToSlash(filepath.Dir(rel)),
		fset: fset,
		file: file,
	}, nil
}

// parseSource builds one source from a fixture string, standing in for a
// file in dir.
func parseSource(t *testing.T, dir, src string) source {
	t.Helper()

	fset := token.NewFileSet()
	file, err := goparser.ParseFile(fset, "fixture.go", src, 0)
	if err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}

	return source{path: dir + "/fixture.go", dir: dir, fset: fset, file: file}
}

func plural(n int, noun string) string {
	if n == 1 {
		return strconv.Itoa(n) + " " + noun
	}

	return strconv.Itoa(n) + " " + noun + "s"
}

func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting working directory: %v", err)
	}

	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(data), "module "+modulePath+"\n") {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found above the diag package")
		}

		dir = parent
	}
}
