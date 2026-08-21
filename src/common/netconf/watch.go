package netconf

import (
	"context"

	"go.aledante.io/FlowSeer/src/common/yang"
)

// watch.go wires the shared Collection Primitives (yang.Walker /
// yang.TickWatcher) to NETCONF reads: subtree-filtered <get> per
// traversal or tick, decoded by the descriptor's XML row codec. The
// session never sees generated packages — callers hand it descriptors
// (R7).

// Walk starts a bounded traversal of desc's subtree over sess: one
// <get> under the descriptor's subtree filter, every decoded row
// yielded.
func Walk[Row any, Key comparable](ctx context.Context, sess *Session, desc yang.ListDescriptor[Row, Key]) *yang.Walker[Row] {
	return yang.NewWalker(ctx, sessionFetch(sess, desc.Path), desc.Codec.DecodeXML, 0)
}

// Watch starts a tick-diff Watcher over desc's subtree (KTD4:
// bounded-cardinality subtrees only — config trees, interface state).
// Cold start emits Added per row; ticks re-read the subtree and emit
// Added/Modified/Removed per changed row.
func Watch[Row any, Key comparable](ctx context.Context, sess *Session, desc yang.ListDescriptor[Row, Key], cfg yang.WatchConfig) *yang.TickWatcher[Row, Key] {
	return yang.NewTickWatcher(ctx, desc.Codec, sessionFetch(sess, desc.Path), desc.Codec.DecodeXML, cfg)
}

// sessionFetch adapts Session.Get to the primitive's fetch contract.
func sessionFetch(sess *Session, p yang.Path) yang.FetchFunc {
	return func(ctx context.Context) ([]byte, error) {
		return sess.Get(ctx, p)
	}
}
