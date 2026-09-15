package smi

import (
	"slices"
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/smi/internal/frame"
	"go.aledante.io/FlowSeer/src/protocol/smi/internal/lex"
	"go.aledante.io/FlowSeer/src/protocol/smi/internal/parse"
)

// modBuild is one module part-way through resolution: what the parser
// produced, the imports read off the frames, and the [Module] being
// filled in from them.
type modBuild struct {
	name  string
	path  string
	src   *parse.Result
	pm    *parse.Module
	decls map[string]parse.Ref
	out   *Module
}

// resolveState tracks a memoized resolution so a definition that depends
// on itself is caught rather than recursed into.
type resolveState uint8

const (
	stateOpen resolveState = iota
	stateBusy
	stateDone
)

// oidResult is one memoized OID resolution. Fail names what did not
// resolve, which is what the declaration's own diagnostic quotes.
type oidResult struct {
	oid   OID
	fail  string
	ok    bool
	state resolveState
}

// resolver turns parsed files into one resolved module set.
//
// It is a single-pass-per-question design rather than a fixed-point
// loop: every lookup is memoized and recursive, so a forward reference
// and a cross-module reference cost the same thing and neither needs the
// modules to have been loaded in any particular order.
type resolver struct {
	mods   []*modBuild
	byName map[string]*modBuild

	diags []Diagnostic
	noted map[string]bool

	oids  map[string]*oidResult
	types map[string]*Type
	busy  map[string]bool
}

// resolve is the whole pass: gather the modules, order them, resolve
// every OID and type, build the tree, and merge the result in canonical
// order.
func resolve(sources []source) *ModuleSet {
	r := &resolver{
		byName: make(map[string]*modBuild),
		noted:  make(map[string]bool),
		oids:   make(map[string]*oidResult),
		types:  make(map[string]*Type),
		busy:   make(map[string]bool),
	}

	r.gather(sources)
	r.linkImports(sources)

	set := &ModuleSet{
		byName: make(map[string]*Module, len(r.mods)),
		byOID:  make(map[string]*Node),
		lines:  make(map[string]*LineTable, len(sources)),
	}

	for _, b := range r.mods {
		r.build(b)
		set.modules = append(set.modules, b.out)
		set.byName[b.name] = b.out
		set.types = append(set.types, b.out.Types...)
	}

	set.order = r.dependencyOrder()
	r.placeNodes(set)
	r.classify(set)
	r.resolveIndexes(set)

	for _, s := range sources {
		set.lines[s.path] = s.parsed.Lines
		r.diags = append(r.diags, s.parsed.Diagnostics...)
	}
	set.diags = canonicalDiagnostics(r.diags)

	return set
}

// gather collects every module the sources define, in canonical order:
// by module name, and by file path where two files define the same name.
// The first file in that order keeps the name, because a name has to
// mean one thing and the choice has to be one a reader can predict.
func (r *resolver) gather(sources []source) {
	type candidate struct {
		path string
		src  *parse.Result
		pm   *parse.Module
	}

	var all []candidate
	for _, s := range sources {
		for i := range s.parsed.Modules {
			all = append(all, candidate{path: s.path, src: s.parsed, pm: &s.parsed.Modules[i]})
		}
	}

	slices.SortStableFunc(all, func(a, b candidate) int {
		if c := strings.Compare(a.pm.Name, b.pm.Name); c != 0 {
			return c
		}

		return strings.Compare(a.path, b.path)
	})

	for _, c := range all {
		if kept, dup := r.byName[c.pm.Name]; dup {
			r.raise(c.path, c.pm.Span.Start, ErrCodeDuplicateModule,
				ArgString(c.pm.Name), ArgString(kept.path))

			continue
		}

		b := &modBuild{
			name:  c.pm.Name,
			path:  c.path,
			src:   c.src,
			pm:    c.pm,
			decls: make(map[string]parse.Ref, len(c.pm.Decls)),
			out: &Module{
				Name:       c.pm.Name,
				File:       c.path,
				Dialect:    Dialect(c.pm.Dialect),
				nodeByName: make(map[string]*Node),
				typeByName: make(map[string]*Type),
			},
		}

		for _, ref := range c.pm.Decls {
			name := c.pm.Decl(ref).Name
			if name == "" {
				continue
			}
			if _, seen := b.decls[name]; !seen {
				b.decls[name] = ref
			}
		}

		r.mods = append(r.mods, b)
		r.byName[c.pm.Name] = b
	}
}

// linkImports attaches each module's IMPORTS lists, reported against the
// modules that were actually loaded.
func (r *resolver) linkImports(sources []source) {
	byPath := make(map[string]*source, len(sources))
	for i := range sources {
		byPath[sources[i].path] = &sources[i]
	}

	for _, b := range r.mods {
		s := byPath[b.path]
		if s == nil {
			continue
		}

		fm, ok := framedModule(s, b.name)
		if !ok {
			continue
		}

		for _, raw := range imports(fm, s.frames.Source) {
			if _, loaded := r.byName[raw.module]; !loaded {
				r.raise(b.path, raw.offset, ErrCodeModuleNotFound, ArgString(raw.module))
			}
			b.out.Imports = append(b.out.Imports, Import{Module: raw.module, Symbols: raw.symbols})
		}
	}
}

// framedModule finds the framed module a build came from, so the IMPORTS
// frames can be read for it.
func framedModule(s *source, name string) (frame.Module, bool) {
	for _, fm := range s.frames.Modules {
		if fm.Name == name {
			return fm, true
		}
	}

	return frame.Module{}, false
}

// dependencyOrder returns module names with every import before its
// importer, breaking each cycle at the back edge the walk finds.
//
// A cycle is diagnosed and dropped rather than failing the load: both
// modules in a cycle still define everything they define, and the only
// thing an acyclic graph was needed for is the order a consumer that
// processes one module at a time walks in. Symbol lookup still follows a
// broken edge, because the author's IMPORTS list is still the best
// statement of where a symbol comes from.
func (r *resolver) dependencyOrder() []string {
	const (
		white = 0
		gray  = 1
		black = 2
	)

	color := make(map[string]int, len(r.mods))
	out := make([]string, 0, len(r.mods))

	var visit func(b *modBuild)
	visit = func(b *modBuild) {
		color[b.name] = gray

		for i := range b.out.Imports {
			imp := &b.out.Imports[i]
			target, ok := r.byName[imp.Module]
			if !ok || target == b {
				continue
			}

			switch color[target.name] {
			case white:
				visit(target)
			case gray:
				imp.BackEdge = true
				r.raise(b.path, b.pm.Span.Start, ErrCodeImportCycle,
					ArgString(imp.Module), ArgString(b.name))
			}
		}

		color[b.name] = black
		out = append(out, b.name)
	}

	for _, b := range r.mods {
		if color[b.name] == white {
			visit(b)
		}
	}

	return out
}

// lookupDecl finds the module that declares name, as the module b sees
// it: its own declarations first, then the modules its IMPORTS name,
// then every loaded module.
//
// The last step is the lenient one and it is deliberate. Vendor MIBs use
// symbols they never import often enough that refusing them would cost
// more definitions than it would report, so the symbol resolves and the
// missing import is graded instead.
func (r *resolver) lookupDecl(b *modBuild, name string) (*modBuild, bool) {
	if _, ok := b.decls[name]; ok {
		return b, true
	}

	for _, imp := range b.out.Imports {
		if !slices.Contains(imp.Symbols, name) {
			continue
		}
		target, ok := r.byName[imp.Module]
		if !ok {
			continue
		}
		if _, ok := target.decls[name]; ok {
			return target, true
		}
	}

	for _, m := range r.mods {
		if m == b {
			continue
		}
		if _, ok := m.decls[name]; ok {
			r.noteMissingImport(b, name, m.name)

			return m, true
		}
	}

	return nil, false
}

// noteMissingImport grades a symbol used without an import, once per
// module and symbol however many declarations use it.
func (r *resolver) noteMissingImport(b *modBuild, name, from string) {
	key := b.name + "\x00" + name
	if r.noted[key] {
		return
	}
	r.noted[key] = true

	r.raise(b.path, b.pm.Span.Start, ErrCodeMissingImport, ArgString(name), ArgString(from))
}

// wellKnownRoots are the ASN.1 registration roots. They are not declared
// by any MIB and no module imports them, so they resolve silently rather
// than being graded as a missing import the way a type would be.
var wellKnownRoots = map[string]uint32{
	"ccitt":            0,
	"iso":              1,
	"joint-iso-ccitt":  2,
	"joint-iso-itu-t":  2,
	"itu-t":            0,
	"itu-r":            0,
	"itu":              0,
	"joint-iso-ccitt2": 2,
}

// oidOf resolves the OID a declaration is assigned, following the parent
// it names through as many modules as that takes.
//
// The result is memoized, so a subtree whose root is named by a hundred
// declarations is walked once, and a definition that reaches itself
// leaves the whole chain unresolved rather than recursing.
func (r *resolver) oidOf(b *modBuild, name string) (OID, string, bool) {
	key := b.name + "\x00" + name

	if got, seen := r.oids[key]; seen {
		if got.state == stateBusy {
			return OID{}, name, false
		}

		return got.oid, got.fail, got.ok
	}

	res := &oidResult{state: stateBusy}
	r.oids[key] = res

	oid, fail, ok := r.computeOID(b, name)
	*res = oidResult{oid: oid, fail: fail, ok: ok, state: stateDone}

	return oid, fail, ok
}

func (r *resolver) computeOID(b *modBuild, name string) (OID, string, bool) {
	ref, ok := b.decls[name]
	if !ok {
		return OID{}, name, false
	}

	if ref.Kind == parse.DeclTrapType {
		return r.trapOID(b, ref)
	}

	span, ok := assignmentOf(b.pm, ref)
	if !ok || span.End <= span.Start {
		return OID{}, "its ::= value", false
	}

	arcs, ok := parseArcs(b.src, span)
	if !ok || len(arcs) == 0 {
		return OID{}, "its ::= value", false
	}

	base, fail, ok := r.arcBase(b, arcs[0])
	if base.Len() == 0 {
		return OID{}, fail, false
	}

	subs := slices.Clone(base.Subs())
	for _, a := range arcs[1:] {
		if !a.hasNum {
			return OID{}, a.name, false
		}
		subs = append(subs, a.num)
	}

	if len(subs) == 0 || len(subs) > MaxOIDLength {
		return OID{}, "its ::= value", false
	}

	// A declaration the parser withheld still wrote the value it was
	// assigned, and everything registered under it is reached through
	// that value. So the OID comes back and the declaration does not
	// count as resolved: a dependent takes its place in the tree and is
	// still marked, and still told which declaration it is waiting on.
	if ref.Kind == parse.DeclBad {
		return OID{subs: subs}, name, false
	}

	return OID{subs: subs}, fail, ok
}

// arcBase resolves the first arc of an assignment, which is the only one
// that may name something instead of counting.
//
// An OID and an unresolved verdict travel together here rather than
// exclusively: a chain running through a declaration the parser withheld
// still knows where it is registered, and losing the arc would take the
// whole subtree out of the tree over one clause one arc up.
func (r *resolver) arcBase(b *modBuild, a arc) (OID, string, bool) {
	if a.name == "" {
		if !a.hasNum {
			return OID{}, "its ::= value", false
		}

		return OID{subs: []uint32{a.num}}, "", true
	}

	if root, ok := wellKnownRoots[a.name]; ok {
		return OID{subs: []uint32{root}}, "", true
	}

	target, ok := r.lookupDecl(b, a.name)
	if !ok {
		return OID{}, a.name, false
	}

	oid, fail, ok := r.oidOf(target, a.name)
	if oid.Len() == 0 {
		return OID{}, fail, false
	}

	return oid, fail, ok
}

// trapOID places an SMIv1 trap. The enterprise names the prefix and the
// parser already worked out the suffix, zero sub-identifier and all, so
// this concatenates rather than re-deriving RFC 3584 §2.1.2's rule.
func (r *resolver) trapOID(b *modBuild, ref parse.Ref) (OID, string, bool) {
	t := &b.pm.TrapTypes[ref.Index]
	if !t.Notification.Valid {
		return OID{}, "its trap number", false
	}

	enterprise := strings.Trim(b.src.Text(t.Notification.Enterprise), "{} \t\r\n")
	if enterprise == "" {
		return OID{}, "its ENTERPRISE clause", false
	}

	base, fail, ok := r.arcBase(b, arc{name: enterprise})
	if !ok {
		return OID{}, fail, false
	}
	if base.Len()+len(t.Notification.Suffix) > MaxOIDLength {
		return OID{}, "its notification OID", false
	}

	subs := slices.Clone(base.Subs())
	for _, s := range t.Notification.Suffix {
		if s < 0 || s > maxSubIdentifier {
			return OID{}, "its trap number", false
		}
		subs = append(subs, uint32(s))
	}

	return OID{subs: subs}, "", true
}

// maxSubIdentifier is the largest value one arc may take, which is what
// a trap number has to fit in to be placed in the tree at all.
const maxSubIdentifier = 4294967295

// arc is one element of an assignment's brace group: a descriptor, a
// number, or a descriptor with its number in parentheses.
type arc struct {
	name   string
	num    uint32
	hasNum bool
}

// parseArcs reads an assignment's brace group.
//
// Token gaps remove comments under the file's original lexer mode while
// preserving adjacent vendor spellings such as "802dot3".
func parseArcs(src *parse.Result, span parse.Span) ([]arc, bool) {
	tokens := src.Tokens(span)
	var significant strings.Builder
	for i, tok := range tokens {
		if i > 0 && tok.Offset > tokens[i-1].End() && tokens[i-1].Kind != lex.KindLeftParen && tok.Kind != lex.KindRightParen {
			significant.WriteByte(' ')
		}
		significant.WriteString(src.Text(parse.Span{Start: tok.Offset, End: tok.End()}))
	}
	text := significant.String()
	var out []arc

	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == '{' || c == '}' || c == ',' || c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++

			continue
		case !isArcByte(c):
			return nil, false
		}

		start := i
		for i < len(text) && isArcByte(text[i]) {
			i++
		}
		word := text[start:i]

		a := arcWord(word)

		// "name(3)" binds the descriptor to its number in one arc, and
		// ASN.1 lets white space sit between the two. The IEEE 802.1
		// modules write "iso (1) iso-identified-organization (3) …" and
		// reading the space as the end of the value would leave every
		// one of them anchored nowhere.
		if open := skipArcSpace(text, i); open < len(text) && text[open] == '(' {
			i = open
			j := i + 1
			for j < len(text) && text[j] >= '0' && text[j] <= '9' {
				j++
			}
			if j == i+1 || j >= len(text) || text[j] != ')' {
				return nil, false
			}
			n, err := strconv.ParseUint(text[i+1:j], 10, 32)
			if err != nil {
				return nil, false
			}
			a.num = uint32(n)
			a.hasNum = true
			i = j + 1
		}

		out = append(out, a)
	}

	return out, true
}

// arcWord reads one word of an assignment as either a sub-identifier or
// a label.
//
// A leading digit is not proof of a number. RFC 2578 §3.1 starts a
// descriptor with a lowercase letter, and the corpus writes labels that
// do not — IEEE8023-LAG-MIB anchors itself at
// "{ iso(1) member-body(2) us(840) 802dot3(10006) snmpmibs(300) 43 }" —
// where what the arc is worth is the number in parentheses and the label
// is only a label. So a word that does not read as a sub-identifier is
// one, rather than costing the whole value.
func arcWord(word string) arc {
	if n, err := strconv.ParseUint(word, 10, 32); err == nil {
		return arc{num: uint32(n), hasNum: true}
	}

	return arc{name: word}
}

// skipArcSpace returns the first index at or after i that is not white
// space.
func skipArcSpace(text string, i int) int {
	for i < len(text) && (text[i] == ' ' || text[i] == '\t' || text[i] == '\r' || text[i] == '\n') {
		i++
	}

	return i
}

func isArcByte(c byte) bool {
	return c == '-' || c == '_' ||
		(c >= '0' && c <= '9') ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z')
}

// canonicalDiagnostics returns one ordering of the diagnostics a load
// produced: by file, then by where in the file the condition was found,
// then by code. Nothing about how many workers ran or what order the
// caller named the files in survives it.
func canonicalDiagnostics(diags []Diagnostic) []Diagnostic {
	out := slices.Clone(diags)
	slices.SortStableFunc(out, func(a, b Diagnostic) int {
		if c := strings.Compare(a.Position().File, b.Position().File); c != 0 {
			return c
		}
		if c := a.Position().Offset - b.Position().Offset; c != 0 {
			return c
		}

		return strings.Compare(a.Code().String(), b.Code().String())
	})

	return out
}

// raise records a resolution diagnostic against a file and offset.
//
// It inherits [MustRaise]'s panic on an uncataloged code or an argument
// count the code's catalog row does not declare. code and args come from
// the resolver's own call sites, so the invariant is proven by the arity
// scan in internal/diag, not left to the caller.
func (r *resolver) raise(file string, offset int32, code errs.Code, args ...Arg) {
	r.diags = append(r.diags, MustRaise(Position{File: file, Offset: int(offset)}, code, args...))
}

// build turns one module's declarations into nodes and types, in the
// order the source writes them.
func (r *resolver) build(b *modBuild) {
	for _, ref := range b.pm.Decls {
		switch ref.Kind {
		case parse.DeclTextualConvention, parse.DeclTypeAssignment:
			r.addType(b, b.pm.Decl(ref).Name)
		case parse.DeclBad:
			r.buildBadNode(b, ref)
		default:
			r.buildNode(b, ref)
		}
	}

	r.fillIdentity(b)
}

// addType resolves one textual convention or type assignment into the
// module's type list.
func (r *resolver) addType(b *modBuild, name string) {
	if name == "" {
		return
	}
	if _, dup := b.out.typeByName[name]; dup {
		return
	}

	t := r.namedType(b, name)
	if t == nil {
		return
	}

	b.out.typeByName[name] = t
	b.out.Types = append(b.out.Types, t)
}

// fillIdentity copies the MODULE-IDENTITY clauses onto the module. A
// module without one keeps empty strings rather than borrowing another
// declaration's prose.
func (r *resolver) fillIdentity(b *modBuild) {
	if len(b.pm.ModuleIdentities) == 0 {
		return
	}

	mi := &b.pm.ModuleIdentities[0]
	b.out.Organization = b.src.StringValue(mi.Organization)
	b.out.ContactInfo = b.src.StringValue(mi.ContactInfo)
	b.out.Description = b.src.StringValue(mi.Description)
	b.out.LastUpdated = b.src.StringValue(mi.LastUpdated)

	if n, ok := b.out.Node(mi.Name); ok {
		b.out.Identity = n
	}
}

// buildNode resolves one declaration into a node.
//
// A declaration that loses its OID or its SYNTAX is kept and marked
// unresolved, and it carries exactly one diagnostic naming what it was
// waiting on. That is the whole of the no-cascade rule: a name nothing
// defines costs one diagnostic per declaration that wanted it, not one
// per place the name is written.
func (r *resolver) buildNode(b *modBuild, ref parse.Ref) {
	d := b.pm.Decl(ref)
	n := &Node{
		Name:    d.Name,
		Module:  b.name,
		Dialect: b.out.Dialect,
		Kind:    nodeKindOf(ref.Kind),
	}

	reported := false
	report := func(what string) {
		n.Unresolved = true
		if reported {
			return
		}
		reported = true
		r.raise(b.path, d.Span.Start, ErrCodeUnresolvedDeclaration,
			ArgString(d.Name), ArgString(what))
	}

	oid, fail, ok := r.oidOf(b, d.Name)
	n.OID = oid
	if !ok {
		report(fail)
	}

	// A name the declaration lost costs it resolved status without a
	// diagnostic of its own: the parser already reported the token it
	// could not read, at the offset it was written, which is more than
	// this pass could say about it.
	if d.NameLost {
		n.Unresolved = true
	}

	r.fillNode(b, ref, n, report)

	// A type that did not resolve leaves the object it types unrenderable
	// too. Most such types arrive with a name that failed to resolve and
	// report above has already run; one whose SYNTAX contradicts the
	// definition it refines carries its own diagnostic and only needs the
	// mark.
	if n.Type != nil && n.Type.Unresolved {
		n.Unresolved = true
	}

	addNode(b.out, n)
}

// buildBadNode resolves a declaration the parser withheld because a
// clause its macro requires never arrived.
//
// The node is marked unresolved and carries no type at all, which is the
// point of withholding it: a type defaulted in for a SYNTAX that never
// arrived renders exactly as if somebody had written it.
//
// What the node is not is absent. It keeps the OID it was assigned, the
// kind its macro gave it, and the clause values that did parse, because
// everything registered under it is reached through that OID and read
// through that kind. HUAWEI-BRAS-DPI-MIB is the shape: an SMIv2 module
// whose OBJECT-TYPEs wrote no DESCRIPTION, where a withheld table with
// no place in the tree takes its row and its columns down with it and
// leaves a reader unable to tell a declaration the parser refused from
// one the MIB never wrote.
//
// Nothing here raises a diagnostic. The parser already named every
// clause that was missing, at the declaration it was missing from.
func (r *resolver) buildBadNode(b *modBuild, ref parse.Ref) {
	bd := &b.pm.Bad[ref.Index]

	kind := NodeUnknown
	if bd.Intended != parse.DeclBad {
		kind = nodeKindOf(bd.Intended)
	}
	if isTableSyntax(bd.Syntax, b.src.Text(bd.Syntax.Span)) {
		kind = NodeTable
	}

	n := &Node{
		Name:       bd.Name,
		Module:     b.name,
		Dialect:    b.out.Dialect,
		Kind:       kind,
		Unresolved: true,
	}
	n.OID, _, _ = r.oidOf(b, bd.Name)

	for _, c := range bd.Clauses {
		switch c.Clause {
		case parse.ClauseMaxAccess, parse.ClauseAccess:
			n.Access = parseAccess(b.src.Text(c.Span))
		case parse.ClauseStatus:
			n.Status = parseStatus(b.src.Text(c.Span))
		case parse.ClauseDescription:
			n.Description = b.src.StringValue(c.Span)
		case parse.ClauseReference:
			n.Reference = b.src.StringValue(c.Span)
		case parse.ClauseUnits:
			n.Units = b.src.StringValue(c.Span)
		case parse.ClauseAugments:
			n.Augments = b.src.Text(c.Span)
		}
	}

	addNode(b.out, n)
}

// fillNode copies the clauses a node carries out of its declaration.
func (r *resolver) fillNode(b *modBuild, ref parse.Ref, n *Node, report func(string)) {
	switch ref.Kind {
	case parse.DeclObjectType:
		o := &b.pm.ObjectTypes[ref.Index]
		n.Access = parseAccess(b.src.Text(o.MaxAccess) + b.src.Text(o.Access))
		n.Status = parseStatus(b.src.Text(o.Status))
		n.Description = b.src.StringValue(o.Description)
		n.Reference = b.src.StringValue(o.Reference)
		n.Units = b.src.StringValue(o.Units)
		n.Augments = b.src.Text(o.Augments)
		n.Default = resolveDefault(b, o.DefaultValue)

		for _, part := range o.Index.Parts {
			n.Index = append(n.Index, IndexPart{Name: b.src.Text(part.Name), Implied: part.Implied})
		}

		t, fail := r.syntaxType(b, o.SyntaxType)
		n.Type = t
		if fail != "" {
			report(fail)
		}
		if isTableSyntax(o.SyntaxType, b.src.Text(o.Syntax)) {
			n.Kind = NodeTable
		}

	case parse.DeclObjectIdentity:
		o := &b.pm.ObjectIdentities[ref.Index]
		n.Status = parseStatus(b.src.Text(o.Status))
		n.Description = b.src.StringValue(o.Description)
		n.Reference = b.src.StringValue(o.Reference)

	case parse.DeclModuleIdentity:
		o := &b.pm.ModuleIdentities[ref.Index]
		n.Description = b.src.StringValue(o.Description)

	case parse.DeclNotificationType:
		o := &b.pm.NotificationTypes[ref.Index]
		n.Status = parseStatus(b.src.Text(o.Status))
		n.Description = b.src.StringValue(o.Description)
		n.Reference = b.src.StringValue(o.Reference)

	case parse.DeclTrapType:
		o := &b.pm.TrapTypes[ref.Index]
		n.Description = b.src.StringValue(o.Description)
		n.Reference = b.src.StringValue(o.Reference)

	case parse.DeclObjectGroup:
		o := &b.pm.ObjectGroups[ref.Index]
		n.Status = parseStatus(b.src.Text(o.Status))
		n.Description = b.src.StringValue(o.Description)
		n.Reference = b.src.StringValue(o.Reference)

	case parse.DeclNotificationGroup:
		o := &b.pm.NotificationGroups[ref.Index]
		n.Status = parseStatus(b.src.Text(o.Status))
		n.Description = b.src.StringValue(o.Description)
		n.Reference = b.src.StringValue(o.Reference)

	case parse.DeclModuleCompliance:
		o := &b.pm.ModuleCompliances[ref.Index]
		n.Status = parseStatus(b.src.Text(o.Status))
		n.Description = b.src.StringValue(o.Description)
		n.Reference = b.src.StringValue(o.Reference)

	case parse.DeclAgentCapabilities:
		o := &b.pm.AgentCapabilities[ref.Index]
		n.Status = parseStatus(b.src.Text(o.Status))
		n.Description = b.src.StringValue(o.Description)
		n.Reference = b.src.StringValue(o.Reference)
	}
}

// resolveDefault turns a parsed DEFVAL into the value the model
// carries. A quoted string and a radix literal land in one shape,
// because the parser has already decoded both to the octets the object
// would take and the spelling stays in the source.
func resolveDefault(b *modBuild, v parse.Value) Default {
	switch v.Kind {
	case parse.ValueInteger:
		return Default{Kind: DefaultInteger, Number: v.Number}

	case parse.ValueLabel:
		return Default{Kind: DefaultLabel, Name: b.src.Text(v.Name)}

	case parse.ValueString, parse.ValueOctets:
		return Default{Kind: DefaultOctets, Octets: v.Octets}

	case parse.ValueOID:
		return oidDefault(v.Subs)

	case parse.ValueBits:
		bits := make([]string, 0, len(v.Bits))
		for _, name := range v.Bits {
			bits = append(bits, b.src.Text(name))
		}

		return Default{Kind: DefaultBits, Bits: bits}

	default:
		return Default{}
	}
}

// oidDefault places a sub-identifier list in an OID, or reports no
// default when an element is no sub-identifier.
//
// Reading nothing is safe here in a way it would not be elsewhere: the
// list form is graded wherever it is parsed, so the clause already
// carries a diagnostic and a caller that finds no default has been told
// why. Rounding an out-of-range element into a uint32 would instead hand
// back an OID nobody wrote.
func oidDefault(subs []int64) Default {
	out := make([]uint32, 0, len(subs))
	for _, s := range subs {
		if s < 0 || s > maxSubIdentifier {
			return Default{}
		}

		out = append(out, uint32(s))
	}
	if len(out) == 0 || len(out) > MaxOIDLength {
		return Default{}
	}

	return Default{Kind: DefaultOID, OID: OID{subs: out}}
}

// addNode appends a node in source order and makes it findable by name.
func addNode(m *Module, n *Node) {
	m.Nodes = append(m.Nodes, n)
	if n.Name == "" {
		return
	}
	if _, dup := m.nodeByName[n.Name]; !dup {
		m.nodeByName[n.Name] = n
	}
}

// nodeKindOf is the kind a declaration gets before the tree is walked.
// An OBJECT-TYPE starts as a scalar and is demoted to a row or a column
// by [resolver.classify] once its parent's kind is known.
func nodeKindOf(k parse.DeclKind) NodeKind {
	switch k {
	case parse.DeclObjectType:
		return NodeScalar
	case parse.DeclNotificationType, parse.DeclTrapType:
		return NodeNotification
	case parse.DeclObjectGroup, parse.DeclNotificationGroup:
		return NodeGroup
	case parse.DeclModuleCompliance:
		return NodeCompliance
	case parse.DeclAgentCapabilities:
		return NodeCapabilities
	default:
		return NodeNode
	}
}

// isTableSyntax reports whether a SYNTAX clause declares a conceptual
// table. "SEQUENCE OF Foo" is a table and "SEQUENCE { … }" is the row
// type that goes with it, and the two share a base type, so the source
// spelling is what tells them apart.
func isTableSyntax(t parse.Type, text string) bool {
	if t.Base != parse.BaseSequence {
		return false
	}

	fields := strings.Fields(text)

	return len(fields) >= 2 && strings.EqualFold(fields[1], "OF")
}

// namedType resolves a textual convention or type assignment the module
// declares. A chain of conventions that closes on itself resolves to an
// unresolved type rather than recursing.
func (r *resolver) namedType(b *modBuild, name string) *Type {
	key := b.name + "\x00" + name

	if t, done := r.types[key]; done {
		return t
	}
	if r.busy[key] {
		return &Type{Name: name, Module: b.name, Unresolved: true}
	}

	ref, ok := b.decls[name]
	if !ok {
		return nil
	}

	var (
		t        *Type
		pt       parse.Type
		nameLost bool
	)

	switch ref.Kind {
	case parse.DeclTextualConvention:
		tc := &b.pm.TextualConventions[ref.Index]
		pt = tc.SyntaxType
		nameLost = tc.NameLost
		t = &Type{
			Name:        name,
			Module:      b.name,
			DisplayHint: b.src.StringValue(tc.DisplayHint),
			Description: b.src.StringValue(tc.Description),
			Reference:   b.src.StringValue(tc.Reference),
			Status:      parseStatus(b.src.Text(tc.Status)),
		}
	case parse.DeclTypeAssignment:
		ta := &b.pm.TypeAssignments[ref.Index]
		pt = ta.SyntaxType
		nameLost = ta.NameLost
		t = &Type{Name: name, Module: b.name}
	default:
		return nil
	}

	r.busy[key] = true
	r.applySyntax(b, t, pt)
	delete(r.busy, key)

	// A member name the parser dropped or cut short leaves the list
	// either short or holding a fragment, and either way nothing may
	// render this type as the definition its author wrote.
	if nameLost {
		t.Unresolved = true
	}

	r.types[key] = t

	return t
}

// syntaxType resolves an object's SYNTAX clause and returns the name
// that did not resolve, or the empty string when it did.
//
// A clause that only names a type shares the resolved type rather than
// copying it, so every object typed DisplayString points at one Type. A
// clause that adds its own constraints or named numbers gets a type of
// its own, keeping the name it refined so a renderer can still see which
// convention it came from.
func (r *resolver) syntaxType(b *modBuild, pt parse.Type) (*Type, string) {
	t := &Type{Module: b.name}
	fail := r.applySyntax(b, t, pt)

	if t.Name != "" && len(pt.Members) == 0 && len(pt.Ranges) == 0 && len(pt.Sizes) == 0 {
		if shared, ok := r.sharedType(b, t.Name); ok {
			return shared, fail
		}
	}

	return t, fail
}

// sharedType returns the resolved definition of a named type, so an
// object that refines nothing points at the same Type every other such
// object does.
func (r *resolver) sharedType(b *modBuild, name string) (*Type, bool) {
	target, ok := r.lookupDecl(b, name)
	if !ok {
		return nil, false
	}

	t := r.namedType(target, name)
	if t == nil || t.Unresolved {
		return nil, false
	}

	return t, true
}

// applySyntax reads one parsed type onto t, following a named type
// through to the base it stands on. It returns the name that did not
// resolve, or the empty string.
func (r *resolver) applySyntax(b *modBuild, t *Type, pt parse.Type) string {
	if len(pt.Members) > 0 {
		t.Members = make([]Member, 0, len(pt.Members))
		for _, m := range pt.Members {
			t.Members = append(t.Members, Member{Name: b.src.Text(m.Name), Number: m.Number})
		}
	}
	if len(pt.Ranges) > 0 {
		t.Ranges = make([]Range, 0, len(pt.Ranges))
		for _, rg := range pt.Ranges {
			t.Ranges = append(t.Ranges, Range{Min: rg.Min, Max: rg.Max})
		}
	}
	if len(pt.Sizes) > 0 {
		t.Sizes = make([]Range, 0, len(pt.Sizes))
		for _, sz := range pt.Sizes {
			t.Sizes = append(t.Sizes, Range{Min: sz.Min, Max: sz.Max})
		}
	}

	if pt.Base != parse.BaseNamed {
		t.Base = mapBase(pt.Base)

		return ""
	}

	name := b.src.Text(pt.Name)
	if name == "" {
		t.Unresolved = true

		return "its SYNTAX clause"
	}

	if t.Name == "" {
		t.Name = name
	}
	t.Parent = name

	target, ok := r.lookupDecl(b, name)
	if !ok {
		t.Unresolved = true

		return name
	}

	parent := r.namedType(target, name)
	if parent == nil || parent.Unresolved {
		t.Unresolved = true

		return name
	}

	t.Base = parent.Base
	if len(t.Members) == 0 {
		t.Members = parent.Members
	} else if len(parent.Members) > 0 {
		r.gradeRefinement(b, t, parent, pt.Members)
	}
	if len(t.Ranges) == 0 {
		t.Ranges = parent.Ranges
	}
	if len(t.Sizes) == 0 {
		t.Sizes = parent.Sizes
	}
	if t.DisplayHint == "" {
		t.DisplayHint = parent.DisplayHint
	}

	return ""
}

// gradeRefinement reads the named numbers a declaration wrote against
// the ones the type it names already carries.
//
// RFC 2579 §3.5 lets a SYNTAX clause restrict the enumeration of the
// textual convention it names, and RFC 2578 §9 makes that a narrowing of
// the set of values rather than a redefinition: the names and the
// numbers stay the convention's. A narrowing is what the author asked
// for and is kept silently.
//
// A member the convention does not carry under that number is a
// different claim about the same wire value — RUCKUS-ZD-SYSTEM-MIB
// writes TruthValue { false(0), true(1) } where RFC 2579 §2 numbers them
// 2 and 1 — and there is no reading that satisfies both. The members
// stay as the source wrote them, because that is what a reader has to
// see to know which two definitions disagree, and the type is left
// unresolved so nothing renders a mapping the MIB contradicts.
func (r *resolver) gradeRefinement(b *modBuild, t, parent *Type, members []parse.Member) {
	numbers := make(map[string]int64, len(parent.Members))
	for _, m := range parent.Members {
		numbers[m.Name] = m.Number
	}

	for _, m := range members {
		name := b.src.Text(m.Name)
		if n, declared := numbers[name]; declared && n == m.Number {
			continue
		}

		t.Unresolved = true
		r.raise(b.path, m.Span.Start, ErrCodeEnumerationRefinementConflict,
			ArgString(name), ArgInt(int(m.Number)), ArgString(t.Parent))
	}
}

// mapBase turns the parser's base type into the model's. The two sets
// are separate because the parser's [parse.BaseNamed] means "not
// classified yet", which is a statement about one declaration rather
// than about a resolved type.
func mapBase(b parse.BaseType) BaseType {
	switch b {
	case parse.BaseInteger:
		return BaseInteger
	case parse.BaseInteger32:
		return BaseInteger32
	case parse.BaseUnsigned32:
		return BaseUnsigned32
	case parse.BaseGauge32:
		return BaseGauge32
	case parse.BaseCounter32:
		return BaseCounter32
	case parse.BaseCounter64:
		return BaseCounter64
	case parse.BaseTimeTicks:
		return BaseTimeTicks
	case parse.BaseOctetString:
		return BaseOctetString
	case parse.BaseObjectIdentifier:
		return BaseObjectIdentifier
	case parse.BaseBits:
		return BaseBits
	case parse.BaseIPAddress:
		return BaseIPAddress
	case parse.BaseOpaque:
		return BaseOpaque
	case parse.BaseNull:
		return BaseNull
	case parse.BaseSequence:
		return BaseSequence
	default:
		return BaseUnknown
	}
}

func parseAccess(s string) Access {
	switch strings.TrimSpace(s) {
	case "not-accessible":
		return AccessNotAccessible
	case "accessible-for-notify":
		return AccessAccessibleForNotify
	case "read-only":
		return AccessReadOnly
	case "read-write":
		return AccessReadWrite
	case "read-create":
		return AccessReadCreate
	case "write-only":
		return AccessWriteOnly
	default:
		return AccessUnknown
	}
}

func parseStatus(s string) Status {
	switch strings.TrimSpace(s) {
	case "current":
		return StatusCurrent
	case "mandatory":
		return StatusMandatory
	case "optional":
		return StatusOptional
	case "deprecated":
		return StatusDeprecated
	case "obsolete":
		return StatusObsolete
	default:
		return StatusUnknown
	}
}

// assignmentOf returns the "::=" value a declaration was given.
//
// A withheld declaration is read out of the clauses that did parse,
// which is what lets everything registered beneath its OID still be
// placed. A withheld TRAP-TYPE is the exception: the macro assigns a
// trap number rather than an OID, and only the dialect pass turns one
// into a notification OID — for a declaration that kept its ENTERPRISE
// clause, which a withheld one by definition did not.
func assignmentOf(m *parse.Module, ref parse.Ref) (parse.Span, bool) {
	switch ref.Kind {
	case parse.DeclBad:
		return badAssignment(&m.Bad[ref.Index])
	case parse.DeclObjectType:
		return m.ObjectTypes[ref.Index].Assignment, true
	case parse.DeclObjectIdentity:
		return m.ObjectIdentities[ref.Index].Assignment, true
	case parse.DeclModuleIdentity:
		return m.ModuleIdentities[ref.Index].Assignment, true
	case parse.DeclNotificationType:
		return m.NotificationTypes[ref.Index].Assignment, true
	case parse.DeclObjectGroup:
		return m.ObjectGroups[ref.Index].Assignment, true
	case parse.DeclNotificationGroup:
		return m.NotificationGroups[ref.Index].Assignment, true
	case parse.DeclModuleCompliance:
		return m.ModuleCompliances[ref.Index].Assignment, true
	case parse.DeclAgentCapabilities:
		return m.AgentCapabilities[ref.Index].Assignment, true
	case parse.DeclValueAssignment:
		return m.ValueAssignments[ref.Index].Assignment, true
	default:
		return parse.Span{}, false
	}
}

func badAssignment(bd *parse.BadDecl) (parse.Span, bool) {
	if bd.Intended == parse.DeclTrapType {
		return parse.Span{}, false
	}

	for _, c := range bd.Clauses {
		if c.Clause == parse.ClauseAssignment {
			return c.Span, true
		}
	}

	return parse.Span{}, false
}

// placeNodes registers every resolved node by OID and links the tree.
//
// A node's parent is its nearest declared ancestor rather than the arc
// directly above it, because a vendor MIB routinely hangs a subtree off
// an arc nothing in the loaded set names.
//
// One OID is one node in the tree, so where two declarations claim one
// the registration goes to one of them. The other still gets its parent
// pointer, because that is what its kind is read against — two modules
// declaring the same conceptual table is the corpus's own spelling of a
// split MIB, and leaving the second one's row and columns unclassified
// would report a table as a set of scalars. What it does not get is a
// place in its parent's Children, so a consumer walking the tree still
// sees one node per arc.
func (r *resolver) placeNodes(set *ModuleSet) {
	var claimants []*Node

	for _, m := range set.modules {
		for _, n := range m.Nodes {
			if n.OID.Len() == 0 {
				continue
			}
			claimants = append(claimants, n)

			key := n.OID.String()
			kept, dup := set.byOID[key]
			if !dup {
				set.byOID[key] = n

				continue
			}

			set.byOID[key] = r.settleOIDCollision(kept, n, key)
		}
	}

	slices.SortStableFunc(claimants, func(a, b *Node) int { return a.OID.Compare(b.OID) })

	for _, n := range claimants {
		registered := set.byOID[n.OID.String()] == n

		parent := n.OID.Parent()
		for parent.Len() > 0 {
			if p, ok := set.byOID[parent.String()]; ok && p != n {
				n.Parent = p
				if registered {
					p.Children = append(p.Children, n)
				}

				break
			}
			parent = parent.Parent()
		}

		if n.Parent == nil && registered {
			set.roots = append(set.roots, n)
		}
	}
}

// settleOIDCollision decides which of two declarations claiming one OID
// the tree is built from, and reports the loser.
//
// RFC 2578 §3.6 gives every descriptor its own OBJECT IDENTIFIER value,
// so two claimants are the MIB contradicting itself and neither reading
// is the author's. What the choice costs is not symmetric, though. An
// OBJECT IDENTIFIER value only names an arc, while a macro declares an
// object whose kind everything beneath it is read through: drop a
// conceptual table and its row and columns come back as scalars, which
// is a whole subtree misread over a collision one arc up. So a
// declaration that carries a shape wins, and two of a kind are settled
// by the order the modules were gathered in, which is the one a reader
// can predict.
//
// The loser keeps its OID and its clauses and is not marked unresolved:
// nothing about it failed to resolve, and what it lost is a registration
// the RFC never let two declarations share. The diagnostic names both,
// which is what lets a caller decide.
func (r *resolver) settleOIDCollision(kept, other *Node, oid string) *Node {
	winner, loser := kept, other
	if oidClaimStrength(other.Kind) > oidClaimStrength(kept.Kind) {
		winner, loser = other, kept
	}

	file, offset := r.declPosition(loser)
	r.raise(file, offset, ErrCodeDuplicateOID,
		ArgString(qualify(loser)), ArgString(qualify(winner)), ArgString(oid))

	return winner
}

// oidClaimStrength ranks a claimant of a contested OID by how much of
// the tree is read through it. A declaration nothing classified says
// least; a naming node says the arc has a name; a macro says what the
// node is, which is what its subordinates are classified against.
func oidClaimStrength(k NodeKind) int {
	switch k {
	case NodeUnknown:
		return 0
	case NodeNode:
		return 1
	default:
		return 2
	}
}

// qualify names a node the way a diagnostic about two modules has to,
// since the point of the message is that the two are not the same
// declaration.
func qualify(n *Node) string { return n.Module + "." + n.Name }

// declPosition returns the file and offset a node was declared at. A
// node whose module or declaration cannot be found back points at the
// start of the file, which is still the file that wrote it.
func (r *resolver) declPosition(n *Node) (string, int32) {
	b, ok := r.byName[n.Module]
	if !ok {
		return "", 0
	}

	ref, ok := b.decls[n.Name]
	if !ok {
		return b.path, 0
	}

	return b.path, b.pm.Decl(ref).Span.Start
}

// classify settles the table shape and assembles the tables.
//
// It walks in OID order so a parent's kind is always known before its
// children are looked at, which is what lets the shape be read off the
// tree instead of off the row type's spelling.
func (r *resolver) classify(set *ModuleSet) {
	ordered := make([]*Node, 0, len(set.byOID))
	for _, m := range set.modules {
		for _, n := range m.Nodes {
			if n.OID.Len() > 0 {
				ordered = append(ordered, n)
			}
		}
	}
	slices.SortStableFunc(ordered, func(a, b *Node) int { return a.OID.Compare(b.OID) })

	for _, n := range ordered {
		if n.Kind != NodeScalar || n.Parent == nil {
			continue
		}
		switch n.Parent.Kind {
		case NodeTable:
			n.Kind = NodeRow
		case NodeRow:
			n.Kind = NodeColumn
		}
	}

	for _, m := range set.modules {
		for _, n := range m.Nodes {
			if n.Kind != NodeTable {
				continue
			}
			m.Tables = append(m.Tables, buildTable(n))
		}
	}
}

// buildTable gathers a table's row and columns. A table whose row never
// resolved still yields a Table, because the table node is real and a
// consumer that can see it is better off than one that cannot.
func buildTable(table *Node) *Table {
	t := &Table{Node: table}

	for _, child := range table.Children {
		if child.Kind == NodeRow {
			t.Row = child

			break
		}
	}

	if t.Row == nil {
		return t
	}

	// The table gets its own copy of the parts so that resolving them
	// leaves the row's clause exactly as written.
	t.Index = slices.Clone(t.Row.Index)
	t.Augments = t.Row.Augments
	for _, col := range t.Row.Children {
		if col.Kind == NodeColumn {
			t.Columns = append(t.Columns, col)
		}
	}

	return t
}

// resolveIndexes fills in every table's key: each INDEX part gets the
// column and type it names, and each AUGMENTS clause gets the table it
// augments together with that table's parts.
//
// It runs after classify has assembled every module's tables, because an
// AUGMENTS clause may name a row in another module and the table over
// that row has to exist before it can be linked. INDEX parts are
// resolved for every table first, so that an augmenting table always
// copies parts that are already resolved whatever order the modules
// come in.
func (r *resolver) resolveIndexes(set *ModuleSet) {
	byRow := make(map[*Node]*Table)
	for _, m := range set.modules {
		for _, t := range m.Tables {
			if t.Row != nil {
				byRow[t.Row] = t
			}
		}
	}

	for _, m := range set.modules {
		b := r.byName[m.Name]
		for _, t := range m.Tables {
			if t.Row == nil {
				continue
			}
			for i := range t.Index {
				r.resolveIndexPart(b, t.Row, &t.Index[i])
			}
		}
	}

	linked := make(map[*Table]bool)
	for _, m := range set.modules {
		b := r.byName[m.Name]
		for _, t := range m.Tables {
			if t.Row != nil && t.Augments != "" {
				r.linkAugments(set, b, t, byRow, linked)
			}
		}
	}
}

// augmentedTable finds the table over the row an AUGMENTS clause names.
//
// The row is looked up by name first. When that declaration lost an OID
// collision, RMON-MIB's etherStatsEntry beside RFC1271-MIB's for one,
// no table owns it, and the table over the node that kept the OID is
// the one the wire agrees with, so that is the second try.
func (r *resolver) augmentedTable(set *ModuleSet, b *modBuild, name string, byRow map[*Node]*Table) (*Table, bool) {
	row := r.lookupNode(b, name)
	if row == nil {
		return nil, false
	}
	if t, ok := byRow[row]; ok {
		return t, true
	}

	placed, ok := set.byOID[row.OID.String()]
	if !ok || placed.Kind != NodeRow {
		return nil, false
	}
	t, ok := byRow[placed]

	return t, ok
}

// resolveIndexPart resolves one INDEX part of row to the column it
// names, with the same precedence a parent OID gets: the row's own
// module, then the modules its IMPORTS name, then any loaded module.
//
// The diagnostic is raised on the row, which is the declaration that
// wrote the clause; the part itself has no position of its own.
func (r *resolver) resolveIndexPart(b *modBuild, row *Node, part *IndexPart) {
	part.Node = r.lookupNode(b, part.Name)
	if part.Node != nil && part.Node.Type != nil && !part.Node.Type.Unresolved {
		part.Type = part.Node.Type

		return
	}

	part.Unresolved = true
	file, offset := r.declPosition(row)
	r.raise(file, offset, ErrCodeUnresolvedIndexPart, ArgString(part.Name))
}

// linkAugments resolves a table's AUGMENTS clause to the table over the
// row it names and copies that table's resolved parts.
//
// RFC 2578 §7.8.1 requires the augmented row to carry an INDEX clause,
// but a vendor row may augment a row that itself augments another, so
// the augmented table is linked first and a chain that closes on itself
// stops where it started rather than recursing.
func (r *resolver) linkAugments(set *ModuleSet, b *modBuild, t *Table, byRow map[*Node]*Table, linked map[*Table]bool) {
	if linked[t] {
		return
	}
	linked[t] = true

	base, ok := r.augmentedTable(set, b, t.Augments, byRow)
	if !ok || base == t {
		file, offset := r.declPosition(t.Row)
		r.raise(file, offset, ErrCodeUnresolvedAugments, ArgString(t.Augments))

		return
	}

	if base.Augments != "" {
		r.linkAugments(set, r.byName[base.Node.Module], base, byRow, linked)
	}

	t.AugmentsTable = base
	t.Index = slices.Clone(base.Index)
}

// lookupNode finds the node name resolves to as module b sees it, or nil
// when no loaded module declares one under that name.
func (r *resolver) lookupNode(b *modBuild, name string) *Node {
	target, ok := r.lookupDecl(b, name)
	if !ok {
		return nil
	}

	n, ok := target.out.Node(name)
	if !ok {
		return nil
	}

	return n
}

// builtinTypes are the SMI base types under the spellings RFC 2578 §7.1
// gives them. They are what an unqualified type lookup consults first,
// because a module that redefines Counter32 cannot change what the wire
// carries.
//
// The table is built once and never written to, which is what makes an
// unqualified lookup safe for concurrent readers.
var builtinTypes = func() map[string]*Type {
	bases := []BaseType{
		BaseInteger, BaseInteger32, BaseUnsigned32, BaseGauge32, BaseCounter32,
		BaseCounter64, BaseTimeTicks, BaseOctetString, BaseObjectIdentifier,
		BaseBits, BaseIPAddress, BaseOpaque, BaseNull,
	}

	m := make(map[string]*Type, len(bases))
	for _, b := range bases {
		m[b.String()] = &Type{Name: b.String(), Base: b}
	}

	return m
}()

func builtinType(name string) (*Type, bool) {
	t, ok := builtinTypes[name]

	return t, ok
}
