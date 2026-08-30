package parse

import (
	"strconv"

	"go.aledante.io/FlowSeer/src/common/smi/internal/diag"
	"go.aledante.io/FlowSeer/src/common/smi/internal/lex"
)

// Clause names one clause of an SMI macro. Every clause the RFCs define
// is here, including the ones no consumer reads today: a clause the
// parser does not name is a clause the AST cannot preserve, and the
// object model is meant to survive the arrival of a second consumer.
type Clause uint8

// The clauses. Their order here is editorial; the order a macro writes
// them in comes from clauseOrder.
const (
	ClauseNone Clause = iota
	ClauseSyntax
	ClauseUnits
	ClauseMaxAccess
	ClauseAccess
	ClauseStatus
	ClauseDescription
	ClauseReference
	ClauseIndex
	ClauseAugments
	ClauseDefval
	ClauseLastUpdated
	ClauseOrganization
	ClauseContactInfo
	ClauseRevision
	ClauseDisplayHint
	ClauseObjects
	ClauseNotifications
	ClauseVariables
	ClauseEnterprise
	ClauseModule
	ClauseMandatoryGroups
	ClauseGroup
	ClauseObject
	ClauseWriteSyntax
	ClauseMinAccess
	ClauseProductRelease
	ClauseSupports
	ClauseIncludes
	ClauseVariation
	ClauseCreationRequires
	ClauseAssignment
	numClauses
)

// clauseNames are the words a clause is written as, which is what a
// diagnostic names it by. [ClauseAssignment] has no keyword of its own,
// so it renders as the operator that introduces it.
var clauseNames = [...]string{
	ClauseNone:             "none",
	ClauseSyntax:           "SYNTAX",
	ClauseUnits:            "UNITS",
	ClauseMaxAccess:        "MAX-ACCESS",
	ClauseAccess:           "ACCESS",
	ClauseStatus:           "STATUS",
	ClauseDescription:      "DESCRIPTION",
	ClauseReference:        "REFERENCE",
	ClauseIndex:            "INDEX",
	ClauseAugments:         "AUGMENTS",
	ClauseDefval:           "DEFVAL",
	ClauseLastUpdated:      "LAST-UPDATED",
	ClauseOrganization:     "ORGANIZATION",
	ClauseContactInfo:      "CONTACT-INFO",
	ClauseRevision:         "REVISION",
	ClauseDisplayHint:      "DISPLAY-HINT",
	ClauseObjects:          "OBJECTS",
	ClauseNotifications:    "NOTIFICATIONS",
	ClauseVariables:        "VARIABLES",
	ClauseEnterprise:       "ENTERPRISE",
	ClauseModule:           "MODULE",
	ClauseMandatoryGroups:  "MANDATORY-GROUPS",
	ClauseGroup:            "GROUP",
	ClauseObject:           "OBJECT",
	ClauseWriteSyntax:      "WRITE-SYNTAX",
	ClauseMinAccess:        "MIN-ACCESS",
	ClauseProductRelease:   "PRODUCT-RELEASE",
	ClauseSupports:         "SUPPORTS",
	ClauseIncludes:         "INCLUDES",
	ClauseVariation:        "VARIATION",
	ClauseCreationRequires: "CREATION-REQUIRES",
	ClauseAssignment:       "::=",
}

// extend grows a span to cover end, leaving it alone when a clause's
// payload was rejected and reported no end of its own.
func extend(s *Span, end int32) {
	if end > s.End {
		s.End = end
	}
}

// String returns the clause's spelling, or "clause(N)" off the set.
func (c Clause) String() string {
	if int(c) >= len(clauseNames) {
		return "clause(" + strconv.Itoa(int(c)) + ")"
	}

	return clauseNames[c]
}

// ClauseSet is a set of clauses. Every clause fits in a word, which is
// what lets a node carry the set it parsed and a sync set be a value
// rather than a map lookup per token.
type ClauseSet uint64

func setOf(clauses ...Clause) ClauseSet {
	var s ClauseSet
	for _, c := range clauses {
		s |= 1 << c
	}

	return s
}

// Has reports whether c is in the set.
func (s ClauseSet) Has(c Clause) bool { return s&(1<<c) != 0 }

func (s ClauseSet) with(c Clause) ClauseSet { return s | 1<<c }

// String lists the set's clauses in declaration order, so a test failure
// names words rather than a bit pattern.
func (s ClauseSet) String() string {
	out := "["
	for c := ClauseNone + 1; c < numClauses; c++ {
		if !s.Has(c) {
			continue
		}
		if len(out) > 1 {
			out += " "
		}
		out += c.String()
	}

	return out + "]"
}

// Satisfies reports whether the set holds every clause kind requires in
// dialect d.
//
// This is the gate a resolution pass reads: a declaration that kept its
// OID but lost a required clause fails here and so cannot be rendered as
// if it were whole. It takes the dialect because what a macro requires
// is not the same under both SMIs, and a caller that has a declaration
// always has the module it came from.
func (s ClauseSet) Satisfies(kind DeclKind, d Dialect) bool {
	req := requiredIn(kind, d)

	return s.withAccess()&req == req
}

// Missing returns the clauses kind requires in dialect d that the set
// lacks.
func (s ClauseSet) Missing(kind DeclKind, d Dialect) ClauseSet {
	return requiredIn(kind, d) &^ s.withAccess()
}

// withAccess counts an SMIv1 ACCESS clause as the access clause an
// OBJECT-TYPE requires, whichever dialect is grading. The two are
// separate fields because their semantics are inverted, and the dialect
// pass grades which one belongs where; demoting every SMIv1 object over
// the spelling would prejudge that.
func (s ClauseSet) withAccess() ClauseSet {
	if s.Has(ClauseAccess) {
		return s.with(ClauseMaxAccess)
	}

	return s
}

// clauseOrder is each macro's fixed clause order, which is the order the
// RFCs give and the order the out-of-order diagnostic measures against.
// Clauses that may repeat or interleave are not here; they are in
// extraClauses, because ordering a repeated clause against itself would
// report every well-formed MODULE-COMPLIANCE in the corpus.
var clauseOrder = [numDeclKinds][]Clause{
	DeclObjectType: {
		ClauseSyntax, ClauseUnits, ClauseMaxAccess, ClauseAccess, ClauseStatus,
		ClauseDescription, ClauseReference, ClauseIndex, ClauseAugments, ClauseDefval,
	},
	DeclObjectIdentity:    {ClauseStatus, ClauseDescription, ClauseReference},
	DeclModuleIdentity:    {ClauseLastUpdated, ClauseOrganization, ClauseContactInfo, ClauseDescription},
	DeclTextualConvention: {ClauseDisplayHint, ClauseStatus, ClauseDescription, ClauseReference, ClauseSyntax},
	DeclNotificationType:  {ClauseObjects, ClauseStatus, ClauseDescription, ClauseReference},
	DeclTrapType:          {ClauseEnterprise, ClauseVariables, ClauseDescription, ClauseReference},
	DeclObjectGroup:       {ClauseObjects, ClauseStatus, ClauseDescription, ClauseReference},
	DeclNotificationGroup: {ClauseNotifications, ClauseStatus, ClauseDescription, ClauseReference},
	DeclModuleCompliance:  {ClauseStatus, ClauseDescription, ClauseReference},
	DeclAgentCapabilities: {ClauseProductRelease, ClauseStatus, ClauseDescription, ClauseReference},
}

// extraClauses are the clauses a macro may repeat or interleave, which
// the clause loop accepts anywhere and the order check ignores.
var extraClauses = [numDeclKinds]ClauseSet{
	DeclModuleIdentity:    setOf(ClauseRevision),
	DeclModuleCompliance:  setOf(ClauseModule, ClauseMandatoryGroups, ClauseGroup, ClauseObject),
	DeclAgentCapabilities: setOf(ClauseSupports, ClauseIncludes, ClauseVariation),
}

// requiredClauses is what a declaration of each kind must carry to be
// resolvable. It is the RFC's mandatory set, not the subset the code
// generator happens to read.
var requiredClauses = [numDeclKinds]ClauseSet{
	DeclObjectType:        setOf(ClauseSyntax, ClauseMaxAccess, ClauseStatus, ClauseDescription, ClauseAssignment),
	DeclObjectIdentity:    setOf(ClauseStatus, ClauseDescription, ClauseAssignment),
	DeclModuleIdentity:    setOf(ClauseLastUpdated, ClauseOrganization, ClauseContactInfo, ClauseDescription, ClauseAssignment),
	DeclTextualConvention: setOf(ClauseStatus, ClauseDescription, ClauseSyntax),
	DeclNotificationType:  setOf(ClauseStatus, ClauseDescription, ClauseAssignment),
	DeclTrapType:          setOf(ClauseEnterprise, ClauseAssignment),
	DeclObjectGroup:       setOf(ClauseObjects, ClauseStatus, ClauseDescription, ClauseAssignment),
	DeclNotificationGroup: setOf(ClauseNotifications, ClauseStatus, ClauseDescription, ClauseAssignment),
	DeclModuleCompliance:  setOf(ClauseStatus, ClauseDescription, ClauseModule, ClauseAssignment),
	DeclAgentCapabilities: setOf(ClauseProductRelease, ClauseStatus, ClauseDescription, ClauseAssignment),
	DeclValueAssignment:   setOf(ClauseAssignment),
	DeclTypeAssignment:    setOf(ClauseSyntax),
}

// allowedClauses and clauseOrdinals are derived from the tables above so
// that adding a clause to a macro means editing one line.
var (
	allowedClauses [numDeclKinds]ClauseSet
	clauseOrdinals [numDeclKinds][numClauses]int8
)

func init() {
	for kind := range numDeclKinds {
		set := extraClauses[kind]
		for i, c := range clauseOrder[kind] {
			set = set.with(c)
			clauseOrdinals[kind][c] = int8(i + 1)
		}
		allowedClauses[kind] = set
	}
}

// clauseOf maps a reserved word to the clause it introduces. A word that
// introduces no clause, and one that only appears inside a clause's
// payload, both map to [ClauseNone].
func clauseOf(kw lex.Keyword) Clause {
	switch kw {
	case lex.KeywordSyntax:
		return ClauseSyntax
	case lex.KeywordUnits:
		return ClauseUnits
	case lex.KeywordMaxAccess:
		return ClauseMaxAccess
	case lex.KeywordAccess:
		return ClauseAccess
	case lex.KeywordStatus:
		return ClauseStatus
	case lex.KeywordDescription:
		return ClauseDescription
	case lex.KeywordReference:
		return ClauseReference
	case lex.KeywordIndex:
		return ClauseIndex
	case lex.KeywordAugments:
		return ClauseAugments
	case lex.KeywordDefval:
		return ClauseDefval
	case lex.KeywordLastUpdated:
		return ClauseLastUpdated
	case lex.KeywordOrganization:
		return ClauseOrganization
	case lex.KeywordContactInfo:
		return ClauseContactInfo
	case lex.KeywordRevision:
		return ClauseRevision
	case lex.KeywordDisplayHint:
		return ClauseDisplayHint
	case lex.KeywordObjects:
		return ClauseObjects
	case lex.KeywordNotifications:
		return ClauseNotifications
	case lex.KeywordVariables:
		return ClauseVariables
	case lex.KeywordEnterprise:
		return ClauseEnterprise
	case lex.KeywordModule:
		return ClauseModule
	case lex.KeywordMandatoryGroups:
		return ClauseMandatoryGroups
	case lex.KeywordGroup:
		return ClauseGroup
	case lex.KeywordObject:
		return ClauseObject
	case lex.KeywordWriteSyntax:
		return ClauseWriteSyntax
	case lex.KeywordMinAccess:
		return ClauseMinAccess
	case lex.KeywordProductRelease:
		return ClauseProductRelease
	case lex.KeywordSupports:
		return ClauseSupports
	case lex.KeywordIncludes:
		return ClauseIncludes
	case lex.KeywordVariation:
		return ClauseVariation
	case lex.KeywordCreationRequires:
		return ClauseCreationRequires
	default:
		return ClauseNone
	}
}

// pending is one declaration under construction. It holds every clause
// any macro can carry, and the projection into a typed node reads back
// only the ones its macro has, which is what keeps one clause loop
// serving twelve declaration kinds.
type pending struct {
	kind      DeclKind
	present   ClauseSet
	maxOrd    int8
	maxClause Clause
	nameLost  bool

	clauses []ClauseText
	text    [numClauses]Span

	objects       []Span
	notifications []Span
	variables     []Span
	index         Index
	syntax        Type
	defval        Value
	hint          DisplayHint
	revisions     []Revision
	modules       []ComplianceModule
	supports      []Supported
}

// reset starts a new declaration, keeping the clause slice's capacity
// because a bad declaration clones it before the next one begins.
func (p *parser) reset(kind DeclKind) {
	clauses := p.d.clauses[:0]
	p.d = pending{kind: kind, clauses: clauses}
}

// record notes that clause c parsed, covering span. The first
// occurrence of a clause wins, because a reader can predict that and
// cannot predict the alternative.
func (p *parser) record(c Clause, span Span) {
	repeats := extraClauses[p.d.kind].Has(c)
	ord := clauseOrdinals[p.d.kind][c]

	switch {
	case p.d.present.Has(c) && !repeats:
		p.raise(span.Start, diag.ErrCodeDuplicateClause, diag.ArgString(c.String()))
	case ord > 0 && ord < p.d.maxOrd:
		p.raise(span.Start, diag.ErrCodeClauseOutOfOrder, diag.ArgString(c.String()), diag.ArgString(p.d.maxClause.String()))
	}

	if ord > p.d.maxOrd {
		p.d.maxOrd = ord
		p.d.maxClause = c
	}
	if !p.d.present.Has(c) {
		p.d.text[c] = span
	}

	p.d.present = p.d.present.with(c)
	p.d.clauses = append(p.d.clauses, ClauseText{Clause: c, Span: span})
}

// clauses reads a macro's clause sequence up to its "::=" or the end of
// the frame. Every path through the loop consumes at least one token, so
// the loop ends whatever the source does.
func (p *parser) clauses(kind DeclKind) {
	sync := allowedClauses[kind]

	for p.more() && !p.at(lex.KindAssign) {
		c := clauseOf(p.keyword())
		if c == ClauseNone || !allowedClauses[kind].Has(c) {
			p.unexpectedClause(kind, c)
			p.advance(sync)

			continue
		}

		p.next()
		p.readClause(kind, c)
	}
}

func (p *parser) unexpectedClause(kind DeclKind, c Clause) {
	if c != ClauseNone {
		p.raise(p.offset(), diag.ErrCodeUnknownClause, diag.ArgString(c.String()), diag.ArgString(kind.String()))

		return
	}

	p.raise(p.offset(), diag.ErrCodeUnexpectedToken, diag.ArgString(p.text()), diag.ArgString("a clause keyword"))
}

// readClause reads one clause's payload, the keyword already consumed.
func (p *parser) readClause(kind DeclKind, c Clause) {
	switch c {
	case ClauseDescription, ClauseReference, ClauseUnits,
		ClauseOrganization, ClauseContactInfo, ClauseLastUpdated, ClauseProductRelease:
		p.recordIf(c, p.quoted())

	case ClauseDisplayHint:
		quote := p.tok()
		span := p.quoted()
		if span.End > span.Start {
			p.d.hint = p.parseDisplayHint(span, p.res.StringValue(quote))
		}
		p.recordIf(c, span)

	case ClauseStatus, ClauseMaxAccess, ClauseAccess:
		p.recordIf(c, p.word())

	case ClauseSyntax, ClauseWriteSyntax:
		from := p.pos
		span := p.typeSpan(allowedClauses[kind])
		if c == ClauseSyntax {
			p.d.syntax = p.parseType(p.toks[from:p.pos])
		}
		p.recordIf(c, span)

	case ClauseDefval:
		from := p.pos
		span := p.group()
		p.d.defval = p.parseDefault(p.toks[from:p.pos], p.d.syntax.Base)
		p.recordIf(c, span)

	case ClauseIndex:
		p.readIndex()

	case ClauseAugments:
		p.recordIf(c, p.singleName())

	case ClauseObjects:
		span, names := p.nameList()
		p.d.objects = names
		p.recordIf(c, span)

	case ClauseNotifications:
		span, names := p.nameList()
		p.d.notifications = names
		p.recordIf(c, span)

	case ClauseVariables:
		span, names := p.nameList()
		p.d.variables = names
		p.recordIf(c, span)

	case ClauseEnterprise:
		if p.at(lex.KindLeftBrace) {
			p.recordIf(c, p.group())

			return
		}
		p.recordIf(c, p.word())

	case ClauseRevision:
		p.readRevision()

	case ClauseModule:
		p.readComplianceModule()

	case ClauseMandatoryGroups:
		span, names := p.nameList()
		p.currentModule().MandatoryGroups = names
		p.recordIf(c, span)

	case ClauseGroup, ClauseObject:
		p.readRefinement(c)

	case ClauseSupports:
		p.readSupports()

	case ClauseIncludes:
		span, names := p.nameList()
		p.currentSupport().Includes = names
		p.recordIf(c, span)

	case ClauseVariation:
		p.readVariation()
	}
}

// recordIf records a clause only when its payload parsed. A clause whose
// payload was rejected is absent rather than present-and-empty, so the
// required-clause gate sees what a reader would see.
func (p *parser) recordIf(c Clause, span Span) {
	if span.End > span.Start {
		p.record(c, span)
	}
}

// quoted reads a quoted string payload.
func (p *parser) quoted() Span {
	if !p.at(lex.KindQuotedString) {
		p.raise(p.offset(), diag.ErrCodeUnexpectedToken, diag.ArgString(p.text()), diag.ArgString("a quoted string"))

		return Span{}
	}

	span := p.span()
	p.next()

	return span
}

// word reads a one-token payload such as a STATUS or an access value.
func (p *parser) word() Span {
	if !p.isName() && p.keyword() == lex.KeywordNone {
		p.raise(p.offset(), diag.ErrCodeUnexpectedToken, diag.ArgString(p.text()), diag.ArgString("a name"))

		return Span{}
	}

	span := p.span()
	p.next()

	return span
}

// typeSpan reads a type as the source it covers, stopping at the next
// clause of the enclosing macro.
//
// Finding the boundary and reading the type are two jobs, and this is
// the first. What the tokens inside the span say is read by [Type] from
// the exact token range this consumed, so the clause boundary is settled
// once and the value grammar never has to guess at one.
func (p *parser) typeSpan(sync ClauseSet) Span {
	start := p.offset()
	end := start

	for p.more() && !p.startsClause(sync) && !p.at(lex.KindAssign) {
		if p.opener() {
			end = p.group().End

			continue
		}

		end = p.tok().End()
		p.next()
	}

	return Span{Start: start, End: end}
}

// group consumes one balanced bracket group and returns what it covered.
// It is where both the nesting cap and the member cap bite, since every
// braced construct in the grammar comes through here.
func (p *parser) group() Span {
	if !p.opener() {
		p.raise(p.offset(), diag.ErrCodeUnexpectedToken, diag.ArgString(p.text()), diag.ArgString("an opening bracket"))

		return Span{}
	}

	start := p.offset()
	end := start
	depth := 0
	commas := 0

	for p.more() {
		t := p.toks[p.pos]

		switch t.Kind {
		case lex.KindLeftBrace, lex.KindLeftParen, lex.KindLeftBracket:
			depth++
			p.enter(t.Offset)
		case lex.KindRightBrace, lex.KindRightParen, lex.KindRightBracket:
			depth--
			p.leave()
		case lex.KindComma:
			if depth == 1 {
				commas++
				if commas >= MaxMembers {
					p.limit("enumeration members", MaxMembers, t.Offset)
				}
			}
		}

		end = t.End()
		p.next()

		if depth == 0 {
			break
		}
	}

	return Span{Start: start, End: end}
}

// nameList reads a "{ a, b, c }" payload, returning the group's span and
// each descriptor's.
func (p *parser) nameList() (Span, []Span) {
	if !p.at(lex.KindLeftBrace) {
		p.raise(p.offset(), diag.ErrCodeUnexpectedToken, diag.ArgString(p.text()), diag.ArgString("{"))

		return Span{}, nil
	}

	start := p.offset()
	end := p.tok().End()
	p.next()

	var names []Span
	for p.more() && !p.at(lex.KindRightBrace) {
		switch {
		case p.isName():
			names = append(names, p.span())
			if len(names) > MaxMembers {
				p.limit("enumeration members", MaxMembers, p.offset())
			}
			p.next()
		case p.at(lex.KindComma):
			p.next()
		case p.opener():
			end = p.group().End
		default:
			p.raise(p.offset(), diag.ErrCodeUnexpectedToken, diag.ArgString(p.text()), diag.ArgString("a descriptor"))
			p.next()
		}
	}

	if p.at(lex.KindRightBrace) {
		end = p.tok().End()
		p.next()
	}

	return Span{Start: start, End: end}, names
}

// singleName reads a "{ name }" payload and returns the name's span.
// AUGMENTS is the only clause shaped this way, and what it names is left
// unresolved on purpose.
func (p *parser) singleName() Span {
	span, names := p.nameList()
	if len(names) == 1 {
		return names[0]
	}

	return span
}

// readIndex reads an INDEX clause, keeping each column and whether
// IMPLIED preceded it. Nothing about the columns is resolved.
func (p *parser) readIndex() {
	if !p.at(lex.KindLeftBrace) {
		p.raise(p.offset(), diag.ErrCodeUnexpectedToken, diag.ArgString(p.text()), diag.ArgString("{"))

		return
	}

	start := p.offset()
	end := p.tok().End()
	p.next()

	idx := Index{}
	implied := false

	for p.more() && !p.at(lex.KindRightBrace) {
		switch {
		case p.keyword() == lex.KeywordImplied:
			implied = true
			p.next()
		case p.isName():
			idx.Parts = append(idx.Parts, IndexPart{Name: p.span(), Implied: implied})
			implied = false
			p.next()
		case p.at(lex.KindComma):
			p.next()
		default:
			p.raise(p.offset(), diag.ErrCodeUnexpectedToken, diag.ArgString(p.text()), diag.ArgString("a descriptor"))
			p.next()
		}
	}

	if p.at(lex.KindRightBrace) {
		end = p.tok().End()
		p.next()
	}

	idx.Span = Span{Start: start, End: end}
	p.d.index = idx
	p.record(ClauseIndex, idx.Span)
}

// readRevision reads a REVISION date and the DESCRIPTION that belongs to
// it rather than to the module.
func (p *parser) readRevision() {
	date := p.quoted()
	rev := Revision{Span: date, Date: date}

	if p.keyword() == lex.KeywordDescription {
		p.next()
		rev.Description = p.quoted()
		extend(&rev.Span, rev.Description.End)
	} else {
		p.raise(p.offset(), diag.ErrCodeMissingClause, diag.ArgString(ClauseRevision.String()), diag.ArgString(ClauseDescription.String()))
	}

	p.d.revisions = append(p.d.revisions, rev)
	p.recordIf(ClauseRevision, rev.Span)
}

// currentModule returns the MODULE clause a refinement belongs to,
// opening an implicit one when the source refines without opening. A
// compliance statement that lost its MODULE keyword still says which
// groups it requires, and dropping the refinements would lose that.
func (p *parser) currentModule() *ComplianceModule {
	if len(p.d.modules) == 0 {
		p.d.modules = append(p.d.modules, ComplianceModule{})
	}

	return &p.d.modules[len(p.d.modules)-1]
}

func (p *parser) readComplianceModule() {
	cm := ComplianceModule{Span: Span{Start: p.offset(), End: p.offset()}}
	if p.isName() {
		cm.Name = p.span()
		cm.Span.End = p.tok().End()
		p.next()
	}

	p.d.modules = append(p.d.modules, cm)
	p.record(ClauseModule, p.d.modules[len(p.d.modules)-1].Span)
}

// readRefinement reads one GROUP or OBJECT clause of a MODULE clause.
// Both end at a DESCRIPTION, which is why they are read here rather than
// left to the outer loop: a DESCRIPTION is also a clause of the
// compliance statement itself.
func (p *parser) readRefinement(c Clause) {
	r := Refinement{Kind: RefineGroup, Span: Span{Start: p.offset(), End: p.offset()}}
	if c == ClauseObject {
		r.Kind = RefineObject
	}

	r.Name = p.word()
	extend(&r.Span, r.Name.End)

	sync := setOf(ClauseSyntax, ClauseWriteSyntax, ClauseMinAccess, ClauseDescription,
		ClauseGroup, ClauseObject, ClauseModule)

	for p.more() {
		switch clauseOf(p.keyword()) {
		case ClauseSyntax:
			p.next()
			r.Syntax = p.typeSpan(sync)
			extend(&r.Span, r.Syntax.End)
		case ClauseWriteSyntax:
			p.next()
			r.WriteSyntax = p.typeSpan(sync)
			extend(&r.Span, r.WriteSyntax.End)
		case ClauseMinAccess:
			p.next()
			r.MinAccess = p.word()
			extend(&r.Span, r.MinAccess.End)
		case ClauseDescription:
			p.next()
			r.Description = p.quoted()
			extend(&r.Span, r.Description.End)
		default:
			m := p.currentModule()
			m.Refinements = append(m.Refinements, r)
			p.record(c, r.Span)

			return
		}
	}

	m := p.currentModule()
	m.Refinements = append(m.Refinements, r)
	p.record(c, r.Span)
}

// currentSupport returns the SUPPORTS clause an INCLUDES or VARIATION
// belongs to, opening an implicit one for the same reason
// [parser.currentModule] does.
func (p *parser) currentSupport() *Supported {
	if len(p.d.supports) == 0 {
		p.d.supports = append(p.d.supports, Supported{})
	}

	return &p.d.supports[len(p.d.supports)-1]
}

func (p *parser) readSupports() {
	s := Supported{Span: Span{Start: p.offset(), End: p.offset()}}
	if p.isName() {
		s.Module = p.span()
		s.Span.End = p.tok().End()
		p.next()
	}

	p.d.supports = append(p.d.supports, s)
	p.record(ClauseSupports, p.d.supports[len(p.d.supports)-1].Span)
}

// readVariation reads one VARIATION clause. Object and notification
// variations share this reader because they share a grammar; which
// clauses are legal follows from what the refined name turns out to be,
// and that is not known until it is resolved.
func (p *parser) readVariation() {
	v := Variation{Span: Span{Start: p.offset(), End: p.offset()}}
	v.Name = p.word()
	extend(&v.Span, v.Name.End)

	sync := setOf(ClauseSyntax, ClauseWriteSyntax, ClauseAccess, ClauseCreationRequires,
		ClauseDefval, ClauseDescription, ClauseVariation, ClauseSupports)

	for p.more() {
		switch clauseOf(p.keyword()) {
		case ClauseSyntax:
			p.next()
			from := p.pos
			v.Syntax = p.typeSpan(sync)
			v.SyntaxType = p.parseType(p.toks[from:p.pos])
			extend(&v.Span, v.Syntax.End)
		case ClauseWriteSyntax:
			p.next()
			v.WriteSyntax = p.typeSpan(sync)
			extend(&v.Span, v.WriteSyntax.End)
		case ClauseAccess:
			p.next()
			v.Access = p.word()
			extend(&v.Span, v.Access.End)
		case ClauseCreationRequires:
			p.next()
			span, names := p.nameList()
			v.CreationRequires = names
			extend(&v.Span, span.End)
		case ClauseDefval:
			p.next()
			from := p.pos
			v.Defval = p.group()
			v.DefaultValue = p.parseDefault(p.toks[from:p.pos], v.SyntaxType.Base)
			extend(&v.Span, v.Defval.End)
		case ClauseDescription:
			p.next()
			v.Description = p.quoted()
			extend(&v.Span, v.Description.End)
		default:
			s := p.currentSupport()
			s.Variations = append(s.Variations, v)
			p.record(ClauseVariation, v.Span)

			return
		}
	}

	s := p.currentSupport()
	s.Variations = append(s.Variations, v)
	p.record(ClauseVariation, v.Span)
}

// assignment reads the "::= value" tail a macro invocation ends with.
// The value is kept as source: a brace group is an OID whose parent this
// file may not define, and a bare integer is a trap number, and resolving
// either is a later pass's work.
func (p *parser) assignment() {
	if !p.at(lex.KindAssign) {
		return
	}

	p.next()
	if !p.more() {
		return
	}

	if p.opener() {
		p.record(ClauseAssignment, p.group())

		return
	}

	start := p.offset()
	end := start
	for p.more() {
		end = p.tok().End()
		p.next()
	}

	p.record(ClauseAssignment, Span{Start: start, End: end})
}
