// Package integration checks NETCONF protocol behavior against container and
// lab servers. Untagged tests validate the harness with local fixtures.
// Compile and skip either external tier without starting an environment:
//
//	go test -short -tags yang_integration_t1 ./src/common/netconf/test/integration
//	go test -short -tags yang_integration_t4 ./src/common/netconf/test/integration
//
// Run from the repository root. Each tier owns its server prerequisites:
//
//   - yang_integration_t1: netopeer2/sysrepo in Docker with the
//     yanggen fixture modules installed. Owns the protocol smoke —
//     hello/capabilities, get-config, the candidate edit/commit
//     cycle, and the validate-failure discard path — driven through
//     the public [netconf.Dial] constructor and the committed
//     generated fixture bindings.
//   - yang_integration_t4: opt-in live lab devices via the
//     YANG_NETCONF_T4_TARGETS environment contract (unset = skip,
//     malformed = fail). Owns the typed identity read and the
//     invalid-edit rollback proof on IOS-XE hardware.
//
// Each tier installs its own TestMain; select only one tier tag per invocation.
// Short mode exits before Docker setup or live-target parsing. Malformed target
// errors report entry positions without credentials. Live write tests require
// their named fixture users to be absent, register cleanup before attempting an
// edit, and report cleanup failures. Lab operators must approve writes before
// running the live tier; setting its target environment selects the devices.
package integration
