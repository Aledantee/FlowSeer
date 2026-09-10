// Package freeze implements the control-plane freeze:
// control-plane freeze on a device's lane. Gate pauses a checkpointed
// mutation's submission step while the hosting edge's own contact cannot be
// confirmed, without manufacturing a terminal disposition for work it
// merely delays, and never gates the acknowledgement barrier — an
// already-verified mutation still reaches its terminal acknowledgement
// while frozen.
//
// See docs/architecture/2026-09-05-verified-device-access-direction.md,
// the control-plane freeze.
package freeze
