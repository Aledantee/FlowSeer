---
title: Unverified External Claims - Plan
type: fix
date: 2026-09-30
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: rework
execution: mixed
---

# Unverified External Claims - Plan

> Implemented. 6 units, 2026-09-30T09:30:52Z to 2026-09-30T11:01:21Z.

## Goal

Every claim in the tree about an external system that the evidence below
shows to be false is corrected against its pinned or vendored source: a
pinned library, a vendored MIB, the one independent GLBP decoder, and the
buf CLI that writes `generated/`. Each wrong claim is corrected
where it lives. Where the wrong claim sits in code, a test pins the corrected
behavior.

Stop condition: the buf CLI at the pinned version regenerates `generated/`
with a diff. Every later unit regenerates through the pin, so the pin must
reproduce main byte for byte first.

## Decisions

- **One plan, not a parent with phases.** Why: the units form three
  clusters (U1 to U4 through `After`, U5, and U6), and the plan runs past
  300 lines, mostly source citations. Split phases after the first would
  start `needs-decisions` and be re-planned, which would redo the source
  reads this plan already records. U5 and U6 run in the first wave in
  their own worktrees either way.
- **Pin the buf CLI at v1.73.0 in its own tool module, `tools/buf/go.mod`,
  and run it as `go tool -modfile=tools/buf/go.mod buf`.** Why: a `tool`
  line in the root `go.mod` pulls buf's dependency graph into the product
  module, and minimal version selection can then raise
  `google.golang.org/protobuf`, grpc, otel, cel-go, or protovalidate. That
  would break the plugin-equals-runtime pin below and move the module
  versions this plan cites. Every buf invocation today runs whatever
  `buf` is on `PATH` and only checks that it exists
  (`.agents/skills/verify-change/scripts/verify-change.sh:473-474`). The
  CLI release decides the `java_multiple_files` byte in every descriptor
  (`docs/solutions/conventions/unpinned-buf-remote-plugins-drift-the-whole-generated-tree.md:38-42`).
  v1.73.0 is the version main was generated with and the one installed
  here (`buf --version`). A version assertion in the verifier would leave
  each developer on their own install.
- **Pin the remote plugins to the runtime versions in `go.mod`:
  `buf.build/protocolbuffers/go:v1.36.12` and `buf.build/connectrpc/go:v1.20.0`.**
  Why: `buf.gen.yaml:20,23` has no versions, so the plugin moves when the
  registry does. The generated headers read `protoc-gen-go v1.36.12`, and
  `go.mod:13,44` pins connect v1.20.0 and protobuf v1.36.12.
- **Leave `tools/hooks/proto-check.sh` on `PATH` buf.** Why: it is a
  policy surface (`AGENTS.md`, Hard boundaries), and it runs only
  `buf format` and `buf lint`, which do not write `generated/`. Moving it
  to the pinned buf is a separate change for guardrail review (Open
  questions). Until then a `PATH` buf at another version can format a file
  the verifier's pinned `buf format -d` rejects.
- **Rename the buf solution doc to
  `the-buf-cli-version-decides-one-option-byte-in-every-generated-descriptor.md`.**
  Why: its body already names the CLI version as the root cause
  (`:21`), and the current name blames the plugins. That misdirected the
  09-29 regeneration (`089a2330`, reverted in `9c18947d`).
- **Six `IMPLICIT` fields move to the edition default. The style doc
  stays unchanged.** Why: `docs/code-style-proto.md:113-122` forbids
  `field_presence` with no carve-out and names this exact case (a counter
  that reads zero, a flag that reads false). No Go code calls `Has*` on
  any of the six fields. Every read is a getter
  (`src/services/device/internal/journal/journal.go:224,460`,
  `src/modules/localnet/access/lane.go:845`,
  `src/services/device/internal/dispatchapi/report.go:136`). Stored
  records still parse, because an absent field reads as its zero value.
  This breaks the wire, which `AGENTS.md` accepts before the first stable
  release.
- **The edge reports why a capture stopped, on the final chunk.** Why:
  `capture.Engine` already holds the exact reason in every exit branch
  (`src/modules/capture/engine.go:216,285-360`), but `Batch`
  (`src/modules/capture/capture.go:29-37`) and `CapturePacketChunk`
  (`spec/proto/flowseer/model/capture/v1/capture_chunk.proto:23-36`) have
  no field for it. Central therefore guesses (`deriveStopReason`,
  `src/services/device/internal/captureapi/edge_service.go:784-799`). It
  checks packets (`:788`) before duration (`:791`) and bytes last
  (`:794`), so a run with both a byte and a duration bound that stops on
  bytes is recorded as DURATION. Any stop the engine reports as OPERATOR
  on a final batch is recorded as a budget reason. No in-process capture
  path exists, so the upload stream is the only wire.
- **Central maps the reported reason to a lifecycle the way the engine
  does.** OPERATOR gives CANCELED, and any budget reason gives COMPLETED
  (`src/modules/capture/engine.go:382-390`). A session already CANCELED by
  the operator keeps OPERATOR. Why: `operator_service.go:150` records that
  at the operator stop, and `edge_service.go:645` already skips CANCELED
  sessions. A reported reason that disagrees, such as a budget hit that
  raced the stop, is logged and the operator's value is kept. Recording
  COMPLETED with OPERATOR would contradict
  `capture_session.proto:138-139,156`.
- **A final chunk never carries ERROR.** Why: a run that fails sends no
  final batch (`engine.go:366-371`), and central records ERROR itself in
  `failStream` (`edge_service.go:735`).
- **The GLBP layer follows Wireshark's `packet-glbp.c` and cites it. It
  cites no RFC.** Why: GLBP has no RFC. RFC 7868 is EIGRP, and
  `src/edge/netpen/layers/eigrp.go:1` cites it for EIGRP. Wireshark's
  dissector is the only independent decoder. The implementer cites it at
  a commit hash, not at `master`
  (https://github.com/wireshark/wireshark/blob/master/epan/dissectors/packet-glbp.c,
  offsets at lines 160-240 and 289-345 on 2026-09-30).
  It reads a different wire from the one `glbp.go` builds:
  - a 12-byte header: version, unknown, group (2, at offset 2),
    unknown (2), owner MAC (6)
  - TLVs whose type and length are one byte each, the length counting
    the two header bytes
  - a Hello TLV (type 1): unknown, VG state (Listen 4, Speak 8,
    Standby 0x10, Active 0x20), unknown, priority, unknown (2), hello and
    hold intervals (4 bytes each, milliseconds), redirect (2),
    timeout (2), unknown (2), address type (1), address length (1),
    virtual address

  `glbp.go` instead has a 23-byte fixed header, an opcode at byte 2, the
  group at bytes 3-4, two-byte TLV type and length, and states 0 to 4. The
  fixture `glbp.pcap` was authored from that same belief
  (`src/edge/netpen/layers/testdata/PROVENANCE.md:73-83`), so the
  round-trip test cannot see the error. So were the attack fixtures that
  `src/edge/netpen/attacks/fh/harvest_fh.go:141-145,505-540` writes.
- **The resign teardown sends a Hello in Wireshark's Listen state (4).**
  Why: it now uses `GLBPStateInit` (`attacks/fh/glbp.go:44`), which has no
  wire value in the dissector. Listen is the lowest state the dissector
  names, and a router in Listen does not claim the virtual gateway.
- **Edge-bus storage: correct the prose, add the missing test, and name
  the limit in the attach error. `MaxStoreBytes` keeps its zero default.**
  Why: nats-server v2.14.6 (`go.mod:18`) turns zero into `0.75 × free disk`
  once at start (`server/jetstream.go:2760-2765`, `disk_avail.go:31`,
  `finalizeDynamicMaxStore` at `jetstream.go:546-565`). Enabling an account
  then checks the sum of account budgets against it (`jetstream.go:2665-2681`).
  Edge N+1 is refused with `insufficient storage resources available`
  (err_code 10047), and the client sees only `jetstream not enabled`. The
  comment at `src/modules/edgebus/hub.go:145-151`, the one at
  `hub.go:413-416`, and `src/modules/edgebus/README.md:47-50` call the
  ceiling a backstop that no number of edges can reach, which is false. A
  pinned default would trade a disk-dependent cap for a fixed one, and no
  evidence favors either.
- **The attach error is computed from the ceiling and the budgets, after
  the server refuses.** Why: `JetStreamReservedResources()`
  (`jetstream.go:1146-1154`) returns stream reservations, not the account
  budget sum that refuses the account (`:2664-2681`). The refusal itself
  is only logged (`accounts.go:3912-3917`). When `waitForJetStream` fails
  (`hub.go:298,437`), `AttachEdge` reads the ceiling from
  `srv.JetStreamConfig().MaxStore` (`jetstream.go:1114`). If the central
  budget plus the edge budget times the number of attached edges plus one
  exceeds it, the error names the storage limit and both numbers. The hub
  keeps the central budget next to `edgeBudget` for this. It does not
  check before attaching, since the server skips the check while
  `maxStorePending` is set (`jetstream.go:2658`) and a pre-check would
  refuse edges the server accepts.
- **`rpc.method` on the gNMI span is the full method name without the
  leading slash.** Why: semconv v1.43.0 (otel v1.46.0) defines
  `rpc.method` as "the fully-qualified logical name of the method"
  (`~/go/pkg/mod/go.opentelemetry.io/otel@v1.46.0/semconv/v1.43.0/attribute_group.go:13062-13070`).
  The device service already follows this
  (`src/services/device/internal/telemetry/telemetry.go:181-199`). The
  gNMI stubs name the methods `/gnmi.gNMI/Get` and `/gnmi.gNMI/Set`
  (`github.com/openconfig/gnmi@v0.14.1/proto/gnmi/gnmi_grpc.pb.go:23-24`).
  `unaryCtx` uses one `op` string for both the span name and
  `rpc.method` (`src/protocol/gnmi/session.go:209-211`), so it gains a
  separate method-name parameter and the span name stays `gnmi.Get`.

- **Requirement 2 covers instructions to run buf, not prose that names
  it.** A hit that names the tool or its output as a noun ("`buf
  generate` output", "every `buf breaking` category") stays as written.
  Every hit that tells a reader or agent to run buf names the pinned
  command, including `AGENTS.md:44` and
  `.agents/skills/delegate/SKILL.md:227`, which join U1. U1 may edit that
  one `AGENTS.md` line. (decided by the user, 2026-09-30)

## Requirements

1. The pinned buf's `generate` on a clean checkout of this branch leaves
   `git status generated/` empty. Example: after U1, running the
   verifier's generate gate on an unchanged `.proto` reports no drift.
2. No file outside `tools/hooks/` tells a person or agent to run a bare
   `buf`, and the verifier runs only the pinned one. Example:
   `git grep -nE '(^|[^-/.a-z])buf (generate|lint|format|breaking)' -- ':!tools/hooks' ':!docs/plans' ':!generated'`
   finds the pinned form, or buf named as a noun rather than as a
   command to run.
3. The root `go.mod` requirements do not change. Example:
   `go list -m all` before and after U1 print the same lines.
4. A gNMI Get span carries `rpc.method = "gnmi.gNMI/Get"` and the span
   name `gnmi.Get`. Example: an in-memory span recorder around
   `Session.Get` records both.
5. Every MIB object a schema comment cites exists in `spec/mib/`.
   Example: `ospf_area.proto` cites `ospfAsBdrRtrCount`
   (`spec/mib/ietf/OSPF-MIB:784`). `bgp_peer.proto` cites OpenConfig
   `local-as` and `peer-as`
   (`spec/yang/openconfig/openconfig-bgp-common.yang:266,272`) for the
   32-bit numbers, and it says `bgpPeerRemoteAs` is 16-bit
   (`BGP4-MIB:293-294`).
6. No file under `spec/proto/flowseer/` sets `field_presence`. Example:
   `grep -rn field_presence spec/proto/flowseer` prints nothing, and
   `DeviceLaneRecord.HasHighWatermark` exists.
7. Central records the reason the edge reports. Example: a budget of
   `max_bytes=1000, max_duration=60s` whose engine stops on bytes records
   `CAPTURE_STOP_REASON_BYTE_COUNT` and COMPLETED in `CaptureSessionState`,
   where today it records DURATION. A final chunk reporting OPERATOR on a
   RUNNING session records OPERATOR and CANCELED.
8. A final chunk carries a stop reason and a non-final chunk does not.
   Example: `CapturePacketChunk{final: true}` with no `stop_reason`,
   `{final: true, stop_reason: ERROR}`, and
   `{final: false, stop_reason: DURATION}` each fail protovalidate.
   `{final: true, stop_reason: BYTE_COUNT}` passes.
9. A GLBP Hello that netpen emits decodes under Wireshark's layout.
   Example: group 1, priority 255, state Active, hello 3000 ms, hold
   10000 ms, virtual IPv4 10.0.0.1 encode to the bytes a test spells out
   from `packet-glbp.c`. Decoding those bytes returns the same values.
10. Attaching an edge past the store ceiling returns an error that names
    the storage limit. Example: `MaxStoreBytes = 640 MiB`, central budget
    512 MiB, edge budget 128 MiB. The first edge attaches. The second
    fails with an error whose text includes "storage".

## Out of scope

- `tools/hooks/proto-check.sh` switching to the pinned buf (policy
  surface, Open questions).
- Changing the edge-bus default ceiling or budgets.
- The engine's `context.DeadlineExceeded` to OPERATOR mapping
  (`src/modules/capture/engine.go:299-300`). It is kept, not re-decided.
- A GLBP lab capture. None exists, and the matrix row stays
  `live lab (pending)`
  (`src/edge/netpen/test/integration/VALIDATION_MATRIX.md:133`).
- Auditing the rest of `src/edge/netpen/attacks/**` for similar claims.
- The workflow cause of these mistakes. That goes to
  `docs/agent-observations.md` through `compound` Observe.

## Units

### U1. Pin the buf CLI and the remote plugins

Files: tools/buf/go.mod, tools/buf/go.sum, buf.gen.yaml, .agents/skills/verify-change/scripts/verify-change.sh, .agents/skills/implement/SKILL.md, .agents/skills/delegate/SKILL.md, AGENTS.md (line 44 only), .agents/skills/delegate/references/hookless-merge.md, .agents/skills/delegate/references/orca-sandbox.md, README.md, CONTRIBUTING.md, spec/proto/ruckus/README.md, docs/code-style-proto.md, docs/code-style.md, docs/solutions/conventions/unpinned-buf-remote-plugins-drift-the-whole-generated-tree.md (renamed to the-buf-cli-version-decides-one-option-byte-in-every-generated-descriptor.md), docs/solutions/README.md
After: none
Change: `tools/buf/go.mod` is a module that holds only
`tool github.com/bufbuild/buf/cmd/buf` at v1.73.0. `buf.gen.yaml` names
`buf.build/protocolbuffers/go:v1.36.12` and `buf.build/connectrpc/go:v1.20.0`.
The verifier runs the pinned buf for format, lint, breaking, and generate
through one shell variable. Both `need_tool buf` checks (`:474` and
`:759`) are gone. Every file listed above that tells a person or agent to
run `buf` (for example `README.md:36`, `CONTRIBUTING.md:78,81`,
`docs/code-style-proto.md:9,436-443`, `docs/code-style.md:433`,
`.agents/skills/implement/SKILL.md:66,89`) names the pinned command. The
solution doc moves to its new name. Its `resolution_type` becomes a fix,
its "How to apply" names the pin, and its verifier cite points at the
current generate line. `docs/solutions/README.md` links the new name.
Tests: the verifier's generate gate over `spec/proto/` reports no drift.
`go list -m all` in the root module prints the same lines before and
after. Every Requirement 2 grep hit that tells a reader to run buf names the pinned form.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- tools/buf/ buf.gen.yaml .agents/skills/ AGENTS.md README.md CONTRIBUTING.md spec/proto/ruckus/README.md docs/code-style-proto.md docs/code-style.md docs/solutions/`

### U2. Correct the MIB, parser, BPF, and semconv claims

Files: spec/proto/flowseer/net/protocol/ospf/v1/ospf_area.proto, spec/proto/flowseer/net/protocol/bgp/v1/bgp_peer.proto, generated/ (regenerated), mibgen.yaml, docs/solutions/conventions/bpf-rawinstruction-never-satisfies-newvm.md (renamed, see Change), docs/solutions/README.md, docs/code-style-proto.md, src/protocol/gnmi/session.go, src/protocol/gnmi/session_test.go
After: U1
Change: `ospf_area.proto:35` cites `ospfAsBdrRtrCount`.
`bgp_peer.proto:64,66` cite OpenConfig `peer-as` and `local-as`. Line 64
says that BGP4-MIB `bgpPeerRemoteAs` is `Integer32 (0..65535)` and reads
AS_TRANS (23456, RFC 6793 §9) for a 4-byte peer. `mibgen.yaml:58-61`
says `src/protocol/smi` resolves IMPORTS. The BPF solution doc's title,
`root_cause`, filename, and body say that `[]bpf.RawInstruction` does not
convert to `NewVM`'s `[]bpf.Instruction` parameter
(`golang.org/x/net@v0.58.0/bpf/vm.go:20`), because Go slices are not
covariant. The failure it records comes from a `[]bpf.Instruction` whose
elements are `RawInstruction` values, which `NewVM`'s type switch
(`vm.go:25-70`) never matches as a return. Its index row follows the
rename. `docs/code-style-proto.md:315-320` says `IGNORE_IF_ZERO_VALUE`
acts on repeated and map fields and on `IMPLICIT` fields, and is a no-op
on any field that tracks presence
(`buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go@v1.36.12-20260825204119-511051f7f437.2/buf/validate/validate.pb.go:131-145`).
`unaryCtx` in `gnmi/session.go` takes the span operation and the RPC
method separately. Get and Set pass
`strings.TrimPrefix(gpb.GNMI_Get_FullMethodName, "/")` and the Set
equivalent as the method.
Tests: `session_test.go` gains a case with an in-memory span exporter
(`go.opentelemetry.io/otel/sdk/trace/tracetest`). It asserts span name
`gnmi.Get` with `rpc.method` `gnmi.gNMI/Get`, and `gnmi.Set` with
`gnmi.gNMI/Set`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/protocol/ospf/v1/ospf_area.proto spec/proto/flowseer/net/protocol/bgp/v1/bgp_peer.proto mibgen.yaml docs/solutions/ docs/code-style-proto.md src/protocol/gnmi/`

### U3. Return the six implicit-presence fields to the edition default

Files: spec/proto/flowseer/store/device/v1/lane_record.proto, spec/proto/flowseer/edge/dispatch/v1/execution.proto, generated/ (regenerated), src/services/device/internal/edgeapi/submission_test.go, src/services/device/internal/journal/journal_test.go
After: U1
Change: `lane_record.proto:39,47,56,59` and `execution.proto:51,89` drop
`[features.field_presence = IMPLICIT]`. Their comments drop "Implicit
presence" and say what zero or false means, as
`docs/code-style-proto.md:119-121` asks. The builder fields become
pointers, so `submission_test.go:54,213` pass
`proto.Uint64(sequence)`. The CEL rules at `lane_record.proto:27,32` read
the same under explicit presence, and a test proves it.
Tests: `journal_test.go` gains a case that validates a `DeviceLaneRecord`
twice, once with `high_watermark` and `dispatched` unset and once with
them explicitly set to zero and false. Both results match the result
before the change. The existing journal, edgeapi, and dispatch tests pass.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/store/device/v1/lane_record.proto spec/proto/flowseer/edge/dispatch/v1/execution.proto src/services/device/internal/journal/ src/services/device/internal/edgeapi/ src/services/device/internal/dispatchapi/ src/modules/localnet/access/`

### U4. The edge reports the capture stop reason

Files: spec/proto/flowseer/model/capture/v1/capture_chunk.proto, generated/ (regenerated), src/modules/capture/capture.go, src/modules/capture/engine.go, src/modules/capture/engine_test.go, src/edge/agent/internal/capture/upload.go, src/edge/agent/internal/capture/capture_test.go, src/edge/agent/internal/capture/README.md, src/services/device/internal/captureapi/edge_service.go, src/services/device/internal/captureapi/edge_service_test.go, src/services/device/internal/captureapi/operator_service_test.go, src/services/device/test/integration/capture_test.go
After: U1
Change: `CapturePacketChunk` has `CaptureStopReason stop_reason = 6` with
`enum = {defined_only: true, not_in: [0, 5]}`, the field-rule shape of
`capture_session.proto:264-267`. A message rule with id
`capture_packet_chunk.final_has_stop_reason` holds
`this.final == has(this.stop_reason)`. `capture.Batch` has `StopReason`,
which `flush` sets only on the final batch, from the engine's local
`stopReason`. `upload.go:180` copies it into the chunk. In a
non-CANCELED session, `edge_service.go` sets the reported reason and maps
OPERATOR to CANCELED and a budget reason to COMPLETED. In a CANCELED
session it logs a disagreement and keeps OPERATOR. `deriveStopReason`
and its imports are deleted. The agent README (`:24-37`, `:53-61`)
describes the reported reason.
Tests:
- `engine_test.go` asserts `Batch.StopReason` on the final batch for
  PACKET_COUNT (`:78-108`) and DURATION (`:347`). It adds a byte stop
  under both a byte and a duration bound, which reports BYTE_COUNT.
- `edge_service_test.go:680-712` sends BYTE_COUNT on the final chunk of a
  session whose budget has bytes and duration, and asserts BYTE_COUNT
  (DURATION before the change). It adds OPERATOR on a RUNNING session,
  which records CANCELED, and PACKET_COUNT on a CANCELED session, which
  keeps OPERATOR.
- The final chunks built without a reason at
  `operator_service_test.go:588-603,715-721` and
  `test/integration/capture_test.go:304-321` gain one. The integration
  assertion at `:787` still expects PACKET_COUNT.
- `capture_test.go` asserts the uploaded final chunk carries the reason,
  and its `:572` comment stops naming `deriveStopReason`.
- A protovalidate case covers each Requirement 8 example.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/model/capture/v1/capture_chunk.proto src/modules/capture/ src/edge/agent/internal/capture/ src/services/device/internal/captureapi/ src/services/device/test/integration/`

### U5. GLBP on Wireshark's wire layout

Files: src/edge/netpen/layers/glbp.go, src/edge/netpen/layers/l3_test.go, src/edge/netpen/layers/testdata/glbp.pcap, src/edge/netpen/layers/testdata/glbp.json, src/edge/netpen/layers/testdata/PROVENANCE.md, src/edge/netpen/layers/harvest.py, src/edge/netpen/attacks/fh/glbp.go, src/edge/netpen/attacks/fh/harvest_fh.go, src/edge/netpen/attacks/fh/fh_test.go, src/edge/netpen/attacks/testdata/fh/glbp.pcap, src/edge/netpen/attacks/testdata/fh/glbp_restore.pcap, src/edge/netpen/test/integration/t2_superset_test.go, src/edge/netpen/test/integration/VALIDATION_MATRIX.md
After: none
Change: the layer decodes and serializes the 12-byte header and one-byte
TLVs in the layout under Decisions. It types the Hello TLV (VG state as
Wireshark's 4, 8, 0x10, 0x20, priority, hello and hold intervals in
milliseconds, virtual address) and keeps other TLVs raw. Every "RFC 7868"
comment in the layer and in `harvest_fh.go:12,140` names `packet-glbp.c`
at a commit hash instead. The opcode type goes, since the wire has none.
The hijack attack builds a Hello TLV with priority 255 and state Active,
and the resign teardown sends state Listen. `harvest_fh.go` writes both
attack fixtures in the new layout, and the layer fixture and its
provenance entry are regenerated. The provenance entry says that
Wireshark is the layout source and that no device capture confirms it.
Tests: `l3_test.go` gains a known-bytes test that spells out a Hello
frame byte for byte from `packet-glbp.c` offsets, with no encoder
involved. It decodes the frame and checks every field, and it checks that
encoding the decoded layer returns the same bytes. A TLV length below 2
is rejected. The existing round-trip test and the `fh_test.go:520-525`
byte comparison run on the regenerated fixtures.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen/layers/ src/edge/netpen/attacks/ src/edge/netpen/test/integration/`

### U6. State the edge-bus storage ceiling truthfully

Files: src/modules/edgebus/hub.go, src/modules/edgebus/README.md, src/modules/edgebus/storage_test.go, docs/solutions/architecture-patterns/per-account-jetstream-disk-budgets-reserve-against-the-server-store-ceiling.md
After: none
Change: the `hub.go:42-47` field doc, the comments at `:145-151` and
`:413-416`, and `README.md:47-50` say that account budgets are
reservations against the server's store ceiling. Zero sets that ceiling
once at start to 75% of free disk, which caps edges at
`(ceiling − central budget) / edge budget`. `AttachEdge` returns the
storage error described under Decisions when the server refuses. The
solution doc cites symbols and v2.14.6, not v2.14.1 line numbers. It
describes the account-budget sum check (`jetstream.go:2665-2674`)
separately from `storeReserved`, and it adds the frozen ceiling and
restart caveat.
Tests: `storage_test.go` starts a hub with `MaxStoreBytes = 640 MiB`,
central budget 512 MiB, and edge budget 128 MiB. A pinned ceiling skips
the disk probe (`jetstream.go:2760-2761`), so the test is hermetic. The
first `AttachEdge` succeeds. The second fails, and its error text
contains "storage".
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/edgebus/ docs/solutions/architecture-patterns/per-account-jetstream-disk-budgets-reserve-against-the-server-store-ceiling.md`

Waves: U1 U5 U6 | U2 U3 U4

## Verification

Run the verifier over the union of changed paths. Do not use `--full`,
because it builds `generated/go/yang` and exhausts host memory. After the
last unit, run the pinned buf's `generate` and check that `git status
generated/` holds only the files U2, U3, and U4 changed.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] Package READMEs and convention docs updated in the same change as
      their code.
- [ ] `grep -rn field_presence spec/proto/flowseer` prints nothing.
- [ ] No "RFC 7868" left in `src/edge/netpen/layers/glbp.go`,
      `attacks/fh/glbp.go`, or `attacks/fh/harvest_fh.go`.
- [ ] This plan's `status` set, with an outcome note under its title.
- [ ] No plan labels in code.

## Open questions

- Should `tools/hooks/proto-check.sh` run the pinned buf so the hook and
  the verifier format with one version? It is a policy surface. Stage it
  for a person's review and do not edit it inside U1.
- If `buf.build/connectrpc/go:v1.20.0` is not a published plugin tag, pin
  the tag that regenerates the current `*.connect.go` files byte for byte,
  and record the tag in U1's commit.
