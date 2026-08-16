package snmp

import "context"

// Session is the per-target SNMP operation surface. Concrete Sessions are
// constructed by the [NewSession] function and
// are concurrency-safe (calls are serialized internally). Every
// operation takes a [context.Context] as its first argument; the
// context is never stored on the Session.
//
// Trap-send is intentionally absent — no FlowSeer actor sends
// traps. Trap reception is exposed by the [ListenTraps] function
// and [TrapStream].
type Session interface {
	// Get performs an SNMP Get for the supplied OIDs. The returned
	// [VarBind] slice has one element per requested OID, in request
	// order. Per-PDU agent failures surface as [*PDUError]; aggregated
	// failures across multiple calls compose via [errors.Join].
	//
	// Trailing [CallOption] values override session-level Timeout,
	// Retries, and MaxOIDs for the duration of this call only.
	Get(ctx context.Context, oids []OID, opts ...CallOption) ([]VarBind, error)
	// GetNext performs an SNMP GetNext for the supplied OIDs. The
	// returned VarBinds carry the lexicographically-next OIDs the agent
	// observed.
	//
	// Trailing [CallOption] values override session-level Timeout,
	// Retries, and MaxOIDs for the duration of this call only.
	GetNext(ctx context.Context, oids []OID, opts ...CallOption) ([]VarBind, error)
	// GetBulk performs an SNMP GetBulk (v2c/v3) with the given
	// non-repeater and max-repetition counts. nonRepeaters and
	// maxRepetitions follow the wire encoding (uint8); callers that
	// pass values out of range receive an error.
	//
	// Trailing [CallOption] values override session-level Timeout,
	// Retries, and MaxOIDs for the duration of this call only.
	GetBulk(ctx context.Context, nonRepeaters, maxRepetitions uint8, oids []OID, opts ...CallOption) ([]VarBind, error)
	// Walk performs an SNMPv1-compatible subtree walk rooted at root.
	// The returned [*Walker] carries any terminal error via
	// [Walker.Err]; errors are not returned alongside the Walker.
	Walk(ctx context.Context, root OID, opts ...CallOption) *Walker
	// BulkWalk performs a GetBulk-driven subtree walk rooted at root.
	// The returned [*Walker] carries any terminal error via
	// [Walker.Err].
	BulkWalk(ctx context.Context, root OID, opts ...CallOption) *Walker
	// BulkWalkRaw performs a GetBulk-driven subtree walk that yields
	// undecoded [RawVarBind]s — the fast path generated MIB bindings
	// consume. Termination guards and failure modes mirror
	// [Session.BulkWalk] exactly; sessions or responses that cannot
	// take the raw wire path degrade transparently by yielding
	// pre-decoded varbinds (RawVarBind.VB non-nil). The returned
	// [*RawWalker] carries any terminal error via [RawWalker.Err].
	BulkWalkRaw(ctx context.Context, root OID, opts ...CallOption) *RawWalker
	// Set performs an SNMP Set for the supplied VarBinds. The returned
	// slice is the agent's response, normally echoing the request.
	//
	// Trailing [CallOption] values override session-level Timeout,
	// Retries, and MaxOIDs for the duration of this call only.
	Set(ctx context.Context, vbs []VarBind, opts ...CallOption) ([]VarBind, error)
	// Close releases the Session's underlying wire resources. Close is
	// safe to call from any goroutine and is idempotent.
	Close() error
}
