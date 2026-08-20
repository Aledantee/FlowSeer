package restconf

import (
	"context"

	"go.aledante.io/FlowSeer/src/common/yang"
)

// watch.go wires the shared Collection Primitives (yang.Walker /
// yang.TickWatcher) to RESTCONF reads: one GET on the descriptor's
// data-resource URI per traversal or tick, decoded by the
// descriptor's RFC 7951 row codec. The session never sees generated
// packages — callers hand it descriptors (R7).

// Walk starts a bounded traversal of desc's subtree over sess.
func Walk[Row any, Key comparable](ctx context.Context, sess *Session, desc yang.ListDescriptor[Row, Key]) *yang.Walker[Row] {
	return yang.NewWalker(ctx, sessionFetch(sess, desc.Path), desc.Codec.DecodeJSON, 0)
}

// Watch starts a tick-diff Watcher over desc's subtree (KTD4:
// bounded-cardinality subtrees only). An absent resource (404) is an
// empty row set, so a subtree disappearing surfaces as Removed
// events.
func Watch[Row any, Key comparable](ctx context.Context, sess *Session, desc yang.ListDescriptor[Row, Key], cfg yang.WatchConfig) *yang.TickWatcher[Row, Key] {
	return yang.NewTickWatcher(ctx, desc.Codec, sessionFetch(sess, desc.Path), desc.Codec.DecodeJSON, cfg)
}

// sessionFetch adapts Session.Get to the primitive's fetch contract.
func sessionFetch(sess *Session, p yang.Path) yang.FetchFunc {
	return func(ctx context.Context) ([]byte, error) {
		return sess.Get(ctx, p, GetOptions{})
	}
}
