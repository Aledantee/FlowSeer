package parse

import (
	"strconv"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/smi"
)

// wrapV1 and wrapV2 put body inside a module whose IMPORTS say which
// dialect it is written in, because that is the signal a reader of a
// real MIB has and the one detection is meant to read.
func wrapV1(body string) string {
	return "TEST-MIB DEFINITIONS ::= BEGIN\n" +
		"IMPORTS\n    OBJECT-TYPE, Counter, Gauge\n        FROM RFC1155-SMI;\n" +
		body + "\nEND\n"
}

func wrapV2(body string) string {
	return "TEST-MIB DEFINITIONS ::= BEGIN\n" +
		"IMPORTS\n    OBJECT-TYPE, Counter32, Gauge32\n        FROM SNMPv2-SMI;\n" +
		body + "\nEND\n"
}

func wantDialect(t *testing.T, m *Module, want Dialect) {
	t.Helper()

	if m.Dialect != want {
		t.Errorf("module %s reads as %v, want %v", m.Name, m.Dialect, want)
	}
}

func TestDialectFollowsImports(t *testing.T) {
	v1 := parseSource(t, wrapV1(`
sysUpTime OBJECT-TYPE
    SYNTAX      TimeTicks
    ACCESS      read-only
    STATUS      mandatory
    ::= { system 3 }
`))
	wantCodes(t, v1)
	wantDialect(t, module(t, v1), DialectV1)

	v2 := parseSource(t, wrapV2(`
sysUpTime OBJECT-TYPE
    SYNTAX      TimeTicks
    MAX-ACCESS  read-only
    STATUS      current
    DESCRIPTION "uptime"
    ::= { system 3 }
`))
	wantCodes(t, v2)
	wantDialect(t, module(t, v2), DialectV2)
}

// A module that imports from neither SMI is read as SMIv2 by the clauses
// it writes, which is the only evidence left.
func TestDialectFallsBackToClauseForms(t *testing.T) {
	r := parseSource(t, wrap(`
sysUpTime OBJECT-TYPE
    SYNTAX      TimeTicks
    ACCESS      read-only
    STATUS      mandatory
    ::= { system 3 }
`))
	wantCodes(t, r)
	wantDialect(t, module(t, r), DialectV1)
}

// The resolver places a trap by concatenating what the ENTERPRISE clause
// resolves to with the suffix the AST hands it, so the test resolves the
// enterprise the way the resolver will and checks the whole OID.
func TestTrapNotificationOID(t *testing.T) {
	r := parseSource(t, wrapV1(`
linkDown TRAP-TYPE
    ENTERPRISE  cisco
    VARIABLES   { ifIndex }
    DESCRIPTION "a link went down"
    ::= 2
`))
	wantCodes(t, r)
	m := module(t, r)

	if len(m.TrapTypes) != 1 {
		t.Fatalf("got %d trap types, want 1", len(m.TrapTypes))
	}
	trap := m.TrapTypes[0]

	if !trap.Notification.Valid {
		t.Fatal("the trap carries no notification OID")
	}
	wantText(t, r, "ENTERPRISE", trap.Notification.Enterprise, "cisco")

	// The enterprise name resolves in a later pass; here it stands for
	// whatever that pass will find.
	enterprises := map[string]string{"cisco": "1.3.6.1.4.1.9"}
	oid := enterprises[r.Text(trap.Notification.Enterprise)]
	for _, sub := range trap.Notification.Suffix {
		oid += "." + strconv.FormatInt(sub, 10)
	}

	if oid != "1.3.6.1.4.1.9.0.2" {
		t.Errorf("the trap resolves to %s, want 1.3.6.1.4.1.9.0.2", oid)
	}
}

func TestTrapNumberMustBeANumber(t *testing.T) {
	r := parseSource(t, wrapV1(`
linkDown TRAP-TYPE
    ENTERPRISE  cisco
    DESCRIPTION "a link went down"
    ::= { cisco 2 }
`))
	wantCodes(t, r, smi.ErrCodeUnexpectedToken)
	m := module(t, r)

	if len(m.TrapTypes) != 1 {
		t.Fatalf("got %d trap types, want 1", len(m.TrapTypes))
	}
	if m.TrapTypes[0].Notification.Valid {
		t.Error("a trap whose ::= is not a number was given a notification OID")
	}
}

func TestAccessValueGradedByDialect(t *testing.T) {
	v1 := parseSource(t, wrapV1(`
ifAdminStatus OBJECT-TYPE
    SYNTAX      INTEGER
    ACCESS      write-only
    STATUS      mandatory
    ::= { ifEntry 7 }
`))
	wantCodes(t, v1)
	if got := module(t, v1).ObjectTypes[0].Access; v1.Text(got) != "write-only" {
		t.Errorf("ACCESS spans %q, want %q", v1.Text(got), "write-only")
	}

	v2 := parseSource(t, wrapV2(`
ifAdminStatus OBJECT-TYPE
    SYNTAX      INTEGER
    MAX-ACCESS  write-only
    STATUS      current
    DESCRIPTION "an administrative state"
    ::= { ifEntry 7 }
`))
	wantCodes(t, v2, smi.ErrCodeDialectValueMismatch)
}

// read-create is the mirror: an SMIv2 access value has no meaning in a
// module written against RFC 1212.
func TestSMIv2AccessValueInSMIv1IsDiagnosed(t *testing.T) {
	r := parseSource(t, wrapV1(`
ifRowStatus OBJECT-TYPE
    SYNTAX      INTEGER
    ACCESS      read-create
    STATUS      mandatory
    ::= { ifEntry 8 }
`))
	wantCodes(t, r, smi.ErrCodeDialectValueMismatch)
}

func TestStatusValueGradedByDialect(t *testing.T) {
	v1Mandatory := parseSource(t, wrapV1(`
ifIndex OBJECT-TYPE
    SYNTAX      INTEGER
    ACCESS      read-only
    STATUS      mandatory
    ::= { ifEntry 1 }
`))
	wantCodes(t, v1Mandatory)

	v1Current := parseSource(t, wrapV1(`
ifIndex OBJECT-TYPE
    SYNTAX      INTEGER
    ACCESS      read-only
    STATUS      current
    ::= { ifEntry 1 }
`))
	wantCodes(t, v1Current, smi.ErrCodeDialectValueMismatch)

	v2Current := parseSource(t, wrapV2(`
ifIndex OBJECT-TYPE
    SYNTAX      INTEGER
    MAX-ACCESS  read-only
    STATUS      current
    DESCRIPTION "an interface index"
    ::= { ifEntry 1 }
`))
	wantCodes(t, v2Current)

	v2Mandatory := parseSource(t, wrapV2(`
ifIndex OBJECT-TYPE
    SYNTAX      INTEGER
    MAX-ACCESS  read-only
    STATUS      mandatory
    DESCRIPTION "an interface index"
    ::= { ifEntry 1 }
`))
	wantCodes(t, v2Mandatory, smi.ErrCodeDialectValueMismatch)
}

// RFC 1212 leaves DESCRIPTION optional and RFC 2578 requires it, so the
// same object is whole in one dialect and unresolved in the other.
func TestDescriptionIsOptionalInSMIv1(t *testing.T) {
	v1 := parseSource(t, wrapV1(`
ifIndex OBJECT-TYPE
    SYNTAX      INTEGER
    ACCESS      read-only
    STATUS      mandatory
    ::= { ifEntry 1 }
`))
	wantCodes(t, v1)
	m := module(t, v1)

	if len(m.ObjectTypes) != 1 {
		t.Fatalf("got %d object types, want 1", len(m.ObjectTypes))
	}
	if !m.ObjectTypes[0].Present.Satisfies(DeclObjectType, DialectV1) {
		t.Error("an SMIv1 object without a DESCRIPTION is unresolved")
	}

	v2 := parseSource(t, wrapV2(`
ifIndex OBJECT-TYPE
    SYNTAX      INTEGER
    MAX-ACCESS  read-only
    STATUS      current
    ::= { ifEntry 1 }
`))
	wantCodes(t, v2, smi.ErrCodeMissingClause)
	if got := len(module(t, v2).Bad); got != 1 {
		t.Errorf("got %d bad declarations, want 1", got)
	}
}

func TestSMIv1TypesMapToSMIv2(t *testing.T) {
	r := parseSource(t, wrapV1(`
ifInOctets OBJECT-TYPE
    SYNTAX      Counter
    ACCESS      read-only
    STATUS      mandatory
    ::= { ifEntry 10 }

ifSpeed OBJECT-TYPE
    SYNTAX      Gauge (0..4294967295)
    ACCESS      read-only
    STATUS      mandatory
    ::= { ifEntry 5 }

ifPhysAddress OBJECT-TYPE
    SYNTAX      NetworkAddress
    ACCESS      read-only
    STATUS      mandatory
    ::= { ifEntry 6 }
`))
	wantCodes(t, r)
	m := module(t, r)

	want := []struct{ syntax, mapped string }{
		{"Counter", "Counter32"},
		{"Gauge (0..4294967295)", "Gauge32"},
		{"NetworkAddress", "IpAddress"},
	}
	if len(m.ObjectTypes) != len(want) {
		t.Fatalf("got %d object types, want %d", len(m.ObjectTypes), len(want))
	}
	for i, w := range want {
		o := m.ObjectTypes[i]
		wantText(t, r, "SYNTAX", o.Syntax, w.syntax)
		if o.MappedSyntax != w.mapped {
			t.Errorf("%s maps to %q, want %q", o.Name, o.MappedSyntax, w.mapped)
		}
	}
}

func TestSMIv1TypeInSMIv2IsDiagnosed(t *testing.T) {
	r := parseSource(t, wrapV2(`
ifInOctets OBJECT-TYPE
    SYNTAX      Counter
    MAX-ACCESS  read-only
    STATUS      current
    DESCRIPTION "octets in"
    ::= { ifEntry 10 }
`))
	wantCodes(t, r, smi.ErrCodeDialectValueMismatch)

	if got := module(t, r).ObjectTypes[0].MappedSyntax; got != "Counter32" {
		t.Errorf("the object maps to %q, want %q", got, "Counter32")
	}
}

// RFC 1212 lets an INDEX name a type rather than an object, which the
// corpus does and which must not cost the declaration.
func TestSMIv1IndexAdmitsBareType(t *testing.T) {
	r := parseSource(t, wrapV1(`
atEntry OBJECT-TYPE
    SYNTAX      AtEntry
    ACCESS      not-accessible
    STATUS      mandatory
    INDEX       { atIfIndex, NetworkAddress }
    ::= { atTable 1 }
`))
	wantCodes(t, r)
	m := module(t, r)

	if len(m.ObjectTypes) != 1 {
		t.Fatalf("got %d object types, want 1", len(m.ObjectTypes))
	}
	idx := m.ObjectTypes[0].Index
	if len(idx.Parts) != 2 {
		t.Fatalf("got %d index parts, want 2", len(idx.Parts))
	}
	wantText(t, r, "second column", idx.Parts[1].Name, "NetworkAddress")
}

func TestDialectIsRecordedPerModule(t *testing.T) {
	src := "OLD-MIB DEFINITIONS ::= BEGIN\n" +
		"IMPORTS OBJECT-TYPE FROM RFC1155-SMI;\n" +
		"oldThing OBJECT-TYPE\n" +
		"    SYNTAX INTEGER\n    ACCESS read-only\n    STATUS mandatory\n" +
		"    ::= { old 1 }\n" +
		"END\n\n" +
		"NEW-MIB DEFINITIONS ::= BEGIN\n" +
		"IMPORTS OBJECT-TYPE FROM SNMPv2-SMI;\n" +
		"newThing OBJECT-TYPE\n" +
		"    SYNTAX INTEGER\n    MAX-ACCESS read-only\n    STATUS current\n" +
		"    DESCRIPTION \"a thing\"\n" +
		"    ::= { new 1 }\n" +
		"END\n"

	r := parseSource(t, src)
	wantCodes(t, r)

	if len(r.Modules) != 2 {
		t.Fatalf("got %d modules, want 2", len(r.Modules))
	}
	wantDialect(t, &r.Modules[0], DialectV1)
	wantDialect(t, &r.Modules[1], DialectV2)

	if !strings.HasPrefix(r.Modules[0].Name, "OLD") {
		t.Errorf("the first module is %q, want OLD-MIB", r.Modules[0].Name)
	}
}
