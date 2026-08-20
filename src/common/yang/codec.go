package yang

// codec.go declares the wire-codec contracts generated bindings
// implement. One generated struct satisfies all of them, so the same
// typed value drives NETCONF XML, RESTCONF RFC 7951 JSON, and — via
// [LeafEnumerator] — gNMI path/typed-value pairs (R6). This package is
// the only API surface generated code may depend on (R7); the
// protocol libraries consume these interfaces and never import
// generated packages.

// XMLValue is a binding node that encodes to and decodes from its
// NETCONF XML representation: the sequence of child elements of the
// node's parent (for a container, one element; for a list, one
// element per entry). Implementations must produce namespace-correct
// output without external schema lookups.
type XMLValue interface {
	// MarshalYANGXML renders the node as NETCONF XML.
	MarshalYANGXML() ([]byte, error)
	// UnmarshalYANGXML replaces the node's content with the decoded
	// form of data. Unknown child elements are ignored, matching the
	// protocol rule that a client must tolerate peers with augmented
	// or newer schemas.
	UnmarshalYANGXML(data []byte) error
}

// JSONValue is a binding node that encodes to and decodes from its
// RFC 7951 JSON representation, honoring the 7951 quirks ([Value]'s
// encoding rules: 64-bit integers and decimal64 as strings, empty as
// [null], identityref with module prefix).
type JSONValue interface {
	// MarshalJSON7951 renders the node as an RFC 7951 JSON object
	// value (for a list, a JSON array of entry objects).
	MarshalJSON7951() ([]byte, error)
	// UnmarshalJSON7951 replaces the node's content with the decoded
	// form of data, ignoring unknown members.
	UnmarshalJSON7951(data []byte) error
}

// LeafEnumerator is a binding node that walks its populated leaves as
// (path, typed value) pairs — the primitive the gNMI library uses to
// build Set updates and match Subscribe notifications. Paths are
// relative to the node itself; fn returning false stops the walk.
type LeafEnumerator interface {
	VisitLeaves(fn func(Path, Value) bool)
}

// Node is the full codec surface a generated binding struct provides:
// all three wire forms from one typed value.
type Node interface {
	XMLValue
	JSONValue
	LeafEnumerator
}
