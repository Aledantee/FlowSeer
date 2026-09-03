// Package integration owns netpen's offline harness checks and opt-in lab
// suites. The default tests check the validation matrix and JSONL assertions.
// Use short mode to check either tagged suite without contacting Docker:
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
// The netpen_t2 tag checks operator-supplied environment settings and logs the
// intended attack/target pairs. Its behavioral execution and assertions remain
// unimplemented. A passing T2 run is not vendor-validation evidence.
//
// Both tiers skip when Docker is unavailable; T2 also skips when its required
// environment settings are absent. Each tag defines TestMain, so select only
// one tier per invocation. Neither live tier runs in the default tests.
//
// See VALIDATION_MATRIX.md and t2/README.md for evidence limits and operator
// prerequisites. Live runs use the Taskfile tier-t1 and tier-t2 tasks.
package integration
