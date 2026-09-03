package differential

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/sleepinggenius2/gosmi"
	gosmimodels "github.com/sleepinggenius2/gosmi/models"
	gosmitypes "github.com/sleepinggenius2/gosmi/types"

	"go.aledante.io/FlowSeer/src/protocol/smi"
)

// Field names one comparable field of the projection.
//
// The projection is the field set gosmi can supply, and it is written
// down rather than implied so a reader can see what this harness
// checks and what it does not. [Fields] is the whole list; [Excluded]
// names what was left out and why.
type Field string

// The projection. Every field here is read off both models and
// compared; anything a comparison reports carries one of these names.
const (
	// FieldDeclarationPresent fires when gosmi declares a name in a
	// module and the parser does not. The other direction is counted as
	// a shortfall instead — see [Compare].
	FieldDeclarationPresent Field = "declaration-present"

	// FieldTypePresent fires when gosmi declares a named type in a
	// module and the parser does not.
	FieldTypePresent Field = "type-present"

	FieldOID    Field = "oid"
	FieldKind   Field = "kind"
	FieldAccess Field = "access"
	FieldStatus Field = "status"

	// FieldTypeName is the name of the type an object's SYNTAX resolved
	// to: a textual convention, a type assignment, or empty for a type
	// written inline.
	FieldTypeName Field = "type-name"

	// FieldBaseType is what the type finally stands on, spelled in
	// gosmi's vocabulary. See [gosmiBase] for the correspondence.
	FieldBaseType Field = "base-type"

	// FieldMembers is the named-number set of an enumeration or a BITS
	// type, compared as a name-to-number map. Declaration order is not
	// compared — see [Excluded].
	FieldMembers Field = "members"

	// FieldRanges is the range or SIZE constraint set, compared sorted.
	FieldRanges Field = "ranges"

	// FieldDisplayHint is the DISPLAY-HINT of a textual convention.
	FieldDisplayHint Field = "display-hint"

	// FieldDescriptionPresent reports that gosmi kept a DESCRIPTION the
	// parser did not. The text itself is not compared — see [Excluded].
	FieldDescriptionPresent Field = "description-present"
)

// Fields returns the projection in report order.
func Fields() []Field {
	return []Field{
		FieldDeclarationPresent,
		FieldTypePresent,
		FieldOID,
		FieldKind,
		FieldAccess,
		FieldStatus,
		FieldTypeName,
		FieldBaseType,
		FieldMembers,
		FieldRanges,
		FieldDisplayHint,
		FieldDescriptionPresent,
	}
}

// Excluded is what the projection deliberately leaves out, each with the
// reason it is out. It is data rather than prose so the list cannot be
// updated in a comment and forgotten in the code.
//
// Two reasons recur. A clause gosmi never surfaced cannot be compared
// against anything, and a clause the parser deliberately does not
// resolve has nothing on its side to compare either; both are covered
// by the semantic golden fixtures in package smi instead.
var Excluded = map[string]string{
	"INDEX / AUGMENTS / IMPLIED": "the resolver deliberately returns index columns as the names the source wrote " +
		"and computes no index structure, and gosmi's models.Node carries no index at all",
	"DEFVAL": "gosmi's models.Node carries no default value, so there is nothing on its side to compare; " +
		"the construct that would matter most here is also the one it panics on",
	"UNITS": "gosmi folds a node's UNITS into the type it hands back and the parser keeps it on the " +
		"declaration, so the two are not the same field wearing one name",
	"REFERENCE": "gosmi's models.Node has no reference field; only its type does",
	"DESCRIPTION text": "neither RFC 2578 nor RFC 2579 says how the white space inside a quoted clause is " +
		"folded, so the two implementations fold it differently and a text comparison would report " +
		"formatting rather than meaning. Presence is compared instead, which is what catches a dropped clause",
	"IMPORTS": "gosmi flattens the clause into module-and-symbol pairs and drops the distinction the parser " +
		"records between an import it followed and one it broke to cut a cycle",
	"MODULE-IDENTITY metadata": "ORGANIZATION, CONTACT-INFO and LAST-UPDATED are free text or, for the " +
		"revision date, a parsed time.Time on gosmi's side and the source string on the parser's",
	"declaration order": "gosmi sorts named numbers and ranges as it builds them, so neither members nor " +
		"ranges keep the order the MIB wrote them in and only the sets are comparable",
}

// Divergence is one field of one subject the two models disagree about.
type Divergence struct {
	Module string

	// Subject is the declaration or type the field belongs to.
	Subject string

	Field Field

	// Ours and Gosmi are the two readings, rendered for a human. An
	// empty string means the side had nothing there.
	Ours  string
	Gosmi string

	// OursUnresolved reports that the parser marked the declaration
	// unresolved: a clause its kind requires never arrived, or a name it
	// needed did not resolve. Such a declaration still carries what did
	// parse, so the mark is not a claim that any given field is empty —
	// it is what tells a rule that the declaration never reached the OID
	// tree from one that is placed and merely disagrees.
	OursUnresolved bool
}

// String renders the divergence as one line naming the module, the
// subject and the field, which is what a failure has to say for a
// reviewer to find it.
func (d Divergence) String() string {
	return fmt.Sprintf("%s %s %s: parser %q, gosmi %q", d.Module, d.Subject, d.Field, d.Ours, d.Gosmi)
}

// Fault says which implementation an adjudicated divergence was
// decided against.
//
// Neither side is authoritative by default, so every entry names one.
// A divergence read against the RFC clause text lands on gosmi, on the
// parser, on the MIB where it contradicts itself and the two readings
// differ, or — where the clause says something neither model can hold —
// on both.
type Fault string

// The verdicts.
const (
	FaultGosmi  Fault = "gosmi"
	FaultParser Fault = "parser"
	FaultBoth   Fault = "both"

	// FaultSource is neither implementation's: the MIB contradicts
	// itself and the two resolve the contradiction differently.
	FaultSource Fault = "source"
)

// ExpectedDivergence is one adjudicated way the two models disagree:
// what it is called, who was found at fault, and the reading of the
// source or the RFC that decided it.
//
// A parser fault is recorded here rather than fixed by the harness,
// because a differential test that quietly bent the model to agree with
// its comparand would be measuring nothing. Recording it keeps the
// finding committed and reviewable.
//
// Match decides whether a divergence is this one, and the rules are
// tried in the order [ExpectedDivergences] returns them, so a rule that
// names a specific cause comes before a broader one that would also
// swallow it. A rule that matches nothing over the whole corpus fails
// the run: an expectation nobody meets is either a defect that got
// fixed or a rule that never described anything, and both want a
// reviewer.
type ExpectedDivergence struct {
	Name   string
	Fault  Fault
	Reason string
	Match  func(Divergence) bool
}

// ExpectedDivergences returns the divergences this harness accepts,
// each with the side it was decided against.
//
// Most of them are places gosmi loses what the source said, and the
// four the replacement was argued on are all here: a BITS member's
// number is discarded, an accessible-for-notify object comes back with
// no access at all, and an inline enumeration and an application type
// both arrive under a name the MIB did not write.
//
// One is the parser's own, and it is a documented trade rather than an
// oversight: the shape of a conceptual table is read off the OID tree,
// so a subtree nothing in the loaded set anchors has no shape to read.
// It is recorded rather than fixed because a differential test that bent
// the model to agree with its comparand would be measuring nothing, and
// because changing what the parser classifies from is a change to the
// parser, not to the harness that found it.
func ExpectedDivergences() []ExpectedDivergence {
	return []ExpectedDivergence{
		{
			Name:  "bits-member-numbers-discarded",
			Fault: FaultGosmi,
			Reason: "gosmi parses a BITS member's declared number and then discards it: " +
				"smi/internal/module.go converts the number with the type's own base, and " +
				"smi/internal/type.go's GetValue has no case for BaseTypeBits, so every member " +
				"arrives as 0. The loss is inside an internal package of the dependency, so " +
				"there is no override seam short of a fork",
			Match: func(d Divergence) bool {
				if d.Field != FieldMembers || !allNumbersZero(d.Gosmi) || allNumbersZero(d.Ours) {
					return false
				}

				ours, theirs := strings.Fields(d.Ours), strings.Fields(d.Gosmi)
				if len(ours) != len(theirs) {
					return false
				}
				for i, member := range ours {
					name, _, ok := strings.Cut(member, "=")
					if !ok || theirs[i] != name+"=0" {
						return false
					}
				}

				return true
			},
		},
		{
			Name:  "accessible-for-notify-misspelled-in-gosmi",
			Fault: FaultGosmi,
			Reason: "gosmi's parser spells the access constant \"accesible-for-notify\" with one s, " +
				"so parser.Access.ToSmi never matches the value its own grammar accepts and every " +
				"accessible-for-notify object comes back as access unknown",
			Match: func(d Divergence) bool {
				return d.Field == FieldAccess && d.Ours == "accessible-for-notify" && d.Gosmi == "unknown"
			},
		},
		{
			Name:  "read-create-collapsed-to-read-write",
			Fault: FaultGosmi,
			Reason: "gosmi has no read-create access level and maps the clause onto read-write, " +
				"which loses the row-creation contract RFC 2578 §7.3 attaches to it",
			Match: func(d Divergence) bool {
				return d.Field == FieldAccess && d.Ours == "read-create" && d.Gosmi == "read-write"
			},
		},
		{
			Name:  "write-only-dropped",
			Fault: FaultGosmi,
			Reason: "gosmi maps the SMIv1 write-only access level to unknown with a source comment " +
				"asking what it should be, so an SMIv1 object declared write-only comes back with " +
				"no access at all",
			Match: func(d Divergence) bool {
				return d.Field == FieldAccess && d.Ours == "write-only" && d.Gosmi == "unknown"
			},
		},
		{
			Name:  "inline-type-given-a-synthetic-name",
			Fault: FaultGosmi,
			Reason: "a type written inline in a SYNTAX clause has no name in the MIB. gosmi's " +
				"CreateType walks to the parent type whenever the type it was handed is unnamed, " +
				"so an inline INTEGER {…} comes back as Enumeration, an inline BITS {…} as Bits, " +
				"and a constrained INTEGER32 (1..16) as Integer32. The parser leaves an inline type " +
				"unnamed, so the name still says whether the MIB wrote one",
			Match: func(d Divergence) bool {
				return d.Field == FieldTypeName && d.Ours == ""
			},
		},
		{
			Name:  "application-type-normalized-to-integer-base",
			Fault: FaultGosmi,
			Reason: "gosmi derives a base type from the range an application type's definition " +
				"declares, so Counter32, Gauge32, TimeTicks, IpAddress and Opaque all arrive as one " +
				"of Unsigned32, Unsigned64 or OctetString and only the type-name string still says " +
				"which one the MIB wrote",
			Match: func(d Divergence) bool {
				return d.Field == FieldBaseType && applicationBases[d.Ours] == d.Gosmi
			},
		},
		{
			Name:  "integer-base-derived-from-the-declared-range",
			Fault: FaultGosmi,
			Reason: "gosmi picks an object's base type from the bounds its SYNTAX subtype declares " +
				"rather than from the type being subtyped, so INTEGER (1..16) comes back as " +
				"Unsigned32. It also compares the declared bounds as strings, so INTEGER (0..7) " +
				"comes back as Unsigned64 because \"7\" sorts after \"4294967295\" and " +
				"INTEGER (-99..99) comes back as Integer64. RFC 2578 §7.1.1 " +
				"fixes INTEGER and Integer32 at the signed 32-bit range and §9 makes subtyping a " +
				"restriction of values, not a change of type",
			Match: func(d Divergence) bool {
				return d.Field == FieldBaseType && d.Ours == "Integer32" &&
					(d.Gosmi == "Unsigned32" || d.Gosmi == "Unsigned64" || d.Gosmi == "Integer64")
			},
		},
		{
			Name:  "named-bits-convention-reported-as-an-enumeration",
			Fault: FaultGosmi,
			Reason: "a TEXTUAL-CONVENTION whose SYNTAX is BITS {…} comes back from gosmi with base " +
				"type Enum: the type-assignment branch in smi/internal/module.go sets Enum after " +
				"adding the named numbers without checking whether the parent was BITS. It is the " +
				"same site that discards the members' numbers, so a named BITS convention loses " +
				"both what it is and what its bits are numbered",
			Match: func(d Divergence) bool {
				return d.Field == FieldBaseType && d.Ours == "Bits" && d.Gosmi == "Enum"
			},
		},
		{
			Name:  "constraint-restated-from-a-type-the-declaration-did-not-write",
			Fault: FaultGosmi,
			Reason: "gosmi hands back a range the declaration never wrote, from either of two " +
				"places. SNMPv2-SMI defines the application types as subtyped INTEGERs and OCTET " +
				"STRINGs, so an object declared SYNTAX Counter32 arrives carrying 0..4294967295 " +
				"and one declared IpAddress carries 4..4, while the parser models those as base " +
				"types whose range RFC 2578 §7.1 fixes rather than as a subtype the object " +
				"declared. The rest come from gosmi looking a SYNTAX name up in one global type " +
				"namespace instead of through the module the IMPORTS clause names, so an object " +
				"importing DisplayString FROM RFC1213-MIB, where it is a plain OCTET STRING, " +
				"arrives with SNMPv2-TC's SIZE (0..255)",
			Match: func(d Divergence) bool {
				return d.Field == FieldRanges && d.Ours == ""
			},
		},
		{
			Name:  "display-hint-taken-from-a-same-named-type-in-another-module",
			Fault: FaultGosmi,
			Reason: "gosmi resolves a SYNTAX name against one global type namespace rather than " +
				"through the module the IMPORTS clause names it from, so an object that imports " +
				"DisplayString FROM RFC1213-MIB — where RFC 1213 declares it as a plain OCTET " +
				"STRING with no DISPLAY-HINT — comes back carrying SNMPv2-TC's \"255a\". Only this " +
				"direction is expected: a hint the parser lost and gosmi kept would still fail",
			Match: func(d Divergence) bool {
				return d.Field == FieldDisplayHint && d.Ours == ""
			},
		},
		{
			Name:  "shape-unread-where-nothing-anchors-the-subtree",
			Fault: FaultParser,
			Reason: "the parser reads a conceptual table's shape off the OID tree rather than off " +
				"the row type's SEQUENCE spelling, which is what lets a MIB whose SEQUENCE type " +
				"assignment failed to parse still yield its columns. The cost is here: where the " +
				"module anchoring a subtree is not on the search path — LANCOM-REF-MIB ships as " +
				"lancomref.mib and no corpus file declares CISCOSB-DHCPv6 at all — nothing under it " +
				"resolves to an OID, so there is no tree to classify against and every object in it " +
				"comes back a scalar. gosmi classifies from the row type and reports rows and " +
				"columns for declarations it has no OID for either, so neither side placed the " +
				"subtree; only the parser lost its shape",
			Match: func(d Divergence) bool {
				return d.Field == FieldKind && d.OursUnresolved && d.Ours == "scalar" &&
					(d.Gosmi == "row" || d.Gosmi == "column")
			},
		},
		{
			Name:  "descriptor-beginning-with-a-digit-read-as-an-arc-of-its-own",
			Fault: FaultGosmi,
			Reason: "RFC 2578 §3.1 starts a descriptor with a lowercase letter, and the IEEE 802.3 " +
				"modules anchor themselves at { iso(1) member-body(2) us(840) 802dot3(10006) " +
				"snmpmibs(300) 43 }. gosmi reads the leading digits of 802dot3 as a sub-identifier " +
				"of their own and the rest as a label, which registers the module one arc deeper " +
				"than IEEE 802.3 has it — the same MIB spelled ieee802dot3(10006) elsewhere in the " +
				"corpus resolves to 1.2.840.10006.300.43. The parser reads the whole word as the " +
				"label it is and takes the number from the parentheses",
			Match: func(d Divergence) bool {
				return d.Field == FieldOID && oneExtraArc(d.Ours, d.Gosmi)
			},
		},
		{
			Name:  "column-omitted-from-the-row-SEQUENCE",
			Fault: FaultSource,
			Reason: "where a MIB declares an object under a conceptual row but leaves it out of " +
				"the row's SEQUENCE, the two implementations resolve the contradiction " +
				"differently: gosmi classifies from the SEQUENCE and calls the object a scalar, " +
				"and the parser classifies from the OID tree and calls it a column, which is what " +
				"RFC 2578 §7.1.12 makes every subordinate of a conceptual row",
			Match: func(d Divergence) bool {
				return d.Field == FieldKind && d.Ours == "column" && d.Gosmi == "scalar"
			},
		},
		{
			Name:  "smiv1-trap-zero-arc-given-a-placeholder-node",
			Fault: FaultGosmi,
			Reason: "for an SMIv1 TRAP-TYPE, gosmi invents a node named after the enterprise with " +
				"a \"#\" appended to stand for the zero sub-identifier RFC 2576 §3 puts between the " +
				"enterprise and the trap number. The parser computes the notification OID " +
				"<enterprise>.0.<trap-number> without registering a declaration nothing in the MIB " +
				"wrote",
			Match: func(d Divergence) bool {
				return d.Field == FieldDeclarationPresent && strings.HasSuffix(d.Subject, "#")
			},
		},
		{
			Name:  "named-number-saturated-to-the-signed-32-bit-maximum",
			Fault: FaultGosmi,
			Reason: "gosmi parses every named number and range bound with strconv.ParseInt at 32 " +
				"bits and discards the error, so a value the MIB wrote above 2147483647 — an " +
				"enumeration's unknown(4294967295), or UPS-MIB's INTEGER (-1..2147483648) — comes " +
				"back saturated at the signed 32-bit maximum rather than as what was written",
			Match: func(d Divergence) bool {
				return (d.Field == FieldMembers || d.Field == FieldRanges) &&
					strings.Contains(d.Gosmi, "2147483647") && !strings.Contains(d.Ours, "2147483647")
			},
		},
		{
			Name:  "gosmi-reports-unknown-where-the-clause-was-read",
			Fault: FaultGosmi,
			Reason: "unknown is the zero value of gosmi's Access and Status, and it is what a " +
				"declaration it could not read carries as well as one that declared nothing. " +
				"Where the parser read the clause and gosmi reports unknown, there is no reading " +
				"on gosmi's side to compare against",
			Match: func(d Divergence) bool {
				return (d.Field == FieldAccess || d.Field == FieldStatus) && d.Gosmi == "unknown"
			},
		},
		{
			Name:  "arc-labeled-inline-registered-as-a-declaration",
			Fault: FaultGosmi,
			Reason: "a MIB may spell an OBJECT IDENTIFIER value with labeled arcs — " +
				"{ iso(1) org(3) ieee(111) lan-man-stds(802) … } — and gosmi registers a " +
				"declaration for each label. ASN.1 makes a labeled arc a name for one " +
				"sub-identifier of that value and not a descriptor the module defines, so the " +
				"parser resolves the value and declares nothing. This rule matches any name gosmi " +
				"declares and the parser does not, so a declaration the parser genuinely missed " +
				"would land here too; every occurrence in the corpus today is a labeled arc",
			Match: func(d Divergence) bool {
				return d.Field == FieldDeclarationPresent && d.Ours == ""
			},
		},
		{
			Name:  "counter64-maximum-exceeds-a-signed-64-bit-range",
			Fault: FaultBoth,
			Reason: "RFC 2578 §7.1.10 gives Counter64 the range 0..18446744073709551615, and " +
				"neither model can hold it: the parser's Range is a pair of int64 and saturates " +
				"at 9223372036854775807, and gosmi's wraps to -1. The declared maximum is " +
				"recoverable from the base type on both sides, so nothing downstream reads the " +
				"restated bound, but no reading of this range is the one the RFC wrote",
			Match: func(d Divergence) bool {
				return d.Field == FieldRanges && strings.Contains(d.Ours, "9223372036854775807") &&
					strings.Contains(d.Gosmi, "..-1")
			},
		},
	}
}

// AdjudicatedInstances are the divergences that were read against the
// source one at a time rather than described by a rule.
//
// Every one of them is a module that declares one descriptor twice,
// which RFC 2578 §3.6 forbids: HUAWEI-MIB gives USG6525F both
// { fw 421 } and { fw 433 }, HUAWEI-SITE-MONITOR-MIB declares each of
// its site notifications under two parents, and UI-AF60-MIB writes
// af60StationTable first as an OBJECT IDENTIFIER value and then as the
// OBJECT-TYPE that means it. The parser keeps the first occurrence and
// gosmi the last, and nothing in the divergence itself says which
// reading the MIB meant.
//
// They are listed rather than matched by a predicate because the field
// they land on is the OID, and a predicate broad enough to cover them
// would also cover a resolution bug. The key is module, subject and
// field; the value is what reading the source settled.
var AdjudicatedInstances = map[string]string{
	"HUAWEI-MIB.USG6525F.oid": "declared at { fw 421 } and again at { fw 433 }",
	"HUAWEI-MIB.USG6565F.oid": "declared at { fw 423 } and again at { fw 432 }",
	"HUAWEI-MIB.USG6635F.oid": "declared at { fw 284 } and again at { fw 402 }",

	"HUAWEI-SITE-MONITOR-MIB.hwBattTestRecordsAdd.oid":      "declared twice as a NOTIFICATION-TYPE",
	"HUAWEI-SITE-MONITOR-MIB.hwBatterysInslotChange.oid":    "declared twice as a NOTIFICATION-TYPE",
	"HUAWEI-SITE-MONITOR-MIB.hwEnvHumiSensInslotChange.oid": "declared twice as a NOTIFICATION-TYPE",
	"HUAWEI-SITE-MONITOR-MIB.hwEnvTempSensInslotChange.oid": "declared twice as a NOTIFICATION-TYPE",
	"HUAWEI-SITE-MONITOR-MIB.hwPDEsInslotChange.oid":        "declared twice as a NOTIFICATION-TYPE",
	"HUAWEI-SITE-MONITOR-MIB.hwRectifiersInslotChange.oid":  "declared twice as a NOTIFICATION-TYPE",

	"UI-AF60-MIB.af60StationTable.kind": "declared as an OBJECT IDENTIFIER value and again as the OBJECT-TYPE",
}

// InstanceKey is how a divergence is looked up in
// [AdjudicatedInstances].
func InstanceKey(d Divergence) string {
	return d.Module + "." + d.Subject + "." + string(d.Field)
}

// applicationBases maps each application type the parser models onto the
// integer or string base gosmi collapses it into.
var applicationBases = map[string]string{
	"Counter32": "Unsigned32",
	"Gauge32":   "Unsigned32",
	"TimeTicks": "Unsigned32",
	"Counter64": "Unsigned64",
	"IpAddress": "OctetString",
	"Opaque":    "OctetString",
}

// oneExtraArc reports whether theirs is ours with exactly one
// sub-identifier inserted, which is the shape a label read as two arcs
// leaves behind.
func oneExtraArc(ours, theirs string) bool {
	if ours == "" || theirs == "" {
		return false
	}

	a := strings.Split(ours, ".")
	b := strings.Split(theirs, ".")
	if len(b) != len(a)+1 {
		return false
	}

	i := 0
	for i < len(a) && a[i] == b[i] {
		i++
	}

	return slices.Equal(a[i:], b[i+1:])
}

// allNumbersZero reports whether a rendered member set gives every
// member the number 0, which is the shape a discarded BITS numbering
// leaves behind.
func allNumbersZero(members string) bool {
	if members == "" {
		return false
	}

	for _, m := range strings.Split(members, " ") {
		if !strings.HasSuffix(m, "=0") {
			return false
		}
	}

	return true
}

// Projection is one module reduced to the fields of [Fields], read off
// either implementation.
type Projection struct {
	Module string
	Nodes  map[string]Subject
	Types  map[string]Subject
}

// Subject is one declaration or named type, projected.
type Subject struct {
	OID         string
	Kind        string
	Access      string
	Status      string
	TypeName    string
	BaseType    string
	Members     string
	Ranges      string
	DisplayHint string
	HasDesc     bool

	// Unresolved is what the parser marks a declaration that lost
	// something a renderer reads: a clause its kind requires, or a name
	// it needed. It is always false on gosmi's side, which has nothing
	// equivalent.
	Unresolved bool
}

// ProjectGosmi reduces a module gosmi loaded to the projection.
//
// Everything is read here rather than lazily, because the caller tears
// gosmi's module universe down as soon as it returns.
func ProjectGosmi(mod *gosmi.SmiModule) Projection {
	p := Projection{Module: mod.Name, Nodes: map[string]Subject{}, Types: map[string]Subject{}}

	for _, n := range mod.GetNodes() {
		s := Subject{
			OID:     oidString(n.Oid),
			Kind:    gosmiKindNames[n.Kind],
			Access:  gosmiAccessNames[n.Access],
			Status:  gosmiStatusNames[n.Status],
			HasDesc: n.Description != "",
		}
		if n.Type != nil {
			s.TypeName = n.Type.Name
			s.BaseType = gosmiBaseNames[n.Type.BaseType]
			s.Members = gosmiMembers(n.Type)
			s.Ranges = gosmiRanges(n.Type)
			s.DisplayHint = n.Type.Format
		}
		p.Nodes[n.Name] = s
	}

	for _, t := range mod.GetTypes() {
		p.Types[t.Name] = Subject{
			BaseType:    gosmiBaseNames[t.BaseType],
			Status:      gosmiStatusNames[t.Status],
			Members:     gosmiMembers(&t.Type),
			Ranges:      gosmiRanges(&t.Type),
			DisplayHint: t.Format,
			HasDesc:     t.Description != "",
		}
	}

	return p
}

// ProjectSMI reduces one resolved module to the projection, spelling
// every value the way gosmi spells it so the two are comparable.
func ProjectSMI(mod *smi.Module) Projection {
	p := Projection{Module: mod.Name, Nodes: map[string]Subject{}, Types: map[string]Subject{}}

	for _, n := range mod.Nodes {
		s := Subject{
			OID:        n.OID.String(),
			Kind:       n.Kind.String(),
			Access:     n.Access.String(),
			Status:     n.Status.String(),
			HasDesc:    n.Description != "",
			Unresolved: n.Unresolved,
		}
		if n.Type != nil {
			s.TypeName = n.Type.Name
			s.BaseType = gosmiBase(n.Type)
			s.Members = smiMembers(n.Type)
			s.Ranges = smiRanges(n.Type)
			s.DisplayHint = n.Type.DisplayHint
		}
		p.Nodes[n.Name] = s
	}

	for _, t := range mod.Types {
		p.Types[t.Name] = Subject{
			BaseType:    gosmiBase(t),
			Status:      t.Status.String(),
			Members:     smiMembers(t),
			Ranges:      smiRanges(t),
			DisplayHint: t.DisplayHint,
			HasDesc:     t.Description != "",
			Unresolved:  t.Unresolved,
		}
	}

	return p
}

// Compare returns every field of every subject the two projections
// disagree about, and a count per field of the places gosmi supplied
// nothing at all.
//
// The projection is the field set gosmi can supply, and a field it left
// empty for a given subject is not in it: gosmi drops the whole type of
// an object whose SYNTAX names something it could not resolve, drops a
// declaration whose macro it could not read, and leaves a node with no
// OID when it could not place the node's parent. There is nothing on its
// side to be right or wrong about in any of those, so they are counted
// as a shortfall rather than adjudicated as a disagreement.
//
// The reverse direction is a disagreement and is compared: where gosmi
// supplied a value and the parser did not, one of the two lost something
// the source said.
func Compare(ours, theirs Projection) ([]Divergence, map[Field]int) {
	shortfall := map[Field]int{}

	out := compareSubjects(ours.Module, ours.Nodes, theirs.Nodes, FieldDeclarationPresent, nodeFields, shortfall)
	out = append(out,
		compareSubjects(ours.Module, ours.Types, theirs.Types, FieldTypePresent, typeFields, shortfall)...)

	return out, shortfall
}

// nodeFields and typeFields are the fields each kind of subject carries.
// A named type has no place in the OID tree and no access, so comparing
// those would report a difference that says nothing.
var (
	nodeFields = []Field{
		FieldOID, FieldKind, FieldAccess, FieldStatus, FieldTypeName,
		FieldBaseType, FieldMembers, FieldRanges, FieldDisplayHint, FieldDescriptionPresent,
	}
	typeFields = []Field{
		FieldStatus, FieldBaseType, FieldMembers, FieldRanges, FieldDisplayHint, FieldDescriptionPresent,
	}
)

func compareSubjects(
	module string, ours, theirs map[string]Subject, presence Field, fields []Field, shortfall map[Field]int,
) []Divergence {
	var out []Divergence

	names := map[string]bool{}
	for name := range ours {
		names[name] = true
	}
	for name := range theirs {
		names[name] = true
	}

	for _, name := range slices.Sorted(keys(names)) {
		a, hasOurs := ours[name]
		b, hasTheirs := theirs[name]
		if !hasTheirs {
			shortfall[presence]++

			continue
		}
		if !hasOurs {
			out = append(out, Divergence{Module: module, Subject: name, Field: presence, Gosmi: "declared"})

			continue
		}

		for _, f := range fields {
			x, y := a.field(f), b.field(f)
			switch {
			case x == y:
			case y == "":
				shortfall[f]++
			default:
				out = append(out, Divergence{
					Module: module, Subject: name, Field: f,
					Ours: x, Gosmi: y, OursUnresolved: a.Unresolved,
				})
			}
		}
	}

	return out
}

func keys(m map[string]bool) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// field returns one projected field as the string the comparison uses.
func (s Subject) field(f Field) string {
	switch f {
	case FieldOID:
		return s.OID
	case FieldKind:
		return s.Kind
	case FieldAccess:
		return s.Access
	case FieldStatus:
		return s.Status
	case FieldTypeName:
		return s.TypeName
	case FieldBaseType:
		return s.BaseType
	case FieldMembers:
		return s.Members
	case FieldRanges:
		return s.Ranges
	case FieldDisplayHint:
		return s.DisplayHint
	case FieldDescriptionPresent:
		if s.HasDesc {
			return "present"
		}

		return ""
	default:
		return ""
	}
}

// gosmiBase spells a resolved type's base the way gosmi would if it had
// kept what the MIB wrote.
//
// The five bases the two implementations agree on map straight across.
// The application types map to their own RFC 2578 names, which gosmi's
// vocabulary has no member for, so every one of them surfaces as a
// divergence the application-type rule accounts for. That is the point:
// a silent normalization here would hide the loss the harness exists to
// measure.
func gosmiBase(t *smi.Type) string {
	if t.Enumerated() {
		return "Enum"
	}

	switch t.Base {
	case smi.BaseInteger, smi.BaseInteger32:
		return "Integer32"
	case smi.BaseUnsigned32:
		return "Unsigned32"
	case smi.BaseOctetString:
		return "OctetString"
	case smi.BaseObjectIdentifier:
		return "ObjectIdentifier"
	case smi.BaseBits:
		return "Bits"
	case smi.BaseUnknown:
		return "Unknown"
	default:
		return t.Base.String()
	}
}

var gosmiBaseNames = map[gosmitypes.BaseType]string{
	gosmitypes.BaseTypeUnknown:          "Unknown",
	gosmitypes.BaseTypeInteger32:        "Integer32",
	gosmitypes.BaseTypeOctetString:      "OctetString",
	gosmitypes.BaseTypeObjectIdentifier: "ObjectIdentifier",
	gosmitypes.BaseTypeUnsigned32:       "Unsigned32",
	gosmitypes.BaseTypeInteger64:        "Integer64",
	gosmitypes.BaseTypeUnsigned64:       "Unsigned64",
	gosmitypes.BaseTypeEnum:             "Enum",
	gosmitypes.BaseTypeBits:             "Bits",
	gosmitypes.BaseTypePointer:          "Pointer",
}

var gosmiKindNames = map[gosmitypes.NodeKind]string{
	gosmitypes.NodeUnknown:      "unknown",
	gosmitypes.NodeNode:         "node",
	gosmitypes.NodeScalar:       "scalar",
	gosmitypes.NodeTable:        "table",
	gosmitypes.NodeRow:          "row",
	gosmitypes.NodeColumn:       "column",
	gosmitypes.NodeNotification: "notification",
	gosmitypes.NodeGroup:        "group",
	gosmitypes.NodeCompliance:   "compliance",
	gosmitypes.NodeCapabilities: "capabilities",
}

var gosmiAccessNames = map[gosmitypes.Access]string{
	gosmitypes.AccessUnknown:       "unknown",
	gosmitypes.AccessNotAccessible: "not-accessible",
	gosmitypes.AccessNotify:        "accessible-for-notify",
	gosmitypes.AccessReadOnly:      "read-only",
	gosmitypes.AccessReadWrite:     "read-write",
}

var gosmiStatusNames = map[gosmitypes.Status]string{
	gosmitypes.StatusUnknown:    "unknown",
	gosmitypes.StatusCurrent:    "current",
	gosmitypes.StatusMandatory:  "mandatory",
	gosmitypes.StatusOptional:   "optional",
	gosmitypes.StatusDeprecated: "deprecated",
	gosmitypes.StatusObsolete:   "obsolete",
}

// oidString renders a gosmi OID the way the parser's own String does.
func oidString(oid gosmitypes.Oid) string {
	var b strings.Builder
	for i, sub := range oid {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(strconv.FormatUint(uint64(sub), 10))
	}

	return b.String()
}

// gosmiMembers renders a type's named numbers as a sorted
// name-equals-number list. Sorted rather than in source order because
// gosmi sorts them as it builds the type and the order it wrote them in
// is gone by the time anything can read it.
func gosmiMembers(t *gosmimodels.Type) string {
	if t == nil || t.Enum == nil {
		return ""
	}

	out := make([]string, 0, len(t.Enum.Values))
	for _, v := range t.Enum.Values {
		out = append(out, v.Name+"="+strconv.FormatInt(v.Value, 10))
	}
	slices.Sort(out)

	return strings.Join(out, " ")
}

func smiMembers(t *smi.Type) string {
	if len(t.Members) == 0 {
		return ""
	}

	out := make([]string, 0, len(t.Members))
	for _, m := range t.Members {
		out = append(out, m.Name+"="+strconv.FormatInt(m.Number, 10))
	}
	slices.Sort(out)

	return strings.Join(out, " ")
}

// gosmiRanges renders a type's range constraints sorted. gosmi folds a
// SIZE constraint into the same list, so the parser's ranges and sizes
// are folded together on its side too.
func gosmiRanges(t *gosmimodels.Type) string {
	if t == nil {
		return ""
	}

	out := make([]string, 0, len(t.Ranges))
	for _, r := range t.Ranges {
		out = append(out, strconv.FormatInt(r.MinValue, 10)+".."+strconv.FormatInt(r.MaxValue, 10))
	}
	slices.Sort(out)

	return strings.Join(out, " ")
}

func smiRanges(t *smi.Type) string {
	all := slices.Concat(t.Ranges, t.Sizes)
	if len(all) == 0 {
		return ""
	}

	out := make([]string, 0, len(all))
	for _, r := range all {
		out = append(out, strconv.FormatInt(r.Min, 10)+".."+strconv.FormatInt(r.Max, 10))
	}
	slices.Sort(out)

	return strings.Join(out, " ")
}

// SortDivergences orders divergences by module, subject and field so a
// failure lists them the same way twice.
func SortDivergences(ds []Divergence) {
	slices.SortFunc(ds, func(a, b Divergence) int {
		return cmp.Or(
			cmp.Compare(a.Module, b.Module),
			cmp.Compare(a.Subject, b.Subject),
			cmp.Compare(a.Field, b.Field),
		)
	})
}
