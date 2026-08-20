// Package integration drives the FlowSeer NETCONF library against
// real servers, mirroring the SNMP library's tier model (KTD10):
//
//   - yang_integration_t1: netopeer2/sysrepo in Docker with the
//     yanggen fixture modules installed. Owns the protocol smoke —
//     hello/capabilities, get-config, the candidate edit/commit
//     cycle, and the validate-failure discard path — driven through
//     the public [netconf.Dial] constructor and the committed
//     generated fixture bindings.
//   - yang_integration_t4: opt-in live lab devices via the
//     YANG_NETCONF_T4_TARGETS environment contract (unset = skip,
//     malformed = fail). Owns AE1/AE2 on IOS-XE hardware.
//
// Each tier installs its own TestMain in a build-tag-guarded file;
// selecting two tier tags at once fails to compile, by design. Bare
// `go test ./...` runs nothing here.
package integration
