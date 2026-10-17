---
title: netpen Check Model - Plan
type: feat
date: 2026-10-12
artifact_contract: flowseer-plan/v2
execution: code
---

# netpen Check Model - Plan

## Resume on another machine

Branch: `codex/netpen-resume` on `origin`.

Phase 2 is implemented. Its [state](2026-10-12-1259-feat-netpen-check-model-phase2-plan.state.json)
records the implementation range. Review and compound are pending before
landing phase 2 and continuing with phase 3.

```bash
git fetch origin codex/netpen-resume
git switch --track origin/codex/netpen-resume
```

## Goal

An operator runs `netpen` against a segment and gets a verdict per check:
confirmed, resisted, pending, or skipped, with the evidence behind it, and
chooses beforehand how much impact the run may have. The means: the runner
contract becomes probe, observe, verdict, restore, the catalog declares
impact, restore, and blast radius per entry, targets become inputs, the
existing 35 behaviors are migrated and given real observables, and a set of
defensive-control checks is added.

**Stop condition:** a behavior's observable cannot be read from the attack
or watch leg on Linux AF_PACKET at all (not merely not yet). The verdict
contract in `U1` is then too strong for that family and the record's
"every behavior is a check" needs an exception.

## Decisions

The [check model record](../architecture/2026-10-12-netpen-check-model-direction.md)
holds the decisions that outlive this plan: verdicts, the three cost fields,
the impact ceiling, targets as inputs, bounded observation, no process
ownership in the library, and the catalog as a value. It is proposed and
binds nothing until a person accepts it.

- The durability class splits into `Impact`, `Restore`, and `Affects`. Why:
  one enum answers consent and mechanism at once, which forces no-op
  teardown steps (`src/edge/netpen/attacks/l2/dtp.go:53`,
  `src/edge/netpen/attacks/fh/arpspoof.go:37`) and leaves the decay bound
  as prose (`src/edge/netpen/catalog/registrations.go:72`). (decided by the
  user, 2026-10-12)
- A behavior returns a `Verdict`. Why: the compiler then enforces that every
  check decides, which a run-time "no verdict emitted" error would only
  report. (decided by the user, 2026-10-12)
- Scope is the engine, the migration of every existing behavior to real
  observables, and new no-impact checks. Why: the engine alone leaves the
  tool unable to check anything, and the defensive-control checks are what a
  customer runs first. (decided by the user, 2026-10-12)
- The schema version moves to 2 and nothing is shimmed. Why: `AGENTS.md`,
  Agent behavior, rules out compatibility paths, and no consumer outside the
  repository reads version 1.
- Fixture pins stay byte-for-byte where they are today. Why: the tests pass
  the fixture addresses as the target, so the pcaps under
  `src/edge/netpen/attacks/testdata/` keep proving the wire shape while the
  production path reads real targets.
- The rate limit moves into the runner. Why: `Options.Rate` is honored by one
  behavior (`src/edge/netpen/attacks/l2/stproot.go:61`) and ignored by the
  flood behaviors, and a wrapper around `link.Leg` enforces it for all of
  them at once.
- The observation window is a run option, defaulting to three seconds. Why:
  `ghost` waits until cancellation (`src/edge/netpen/attacks/fh/ghost.go:524`)
  and `raguard` waits 100 ms (`src/edge/netpen/attacks/ip6/raguard.go:78`).
  Neither is a choice an operator made. The default is unconfirmed and
  repeated under Open questions.
- `--max-impact` defaults to `temporary` on a single command and on `full`.
  Why: today every single command runs its transient-decay behavior with no
  flag, and a default of `none` would refuse most of the catalog by default.
  Unconfirmed, repeated under Open questions.
- Four phases in sequence. Why: the contract change touches every package
  at once and must land first, the orchestration phase adds typed
  preconditions to the entries the observables phase edits, and the new
  checks reuse the observe pattern the second phase settles.

## Requirements

1. A catalog entry carries `Impact`, `Restore`, and `Affects`, and a `decay`
   restore carries a duration. Example: the `roguera` entry reads
   `Impact: temporary, Restore: decay 30m, Affects: clients`, and
   `catalog.Validate` rejects an entry whose `Restore` is `decay` with a
   zero duration.
2. The gate refuses an entry above the run's impact ceiling before any
   frame. Example: `netpen full --max-impact none` with a watch leg
   attached dispatches `ghost` and `raguard`, emits a `refusal` record for
   each burst entry above the ceiling, the first naming `stproot` with
   reason `impact temporary exceeds ceiling none`, and continues to the
   next entry. The test leg records zero sends for `stproot`.
3. A permanent entry dispatches only when both the ceiling allows it and the
   per-pair acknowledgment names it. Example: `netpen vtp --wipe --max-impact
   permanent` without `-i-accept-permanent=vtp=wipe` is refused, and the hint
   names the real flag.
4. A behavior returns a verdict and the runner emits one `verdict` record per
   (attack, mode) with the verdict, the evidence, and the observed window.
   Example: `ghost` on a test leg that delivers no frames returns
   `resisted` with detail `0 frames in 3s`, and the stream holds exactly one
   `verdict` record for `ghost`.
5. The summary counts verdicts. Example: a run of three checks returning
   `confirmed`, `resisted`, `pending` emits a summary with
   `confirmed: 1, resisted: 1, pending: 1, findings: <evidence count>`.
6. A behavior reads its targets from `Deps.Target` and the leg's own
   addresses from `Leg.Addr()`. Example: `netpen arpspoof --victim 10.1.2.3
   --gateway 10.1.2.1` sends frames whose ARP fields carry those addresses
   and whose Ethernet source is the interface's MAC. No non-test `.go` file
   under `src/edge/netpen/attacks/` holds `172.16.0.10`, `10.0.0.99`, or
   `00:11:22:33:44:55`.
7. A behavior that needs a target it was not given returns a coded usage
   error before any frame. Example: `netpen arpspoof` with no `--victim`
   exits 2 with the hint naming the flag.
8. The runner arms a teardown only for `Restore: active`, and every armed
   step performs work. Example: `arpspoof` arms two unicast repair frames and
   the test leg records them after the poison frames. `dtp --keep-trunk` arms
   nothing and the runner does not fail it for arming nothing.
9. The runner enforces the rate limit. Example: `--rate 10` with a behavior
   that sends 50 frames takes at least 4.9 s on a leg whose clock the test
   controls.
10. A check that observes returns within the window. Example: `raguard` with
    `--observe 2s` and an empty watch leg returns `resisted` between 2.0 s
    and 2.5 s.
11. No record carries fabricated evidence. Example: the `llmnr` and `wpad`
    findings have no `protocol` or `length` field, and `findings_test.go`
    holds the secret-redaction case instead.
12. `full` derives verdicts from the behaviors' verdict records. Example: a
    watch leg that delivers only STP hellos during the burst yields
    `stproot: pending` with detail `no observable`, not `confirmed`.
13. The runner package has no process exit and installs no signal handler.
    Example: a conformance gate under `test/conformance/` fails on `os.Exit`
    or `signal.Notify` in `src/edge/netpen/runner/`.
14. An embedder composes a catalog. Example: a test outside `cmd/` builds
    `catalog.New(fh.ARPSweep, ip6.RAGuard)`, runs both through a runner on
    the test leg, and the stream holds two verdict records.
15. Every migrated behavior that has no observable yet returns `pending`
    with a reason, and the validation matrix lists it. Example: after the
    first phase `hsrp` returns `pending` with detail `no observable: coup
    acceptance needs a hello listener`, and the matrix row shows it.
16. Each existing behavior gains an observable where the attack or watch
    leg can carry one. Example: `hsrp` observes the group's hellos after the
    coup and returns `confirmed` when the active router's hellos stop or
    carry the attacker's address within the window.
17. A defensive-control check declares its own impact and its probe's
    observable before it enters the catalog. Example: an ARP-inspection
    check sends one ARP reply with a binding that contradicts the sweep's
    evidence, observes whether the switch forwards it to the watch leg, and
    is registered with `Impact: none` only if the plan's evidence shows the
    probe changes no neighbor cache.

## Out of scope

- The validation tiers. `test/integration` keeps its tags and the matrix
  stays hand-maintained. Phase 1 regenerates its columns and rows.
- The owned protocol encoders under `src/edge/netpen/layers/`. Their wire
  correctness is a separate audit.
- The Python baseline under `src/edge/netpen/tools/l2l3-audit/` and the
  fixture generators. They stay as the second source for the pins.
- A sustained relay (forwarding victim traffic). `portsteal --relay`
  enables and restores host forwarding and nothing more.
- Input authors: netpen reads flags from the operator, frames from the
  segment it is pointed at, and fixtures written by contributors. The
  segment is untrusted and every decoder treats a frame as data. The
  operator is trusted.

## Units

### U1. Contracts: catalog, findings, link, runner, migration, command

Files: docs/plans/2026-10-12-1259-feat-netpen-check-model-phase1-plan.md

The catalog carries the three cost fields and is a value, a behavior returns
a verdict and reads a typed target, the leg reports its addresses, the
runner enforces the ceiling, the rate limit, and the observation window,
the command exposes the flags, and every existing behavior compiles against
the new contract with `pending` where it has no observable. Claims
requirements 1 to 7, 9 to 11, 13, 14, and 15.

### U2. Observables for the existing behaviors

Files: docs/plans/2026-10-12-1259-feat-netpen-check-model-phase2-plan.md
After: U1

Each behavior under `src/edge/netpen/attacks/` reads the wire for the
effect its probe should have and returns a real verdict, the no-op teardown
steps do the work their names claim, and the frames the review found
unverifiable on the wire (the zero VRRP checksum, the zero-source sweep
request) are corrected against their sources. Claims requirements 8 and
16.

### U3. Recon and orchestration

Files: docs/plans/2026-10-12-1259-feat-netpen-check-model-phase3-plan.md
After: U2

`full` fills the target from recon, runs the burst as the concurrent phase
its comment describes, takes verdicts from the behaviors, and honors the
ceiling. `scan` and recon read the leg's own addresses. Claims requirement
12.

### U4. Defensive-control checks

Files: docs/plans/2026-10-12-1259-feat-netpen-check-model-phase4-plan.md
After: U3

New checks for the controls an operator expects a switch to run, each with
its probe, observable, and declared impact. Claims requirement 17.

Waves: U1 | U2 | U3 | U4

## Verification

```bash
cd src/edge/netpen && go vet ./... && GOOS=linux go vet ./... && go test -race -count=1 ./...
.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen docs/architecture/2026-10-12-netpen-check-model-direction.md
```

`go test -race` needs cgo on Linux, so the `CGO_ENABLED=0` the Taskfile
sets for the static build is left off here. The linux leg's tests compile
only on Linux, so a Linux run is part of the proof.

The `attacks/routing` suite binds a loopback listener for `wpad` and fails
in a sandbox that denies port binding. Phase 1 adds a listener seam so the
suite passes there too. A lab run of `netpen full --max-impact none` on the
T1 FRR segment, recorded by hand in the matrix, closes the parent.

## Definition of done

- [ ] Verifier green for every changed path of every phase.
- [ ] `src/edge/netpen/README.md` exists and states the check model, the
      cost fields, the ceiling, and the target flags.
- [ ] The validation matrix carries the new columns and every (behavior,
      mode) pair.
- [ ] The check model record is accepted or amended by a person.
- [ ] Each phase recorded with `uv run tools/scripts/run.py plan record
      implemented <phase> --units <n> --from <t> --to <t>`.
- [ ] No plan labels in code.

## Open questions

- The `--max-impact` default. `temporary` keeps today's single-command
  behavior. `none` is the safer default for a tool that will be handed to
  operators who have not read the catalog. The implementer takes
  `temporary` unless told otherwise.
- The observation window default of three seconds is a guess at the
  hello intervals involved (STP 2 s, HSRP 3 s, OSPF 10 s). Phase 2 may
  need a per-check default that overrides the run option.
- Whether `arpsweep` and `vlanenum` are checks (they return `confirmed`
  with inventory evidence) or discovery commands like `scan`, which stays
  an orchestration command outside the verdict model. Phase 1 treats the
  two as checks returning `confirmed`.
- Whether a new gate under `test/conformance/` counts as a policy surface
  needing guardrail review. `AGENTS.md` names only `test/conformance/a11y/`.
  Phase 1's last unit adds the gate and flags it in its handoff.
- Whether the validation matrix should be generated from the catalog
  rather than guarded by `TestValidationMatrixCompleteness`. Phase 1 keeps
  the guard.
