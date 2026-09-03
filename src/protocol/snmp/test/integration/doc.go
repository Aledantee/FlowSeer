// Package integration checks the SNMP library and generated MIB bindings.
// Default tests use in-process sessions and trap streams to verify harness
// assertions, manifest validation, and generated row presence and BITS decoding.
//
// Four opt-in tiers exercise the public snmp.Session, snmp.TrapStream, and
// snmp.Walker APIs against external agents:
//
//   - snmp_integration_t1: Net-SNMP snmpd in Docker provides a USM matrix,
//     forged wire shapes, dense-row walks, and a subset of Watch scenarios.
//   - snmp_integration_t2: Nokia SR Linux via containerlab provides dense-row
//     walks and linkUp/linkDown trap reception. Cold-start and NOS-sourced v3
//     trap tests remain placeholders.
//   - snmp_integration_t3: snmpsim replays committed .snmprec fixtures, including
//     wrong-type values checked through the generated walker's fallback decoder.
//   - snmp_integration_t4: operator-supplied devices provide scalar and table
//     regression checks. This tier does not provision or modify those devices.
//
// Select exactly one tier tag; each defines TestMain, so selecting two produces
// a compile error. Short mode runs offline tests and skips external setup,
// regardless of installed tools or configured targets. For example:
//
//	go test -race -short -tags=snmp_integration_t1 ./src/protocol/snmp/test/integration
//
// Without -short, T1 through T3 own container startup and normal teardown in
// TestMain. T2 changes an interface's admin state in its ephemeral lab and
// registers an enable cleanup before disabling it. T4 requires SNMP_T4_TARGETS.
// See README.md and Taskfile.yml for the live tier commands and prerequisites.
package integration
