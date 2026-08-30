package parse

import (
	"strconv"
	"strings"
	"testing"
)

// squeeze collapses runs of whitespace so a span's text can be compared
// against a one-line expectation while the fixture keeps its line breaks.
func squeeze(s string) string { return strings.Join(strings.Fields(s), " ") }

func wantText(t *testing.T, r *Result, what string, got Span, want string) {
	t.Helper()

	if s := squeeze(r.Text(got)); s != want {
		t.Errorf("%s spans %q, want %q", what, s, want)
	}
}

func wantValue(t *testing.T, r *Result, what string, got Span, want string) {
	t.Helper()

	if s := r.StringValue(got); s != want {
		t.Errorf("%s reads %q, want %q", what, s, want)
	}
}

func TestObjectTypeKeepsEveryClause(t *testing.T) {
	src := wrap(`
ifSpeed OBJECT-TYPE
    SYNTAX      Gauge32 (0..4294967295)
    UNITS       "bits per second"
    MAX-ACCESS  read-only
    STATUS      current
    DESCRIPTION "an estimate of the interface's bandwidth"
    REFERENCE   "RFC 2863"
    DEFVAL      { 0 }
    ::= { ifEntry 5 }
`)

	r := parseSource(t, src)
	wantCodes(t, r)
	m := module(t, r)

	if len(m.ObjectTypes) != 1 {
		t.Fatalf("got %d object types, want 1", len(m.ObjectTypes))
	}
	o := m.ObjectTypes[0]

	if o.Name != "ifSpeed" {
		t.Errorf("got name %q, want %q", o.Name, "ifSpeed")
	}
	wantText(t, r, "declaration", o.Span,
		`ifSpeed OBJECT-TYPE SYNTAX Gauge32 (0..4294967295) UNITS "bits per second" `+
			`MAX-ACCESS read-only STATUS current `+
			`DESCRIPTION "an estimate of the interface's bandwidth" REFERENCE "RFC 2863" `+
			`DEFVAL { 0 } ::= { ifEntry 5 }`)
	wantText(t, r, "SYNTAX", o.Syntax, "Gauge32 (0..4294967295)")
	wantValue(t, r, "UNITS", o.Units, "bits per second")
	wantText(t, r, "MAX-ACCESS", o.MaxAccess, "read-only")
	wantText(t, r, "STATUS", o.Status, "current")
	wantValue(t, r, "DESCRIPTION", o.Description, "an estimate of the interface's bandwidth")
	wantValue(t, r, "REFERENCE", o.Reference, "RFC 2863")
	wantText(t, r, "DEFVAL", o.Defval, "{ 0 }")
	wantText(t, r, "assignment", o.Assignment, "{ ifEntry 5 }")
}

func TestIndexAugmentsAndImpliedAreUnresolved(t *testing.T) {
	src := wrap(`
tcpConnEntry OBJECT-TYPE
    SYNTAX      TcpConnEntry
    MAX-ACCESS  not-accessible
    STATUS      current
    DESCRIPTION "a connection"
    INDEX       { tcpConnLocalAddress, IMPLIED tcpConnLocalPort }
    ::= { tcpConnTable 1 }

tcpConnExtEntry OBJECT-TYPE
    SYNTAX      TcpConnExtEntry
    MAX-ACCESS  not-accessible
    STATUS      current
    DESCRIPTION "an extension"
    AUGMENTS    { tcpConnEntry }
    ::= { tcpConnExtTable 1 }
`)

	r := parseSource(t, src)
	wantCodes(t, r)
	m := module(t, r)

	if len(m.ObjectTypes) != 2 {
		t.Fatalf("got %d object types, want 2", len(m.ObjectTypes))
	}

	idx := m.ObjectTypes[0].Index
	wantText(t, r, "INDEX", idx.Span, "{ tcpConnLocalAddress, IMPLIED tcpConnLocalPort }")
	if len(idx.Parts) != 2 {
		t.Fatalf("got %d index parts, want 2", len(idx.Parts))
	}
	wantText(t, r, "first column", idx.Parts[0].Name, "tcpConnLocalAddress")
	if idx.Parts[0].Implied {
		t.Error("the first column is marked IMPLIED")
	}
	wantText(t, r, "second column", idx.Parts[1].Name, "tcpConnLocalPort")
	if !idx.Parts[1].Implied {
		t.Error("the IMPLIED column is not marked")
	}

	wantText(t, r, "AUGMENTS", m.ObjectTypes[1].Augments, "tcpConnEntry")
	if m.ObjectTypes[1].Index.Parts != nil {
		t.Error("an AUGMENTS declaration carries index parts")
	}
}

func TestModuleIdentityKeepsEveryRevision(t *testing.T) {
	src := wrap(`
testMIB MODULE-IDENTITY
    LAST-UPDATED "202608300000Z"
    ORGANIZATION "FlowSeer"
    CONTACT-INFO "nobody@example.invalid"
    DESCRIPTION  "the test module"
    REVISION     "202608300000Z"
    DESCRIPTION  "the second revision"
    REVISION     "202601010000Z"
    DESCRIPTION  "the first revision"
    ::= { enterprises 99 }
`)

	r := parseSource(t, src)
	wantCodes(t, r)
	m := module(t, r)

	if len(m.ModuleIdentities) != 1 {
		t.Fatalf("got %d module identities, want 1", len(m.ModuleIdentities))
	}
	mi := m.ModuleIdentities[0]

	wantValue(t, r, "LAST-UPDATED", mi.LastUpdated, "202608300000Z")
	wantValue(t, r, "ORGANIZATION", mi.Organization, "FlowSeer")
	wantValue(t, r, "CONTACT-INFO", mi.ContactInfo, "nobody@example.invalid")
	wantValue(t, r, "DESCRIPTION", mi.Description, "the test module")
	wantText(t, r, "assignment", mi.Assignment, "{ enterprises 99 }")

	if len(mi.Revisions) != 2 {
		t.Fatalf("got %d revisions, want 2", len(mi.Revisions))
	}
	wantValue(t, r, "first revision date", mi.Revisions[0].Date, "202608300000Z")
	wantValue(t, r, "first revision description", mi.Revisions[0].Description, "the second revision")
	wantValue(t, r, "second revision date", mi.Revisions[1].Date, "202601010000Z")
	wantValue(t, r, "second revision description", mi.Revisions[1].Description, "the first revision")
}

func TestObjectIdentityAndTextualConvention(t *testing.T) {
	src := wrap(`
testIdentity OBJECT-IDENTITY
    STATUS      current
    DESCRIPTION "an identity"
    REFERENCE   "somewhere"
    ::= { testMIB 1 }

TestTC ::= TEXTUAL-CONVENTION
    DISPLAY-HINT "255a"
    STATUS       current
    DESCRIPTION  "a convention"
    REFERENCE    "RFC 2579"
    SYNTAX       OCTET STRING (SIZE (0..255))
`)

	r := parseSource(t, src)
	wantCodes(t, r)
	m := module(t, r)

	if len(m.ObjectIdentities) != 1 {
		t.Fatalf("got %d object identities, want 1", len(m.ObjectIdentities))
	}
	oi := m.ObjectIdentities[0]
	wantText(t, r, "STATUS", oi.Status, "current")
	wantValue(t, r, "DESCRIPTION", oi.Description, "an identity")
	wantValue(t, r, "REFERENCE", oi.Reference, "somewhere")
	wantText(t, r, "assignment", oi.Assignment, "{ testMIB 1 }")

	if len(m.TextualConventions) != 1 {
		t.Fatalf("got %d textual conventions, want 1", len(m.TextualConventions))
	}
	tc := m.TextualConventions[0]
	if tc.Name != "TestTC" {
		t.Errorf("got name %q, want %q", tc.Name, "TestTC")
	}
	wantValue(t, r, "DISPLAY-HINT", tc.DisplayHint, "255a")
	wantText(t, r, "STATUS", tc.Status, "current")
	wantValue(t, r, "DESCRIPTION", tc.Description, "a convention")
	wantValue(t, r, "REFERENCE", tc.Reference, "RFC 2579")
	wantText(t, r, "SYNTAX", tc.Syntax, "OCTET STRING (SIZE (0..255))")
}

func TestNotificationsGroupsAndAssignments(t *testing.T) {
	src := wrap(`
testTrap NOTIFICATION-TYPE
    OBJECTS     { ifIndex, ifAdminStatus }
    STATUS      current
    DESCRIPTION "a notification"
    REFERENCE   "RFC 2863"
    ::= { testMIB 2 }

oldTrap TRAP-TYPE
    ENTERPRISE  testMIB
    VARIABLES   { ifIndex }
    DESCRIPTION "an SMIv1 trap"
    ::= 3

testGroup OBJECT-GROUP
    OBJECTS     { ifIndex, ifSpeed }
    STATUS      current
    DESCRIPTION "a group"
    ::= { testMIB 4 }

testNotifyGroup NOTIFICATION-GROUP
    NOTIFICATIONS { testTrap }
    STATUS        current
    DESCRIPTION   "a notification group"
    ::= { testMIB 5 }

testRoot OBJECT IDENTIFIER ::= { enterprises 42 }

TestSeq ::= SEQUENCE { ifIndex Integer32, ifSpeed Gauge32 }
`)

	r := parseSource(t, src)
	wantCodes(t, r)
	m := module(t, r)

	if len(m.NotificationTypes) != 1 {
		t.Fatalf("got %d notification types, want 1", len(m.NotificationTypes))
	}
	nt := m.NotificationTypes[0]
	if len(nt.Objects) != 2 {
		t.Fatalf("got %d objects, want 2", len(nt.Objects))
	}
	wantText(t, r, "first object", nt.Objects[0], "ifIndex")
	wantText(t, r, "assignment", nt.Assignment, "{ testMIB 2 }")

	if len(m.TrapTypes) != 1 {
		t.Fatalf("got %d trap types, want 1", len(m.TrapTypes))
	}
	tt := m.TrapTypes[0]
	wantText(t, r, "ENTERPRISE", tt.Enterprise, "testMIB")
	if len(tt.Variables) != 1 {
		t.Fatalf("got %d variables, want 1", len(tt.Variables))
	}
	wantText(t, r, "trap number", tt.Assignment, "3")

	if len(m.ObjectGroups) != 1 || len(m.ObjectGroups[0].Objects) != 2 {
		t.Fatalf("got object groups %d, want one with two objects", len(m.ObjectGroups))
	}
	if len(m.NotificationGroups) != 1 || len(m.NotificationGroups[0].Notifications) != 1 {
		t.Fatalf("got notification groups %d, want one with one notification", len(m.NotificationGroups))
	}

	if len(m.ValueAssignments) != 1 {
		t.Fatalf("got %d value assignments, want 1", len(m.ValueAssignments))
	}
	wantText(t, r, "assignment", m.ValueAssignments[0].Assignment, "{ enterprises 42 }")

	if len(m.TypeAssignments) != 1 {
		t.Fatalf("got %d type assignments, want 1", len(m.TypeAssignments))
	}
	wantText(t, r, "SYNTAX", m.TypeAssignments[0].Syntax, "SEQUENCE { ifIndex Integer32, ifSpeed Gauge32 }")
}

func TestModuleComplianceRoundTripsEveryClause(t *testing.T) {
	src := wrap(`
testCompliance MODULE-COMPLIANCE
    STATUS      current
    DESCRIPTION "the compliance statement"
    REFERENCE   "RFC 2580"
    MODULE
        MANDATORY-GROUPS { testGroup, testNotifyGroup }
        GROUP  optionalGroup
        DESCRIPTION "only when the agent supports it"
        OBJECT ifSpeed
        SYNTAX Gauge32 (0..1000)
        WRITE-SYNTAX Gauge32 (0..100)
        MIN-ACCESS read-only
        DESCRIPTION "narrowed"
    MODULE IF-MIB
        MANDATORY-GROUPS { ifGeneralGroup }
    ::= { testMIB 6 }
`)

	r := parseSource(t, src)
	wantCodes(t, r)
	m := module(t, r)

	if len(m.ModuleCompliances) != 1 {
		t.Fatalf("got %d compliance statements, want 1", len(m.ModuleCompliances))
	}
	mc := m.ModuleCompliances[0]

	wantText(t, r, "STATUS", mc.Status, "current")
	wantValue(t, r, "DESCRIPTION", mc.Description, "the compliance statement")
	wantValue(t, r, "REFERENCE", mc.Reference, "RFC 2580")
	wantText(t, r, "assignment", mc.Assignment, "{ testMIB 6 }")

	if len(mc.Modules) != 2 {
		t.Fatalf("got %d MODULE clauses, want 2", len(mc.Modules))
	}

	first := mc.Modules[0]
	if first.Name != (Span{}) {
		t.Errorf("the first MODULE names %q, want the enclosing module", r.Text(first.Name))
	}
	if len(first.MandatoryGroups) != 2 {
		t.Fatalf("got %d mandatory groups, want 2", len(first.MandatoryGroups))
	}
	if len(first.Refinements) != 2 {
		t.Fatalf("got %d refinements, want a GROUP and an OBJECT", len(first.Refinements))
	}

	group := first.Refinements[0]
	if group.Kind != RefineGroup {
		t.Errorf("got kind %v, want a GROUP refinement", group.Kind)
	}
	wantText(t, r, "GROUP name", group.Name, "optionalGroup")
	wantValue(t, r, "GROUP description", group.Description, "only when the agent supports it")

	object := first.Refinements[1]
	if object.Kind != RefineObject {
		t.Errorf("got kind %v, want an OBJECT refinement", object.Kind)
	}
	wantText(t, r, "OBJECT name", object.Name, "ifSpeed")
	wantText(t, r, "OBJECT syntax", object.Syntax, "Gauge32 (0..1000)")
	wantText(t, r, "OBJECT write-syntax", object.WriteSyntax, "Gauge32 (0..100)")
	wantText(t, r, "OBJECT min-access", object.MinAccess, "read-only")
	wantValue(t, r, "OBJECT description", object.Description, "narrowed")

	second := mc.Modules[1]
	wantText(t, r, "second MODULE", second.Name, "IF-MIB")
	if len(second.MandatoryGroups) != 1 {
		t.Fatalf("got %d mandatory groups, want 1", len(second.MandatoryGroups))
	}
}

func TestAgentCapabilitiesRoundTripsEveryClause(t *testing.T) {
	src := wrap(`
testCaps AGENT-CAPABILITIES
    PRODUCT-RELEASE "the test agent"
    STATUS          current
    DESCRIPTION     "what the agent implements"
    REFERENCE       "RFC 2580"
    SUPPORTS  IF-MIB
    INCLUDES  { ifGeneralGroup, ifPacketGroup }
    VARIATION ifAdminStatus
    SYNTAX    INTEGER { up(1), down(2) }
    WRITE-SYNTAX INTEGER { up(1) }
    ACCESS    read-only
    CREATION-REQUIRES { ifIndex }
    DEFVAL    { up }
    DESCRIPTION "no testing state"
    VARIATION linkDown
    ACCESS      not-implemented
    DESCRIPTION "the agent never sends it"
    ::= { testMIB 7 }
`)

	r := parseSource(t, src)
	wantCodes(t, r)
	m := module(t, r)

	if len(m.AgentCapabilities) != 1 {
		t.Fatalf("got %d capability statements, want 1", len(m.AgentCapabilities))
	}
	ac := m.AgentCapabilities[0]

	wantValue(t, r, "PRODUCT-RELEASE", ac.ProductRelease, "the test agent")
	wantText(t, r, "STATUS", ac.Status, "current")
	wantValue(t, r, "DESCRIPTION", ac.Description, "what the agent implements")
	wantValue(t, r, "REFERENCE", ac.Reference, "RFC 2580")
	wantText(t, r, "assignment", ac.Assignment, "{ testMIB 7 }")

	if len(ac.Supports) != 1 {
		t.Fatalf("got %d SUPPORTS clauses, want 1", len(ac.Supports))
	}
	s := ac.Supports[0]
	wantText(t, r, "SUPPORTS", s.Module, "IF-MIB")
	if len(s.Includes) != 2 {
		t.Fatalf("got %d included groups, want 2", len(s.Includes))
	}
	if len(s.Variations) != 2 {
		t.Fatalf("got %d variations, want an object one and a notification one", len(s.Variations))
	}

	object := s.Variations[0]
	wantText(t, r, "object variation", object.Name, "ifAdminStatus")
	wantText(t, r, "variation syntax", object.Syntax, "INTEGER { up(1), down(2) }")
	wantText(t, r, "variation write-syntax", object.WriteSyntax, "INTEGER { up(1) }")
	wantText(t, r, "variation access", object.Access, "read-only")
	if len(object.CreationRequires) != 1 {
		t.Fatalf("got %d creation requirements, want 1", len(object.CreationRequires))
	}
	wantText(t, r, "variation defval", object.Defval, "{ up }")
	wantValue(t, r, "variation description", object.Description, "no testing state")

	notification := s.Variations[1]
	wantText(t, r, "notification variation", notification.Name, "linkDown")
	wantText(t, r, "notification access", notification.Access, "not-implemented")
	wantValue(t, r, "notification description", notification.Description, "the agent never sends it")
	if notification.Syntax != (Span{}) {
		t.Error("a notification variation carries a SYNTAX it never had")
	}
}

// TestSlabsArePreSized checks the property that lets a corpus sweep parse
// a thousand-declaration module without a growth curve per kind: the
// frames are counted before any node is built, so no slab reallocates.
func TestSlabsArePreSized(t *testing.T) {
	var b strings.Builder
	const objects = 200

	for i := range objects {
		b.WriteString(objectTypeSource(i))
	}
	for i := range objects {
		b.WriteString(valueAssignmentSource(i))
	}

	r := parseSource(t, wrap(b.String()))
	wantCodes(t, r)
	m := module(t, r)

	checks := []struct {
		name     string
		length   int
		capacity int
	}{
		{"declaration order", len(m.Decls), cap(m.Decls)},
		{"object types", len(m.ObjectTypes), cap(m.ObjectTypes)},
		{"value assignments", len(m.ValueAssignments), cap(m.ValueAssignments)},
	}
	for _, c := range checks {
		if c.length != c.capacity {
			t.Errorf("the %s slab holds %d and has room for %d, so it grew", c.name, c.length, c.capacity)
		}
	}
	if len(m.ObjectTypes) != objects || len(m.ValueAssignments) != objects {
		t.Errorf("got %d object types and %d value assignments, want %d of each",
			len(m.ObjectTypes), len(m.ValueAssignments), objects)
	}
	if cap(m.NotificationTypes) != 0 {
		t.Errorf("a kind the module never uses reserved room for %d", cap(m.NotificationTypes))
	}
}

func objectTypeSource(i int) string {
	return "o" + strconv.Itoa(i) + ` OBJECT-TYPE
    SYNTAX      INTEGER
    MAX-ACCESS  read-only
    STATUS      current
    DESCRIPTION "an object"
    ::= { testMIB ` + strconv.Itoa(i) + " }\n\n"
}

func valueAssignmentSource(i int) string {
	return "v" + strconv.Itoa(i) + " OBJECT IDENTIFIER ::= { testMIB " + strconv.Itoa(i) + " }\n"
}
