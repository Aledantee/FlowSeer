// Package integration owns netpen's offline harness checks and opt-in lab
// suites. The default tests check the validation matrix and JSONL assertions.
// Use short mode to check either tagged suite without contacting Docker or the
// live lab:
//
//	go -C src/edge/netpen test -race -short -tags=netpen_t1 ./test/integration
//	go -C src/edge/netpen test -race -short -tags=netpen_t2 ./test/integration
//
// # Live tiers
//
// The netpen_t1 tag starts two FRR containers through docker compose, waits for
// OSPF adjacency, and runs the binary installed in r1. AE5 checks full-command
// completion; AE6 compares record kinds and finding modules across two runs.
// The supplied FRR image does not include netpen: install the binary before a
// live run. Missing binaries and command failures fail these tests. The ring
// fixture has no executing capture/decoder test, so this suite does not prove
// wire shape or vendor behavior. Static linking is checked separately on the
// local release artifact; the suite does not establish an air gap.
//
// The netpen_t2 tag drives OSPF injection through a Linux host over SSH and
// checks the IOS-XE target's own neighbor table. A pass proves that router ID
// 10.0.0.99 appeared and cleared after teardown. The injector runs netpen under
// sudo with a password supplied through the lab environment. The seven other
// superset attacks remain explicit pending entries and supply no evidence.
//
// T1 skips when Docker is unavailable. T2 skips when its required lab settings
// are absent or the target has no active OSPF process. Each tag defines TestMain,
// so select only one tier per invocation. Neither live tier runs in the default
// tests.
//
// See VALIDATION_MATRIX.md and t2/README.md for evidence limits and operator
// prerequisites. Live runs use the Taskfile tier-t1 and tier-t2 tasks.
package integration
