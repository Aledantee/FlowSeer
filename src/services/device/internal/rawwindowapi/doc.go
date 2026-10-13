// Package rawwindowapi holds central's raw windows: the bounded periods in
// which an edge's adapter keeps the original bytes of the records a selector
// matches.
//
// One record per tenant and edge lives in the raw-windows JetStream bucket under
// the key <tenant>.<edge-id>. It holds the fence of the edge process that
// subscribed, the control revision, the sources that process runs, and every
// window with the control state central still owes the edge for it. Every
// change is one compare-and-swap, so an open admits a window and bounds the
// edge's aggregate budget against concurrent opens in one step.
//
// The package implements two Connect services over that [Store]:
//   - OperatorService (RawWindowServiceHandler): open, get, and close, after
//     capture access on the edge and an active full-payload grant.
//   - EdgeService (RawWindowEdgeServiceHandler): streams owed controls to the
//     subscribed edge process and applies its reports.
//
// A window is pending until the edge reports it. A close is recorded before it
// is delivered and stays owed until the edge reports the end or the window
// expires, so a lost delivery is resent on the stream's tick without another
// operator call.
package rawwindowapi
