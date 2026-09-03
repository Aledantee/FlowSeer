package parse

import (
	"go.aledante.io/FlowSeer/src/protocol/smi/internal/frame"
	"go.aledante.io/FlowSeer/src/protocol/smi/internal/lex"
)

// Module is one module's declarations, held in a slab per declaration
// kind.
//
// The slabs are the storage and [Module.Decls] is the order: a
// declaration is addressed by a [Ref], which is a kind and an int32, so
// nothing in the AST is a pointer and a module's nodes sit contiguously
// in memory whichever way a pass walks them. Each slab is sized from the
// frames the framer cut before any of them is filled, so a module of a
// thousand declarations costs a dozen allocations rather than a growth
// curve per kind.
type Module struct {
	Name string
	Span Span

	// Dialect is which SMI this module was read as. It is a property of
	// the module rather than of the file because one file may hold both,
	// and it is settled before any declaration is parsed: which clauses
	// an OBJECT-TYPE must carry depends on it.
	Dialect Dialect

	Decls []Ref

	ObjectTypes        []ObjectType
	ObjectIdentities   []ObjectIdentity
	ModuleIdentities   []ModuleIdentity
	TextualConventions []TextualConvention
	NotificationTypes  []NotificationType
	TrapTypes          []TrapType
	ObjectGroups       []ObjectGroup
	NotificationGroups []NotificationGroup
	ModuleCompliances  []ModuleCompliance
	AgentCapabilities  []AgentCapabilities
	ValueAssignments   []ValueAssignment
	TypeAssignments    []TypeAssignment
	Bad                []BadDecl
}

// Decl returns what every declaration carries, whichever slab ref
// addresses. A ref that points nowhere yields the zero Decl rather than
// panicking, since a ref is only as good as the pass that built it.
func (m *Module) Decl(ref Ref) Decl {
	switch ref.Kind {
	case DeclObjectType:
		return declAt(m.ObjectTypes, ref.Index, func(n ObjectType) Decl { return n.Decl })
	case DeclObjectIdentity:
		return declAt(m.ObjectIdentities, ref.Index, func(n ObjectIdentity) Decl { return n.Decl })
	case DeclModuleIdentity:
		return declAt(m.ModuleIdentities, ref.Index, func(n ModuleIdentity) Decl { return n.Decl })
	case DeclTextualConvention:
		return declAt(m.TextualConventions, ref.Index, func(n TextualConvention) Decl { return n.Decl })
	case DeclNotificationType:
		return declAt(m.NotificationTypes, ref.Index, func(n NotificationType) Decl { return n.Decl })
	case DeclTrapType:
		return declAt(m.TrapTypes, ref.Index, func(n TrapType) Decl { return n.Decl })
	case DeclObjectGroup:
		return declAt(m.ObjectGroups, ref.Index, func(n ObjectGroup) Decl { return n.Decl })
	case DeclNotificationGroup:
		return declAt(m.NotificationGroups, ref.Index, func(n NotificationGroup) Decl { return n.Decl })
	case DeclModuleCompliance:
		return declAt(m.ModuleCompliances, ref.Index, func(n ModuleCompliance) Decl { return n.Decl })
	case DeclAgentCapabilities:
		return declAt(m.AgentCapabilities, ref.Index, func(n AgentCapabilities) Decl { return n.Decl })
	case DeclValueAssignment:
		return declAt(m.ValueAssignments, ref.Index, func(n ValueAssignment) Decl { return n.Decl })
	case DeclTypeAssignment:
		return declAt(m.TypeAssignments, ref.Index, func(n TypeAssignment) Decl { return n.Decl })
	default:
		return declAt(m.Bad, ref.Index, func(n BadDecl) Decl { return n.Decl })
	}
}

func declAt[T any](slab []T, i int32, get func(T) Decl) Decl {
	if i < 0 || int(i) >= len(slab) {
		return Decl{}
	}

	return get(slab[i])
}

// newModule allocates a module's slabs from the frames it will hold.
//
// The count comes from reading each frame's head, which is two token
// comparisons and gives the exact number of each kind rather than an
// estimate. The bad slab is the one exception: a declaration turns bad
// only once its clauses are read, so its slab is sized from the frames
// the framer already could not classify.
func newModule(fm frame.Module) Module {
	var counts [numDeclKinds]int32
	declared := 0

	for _, fr := range fm.Frames {
		kind, ok := declKindOf(fr)
		if !ok {
			continue
		}
		counts[kind]++
		declared++
	}

	return Module{
		Name:               fm.Name,
		Span:               fm.Span,
		Decls:              make([]Ref, 0, declared),
		ObjectTypes:        make([]ObjectType, 0, counts[DeclObjectType]),
		ObjectIdentities:   make([]ObjectIdentity, 0, counts[DeclObjectIdentity]),
		ModuleIdentities:   make([]ModuleIdentity, 0, counts[DeclModuleIdentity]),
		TextualConventions: make([]TextualConvention, 0, counts[DeclTextualConvention]),
		NotificationTypes:  make([]NotificationType, 0, counts[DeclNotificationType]),
		TrapTypes:          make([]TrapType, 0, counts[DeclTrapType]),
		ObjectGroups:       make([]ObjectGroup, 0, counts[DeclObjectGroup]),
		NotificationGroups: make([]NotificationGroup, 0, counts[DeclNotificationGroup]),
		ModuleCompliances:  make([]ModuleCompliance, 0, counts[DeclModuleCompliance]),
		AgentCapabilities:  make([]AgentCapabilities, 0, counts[DeclAgentCapabilities]),
		ValueAssignments:   make([]ValueAssignment, 0, counts[DeclValueAssignment]),
		TypeAssignments:    make([]TypeAssignment, 0, counts[DeclTypeAssignment]),
		Bad:                make([]BadDecl, 0, counts[DeclBad]),
	}
}

// declKindOf reads a frame's head. The second report is false for the
// frames that carry no declaration at all: an IMPORTS list, and the
// macro, EXPORTS and CHOICE bodies the framer consumes so the
// declarations after them land correctly.
func declKindOf(fr frame.Frame) (DeclKind, bool) {
	switch fr.Kind {
	case frame.KindMacroInvocation:
		return macroKind(keywordAt(fr, 1)), true
	case frame.KindTrapType:
		return DeclTrapType, true
	case frame.KindValueAssignment:
		return DeclValueAssignment, true
	case frame.KindTypeAssignment:
		if keywordAt(fr, 2) == lex.KeywordTextualConvention {
			return DeclTextualConvention, true
		}

		return DeclTypeAssignment, true
	case frame.KindUnrecognized:
		return DeclBad, true
	default:
		return DeclBad, false
	}
}

func macroKind(kw lex.Keyword) DeclKind {
	switch kw {
	case lex.KeywordObjectType:
		return DeclObjectType
	case lex.KeywordObjectIdentity:
		return DeclObjectIdentity
	case lex.KeywordModuleIdentity:
		return DeclModuleIdentity
	case lex.KeywordNotificationType:
		return DeclNotificationType
	case lex.KeywordTrapType:
		return DeclTrapType
	case lex.KeywordObjectGroup:
		return DeclObjectGroup
	case lex.KeywordNotificationGroup:
		return DeclNotificationGroup
	case lex.KeywordModuleCompliance:
		return DeclModuleCompliance
	case lex.KeywordAgentCapabilities:
		return DeclAgentCapabilities
	default:
		return DeclBad
	}
}

func keywordAt(fr frame.Frame, i int) lex.Keyword {
	if i >= len(fr.Tokens) || fr.Tokens[i].Kind != lex.KindKeyword {
		return lex.KeywordNone
	}

	return fr.Tokens[i].Keyword
}
