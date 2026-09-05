// Package drift implements the direction record's decision 6: a managed
// field observed changed with no admitted mutation explaining it is
// resolved differently under each management mode. OPERATOR_MANAGED blocks
// the lane until an operator accepts, restores, or replaces the intent;
// AUTHORITATIVE disposes the interrupted work and queues an ordinary
// reconciliation intent instead of blocking. Both hold the last
// acknowledged intent as central's expected state.
//
// See docs/architecture/2026-09-05-verified-device-access-direction.md,
// decision 6.
package drift
