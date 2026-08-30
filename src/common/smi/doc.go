// Package smi is FlowSeer's SMIv1/SMIv2 parser and object model: the
// layer that turns MIB source into a resolved module set the code
// generator renders and a future runtime loader could read.
//
// Its defining property is what it does after the first error. Vendor
// MIBs are malformed at a rate that makes strictness useless — a parser
// that abandons a file on its first syntax error is a parser that cannot
// read the corpus this repository ships. So a malformed declaration costs
// that declaration and nothing else, and the four conditions in
// [ErrCodeUnterminatedString], [ErrCodeUnterminatedComment],
// [ErrCodeMissingModuleHeader] and [ErrCodeLimitExceeded] are the only
// ones that cost a file.
//
// # No cycle with the SNMP runtime
//
// This package imports nothing from src/common/snmp, and that is a
// contract rather than an accident of the current code. It defines its
// own OID and base-type representations so that a later model-driven
// decode path in the SNMP runtime can depend on the parser without the
// parser depending back. A dependency in that direction would close a
// cycle the moment the runtime wanted to load a MIB.
//
// # Diagnostics
//
// A [Diagnostic] is one thing wrong with a source file. It is not an
// error and does not implement the error interface: a parse that reports
// a thousand of them has not failed, and giving them the error shape
// would invite callers to return the first and stop.
//
// A diagnostic's identity is its [errs.Code], drawn from the append-only
// smi namespace and generated from the table in internal/catalog. What
// the message says, where it points, and how severely it is graded are
// all free to change; the code is not, because the code is what a
// caller's baseline pins.
//
// Severity is a grade, never a policy. The parser has no abort threshold,
// never stops early because a level was reached, and offers no knob to
// make it do so. Whether a given file's diagnostics are acceptable is the
// caller's judgment — see [Severity.NeedsBaselineReason] for the one
// obligation the scale does impose.
//
// # Conventions this package holds itself to
//
// Nothing on the raise path formats a string. [Raise] copies a fixed-size
// array of [Arg] values into the diagnostic and returns; the catalog's
// format string is applied by [Diagnostic.Render], which runs once per
// diagnostic somebody actually reads. A corpus run raises far more
// diagnostics than it renders, so the asymmetry is worth the argument
// type.
//
// Positions are byte offsets, not lines, columns, pointers or strings.
// Tokens and nodes carry an offset into the source they came from, and
// [LineTable] turns an offset into a line and column at render time. That
// keeps a token small enough to live in a slab and keeps position
// arithmetic out of the hot path.
//
// Internal packages carry the machinery, and package smi carries what a
// caller needs. The rule of thumb is that reporting a diagnostic should
// take one import and one call.
package smi
