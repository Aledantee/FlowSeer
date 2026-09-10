// Package lane holds the per-device admission queue the one-ordered-lane-
// per-device rule requires: one ordered FIFO per device, with priority
// applied only at admission and never again, plus the poll coalescing that
// lets two overlapping reads for the same target share one device
// round-trip. Queue assigns each admitted item a local Position distinct
// from and unrelated to flowseer.device.access.v1.MutationState's central
// Sequence — Position exists before central ever assigns a sequence, and
// some admitted items (a local identity probe) never get one.
package lane
