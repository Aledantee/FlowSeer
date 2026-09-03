package parse

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/smi/internal/diag"
	"go.aledante.io/FlowSeer/src/common/smi/internal/frame"
	"go.aledante.io/FlowSeer/src/common/smi/internal/lex"
)

const testFile = "test.mib"

// wrap puts body inside a minimal module so a fixture can be written as
// the declarations it is about.
func wrap(body string) string {
	return "TEST-MIB DEFINITIONS ::= BEGIN\n" + body + "\nEND\n"
}

func parseSource(t *testing.T, src string) *Result {
	t.Helper()

	return Parse(frame.Cut([]byte(src), frame.Options{File: testFile}))
}

// module returns the file's first module, which every fixture here has
// exactly one of.
func module(t *testing.T, r *Result) *Module {
	t.Helper()

	if len(r.Modules) == 0 {
		t.Fatal("the file yielded no modules")
	}

	return &r.Modules[0]
}

func rendered(r *Result) []diag.Rendered {
	out := make([]diag.Rendered, 0, len(r.Diagnostics))
	for _, d := range r.Diagnostics {
		out = append(out, d.Render(r.Lines))
	}

	return out
}

func wantCodes(t *testing.T, r *Result, want ...errs.Code) {
	t.Helper()

	got := make([]errs.Code, 0, len(r.Diagnostics))
	for _, d := range r.Diagnostics {
		got = append(got, d.Code())
	}
	if len(got) != len(want) {
		t.Fatalf("got diagnostics %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("diagnostic %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

// at returns the line and column of the first occurrence of needle, so a
// fixture can move without a test learning a new magic number.
func at(t *testing.T, src, needle string) (int, int) {
	t.Helper()

	i := strings.Index(src, needle)
	if i < 0 {
		t.Fatalf("fixture has no %q", needle)
	}

	return diag.NewLineTable([]byte(src)).LineColumn(i)
}

func declNamed(m *Module, name string) (Ref, bool) {
	for _, ref := range m.Decls {
		if m.Decl(ref).Name == name {
			return ref, true
		}
	}

	return Ref{}, false
}

func TestObjectTypeMissingSyntaxIsBad(t *testing.T) {
	src := wrap(`
before OBJECT-TYPE
    SYNTAX      INTEGER
    MAX-ACCESS  read-only
    STATUS      current
    DESCRIPTION "before"
    ::= { test 1 }

broken OBJECT-TYPE
    MAX-ACCESS  read-only
    STATUS      current
    DESCRIPTION "broken"
    ::= { test 2 }

after OBJECT-TYPE
    SYNTAX      INTEGER
    MAX-ACCESS  read-only
    STATUS      current
    DESCRIPTION "after"
    ::= { test 3 }
`)

	r := parseSource(t, src)
	m := module(t, r)

	if len(m.ObjectTypes) != 2 {
		t.Fatalf("got %d object types, want the two well-formed ones", len(m.ObjectTypes))
	}
	if len(m.Bad) != 1 {
		t.Fatalf("got %d bad declarations, want 1", len(m.Bad))
	}

	bad := m.Bad[0]
	if bad.Name != "broken" {
		t.Errorf("bad declaration is named %q, want %q", bad.Name, "broken")
	}
	if bad.Intended != DeclObjectType {
		t.Errorf("bad declaration intended %v, want %v", bad.Intended, DeclObjectType)
	}
	if !bad.Missing.Has(ClauseSyntax) {
		t.Errorf("missing set %v does not name SYNTAX", bad.Missing)
	}
	if !bad.Present.Has(ClauseDescription) {
		t.Errorf("present set %v lost the DESCRIPTION that did parse", bad.Present)
	}

	ds := rendered(r)
	if len(ds) != 1 {
		t.Fatalf("got %v, want one diagnostic", ds)
	}
	if ds[0].Code != diag.ErrCodeMissingClause {
		t.Errorf("got %v, want %v", ds[0].Code, diag.ErrCodeMissingClause)
	}

	line, col := at(t, src, "broken OBJECT-TYPE")
	if ds[0].Line != line || ds[0].Column != col {
		t.Errorf("diagnostic at %d:%d, want %d:%d", ds[0].Line, ds[0].Column, line, col)
	}
}

func TestBadDeclarationIsNotAbsent(t *testing.T) {
	src := wrap(`
broken OBJECT-TYPE
    STATUS      current
    ::= { test 1 }
`)

	r := parseSource(t, src)
	m := module(t, r)

	ref, ok := declNamed(m, "broken")
	if !ok {
		t.Fatal("the bad declaration is not in the module's declaration order")
	}
	if ref.Kind != DeclBad {
		t.Errorf("got kind %v, want %v", ref.Kind, DeclBad)
	}
	if _, ok := declNamed(m, "neverWritten"); ok {
		t.Error("a name the source never carried resolves to a declaration")
	}
}

func TestClausesOutOfOrderStillParse(t *testing.T) {
	src := wrap(`
scrambled OBJECT-TYPE
    SYNTAX      INTEGER
    STATUS      current
    MAX-ACCESS  read-only
    DESCRIPTION "scrambled"
    ::= { test 1 }
`)

	r := parseSource(t, src)
	m := module(t, r)

	if len(m.ObjectTypes) != 1 {
		t.Fatalf("got %d object types, want 1", len(m.ObjectTypes))
	}
	wantCodes(t, r, diag.ErrCodeClauseOutOfOrder)

	got := r.StringValue(m.ObjectTypes[0].Description)
	if got != "scrambled" {
		t.Errorf("got description %q, want %q", got, "scrambled")
	}
}

func TestDuplicateClausesKeepFirstDecodedPayload(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		read func(*Result, *Module) []string
		want []string
	}{
		{
			name: "syntax default and index",
			body: `o OBJECT-TYPE
    SYNTAX INTEGER
    SYNTAX OCTET STRING
    MAX-ACCESS read-only
    STATUS current
    DESCRIPTION "object"
    INDEX { first }
    INDEX { IMPLIED second }
    DEFVAL { 1 }
    DEFVAL { 2 }
    ::= { iso 3 }`,
			read: func(r *Result, m *Module) []string {
				o := m.ObjectTypes[0]
				return []string{
					r.Text(o.Syntax), o.SyntaxType.Base.String(),
					r.Text(o.Defval), fmt.Sprint(o.DefaultValue.Number),
					r.Text(o.Index.Parts[0].Name), fmt.Sprint(o.Index.Parts[0].Implied),
				}
			},
			want: []string{"INTEGER", "INTEGER", "{ 1 }", "1", "first", "false"},
		},
		{
			name: "display hint",
			body: `Convention ::= TEXTUAL-CONVENTION
    DISPLAY-HINT "d"
    DISPLAY-HINT "x"
    STATUS current
    DESCRIPTION "convention"
    SYNTAX INTEGER`,
			read: func(r *Result, m *Module) []string {
				c := m.TextualConventions[0]
				return []string{r.StringValue(c.DisplayHint), string(c.Hint.Format)}
			},
			want: []string{"d", "d"},
		},
		{
			name: "objects",
			body: `n NOTIFICATION-TYPE
    OBJECTS { first }
    OBJECTS { second }
    STATUS current
    DESCRIPTION "notification"
    ::= { iso 3 }`,
			read: func(r *Result, m *Module) []string {
				return []string{r.Text(m.NotificationTypes[0].Objects[0])}
			},
			want: []string{"first"},
		},
		{
			name: "notifications",
			body: `g NOTIFICATION-GROUP
    NOTIFICATIONS { first }
    NOTIFICATIONS { second }
    STATUS current
    DESCRIPTION "group"
    ::= { iso 3 }`,
			read: func(r *Result, m *Module) []string {
				return []string{r.Text(m.NotificationGroups[0].Notifications[0])}
			},
			want: []string{"first"},
		},
		{
			name: "variables",
			body: `n TRAP-TYPE
    ENTERPRISE iso
    VARIABLES { first }
    VARIABLES { second }
    ::= 3`,
			read: func(r *Result, m *Module) []string {
				return []string{r.Text(m.TrapTypes[0].Variables[0])}
			},
			want: []string{"first"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := parseSource(t, wrap(tc.body))
			if len(r.Diagnostics) == 0 {
				t.Fatal("duplicate clauses raised no diagnostic")
			}
			for _, d := range r.Diagnostics {
				if d.Code() != diag.ErrCodeDuplicateClause {
					t.Errorf("got diagnostic %v, want %v", d.Code(), diag.ErrCodeDuplicateClause)
				}
			}
			if got := tc.read(r, module(t, r)); !slices.Equal(got, tc.want) {
				t.Errorf("got clause values %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAbsentOptionalClauseIsSilent(t *testing.T) {
	src := wrap(`
plain OBJECT-TYPE
    SYNTAX      INTEGER
    MAX-ACCESS  read-only
    STATUS      current
    DESCRIPTION "no units, no reference, no defval"
    ::= { test 1 }
`)

	r := parseSource(t, src)
	wantCodes(t, r)

	m := module(t, r)
	if len(m.ObjectTypes) != 1 {
		t.Fatalf("got %d object types, want 1", len(m.ObjectTypes))
	}
	if !m.ObjectTypes[0].Present.Satisfies(DeclObjectType, DialectV2) {
		t.Error("a declaration with every required clause reports its required set unsatisfied")
	}
}

func TestAbsentRequiredClauseIsUnsatisfied(t *testing.T) {
	src := wrap(`
noStatus OBJECT-TYPE
    SYNTAX      INTEGER
    MAX-ACCESS  read-only
    DESCRIPTION "status is required"
    ::= { test 1 }
`)

	r := parseSource(t, src)
	wantCodes(t, r, diag.ErrCodeMissingClause)

	m := module(t, r)
	if len(m.Bad) != 1 {
		t.Fatalf("got %d bad declarations, want 1", len(m.Bad))
	}
	if m.Bad[0].Present.Satisfies(DeclObjectType, DialectV2) {
		t.Error("a declaration missing STATUS reports its required set satisfied")
	}
}

func TestOneDiagnosticPerSourceLine(t *testing.T) {
	var b strings.Builder
	for i := range 20 {
		fmt.Fprintf(&b, "bad%d OBJECT-TYPE WHAT EVER ::= { test %d }\n", i, i)
	}
	b.WriteString(`good OBJECT-TYPE SYNTAX INTEGER MAX-ACCESS read-only STATUS current DESCRIPTION "ok" ::= { test 99 }` + "\n")

	r := parseSource(t, wrap(b.String()))
	m := module(t, r)

	if len(m.Bad) != 20 {
		t.Errorf("got %d bad declarations, want 20", len(m.Bad))
	}
	if len(m.ObjectTypes) != 1 {
		t.Fatalf("got %d object types, want the twenty-first declaration", len(m.ObjectTypes))
	}
	if m.ObjectTypes[0].Name != "good" {
		t.Errorf("got %q, want %q", m.ObjectTypes[0].Name, "good")
	}

	seen := map[int]diag.Rendered{}
	for _, d := range rendered(r) {
		if prev, dup := seen[d.Line]; dup {
			t.Errorf("line %d carries two diagnostics:\n %v\n %v", d.Line, prev, d)
		}
		seen[d.Line] = d
	}
}

func TestErrorConsumesNoMoreThanItsFrame(t *testing.T) {
	src := wrap(`
garbage OBJECT-TYPE
    ] ) 7 , , | .. ] }
    ::= { test 1 }

survivor OBJECT-TYPE
    SYNTAX      INTEGER
    MAX-ACCESS  read-only
    STATUS      current
    DESCRIPTION "survivor"
    ::= { test 2 }
`)

	r := parseSource(t, src)
	m := module(t, r)

	if len(m.ObjectTypes) != 1 || m.ObjectTypes[0].Name != "survivor" {
		t.Fatalf("got %d object types, want only survivor", len(m.ObjectTypes))
	}

	start := m.ObjectTypes[0].Span.Start
	for _, d := range r.Diagnostics {
		if int32(d.Position().Offset) >= start {
			t.Errorf("diagnostic %v at offset %d reaches into the next frame, which starts at %d",
				d.Code(), d.Position().Offset, start)
		}
	}
}

func TestNestingBeyondCapIsFatal(t *testing.T) {
	depth := frame.MaxDepth + 8

	var b strings.Builder
	b.WriteString("deep OBJECT-TYPE SYNTAX INTEGER ")
	b.WriteString(strings.Repeat("(", depth))
	b.WriteString("0")
	b.WriteString(strings.Repeat(")", depth))
	b.WriteString(` MAX-ACCESS read-only STATUS current DESCRIPTION "d" ::= { test 1 }`)

	src := []byte(b.String())
	res := lex.Lex(src, lex.Options{File: testFile})
	fr := frame.Frame{
		Kind:   frame.KindMacroInvocation,
		Name:   "deep",
		Span:   frame.Span{Start: 0, End: int32(len(src))},
		Tokens: res.Tokens,
	}

	p := newParser(res, testFile)
	m := newModule(frame.Module{Name: "TEST-MIB", Span: fr.Span, Frames: []frame.Frame{fr}})
	p.declaration(&m, fr)

	if !p.fatal {
		t.Error("nesting past the cap did not stop the file")
	}
	if len(p.diags) == 0 || p.diags[len(p.diags)-1].Code() != diag.ErrCodeLimitExceeded {
		t.Fatalf("got %v, want a limit diagnostic", p.diags)
	}
}

func TestDiagnosticLimitStopsTheFile(t *testing.T) {
	var b strings.Builder
	for i := range frame.MaxDiagnostics + 100 {
		fmt.Fprintf(&b, "o%d OBJECT-TYPE ::= { test %d }\n", i, i)
	}

	r := parseSource(t, wrap(b.String()))

	if len(r.Diagnostics) != frame.MaxDiagnostics+1 {
		t.Errorf("got %d diagnostics, want the cap plus the one that reports it", len(r.Diagnostics))
	}

	last := r.Diagnostics[len(r.Diagnostics)-1]
	if last.Code() != diag.ErrCodeLimitExceeded {
		t.Errorf("got %v, want %v", last.Code(), diag.ErrCodeLimitExceeded)
	}
	if len(r.Modules) != 0 {
		t.Errorf("got %d modules, want none: a limit costs the file", len(r.Modules))
	}
}

func TestEnumerationMemberLimit(t *testing.T) {
	var b strings.Builder
	b.WriteString("wide OBJECT-TYPE SYNTAX INTEGER {")
	for i := range MaxMembers + 1 {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "m%d(%d)", i, i)
	}
	b.WriteString(`} MAX-ACCESS read-only STATUS current DESCRIPTION "d" ::= { test 1 }` + "\n")

	r := parseSource(t, wrap(b.String()))

	if len(r.Diagnostics) == 0 || r.Diagnostics[len(r.Diagnostics)-1].Code() != diag.ErrCodeLimitExceeded {
		t.Fatalf("got %d diagnostics, want a limit diagnostic last", len(r.Diagnostics))
	}
}

// TestNoPanicEscapesTheFrame feeds the parser input designed to derail it
// and asserts the only thing that reaches the caller is diagnostics. A
// bail-out that is not the sentinel re-panics by design, so a crash here
// is a real parser bug rather than a rejected input.
func TestNoPanicEscapesTheFrame(t *testing.T) {
	bodies := []string{
		"",
		"x OBJECT-TYPE",
		"x OBJECT-TYPE SYNTAX",
		"x OBJECT-TYPE INDEX { IMPLIED",
		"x OBJECT-TYPE INDEX { , , } ::= { }",
		"x OBJECT-TYPE DEFVAL { { } } ::= { test 1 }",
		"x MODULE-COMPLIANCE MODULE MODULE MODULE ::= { test 1 }",
		"x AGENT-CAPABILITIES SUPPORTS VARIATION VARIATION ::= { test 1 }",
		"x NOTIFICATION-TYPE OBJECTS { a, } STATUS ::= { test 1 }",
		"x TRAP-TYPE ENTERPRISE VARIABLES ::= 1",
		"X ::= TEXTUAL-CONVENTION SYNTAX",
		"X ::= SEQUENCE {",
		"x OBJECT IDENTIFIER ::= {",
		"::= ::= ::=",
		"} } } }",
	}

	for _, body := range bodies {
		r := parseSource(t, wrap(body))
		for _, m := range r.Modules {
			for _, ref := range m.Decls {
				if ref.Kind >= numDeclKinds {
					t.Errorf("%q: declaration kind %d is off the set", body, ref.Kind)
				}
			}
		}
	}
}

// TestGradeAtTheDiagnosticLimitDoesNotEscape pins the recover the grade
// pass needs. Grading runs after the declaration loop, so a limit it
// reaches unwinds with no frame boundary between it and Parse's caller.
func TestGradeAtTheDiagnosticLimitDoesNotEscape(t *testing.T) {
	// An SMIv2 module whose ACCESS clause says an SMIv1-only word: the
	// declaration parses, and only the grade pass has anything to say
	// about it.
	src := wrap(`IMPORTS OBJECT-TYPE FROM SNMPv2-SMI;
o OBJECT-TYPE SYNTAX INTEGER ACCESS write-only STATUS current DESCRIPTION "d" ::= { test 1 }`)

	f := frame.Cut([]byte(src), frame.Options{File: testFile})

	// Seed the file at the cap, which the parser clones as its starting
	// diagnostic list, so the grade pass is the call that trips it.
	for range diag.MaxDiagnostics {
		f.Diagnostics = append(f.Diagnostics, diag.Raise(
			diag.Position{File: testFile},
			diag.ErrCodeLimitExceeded,
			diag.ArgString("diagnostics"), diag.ArgInt(diag.MaxDiagnostics),
		))
	}

	r := Parse(f)

	if r == nil {
		t.Fatal("Parse returned nothing")
	}
	last := r.Diagnostics[len(r.Diagnostics)-1]
	if last.Code() != diag.ErrCodeLimitExceeded {
		t.Errorf("got %v, want the limit diagnostic that ended the grade pass", last.Code())
	}
}
