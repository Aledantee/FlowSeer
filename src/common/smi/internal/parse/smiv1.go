package parse

import (
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/common/smi/internal/diag"
)

// maxSubIdentifier is the largest value an OID sub-identifier may take,
// which is what bounds a trap number: the number becomes a
// sub-identifier of the notification OID, so a number that cannot be one
// cannot be placed in the tree.
const maxSubIdentifier = 4294967295

// NotificationOID is where a TRAP-TYPE's notification OID comes from,
// given as the two parts resolution concatenates.
//
// Enterprise is the ENTERPRISE clause exactly as written and resolves to
// the prefix. Suffix is appended to that prefix whole, and it already
// carries the zero sub-identifier RFC 3584 §2.1.2 inserts between the
// enterprise and the trap number: the rule is applied here, once, so
// that a resolver placing the trap concatenates rather than re-deriving
// it. Valid is false when the "::=" value was not a usable trap number,
// which leaves the trap readable in the AST with no place in the tree.
type NotificationOID struct {
	Enterprise Span
	Suffix     [2]int64
	Valid      bool
}

// dialects is the set of SMI dialects that define some clause value.
type dialects uint8

const (
	inV1 dialects = 1 << iota
	inV2
)

func (s dialects) has(d Dialect) bool {
	if d == DialectV1 {
		return s&inV1 != 0
	}

	return s&inV2 != 0
}

// accessValues is what an ACCESS or MAX-ACCESS clause may say, per RFC
// 1212 §4 and RFC 2578 §7.3. The two clauses share a table because they
// share a vocabulary; what they do not share is meaning, which is why
// the AST keeps them in separate fields. SMIv1 states the minimum access
// an implementation must provide and SMIv2 states the maximum it may,
// so a value read as the wrong one inverts the object's contract.
var accessValues = map[string]dialects{
	"not-accessible":        inV1 | inV2,
	"read-only":             inV1 | inV2,
	"read-write":            inV1 | inV2,
	"write-only":            inV1,
	"read-create":           inV2,
	"accessible-for-notify": inV2,
}

// statusValues is what a STATUS clause may say. SMIv1's "mandatory" and
// "optional" describe an implementation's obligation; SMIv2's "current"
// describes the definition's own standing, and the two scales do not
// line up, which is why neither dialect's extra values are quietly
// accepted in the other.
var statusValues = map[string]dialects{
	"mandatory":  inV1,
	"optional":   inV1,
	"current":    inV2,
	"deprecated": inV1 | inV2,
	"obsolete":   inV1 | inV2,
}

// equivalentTypes are the SMIv1 base types RFC 3584 §2.1.1 replaces, and
// what each becomes. NetworkAddress is a CHOICE with one arm in
// practice, which is why it collapses to IpAddress.
var equivalentTypes = map[string]string{
	"Counter":        "Counter32",
	"Gauge":          "Gauge32",
	"NetworkAddress": "IpAddress",
}

// MapType returns the SMIv2 type an SMIv1 base type is equivalent to,
// and whether name was one. It is the whole of the type half of RFC 3584
// §2.1.1: the remaining conversions there are rewrites of a definition
// rather than substitutions of a name.
func MapType(name string) (string, bool) {
	v2, ok := equivalentTypes[name]

	return v2, ok
}

// grade reads the module's declarations against the dialect it was
// detected as.
//
// It runs after the clause loop rather than inside it because the
// dialect is a property of the module and a clause reader only sees one
// declaration. Everything here is a judgement about a value that already
// parsed, so nothing it finds costs a declaration: a mismatched STATUS
// is still a STATUS, and dropping the object over it would lose more
// than it reported.
func (p *parser) grade(m *Module) {
	for i := range m.ObjectTypes {
		o := &m.ObjectTypes[i]
		p.gradeValue(ClauseAccess, o.Access, accessValues, m.Dialect)
		p.gradeValue(ClauseMaxAccess, o.MaxAccess, accessValues, m.Dialect)
		p.gradeValue(ClauseStatus, o.Status, statusValues, m.Dialect)
		o.MappedSyntax = p.mapSyntax(o.Syntax, m.Dialect)
	}

	for i := range m.TrapTypes {
		t := &m.TrapTypes[i]
		t.Notification = p.notificationOID(t)
	}

	// The macros below carry a STATUS and nothing else this pass grades.
	for _, o := range m.ObjectIdentities {
		p.gradeValue(ClauseStatus, o.Status, statusValues, m.Dialect)
	}
	for _, tc := range m.TextualConventions {
		p.gradeValue(ClauseStatus, tc.Status, statusValues, m.Dialect)
	}
	for _, n := range m.NotificationTypes {
		p.gradeValue(ClauseStatus, n.Status, statusValues, m.Dialect)
	}
	for _, g := range m.ObjectGroups {
		p.gradeValue(ClauseStatus, g.Status, statusValues, m.Dialect)
	}
	for _, g := range m.NotificationGroups {
		p.gradeValue(ClauseStatus, g.Status, statusValues, m.Dialect)
	}
	for _, c := range m.ModuleCompliances {
		p.gradeValue(ClauseStatus, c.Status, statusValues, m.Dialect)
	}
	for _, a := range m.AgentCapabilities {
		p.gradeValue(ClauseStatus, a.Status, statusValues, m.Dialect)
	}

	// A declaration that lost a required clause keeps the ones it has,
	// and those are worth grading for the same reason they were kept.
	for i := range m.Bad {
		for _, ct := range m.Bad[i].Clauses {
			switch ct.Clause {
			case ClauseAccess, ClauseMaxAccess:
				p.gradeValue(ct.Clause, ct.Span, accessValues, m.Dialect)
			case ClauseStatus:
				p.gradeValue(ct.Clause, ct.Span, statusValues, m.Dialect)
			}
		}
	}
}

// gradeValue reports a clause value that only the other dialect defines.
// A value in neither table is left alone: it is a wrong word rather than
// a wrong dialect, and saying which SMI it belongs to would be a guess.
func (p *parser) gradeValue(c Clause, span Span, table map[string]dialects, d Dialect) {
	if span.End <= span.Start {
		return
	}

	value := p.spanText(span)
	in, known := table[value]
	if !known || in.has(d) {
		return
	}

	p.raise(span.Start, diag.ErrCodeDialectValueMismatch,
		diag.ArgString(c.String()), diag.ArgString(value),
		diag.ArgString(d.other().String()), diag.ArgString(d.String()))
}

// mapSyntax returns the SMIv2 type the SYNTAX clause's base type is
// equivalent to, and reports an SMIv1 type written in an SMIv2 module.
//
// The mapping is recorded whatever the dialect, because what Counter
// means does not depend on which module it was written in, and the
// source spelling stays in Syntax: a later diagnostic that named
// Counter32 would send a reader looking for a word the file does not
// contain.
func (p *parser) mapSyntax(syntax Span, d Dialect) string {
	base := baseTypeName(p.spanText(syntax))

	v2, ok := MapType(base)
	if !ok {
		return ""
	}

	if d != DialectV1 {
		p.raise(syntax.Start, diag.ErrCodeDialectValueMismatch,
			diag.ArgString(ClauseSyntax.String()), diag.ArgString(base),
			diag.ArgString(DialectV1.String()), diag.ArgString(d.String()))
	}

	return v2
}

// baseTypeName is the leading type name of a SYNTAX clause, which is all
// the dialect mapping needs: "Gauge (0..255)" and "Gauge" name the same
// base type, and the constraint is the value grammar's business.
func baseTypeName(syntax string) string {
	for i := 0; i < len(syntax); i++ {
		if !isNameByte(syntax[i]) {
			return syntax[:i]
		}
	}

	return syntax
}

// isNameByte reports whether b may appear in a descriptor. MIB names are
// ASCII by RFC 2578 §3.1, so a byte scan reads the same as a rune one.
func isNameByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9', b == '-':
		return true
	default:
		return false
	}
}

// notificationOID turns a TRAP-TYPE into the notification OID it denotes
// under RFC 3584 §2.1.2: the enterprise, a zero sub-identifier, then the
// trap number. The zero is what keeps an enterprise's traps from
// colliding with the objects registered directly beneath it.
func (p *parser) notificationOID(t *TrapType) NotificationOID {
	oid := NotificationOID{Enterprise: t.Enterprise}

	assigned := p.spanText(t.Assignment)

	number, ok := trapNumber(assigned)
	if !ok {
		p.raise(t.Assignment.Start, diag.ErrCodeUnexpectedToken,
			diag.ArgString(assigned), diag.ArgString("a trap number"))

		return oid
	}

	oid.Suffix = [2]int64{0, number}
	oid.Valid = true

	return oid
}

// trapNumber reads the bare integer a TRAP-TYPE assigns. The macro
// assigns a number rather than an OID, so anything else — a brace group
// a converted module left behind, a negative value, one too large to be
// a sub-identifier — is not a trap number.
func trapNumber(text string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil || n < 0 || n > maxSubIdentifier {
		return 0, false
	}

	return n, true
}
