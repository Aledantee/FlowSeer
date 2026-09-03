// Package integration drives the FlowSeer RESTCONF library against
// real servers, mirroring the SNMP library's tier model:
//
//   - yang_integration_t1: a clixon RESTCONF server in Docker. Owns
//     the protocol smoke — host-meta root discovery, reads, and edits
//     verified by read-back — through the public [restconf.Dial]
//     constructor.
//   - yang_integration_t4: opt-in live lab devices via the
//     YANG_RESTCONF_T4_TARGETS environment contract (unset = skip,
//     malformed = fail). Owns the typed identity reads and the ICX
//     reversible-edit validation.
//
// Each tier installs its own TestMain in a build-tag-guarded file.
// Both tiers skip before opening connections when -short is set.
// Bare `go test ./...` runs offline snapshot and read-back decoding checks.
package integration
