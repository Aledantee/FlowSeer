package yang

// row.go declares the row-machinery contract between generated
// bindings and the protocol libraries' Watcher/Walker primitives. A
// YANG `list` entry is a row; its identity is the list's key leaves
// (compound keys become generated composite key structs). Nested
// lists flatten — each inner entry is its own row whose key struct
// includes ancestor keys. A non-list subtree is one synthetic row
// whose identity is the subtree path.
//
// The contract is a generic struct of functions rather than an
// interface because Go interfaces cannot carry generic methods; a
// [ListDescriptor] value bundles a list's path with its codec so a
// protocol library can build subtree filters, URIs, and gNMI paths
// without schema knowledge — and without importing generated
// packages.

// RowCodec bundles the per-list functions yanggen emits for one YANG
// list: wire decoding into row structs, change detection, partial
// update, and key extraction. Key must be comparable — it is the
// Watcher's snapshot-map key.
type RowCodec[Row any, Key comparable] struct {
	// DecodeXML decodes a NETCONF XML payload containing the list's
	// entries (the reply subtree selected by the list's path) into
	// one row per entry, in document order.
	DecodeXML func(data []byte) ([]Row, error)
	// DecodeJSON decodes the RFC 7951 JSON array of list entries
	// into one row per entry, in document order.
	DecodeJSON func(data []byte) ([]Row, error)
	// Equal reports whether two rows carry identical data. The
	// Watcher's tick diff calls it once per surviving row pair.
	Equal func(a, b Row) bool
	// Merge overlays update's populated fields onto base and returns
	// the result, preserving base's fields the update did not carry —
	// the partial-state merge for gNMI Subscribe updates.
	Merge func(base, update Row) Row
	// Key extracts the row's identity: the list's key leaves,
	// including ancestor keys for flattened nested lists.
	Key func(row Row) Key
}

// ListDescriptor is the value a caller hands to a protocol library's
// Watcher or Walker constructor: one YANG list (or synthetic-row
// subtree), located by Path, decoded and diffed by Codec. yanggen
// emits one descriptor per list.
type ListDescriptor[Row any, Key comparable] struct {
	// Path locates the list itself, module-qualified, without key
	// predicates. For a synthetic-row subtree it locates the subtree
	// root.
	Path Path
	// Codec is the list's generated row machinery.
	Codec RowCodec[Row, Key]
}
