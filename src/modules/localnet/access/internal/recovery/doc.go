// Package recovery implements the ambiguity rule: ambiguity
// stays indeterminate. Runner observes a mutation's affected state before
// every retry attempt and authorizes a retry only after a device-native
// fence succeeds or after repeated fresh observations across the
// delayed-apply horizon show the state unchanged; failing both within the
// horizon, it abandons the mutation. Hold is the per-device latch an
// abandonment engages: the lane stays blocked for that device until an
// explicit resolution call clears it, never a retry.
//
// See docs/architecture/2026-09-05-verified-device-access-direction.md,
// "Ambiguity stays indeterminate".
package recovery
