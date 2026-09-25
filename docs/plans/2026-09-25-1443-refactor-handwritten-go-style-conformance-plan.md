---
title: Hand-Written Go Style Conformance - Plan
type: refactor
date: 2026-09-25
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: code
---

# Hand-Written Go Style Conformance - Plan

> Implemented. 6 units, 2026-09-25T14:50:27Z to 2026-09-25T17:55:26Z.

## Goal

Hand-written Go under `src/` and `test/` conforms to the judgement rules in
[`code-style.md`](../code-style.md) and [`doc-style.md`](../doc-style.md) that no
linter checks: comments explain why instead of restating the line, none carries a
planning identifier or a development-history clause, a type whose values cross
goroutines says whether it is safe for concurrent use, test failures print got
before want, and the two contracts the audit found false (`captureapi.Store`'s
concurrency claim, `deviceapi.KVWatcher`'s stop function) hold. The means: one
pass per package tree over the sites named below, each landing with the repository
verifier on its paths.

Stop condition: if renaming a `trace.Layer` or `analysis.IssueCode` identifier in
Unit 1 also changes its string value, that item drops — those values are compared
in golden traces and conformance corpora.

## Decisions

- Units are cut by file set, one per package tree, `src/common` split at
  `netsim`. Why: the trees share no file, so no unit's verifier run reaches
  another's packages, and `netsim` alone holds 70 of the 207 got/want sites, all
  29 planning-identifier sites in Go, and the renames. The one `After:` edge, U2
  on U3, is a citation rather than a shared file.
- Six units, one plan, and no phase split, although the plan runs past
  [`phases.md`](../../.claude/skills/plan/references/phases.md)'s 300-line
  trigger and its six clusters are that reference's split condition. Why: the
  length is the audit's site inventory, not decisions. A split would mark phases 2
  to 6 `needs-decisions`, and their re-plan would have to re-derive that inventory
  across 1,228 files, which is the work this plan exists to preserve. The ledger
  `implement` keeps carries units across sessions the same way it carries phases.
  **Unconfirmed.**
- Requirement 6 covers the 51 exported types the Units enumerate: those whose
  values or implementations cross goroutines somewhere in this repository. That
  includes a plain value struct a concurrent reader holds
  (`netsimload.{LatencyStats,FlowObservation,Observation}`, whose sibling
  `Accumulator` already states its contract), a struct whose only method is
  `Clone` (`fabric.PhyAssumption`), and an interface whose implementations decide
  the answer (`capture.Source`). It leaves the remaining 416 exported types alone.
  Why: `code-style.md` Concurrency asks every exported type for the statement, and
  `doc-style.md` says to delete a comment that is the type name in more words; the
  line between them is whether a reader can put the value on two goroutines.
  **Unconfirmed.**
- `captureapi.NewStore` takes the clock; `SetClock` goes. Why: `Store`'s doc says
  "Safe for concurrent use" while `SetClock` assigns `s.clock` outside `s.mu` and
  `DeleteSession`, `pruneDeletedLocked` and `SweepExpired` read it, so the stated
  contract is false. Breaking the signature is this repository's remedy
  ([`AGENTS.md`](../../AGENTS.md), Agent behavior). **Unconfirmed.**
- `port.{LayerVlan,LayerStp,LayerLag,LayerPoe,Lag}` and
  `netmodel.{IssueInvalidVlanID,IssueConflictVlan,IssueInvalidPoePriority}` are
  renamed to keep their initialisms' case; every string value stays identical.
  Why: `code-style.md` Naming keeps `ID`, `VLAN`, `STP`, `LAG`, `PoE` cased, and
  `ReasonMTUExceeded` in the same file already does. `revive`'s `var-naming`
  carries golint's initialism list, which holds none of these four, so no linter
  sees them. **Unconfirmed.**
- `captureapi.Store.GetSession` becomes `Store.Session` (requirement 16). Why: a
  store accessor, not a proto-derived RPC handler, so the prefix has no
  exemption. **Unconfirmed.**
- 140 of the 156 verbless `fmt.Errorf` calls stay out of scope; the 16 the Units
  name are converted (requirement 15). Why: the durable fix for the rest is
  `perfsprint`'s `errorf` check in `.golangci.yml`, which needs the guardrail
  review `AGENTS.md` requires of a policy surface. The 16 are each a bare sentence
  in a function that already returns plain errors, so converting them costs
  nothing and leaves no half-applied rule inside a file. **Unconfirmed.**
- The panic `make(chan T, buf)` raises on a negative `buf` inside `pump.New` is
  read as outside `code-style.md` Panics' Named clause, which governs where a
  `panic` statement may appear; this one is raised by the runtime. The Documented
  and Handled clauses still apply, and U3 supplies the proven answer by rejecting
  a negative buffer at the three boundaries that accept a caller-supplied
  capacity. Why not rename `New` to `MustNew`: the panic is unreachable once those
  boundaries reject, and `code-style.md` Remedies prefers making the branch
  unreachable over a new name. **Unconfirmed.**

## Requirements

1. No process-narration comment remains. Acceptance:
   `src/services/device/internal/edge/verifier.go:160` carries no comment, the
   eight `// Step N:` prefixes in that file are gone, and
   `rg -n '^\s*//\s*Step [0-9]' src test` prints no line.
2. No Go or hand-written configuration comment carries a planning or requirement
   identifier. Acceptance:
   `src/common/netsim/vswitch/diff_coverage_test.go:23` says "every exported
   `vswitch.Config` field reaches `Diff`" in place of "R9's gate", and
   `src/protocol/yang/cmd/yanggen/yanggen.yaml:1` names the vendored surface
   without `(KD5)`.
3. No comment describes development history or unbuilt work. Acceptance:
   `src/protocol/snmp/decode.go:18` states that generated decoders coerce, with
   no "previously generated".
4. No banner or section-divider comment remains, in any comment syntax.
   Acceptance: `src/protocol/snmp/decode.go:10`'s `// Wire-type leniency helpers`
   title line is gone while the coercion table below it stays, and
   `src/edge/netpen/Taskfile.yml` holds no `# --- … ---` line.
5. A failure comparing two values prints got before want. Acceptance:
   `t.Fatalf("expected PENDING lifecycle, got: %v", got)` becomes
   `t.Errorf("got lifecycle %v, want PENDING", got)`, at all 207 sites the Units
   count.
6. Every exported type whose values or implementations cross goroutines states
   its concurrent-use contract — the 51 the Units enumerate. Acceptance:
   `captureapi.Broadcaster`'s doc says "A Broadcaster is safe for concurrent
   use."; `netsimtest.Registry`'s says it is not.
7. `captureapi.Store`'s concurrency contract holds. Acceptance: `NewStore` takes
   the clock, no `SetClock` method exists, all three test callers reach the
   clock through the constructor, and `store_test.go`'s and
   `operator_service_test.go`'s expiry cases still assert both sides of an
   artifact's expiry. A `-race` run is a regression guard here, not evidence: see
   Verification.
8. One package comment per package. Acceptance:
   `src/services/device/internal/captureapi/store.go` begins `package
   captureapi`; only `doc.go` carries the comment.
9. Exported netsim identifiers keep their initialisms cased with unchanged
   values. Acceptance: `port.LayerVLAN == "vlan"`, `port.LAG == "Lag"`,
   `netmodel.IssueInvalidVLANID == "netmodel.vlan.invalid_id"`.
10. A stop function a caller may call twice is idempotent. Acceptance:
    `deviceapi.KVWatcher.Watch`'s second return is `sync.OnceFunc`-wrapped, and a
    test calls it twice and reaches the next statement. That test panics with
    "close of closed channel" against the current code.
11. A doc comment that only paraphrases its signature states the contract.
    Acceptance: `captureapi.OperatorService.GetCaptureSession`'s doc says it
    answers `CodeNotFound` for a session the store does not hold.
12. Every function that can panic documents the panic, its invariant, and which
    of `code-style.md` Panics' three handling answers it relies on. Acceptance:
    `netsimtest.mustBuildPortTable`'s doc at
    `src/common/netsim/internal/netsimtest/diffcoverage.go:567` names the proven
    answer and the test that runs it.
13. No paragraph of a comment holds three em-dashes, and no comment uses a
    trailing participle, negative parallelism, puffery, vague attribution, or
    capitals for emphasis. Acceptance:
    `src/common/netsim/fabric/scenario.go:489` ends its sentence instead of
    ", ensuring index assignment does not introduce false diffs", and
    `src/protocol/snmp/reactor.go:107-112` — the one paragraph in scope with three
    em-dashes — keeps one.
14. Every `sync.Mutex`/`RWMutex` field says which fields it guards, on the
    struct. Acceptance: `src/protocol/syslog/receiver.go:71` reads
    `mu sync.Mutex // guards connections and terminal`. 57 sites: `src/common` 3,
    `src/protocol` 16, `src/modules` 22, `src/services` 5, `src/edge` 11.
15. The 16 verbless `fmt.Errorf` calls the Units name use `errors.New`.
    Acceptance: `src/common/service/bus.go:200,219` and
    `src/edge/netpen/layers/hsrp.go:104` call `errors.New` with their strings
    unchanged.
16. No `Get` prefix on a plain accessor. Acceptance: `captureapi.Store.Session`
    exists and `Store.GetSession` does not.

## Out of scope

- The 416 exported types whose values do not cross goroutines. Requirement 6 is
  scoped to the 51 that do.
- 140 verbless `fmt.Errorf` calls in 29 files (largest:
  `netsimtest/comparison_cases.go` 42, `service/delivery.go` 13,
  `netsim/stream/stream.go` 9). Requirement 15 takes the other 16. See Open
  questions.
- The ~40 gopacket `LayerType`/`CanDecode`/`NextLayerType` doc comments in
  `src/edge/netpen/layers/`. `revive`'s `exported` rule requires a comment on
  each and the only contract they carry (the single layer type, no sub-layers) is
  already the sentence.
- `X, not Y` across `src/modules` (~40 non-test comments) and `X rather than Y`
  across `src/services`. Both are the repository's own voice; the rule bites on a
  habit, and that judgement is the owner's.
- `.golangci.yml`, `AGENTS.md`, `tools/hooks/`, `generated/`, `buf.lock`.
- Files another session is editing, which this plan does not touch:
  `src/protocol/snmp/cmd/mibgen/**`, `src/protocol/yang/cmd/yanggen/*.go`,
  `src/protocol/internal/goname/**`,
  `src/protocol/snmp/bench/{streaming_memory,tablewalk,tablewalk_parity}_test.go`,
  `src/protocol/gnmi/watch_test.go`,
  `src/protocol/netconf/test/integration/t4_{lab,manual_verify}_test.go`,
  `src/protocol/restconf/test/integration/t4_lab_test.go`,
  `src/protocol/snmp/test/integration/{assertions.go,assertions_test.go,
  presence_test.go,streaming_walk_test.go,t1_walk_test.go,t1_watch_test.go,
  t2_collector_test.go,t3_offspec_test.go,t3_replay_test.go,
  t4_manual_verify_test.go}`, `src/modules/localnet/collect/*`,
  `src/modules/localnet/snmpmap/{ifmib,lldp,phy,phy_ddm}*.go`,
  `src/modules/localnet/access/fakewalk_test.go`,
  `src/modules/localnet/access/internal/capability/interfaces/snmp_test.go`, and
  every other file importing `generated/go/{mib,yang}`. Three findings fall in
  them and are not fixed here: `src/modules/localnet/snmpmap/lldp_test.go:152`
  ("pins KTD6"), `src/protocol/snmp/test/integration/t1_watch_test.go:82,87`
  (follow-up clauses), and `src/protocol/snmp/test/integration/assertions.go:29`
  (a trailing participle).
- New behavior. Requirements 7 and 10 change behavior; every other requirement
  changes comments, failure text, or an identifier. The tests the Units add
  beyond those two are assertions missing from tests this plan already touches
  (`vswitch/fork_test.go`, three `test/conformance/` fixtures,
  `captureapi/edge_service_test.go:111`) plus the `capture.Config` host test
  `code-style.md` Testing requires of every module.

## Units

### U1. netsim comments, messages, and initialisms
Files: `src/common/netsim/`
After: none
Change, by requirement:
- R1, R2 and R4 are grep-derivable in this tree. Run the Verification commands
  scoped to `src/common/netsim`: 29 planning-label sites (all in `vswitch/**` and
  `fabric/**` test files plus `vswitch/switch.go:1968` and
  `internal/netsimtest/diffcoverage_test.go:15`; the `R1` and `R2` in
  `fabric/stp_test.go:1456` are region names, not labels — leave them), and the
  16 `// -----` dividers in `internal/netsimtest/comparison_cases.go`.
- Comments that restate the call beneath them, 15 sites:
  `vswitch/fork_test.go:65,86,97,141,154,169,182,186,204,208,223,318,321`;
  `internal/netsimtest/cases.go:280`; `fabric/config.go:770`.
  `internal/netsimtest/diffcoverage.go:134` is not one of these: it is the only
  statement of why `filepath.Join` climbs five levels. Keep it, or move it into
  `repoRoot`'s doc at `:125`.
- Two of those comments claim an assertion the test does not make. Add it:
  `sw.Groups(10)` learned the group before `fork_test.go:136` asserts the fork
  did not, and `sw.Roles()["1/1/1"]` reached `RoleDisabled` before `:67` asserts
  the fork's did not. Both assertions pass today if the source-side action did
  nothing, which is what `code-style.md` Testing means by asserting the state
  before the outcome.
- `internal/netsimtest/diffcoverage.go:357`: delete the stale first copy of
  `mutateLeaf`'s doc comment, which says it panics while the function returns an
  error.
- R12, 4 sites: `internal/netsimtest/diffcoverage.go:567` (`mustBuildPortTable`
  states the invariant but names no handling answer — name the proven one and the
  test that runs it); `internal/netsimtest/scale.go:275` documents `mustNil`'s
  invariant (the topology is built from in-package constants) and its answer;
  `scale.go:49,64` document the panic they inherit from it.
- R6, 15 sites: `internal/netsimtest/corpus.go:751` (`Registry`, not safe);
  `fabric/config.go:509`; `search/fault.go:28`; `search/l2.go:35`;
  `vswitch/bridge/{bridge.go:443,config.go:31,config.go:150,result.go:63}`;
  `vswitch/filter/{config.go:49,config.go:69,filter.go:161}`;
  `vswitch/phy/{ethernet.go:70,ethernet.go:87,poe.go:154}`;
  `vswitch/stp/bpdu.go:130`.
- R11: `internal/netsimtest/corpus.go:756` (`NewRegistry`);
  `vswitch/port/port.go:84` (`Kind.Canonical`, state the stable token).
- R3: `vswitch/routing/neighbor_test.go:637`; `fabric/routing_test.go:634,756`;
  `vswitch/stp/tree_internal_test.go:46`; `vswitch/lag/config_test.go:236`.
- R13: `internal/netsimtest/diffcoverage.go:99,469` (negative parallelism, the
  same sentence twice); `fabric/scenario.go:489` and `vswitch/stp/config.go:3`
  (trailing participles).
- R5, 70 sites: the got/want grep scoped to `src/common/netsim`. The weight is in
  `vswitch/netmodel/` (36), `vswitch/switch_test.go` (9) and `vswitch/routing/`
  (11); the rest are one or two per file across 12 files.
- R9: the eight identifiers in Decisions. References are found with
  `rg -n '\b(LayerVlan|LayerStp|LayerLag|LayerPoe|IssueInvalidVlanID|IssueConflictVlan|IssueInvalidPoePriority)\b' src`
  plus, for `Lag`, both `rg -n '\bport\.Lag\b' src` and
  `rg -n 'Lag\b' src/common/netsim/vswitch/port` — inside package `port` the
  constant is unqualified at `port.go:72,231,374,471,487`, which the qualified
  pattern misses. Rename the identifier only; the string literal on the right of
  each `=` stays byte-identical.
- R14, 1 site: `vswitch/mcast/layer.go:105`.

Tests: the two added state assertions in `vswitch/fork_test.go`. The rename's
risk — a changed constant *value* — is caught by the existing golden trace and
corpus comparisons in `internal/netsimtest/` and `fabric/`, which compare the
string values; the message changes are held by the suites already asserting on
them.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim`

### U2. Foundations and conformance suites
Files: `src/common/errs/`, `src/common/pump/`, `src/common/service/`,
`src/common/spawn/`, `src/common/net/`, `test/conformance/`
After: U3. `pump.New`'s doc comment here claims the proven handling answer for
its panic, and that claim is true only once U3 has landed the buffer rejection in
`protocol/yang/watch.go` and `protocol/gnmi/{watch,subscribe}.go` that the comment
cites. No file is shared; the dependency is the citation, and a comment landing
first would be the "check that ran once" `code-style.md` Panics warns about.
Change, by requirement:
- R1: `pump/pump.go:132,187` keep only the reason (Go's `select` picks randomly
  among ready cases).
- R12: `pump/pump.go:59` documents the invariant (`buf` is never negative), names
  the proven answer, and cites the three boundaries U3 makes reject a negative
  capacity. It does not claim the panic is init-time: `New` is called at run time
  by `protocol/{snmp,gnmi,yang}` and `modules/capture`.
- R11: `net/pcap/reader.go:41`; `net/netaddr/mac.go:111` (accepted separators and
  error contract, as `Parse` at `:38` states them).
- R3: `errs/code_test.go:133` states what `NewCode` does, not what it stopped
  doing.
- R15, 5 sites: `service/bus.go:200,219`;
  `test/conformance/proto/layout_test.go:550,556,561`.
- R11 and R3 under `test/conformance/`: `proto/layout_test.go:149` and
  `proto/field_constraint_class_test.go:187,196,427` say the positive form;
  `proto/field_constraint_class_test.go:158,215` drop the inflated lead-in;
  `proto/field_constraint_class_test.go:20,42,447`, `proto/layering_test.go:94`
  and `panic/panic_policy_test.go:23,35` state the rule instead of today's tree or
  the day someone adds a sibling; `proto/model_edge_rules_test.go:325` and
  `proto/model_attribute_rules_test.go:190` start with the test's name.
- R5, 7 sites: `proto/phy_rules_test.go:382,385`;
  `dependencies/no_as_test.go:78`; `proto/runtime_message_rules_test.go:187,205`;
  `proto/runtime_bus_rules_test.go:180`; `proto/ip_rules_test.go:247`.
- `proto/field_constraint_class_test.go:260` runs its seven cases as `t.Run`
  subtests.
- Three fixtures pass `protovalidate.Validate` where they are built:
  `proto/model_edge_rules_test.go:368,385`; `proto/interface_rules_test.go:130`.
- R14, 2 sites: `service/telemetry_sdk.go:406,423`.
- `spawn/spawn.go:42` is **not** an R13 site: its five em-dashes sit in four
  paragraphs, one of them a matched parenthetical pair, and no paragraph reaches
  three. Leave it.

Tests: the three fixture validations are themselves the new assertions; the
`t.Run` conversion names every case. A fixture that does not validate is a
blocker, not a fixture to loosen: it means the wire would refuse a message the
suite claims it could receive.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/errs src/common/pump src/common/service src/common/spawn src/common/net test/conformance`

### U3. Protocol libraries
Files: `src/protocol/` less the files Out of scope names,
`src/protocol/yang/cmd/yanggen/yanggen.yaml`,
`src/protocol/snmp/bench/bench-gate.sh`
After: none
Change, by requirement:
- R4: `snmp/decode.go:10`'s `// Wire-type leniency helpers` title line goes; the
  54-line block under it stays where it is, because `DecodeInt32`'s doc at `:65`
  and five siblings point back to "the coercion table at the top of this file" and
  it is the only statement of the accept sets, the lossy conditions, and which
  sentinel each failure returns. `snmp/rawwalk.go:13` loses its
  `// rawwalk.go — …` opening line the same way; the paragraph that follows
  becomes `RawVarBind`'s doc comment, since it describes that type and nothing
  points back to it.
- R1 and comments that restate the assignment beneath them:
  `snmp/watcher.go:303,309,313,317` (four one-line defaults; keep only why 2.0 and
  16 are the conservative pair, and drop ", using conservative starting values");
  `snmp/watcher.go:674` (drop the first sentence, keep the per-row indicator
  reason).
- R3: `snmp/decode.go:18`; `snmp/reactor_test.go:83`; `smi/codes_test.go:81`;
  `smi/differential/differential_test.go:178`; `snmp/pdu.go:465` ("today's
  semantics"); `snmp/oid_test.go:210` ("now carried");
  `snmp/conformance_corpus_test.go:70,128`; `snmp/bench/micro_test.go:220`;
  `snmp/test/integration/t2_traps_test.go:156,244` (keep the skip reason, drop
  "a planned follow-up" and "tracked as a follow-up").
- R2: `snmp/conformance_corpus_test.go:22` names the corpus file the rows come
  from, not "the hardening plan's Tables 1-3" and "the planning-discovered rows";
  `yanggen.yaml:1,21,27,31` (`KD5`, `R8`, `R11`); `bench-gate.sh:2,38`
  ("plan 003, U5", "per U1").
- `snmp/watcher_fallback_test.go:326` resolves its "Not quite." hedge or drops the
  comment.
- R13: `snmp/watch.go:230` drops "(learnings doc)" — vague attribution;
  `snmp/bench/macro_test.go:256` drops "robust"; trailing participles at
  `snmp/options.go:248`, `gnmi/session.go:223`, `restconf/session.go:289`,
  `yang/row.go:34`, `snmp/test/integration/testenv/snmpd.go:38`; negative
  parallelism at `restconf/session.go:127`; `snmp/reactor.go:107-112` is the one
  paragraph in this plan's scope with three em-dashes — keep one. The
  `(usm-msgid-predictability)`-style slugs are `snmp/CONFORMANCE.md` row ids, not
  planning labels; leave them, and leave `yang/revisions.go:82`'s citation of a
  `docs/solutions/` entry, which is a learning this repository keeps.
- R6, 6 sites: `smi/internal/parse/slab.go:18`; `snmp/errors.go:110`;
  `snmp/options.go:57`; `snmp/rawwalk.go:238`; `snmp/trap.go:354`;
  `snmp/watch.go:334`.
- R14, 16 sites: `snmp/{column_walk.go:34, reactor.go:236,238,
  trap_listen.go:371, trap.go:107,116, usm_discovery.go:56,
  usm_engine_table.go:33,72}`; `snmp/bench/netsnmp_native.go:147`;
  `ssh/buffer.go:17`; `syslog/{limits.go:9, receiver.go:71,74, sender.go:50}`;
  `yang/watch.go:198`.
- R11: `netconf/session.go:40` (`Close`: idempotence, whether ctx bounds the
  teardown, what error it may return); `restconf/session.go:69` (`Root`: fixed at
  `Dial`, prefixes every data URL).
- R5, 22 sites: the got/want grep scoped to `src/protocol`. All but
  `smi/bench/fixtures_test.go:190` and `smi/semantic_test.go:390` are in
  `snmp/{usm_security,usm_hardening,usm_notify,session_v3,reactor_v3,
  watcher}_test.go`.
- `snmp/bench/sweep_test.go:137` adds `tb.Helper()` as `sweepDial`'s first
  statement, as `warmOrFatal` at `:125` does.
- R15, 10 sites: `snmp/bench/netsnmp_native.go:192`;
  `snmp/watcher_cadence_test.go:344,501`;
  `netconf/test/integration/targets_test.go:49`;
  `restconf/test/integration/{t4_main_test.go:75, snapshot_test.go:28,50}`;
  `gnmi/test/integration/{t4_main_test.go:75, snapshot_test.go}` (two in the
  latter).
- `yang/watch.go` and `gnmi/{watch,subscribe}.go` reject a negative buffer where
  they validate their configuration, which is what U2's `pump.New` comment cites.
  The three boundaries are `yang.WatchConfig.Buffer` (`watch.go:208`),
  `gnmi.Watch`'s `buf` (`watch.go:108`) and `gnmi.Subscribe`'s `buf`
  (`subscribe.go:96`).

Tests: one case in `src/protocol/yang/` and one in `src/protocol/gnmi/` passing a
negative buffer and asserting the refusal, each of which reaches `pump.New` and
panics against the current code. The rest of this unit is comments and failure
messages, held by the existing suites.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol`

### U4. Modules
Files: `src/modules/` less the files Out of scope names
After: none
Change, by requirement:
- R3: `localnet/access/internal/evidence/store.go:73,81` state what `Store` does
  today and why recording under the correct epoch is the half worth keeping, with
  no "yet" and no plan reference; `localnet/access/lane.go:371,1128`;
  `localnet/access/internal/capability/fastiron/adapter.go:138` and
  `adapter_test.go:24`; `localnet/access/recovery_entry_test.go:276`;
  `capture/engine_test.go:437`.
- R2: `localnet/access/internal/evidence/store.go:81` (the plan reference).
- R1: `localnet/access/internal/capability/fastiron/adapter_test.go:21`.
- R6, 10 sites: `localnet/access/internal/evidence/store.go:70`;
  `localnet/access/internal/freeze/freeze.go:17`;
  `localnet/access/internal/recovery/hold.go:5`;
  `localnet/access/internal/telemetry/view.go:25`;
  `localnet/access/internal/credential/connect_adapter.go:25`;
  `localnet/access/internal/capability/fastiron/adapter.go:28`;
  `edgebus/{hub.go:75,forwarder.go:71,receiver.go:27}`; `capture/capture.go:55`
  (`Source` is an interface: say what an implementation must promise).
- R13: `localnet/access/lane.go:157` drops the shouted `FLOOR`, which
  `doc-style.md` bans as boldface used for decoration; `:165` says the positive
  form of "a floor, not a ceiling";
  `localnet/access/internal/freeze/freeze.go:17`,
  `localnet/access/internal/capability/interfaces/adapter.go:38,228` and
  `capture/engine.go:111` say the positive form. `lane.go:69` is **not** an R13
  site: its two em-dashes are a matched pair in one paragraph. Leave it.
- R11: `localnet/access/internal/lane/queue.go:66` names `itemHeap`, the symbol it
  sits above, and states the ordering contract once; `capture/engine.go:59` (who
  closes the source, which errors a caller branches on, that `Run` follows);
  `capture/engine.go:131` (stop repeating `NewWithSource`'s
  `reportsInterfaceDrops` paragraph verbatim).
- R5, 21 sites: the got/want grep scoped to `src/modules`. 19 are under
  `localnet/access/`, concentrated in `internal/lane/queue_test.go` (7) and
  `internal/evidence/store_test.go` (5); the other two are
  `capture/filter/compile_test.go` and `capture/pcapng/writer_test.go`.
- R14, 22 sites: `capture/{engine.go:54, rawsocket/local_linux.go:83,
  rawsocket/mirror_linux.go:257,261}`; `edgebus/{forwarder.go:84,
  hub.go:93,94,610, keys.go:38, leaf.go:82}`;
  `localnet/access/lane.go:265,267,271,344`;
  `localnet/access/internal/{credential/connect_adapter.go:105,
  evidence/store.go:90, freeze/freeze.go:24,35, lane/coalesce.go:55,
  lane/queue.go:101, mutation/machine.go:85, recovery/hold.go:18}`.

Tests: none in this unit. The `capture.Config` host test `code-style.md` Testing
requires of every module under `src/modules/` has to live outside
`src/modules/capture/`, and its home is the host that assembles the module, so it
is U6's item; U4 changes no behavior and adds no assertion.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules`

### U5. Device service
Files: `src/services/`
After: none
Paths below are relative to `src/services/device/internal/` unless they start
with `../`.
Change, by requirement:
- R1: `edge/verifier.go` loses its eight `// Step N:` prefixes. Keep the clauses
  that say why (untrusted input at `:132`, the skew-versus-expiry remedy at
  `:186`); `:160,165,170,176,181` restate the one comparison beneath them and go
  entirely, comment and all. `edge/doc.go` states the order without counting
  eleven steps. Also `captureapi/edge_service_test.go:235,248,253,259,287`;
  `../test/integration/e2e_test.go:805`;
  `captureapi/edge_service.go:427,153,341` and `captureapi/operator_service.go:38`
  (comments that restate the line); `captureapi/edge_service.go:371` (a
  near-verbatim duplicate of `:330-337`).
- R8: `captureapi/store.go:1` drops its package comment, leaving `doc.go:1`'s.
  `captureapi` is the only directory under `src/` with two.
- R7: `NewStore` takes the clock and `SetClock` goes. Its three callers are
  `captureapi/store_test.go:569`, `captureapi/operator_service_test.go:768` and
  `captureapi/artifact_invariant_internal_test.go:53`. The second calls it on a
  `Store` that file's own harness builds, so the harness takes the clock too.
- R16: `Store.GetSession` becomes `Store.Session`.
- R6, 9 sites: `captureapi.{Broadcaster,EdgeService,OperatorService}`;
  `auditapi/service.go:52`; `dispatchapi/service.go:110`;
  `deviceapi/{service.go:74,watcher.go:14}`; `edgeapi/middleware.go:85`;
  `telemetry/telemetry.go:80`.
- R10: `deviceapi/watcher.go:56`'s stop function is `sync.OnceFunc`-wrapped.
- R11: `captureapi/edge_service.go:44,51,101,126,156,323`;
  `captureapi/operator_service.go:24,41,59,161`;
  `captureapi/store.go:183,348,408` — the buffer size and loss on a slow reader,
  the CAS retry bound and `ErrCodeConflict`, the once-per-session finalization and
  its unlink on failure, the nil-field defaults. Also
  `dispatchapi/service.go:89`: `SweepInterval` is a `time.Duration`, so say "zero
  uses `defaultSweepInterval`", not "nil uses a slower default".
- R3: `host/{validation.go:70,validation.go:131,handle.go:57,config_test.go:201,
  serve.go:249,validation_test.go:102}`; `registry/registry.go:3`.
- R2: `host/serve.go:54`; `drift/drift.go:94`.
- R5, 78 sites: the got/want grep scoped to `src/services`. 69 are in
  `captureapi/{operator_service,edge_service}_test.go`, 7 in
  `../test/integration/capture_test.go`, 2 in `captureapi/store_test.go`.
- `captureapi/edge_service_test.go:111`'s fixture passes `protovalidate.Validate`.
- `captureapi/edge_service.go:352` answers `msgUnauthenticated` and keeps the
  schema detail internal, which `:28-31` already declares is all a refused caller
  learns.
- R14, 5 sites: `captureapi/{edge_service.go:40,148, store.go:66}`;
  `edge/verifier.go:78`; `host/handle.go:74`.

Tests: `captureapi/store_test.go` and `captureapi/operator_service_test.go` reach
the clock through the new constructor and keep every expiry case they assert
today; `deviceapi/watcher_test.go` calls the stop function twice and reaches the
next statement, which panics against the current code.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services`

### U6. Edge applications
Files: `src/edge/`, `src/edge/netpen/Taskfile.yml`,
`src/edge/netpen/test/integration/VALIDATION_MATRIX.md`
After: none
Change, by requirement:
- Reuse: `netpen/attacks/{l2,fh,routing,ip6}/harvest_*.go` hold four
  byte-identical copies of `mustMAC`, its doc block, and `srcBytes`. Each is a
  `//go:build ignore` `package main` in its own directory, run as
  `go run harvest_<x>.go`, so they cannot share a file. Move the helper into a new
  `netpen/attacks/internal/fixtureaddr` package the four generators import; `go
  run` resolves module imports, so the invocation in each file's doc comment is
  unchanged. `MustMAC` there documents the closed-argument answer
  (`code-style.md` Panics, Documented): every caller passes a file-level string
  constant, so the invariant does not travel and no caller owes a comment, the
  same reason `regexp.MustCompile`'s callers owe none. The generators' output
  bytes do not change, so no fixture is regenerated and the existing fixture-pin
  tests are the proof of that.
- R15, 1 site: `netpen/layers/hsrp.go:104`.
- R13: `netpen/runner/stream.go:35-41` keeps one em-dash of its three and states
  the backpressure choice without "This is the boring correct thing".
- `netpen/runner/options.go:117` drops the parenthetical citing the style
  document; the sentence before it already states the contract.
- R6, 11 sites: `netsimload/stats.go:13,23,35` (as sibling `Accumulator` at `:49`
  does); `agent/internal/busattach/busattach.go:56`;
  `agent/internal/dispatch/demux.go:32`;
  `agent/internal/identity/{enroll.go:32,store.go:26}`;
  `agent/internal/report/{deliver.go:37,queue.go:69}`;
  `agent/internal/subscribeloop/loop.go:100`; `netpen/runner/signals.go:35`.
- R3: `netpen/test/integration/testenv/testenv.go:26` says what `Target` returns.
- R2 and R4: `Taskfile.yml:7,47,59,93,99` name the task instead of `U1`, `U13`,
  `KTD14`, `KTD15`, `KTD16`, and its four `# --- … ---` dividers go.
  `VALIDATION_MATRIX.md:114`'s heading loses `(R4)` and
  `netpen/test/integration/matrix_test.go:49` cuts on the new heading. `AE6` is a
  tier name the matrix uses throughout; it stays. The `strings.Cut` separator
  carries the leading and trailing newline, so `## AE6 superset attacks` matches
  that heading and no other line in the file — check that before landing, because
  a separator that matches nothing makes the section read empty rather than fail.
- R5, 9 sites: the got/want grep scoped to `src/edge` — 4 in
  `netpen/full/recon_scan_test.go`, 2 in `agent/internal/capture/capture_test.go`,
  one each in `netpen/full/full_test.go`, `netpen/layers/layers_test.go` and
  `netpen/cmd/netpen/main_test.go`.
- R14, 11 sites: `agent/internal/{capture/session.go:57,
  dispatch/registry.go:36, lanehost/onboard.go:93,95, report/queue.go:75}`;
  `netpen/{catalog/catalog.go:127, full/full.go:100, full/recorder.go:24,
  link/link_linux.go:29, runner/runner.go:79}`;
  `netsimload/packetio/sender_linux.go:38`.

Tests: `agent/host/capture_test.go` gains the `capture.Config` test
`code-style.md` Testing requires of every module under `src/modules/` — one case
that names every exported field of `capture.Config` and its type and constructs or
implements a value for each. `capture_test.go:232` today passes a `capture.Config`
and reads only `cfg.Source`, so no package outside the module shows a host can
fill it. `netpen/test/integration/matrix_test.go` already fails when the heading
and the cut string disagree, which is the test for that rename; the shared
`MustMAC` is exercised by running each generator, which the existing fixture-pin
tests do.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge`

Waves: U1 U3 U4 U5 U6 | U2

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- <the unit's paths>
.claude/skills/verify-change/scripts/verify-change.sh --full   # after the last merge
```

`--full` is required because U1's renames reach every netsim package and U3
touches two nested modules. Then the greps requirements 1, 2, 4, 5 and 14 name, in
both comment syntaxes, because the configuration sites use `#`:

```bash
rg -n '^\s*//\s*Step [0-9]' src test
rg -n '(//|#).*\b(R[0-9]{1,2}|U[0-9]{1,2}|KD[0-9]+|KTD[0-9]*|DR[0-9]+)\b' src test
rg -n '^\s*(//|#)\s*[=*_-]{4,}' src test
rg -n 't\.(Errorf|Fatalf)\("(expected|want) [^"]*, got' src test
rg -n '^\s+\w+\s+sync\.(RW)?Mutex\s*$' src
grep -n 'R4' src/edge/netpen/test/integration/VALIDATION_MATRIX.md
```

Expected output: the second grep prints
`src/common/netsim/fabric/stp_test.go:1456` (region names `R1` and `R2`, which U1
keeps) and the `usm-*` and `enc-*` row ids in `src/protocol/snmp/CONFORMANCE.md`
if that file is in the path list; the last grep prints nothing; the other four
print no line.

What no test in this plan covers, said plainly rather than left to be discovered:

- Requirement 7's race. Deleting `SetClock` removes it by construction, so no test
  can tell the fixed tree from the broken one — the racing caller is gone, and
  `go test -race` passes before and after because all three current callers set
  the clock before first use. The acceptance is the compile-level facts plus the
  preserved expiry cases.
- Requirements 6, 11, 13 and 14 are the content of comments. Nothing executable
  reads them, and the greps see only their shape. A reviewer reading the diff is
  the check.
- Requirement 5 changes failure text. A test whose message is wrong still passes;
  the got/want grep is the whole gate.
- Requirement 9's rename is caught by the compiler, and its one real risk — a
  changed string value — by the netsim golden and corpus comparisons.

## Definition of done

- The verifier is green on every changed path and `--full` is green on the merged
  tree.
- The greps above print their expected output.
- No README or convention document changes: this plan conforms code to documents
  that already say what it now does.
- This plan's `status` is set with an outcome note under its title.
- No plan label appears in code, comments, or commit messages.
- `git diff --stat` names no file under `generated/`, `buf.lock`,
  `.golangci.yml`, `AGENTS.md`, or `tools/hooks/`.

## Open questions

- Whether this plan should have been a parent plan with six phases (Decision,
  unconfirmed). Options: keep it whole, as written (recommended) | split it now
  into a parent and six phase plans, five of them `needs-decisions` | split it and
  carry these Units into the phase files verbatim so nothing is re-derived.
  Recommended because the plan's length is an inventory rather than decisions, and
  `phases.md`'s trigger was written for the opposite case.
- Requirement 6's scope (Decision, unconfirmed). Options: the 51 types the Units
  enumerate (recommended) | all 467 exported types in scope | none, and amend
  `code-style.md` Concurrency to say "every exported type whose values cross
  goroutines". Recommended because the rule as written yields 416 sentences that
  restate the type name; the third option is the durable fix and needs the
  document's owner.
- The 140 verbless `fmt.Errorf` calls left out of scope (Decision, unconfirmed).
  Options: enable `perfsprint`'s `errorf` check in `.golangci.yml` and fix what it
  flags in one mechanical change (recommended) | fix them by hand here | leave
  them. `staticcheck` has no such check — its `S1028` is the inverse. Recommended
  because a linter keeps the rule true; `AGENTS.md` requires guardrail review for
  `.golangci.yml` rather than forbidding the change, so this is a proposal, not a
  boundary.
- `captureapi.Store`'s clock (Decision, unconfirmed). Options: constructor
  parameter, no `SetClock` (recommended) | guard `s.clock` with `s.mu` | document
  that `SetClock` must precede first use. Recommended because the second puts a
  lock on every clock read in the sweep and the third leaves a contract a caller
  can still break.
- The netsim initialism renames (Decision, unconfirmed). Options: all eight
  identifiers (recommended) | only the three `IssueCode` constants, whose 10
  references are cheap | none. Recommended because `port.go` already spells
  `ReasonMTUExceeded` correctly, so the file disagrees with itself.
- `Store.GetSession` → `Store.Session` (Decision, unconfirmed). Options: rename
  (recommended) | leave it, reading the prefix as matching the
  `jetstream.KeyValue.Get` it wraps.
- `pump.New`'s panic and the Named clause (Decision, unconfirmed). Options: read
  the Named clause as governing `panic` statements only, and make the runtime
  panic unreachable at the three boundaries (recommended) | rename `New` to
  `MustNew` and change every caller | give `New` an error return. Recommended
  because the boundaries are where a caller-supplied capacity enters and the
  rename would put `Must` on the package's ordinary constructor.
