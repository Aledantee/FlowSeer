package parse

import (
	"strconv"

	"go.aledante.io/FlowSeer/src/common/smi/internal/frame"
)

// Span is a half-open byte range in the source a node was read from. It
// is the only way a node refers to text: no strings, no pointers, no
// line numbers. Line and column come from the file's line table when a
// diagnostic is rendered, and a clause's text is materialized by
// [Result.Text] or [Result.StringValue] when somebody reads it.
type Span = frame.Span

// DeclKind classifies a declaration.
//
// [DeclBad] is the zero value because an unset kind and a declaration
// nobody could parse have to mean the same thing: a declaration is never
// representable as simply absent, so the kind a consumer reads out of an
// empty slot is the one that says "this did not resolve".
type DeclKind uint8

// The declaration kinds, one per macro the SMI RFCs define plus the two
// plain assignment forms.
const (
	DeclBad DeclKind = iota
	DeclObjectType
	DeclObjectIdentity
	DeclModuleIdentity
	DeclTextualConvention
	DeclNotificationType
	DeclTrapType
	DeclObjectGroup
	DeclNotificationGroup
	DeclModuleCompliance
	DeclAgentCapabilities
	DeclValueAssignment
	DeclTypeAssignment
	numDeclKinds
)

// declKindNames are what a kind renders as in a diagnostic, which for
// the macros is the macro's own spelling: a reader who sees the message
// is looking at that word in the source.
var declKindNames = [...]string{
	DeclBad:               "declaration",
	DeclObjectType:        "OBJECT-TYPE",
	DeclObjectIdentity:    "OBJECT-IDENTITY",
	DeclModuleIdentity:    "MODULE-IDENTITY",
	DeclTextualConvention: "TEXTUAL-CONVENTION",
	DeclNotificationType:  "NOTIFICATION-TYPE",
	DeclTrapType:          "TRAP-TYPE",
	DeclObjectGroup:       "OBJECT-GROUP",
	DeclNotificationGroup: "NOTIFICATION-GROUP",
	DeclModuleCompliance:  "MODULE-COMPLIANCE",
	DeclAgentCapabilities: "AGENT-CAPABILITIES",
	DeclValueAssignment:   "OBJECT IDENTIFIER",
	DeclTypeAssignment:    "type assignment",
}

// String returns the kind's name, or "kind(N)" for a value off the set.
func (k DeclKind) String() string {
	if int(k) >= len(declKindNames) {
		return "kind(" + strconv.Itoa(int(k)) + ")"
	}

	return declKindNames[k]
}

// Ref addresses one declaration: which slab it lives in and where. A
// module's declaration order is a slice of these, so walking the file in
// source order costs no indirection and a node costs no header.
type Ref struct {
	Kind  DeclKind
	Index int32
}

// Decl is what every declaration carries whatever its macro.
//
// Name is a string rather than a span because it is the key every later
// pass looks a declaration up by, and because the lexer already interned
// it: the field costs a word and no allocation, while re-materializing a
// descriptor at every lookup would cost one each time.
//
// Present names the clauses that actually parsed. It is what tells a
// declaration that kept its OID but lost a required clause apart from
// one that is whole, so nothing downstream can render the first as if it
// were the second.
type Decl struct {
	Name    string
	Span    Span
	Present ClauseSet
}

// ClauseText is one clause kept as the source it covers. A bad
// declaration carries these so that everything which did parse survives
// the one thing that did not.
type ClauseText struct {
	Clause Clause
	Span   Span
}

// BadDecl is a declaration whose macro's required clauses are not all
// present.
//
// It is not a discarded declaration. It keeps its name, its span, every
// clause that parsed and the set that did not, so a consumer sees it as
// unresolved rather than missing and a resolution pass can say which
// dependents fell with it.
type BadDecl struct {
	Decl

	// Intended is the macro the head named, or [DeclBad] when the framer
	// could not classify the head at all.
	Intended DeclKind

	// Missing is the required clauses that never arrived.
	Missing ClauseSet

	// Clauses is every clause that did parse, in source order.
	Clauses []ClauseText
}

// ObjectType is an OBJECT-TYPE declaration (RFC 2578 §8, RFC 1212 §4).
//
// Syntax and Defval hold the source each clause covers, and SyntaxType
// and DefaultValue hold what that source says. Both are here because a
// consumer wants the value and a diagnostic wants the file's own words.
//
// Access holds an SMIv1 ACCESS clause and MaxAccess an SMIv2 MAX-ACCESS
// one. They are separate fields because the two spell inverted
// semantics, and collapsing them here would destroy the distinction the
// dialect pass needs.
type ObjectType struct {
	Decl
	Syntax Span

	// SyntaxType is the same clause read as a type: its base type, its
	// enumeration or BITS members with the numbers the source declared,
	// and its range and SIZE constraints.
	SyntaxType Type

	// MappedSyntax is the SMIv2 type the SYNTAX clause's base type is
	// equivalent to when that base type is one of SMIv1's, and empty
	// otherwise. Syntax keeps the source spelling either way, so a
	// consumer that wants the modern type reads this and a diagnostic
	// that quotes the file still quotes the file.
	MappedSyntax string

	Units       Span
	MaxAccess   Span
	Access      Span
	Status      Span
	Description Span
	Reference   Span
	Index       Index
	Augments    Span
	Defval      Span

	// DefaultValue is the DEFVAL clause read per RFC 2578 §7.9.
	DefaultValue Value

	Assignment Span
}

// Index is an INDEX clause held exactly as written.
//
// Nothing here is resolved, and that is deliberate rather than pending:
// the code generator never reads index structure and index decoding is
// generic at runtime, so resolving a column name to a type would buy a
// pass nobody reads.
type Index struct {
	Span  Span
	Parts []IndexPart
}

// IndexPart is one index column and whether IMPLIED preceded it.
type IndexPart struct {
	Name    Span
	Implied bool
}

// ObjectIdentity is an OBJECT-IDENTITY declaration (RFC 2578 §7).
type ObjectIdentity struct {
	Decl
	Status      Span
	Description Span
	Reference   Span
	Assignment  Span
}

// ModuleIdentity is a MODULE-IDENTITY declaration (RFC 2578 §5).
type ModuleIdentity struct {
	Decl
	LastUpdated  Span
	Organization Span
	ContactInfo  Span
	Description  Span
	Revisions    []Revision
	Assignment   Span
}

// Revision is one REVISION clause and the DESCRIPTION that follows it.
type Revision struct {
	Span        Span
	Date        Span
	Description Span
}

// TextualConvention is a TEXTUAL-CONVENTION type assignment (RFC 2579
// §3).
type TextualConvention struct {
	Decl
	DisplayHint Span

	// Hint is the DISPLAY-HINT clause read per RFC 2579 §3.1.
	Hint DisplayHint

	Status      Span
	Description Span
	Reference   Span
	Syntax      Span

	// SyntaxType is the SYNTAX clause read as a type, carrying the
	// numbers a BITS or enumerated type declared for its members.
	SyntaxType Type
}

// NotificationType is a NOTIFICATION-TYPE declaration (RFC 2578 §11).
type NotificationType struct {
	Decl
	Objects     []Span
	Status      Span
	Description Span
	Reference   Span
	Assignment  Span
}

// TrapType is an SMIv1 TRAP-TYPE declaration (RFC 1215).
//
// Assignment holds the trap number rather than an OID, because that is
// what the macro assigns; turning it into the notification OID the SMIv2
// tree wants is the dialect pass's job.
type TrapType struct {
	Decl
	Enterprise  Span
	Variables   []Span
	Description Span
	Reference   Span
	Assignment  Span

	// Notification is the notification OID this trap denotes, derived
	// from the enterprise and the trap number by the dialect pass.
	Notification NotificationOID
}

// ObjectGroup is an OBJECT-GROUP declaration (RFC 2580 §3).
type ObjectGroup struct {
	Decl
	Objects     []Span
	Status      Span
	Description Span
	Reference   Span
	Assignment  Span
}

// NotificationGroup is a NOTIFICATION-GROUP declaration (RFC 2580 §4).
type NotificationGroup struct {
	Decl
	Notifications []Span
	Status        Span
	Description   Span
	Reference     Span
	Assignment    Span
}

// ModuleCompliance is a MODULE-COMPLIANCE declaration (RFC 2580 §5).
type ModuleCompliance struct {
	Decl
	Status      Span
	Description Span
	Reference   Span
	Modules     []ComplianceModule
	Assignment  Span
}

// ComplianceModule is one MODULE clause of a compliance statement. Name
// is empty when the clause names no module, which is how a statement
// refers to the module it sits in.
type ComplianceModule struct {
	Span            Span
	Name            Span
	MandatoryGroups []Span
	Refinements     []Refinement
}

// RefinementKind tells a GROUP refinement from an OBJECT one. The two
// carry different clause sets and mean different things, and the
// keyword is the only place that difference is written down.
type RefinementKind uint8

// The refinement kinds, named for the keyword each is written with.
const (
	RefineGroup RefinementKind = iota
	RefineObject
)

// Refinement is one GROUP or OBJECT clause inside a MODULE clause.
type Refinement struct {
	Span        Span
	Kind        RefinementKind
	Name        Span
	Syntax      Span
	WriteSyntax Span
	MinAccess   Span
	Description Span
}

// AgentCapabilities is an AGENT-CAPABILITIES declaration (RFC 2580 §6).
type AgentCapabilities struct {
	Decl
	ProductRelease Span
	Status         Span
	Description    Span
	Reference      Span
	Supports       []Supported
	Assignment     Span
}

// Supported is one SUPPORTS clause: the module it names, the groups it
// includes and the variations it declares.
type Supported struct {
	Span       Span
	Module     Span
	Includes   []Span
	Variations []Variation
}

// Variation is one VARIATION clause.
//
// Object variations and notification variations share this shape because
// the grammar does: which clauses a variation may carry follows from
// what the name it refines turns out to be, and that is not known until
// the name is resolved.
type Variation struct {
	Span             Span
	Name             Span
	Syntax           Span
	SyntaxType       Type
	WriteSyntax      Span
	Access           Span
	CreationRequires []Span
	Defval           Span

	// DefaultValue is the VARIATION default, which RFC 2580 §6 writes
	// with the same production DEFVAL uses.
	DefaultValue Value

	Description Span
}

// ValueAssignment is a "<descriptor> OBJECT IDENTIFIER ::= { parent n }"
// declaration, the corpus's most common one. Assignment holds the brace
// group unparsed; turning it into an OID is resolution's job, since the
// parent is a name this file may not define.
type ValueAssignment struct {
	Decl
	Assignment Span
}

// TypeAssignment is a "Foo ::= <type>" declaration that is not a textual
// convention: a SEQUENCE row type, a constrained base type, or an alias.
// Syntax is the whole right-hand side as written.
type TypeAssignment struct {
	Decl
	Syntax Span

	// SyntaxType is the same right-hand side read as a type.
	SyntaxType Type
}
