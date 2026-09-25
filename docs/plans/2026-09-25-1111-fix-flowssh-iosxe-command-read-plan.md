---
title: flowssh IOS-XE Command Read - Plan
type: fix
date: 2026-09-25
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
compound: docs/solutions/architecture-patterns/expect-style-prompt-scanner-must-reset-its-window-per-command.md
execution: mixed
amends: docs/plans/2026-09-23-2228-feat-netpen-lab-vendor-validation-plan.md
---

# flowssh IOS-XE Command Read - Plan

> Implemented. The command reader and IOS-XE builders now use opt-in echo
> anchoring. U2 live validation (2026-09-25) confirmed the fix against the IOS-XE
> lab target: `Session.Run` reads `show ip ospf` / `show ip ospf neighbor`
> correctly and netpen's injected OSPF adjacency was observed forming and
> clearing on the device. `TestT2OSPFLiveLab` does not yet pass end to end, and
> the matrix `ospf` cells stay `pending live run`, because of a *separate* netpen
> bug the live run surfaced: netpen advertises OSPF router ID `10.0.0.153`
> (`attacks/routing/helpers.go` `attackerRouterID = 0x0a000099`, whose value is
> `.153` though its comment and source address say `.99`), which the assertion's
> expected `10.0.0.99` does not match. Fixing it entangles the harvest generator's
> OSPF checksums and byte-offset teardown tests, so it is deferred to its own
> netpen plan.

> Root cause confirmed by a live diagnostic against LABRT42 (2026-09-25): every
> `Session.Run` returns the *previous* command's output — a one-command
> off-by-one, not CR-vs-LF. The device responds to LF and emits output normally.
> The fix anchors each Run on its own command echo. See Background evidence.

## Goal

Make `src/protocol/ssh` (`flowssh`) `Session.Run` return the output of the
command it just sent, against a live Cisco IOS-XE device that echoes a prompt
between commands, so the `netpen_t2` `TestT2OSPFLiveLab` observable reads
succeed and the `ospf` VALIDATION_MATRIX cells carry real vendor truth instead
of `pending live run`. The means is opting the IOS-XE commands into a Run scan
anchored on that command's own echo, so a residual prompt left in the ring
cannot be matched as the current command's terminator. This plan is wrong if
anchoring on the echo cannot be done without breaking the documented no-echo
shell support and no real caller needs a fresh design — then U1 routes back as
a blocker.

## Background evidence

Live diagnostic (throwaway `TestT2Diag`, run and reverted 2026-09-25) ran
several commands through one flowssh session after a priming Run and logged
`Result.Output` with `Evidence.BytesReceived`:

```
PRIME                             matched=iosxe-privileged bytes=14 out="\r\n\r\n\r\n"
CMD "show clock"                  bytes=10  out="\r\n"
CMD "terminal length 0"           bytes=54  out="show clock\r\n09:16:11.107 UTC Fri Sep 25 2026\r\n"
CMD "show ip ospf interface brief" bytes=27 out="terminal length 0\r\n"
CMD "show ip ospf neighbor"       bytes=185 out="show ip ospf interface brief\r\n<the interface-brief table>"
CMD "show ip ospf"                bytes=31  out=" neighbor\r\n"
```

Each Run returns the prior command's output. So the device does respond to the
LF terminator and does emit output (bytes arrive; `show clock`'s time text and
the `Gi2.999` table both show up) — **CR-vs-LF (C1) is ruled out**. The failure
is a one-command lag (**C2**): a residual prompt sits in the ring, and the next
Run matches it before the current command's echo and output arrive, returning
what preceded it and leaving the current output for the Run after.

Mechanism (verified in code):
- `Session.Run` (`src/protocol/ssh/command.go:120`) `reset`s the rings, writes
  `cmd.Line + "\n"`, then loops `ring.waitFor` with a match that calls
  `echoSkipLen(buf, echo)` then `scanPrompt(buf[skip:], …)`.
- `echoSkipLen` (`command.go:261`) uses `bytes.HasPrefix`: it returns 0 unless
  the echo is at the *start* of the buffer. When a residual prompt precedes the
  echo, the skip is 0 and `scanPrompt` runs over the whole buffer, matching that
  residual prompt as the command's own.
- `ring.reset` (`src/protocol/ssh/buffer.go`) drops only bytes already
  retained; a prompt the previous command's LF reprinted arrives from the drain
  goroutine after `reset` and so survives into this Run.
- FastIron reads correctly today (`src/modules/localnet/access/...`), so the fix
  must not change the bytes that path sees.

## Decisions

- Fix opted-in commands by anchoring each Run's prompt scan on that Run's own
  command echo: locate the echo as a substring, discard everything up to and
  including it, and scan for the prompt only in what follows; until the echo
  appears, match nothing.
  Why: the residual prompt always precedes the echo (the echo is the device's
  response to *this* command), so anchoring on the echo makes a stale prompt
  unmatchable and removes the lag at its source, for the first command and every
  later one. It subsumes the login-prompt race, so no separate priming Run is
  needed.
- Echo-anchoring is OPT-IN per `Command`, default OFF. Why: the default must be
  today's whole-buffer scan so FastIron and the device service are byte-identical
  and untouched (no caller edits) — the FastIron test shells deliberately do not
  echo non-empty commands, so an anchoring default would break them and forcing
  an opt-out onto those callers is a change this fix must not make. Only the
  netpen IOS-XE command builders (`lab/iosxe.go`) set the opt-in, since their
  live device echoes and exhibits the lag. A `Command` field (name it for the
  behavior, e.g. `AnchorOnEcho bool`) carries it, defaulting false.
- Drop the priming-Run idea from the prior draft. Why: the diagnostic shows a
  prime does not help (it seeds the same lag — its own LF reprints a prompt that
  the next Run then matches); the echo anchor fixes the first command directly.
- No line-terminator change. Why: C1 is ruled out; LF executes commands and
  yields output on this IOS-XE.
- Ruled: preserve no-echo support by making echo anchoring opt-in through
  `Command.AnchorOnEcho`. Existing callers retain the whole-buffer scan, while
  the IOS-XE OSPF commands opt in. Why: FastIron's no-echo behavior stays
  unchanged, and callers affected by residual prompts can require the stronger
  boundary. Cost if wrong: each affected command builder must opt in explicitly.
- No compatibility shim. Why: pre-stability building blocks; the fix changes
  internal scan behavior, and every caller's observable result only becomes
  correct.

## Requirements

1. `Session.Run` returns the output of the command it sent, not a prior
   command's, when the shell echoes and a residual prompt is in the ring.
   Acceptance: a `command_test.go` case feeds, through the in-process SSH
   server, a stream where a residual prompt precedes the echo
   (`LABRT42#` then `show clock\r\n<output>\r\nLABRT42#`); Run returns
   `<output>`, not empty.
2. `TestT2OSPFLiveLab` reads a non-empty `show ip ospf` containing "routing
   process" against LABRT42 without a priming Run, and does not skip at the
   prerequisite guard. Acceptance: the up-check `Result.Output` lowercased
   contains "routing process" and "ospf".
3. The two OSPF injections each yield a `finding:ospf` class, `10.0.0.99`
   appears in `show ip ospf neighbor` within 10 s and clears within 40 s.
   Acceptance: `TestT2OSPFLiveLab` passes end to end against LABRT42.
4. FastIron reads are unchanged. Acceptance:
   `go test ./src/modules/localnet/access/...` green, and the fastiron adapter
   sends and matches the same bytes it does today.
5. The `ospf` VALIDATION_MATRIX `t1 AE6` and `t2` cells carry the dated live
   result, replacing `pending live run`.

## Out of scope

- Non-OSPF netpen behaviors (seven superset attacks stay pending).
- SNMP/NETCONF observables; the observable stays CLI over SSH.
- Any FastIron or device-service behavior change beyond staying green.

## Units

### U1. Anchor the prompt scan on the command echo
Files: `src/protocol/ssh/command.go`, `src/protocol/ssh/command_test.go`,
`src/edge/netpen/test/integration/lab/iosxe.go`
After: none
Change: in `Run`'s `waitFor` match (`command.go:210-223`), find the command
echo as a substring of the buffer and discard everything up to and including it
before `scanPrompt` when `Command.AnchorOnEcho` is true; while the echo has not
appeared, return `(0, false)` so no prompt matches. Keep the existing
`\r\n`/`\n`-after-echo trimming. The default false value preserves today's
whole-buffer scan for no-echo callers. `MorePattern` handling and the
`res.consumed`/`outputEnd` offsets stay correct relative to the new anchor.
Set `AnchorOnEcho` on both IOS-XE OSPF command builders.
Tests: `command_test.go` — (a) residual-prompt-before-echo returns the current
output (R1); (b) the existing no-residual case still returns the full output;
(c) a `--More--` paginated command still assembles across pages; (d) if no-echo
support is kept, a no-echo command still matches as before. Use the package's
in-process SSH server harness.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/ssh/command.go src/protocol/ssh/command_test.go src/edge/netpen/test/integration/lab/iosxe.go`

### U2. Live-validate and record vendor truth
Files: `src/edge/netpen/test/integration/VALIDATION_MATRIX.md`,
`docs/plans/2026-09-23-2228-feat-netpen-lab-vendor-validation-plan.md`
After: U1
Change: with the lab wired (recreate Kali `eth1.999`, confirm
`ping -I eth1.999 10.0.0.42`; env per memory `project_flowssh_iosxe_blocker`),
run `go -C src/edge/netpen test -race -tags=netpen_t2 -run TestT2OSPFLiveLab`.
On pass, set the `ospf` `t1 AE6` and `t2` matrix cells to the dated live result
and the emitted finding-class, and update the 2026-09-23 plan's outcome note to
record that T2 OSPF vendor validation is now obtained. If it still cannot pass,
record why and leave the cells `pending live run` (never a fabricated pass).
Space out SSH per the lockout-avoidance recipe.
Tests: `matrix_test.go` stays green (structure/superset guards).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen/test/integration/VALIDATION_MATRIX.md docs/plans/2026-09-23-2228-feat-netpen-lab-vendor-validation-plan.md`
(the netpen paths verify with `go -C src/edge/netpen ... -tags=netpen_t2`, per
docs/solutions/conventions/a-package-behind-a-build-tag-fails-untagged-go-vet.md).

Waves: U1 | U2

## Verification

- Offline: `go test ./src/protocol/ssh/... ./src/modules/localnet/access/...`
  green (FastIron unchanged); `go -C src/edge/netpen test -race -short -tags=netpen_t2 ./test/integration/...`
  green; `verify-change` green for every changed path (tagged for the netpen
  tag-gated package).
- Live (lab): `TestT2OSPFLiveLab` passes against LABRT42 — non-empty OSPF
  status read, `10.0.0.99` appears then clears, two runs match finding-class.

## Definition of done

- `verify-change` green for every changed path; offline suites green; FastIron
  path unchanged and green.
- `TestT2OSPFLiveLab` passes against LABRT42, or the matrix cells stay
  `pending live run` with the recorded reason.
- VALIDATION_MATRIX `ospf` cells and the 2026-09-23 plan outcome note updated in
  this change; this plan's `status` set with an outcome note.
- No `R#`/`U#` labels in code.
