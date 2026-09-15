---
title: Panic Policy Phase 1 - The Rule and the Decided Conversions - Plan
type: refactor
date: 2026-09-15
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
parent: docs/plans/2026-09-15-1020-refactor-panic-policy-plan.md
---

# Panic Policy Phase 1 - The Rule and the Decided Conversions - Plan

## Goal

`docs/code-style.md` states the three clauses and the goroutine boundary, and
every panic outside `src/protocol/smi/internal/parse` and outside a goroutine
satisfies them. The means: one doc edit and ten package-local changes.

This phase is wrong if a unit's propagation reaches an exported API the unit
does not name. That has happened four times in this plan's history —
`scheduleDequeue`, `decodeNatural`, `mustSetOperStatus`, `newOIDCall` — so each
unit below names the enclosing function of every call site, not just the line,
and an implementer who finds a fifth stops and re-plans rather than widening
the unit.

## Decisions

The parent's Decisions govern. Five are specific to this phase, and each
replaces an approach a review showed was blocked:

- `errs.NewCode` loses its panics and keeps its name. Why: both duplicate the
  repo-wide scan at `src/common/errs/code_test.go:182`, which is stronger on
  every axis — it fails at `go test` time, covers packages no binary links, and
  rejects a non-literal argument (`code_test.go:254`). Renaming to
  `MustNewCode` would touch 236 declaration sites to preserve a check nothing
  needs.
- `diag.Raise` becomes `diag.MustRaise` with a source-scan proof, rather than
  degrading to a rendered diagnostic. Why: the degrade route needed a new
  catalog row, and `src/protocol/smi/coverage_test.go:335` fails any row with
  no fixture under `testdata/malformed/`. A parser-defect code cannot have one
  — a fixture is a MIB, and the defect is one a MIB cannot provoke — so that
  route is blocked with no exemption path. The proof route needs no row: an AST
  test reads every `MustRaise` call site and checks its argument count against
  the catalog arity, which satisfies clause 2 and mirrors the `NewCode` scan
  this repository already runs.
- `mustSetOperStatus` records a sticky fault rather than propagating an error.
  Why: its third call site (`switch.go:1805`) is inside `updateLagState`, not
  `LinkChange`, and propagation from there reaches `applyLAGEffects` →
  `interceptLACP` → `forward` → `Forward` and `Peek`, the switch's core API
  that `fabric` drives. `LinkChange` alone has 108 call sites across eight
  files, 77 of them in `vswitch/lag/` and `vswitch/stp/`. U7 uses the same
  shape for `fabric`, so netsim gets one fault shape rather than two.
- `mibgen` validates OIDs in a pre-pass, not at emission. Why: `newOIDCall` has
  11 callers and two — `emit_watch.go:159` and `emit_table.go:92` — sit inside
  `jen` closures that cannot return an error, the shape that blocked U6's first
  draft. Validating the resolved tree once, before any emitter runs, leaves
  `newOIDCall`'s signature alone.
- `newRing`'s non-positive-limit branch is deleted, not converted. Why: its doc
  comment says callers always default the limit first, so no caller can provoke
  the error; `docs/code-style.md:38-40` bans handling errors that cannot occur;
  and an error return there adds a shell-channel leak path at
  `src/protocol/ssh/session.go:131-132` on a branch that never executes.

## Requirements

Parent requirements 1, 2, 4, 5, 6, 7, 8, 9, 10, 11 and 12 land here. Parent
requirement 3 is phase 4, 13 is phase 2, and 14 is phase 3.

## Out of scope

- `src/protocol/smi/internal/parse` — the bailout is phase 2.
- Goroutines — the helper and the 64-site audit are phase 4.
- The enforcement check — phase 3.

## Units

### U1. State the rule in the style guide
Files: `docs/code-style.md`
After: none
Change: the panic sentence in the bullet at `docs/code-style.md:147-148`
becomes its own subsection; the naked-return sentence sharing that bullet stays
where it is. The subsection states the three clauses, the goroutine boundary,
the sanctioned remedies, and that adding a recover boundary is not one. It
records the retirement of "a panic that crosses a package boundary is a bug" as
a decision, with its successor: the ban is on unnamed panics crossing.
The Doc comments section at `docs/code-style.md:44-47` is amended in the same
edit. An inherited panic is named as the non-obvious contract that makes a doc
comment on an unexported function mandatory, so clause 3 and that section do
not contradict each other.
The Principles entry at `docs/code-style.md:26-27` gains a cross-reference.
Tests: none — prose, governed by `docs/doc-style.md`. Keep it to sentences: an
earlier draft ran six clauses across fifteen lines without a sentence break.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/code-style.md`

### U2. errs.NewCode stops panicking; the scan takes over its coverage
Files: `src/common/errs/code.go`, `src/common/errs/doc.go`,
`src/common/errs/README.md`, `src/common/errs/code_test.go`,
`src/protocol/smi/codes_test.go`
After: none
Change: `NewCode` drops the `validateCode` panic and the duplicate-registration
panic, keeping the `registry.LoadOrStore` that `Codes()` reads and discarding
its `loaded` result. Name, signature and all 236 non-test declaration sites are
untouched.
The scan at `code_test.go:182` stops skipping `_test.go`, which is where the
panic's only remaining coverage was. It must also start excluding
`src/common/errs` itself, which it does not do today — `code_test.go:187-214`
skips only dot-directories, `testdata`, non-Go files and `_test.go`. That
exclusion is added, not kept: this package's own tests pass deliberately
malformed non-literal codes the scan is built to reject.
`src/protocol/smi/codes_test.go:81-84` reasons from the panic ("every code
reaching the process registry is proof the generated literal was accepted").
That is false after this unit and moves to the scan.
`src/common/errs/README.md:41` and `doc.go:67-74` document the panic and are
updated in the same change, per `docs/doc-style.md`.
Tests: `src/common/errs/code_test.go` — `NewCode` with a malformed name and
with an already-registered name both return without panicking; the scan finds
the non-test declarations and the 7 test-declared codes outside this package
(`heartbeat_test.go:302`, `contract_test.go:13`, `connecterr_test.go:16,17,18`,
`consistency_test.go:41,42`); a duplicate literal added to a `_test.go` fixture
fails the scan. Count the declarations before writing the assertion — an
earlier draft asserted 241 and 8, and both were wrong.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/errs src/protocol/smi`

### U3. diag.Raise becomes MustRaise and gains its proof
Files: `src/protocol/smi/internal/diag/diagnostic.go`,
`src/protocol/smi/internal/diag/diagnostic_test.go`,
`src/protocol/smi/internal/diag/arity_scan_test.go` (new), and the 8 direct
call sites in `src/protocol/smi/internal/parse/`
After: none
Change: `Raise` is renamed `MustRaise`, keeping its body and its reasoning
about why both conditions are parser bugs. The panic then satisfies clause 1 by
name and clause 2 by proof: a new AST test walks non-test source, finds every
`MustRaise` call, resolves its `errs.Code` argument to a catalog row, and fails
when the argument count disagrees with the row's arity or the code is not
cataloged. `src/common/errs/code_test.go:300-320` is the model for resolving
the callee through its import path rather than by bare name.
`Diagnostic.nargs` is clamped to `catalog.MaxArgs` (`catalog.go:27`, currently
4) regardless, because the arity panic is what keeps `Message()`'s `d.args[i]`
loop (`diagnostic.go:312-315`) in bounds and the proof is a test, not a
compile-time guarantee.
No catalog row is added, so `zz_generated_codes.go`, `COVERAGE.md` and
`shipped-codes.txt` are untouched, and `coverage_test.go:335` stays green.
Tests: `arity_scan_test.go` — a fixture call with the wrong argument count
fails the scan; one with an uncataloged code fails; the real tree passes.
`diagnostic_test.go:234` — the existing recover-based cases target `MustRaise`;
a five-argument call renders rather than indexing out of range.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/smi`

> BLOCKED — needs re-plan (found during implementation). Two facts the unit did
> not account for, both of the kind the parent's Goal says to stop on rather
> than widen:
>
> 1. **Scope reaches a public API.** `Raise` is not confined to
>    `internal/parse`. There is a public `smi.Raise`
>    (`src/protocol/smi/diagnostic.go:93`) wrapping `diag.Raise`, called from
>    `smi/resolve.go:602` and `smi/codes_test.go:109`; `diag.Raise` itself is
>    called from `internal/lex/lex.go:576,587`, `internal/frame/frame.go:173,652,670`
>    and `internal/parse/recover.go:169,176`. The rename is a public-API change
>    (sanctioned pre-1.0, but not what the unit named — it named "8 direct call
>    sites in `parse/`", of which there are 2).
> 2. **The proof as specified is infeasible.** Several of those call sites are
>    variadic forwarders — `diag.Raise(pos, code, args...)` (`lex.go:587`),
>    `diag.Raise(..., code, args...)` (`recover.go:169`) — passing a runtime
>    `errs.Code` parameter and a spread `args...`. A static AST scan cannot
>    resolve the code to a catalog row or count the args at those sites, so
>    "walks non-test source, finds every `MustRaise` call, resolves its
>    `errs.Code` argument" does not hold. A workable proof must either scan only
>    direct constant-code sites (leaving the forwarders proven elsewhere) or use
>    `go/types` and still cannot count a spread `args...`.
>
> The rename itself is safe; the re-plan is about naming the full call-site set
> (lex, frame, parse, smi public wrapper, resolve) and a proof mechanism that
> holds against variadic forwarders. Not landed by this pass.

### U4. catalog.Register returns an error
Files: `src/edge/netpen/catalog/catalog.go`,
`src/edge/netpen/catalog/registrations.go`,
`src/edge/netpen/catalog/catalog_test.go`,
`src/edge/netpen/catalog/snapshots_test.go`
After: none
Change: `Register(b Behavior) error` returns the post-read error and
`validate`'s error instead of panicking; `MustRegister(b Behavior)` wraps it
and panics, and the four `registrations.go` call sites (`:19,51,54,87`) call it
from `init()` — clause 2's init-time answer, cited in `MustRegister`'s doc
comment. The doc comment's "It panics for…" sentences move to `MustRegister`.
`snapshots_test.go:26` calls `Register` and must handle the new error:
`errcheck` is on (`.golangci.yml:8`) with no test exclusion, so discarding it
fails lint.
`netsimtest.Registry` (`src/common/netsim/internal/netsimtest/corpus.go:761,773`)
is the model pair.
Tests: `catalog_test.go` — `Register` with an empty `Name` returns a non-nil
error and leaves `Behaviors()` unchanged; `Register` after `Behaviors()`
returns the post-read error; `MustRegister` panics on both.
Verify: `cd src/edge/netpen && go test ./catalog/...`, then
`.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen/catalog`

### U5. Delete newRing's unreachable branch
Files: `src/protocol/ssh/buffer.go`, `src/protocol/ssh/buffer_test.go`
After: none
Change: `newRing` drops the non-positive-limit panic and its branch entirely,
keeping its signature. Both callers are at `src/protocol/ssh/session.go:131,132`
inside one composite literal and both pass a defaulted limit — the doc
comment's "callers always default it first" is the caller contract that makes
the branch unreachable, and `docs/code-style.md:38-40` bans handling an error
that cannot occur. The doc comment states the contract instead of the panic.
Nothing is added at `session.go`, so the shell-channel leak an error return
would have introduced does not arise.
Tests: `buffer_test.go` — `newRing` with the defaulted limit behaves as before.
The deleted branch had no test of its own to remove: no `buffer_test.go`
existed and no test constructed a `ring`, so `buffer_test.go` is added new with
one constructor test rather than losing a panic case.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/ssh`

### U6. mibgen resolves the decoder variant before emission
Files: `src/protocol/snmp/cmd/mibgen/emit_tc.go`,
`src/protocol/snmp/cmd/mibgen/emit_key.go`,
`src/protocol/snmp/cmd/mibgen/emit_resolve_test.go`
After: none
Change: the unregistered-variant check moves off the emission path.
`DecodeFunc` is `func() *jen.Statement` (`emit_tc.go:154`) and is invoked in
`jen` callbacks at `emit_scalar.go:63` and `emit_table.go:94` that cannot
return an error, so the helpers keep their signatures and the variant is
resolved through `decoderHelperFor` before the `resolved` value is built.
The nine call sites are `emit_tc.go:307,324,390,399,457,567,589` and
`emit_key.go:156,166`; `decodeIntCast` (`emit_tc.go:716-718`) reaches
`decodeCast` indirectly. Eight of the nine sit in functions that return a bare
`resolved` and today cannot fail: `enumResolved` (`:302`), `numericResolved`
(`:319`), `applicationType` (`:374`, returning `(resolved, bool)`),
`resolveBase` (`:553`), `resolvedBytes` (`:583`), `keyedResolved`
(`emit_key.go:149`). Only `resolveOverride` (`:413`) already returns an error.
Reaching `resolveType` (`emit_tc.go:187`), the first function with an error
return, changes seven signatures and their internal call sites at
`emit_tc.go:193,212,226,233,249,256,261,264,377,379,381,383,418,556,558,560,575`
and `emit_key.go:170`. That is the unit's real size. It is package-local, and
no exported API leaves `mibgen`.
`emit_resolve_test.go:61,127` call `naturalResolved` directly and move with the
signature. `tcDelegate` (`emit_tc.go:637`) uses neither helper and is
unaffected.
Tests: `emit_resolve_test.go` — resolving an unregistered variant returns an
error naming it; a generation run over a TC with an unresolvable variant fails
without writing output; a run over the golden corpus is byte-identical.

Landed shape (deviation from the threading design above): every `resolved` an
emitter reads reaches it through `resolveType`, and every non-`tcDelegate`
`resolved`'s `Variant` field equals the variant it passes to the decode
helpers. So the check is one call — `validateDecoderVariant(r.Variant)` — at the
`resolveType` entry point, before any emitter touches the resolved. That covers
every emitted `DecodeFunc` without threading an error return through
`enumResolved`, `numericResolved`, `applicationType`, `resolveBase`,
`resolvedBytes` and `keyedResolved`, all of which pass compile-time-constant
variants that cannot be unregistered — an error return there would handle an
error that cannot occur, which `docs/code-style.md` bans. The emission helpers
are renamed `mustDecodeNatural`/`mustDecodeIntCast`/`mustDecodeCast`; their
panic is now the clause-2 proven guard the pre-pass and
`TestValidateDecoderVariant` make unreachable. `naturalResolved` keeps its
`resolved` return, so `emit_resolve_test.go:61,127` are untouched.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/snmp/cmd/mibgen`

### U7. fabric records a scheduling fault
Files: `src/common/netsim/fabric/fabric.go`, `src/common/netsim/fabric/run.go`,
`src/common/netsim/fabric/run_test.go`
After: none
Change: `scheduleDequeue` records both invariant breaches on a sticky `err`
field and returns without scheduling; `Fabric` gains `Err()`. The field goes on
`type Fabric struct` at `fabric.go:156-177`, not in `run.go`.
There is no error path above it: all four call sites (`run.go:635,646,672,717`)
are in `serve` (`run.go:623`), which returns nothing; `enqueueEgress`
(`run.go:604`) returns nothing; and `Step` is `(Entry, bool)` (`run.go:311`)
with 49 call sites across six test files in two packages. The sticky field is
`bufio.Scanner`'s shape.
`Step` returns `false` once the fault is set, so a fault ends the run. Two
consequences this unit owns: `Step`'s doc comment (`run.go:305-310`) ends "Step
returns false when the arrival queue is empty" and becomes wrong; and
`Run(n int) int` (`run.go:926`) drives `Step` in a loop and would otherwise
return a short count with no signal, so it reports the fault too.

`Run` keeps its `int` signature rather than becoming `(int, error)`. Widening
it would touch ~85 `fab.Run(N)` call sites across a dozen fabric test files the
unit does not name — the propagation this phase's Goal says to stop at. Instead
`Run`'s and `Step`'s doc comments direct the caller to `Err()`, and the
caller-side assertion is a white-box test in `run_internal_test.go` (not the
black-box `run_test.go`, which cannot reach `scheduleDequeue`) that records a
fault and reads it back through `Err()`.
Per the parent's remedy rule the caller-side assertion lands here: a fault
recorded and never read is a test failure, because `errcheck` cannot see a
sticky field.
Tests: `run_test.go` — scheduling before `f.clock` after the run has started
records an error naming the clock and leaves the queue unscheduled; a second
schedule on an endpoint with `dequeuePending` records an error; `Step` returns
`false` afterwards; `Run` reports the fault rather than a short count; `Err`
reports the first fault and not the second.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric`

### U8. Generator scripts exit through run() error
Files: `src/edge/netpen/attacks/l2/harvest_l2.go`,
`src/edge/netpen/attacks/fh/harvest_fh.go`,
`src/edge/netpen/attacks/ip6/harvest_ip6.go`,
`src/edge/netpen/attacks/routing/harvest_routing.go`
After: none
Change: each script's `main` becomes
`func main() { if err := run(); err != nil { log.Fatal(err) } }`, and the
panics in `main`, `writePcap` and the packet builders become wrapped error
returns.
`mustMAC` (`harvest_l2.go:43`, `harvest_fh.go:45`, `harvest_ip6.go:68`,
`harvest_routing.go:65`) stays: it is `must`-prefixed, its 39 call sites are
mostly package-level `var` blocks (`harvest_l2.go:31-41` holds 8 of that file's
12), and those are clause 2's init-time answer over compile-time-constant MAC
literals.
`harvest_routing.go` looks smallest at one panic site, but that site is
`panicErr` (`:71-75`) with five callers (`:67,431,438,442,445`) — it is that
file's real violation.
Tests: none — `//go:build ignore` scripts that no test binary builds. The check
is that each regenerates its fixtures byte-identically.
Verify: from each script's directory,
`go run harvest_l2.go && git diff --stat -- ../testdata/`

Landed shape (two deviations):
1. **Sticky error, not per-builder returns.** The builders feed their bytes to
   `writePcap` in-line (`writePcap(path, dtpFrame(0x03), dtpFrame(0x81))`), and
   several builders delegate to a shared `craft`/`dot3SNAP` serializer. Threading
   `([]byte, error)` through every builder would cascade the return change across
   dozens of inline call sites — the widening the parent's Goal says to stop at.
   Instead each script gains a package-level `genErr` and a `fail` recorder: a
   serialize failure records into `genErr` and returns nil, `writePcap` short-
   circuits once `genErr` is set, and `run` returns `genErr`. This is the
   sticky-fault shape U7 and U10 use, with `run` as the caller-side read. Builder
   signatures are untouched. `harvest_routing.go`'s `panicErr` helper is removed;
   `mustMAC` keeps its own inline init-time panic.
2. **Byte-identical regeneration is not achievable and was not asserted.** Every
   `writePcap` stamps each packet with a wall-clock timestamp (`time.Now()` in
   ip6/routing; the zero-time path in l2/fh still varies), so two runs of the
   same script differ at the pcap timestamp offsets. This predates the change —
   the fixtures were never reproducible — so the regenerated pcaps are not
   committed. The check that landed is that each script compiles, runs, exits 0,
   and prints its OK line; the success path is byte-for-byte the pre-change logic
   (only the error path changed), so the fixtures' non-timestamp content is
   unchanged.

### U9. Document the surviving panics and their callers
Files: `src/protocol/snmp/oid.go`, `src/protocol/snmp/watch.go`,
`src/common/netsim/internal/netsimtest/corpus.go`,
`src/edge/netpen/catalog/catalog.go`,
`src/edge/netpen/attacks/*/harvest_*.go`,
`src/protocol/snmp/cmd/mibgen/emit_watch.go`
After: U4, U8, U11
Change: every surviving `Must`/`must` function states the panic, its invariant,
and which clause-2 answer it relies on. `MustOID` (`oid.go:65`) and
`MustChangeIndicator` (`watch.go:237`) already state the first two and gain the
third. `Registry.MustRegister` (`corpus.go:773`) states that it panics but not
the invariant. `mustMAC` has no doc comment at all.
The caller obligation terminates where the parent says it does: at a caller
supplying literal or compile-time-constant arguments, and not at all for a
panic proven unreachable. Generated MIB code is the second case — U11 makes
`MustOID` provably valid there, so no generated accessor and none of its
callers owes a sentence. `MustOID`'s own doc comment cites U11's proof, which
is why this unit lands after it: a comment citing a proof that does not exist
yet is the "argument that the value must be valid" U1 bans.
`init()` incurs nothing, for the same reason a `var` block does not. The five
non-test sites are `registrations.go:10`, `reactor.go:81`, `layers.go:70`,
`cmd/netpen/main.go:135`, `clause.go:232`.
`emit_watch.go:148-160` writes the `entryLen` call at
`testdata/golden/fakemib/mib.go:473`; it is the emitter an earlier draft
missed, and any other of `newOIDCall`'s 11 callers writing into a function body
is in the same position.
Tests: none — doc comments.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/snmp src/common/netsim src/edge/netpen`

### U10. vswitch records an oper-status fault
Files: `src/common/netsim/vswitch/switch.go`,
`src/common/netsim/vswitch/switch_test.go`
After: none
Change: `mustSetOperStatus` (`switch.go:2115`) records the error
`SetOperStatus` already returns on a sticky field and returns, instead of
panicking. `Switch` gains an `Err()` reporting the first fault, and the methods
that can record one say so.
The sticky shape is forced by the call graph. The three sites are
`switch.go:1805` (in `updateLagState`, `:1800`), `:2024` and `:2042` (in
`LinkChange`, `:2003`). Propagating from `updateLagState` reaches
`applyLAGEffects` (`:1791`) → `interceptLACP` (`:1577`) → `forward` (`:583`) →
`Forward` (`:496`) and `Peek` (`:502`), and `LinkChange` has 108 call sites
across `fabric/fabric.go` (4), `vswitch/lag/layer.go` (2),
`lag/layer_test.go` (43), `lag/fact_test.go` (3), `stp/layer_test.go` (34),
`protocol_link_test.go` (11) and `switch_test.go` (5). None of that is in this
unit, and the sticky field is what keeps it out.
`src/common/netsim/vswitch/README.md:266` documents `LinkChange`'s signature,
which does not change under this shape — confirm that before deciding the
README is untouched.
The caller-side assertion lands here as in U7: a recorded fault nothing reads
fails a test.
Tests: `switch_test.go` — an oper-status transition failing `validateOperStatus`
(`switch.go:2121`) records a fault and does not panic; `Err` reports the first;
the existing link-transition cases still pass.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch`

### U11. mibgen validates every OID in a pre-pass
Files: `src/protocol/snmp/cmd/mibgen/emit.go`, `emit_oid_scan_test.go` (new)
After: none

Open question settled: the stage that owns the whole tree the emitters read is
`renderModule` (`emit.go`), which already runs a per-module pre-pass
(`discoverIndicators`) after `checkSourceNames` and before any emitter. The OID
pre-pass goes there, iterating `mod.Nodes`. Because `Emit` runs every module's
`renderModule` before `emitIdentity`, and the identity entries are a subset of
those same module nodes, the per-module pass also covers the identity package —
no separate identity pre-pass is needed.
Change: every OID is validated with `snmp.NewOID` once, before any emitter
runs, and generation fails with that error. `newOIDCall` (`emit.go:298-325`)
keeps its `*jen.Statement` signature and gains a doc sentence citing where
validation happened. That citation is what makes the generated `MustOID` calls
satisfy clause 2, and they do not satisfy it today: the emitter guarantees each
component parses as a `uint32` via `ParseUint(p, 10, 32)`, while `MustOID`
enforces `validateSubs` (`oid.go:94`) — first sub-identifier 0, 1 or 2; second
in `[0,39]` when the first is 0 or 1; at most 128 sub-identifiers. Nothing in
the pipeline checks those: `smi.NewOID` (`src/protocol/smi/model.go:40`)
returns no error and `smi.MaxOIDLength` covers length alone.
The pre-pass is why this is not U6's problem: `newOIDCall` has 11 callers, and
`emit_watch.go:159` and `emit_table.go:92` are inside `jen` closures that
cannot return an error.
The `ParseUint` fallback at `emit.go:313`, which emits a string literal to trip
compilation, becomes dead once validation runs and is removed with it.
Per clause 2 the proof is a test over the artifacts, not the generator: a test
reads the committed generated files, extracts every `MustOID(` literal, and
feeds it to `snmp.NewOID`. That runs on every `go test`, which the generator
alone does not.
Tests: the artifact scan above; a resolved tree carrying an OID whose first arc
is 3 fails generation; one with 129 sub-identifiers fails; one whose second arc
is 40 under a first arc of 1 fails; a regeneration over the golden corpus is
byte-identical — verified, since all 86 `MustOID` calls under `testdata/golden/`
are `1.3.6.1…` and `newOIDCall("")` → `MustOID()` → `NewOID()` returns
`(OID{}, nil)` (`oid.go:46-48`).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/snmp/cmd/mibgen generated`

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
go test -race ./...
cd src/edge/netpen && go test -race ./...
```

The root run does not descend into `src/edge/netpen`, so U4 and U8 need the
second command. The no-panic sweep runs over `.go` files only: README code
samples in `src/common/netsim/vswitch/README.md`,
`src/common/netsim/fabric/README.md`, `src/common/netsim/vswitch/lag/README.md`
and `src/protocol/ssh/README.md` are prose and are not findings.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `go test -race ./...` green at the root and in `src/edge/netpen`.
- [ ] Each `harvest_*.go` regenerates its fixtures byte-identically.
- [ ] No panic remains under `src/` outside a `Must`/`must` function, a
      `_test.go`, a goroutine (phase 4) or `src/protocol/smi/internal/parse`
      (phase 2).
- [ ] Every surviving `Must`/`must` function names its clause-2 answer.
- [ ] Every sticky-fault conversion has a test that fails when the fault is
      recorded and never read.
- [ ] This plan's `status` set, and `U1. Landed:` filled in the parent.

## Open questions

- U11's "resolve stage that builds the tree the emitters read" was settled
  during implementation: it is `renderModule` in `emit.go`. See U11.
