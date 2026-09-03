// Package testenv provides the shared container environment for the
// YANG protocol libraries' t1 integration tier: a
// netopeer2/sysrepo NETCONF server with the yanggen fixture modules
// installed, a clixon RESTCONF server, and FlowSeer's own gNMI
// reference target — each built or configured from a Docker context
// under testdata/ and probed for protocol-level readiness through the
// libraries' public constructors before tests run.
//
// Container startup helpers are guarded by the yang_integration_t1 build
// tag. Default tests check fixture-copy drift and readiness cancellation
// without Docker; tagged package tests also exercise startup cleanup with
// fake containers. Readiness is bounded by the caller's context and a
// 60-second deadline. Returned cleanup callbacks attempt termination with
// an independent 60-second deadline and return any termination error.
package testenv
