// Package integration drives the FlowSeer SNMP library against real SNMP
// agents. Tests in this package and its subdirectories exercise the
// public [snmp.Session] / [snmp.TrapStream] / [snmp.Walker] surface end
// to end on the wire; no fake [snmp.Session] is constructed here.
//
// # Tiers
//
// Three independent tiers are gated by build tags so bare `go test ./...`
// runs zero integration tests:
//
//   - snmp_integration_t1: Net-SNMP snmpd in Docker. Owns the USM
//     auth/priv matrix and the forged-edge cases (NoSuchObject,
//     EndOfMibView, oversized OCTET STRINGs, malformed DateAndTime,
//     mid-table truncation) that real switches cannot produce on demand.
//   - snmp_integration_t2: Nokia SR Linux deployed via containerlab.
//     Owns the end-to-end dense-row collector flow and real-NOS trap
//     reception (coldStart, linkUp/linkDown via admin-state toggle).
//   - snmp_integration_t3: lextudio/snmpsim replay of committed .snmprec
//     captures. Owns vendor regression coverage; adding a new vendor is
//     a .snmprec file plus a manifest entry, no Go code.
//
// # Tag selection
//
// Each tier installs its own TestMain in a build-tag-guarded file under
// this package. Setting two tier tags at the same invocation produces a
// compile error ("multiple definitions of TestMain") — by design. There
// is no runtime guard with a friendlier message because the offending
// invocation never produces a runnable test binary; the failure surfaces
// at `go test` compile time. Always select exactly one tier tag:
//
//	go test -tags=snmp_integration_t1 ./test/integration/snmp/...
//	go test -tags=snmp_integration_t2 ./test/integration/snmp/...
//	go test -tags=snmp_integration_t3 ./test/integration/snmp/...
//
// # Dialing
//
// Every tier reaches the wire through the public [snmp.NewSession] and
// [snmp.ListenTraps] constructors. There is no backend-swap seam: the
// SNMP implementation lives in package snmp itself, and tiers point it
// at their agent via [testenv.SetTarget] / [testenv.Target].
//
// # Operator entry points
//
// See test/integration/snmp/README.md and its Taskfile.yml for
// the per-tier developer commands. Each tier owns container/lab
// lifecycle inside its TestMain so cleanup runs even on panic.
package integration
