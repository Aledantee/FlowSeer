// Package integration drives the FlowSeer gNMI library against real
// servers, mirroring the SNMP library's tier model (KTD10):
//
//   - yang_integration_t1: FlowSeer's gNMI reference target in Docker
//     (built from the testenv context). Owns the protocol smoke —
//     Capabilities, Get, Set, Subscribe ONCE and STREAM with
//     sync_response — through the public [gnmi.Dial] constructor and
//     the committed generated fixture bindings.
//   - yang_integration_t4: opt-in live lab devices via the
//     YANG_GNMI_T4_TARGETS environment contract (unset = skip,
//     malformed = fail). Owns AE1 on Aruba CX and the R14 Set
//     verdict.
//
// Each tier installs its own TestMain in a build-tag-guarded file;
// bare `go test ./...` runs nothing here.
package integration
