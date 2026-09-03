// Package testenv provides the shared container environment for the
// YANG protocol libraries' t1 integration tier: a
// netopeer2/sysrepo NETCONF server with the yanggen fixture modules
// installed, a clixon RESTCONF server, and FlowSeer's own gNMI
// reference target — each built or configured from a Docker context
// under testdata/ and probed for protocol-level readiness through the
// libraries' public constructors before tests run.
//
// Helpers are guarded by the yang_integration_t1 build tag so bare
// `go test ./...` never touches Docker; the always-built portion of
// this package is the fixture-copy drift gate.
package testenv
