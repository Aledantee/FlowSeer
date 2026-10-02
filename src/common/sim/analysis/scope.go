package analysis

import (
	"encoding/hex"
	"strconv"
	"strings"
)

// ScopeKind identifies the shape of an evaluation scope. ScopeKind is safe for concurrent use.
type ScopeKind uint8

const (
	// ScopeWhole identifies the whole analysis. It is the zero value.
	ScopeWhole ScopeKind = iota

	// ScopeNode identifies one simulated node.
	ScopeNode

	// ScopePort identifies one port on a simulated node.
	ScopePort

	// ScopeLink identifies one link.
	ScopeLink

	// ScopeProtocol identifies one protocol instance on a simulated node.
	ScopeProtocol

	// ScopeJourney identifies one frame or packet journey.
	ScopeJourney

	// ScopeField identifies a field path beneath another scope.
	ScopeField
)

// String returns the stable lowercase name of the scope kind.
func (k ScopeKind) String() string {
	switch k {
	case ScopeWhole:
		return "analysis"
	case ScopeNode:
		return "node"
	case ScopePort:
		return "port"
	case ScopeLink:
		return "link"
	case ScopeProtocol:
		return "protocol"
	case ScopeJourney:
		return "journey"
	case ScopeField:
		return "field"
	default:
		return "scope"
	}
}

// Scope identifies the exact subject over which a [Status] applies. Its
// constructors preserve hierarchy, so a node contains its ports and protocol
// instances while sibling ports remain disjoint. The zero value is the whole
// analysis. Scope is immutable and safe for concurrent use.
type Scope struct {
	key      string
	rendered string
	kind     ScopeKind
}

// WholeScope returns the scope covering the whole analysis.
func WholeScope() Scope {
	return Scope{}
}

// NodeScope returns the scope for nodeID.
func NodeScope(nodeID string) Scope {
	return appendScope(Scope{}, ScopeNode, nodeID)
}

// PortScope returns the scope for portID on nodeID.
func PortScope(nodeID, portID string) Scope {
	return appendScope(NodeScope(nodeID), ScopePort, portID)
}

// LinkScope returns the scope for linkID.
func LinkScope(linkID string) Scope {
	return appendScope(Scope{}, ScopeLink, linkID)
}

// ProtocolScope returns the scope for a named protocol instance on nodeID.
func ProtocolScope(nodeID, protocol, instance string) Scope {
	return appendScope(NodeScope(nodeID), ScopeProtocol, protocol, instance)
}

// JourneyScope returns the scope for journeyID.
func JourneyScope(journeyID string) Scope {
	return appendScope(Scope{}, ScopeJourney, journeyID)
}

// FieldScope returns the scope for path beneath parent. Empty path elements
// remain significant so the rendered scope and canonical ordering stay lossless.
func FieldScope(parent Scope, path ...string) Scope {
	var key strings.Builder
	key.WriteString(parent.key)
	key.WriteString(strconv.Itoa(int(ScopeField)))
	key.WriteByte(':')
	for _, value := range path {
		encoded := hex.EncodeToString([]byte(value))
		key.WriteByte('v')
		key.WriteString(strconv.Itoa(len(encoded)))
		key.WriteByte(':')
		key.WriteString(encoded)
		key.WriteByte(';')
	}

	var rendered strings.Builder
	if parent.rendered != "" {
		rendered.WriteString(parent.rendered)
		rendered.WriteByte('/')
	}
	rendered.WriteString(ScopeField.String())
	rendered.WriteByte('[')
	for i, value := range path {
		if i > 0 {
			rendered.WriteByte(',')
		}
		rendered.WriteString(strconv.Quote(value))
	}
	rendered.WriteByte(']')

	return Scope{key: key.String(), rendered: rendered.String(), kind: ScopeField}
}

// Kind returns the most specific kind represented by the scope.
func (s Scope) Kind() ScopeKind {
	return s.kind
}

// Compare returns an integer comparing two scopes in canonical hierarchical order.
func (s Scope) Compare(other Scope) int {
	return strings.Compare(s.key, other.key)
}

// Contains reports whether other is equal to or nested beneath s.
func (s Scope) Contains(other Scope) bool {
	return strings.HasPrefix(other.key, s.key)
}

// Overlaps reports whether either scope contains the other. Issues on an
// ancestor affect its descendants, and issues on descendants affect an
// aggregate evaluation of their ancestor.
func (s Scope) Overlaps(other Scope) bool {
	return s.Contains(other) || other.Contains(s)
}

// String returns a deterministic representation with quoted identifiers.
func (s Scope) String() string {
	if s.key == "" {
		return ScopeWhole.String()
	}
	return s.rendered
}

func appendScope(parent Scope, kind ScopeKind, values ...string) Scope {
	var key strings.Builder
	key.WriteString(parent.key)
	key.WriteString(strconv.Itoa(int(kind)))
	key.WriteByte(':')

	var rendered strings.Builder
	if parent.rendered != "" {
		rendered.WriteString(parent.rendered)
		rendered.WriteByte('/')
	}
	rendered.WriteString(kind.String())
	rendered.WriteByte('[')
	for i, value := range values {
		if i > 0 {
			key.WriteByte(',')
			rendered.WriteByte(',')
		}
		key.WriteString(hex.EncodeToString([]byte(value)))
		rendered.WriteString(strconv.Quote(value))
	}
	key.WriteByte(';')
	rendered.WriteByte(']')

	return Scope{key: key.String(), rendered: rendered.String(), kind: kind}
}
