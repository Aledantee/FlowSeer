// Package yang is the shared runtime under FlowSeer's model-driven
// device-access layer: the YANG path and typed-value model, the wire
// codec contracts generated bindings implement, and the row machinery
// the collection primitives consume. It is the meeting point of three
// parties that never import each other:
//
//   - yanggen-generated binding packages (generated/go/yang/...)
//     implement the [Node] codec surface and emit [ListDescriptor]
//     values. Generated code may import ONLY this package's public
//     API — never a protocol library, never anything internal.
//   - the protocol libraries (netconf, restconf, gnmi) accept
//     [Path], [Value], and descriptor values; they never import
//     generated packages, so the full generated surface stays out of
//     every consumer's import graph.
//   - callers connect the two by handing generated descriptors to
//     protocol-library constructors.
//
// This file is the authoritative statement of the package contract;
// if a README ever disagrees, this file wins.
//
// # Paths
//
// [Path] addresses a node in a YANG data tree as module-qualified
// segments with list-key predicates. One Path renders to all three
// wire forms: [Path.SubtreeFilterXML] (NETCONF RFC 6241 subtree
// filter), [Path.RESTCONFURI] (RFC 8040 data-resource identifier),
// and [Path.String] (the gNMI string form; the gnmi library maps
// segments onto gNMI PathElem protos). [ParsePath] and
// [ParseRESTCONFURI] invert the latter two.
//
// # Values
//
// [Value] carries one typed leaf: a [Type] (base-type kind plus
// decimal64 scale and union members) and a single payload field.
// Encoding is canonical everywhere — [Value.Canonical] for XML text
// and key predicates, [Value.MarshalJSON7951] for RFC 7951 JSON with
// its quirks (64-bit integers and decimal64 as JSON strings, empty as
// [null], identityref module-prefixed, bits space-joined). Decoding
// ([ParseCanonical], [ParseJSON7951]) is strict on structure but
// lenient where devices are known to stray: the numeric kinds accept
// both the number and string JSON spellings. Union resolution is
// strictly first-match in YANG definition order, so a given input
// always resolves to the same member type.
//
// # Rows
//
// A YANG `list` entry is a row; row identity is the list's key
// leaves. [RowCodec] bundles the generated decode/equal/merge/key
// functions, and [ListDescriptor] pairs a codec with the list's
// path — the unit of currency between generated code and the
// primitives. See row.go for the flattening rules for nested lists
// and synthetic rows.
//
// # Errors
//
// Failures carry errs codes in the append-only yang/ namespace
// (errors.go). Codes are a wire contract: never renamed, never
// reused.
//
// # Concurrency
//
// Everything in this package is immutable after construction:
// [Path], [Type], [Value], and descriptor values are safe for
// concurrent use by multiple goroutines as long as callers do not
// mutate shared slices in place. Generated binding structs are plain
// data and follow the usual rule — concurrent reads are safe,
// concurrent writes need caller synchronization.
package yang
