---
title: Unverified External Claims - Plan
type: fix
date: 2026-09-30
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: partially-implemented
review: rework
execution: mixed
---

# Unverified External Claims - Plan

> Partially implemented. U1 to U7 and U9 are implemented and verified; U7
> passed guardrail review (5f19ebeb). U8 reopens for the lock that removes
> the `Hub.Close` exception from Requirement 12.

## Goal

Every claim in the tree about an external system that the evidence below
shows to be false is corrected against its pinned or vendored source: a
pinned library, a vendored MIB, the one independent GLBP decoder, and the
buf CLI that writes `generated/`. Each wrong claim is corrected
where it lives. Where the wrong claim sits in code, a test pins the corrected
behavior.

U7 to U9 close what the landed units left open. The verifier runs no gate
for a change to the buf pin. The edge-bus attach error decides "storage"
from arithmetic that counts the attaching account once the server has
enabled it. A capture import comment claims a rule package its package
never validates.

Stop condition: the buf CLI at the pinned version regenerates `generated/`
with a diff. Every later unit regenerates through the pin, so the pin must
reproduce main byte for byte first.

## Decisions

- **One plan, not a parent with phases.** Why: the units form three
  clusters (U1 to U4 through `After`, U5, and U6), and the plan runs past
  300 lines, mostly source citations. Split phases after the first would
  start `needs-decisions` and be re-planned, which would redo the source
  reads this plan already records. U5 and U6 run in the first wave in
  their own worktrees either way. U7 to U9 stay in this plan, as the two
  Decisions dated 2026-09-30 at the end of this list require.
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
- **`AttachEdge` reads whether the server enabled the edge account right
  after connect, and names storage on a failed wait only when it had
  not.** It looks the account up with `Server.LookupAccount`
  (`server/server.go:2094`) and reads `Account.JetStreamEnabled`
  (`server/jetstream.go:2076-2084`) before the wait starts, and acts on
  the reading only if the wait fails. Why: in nats-server v2.14.6
  (`go.mod:18`) the flag is the
  refusal. `EnableJetStream` sets `a.js` only at `jetstream.go:1211` and
  `:1241`, and a refused `sufficientResources` returns before either
  (`:1218-1221`). The refusal itself is only logged
  (`accounts.go:3912-3917`). Enablement also finishes before
  `nats.Connect` returns. CONNECT authentication looks the account up
  (`server/client.go:2370`, `server/auth.go:1020`). A fetched account
  applies its claims at once (`server/server.go:2208-2213`,
  `server/accounts.go:4088-4089`), and claims with JetStream limits run
  `configJetStream` and `EnableJetStream` (`accounts.go:3911-3917`,
  `jetstream.go:837-849`). nats.go v1.53.1 (`go.mod:19`) writes CONNECT
  with a PING and returns only after the PONG (`nats.go:3144-3190`). So
  the flag is settled before the wait starts, whatever the wait returns
  and whatever the caller's context holds. The JetStream API imports are
  also in place by then (`jetstream.go:1246`), so the comments at
  `src/modules/edgebus/hub.go:295-297` and `:437-438`, which say the
  account's JetStream is provisioned a beat after the first connection,
  are false. Reading the flag before the wait, not after it, keeps a
  `Hub.Close` during the wait's 10 seconds from hiding a refusal (next
  Decision). The lookup does not re-fetch a
  registered account unless it has expired (`server.go:2064-2084`), and
  hub account JWTs carry no expiry (`src/modules/edgebus/keys.go:242-251`).
  The arithmetic this replaces counted the attaching account itself once
  the server had enabled it (`src/modules/edgebus/hub.go:452-456`), so a
  canceled attach on a fresh hub read 512 + 2 × 128 MiB against a 640 MiB
  ceiling and named storage. Its `maxStorePending` reason did not hold
  either: the server clears the flag before startup completes
  (`jetstream.go:521-526`, `:546-553`). If U8's fresh-hub test ever
  names storage, enablement lags connect and this Decision is wrong.
- **Only a storage refusal leaves a live hub's edge account disabled.**
  Why: hub accounts reserve no memory (`keys.go:245-250` sets
  `MemoryStorage: jwt.NoLimit`, and `sufficientResources` sums only
  positive limits, `jetstream.go:2637-2649`), so the memory checks
  (`:2654`, `:2676`) cannot refuse them. Every edge JWT carries a disk
  budget (`hub.go:419-420`), so its claims never take the disable branch
  (`jetstream.go:854-857`). What remains is a server that is shutting
  down (`jetstream.go:1181-1207`), and shutdown clears every account's
  JetStream (`jetstream.go:1072-1074`, reached from `server.go:2611`).
  `Close` sets `h.closed` before it calls `srv.Shutdown`
  (`hub.go:593,606`), so the hub reads `closed` after the flag. An open
  hub at that read means a false flag was a refusal. A failed lookup
  cannot happen either, since a live server keeps every authenticated
  account registered (`server.go:2217`).
- **No exception to the storage rule: the attach holds off `Close` from
  the connect through the flag read.** The attach holds a read lock on a
  hub mutex from before the connect until after the flag read, and
  `Close` takes that lock for writing before `srv.Shutdown`. Shutdown can
  then never clear an account's JetStream between the connect and the
  read, so a false flag on a hub that was open at the connect is always a
  refusal. `Close` waits only for that short section, never for the
  10-second wait. (decided by the user, 2026-09-30)
- **The refusal carries its own code, `edgebus/storage`.** Why: a text
  match cannot tell it from a failed stream setup, because a stream whose
  `MaxBytes` exceeds the account budget fails with the server's own
  "insufficient storage resources available"
  (`server/stream.go:831` to `jetstream.go:2544-2546`). A code is what
  `errs.CodeOf` reads, outermost first (`src/common/errs/code.go:58-61`),
  and what the hub's skip event already reports as `error.type`
  (`hub.go:260-263`, `:684-689`).
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

- **The verifier routes `tools/buf/*` to the proto gates, not the Go
  module gates.** A change under `tools/buf/` runs format, lint, breaking,
  and the generate drift check through the pinned buf, plus `go mod
  verify` for that module. The `add_module` skip added in `5c8b1a7c`
  goes. The diff to `verify-change.sh` is merge-gate configuration and is
  staged for a person's guardrail review before `land`. (decided by the
  user, 2026-09-30)
- **The edge-bus storage error is re-planned in this plan.** `AttachEdge`
  names the storage limit if and only if the server refused this edge
  account's JetStream enablement for its storage reservation, whatever
  error the wait loop returns and whatever the caller's context. A
  deterministic test covers each direction: a refused account names
  storage under a canceled or short context, and an enabled account whose
  wait fails for another reason (canceled context on a fresh hub, retry
  after a failed stream setup) does not. The Decision on computing the
  error from the ceiling is replaced, since its `maxStorePending` reason
  does not hold after startup in nats-server v2.14.6. Implement and
  review re-run for the new units only. (decided by the user, 2026-09-30)
- **`go mod verify` for `tools/buf` runs before the first pinned buf
  command.** Why: `go tool -modfile=tools/buf/go.mod buf` builds buf from
  the module cache that `go mod verify` checks (`go help mod verify`), so
  a modified cache fails before it runs. `go -C tools/buf mod verify`
  prints "all modules verified" on this tree.
- **A `tools/buf/` change drops the `--path` limits from format, lint,
  and breaking.** Why: a new buf version can judge any schema file
  differently, so a run that names one `.proto` beside the pin bump still
  checks the whole module. The existing no-path forms already do this
  (`verify-change.sh:766-767`, `:795-796`).
- **`--print-selection` reports the protobuf gates and the tool module.**
  Why: the selection tests run with `go` replaced by a stub that exits 98
  (`tools/hooks/tests/run.sh:693-699`), so the selection output is the
  only thing a test can check without running buf. Today it prints only
  `service_otel_integration`, `module=`, and `dependent=` lines
  (`verify-change.sh:417-435`). The new lines print only when set, so
  every existing exact-match case (`run.sh:720-733`) keeps its output.
- **`captureapi` blank-imports `net/switching/v1` alone.** Why: its one
  production validation is the uploaded chunk
  (`src/services/device/internal/captureapi/edge_service.go:539`). A
  chunk's packets carry `net/switching` VLAN rules in their mirror fields
  (`spec/proto/flowseer/net/capture/v1/packet_record.proto:35`,
  `mirror.proto:90-110`), imported as an option only (`mirror.proto:7`),
  so `mirror.pb.go` does not import the package. protovalidate v1.4.0
  resolves predefined rules through `protoregistry.GlobalTypes`
  (`buf.build/go/protovalidate@v1.4.0/validator.go:60`) and refuses to
  compile an unknown one (`cache.go:61-66`). No message a chunk reaches
  carries a `net/key` rule. The capture schema's only one is
  `LocalInterfaceSource.interface_name`
  (`spec/proto/flowseer/model/capture/v1/capture_session.proto:43`), and
  `src/services/device/internal/host/validation.go:17-18` links `net/key/v1`
  for the operator requests that carry it. The `net/switching` import
  stays although `model/access/v1` also links the package through
  `net/interface/v1`, since that path is incidental to capture.

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
11. A change under `tools/buf/` runs `go mod verify` for that module and
    the protobuf gates through the pin, and `tools/buf/go.mod` or
    `go.sum` selects no Go module gate. Example:
    `verify-change.sh --print-selection -- tools/buf/go.mod` prints
    `proto=true` and `tool_module=tools/buf mode=mod-verify` and no line
    that starts with `module=`. `verify-change.sh -- tools/buf/go.mod` logs
    `go -C tools/buf mod verify` before the first buf command and passes.
12. `AttachEdge` returns `edgebus/storage` if and only if the server
    refused the edge account's JetStream, including when a `Hub.Close`
    races the attach (Decisions).
    Example: on the 640 MiB hub of
    Requirement 10, edge B attached under a canceled context returns an
    error for which `errs.CodeOf` gives `edgebus/storage`. On a fresh hub
    with the same budgets, edge A attached under a canceled context
    returns `edgebus/hub`, and attaching A again with a live context
    succeeds.
13. `captureapi` links only the rule packages the messages it validates
    reach. Example: `validation_imports.go` blank-imports
    `net/switching/v1` alone, and `go test ./src/services/device/internal/captureapi/`
    passes.

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
- Failing a refused attach before the wait. U8 reads the flag before the
  wait but acts on it only after the wait fails, as the 2026-09-30
  Decision frames it. Under a live context a refused account still waits
  out the loop's 10-second deadline (`src/modules/edgebus/hub.go:299`)
  before `AttachEdge` returns.
- Mapping `edgebus/storage` to a Connect code in `AttachBus`
  (`src/services/device/internal/edgeapi/service.go:180-183`).
- Hostile input to the verifier. It reads path lists from the
  repository's own contributors and agents, a trusted author, and U7 does
  not harden it against crafted path names.

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

### U7. Route the pinned buf module to the protobuf gates

Files: .agents/skills/verify-change/scripts/verify-change.sh, tools/hooks/tests/run.sh, .agents/skills/verify-change/SKILL.md, .agents/skills/verify-change/references/gate-coverage.md
After: U1
Change: the `add_module` skip at `verify-change.sh:214-216` is deleted.
An independent `tools/buf/*` case beside the mibgen one (`:295-300`) sets
`proto=true` and a `buf_module` flag for every path under `tools/buf/`.
In the classification loop, a `tools/buf/go.mod|tools/buf/go.sum` arm
ahead of the generic `go.mod` arm (`:275-278`) selects no Go module, and
its comment says the independent case routes those files to the
protobuf gates. Under `--full`, module discovery (`:238-240`) sets
`buf_module` for `./tools/buf/go.mod` instead of calling `add_module`,
since `--full` already selects the protobuf gates. With `buf_module` set,
the protobuf block runs `go -C tools/buf mod verify` first, then format,
lint, and breaking without `--path`, then the generate drift check
(`:802-807`). `--print-selection` prints `proto=true` when the protobuf
gates are selected and `tool_module=tools/buf mode=mod-verify` when the
tool module is, after the `service_otel_integration` line and only when
set. `SKILL.md:44-45` and `gate-coverage.md` say that a `tools/buf/` path
selects these gates and no Go module gate, since the module holds no Go
package (`golangci-lint run` there exits 5, "no go files to analyze").
`tools/hooks/tests/run.sh` is a policy surface (`AGENTS.md`, Hard
boundaries), and the edit hook asks before each edit to it
(`tools/hooks/pre-tool-policy.sh:54-69`). The hook never asks for
`verify-change.sh`, which is merge-gate configuration by the 2026-09-30
Decision, so the implementer treats it the same way without a prompt.
U7 runs last in the implementing session, after U8 and U9 have
committed, so its staged diff cannot enter their commits. It ends with
all four files staged and uncommitted for a person's guardrail review.
`implement` leaves U7 `in_progress` with the note "staged for guardrail
review" and names the staged paths in its report. It does not use
`blocked`, which means three failed verifier rounds and sends a unit back
to `plan` (`implement/SKILL.md:119-121`, `land/SKILL.md:72`). After a
person reviews and commits the diff, the session re-runs U7's Verify
command and runs `ledger.py set U7 passed`. Until then `land` stops on the
`in_progress` unit (`land/SKILL.md:71`).
Tests: in `tools/hooks/tests/run.sh`, beside the selection cases
(`:696-739`):
- `select_verifier -- tools/buf/go.mod` and `-- tools/buf/go.sum` each
  print `proto=true` and `tool_module=tools/buf mode=mod-verify`, and no
  line that starts with `module=` (a `*module=*` glob would match
  `tool_module=`). Before the change both print only
  `service_otel_integration=false`.
- `select_verifier -- buf.gen.yaml` prints `proto=true` and no
  `tool_module=` line.
- The selection fixture (`:680-683`) gains `tools/buf/go.mod`, and
  `select_verifier --full` prints `tool_module=tools/buf mode=mod-verify`
  and no line that starts with `module=./tools/buf`. Today `--full` prints
  `module=./tools/buf mode=full`, because discovery passes
  `./tools/buf` and the skip compares against `tools/buf`. Match the
  anchored form `*$'\n'module=./tools/buf*`, since `tool_module=tools/buf`
  contains `module=tools/buf`. The first-line check at `:702` still
  holds.
- The exact-match cases at `:720-733` pass unchanged.
- End to end, `verify-change.sh -- tools/buf/go.mod` on this tree logs
  `+ go -C tools/buf mod verify` before the first `buf` command, runs
  breaking without `--path`, and ends "FlowSeer verification passed."
Nothing in the unit tests the dropped `--path` limits for a run that names
a `.proto` file beside a `tools/buf/` path, since the selection output
does not show them. The implementer reads that branch in the diff.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .agents/skills/verify-change/ tools/hooks/tests/run.sh tools/buf/go.mod`

### U8. Name storage only for an account the server refused

Files: src/modules/edgebus/hub.go, src/modules/edgebus/storage_test.go, src/modules/edgebus/README.md, docs/solutions/architecture-patterns/per-account-jetstream-disk-budgets-reserve-against-the-server-store-ceiling.md
After: U6
Change: `hub.go` declares `ErrCodeStorage = errs.NewCode("edgebus/storage")`
beside `ErrCodeHub` (`:23-24`), with a doc comment saying it marks an edge
attach the server refused because the edge account's budget does not fit
under the store ceiling. After `connectAccount` returns (`:431`) and
before `waitForJetStream` (`:439`), `ensureEdgeAccount` looks the account
up with `srv.LookupAccount(pub)`, reads `JetStreamEnabled()`, and then
reads `h.closed` under `h.mu`, in that order (Decisions). It records the
account as refused when the lookup succeeds, the flag is false, and the
hub is open. When the wait fails, an account not recorded as refused
returns the existing `wait for the edge account JetStream` error with
`ErrCodeHub`, and a refused one returns `errs.From(err).Code(ErrCodeStorage)` with
the `edge`, `ceiling_bytes`, `central_budget_bytes`, and
`edge_budget_bytes` attributes, the ceiling read from
`srv.JetStreamConfig().MaxStore` and left out when that returns nil. Its
message starts with "storage limit exceeded" and names the edge budget
and the ceiling, and no edge count. The `JetStreamNumAccounts` arithmetic
and its comment (`:441-466`) go. A two-line comment at the flag read says
that the server enables the account during CONNECT, so a false flag on an
open hub is a refusal. The `waitForJetStream` doc (`:295-297`) and the
comment at `:437-438` drop the claim that JetStream is provisioned a beat
after the first connection. The wait itself stays (Open questions).
`README.md:45-51` adds that an attach the server
refuses returns `edgebus/storage`. In the solution doc:
- the `symptoms` entry at `:11` names `edgebus/storage` in place of
  `ErrCodeHub`, and `root_cause` (`:14`) cites `sufficientResources` at
  `server/jetstream.go:2629-2684`
- `:101-131` describe the lookup and the flag with the enablement chain
  and the shutdown order under Decisions, and drop the claim at
  `:108-110` that the arithmetic makes the refusal certain
- the message shape at `:113-115` and the example at `:230-236` quote the
  error `TestAttachEdgeRefusedPastStoreCeiling` returns
- `:163-169` say "account budget sum" where they say "account count", and
  "Why This Matters" (`:179-186`) says the hub asks the server
- every `hub.go`, `keys.go`, and `storage_test.go` line cite is re-read
  after the change. Three are wrong today: `defaultCentralBudget` and
  `defaultEdgeBudget` sit at `hub.go:120-121`, not `:121-122` (`:157`),
  `quietLogger.record` at `:650-664`, not `:651-664` (`:185`), and the
  snippet quoted under `keys.go:237-247` (`:37`) is `keys.go:244-251`.
Tests: in `storage_test.go`, each case on a hub with `MaxStoreBytes`
640 MiB, `CentralBudgetBytes` 512 MiB, and `EdgeBudgetBytes` 128 MiB:
- `TestAttachEdgeRefusedPastStoreCeiling` (`:12-37`) also asserts that
  `errs.CodeOf` of the second edge's error is `edgebus.ErrCodeStorage`.
- `TestAttachEdgeRefusedPastStoreCeilingAfterFailedAttach` (`:39-67`)
  runs the second edge under a canceled context and under a 50 ms
  deadline, a fresh hub each, and both return `ErrCodeStorage`. The first
  edge's error, from stream setup, carries `ErrCodeHub` although its
  server text names storage.
- `TestAttachEdgeCanceledOnFreshHubIsNotStorage` attaches edge A to a
  fresh hub under a canceled context. The error carries `ErrCodeHub` and
  its text lacks "storage". A second
  `AttachEdge` for A with `context.Background()` succeeds, which proves
  the server had enabled the account. Before the change this test fails,
  since `hub.go:452-456` reads 512 + 2 × 128 MiB.
- `TestAttachEdgeRetryAfterFailedStreamSetupIsNotStorage` sets
  `EdgeStreamMaxBytes` to 256 MiB, so edge A's first attach fails in
  stream setup after its wait succeeded (`hub.go:439,469`).
  A retry of A under a canceled context returns `ErrCodeHub` without
  "storage". Before the change it names storage.
The lock added by the "No exception to the storage rule" Decision gets a
test: `Close` started while an attach holds the read lock blocks until the
flag read completes (a test hook or a held lock makes the order
deterministic), and a refused attach in that window still returns
`edgebus/storage`. A server that stops on its own stays outside the rule.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/edgebus/ docs/solutions/architecture-patterns/per-account-jetstream-disk-budgets-reserve-against-the-server-store-ceiling.md`

### U9. Link only the rule package capture validation reaches

Files: src/services/device/internal/captureapi/validation_imports.go
After: none
Change: `validation_imports.go` blank-imports
`generated/go/proto/flowseer/net/switching/v1` alone. Its comment says
why: an uploaded chunk's packets carry the `net/switching` VLAN rules in
their mirror fields, the schema imports that file as an option only, and
protovalidate cannot compile a rule whose extension is not registered.
The `net/key/v1` import and the comment's `net/key` clause go (Decisions).
Tests: `go test ./src/services/device/internal/captureapi/` passes. Its
chunk validation cases (`edge_service_test.go:196`, `:894`) compile the
chunk's rules, and `store_test.go:28` links `net/key/v1` for the tests
that validate a session config. Nothing in the unit pins the
`net/switching` import, since `model/access/v1` links that package too
through `net/interface/v1`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/captureapi/`

Waves: U1 U5 U6 | U2 U3 U4 | U7 U8 U9

U1 to U6 have landed. U9 has no prerequisite and joins the open wave.
Within that wave U8 and U9 run first, and U7 runs after both have
committed, since its diff stays staged (U7 Change).

## Verification

Run the verifier over the union of changed paths. Do not use `--full`,
because it builds `generated/go/yang` and exhausts host memory. After the
last unit, run the pinned buf's `generate` and check that `git status
generated/` holds only the files U2, U3, and U4 changed.

For U7 to U9:

- `verify-change.sh --print-selection -- tools/buf/go.mod` prints the two
  lines Requirement 11 names. The end-to-end run on `tools/buf/go.mod`
  calls the remote plugins `buf.gen.yaml:20,23` name, so it needs network
  access to buf.build.
- `go test -race -count=5 -run 'TestAttachEdge' ./src/modules/edgebus/`
  passes every time, since each canceled-context case must hold without a
  timing margin. The background-context refusal case waits out the loop's
  10 seconds on each run.
- `go test ./src/services/device/internal/captureapi/` passes after U9.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] Package READMEs and convention docs updated in the same change as
      their code.
- [ ] `grep -rn field_presence spec/proto/flowseer` prints nothing.
- [ ] No "RFC 7868" left in `src/edge/netpen/layers/glbp.go`,
      `attacks/fh/glbp.go`, or `attacks/fh/harvest_fh.go`.
- [ ] U7's staged diff passed a person's guardrail review and they
      committed it.
- [ ] This plan's `status` set, with an outcome note under its title.
- [ ] No plan labels in code.

## Open questions

- Should `tools/hooks/proto-check.sh` run the pinned buf so the hook and
  the verifier format with one version? It is a policy surface. Stage it
  for a person's review and do not edit it inside U1.
- Settled by U1: `buf.gen.yaml:23` pins `buf.build/connectrpc/go:v1.20.0`.
- Does anything still need the wait at `src/modules/edgebus/hub.go:439`?
  Enablement and the JetStream API imports complete inside CONNECT
  (Decisions), so the lag its comments name does not exist. Whether
  something else lags after connect is unverified. U8 corrects the
  comments and keeps the wait.
