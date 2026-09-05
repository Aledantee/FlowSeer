// Package evidence holds the route-evidence cache the lane consults before
// trusting a route to complete an operation without a fresh read: which
// route last answered one device, under one firmware fingerprint, for one
// operation kind, how completely, and until when that answer may still be
// trusted. A firmware-epoch change invalidates every entry recorded under
// the previous fingerprint, per
// docs/architecture/2026-09-05-verified-device-access-direction.md, decision
// 7. An operation kind with no configured lifetime has no evidence policy
// at all and blocks mutation rather than trusting an unbounded cache entry.
package evidence
