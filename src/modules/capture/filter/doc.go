// Package filter compiles a CaptureFilter into a classic BPF program: a
// linear instruction sequence the kernel accepts through SO_ATTACH_FILTER or
// golang.org/x/net/bpf's own VM can run directly against an already-decoded
// frame. Compile never needs an expression compiler: a CaptureFilterClause's
// populated fields are combined with AND and a CaptureFilter's clauses are
// evaluated as alternatives, combined with OR, and every comparison the
// compiler emits is a forward-only conditional jump.
package filter
