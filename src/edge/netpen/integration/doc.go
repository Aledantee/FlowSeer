// Package integration drives the netpen binary and its behaviors against
// real lab targets in containerized environments. Tests in this package
// and its subdirectories exercise the full attack runtime — leg open,
// runner dispatch, findings stream, teardown — on the wire; no mock leg
// is constructed here.
//
// # Tiers
//
// Two independent tiers are gated by build tags so bare `go test ./...`
// runs zero integration tests:
//
//   - netpen_t1: FRRouting (FRR) containers provide OSPF/EIGRP-adjacent
//     targets, plus a netpen-vs-netpen ring for Cisco-proprietary
//     protocols (DTP, VTP, MVRP, etc.) that no open-source container can
//     impersonate. Owns the AE6 superset validation matrix: each of the
//     eight superset attacks runs twice and must produce the same
//     findings class. Also owns the AE5 air-gapped smoke: the static
//     binary runs `netpen full` to completion with no Python/network.
//   - netpen_t2: operator-supplied vIOS-class Cisco image. Owns the
//     behavioral-truth validation (ground-truth source (b)). Never
//     automated: the operator provides the image and runs the tier by
//     hand. Never a gate.
//
// # Tag selection
//
// Each tier installs its own TestMain in a build-tag-guarded file under
// this package. Setting two tier tags at the same invocation produces a
// compile error ("multiple definitions of TestMain") — by design, mir
// roring the SNMP integration tier pattern (KTD15). There is no runtime
// guard with a friendlier message because the offending invocation never
// produces a runnable test binary; the failure surfaces at `go test`
// compile time. Always select exactly one tier tag:
//
//	go test -tags=netpen_t1 ./integration/...
//	go test -tags=netpen_t2 ./integration/...
//
// # Host reality
//
// t1 needs Docker (and uses containerlab when available, otherwise plain
// `docker compose`). When the Docker daemon is not running, t1 skips
// cleanly (exit 0) with a printed reason — it never fails on a host
// without Docker. t2 needs the operator's Cisco image and skips cleanly
// when it is not provided. Neither tier is in the default `go test ./...`
// run.
//
// # Operator entry points
//
// See the Taskfile `tier-t1` and `tier-t2` tasks, and VALIDATION_MATRIX.md
// for the per-attack ground-truth labels and AE6 status.
package integration
