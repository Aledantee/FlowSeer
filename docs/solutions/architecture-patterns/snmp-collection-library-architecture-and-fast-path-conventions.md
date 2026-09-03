---
title: SNMP Collection Library — Architecture and Fast-Path Conventions
date: 2026-08-17
category: architecture-patterns
module: common/snmp
problem_type: architecture_pattern
component: service_layer
severity: high
applies_when:
  - working anywhere under src/common/snmp or the generated MIB packages it feeds
  - adding or editing a guarded raw fast path (rawwalk.go, column.go, fused primitives)
  - changing cmd/mibgen emitters or regenerating generated/go/mib bindings
  - pinning a new off-spec device behavior into the conformance corpus and CONFORMANCE.md
  - touching the SNMP hot path or the tiered integration and benchmark gates
related_components:
  - tooling
  - testing_framework
  - observability
  - documentation
tags:
  - snmp
  - library-architecture
  - code-generation
  - fast-path
  - conformance-corpus
  - integration-testing
  - performance-gate
  - golang
---

# SNMP Collection Library — Architecture and Fast-Path Conventions

## Context

`src/common/snmp` is not a wrapper around a wire library. It owns every byte on
the wire — a hand-rolled SNMP-specific BER codec, a structured PDU model, a
per-session UDP reactor that demultiplexes replies by request-id, full SNMPv3/USM
— plus three streaming primitives (`Walker`, `Watcher`, `TrapStream`) on a shared
channel-pump substrate (`src/common/snmp/doc.go:1`, `src/common/snmp/doc.go:34`).
Around it: a code generator (`cmd/mibgen`) emitting MIB bindings, a
provenance-citing conformance corpus, a four-tier integration harness, and a
benchstat-backed perf gate in its own Go module.

That combination is the friction. The package is deliberately fast *and*
deliberately tolerant of misbehaving agents, and those goals pull opposite ways.
Nearly every performance mechanism is a **guarded fast path** that must decline
rather than guess; nearly every correctness mechanism is a **machine-checked pin**
rather than a comment. A newcomer who reads only the happy path will delete a
`return nil, false` and change decode semantics for real devices, or add a corpus
row that silently vanishes from the generated coverage map.

One caveat before trusting in-tree prose: `src/common/snmp/README.md` has drifted.
It documents a `backend/gosnmp/` package and `gosnmp.Dial`
(`src/common/snmp/README.md:68`), while the wire implementation now lives in the
package behind `NewSession` (`src/common/snmp/backend.go:59`) and a test asserts no
gosnmp identifier survives under the package
(`src/common/snmp/no_gosnmp_test.go:15`). It also points at a nonexistent
`docs/conventions/` (`README.md:118`) and an "AGENTS.md R14" rule (`README.md:85`).
**`doc.go` is authoritative**, as the README itself says (`README.md:21`).

## Guidance

### Layers

Bottom to top, each layer has one job and one mirror obligation: BER codec
(`ber.go`); PDU model (`pdu.go`), which doubles as the trace/fixture representation
(`src/common/snmp/pdu.go:13`); reactor (`reactor.go`) — one socket and one read loop
per session, drop-and-count for anything unmatched or forged; session engine
(`session_engine.go`, `session.go`); streaming primitives (`walker.go`,
`rawwalk.go`, `watcher.go`, `trap.go`) over the generic `pump`; typed surface
(`oid.go`, `varbind.go`, `kind.go`, `column.go`, `tc.go`, `decode.go`, `errors.go`);
generated bindings under `generated/go/mib/<module>/`. `instrument.go` threads
injected OpenTelemetry providers and depends on the OTel **API only, never the
SDK** (`src/common/snmp/doc.go:41`).

### 1 — Every optimization is a guarded fast path that declines

A fast path never assumes a shape it has not checked, and on failure returns
*not-applicable*, never an error. `encodeRequestFast` is the canonical shape,
dispatched at the top of `encodeMessage` (`src/common/snmp/pdu.go:723`):

```go
func encodeMessage(m *message) ([]byte, error) {
	if fast, ok := encodeRequestFast(m); ok {
		return fast, nil
	}
	// ... general encodePDU / append-per-nesting-level chain
}
```

Two guards are the whole contract (`src/common/snmp/pdu.go:776`): the PDU type must
be `pduGetRequest` / `pduGetNextRequest` / `pduGetBulkRequest`
(`pdu.go:779`), and **every** varbind must be a `NullVar` (`pdu.go:785`). A Set, a
trap, or any valued varbind declines. The win comes from `berWrapTail`, which
wraps `buf[mark:]` in a TLV header in place with one overlapping copy, so the
datagram assembles in a single buffer instead of one slice per nesting level
(`pdu.go:743`).

The raw walk path applies the rule per varbind. `RawVarBind` carries undecoded BER
name and value octets aliasing the response buffer
(`src/common/snmp/rawwalk.go:36`), and every fused decoder declines:

```go
func RawInteger32(rv RawVarBind) (int32, bool) {
	if rv.VB != nil || rv.Tag != tagInteger {
		return 0, false
	}
	v, err := decodeSignedInt(rv.Value)
	if err != nil || v < math.MinInt32 || v > math.MaxInt32 {
		return 0, false
	}
	return int32(v), true
}
```

Off-spec tag, exception marker, overflow, or a pre-decoded `VB` all yield
`ok=false` (`src/common/snmp/rawwalk.go:89`, `:135`); the caller must fall back to
`RawVarBind.Decode()` plus the column's generic decoder (`rawwalk.go:46`) so error
text and coercion semantics stay identical to `decode.go`. The file header states
this as the package rule (`rawwalk.go:11`).

The decline also happens a layer down. Raw delivery requires the response to pass
mirror validation; otherwise the read loop decodes eagerly and consumers see
pre-decoded varbinds (`src/common/snmp/reactor.go:763`):

```go
if raw && validateRawVarBindList(rawVBL, 1) == nil {
	m.pdu.rawVBL = cloneBytes(rawVBL)
} else {
	vbs, verr := decodeVarBindList(rawVBL, 1)
	// ...
}
```

`validateRawNameOID` rejects a non-minimally-encoded arc (any `0x80` lead octet)
with `errNonCanonicalOID` (`src/common/snmp/pdu.go:509`), because the byte-order
walk guards are only correct on canonical encodings.

### 2 — A fast path is pinned to the path it replaces

Declining is only safe if "identical" is machine-checked. Three patterns exist:

- **Byte-for-byte encoder equivalence.** `TestEncodeRequestFastEquivalence` builds
  representative messages (long-form lengths, a 150-byte community, 8 varbinds,
  `requestID` at `math.MaxInt32` and `-1`), asserts `bytes.Equal(fast, slow)`
  against the general chain, then asserts the fast path *declines* a Set and a
  non-null varbind (`src/common/snmp/rawwalk_test.go:325`). Accepting too much is
  as much a bug as encoding wrong.
- **Engine differential.** `runWalkRaw` is the raw twin of `runWalk` with identical
  guard logic and termination priority over canonical BER name octets; its doc
  comment states the mirror obligation (`src/common/snmp/session_engine.go:457`),
  and `TestBulkWalkRaw_DifferentialWithBulkWalk` drives both engines through clean
  walk, `tooBig` degrade, and mid-walk `EndOfMibView`
  (`src/common/snmp/rawwalk_test.go:78`).
- **Validator/decoder mirror.** `validateRawValue` decides accept/reject without
  materializing a variant; any change to `decodeValue`'s error surface must be
  mirrored (`src/common/snmp/pdu.go:539`), pinned by
  `TestValidateRawValue_MirrorsDecodeValue` over every known tag plus three
  unknown ones (`src/common/snmp/rawwalk_test.go:164`).

One invariant underwrites the byte-keyed design: for canonically-encoded OIDs,
lexicographic byte order of the BER content octets equals numeric arc order, and
byte-prefix equals arc-prefix. `TestOID_WireByteOrder_Property` checks 20 000
random pairs across all base-128 length classes against `OID.Compare`
(`src/common/snmp/rawwalk_test.go:16`). `cmpOIDWire` documents why plain
`bytes.Compare` is *not* arc order — groups of differing length must compare
length-first (`src/common/snmp/rawwalk.go:186`).

### 3 — Wire bytes are the dispatch key, computed once

`OID.WireBytes()` returns freshly-allocated BER content octets
(`src/common/snmp/oid.go:156`); `OID.WireKey()` returns them as an immutable string
so a lookup with raw wire bytes (`map[string(b)]`) is allocation-free
(`oid.go:166`). `AnyColumn.Key()` is that wire key **precomputed at construction**
(`src/common/snmp/column.go:14`), stored by `NewColumn` (`column.go:44`):

```go
return Column[T]{oid: oid, key: oid.WireKey(), kind: kind, decode: decode}
```

Generated packages key both maps on wire keys, not dotted strings: the
OID→`AnyColumn` dispatch map (`src/common/snmp/cmd/mibgen/emit_dispatch.go:9`,
entries at `:47`) and the column→`snmp.Tier` map
(`src/common/snmp/cmd/mibgen/emit_tier.go:79`, keys at `:91`). Both identifiers are
module-prefixed to avoid a guaranteed collision if two generated files land in one
package (`emit_dispatch.go:15`, `emit_tier.go:61`). New hot-path lookups key on
`WireKey`/`Key()` — never format a dotted OID in a loop.

### 4 — Generated code uses only the public API, and moves with its generator

`mibgen` emits one package per MIB module using only `snmp`'s exported surface:
typed scalar accessors, `snmp.NewColumn`, row structs and Walkers, SMI enums, the
dispatch map, `snmp.Decode*` for textual conventions
(`src/common/snmp/cmd/mibgen/doc.go:1`). The generated fused table walker rides
`Session.BulkWalkRaw` (`emit_table.go:168`; interface method at
`src/common/snmp/session.go:53`) and emits per column a fused arm with a generic
`else` (`emit_table.go:359`) — Convention 1 expressed in the emitter.

- CLI is `go run ./src/common/snmp/cmd/mibgen`, with `-verify`, `-check` (fail on
  output drift), and `-update` (`src/common/snmp/cmd/mibgen/main.go:41`; defaults
  at `main.go:12`). The repository-root `generate.go` carries the `go:generate`
  directive, so `go generate .` at the root is the normal entry point. There is
  no `tool` directive for mibgen in `go.mod`.
- **`search_paths` resolve relative to the config file's own directory**
  (`src/common/snmp/cmd/mibgen/config.go:183`). `mibgen.yaml` lives at the
  repository root, so its entries are bare repo-relative paths (`spec/mib/ietf`).
  Moving the config changes that depth.
- An emitter change is not done until the golden fixture is refreshed:
  `go test ./src/common/snmp/cmd/mibgen -run TestEmit_FakeMIB_Golden -update-golden`
  (`src/common/snmp/cmd/mibgen/doc.go:26`).
- **Change-indicator discovery is structural first, config only as the
  exception** (`emit_discovery.go:94`). Precedence: (1) a per-row column inside
  the table matching the indicator name-suffix heuristic; (2) a `mibgen.yaml`
  `indicators:` declaration; (3) a module scalar named `<table><suffix>` or
  `<base><suffix>` with a trailing `Table` stripped (`ifStackLastChange` →
  `ifStackTable`). Rule 3 covers the conventional SMIv2 spelling, so most
  modules need no config at all. Explicit `indicators:` remain necessary only
  where the name carries no correlation — ENTITY-MIB's single
  `entLastChangeTime` fans out across five tables in three sibling subtrees and
  fits no structural rule.
- Tier classification is codegen-time and rule-based — Counter32/64 →
  `TierCounter`; TC `TimeStamp` → `TierIndicator`; indicator name-suffix →
  `TierIndicator`; else `TierState`; `TierStatic` is never auto-assigned
  (`emit_tier.go:22`). The tier map emits only for modules with a Watch-eligible
  table, avoiding generated bloat (`emit_tier.go:53`).

### 5 — Off-spec device behavior is corpus-pinned, with provenance

`conformance_corpus_test.go` is the manifest: one `corpusRow` per quirk with
`Clause`, `Provenance`, `Behavior`, `Adversarial`, `Status`
(`src/common/snmp/conformance_corpus_test.go:59`; rows from `:79`). Editing rules
are in the file: never delete a row; flip to `covered` only with a citing test and
a named adversarial input; flip to `accepted-risk` only with a rationale **and** an
`acceptedRiskAllowlist` entry (`:74`, allowlist `:131`). `validateRow` is the gate
predicate, factored out so `TestConformanceGate_RejectsBadRows` can prove it bites
(`:175`, `:340`). A `covered` row must be cited by a
`// Covers conformance matrix row: <id>` comment, found by an AST scan of every
`_test.go` in the package dir (`:199`, `:240`). The gate cannot prove the cited
test drives the input — which is why `Adversarial` exists, to make that review
concrete and diffable (`:33`).

The two raw-path rows show the discipline, as the corpus records it:

- `raw-wrong-typed-column` — provenance `telegraf #14598; snmp_exporter #338`,
  described as proprietary/buggy agents reporting types diverging from the MIB
  declaration (`:117`). Pinned in-package by
  `TestRawWalk_WrongTypedColumn_FallsBackToGenericCoercion`
  (`src/common/snmp/conformance_rawpath_test.go:19`) **and** end-to-end by a t3
  `snmprec` replay (`test/integration/snmp/t3_offspec_test.go:29`; manifest
  entry `test/integration/snmp/testdata/snmprec/manifest.yaml:29`).
- `raw-noncanonical-oid-arc` — provenance `chemist/snmp #17`, agents emitting BER
  that is valid but not shortest-form (`:118`); pinned by
  `TestRawWalk_NonCanonicalOIDArc_EagerFallback`
  (`conformance_rawpath_test.go:185`).

Committed state: 33 covered, 3 accepted-risk, 0 pending, 36 total
(`src/common/snmp/CONFORMANCE.md:6`).

### 6 — The matrix generator's one truncation hazard is guarded

`CONFORMANCE.md` is generated: `TestConformanceMatrixUpToDate` golden-compares it
and `UPDATE_CONFORMANCE=1` regenerates it in place
(`src/common/snmp/conformance_corpus_test.go:441`, `:449`). The renderer groups rows
into sections by **ID prefix** from a hoisted `conformanceFamilies` list — `enc-`,
`walk-`, `txp-`, `raw-`, `usm-` (`:374`).

The hazard: the status tally iterates all rows, but each table prints only rows
matching that family's prefix (`:406`). A row matching no family is **counted in
the tally yet printed in no table**. The guard lives in the always-on integrity
test and names the failure mode (`:279`):

```go
if !inFamily {
	t.Errorf("row %q matches no conformanceFamilies prefix — it would be omitted from every CONFORMANCE.md table", r.ID)
}
```

So a new row either fits an existing prefix or you add a family. The list is
hoisted to package scope so guard and renderer read one source (`:369`). The
sibling backstop is `TestConformanceCorpusEnumeration`, which pins
`kindBaselineTest` to the `Kind` enum via `wantKinds = 18` and probes
`Kind(wantKinds+1).String() != "Kind(?)"`, so adding a `Kind` without baseline
coverage fails the gate (`:299`, `:169`).

### 7 — Optimization is measured, in isolation, against a committed baseline

The bench suite is a **separate Go module** so gosnmp — kept only as a comparand —
never re-enters the main dependency graph and `no_gosnmp_test.go` stays green
(`src/common/snmp/bench/go.mod:1`). Micro benchmarks run both arms as
`impl=flowseer` / `impl=gosnmp` sub-benchmarks
(`src/common/snmp/bench/micro_test.go:86`, `:100`); heavier harnesses are
build-tagged (`snmp_bench_fanout`, `snmp_bench_gc`, `snmp_bench_netsnmp`,
`snmp_bench_macro`).

`bench-gate.sh` runs the micro benchmarks at `COUNT=10`, filters to the FlowSeer
arm, and benchstat-compares against committed
`src/common/snmp/bench/testdata/baseline-micro.txt`
(`src/common/snmp/bench/bench-gate.sh:35`, `:51`, `:53`). Only deterministic
metrics hard-fail — `allocs/op` and `B/op`; `sec/op` is advisory unless
`GATE_NS=1` (local only), and throughput/GC/RSS are not gated at all, because
gating noisy metrics erodes trust in the gate (`:7`, `:69`, `:78`). It keys off
benchstat's own significance verdict so high-variance benchmarks read `~` and do
not false-trip (`:18`), and it **never rewrites the baseline** — rebaselining is a
deliberate reviewed commit (`:22`).

The committed baseline records `BenchmarkGet/impl=flowseer` at 56 allocs/op and
1824 B/op, `BenchmarkGetNext` at 56 allocs/op, `BenchmarkGetBulk` at 97, and
`BenchmarkBulkWalk` at 481 (`src/common/snmp/bench/testdata/baseline-micro.txt:5`, `:15`, `:25`,
`:35`).

Per the 2026-08-16 session history, that `56` is post-optimization: four
prototypes were each benchmarked against a fresh baseline with benchstat at
`n=10` and fully reverted before the next; three were adopted. Experiment 4 — the
single-buffer request encode now called `encodeRequestFast` — measured −23%
allocs/op on Get/GetNext (73→56) and −10% B/op with latency unchanged (session
history); experiment 3 was the byte-keyed dispatch of Convention 3 (session
history). The bench Taskfile carries the profile-first rule: never optimize a site
you have not first seen in a profile (`src/common/snmp/bench/Taskfile.yml`,
`profile` task).

### Harnesses at a glance

| Harness | Where | Gate |
| --- | --- | --- |
| Corpus integrity + family guard | `conformance_corpus_test.go:264` | always on |
| Corpus completeness (no `pending`) | `conformance_complete_test.go:1` | tag `snmp_conformance_complete` |
| Coverage-map freshness | `conformance_corpus_test.go:441` | always on |
| Raw-path off-spec pins | `conformance_rawpath_test.go` | always on |
| Misbehaving-responder scenarios | `misbehaving_responder_test.go:198`+ | always on |
| No gosnmp identifier leaks | `no_gosnmp_test.go:15` | always on |
| Integration t1–t4 | `test/integration/snmp/` | tags `snmp_integration_t1..t4` |
| Perf | `src/common/snmp/bench/bench-gate.sh` | `task bench:gate` |

Bare `go test ./...` runs zero integration tests by design; each tier owns its
container/lab lifecycle and selecting two tier tags at once is a deliberate
compile error (`src/common/snmp/README.md:109`).

## Why This Matters

**Declining costs nothing; guessing costs correctness on real devices.** The two
largest measured wins here are safe only because they hand off on anything
unexpected. Turning a decline into an error would make `snmp` *stricter* than the
general path for exactly the agents the corpus exists to tolerate — a
Counter32-declared column arriving Gauge32-tagged would start failing walks that
previously coerced cleanly. Turning it into a silent best-effort decode would
change values, not just errors.

**Unpinned equivalence rots silently.** `encodeRequestFast`/`encodeMessage`,
`runWalkRaw`/`runWalk`, `validateRawValue`/`decodeValue` are three pairs that must
agree, and nothing in the type system enforces it. Editing one half without its
differential test is how a hand-rolled codec starts emitting subtly wrong bytes
that only one vendor notices.

**A silently-omitted corpus row is worse than a missing one.** The family guard
exists because tally and tables iterate differently: a mis-prefixed row inflates
"33 covered" while appearing nowhere a reviewer looks — invisible in a green test
run *and* in the rendered document. It has bitten this repo once.

**A gate that false-trips gets switched off.** Narrowing to `allocs/op` and `B/op`
is why the perf gate is usable on shared hardware; widening it to `ns/op` would
produce regressions from `ColdStart` variance (±680% on the committed baseline,
`bench-gate.sh:11`). Letting the gate rewrite the baseline would convert every
regression into a silent rebaseline.

**Generated code drifting from its generator is unreviewable.** `-check` makes
drift a failure rather than a surprise diff. And because bindings use only the
public API, a change to an unexported helper can never break them — but a change
to `NewColumn`, `AnyColumn`, `RawVarBind`, or a `Raw*` primitive breaks every
emitted package at once.

## When to Apply

- Before adding or modifying any fast path in `pdu.go`, `rawwalk.go`,
  `session_engine.go`, or `reactor.go` — decline-and-fall-through is mandatory, and
  a decline must never surface as an error.
- Before touching `encodeRequestFast`'s guards, `berWrapTail`, or the
  `validateRaw*` family: extend the equivalence/mirror test in the same change.
- When adding a hot-path lookup: key on `OID.WireKey()` / `AnyColumn.Key()`.
- When adding a conformance row: pick a `conformanceFamilies` prefix (or add a
  family), supply `Adversarial`, add the `// Covers conformance matrix row: <id>`
  marker, and regenerate with `UPDATE_CONFORMANCE=1`.
- When marking a row `accepted-risk`: write the `Accepted` rationale *and* the
  allowlist entry — the gate rejects one without the other.
- When adding a `Kind`: bump `wantKinds` and add a `kindBaselineTest` entry in the
  same commit.
- When changing the `mibgen` emitter or config: regenerate bindings, refresh the
  golden fixture, re-check `search_paths` depth if the config moved.
- When claiming a performance improvement: benchmark in isolation against a fresh
  baseline at `COUNT=10`, revert before trying the next idea, run
  `task bench:gate` before declaring done.
- When documenting package behavior: put it in `doc.go`; the README is a map and
  has already drifted.

## Examples

**Adding a fused decoder for a new Kind.** Follow `rawUint32`
(`src/common/snmp/rawwalk.go:135`): reject pre-decoded (`rv.VB != nil`), reject a
non-matching tag, reject decode error and range overflow — all as `ok=false`. Wire
`RawFuse` into the emitter's column info
(`src/common/snmp/cmd/mibgen/emit_table.go:410`) so the generated switch emits the
fused arm with its generic `else` (`emit_table.go:359`), regenerate, and extend
`TestRawPrimitives_FusedAndDecline` (`src/common/snmp/rawwalk_test.go:234`) on both
the accepting and declining sides.

**What "identical to the generic path" means concretely.** The
`raw-wrong-typed-column` row specifies that values *and errors* match
(`conformance_corpus_test.go:117`), and the t3 replay proves it end-to-end: the
capture serves `ifIndex`/`ifType`/`ifOperStatus` (Integer32-declared) as Gauge32,
`ifSpeed` (Gauge32-declared) as Counter32, `ifLastChange` (TimeTicks-declared) as
INTEGER, and `ifInOctets` (Counter32-declared) as Gauge32; every fused arm must
decline and the generic coercion must still yield the exact expected row values
(`test/integration/snmp/t3_offspec_test.go:21`).

**Why the non-canonical-OID row is a fallback, not a rejection.** A name OID with a
redundant `0x80` continuation octet is refused raw delivery by
`validateRawNameOID` (`src/common/snmp/pdu.go:509`), the read loop decodes eagerly
(`reactor.go:763`), and the walk yields identical data through pre-decoded
varbinds — so `cmpOIDWire` never sees an arc it is not correct for
(`conformance_corpus_test.go:118`). Tolerance is delivered by *degrading the
optimization*, not by loosening validation.

**Adapting a non-wire `Session`.** `RawWalkerFromWalker` re-encodes each
`(OID, VarBind)` pair into a pre-decoded `RawVarBind` with `VB` set
(`src/common/snmp/rawwalk.go:309`); fakes, recorders, and middleware satisfy
`BulkWalkRaw` this way and consumers take their generic arm for every varbind.
That is why `rv.VB != nil` is the first check in every fused primitive.

**Accepted risk, done properly.** `enc-unsigned-as-signed` records that an
INTEGER-tagged value with the MSB set decodes to a negative `Integer32Var` and
coercion to `uint32` is rejected as `ErrLossyConversion`
(`src/common/snmp/errors.go:166`) rather than reinterpreted — with a written
rationale (a negative INTEGER is indistinguishable from a genuine `-1`), a named
pinning test, and an allowlist entry (`conformance_corpus_test.go:90`, `:132`).

## Related

- `src/common/snmp/doc.go` — authoritative package reference, including the
  Adaptive Watch matrix mapping behaviors to public symbols and pinning tests
  (`doc.go:90`).
- `src/common/snmp/CONFORMANCE.md` — generated coverage map; regenerate, never
  hand-edit.
- `docs/code-style.md` — repo-wide Go conventions; the agent rules
  (`docs/code-style.md:280`) and merge gate (`:270`) govern every change here.
- `test/integration/snmp/README.md` — per-tier prerequisites and
  walkthroughs.
- Stale prose to fix when next in the area: `src/common/snmp/README.md:68`
  (gosnmp backend section), `:85` (AGENTS.md R14), `:118` (`docs/conventions/`);
  and `test/integration/snmp/doc.go:8`, which says "Three independent
  tiers" and documents only t1–t3 while a build-tag-gated, opt-in t4 live-device
  tier exists (`test/integration/snmp/t4_main_test.go:1`).
