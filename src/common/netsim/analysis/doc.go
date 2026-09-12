// Package analysis defines domain-neutral trust metadata for network simulation results.
//
// Input validity is separate from result status: invalid input is rejected by
// the producing capability, while a valid input may yield an incomplete,
// exhausted, unstable, or unsupported analysis. Issues localize reduced trust
// to immutable scopes and retain producer-owned codes. Metadata and evidence
// catalogs use immutable snapshots and are safe for concurrent use.
//
// Execution stop reasons and domain outcomes belong to the simulator or
// capability that produces them, not to this package.
package analysis
