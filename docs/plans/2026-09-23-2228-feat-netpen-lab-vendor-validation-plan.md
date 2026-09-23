---
title: Lab-Backed netpen Vendor Validation (OSPF / IOS-XE thin slice) - Plan
type: feat
date: 2026-09-23
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
amends: docs/plans/2026-08-23-1042-feat-netpen-port-plan.md
---

# Lab-Backed netpen Vendor Validation (OSPF / IOS-XE thin slice) - Plan

## Goal

Turn the netpen `netpen_t2` tier from a placeholder into a runner that produces
real vendor behavioral truth (VALIDATION_MATRIX source (b)) against a live lab:
for the `ospf` behavior against a Cisco IOS-XE target, inject the attack from a
Linux host and confirm on the device itself that the spoofed adjacency formed
and then cleared. The means is a build-tag-gated harness that drives injection
over SSH to the injector host running the static netpen binary, and reads the
vendor-side observable (`show ip ospf neighbor`) over SSH through the repository's
`src/protocol/ssh` library, asserting an expected finding per behavior. This
plan is wrong if netpen's fixed fixture OSPF frames cannot form an adjacency
with a stock IOS-XE OSPF interface — then vendor truth for OSPF needs the
injected frame to negotiate parameters, which is a netpen change, not a harness
change, and routes back as a blocker.

## Decisions

- The vendor-side observable is read by executing show/display commands over
  SSH, not NETCONF or SNMP. Why: every lab node speaks an SSH CLI under one
  credential (`lab`/`labIt123`), and the observable an operator checks —
  `show ip ospf neighbor` listing the attacker's router ID — is a protocol-state
  fact the CLI exposes directly, where NETCONF/SNMP would need per-vendor models
  or community configuration this lab does not carry. (User-directed 2026-09-23.)
- The vendor CLI interaction reuses `src/protocol/ssh`, not a new SSH client.
  Why: that package already drives a persistent vendor shell with prompt
  matching, `--More--` pagination, password auth (`Options.Password
  secret.Value`, `src/protocol/ssh/options.go:35-36,116-117`) and host-key
  pinning (`options.go:167`), and two captured solutions
  ([expect-style scanner](../solutions/architecture-patterns/expect-style-prompt-scanner-must-reset-its-window-per-command.md),
  [ssh prompt patterns](../solutions/architecture-patterns/ssh-prompt-patterns-need-multiline-anchors-and-must-exclude-siblings.md))
  record how to write its prompts correctly. netpen already requires the main
  module (`src/edge/netpen/go.mod` `require go.aledante.io/FlowSeer` via
  `replace`), so importing `go.aledante.io/FlowSeer/src/protocol/ssh` follows the
  established module direction; the isolation the netpen module enforces keeps
  gopacket/bubbletea out of the *main* module, not main-module packages out of
  netpen.
- The harness orchestrates from the Go test process over SSH: it SSH-execs the
  pre-deployed static netpen binary on the injector for injection, and opens a
  `src/protocol/ssh` session to the target for observation. Why: it keeps the
  work a standard `go test -tags=netpen_t2` tier runnable from a dev host or CI,
  mirroring the t1 model where the binary is pre-installed on the injector and
  driven remotely (`test/integration/t1_ae6_test.go:55-56` runs it via
  `docker exec`). (User-directed 2026-09-23.)
- The injector command is run by a plain non-interactive SSH exec via
  `golang.org/x/crypto/ssh` (a new require pulled into the netpen module with
  the `src/protocol/ssh` import — netpen does not import it today), not through
  the prompt-oriented `src/protocol/ssh` session. Why: the JSONL machine contract needs the binary's exact stdout,
  separated from stderr diagnostics, plus its exit status
  (KTD10: stdout carries only records, stderr carries progress) — a
  non-interactive exec gives all three, where a prompt session would interleave
  them and force a sentinel wrapper.
- Management and injection are separate planes. The target is reached for
  observation at its management address (routed, e.g. `172.16.0.42`); injection
  happens on the data segment where the target has an OSPF interface in
  `10.0.0.0/24`, adjacent to the injector's attack interface. Why: the netpen
  OSPF fixture sources from `10.0.0.99/24` into area `0.0.0.0`
  (`attacks/routing/helpers.go:36,38`, `attacks/routing/ospf.go` `craftOSPFHello`
  netmask `0xffffff00`), so the adjacency forms on a data link, while the CLI
  read uses whatever address the operator manages the device on.
- The target device carries an operator-supplied baseline that matches the
  fixed fixture: an interface with an address in `10.0.0.0/24`, `area 0`,
  network type broadcast, hello 10 / dead 40, no OSPF authentication. Why: the
  injected frames are fixed fixture bytes (`ospf.go` header comment: "Packet
  bytes are fixture data"; hello interval 10s, dead 40s, no auth in
  `craftOSPFHello`), so an adjacency forms only when the device's parameters
  match; the device configuration is a harness precondition, not something
  netpen negotiates. The harness verifies OSPF is up before injecting and skips
  with a clear message otherwise, as the t1 tier waits for adjacency
  (`t1_lab_test.go:48-77`).
- Vendor truth is asserted as the attacker router ID `10.0.0.99` appearing in
  the target's neighbor table, not as a `FULL` adjacency. Why: netpen sends a
  fixed hello/DBD/LSU sequence whose DBD/MTU negotiation may not drive a stock
  IOS-XE peer all the way to `FULL`; the claim source (b) must carry is that the
  device *accepted the spoofed OSPF identity*, which a neighbor-table entry
  proves. The netpen finding alone cannot prove this — `ospf.go:48` states
  "Findings describe transmitted frames, not a confirmed adjacency."
- The `netpen_t2` tier is repurposed to mean the live vendor-lab tier: it gates
  on the lab environment, not on Docker or `NETPEN_T2_IMAGE`. Why: KTD15 already
  designates t2 as the opt-in, operator-supplied Cisco-target tier
  (`docs/plans/2026-08-23-1042-feat-netpen-port-plan.md:273`); this extends it
  from a vIOS-class Docker image to a live lab rather than adding a parallel
  tier, and the Docker-image env
  (`NETPEN_T2_IMAGE`/`NETPEN_T2_TARGET`) described nothing this lab uses.
- Evidence is recorded into `VALIDATION_MATRIX.md` by hand, and the live test
  emits a matrix-ready line. Why: the matrix is hand-maintained by policy
  (`VALIDATION_MATRIX.md:88-92` "the tests do not update this file"), and
  `matrix_test.go` guards its structure; auto-writeback is a larger mechanism
  this slice does not need.
- The lab-backed vendor-validation approach is promoted to a direction record,
  because it lifts the netpen port plan's accepted limitation that vendor
  validation may land only via fixtures + t1 and it governs all future netpen
  vendor validation beyond this plan's units.

## Requirements

1. The harness reads the lab configuration from the environment and builds the
   injector command deterministically. Acceptance: with the lab variables set
   and injection interface `eth0`, the built argv for `ospf` is
   `netpen ospf -i eth0 --json=true --duration 2s --timeout 20s` (the attack
   interface flag is `-i`, `cmd/netpen/main.go:190`); with a
   required variable unset, config construction returns an error naming that
   variable.
2. The IOS-XE observable parser extracts neighbor router IDs from
   `show ip ospf neighbor`, tolerating `--More--` pagination. Acceptance:
   parsing the captured fixture that lists `10.0.0.99` yields a neighbor set
   containing `10.0.0.99`; parsing the fixture with no neighbors yields an empty
   set; a paginated capture yields the same set as its unpaginated form.
3. The IOS-XE privileged-EXEC prompt matches the target's operational prompt and
   excludes configuration mode. Acceptance: the prompt matches a buffer ending
   `\nLABRT42#` and does not match one ending `\nLABRT42(config)#`.
4. The `netpen_t2` tier gates on the lab environment, not Docker. Acceptance:
   with the lab variables unset the tier skips with a message naming the first
   missing variable; the absence of Docker on `PATH` no longer skips it.
5. The live `ospf` test asserts vendor behavioral truth and restoration.
   Acceptance (live, against IOS-XE `.42`): within the poll budget after
   injection, `show ip ospf neighbor` contains router ID `10.0.0.99`; within the
   dead interval (40 s) after the armed teardown runs, it no longer contains it.
6. The `ospf` injection is reproducible in finding-class. Acceptance: two
   injections in one run yield byte-identical `parseFindingsClass` results
   (the AE6 shape, `t1_ae6_test.go:42-48`).
7. The matrix reflects the recorded live result or an explicit pending state,
   and documents the source-(b) recording path. Acceptance: after a passing live
   run the `ospf` row's `t1 AE6` and `t2` cells no longer read `not recorded` /
   `never`; if the live run is deferred, both cells read `pending live run` and
   cite this plan, never a fabricated pass.

## Out of scope

- The other seven superset behaviors (`eigrp`, `wpad`, `etherchannel`, `mld`,
  `raflood`, `lldpspoof`, `glbp`); they stay listed and logged as pending in the
  tier so it is honest, and fan out in follow-up units once this mechanism holds.
- The physical switches on `172.16.0.0/24`, the NX-OS (`.38`), IOSvL2 (`.33`),
  Aruba (`.32`), MikroTik (`.34`), and firewall (`.51`) nodes; the same harness
  will reach them later, one observable parser per platform.
- Configuring the lab devices or the EVE-NG wiring; the baseline is
  operator-supplied and verified as a precondition, introspectable through the
  EVE-NG manager at `10.20.0.101` (admin/eve).
- Automatic write-back of `VALIDATION_MATRIX.md`, NETCONF/SNMP observables, and
  recovering credentials for `.31`/`.61`.
- The t1 containerized tier and its FRR fixture; this plan does not change them.

## Units

### U1. Direction record: netpen lab vendor validation
Files: `docs/architecture/2026-09-23-netpen-lab-vendor-validation.md`
After: none
Change: Records that netpen vendor behavioral truth (VALIDATION_MATRIX source
(b)) is obtained from a live lab — injection driven over SSH to a Linux injector
running the static binary, the vendor observable read over SSH via
`src/protocol/ssh` show-commands, one expected-finding definition per behavior —
and that this supersedes the netpen port plan's accepted limitation
(`docs/plans/2026-08-23-1042-feat-netpen-port-plan.md:273,376-377`) that vendor
validation may land only via fixtures + t1. States the tier mapping: `netpen_t1`
is containerized reproducibility and wire shape; `netpen_t2` is live vendor
behavioral truth over SSH.
Tests: none (docs).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/architecture/2026-09-23-netpen-lab-vendor-validation.md`

### U2. Lab config and SSH injector driver
Files: `src/edge/netpen/test/integration/lab/lab.go`,
`src/edge/netpen/test/integration/lab/lab_test.go`,
`src/edge/netpen/go.mod`, `src/edge/netpen/go.sum`
After: none
Change: Adds a `//go:build netpen_t2` support package `lab`. `Config` is read
from the environment — injector host, user, private-key path, and injection
interface; target management host, user, password (`secret.Value`), host-key
pin, and platform — replacing the Docker-oriented `NETPEN_T2_IMAGE`/
`NETPEN_T2_TARGET`. `InjectCommand(attack string, d, timeout time.Duration)
[]string` builds the injector argv. `RunInjector(ctx, Config, argv)` SSH-execs
the argv on the injector via `golang.org/x/crypto/ssh` and returns stdout
(JSONL), stderr, and exit code as a struct. `golang.org/x/crypto` becomes a new
direct require (it is not in netpen's graph today), so `go.mod`/`go.sum` change.
Config reading and `InjectCommand` are pure and do no I/O.
Tests: `lab_test.go` (runs in `-short`, no network): `Config` from a complete
fake environment succeeds and from one missing each required variable returns an
error naming it (R1); `InjectCommand("ospf", 2s, 20s)` equals the exact argv
(R1); host-key pin parsing accepts a `SHA256:` value and rejects a malformed one.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen/test/integration/lab/lab.go src/edge/netpen/test/integration/lab/lab_test.go src/edge/netpen/go.mod src/edge/netpen/go.sum`

### U3. IOS-XE OSPF-neighbor observable parser
Files: `src/edge/netpen/test/integration/lab/iosxe.go`,
`src/edge/netpen/test/integration/lab/iosxe_test.go`,
`src/edge/netpen/test/integration/lab/testdata/iosxe_show_ip_ospf_neighbor.txt`,
`src/edge/netpen/test/integration/lab/testdata/iosxe_show_ip_ospf_neighbor_paged.txt`,
`src/edge/netpen/test/integration/lab/testdata/iosxe_show_ip_ospf_neighbor_empty.txt`
After: none
Change: Adds, under `//go:build netpen_t2`, `ParseOSPFNeighbors(out string)
[]OSPFNeighbor` (router ID, state, address, interface) and
`HasNeighbor(neighbors, routerID) bool`, plus the `src/protocol/ssh` `Command`
and `Prompt` for IOS-XE privileged EXEC (`LABRT42#`, excluding
`LABRT42(config)#`) with the `--More--` `MorePattern`, written per the two prompt
solutions named in Decisions. Fixtures are captured `show ip ospf neighbor`
output: one listing `10.0.0.99`, one empty, one paginated.
Tests: `iosxe_test.go` (short-mode): table-driven parse of the three fixtures —
`10.0.0.99` present, absent, and present-through-pagination (R2); the prompt
matches `\nLABRT42#` and not `\nLABRT42(config)#` (R3).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen/test/integration/lab/iosxe.go src/edge/netpen/test/integration/lab/iosxe_test.go`

### U4. Live netpen_t2 OSPF-vs-IOS-XE assertion and tier rewrite
Files: `src/edge/netpen/test/integration/t2_main_test.go`,
`src/edge/netpen/test/integration/t2_superset_test.go`,
`src/edge/netpen/test/integration/t2_lab_ospf_test.go`,
`src/edge/netpen/test/integration/t2/README.md`,
`src/edge/netpen/test/integration/doc.go`
After: U2, U3
Change: `TestMain` gates on `lab.Config` (skips naming the first missing
variable) instead of Docker/`NETPEN_T2_IMAGE` (R4). `t2_lab_ospf_test.go` runs
the live cycle: open a `src/protocol/ssh` session to the target and run
`show ip ospf` to confirm OSPF is up on the injection-facing interface, skipping
with a clear message otherwise; inject via `lab.RunInjector` and assert a
well-formed `ospf` finding through the existing `parseFindingsClass`; inject a
second time and assert an identical finding-class (R6); read
`show ip ospf neighbor` and assert `10.0.0.99` present (R5, vendor truth); after
netpen's armed teardown, poll the neighbor table and assert `10.0.0.99` clears
within the 40 s dead-interval budget (R5, restoration); log one matrix-ready
evidence line for the `ospf` `t1 AE6` and `t2` cells. `t2_superset_test.go`
keeps the other seven attacks, logged explicitly as pending (not-yet-implemented)
so a pass is not mistaken for coverage, and its `TestT2ImageProvided` (which reads
the abandoned `NETPEN_T2_IMAGE`, `t2_superset_test.go:35-40`) is removed. The
implementer confirms the retained superset listing's target now comes from
`lab.Config`, since `testenv.SetTarget`/`testenv.Target()` are no longer populated
by the Docker path (`t2_main_test.go:46`, `t2_superset_test.go:19`).
`t2/README.md` and `doc.go` are rewritten
to describe the live lab tier and its prerequisites, replacing the Docker-image
text.
Tests: `t2_lab_ospf_test.go` is the live assertion (built only under
`netpen_t2`); the short-mode path asserts the env-gating and skip messages
offline (R4).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen/test/integration/t2_main_test.go src/edge/netpen/test/integration/t2_superset_test.go src/edge/netpen/test/integration/t2_lab_ospf_test.go src/edge/netpen/test/integration/t2/README.md src/edge/netpen/test/integration/doc.go`

### U5. Record evidence; amend the matrix and the netpen plan
Files: `src/edge/netpen/test/integration/VALIDATION_MATRIX.md`,
`docs/plans/2026-08-23-1042-feat-netpen-port-plan.md`
After: U4
Change: `VALIDATION_MATRIX.md` gains a section describing the live-lab source-(b)
recording path (the `netpen_t2` lab tier, its prerequisites: the required IOS-XE
OSPF baseline, the injector `setcap` grant, and the EVE-NG wiring reference at
`10.20.0.101`), and the `ospf` row's `t1 AE6` and `t2` cells are set from the
live run — or to `pending live run` citing this plan if the run is deferred (R7,
never a fabricated pass). The netpen port plan's outcome note is amended: T2
vendor validation for `ospf` is now obtainable through the lab, and the
fixtures-only limitation is lifted per the U1 direction record.
Tests: `matrix_test.go` stays green (the structure and superset-completeness
guards, `matrix_test.go:19-61`).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen/test/integration/VALIDATION_MATRIX.md docs/plans/2026-08-23-1042-feat-netpen-port-plan.md`

Waves: U1 U2 U3 | U4 | U5

## Verification

- Offline (no lab): `go -C src/edge/netpen test -race -short -tags=netpen_t2 ./test/integration/...`
  is green — config parsing, injector-argv construction, the IOS-XE parser and
  prompt, and the env-gating skip path all pass without a network. `verify-change`
  is green for every changed path.
- Live (lab): with the lab environment set and the injector prepared
  (`setcap cap_net_raw,cap_net_admin+eip` on the deployed `netpen`, or run under
  sudo) and `.42` carrying the OSPF baseline, `task -d src/edge/netpen tier-t2`
  runs `t2_lab_ospf_test.go` against `.42` and passes: the neighbor `10.0.0.99`
  appears after injection and clears after teardown, and the two runs match in
  finding-class. The operator records the `ospf` row from the emitted line.

## Definition of done

- `verify-change` green for every changed path; offline `netpen_t2` tests green
  in `-short`.
- The live `netpen_t2` `ospf` run passes against `.42`, or `VALIDATION_MATRIX.md`
  records the `ospf` cells as `pending live run` citing this plan.
- `VALIDATION_MATRIX.md`, `t2/README.md`, `doc.go`, and the netpen port plan's
  outcome note updated in this change; the U1 direction record written.
- No `R#`/`U#` labels in code, comments, or commit messages.

## Open questions

- Injector privilege: netpen needs `CAP_NET_RAW`/`CAP_NET_ADMIN` for AF_PACKET
  (`link/link_linux.go:41-54`), and the Kali `aledante` sudo password is unknown
  (`labIt123` failed for sudo). Recommended prep: a one-time
  `setcap cap_net_raw,cap_net_admin+eip` on the deployed binary so the harness
  runs it without sudo. Who performs that root action, and is `setcap` acceptable
  versus supplying a sudo credential? (operator)
- Lab baseline and wiring: `.42` needs an interface in `10.0.0.0/24`, area 0,
  broadcast, no auth, L2-adjacent to the injector's attack interface (the Kali
  `eth0` port with no IP is the candidate). The operator sets this via EVE-NG
  (`10.20.0.101`) before the live run; the harness only verifies OSPF is up. Is
  the baseline in place, and which injector interface carries the data segment?
- Adjacency state: the plan asserts neighbor presence, not `FULL`. If the fixed
  fixture reliably reaches `2-WAY`/`FULL` against IOS-XE, the implementer may
  tighten the assertion to that state; decided as presence unless the live run
  shows a stable stronger state.
