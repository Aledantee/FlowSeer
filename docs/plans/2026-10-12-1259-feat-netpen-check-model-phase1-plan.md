---
title: netpen Check Model Phase 1, Contracts - Plan
type: feat
date: 2026-10-12
artifact_contract: flowseer-plan/v2
execution: code
amends: docs/architecture/2026-09-23-netpen-lab-vendor-validation-direction.md
---

# netpen Check Model Phase 1, Contracts - Plan

## Goal

Every package under `src/edge/netpen` compiles against the check contract:
a behavior returns a verdict and reads a typed target, the catalog declares
impact, restore, and blast radius, the runner enforces an impact ceiling,
a rate limit, and an observation window, and the command exposes them.
Every existing behavior runs under the new contract, returning `pending`
where it has no observable yet. The means: three additive units that keep
the module building, then one cutover unit that changes the signature in
the runner, the four attack packages, `full`, and `cmd` at once, since a
signature change across all of them cannot land in parts with the build
green between them.

**Stop condition:** the fixture pins cannot be held by passing the fixture
addresses as the target, because a behavior derives a frame field from
something other than the target or the leg address. The migration unit then
needs a per-behavior fixture seam the parent did not decide.

## Decisions

The parent plan's Decisions apply. Local to this phase:

- A new leaf package `src/edge/netpen/check` owns the contract types and
  imports only `findings` and `link`: `Entry` (name, mode, protocols, legs,
  impact, restore, affects, help, orchestration flag), `Impact`,
  `Restore`, `Affects`, `Target`, `Verdict`, `Deps`, an `Emitter`
  interface (`Emit`, `Finding`, `Progress`), a `Teardown` interface
  (`Arm`), and the `Run` function type. `catalog` imports `check` and adds
  `Behavior` (an `Entry` plus `Run` and `Modes`) and the `Catalog` value
  built by `catalog.New(...)`. `runner` imports both and implements the two
  interfaces. The shipped set is `attacks.Default()` in a new package
  `src/edge/netpen/attacks`, since `catalog` cannot import the family
  packages that import it. Why: today `Deps` names `catalog.Entry`,
  `*runner.Emitter`, and `*runner.Teardown`
  (`src/edge/netpen/runner/options.go:110-148`), so a `check.Deps` naming
  those concrete types while `catalog.Behavior.Run` took `check.Deps`
  would be an import cycle. `runner.go:294` fails at run time when
  `Options.Behaviors` lacks a name the catalog has, and `catalog.go:143`
  latches a global registry so an embedder cannot compose.
- `Impact` is one of `none`, `temporary`, `permanent`. `Restore` is a
  struct with a `Kind` (`none`, `decay`, `active`) and a `Bound` duration
  that is zero when no source states one. `Affects` is one of `none`,
  `device`, `clients`, `fabric`. Why: most teardown text today names no
  timer ("CAM table ages out", "NUD / real master resumes",
  `src/edge/netpen/catalog/registrations.go:23-48`), and inventing one is
  a claim about a device the plan cannot cite. The runner announces
  `decay bound unknown` for a zero bound, as `decayAnnouncement` does for
  empty text today (`src/edge/netpen/runner/durability.go:129-131`).
- The bounds with a source are the lifetimes the probe itself announces,
  read from the frame the behavior sends: `roguera` and `roguedhcp` 1800 s,
  `roguedhcp6` 300 s, `stproot` 6 s (the max age its BPDU carries), `wpad`
  and `llmnr` 30 s (the TTL in the answer). Every other `decay` entry
  carries a zero bound.
- `Verdict` is a struct with a `Kind`, a `Detail` string, and an `Observed`
  duration, with kinds `confirmed`, `resisted`, `pending`, `skipped`.
  Why: three of the four exist as rollup kinds
  (`src/edge/netpen/findings/findings.go:47-56`), and `confirmed` is what
  `full/full.go:312` records as a `KindFinding` verdict today. They fold
  into one record kind `verdict`.
- `Target` has one field per value that names a host, address, group, or
  identity on the network: `Victim`, `Gateway`, `Victim6`, `Server6` (each
  an IP and a MAC), `VirtualIP`, `Group`, `VRID`, `OuterVLAN`,
  `InnerVLAN`, `VoiceVLAN`, `VTPDomain`, `RouterID`, `Prefix6`, `Route4`,
  `OfferIP`, and `SweepNet`. The attacker's own addresses come from
  `Leg.Addr()`. A frame field that only has to be well-formed (a DNS
  transaction ID, a DHCP XID, the MAC stamp of a CAM-flood frame) is probe
  content and stays a constant in the behavior. The cutover classifies
  each of today's constants by that rule, per behavior, in its doc
  comment. Why: the constants under `attacks/*/helpers.go` and the
  per-file literals (`attacks/ip6/helpers.go:32-40`,
  `attacks/ip6/roguedhcp.go:43`, `attacks/l2/portsteal.go:38`,
  `attacks/routing/eigrp.go:257`) hold values one `Victim.IP` cannot, so a
  rule, not a list, decides the edge cases.
- Each behavior test builds its own target from the constants it pins
  today, through a `fixtureTarget()` in its package's test file. Why:
  `arpspoof` pins victim `172.16.0.10` (`attacks/fh/helpers.go:41-46`),
  `llmnr` pins `10.0.0.2`, and the IPv6 suite pins `fd00::10`, so one
  shared target cannot hold them.
- `Target.Need(fields ...Field) error` returns one coded error naming every
  missing field, with `ErrCodeTargetMissing` and exit code 2. The runner
  maps it to a `skipped` verdict and, when `Options.Orchestrated` is
  false, returns it from `Run` so the command exits 2. Any other error
  from a behavior becomes a `pending` verdict carrying the error text, and
  the run continues. Why: parent requirement 7 wants exit 2 on a single
  command, and under `full` a behavior whose target recon could not fill
  is a skipped check, not a run failure.
- A ceiling refusal emits the `refusal` record and continues to the next
  attack. A missing acknowledgment keeps today's behavior and stops the
  run with the coded error (`src/edge/netpen/runner/runner.go:277-292`).
  Why: a ceiling states what the operator asked for, so refusing an entry
  above it is the run working as asked. A missing acknowledgment on an
  entry the operator named is a mistake to stop on.
- `link.Leg` gains `Addr() Addr` carrying the MAC and the IPv4 and IPv6
  addresses, read once at open from `net.InterfaceByName`. Why:
  every attack package has a `srcMAC()` returning the fixture value because
  the leg exposes nothing (`attacks/l2/dtp.go:106`).
- The linux leg keeps one lifecycle mutex for `closed` and the fd and adds
  a write mutex. `readOnce` takes the lifecycle mutex only for the closed
  check and a wait-group increment, releases it, calls `ReadPacketData`,
  and decrements. `Close` sets `closed` under the lifecycle mutex, waits
  for the wait group, then takes the write mutex and calls `tp.Close()`.
  `Send` takes the write mutex and checks `closed` under it. Why:
  `src/edge/netpen/link/link_linux.go:201-210` holds the lock across the
  100 ms poll and `Send` at line 68 waits on it, and afpacket's `Close`
  writes the fd with no lock
  (`~/go/pkg/mod/github.com/gopacket/gopacket@v1.7.3/afpacket/afpacket.go:243-254`)
  while `WritePacketData` reads it (`:567-569`), so a write path with no
  ordering against `Close` races on the fd.
- The rate limit is a `link.Leg` wrapper in `runner` that spaces sends by
  `time.Second / rate` on a clock the test replaces, handed to the behavior
  as `Deps.AttackLeg`. No module is added, and `stproot` drops its own
  pacing (`src/edge/netpen/attacks/l2/stproot.go:61-66`) so nothing paces
  twice. Why: one enforcement point for every behavior, and a token bucket
  is more than a packet pacer needs.
- The runner emits the verdict record itself after `Run` returns, stamped
  with the attack and mode. Why: a behavior cannot forget to emit.
- `SignalHandler` moves to `cmd/netpen`. The runner exports
  `AbandonedSteps` (today `abandonedSteps`,
  `src/edge/netpen/runner/runner.go:549`) and `StopContext` (today the
  unexported `stopCtx` that `signals.go:72` and `:120` read). Why:
  `src/edge/netpen/runner/signals.go:140` calls `os.Exit` from a library
  package.
- `Options.SuppressOutput` is deleted. Why: `runAttack`
  (`cmd/netpen/main.go:382`) and `full.runPhase` (`full/full.go:245`) set
  it and nothing reads it.
- Phase 1 removes teardown arming on the permanent modes (`dtp
  --keep-trunk`, `vlanhop --persist`, `voicevlan --persist`) and leaves the
  no-op steps of the active-restore entries (`arpspoof`, `etherchannel`,
  `portsteal --relay`, `vlanhop`, `voicevlan`) in place with the verdict
  detail naming them. Parent requirement 8 is phase 2's. Why: a teardown
  that does host work needs the sysctl and netlink seams phase 2 decides,
  and a permanent mode with a nil `Deps.Teardown` would otherwise panic on
  `Arm` (`attacks/l2/vlanhop.go:44`, `attacks/l2/voicevlan.go:43`).
- The lab vendor validation record is amended where it says "fixed
  fixture", and the T1 and T2 harness commands pass the flags the
  behaviors now need. Why: `test/integration/lab/lab.go:113-116` and
  `test/integration/t2_lab_ospf_test.go:70` run the command with no
  target, which after the cutover exits 2, and the build tags hide that
  from `go test ./...`.
- The `-race` runs drop `CGO_ENABLED=0`. Why: `go test -race` needs cgo on
  Linux, and the Taskfile sets it for the static release build only.
- `ackList.Set` rejects an entry without `name=mode`. Why: `main.go:97`
  accepts a bare mode that can never match a gate key.
- The WPAD listener comes from `Deps.Listen`, a `func(ctx, network,
  address) (net.Listener, error)` defaulting to `net.Listen`, and the test
  passes an in-memory listener. Why: the suite fails wherever port binding
  is denied.

## Requirements

Parent requirements 1 to 7, 9 to 11, 13, 14, and 15 are claimed here.
Local examples that pin the shape:

1. `attacks.Default().Entries()` is byte-stable and the generator commits
   it. Example: `go generate ./catalog/...` produces no diff against
   `zz_generated_catalog.go` on a clean tree, and the oracle test in
   `attacks/attacks_test.go` names all 40 (behavior, mode) pairs (34
   behaviors and 6 modes) with their three cost fields.
2. The JSONL schema version is 2 and the `verdict` kind exists. Example:
   `findings_test.go` marshals a verdict record and asserts after
   unmarshal, field by field, `schema_version` 2, `kind` `verdict`,
   `attack` `ghost`, and a `verdict` object with `kind` `resisted`,
   `detail` `0 frames in 3s`, and `observed_ms` 3000.
3. The gate order is ceiling, then acknowledgment, then legs. Example: a
   permanent entry under `MaxImpact: temporary` with a valid ack is refused
   with reason `impact permanent exceeds ceiling temporary`, the test leg
   records zero sends for it, and the next attack in the list runs.
4. The runner arms a teardown handle only when `Restore.Kind == active`.
   Example: a behavior registered with `Restore: decay 6s` receives a nil
   `Deps.Teardown`, and a behavior with `Restore: active` that arms no step
   still fails with `ErrCodeTeardownPartial`.
5. The migrated behaviors keep their fixture pins. Example: every existing
   `*_FixturePins` test passes with `Deps.Target` built by its package's
   `fixtureTarget()` and the test leg's `Addr()` returning
   `00:11:22:33:44:55` and the attacker IPv4 the family pins today.
7. A missing target is a skipped verdict and exit 2. Example: `netpen
   arpspoof` with no flags emits a `verdict` record with kind `skipped`
   and detail naming `victim` and `gateway`, exits 2, and the leg records
   zero sends. The same behavior under `full` with an empty target emits
   the same record and the run continues.
6. A behavior without an observable returns `pending` with a reason.
   Example: `RunHSRP` returns `Verdict{Kind: pending, Detail: "no
   observable: coup acceptance needs a hello listener"}`, and
   `VALIDATION_MATRIX.md` carries that text in a new `Observable` column.

## Out of scope

- Real observables for the existing behaviors (phase 2) and `full`'s use of
  recon to fill the target (phase 3). `full` passes the flag target in
  this phase, and a burst behavior whose fields are missing is a `skipped`
  verdict the summary counts.
- Changing any frame byte the fixtures pin. The VRRP checksum and the
  sweep request's source are phase 2 and phase 3.

## Units

### U1. The check package and the catalog value, additive

Files: src/edge/netpen/check/check.go, src/edge/netpen/check/target.go, src/edge/netpen/check/check_test.go, src/edge/netpen/catalog/catalog.go, src/edge/netpen/catalog/codes.go, src/edge/netpen/catalog/catalog_test.go
After: none
Change: `check` holds the types the Decisions name. `catalog` adds
`Behavior` (embedding `check.Entry`, plus `Run` and `Modes`), a `Mode`
with its flag, help, and the three cost fields, `Catalog`, `New`, and the
validation of the new fields. Today's `Register`, `MustRegister`, `Entry`,
`Durability`, the latch, `registrations.go`, and the generated file are
untouched, so `runner`, `full`, `cmd`, and the attack packages build as
before. The new validation applies only to `New`. `codes.go` adds
`ErrCodeCeilingRefused`. `ErrCodeTargetMissing` lives in `check` beside
`Target.Need`, which returns it, since `check` cannot import `catalog`.
Ruled: the registration table's own `Behavior` and `Mode` types, used
only inside `catalog`, become the unexported `registration` and
`registrationMode` so the new types take the names the Decisions give
them, and `catalog.Legs` becomes an alias of `check.Legs` so the table and
the value share one type. The cutover deletes both renamed types.
Tests: `catalog_test.go` gains cases that reject a `decay` restore with a
negative bound, an unknown impact, an unknown affects, a nil `Run` on an
entry not flagged `Orchestration`, and a duplicate (name, mode) pair, and
accept a two-entry catalog whose `Entries()` come back sorted. The
existing oracle and CLI reconciliation tests keep passing.
`check_test.go` covers `Target.Need` naming every missing field in one
error with exit code 2.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen/catalog src/edge/netpen/check`

### U2. Findings schema version 2 and every consumer of the rollup kinds

Files: src/edge/netpen/findings/findings.go, src/edge/netpen/findings/findings_test.go, src/edge/netpen/output/json.go, src/edge/netpen/output/json_test.go, src/edge/netpen/output/tui.go, src/edge/netpen/output/tui_test.go, src/edge/netpen/output/feed_test.go, src/edge/netpen/cmd/netpen/stream.go, src/edge/netpen/cmd/netpen/stream_test.go, src/edge/netpen/full/full.go, src/edge/netpen/full/full_test.go, src/edge/netpen/test/integration/findings_class_test.go
After: none
Change: `SchemaVersion` is 2. `KindVerdict` replaces `KindResisted`,
`KindSkipped`, `KindPending`, and `Rollup`. `Record.Verdict` holds the
kind, detail, and observed milliseconds. `Summary` counts `Confirmed`,
`Resisted`, `Pending`, `Skipped`, `Findings`, `Errors`. The JSON and TUI
modes render a verdict line per attack. `full.computeVerdicts` builds
verdict records with the new kind and `summaryRecord` counts them.
`cmd/netpen/stream.go` counts the verdict kind where it counts the rollup
kinds. `findings_class_test.go` expects version 2. Ruled: `findings`
owns `VerdictKind` and its four constants, since it is the wire and
`check` imports it, so `check.Verdict.Kind` is a `findings.VerdictKind`
and `check` declares no kind of its own. `Summary.Count` tallies one
verdict so the three consumers share one switch.
Tests: `findings_test.go` marshals each kind and the verdict example of
requirement 2. `json_test.go` and `tui_test.go` golden outputs carry the
verdict line. `feed_test.go` counts verdicts into the summary.
`full_test.go`'s verdict cases read the new kind.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen/findings src/edge/netpen/output src/edge/netpen/cmd src/edge/netpen/full src/edge/netpen/test/integration`

### U3. Leg addresses and a write path that does not wait on the poll

Files: src/edge/netpen/link/link.go, src/edge/netpen/link/link_linux.go, src/edge/netpen/link/link_other.go, src/edge/netpen/link/link_test.go, src/edge/netpen/link/link_linux_test.go, src/edge/netpen/attacks/testtest/leg.go, src/edge/netpen/attacks/testtest/leg_test.go, src/edge/netpen/runner/runner_test.go, src/edge/netpen/runner/durability_test.go, src/edge/netpen/full/full_test.go, src/edge/netpen/full/recon_scan_test.go, src/edge/netpen/cmd/netpen/main_test.go
After: none
Change: `Leg.Addr()` returns the interface's MAC and addresses, read at
open through a seam over `net.InterfaceByName` the test replaces. The
locking follows the Decision. The test leg gains `SetAddr`, so
`testtest.New()` keeps its signature and the call sites in the attack
tests stay untouched until the cutover. Every mock leg in the listed test
files gains the method.
Tests: `link_linux_test.go` adds a case where a `Send` completes while a
poll blocks for the full timeout, asserted under 20 ms, and a case where
`Close` during a poll returns after the poll and a later `Send` fails
with `ErrCodeLegOpen`. Both compile only on Linux. `link_test.go` covers
`Addr` on the stub. `leg_test.go` covers `SetAddr`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen/link src/edge/netpen/attacks/testtest src/edge/netpen/runner src/edge/netpen/full src/edge/netpen/cmd`, then `GOOS=linux go vet ./link/...` from `src/edge/netpen`, and `go test -race ./link/...` on a Linux host.

### U4. Cutover: runner, behaviors, orchestrator, command, matrix, lab

Files: src/edge/netpen/runner/, src/edge/netpen/catalog/, src/edge/netpen/attacks/, src/edge/netpen/full/, src/edge/netpen/cmd/netpen/, src/edge/netpen/test/integration/VALIDATION_MATRIX.md, src/edge/netpen/test/integration/matrix_test.go, src/edge/netpen/test/integration/t1_ae6_test.go, src/edge/netpen/test/integration/t2_lab_ospf_test.go, src/edge/netpen/test/integration/lab/lab.go, src/edge/netpen/test/integration/lab/lab_test.go, src/edge/netpen/Taskfile.yml, src/edge/netpen/README.md, docs/architecture/2026-09-23-netpen-lab-vendor-validation-direction.md, test/conformance/
After: U1 U2 U3
Change: `runner.Options` carries `Catalog`, `MaxImpact`, `Target`,
`Observe`, `Rate`, `Acknowledged`, `Orchestrated`, `Timeout`,
`TeardownBudget`, and the two legs. `Behaviors`, `SuppressOutput`,
`runner.Behavior`, and `runner.Deps` are gone. The gate follows the
Decisions, the runner emits the verdict record, wraps the leg for the
rate limit, maps the target error, and exports `AbandonedSteps` and
`StopContext`. Every `Run*` under `attacks/` has the new signature, reads
the target and the leg address, calls `Target.Need`, and returns a
verdict: `ghost` and `raguard` observe for `deps.Observe` and return
`resisted` on an empty window, `arpsweep` and `vlanenum` return
`confirmed` with their inventory as detail, and every other behavior
returns `pending` with a reason naming the missing observable. `llmnr` and
`wpad` emit no secret fields, `wpad` takes its listener from
`deps.Listen`, `dtp` reports its durability from `deps.Entry`, the
permanent modes arm nothing, and `stproot` drops its own pacing. The
`srcMAC` helpers, the fixture address variables in each `helpers.go`, and
`craft.FixtureSrcMAC` are deleted, each family package exports its
`Behavior` values, and `attacks.Default()` returns the shipped catalog
with `scan` as its one `Orchestration` entry. In `catalog`,
`registrations.go`, `Register`, `MustRegister`, `Durability`, and the
latch are deleted, the generator imports `attacks.Default()`, and
`go generate ./catalog/...` rewrites the generated file. `full` and
`cmd` build `Options.Catalog` from `attacks.Default()`, `full/behaviors.go`
and `MergedBehaviors` are deleted, and `catalog.Entries()` call sites in
`main.go` read the catalog value. The command adds `--max-impact`,
`--observe`, `--victim`, `--gateway`, `--vip`, `--group`, `--vrid`,
`--outer-vlan`, `--inner-vlan`, `--voice-vlan`, `--vtp-domain`,
`--router-id`, `--prefix6`, `--route4`, `--offer-ip`, and `--sweep-net`,
builds `Options.Target` from them, and installs the signal handler that
moved from `runner`. `stubNames` and the AST reconciliation test are
replaced by a test that walks `attacks.Default()` against the dispatch
table. `full.computeVerdicts` reads verdict records from each phase and
`observeTraversal` is deleted. The matrix gains `Impact`, `Restore`,
`Affects`, and `Observable` columns and every row is rewritten from the
catalog. The T1 and T2 harness commands pass the target flags their
behaviors need, and the lab record's "fixed fixture" sentences say the
target comes from flags. The Taskfile's `test` task drops `CGO_ENABLED=0`.
`README.md` describes the check model, the cost fields, the ceiling, and
the target flags with one worked command. A conformance gate rejects
`os.Exit` and `signal.Notify` under `src/edge/netpen/runner/`.
Ruled: `Affects` is `device` for switch or router state (CAM, DTP, LLDP,
MVRP, LACP, DHCP pool, snooping), `clients` for end hosts' caches,
leases, and first-hop choice, `fabric` for domain-wide protocol state
(STP, VTP, OSPF, EIGRP), and `none` for the no-impact entries;
`doubletag` is `none` since the registration text said nothing persists.
Ruled: a host flag takes `<ip>` or `<ip>,<mac>`, and `Target` names the
MAC as its own field (`victim-mac`), so a behavior that frames to a host
asks for both parts. Ruled: `arpsweep` keeps the 16-byte protocol
addresses its fixture carries and requests the first three hosts of
`--sweep-net` from the leg's own IPv4. Ruled: a ceiling refusal records
a refusal and a skipped verdict, and a behavior whose target check fails
runs no teardown, since it armed nothing. Ruled: `full` keeps no verdict
list of its own, and the summary counts the verdict records each phase's
runner emitted. Ruled: `attacks.Behaviors()` returns the shipped list so
tests replace a `Run` before building their own catalog.
Tests: `runner_test.go` covers the gate order, the ceiling refusal that
continues, the verdict record per attack, the error-to-pending path, and
the target error as `skipped` with and without `Orchestrated`.
`ratelimit_test.go` covers parent requirement 9 with a fake clock.
`durability_test.go` covers the ceiling against every impact. Every
existing attack suite passes with its `fixtureTarget()`, plus one case per
package where an empty target yields the target error and zero sends.
`ghost_test.go` and `ip6_test.go` cover the empty-window `resisted` path
within the window. `routing_test.go` runs `wpad` on an in-memory
listener. `attacks_test.go` holds the oracle of all 40 pairs.
`snapshots_test.go` pins the regenerated file. `main_test.go` covers the
usage error and exit 2 for a missing target, the ceiling flag, the ack
hint text, and `ackList.Set` rejecting a bare mode. `full_test.go` covers
a burst whose stub behaviors return mixed verdicts and the summary
counts, and the ceiling refusing `stproot` under `none` while `ghost`
runs. `matrix_test.go` checks the four columns per row. `lab_test.go`
asserts the command lines carry the flags. The conformance gate has a
fixture that trips it.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen test/conformance docs/architecture/2026-09-23-netpen-lab-vendor-validation-direction.md`

Waves: U1 U2 U3 | U4

## Verification

```bash
cd src/edge/netpen && go vet ./... && GOOS=linux go vet ./... && go test -race -count=1 ./... && go generate ./catalog/... && git diff --exit-code catalog/zz_generated_catalog.go
.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen test/conformance
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `src/edge/netpen/README.md` written and the matrix regenerated in
      the same change.
- [ ] `uv run tools/scripts/run.py plan record implemented <phase> --units 4 --from <t> --to <t>`.
- [ ] No plan labels in code.

## Open questions

- The parent's open questions on the `--max-impact` and `--observe`
  defaults. U4 and U6 take `temporary` and `3s`.
- Whether the cutover unit, which touches every package in the module,
  should be driven by one worker or split across two worktrees by package
  with a merge at the end. The implementer takes one worker, since no
  package in it builds alone.
