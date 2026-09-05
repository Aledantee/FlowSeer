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
// This package imports nothing from src/protocol/snmp, and that is a
// contract rather than an accident of the current code. It defines its
// own OID and base-type representations so that a later model-driven
// decode path in the SNMP runtime can depend on the parser without the
// parser depending back. A dependency in that direction would close a
// cycle the moment the runtime wanted to load a MIB.
//
// # The resolved model
//
// [Load] names modules and search paths; [LoadFiles] names files. Both
// follow every IMPORTS clause they find, and both return one
// [ModuleSet]: the modules, the OID tree they share, and every
// diagnostic raised on the way. The set is immutable once the call
// returns, so any number of goroutines may read it, and neither entry
// point holds anything between calls — two loads of the same files
// return equal, independent sets.
//
// Nothing about the load reaches the result. Files are read in parallel
// through a bounded pool, longest first, and the modules and diagnostics
// are merged in canonical order afterwards, so the worker count and the
// order the caller named the files in change the wall clock and nothing
// else.
//
// A [Type] carries both what it is called and what it stands on:
// [Type.Name] is the textual convention or type assignment, and
// [Type.Base] is the base type every alias between them was followed
// through to. A renderer classifying an object reads both, because
// TimeStamp and TimeTicks share a base and mean different things.
// [ModuleSet.Type] looks a name up without a module qualifier, in an
// order its doc comment fixes, because which definition wins decides
// what a renderer emits.
//
// # What resolution refuses to invent
//
// INDEX, AUGMENTS and IMPLIED come back on a [Node] as the names the
// source wrote. The [Table] built over the row resolves them: each
// [IndexPart] carries the column it names and that column's type, and
// [Table.AugmentsTable] is the augmented table, whose parts the
// augmenting table inherits. A part or target nothing loaded declares
// is graded and left flagged, and the table stays in the model with its
// raw key, because a consumer that can see the table is better off than
// one that cannot.
//
// [Node.Default] draws a line one clause over. A DEFVAL comes
// back as the value it names — a number, octets, an OID, a set of bit
// names — and a descriptor comes back as the descriptor, because which
// enumeration member or which registered node it stands for is a lookup
// the caller can make and one this pass would have to invent an answer
// for when it is neither.
//
// A declaration missing a clause any renderer reads comes back marked
// [Node.Unresolved] with no type at all, rather than resolving with a
// defaulted one, because a defaulted type renders exactly as if somebody
// had written it. The same mark is what a declaration gets when a name
// it needed did not resolve, and it carries one diagnostic naming that
// reference: twenty declarations waiting on one missing name cost twenty
// diagnostics, not one per site the name appears at.
//
// The mark is the whole of the withholding. Such a declaration keeps the
// OID it was assigned and the clause values that did parse, and so does
// everything registered beneath it, because a subtree reached through an
// arc one clause short would otherwise leave the tree entirely and a
// reader could not tell it from a subtree the MIB never wrote. What the
// descendants keep is the mark and a diagnostic of their own; what they
// do not keep is any claim to be whole.
//
// Two descriptors at one OBJECT IDENTIFIER value is the one place the
// tree cannot hold both readings, since a walk arrives at one node. The
// registration goes to the declaration a subtree is read through — a
// conceptual table over a bare naming node — and the other is diagnosed
// by name, keeps its own OID and clauses, and is classified as though it
// had won, so a table declared twice across two modules does not come
// back as a set of scalars.
//
// A SYNTAX clause may restrict the enumeration of the textual convention
// it names, and what the object wrote is what it reports. What it may
// not do is renumber or rename: RFC 2579 §3.5 allows a sub-type and
// RFC 2578 §9 makes a sub-type a narrowing of the value set. A member
// the convention does not carry is diagnosed and leaves the type
// unresolved, with the members as written, because the two definitions
// decode the same wire value differently and neither is the author's.
//
// A clause that is present but damaged costs the mark too. Where a parse
// error dropped or cut short a name the declaration wrote, what is left
// is either short one name or holding a fragment of one, and a fragment
// renders exactly like a name somebody meant. The parser has already
// reported the token at the offset it stands at, so the mark arrives
// without a second diagnostic.
//
// Leniency continues here. An IMPORTS cycle is diagnosed and broken at
// the back edge rather than failing the load, since both modules still
// define everything they define. A symbol used without an IMPORTS clause
// naming it resolves against the loaded modules and is graded, since the
// corpus writes far more of those than a strict reading could afford to
// drop.
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
//
// The diagnostic types themselves are declared in internal/diag and
// re-exported here as aliases. They have to sit below package smi
// because the lexer, framer and parser raise them and this package loads
// what those produce; declaring them at the top would put package smi on
// both ends of its own import graph. An alias is the same type rather
// than a conversion, so a caller never sees the split.
package smi
