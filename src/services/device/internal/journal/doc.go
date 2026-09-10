// Package journal is central's durable record of every device's lane, in a
// JetStream key-value bucket, one key per device. Its heart is the owed-row
// derivation ([OwedRows]): what central owes an edge is a pure function of
// the record, so any replica computes the same thing after any restart, and
// the outbox is a relay of that function rather than a second source of
// truth. Every write is a compare-and-set on one device's record, which is
// what lets one writer per device hold across central replicas without a
// lease. See docs/architecture/2026-09-05-verified-device-access-direction.md
// for the checkpoint barrier, and src/services/device/README.md for the
// owed-row policy.
package journal
