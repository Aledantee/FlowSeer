// Package integration drives the FlowSeer gNMI library against real
// servers, mirroring the SNMP library's tier model:
//
//   - yang_integration_t1: FlowSeer's gNMI reference target in Docker
//     (built from the testenv context). Owns the protocol smoke —
//     Capabilities, Get, Set, Subscribe ONCE and STREAM with
//     sync_response — through the public [gnmi.Dial] constructor and
//     the committed generated fixture bindings.
//   - yang_integration_t4: opt-in live lab devices via the
//     YANG_GNMI_T4_TARGETS environment contract (unset = skip,
//     malformed = fail). Owns the typed identity read on the gNMI
//     target and the Set write-capability verdict.
//
// Each tier installs its own TestMain in a build-tag-guarded file.
// Both tiers skip before opening connections when -short is set.
// Bare `go test ./...` runs offline snapshot-restoration checks here.
package integration
