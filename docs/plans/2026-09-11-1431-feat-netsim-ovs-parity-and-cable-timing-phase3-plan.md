---
title: Network Simulation, Phase 3: Spanning Tree Parity - Plan
type: feat
date: 2026-09-11
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
parent: docs/plans/2026-09-11-1431-feat-netsim-ovs-parity-and-cable-timing-plan.md
---

# Network Simulation, Phase 3: Spanning Tree Parity - Plan

## Goal

The spanning tree layer speaks legacy 802.1D on a port that hears a version
0 BPDU and returns to RSTP after a migration delay or on a management
check, detects an edge port by the absence of BPDUs when auto-edge is set,
sends at most the transmit hold count of BPDUs per port per second, and
counts transmitted, received, and bad BPDUs per port, with the settings
and the counters carried through the loader and the export. The means is
a version and type on `stp.BPDU` with a codec for the two legacy shapes, a
per-port migration state on the layer driven by the timers the layer
already keeps, a transmit gate on every emission, counters on `PortInfo`,
and six fields on the `net/protocol/stp/v1` messages. The phase is wrong
if a consumer needs a legacy root port to originate Topology Change
Notification BPDUs or needs per-VLAN or multiple instances, which stay
out.

## Decisions

The parent's Decision on spanning tree holds. This phase settles the
shapes its stub left open:

- `stp.BPDU` gains `Version uint8`, the version octet as received or as
  written, and `Type BPDUType`, a `uint8` enum of the package's own whose
  zero value `BPDUTypeRapid` is the RST BPDU, with `BPDUTypeConfiguration`
  and `BPDUTypeTopologyChangeNotification` after it; the codec maps them
  to the wire types 0x02, 0x00, and 0x80. `Encode` writes the shape `Type`
  names, so a literal without the field is the RST BPDU as today: an RST
  BPDU with `Version` 2 when the field is below 2; a Configuration BPDU
  (35 octets after the LLC header: no Version 1 Length field, flags
  masked to Topology Change and Topology Change Acknowledgment, version
  0); a TCN BPDU (4 octets after the LLC header, version 0). `Decode`
  checks the length floor per type after the 7-octet LLC and header
  prefix (39 total for RST, 38 for Configuration, 7 for TCN), applies the
  hello-time rule only to the two shapes that carry timers, and accepts a
  version 0 or 1 type 0x00 or 0x80 body and a version 2 or above type
  0x02 body; anything else stays `unsupported-bpdu`. A decoded
  Configuration BPDU has `Role` Designated, no proposal, no agreement.
  Why: 802.1D-2004 clause 9.3 gives the three shapes and their lengths,
  RSTP-MIB names the versions (`dot1dStpVersion`,
  `spec/mib/ietf/RSTP-MIB:49`), and Open vSwitch classifies the same three
  types (`CONFIGURATION_BPDU = 0x0`, `TOPOLOGY_CHANGE_NOTIFICATION_BPDU =
  0x80`, `RAPID_SPANNING_TREE_BPDU = 0x2`,
  https://raw.githubusercontent.com/openvswitch/ovs/branch-3.3/lib/rstp-common.h).
  A zero-valued enum for the RST shape is what keeps every existing BPDU
  literal in the tests an RST BPDU.
- Protocol migration is per port, the state machine of 802.1D-2004
  clause 17.24 in the layer's event model. Each port keeps `sendRSTP
  bool` and `mdelayWhile time.Time`. A port that comes up enters checking:
  `sendRSTP` true, `mdelayWhile` now plus `MigrateTime` (3 s, fixed by
  Table 17-1, `RSTP_MIGRATE_TIME 3` in
  https://raw.githubusercontent.com/openvswitch/ovs/branch-3.3/lib/rstp.h).
  While `sendRSTP` is true and `mdelayWhile` has passed, a received
  Configuration or TCN BPDU selects STP: `sendRSTP` false, `mdelayWhile`
  restarted. While `sendRSTP` is false and `mdelayWhile` has passed, a
  received RST BPDU returns to checking. `Layer.Mcheck(now time.Time,
  port string) Effects` returns to checking at once and may emit, since
  a designated port answers with an RST BPDU. A received BPDU inside the
  delay still carries information: the priority vector is taken as
  today. A port with `sendRSTP` false emits Configuration BPDUs on
  designated ports, never proposes, and takes no rapid transition:
  `recompute`'s immediate forwarding of a synced point-to-point root port
  is gated on `sendRSTP`, so a port in compatibility mode climbs the
  forward-delay ladder whatever its role; agreement handling is
  unchanged since a legacy peer sends none. Why: this is the
  CHECKING_RSTP, SELECTING_STP, SENSING cycle OVS implements in
  `port_protocol_migration_sm`
  (https://raw.githubusercontent.com/openvswitch/ovs/branch-3.3/lib/rstp-state-machines.c)
  and RSTP-MIB's `dot1dStpPortProtocolMigration` describes the
  management check as a write that "forces this port to transmit RSTP
  BPDUs" and "always returns false(2) when read"
  (`spec/mib/ietf/RSTP-MIB:130`), so the check is a run-time call, not a
  configuration field, and the schema carries none.
- Auto-edge is the Bridge Detection state machine of 802.1D-2004 clause
  17.25 over the existing `edge` field. `Port.AutoEdge bool`; each port
  keeps `edgeDelayWhile time.Time`. A port that comes up, and a port that
  receives any BPDU, sets `edge` to `AdminEdge` and `edgeDelayWhile` to
  now plus the edge delay (`MigrateTime` on a point-to-point port,
  `MaxAge` in force otherwise); a port becomes an edge port when
  `AutoEdge` is set, `sendRSTP` is true, it is proposing (designated,
  discarding, point-to-point), and `edgeDelayWhile` has passed. Becoming
  an edge port moves the port to forwarding at once, as `AdminEdge`
  does today. An auto-detected edge port that receives a BPDU loses its
  edge status, returns to discarding, and restarts the forward-delay
  ladder, proposing again on a point-to-point port; an `AdminEdge` port
  keeps its edge status on a BPDU, as today. Why: OVS's
  `bridge_detection_sm` sets the edge on `(edge_delay_while == 0) &&
  auto_edge && send_rstp && proposing`, its `port_receive_sm` clears
  `oper_edge` and restarts `edge_delay_while` on every received BPDU
  (file cited above), and `ieee8021MstpCistPortAutoEdgePort` names the
  parameter (`spec/mib/ieee/IEEE8021-MSTP-MIB-201806210000Z.mib:1426`,
  the CIST parameter, not an MSTP instance). The admin-edge exception
  keeps today's behavior, which the previous phase's tests fix.
- The transmit hold count is `stp.Config.TxHoldCount uint8`, 0 meaning 6,
  refused outside 1 through 10. Each port keeps `txCount int` and
  `txTick time.Time`: `txCount` drops by one every second after the
  port's first transmission since it was zero, `txTick` being the next
  such instant, and before an emission the layer folds every elapsed tick
  into `txCount`. Emissions have kinds: the designated BPDU (hello,
  proposal, or the reply to an inferior BPDU, all built by `makeBPDU`) and
  the agreement (built by `makeAgreementBPDU`). An emission with
  `txCount` at the bound is held by kind: the port records which kinds
  are pending, `NextWake` includes `txTick` while any is, and `Wake` at
  the tick releases the agreement first and the designated BPDU second,
  each rebuilt from the port's state at release and each gated again, so
  a bound of 1 releases one per tick. A held emission of a kind already
  pending replaces nothing; the kind is released once. Why: this is the
  Port Transmit state machine's `txCount < TxHoldCount` gate with the
  Port Timers' once-a-second decrement (OVS `decrease_rstp_port_timers__`
  and `port_transmit_sm`, file cited above); the default 6 and the 1
  through 10 range are `RSTP_DEFAULT_TRANSMIT_HOLD_COUNT 6`,
  `RSTP_MIN_TRANSMIT_HOLD_COUNT 1`, `RSTP_MAX_TRANSMIT_HOLD_COUNT 10` in
  OVS's `rstp.h`, and RSTP-MIB's `dot1dStpTxHoldCount` is 1..10
  (`spec/mib/ietf/RSTP-MIB:73`; its `DEFVAL 3` is 802.1w's, superseded by
  802.1D-2004 Table 17-1). Rebuilding at release keeps an agreement an
  agreement, which a fresh designated BPDU would not.
- Counters: `PortInfo` gains `TxBPDUs`, `RxBPDUs`, and `BadBPDUs uint64`
  and `SendRSTP bool`. `TxBPDUs` counts emissions the layer produced;
  `RxBPDUs` counts BPDUs `Receive` was given on an enabled port;
  `BadBPDUs` counts the frames the switch could not decode, told to the
  layer through `Layer.BadBPDU(port string)` from `Switch.interceptBPDU`
  when it mutates, so `Peek` counts nothing. Why: OVS keeps `tx_count`,
  `rx_rstp_bpdu_cnt`, and `error_count` per port in `rstp-common.h` and
  its receive path increments the last two exactly there (files cited
  above); no MIB object exists for them, and the export names the OVS
  fields.
- Schema: `BridgeState` gains `tx_hold_count` (uint32, 14, 1..10; absent
  means the source did not report it; mirrors `dot1dStpTxHoldCount`);
  `PortState` gains `auto_edge` (bool, 16; absent means the source did
  not report it; mirrors `ieee8021MstpCistPortAutoEdgePort`),
  `oper_protocol_version` (`ProtocolVersion`, 17, the version the port
  transmits, STP when migrated and RSTP otherwise; absent means
  unreported), `tx_bpdus` (uint64, 18), `rx_bpdus` (uint64, 19), and
  `bad_bpdus` (uint64, 20), each with "Absent means the source did not
  report the counter; zero means none." and naming the OVS field it
  mirrors (`tx_count`, `rx_rstp_bpdu_cnt`, `error_count`). Fields are
  added, none moved; the package README's sources gain
  IEEE8021-MSTP-MIB for the CIST auto-edge object and OVS's
  `rstp-common.h` for the counters. Why: `BridgeState` and `PortState`
  carry the administrative settings beside the observed ones already
  (`admin_edge`, `bridge_hello_time`), `docs/conventions/protobuf.md`
  numbers new fields after the last, and `docs/code-style-proto.md` asks
  every field comment to say what absent means.
- The loader maps `tx_hold_count` to `Config.TxHoldCount` (reports the
  default 6 when absent) and `auto_edge` to `Port.AutoEdge`; the export
  writes `tx_hold_count` in effect, `auto_edge`, `oper_protocol_version`
  from `SendRSTP`, and the three counters; `BridgeState.protocol_version`
  stays RSTP, since the bridge runs RSTP and a port may be in
  compatibility mode. `stp.Diff` reports `tx_hold_count` and `auto_edge`.
  Why: the per-port version is what migration produces, and the
  bridge-level value describes the layer.
- `Switch.Mcheck(now time.Time, port string)` applies the layer's effects
  as `Wake` does, and the fabric drains its emissions and schedules the
  next wake like any other switch call. A wake-up that is not a
  transmission stays that way in the fabric; a held BPDU released at a
  tick is an emission and takes the busy clock like any other. Why:
  phase 1's rule, one rule for every frame.

## Requirements

Every example uses one bridge `B` (priority 32768, address
02:00:00:00:00:02) with ports `1/1/1` and `1/1/2` up and point-to-point
against silent peers, default timers, `Start` at `t0`; `B`'s layer is the
only one driven, and a "received" BPDU is one handed to `Receive` (or, in
the fabric, injected at the port). An inferior Configuration BPDU claims
root priority 61440 from bridge address 02:00:00:00:00:0c; a superior one
claims root priority 4096 from 02:00:00:00:00:0a. Hellos fall at `t0 +
2s`, `t0 + 4s`, and so on.

1. Legacy BPDU decode and encode. Acceptance: `Decode` of a 35-octet
   Configuration BPDU body after the LLC header (protocol 0, version 0,
   type 0, flags 0x01, root and bridge fields of the inferior sender)
   returns `Version` 0, `Type` Configuration, `TopologyChange` true,
   `Role` Designated; `Decode` of a 4-octet TCN body returns `Type`
   TopologyChangeNotification; `Encode` of `BPDU{Type: Configuration}`
   with proposal and role flags set produces a frame whose LLC length is
   38 and whose flags octet is 0x00, and it round-trips through `Decode`;
   `Encode` of a `BPDU{}` is the RST BPDU with version 2 as today; a
   version 2 type 0 body and a version 0 type 2 body are
   `unsupported-bpdu`; a version 3 RST body decodes with `Version` 3.
2. Compatibility on a legacy BPDU. Acceptance: `B:1/1/1` receives an
   inferior Configuration BPDU at `t0 + 4s`; the reply emitted on
   `1/1/1` is a Configuration BPDU (`Version` 0, `Type` Configuration),
   the hello at `t0 + 6s` on `1/1/1` is too while the hello on `1/1/2`
   is an RST BPDU, `PortInfo("1/1/1").SendRSTP` is false, and `1/1/1`
   reaches forwarding at `t0 + 30s` through the forward-delay ladder that
   started when it became designated at `t0`, with no proposal after the
   migration.
3. Return to RSTP. Acceptance: after requirement 2, `Mcheck(t0 + 10s,
   "1/1/1")` makes `SendRSTP` true and the hello at `t0 + 12s` on
   `1/1/1` an RST BPDU; without `Mcheck`, an inferior RST BPDU received
   at `t0 + 10s` (after the delay restarted at `t0 + 4s` has passed) does
   the same, and one received at `t0 + 5s` does not.
4. A legacy BPDU inside the migration delay is ignored for migration.
   Acceptance: a superior Configuration BPDU received on `1/1/2` at
   `t0 + 1s` leaves `SendRSTP` true, and the port takes the root role
   from it.
5. Auto-edge. Acceptance: `1/1/2` with `AutoEdge` true is an edge port
   (`PortInfo.Edge` true, state forwarding) after the wake at `t0 + 3s`
   and not at `t0 + 2s`; an inferior BPDU received on it at `t0 + 5s`
   clears `Edge`, moves it to discarding, and a wake at `t0 + 8s` with no
   further BPDU makes it an edge port again; `1/1/1` with `AutoEdge`
   false stays a non-edge port in discarding at `t0 + 3s`.
6. Transmit hold count. Acceptance: `TxHoldCount` 2; after the hello at
   `t0 + 4s`, three inferior Configuration BPDUs received on `1/1/1` at
   `t0 + 4.1s`, `t0 + 4.2s`, and `t0 + 4.4s` produce one reply emission,
   since the hello took the other slot of that second and a held kind is
   released once; `NextWake` then reports `t0 + 5s`, `Wake(t0 + 5s)`
   emits the held reply, and `PortInfo.TxBPDUs` reads 5 (the link-up
   proposal, two hellos, the reply, the release); `Validate` refuses
   `TxHoldCount` 11. The original example placed the burst at the hello
   instant and counted two replies; the implementation corrected it.
7. Counters. Acceptance: after requirement 2, `PortInfo("1/1/1")` reads
   `RxBPDUs` 1; `Switch.Forward` of an undecodable frame to
   01-80-C2-00-00-00 on a spanning-tree switch reads `BadBPDUs` 1 with
   the trace `unsupported-bpdu` as today, and `Switch.Peek` of the same
   frame leaves it at 1.
8. Schema and export. Acceptance: `netmodel.Stp` writes `tx_hold_count`
   6, `auto_edge` per port, `oper_protocol_version` STP for a migrated
   port and RSTP otherwise, and the three counters; the messages pass
   `protovalidate`; `buf lint` is clean.
9. Loader. Acceptance: a `BridgeState` with `tx_hold_count` 4 and a
   `PortState` with `auto_edge` true load as `Config.TxHoldCount` 4 and
   `Port.AutoEdge` true; an absent `tx_hold_count` reports the default 6.
10. Diff. Acceptance: `stp.Diff` reports `tx_hold_count` 0 to 4 and
    `auto_edge` false to true.

## Out of scope

Everything the parent lists; Topology Change Notification BPDUs
originated by a legacy root port (a received one sets the topology change
timer as a TC flag does); a per-bridge force-version (`dot1dStpVersion`
write); a `protocol_migration` schema field, since the check is a
write-only trigger with no state to carry; MSTP and per-VLAN instances;
BPDU guard and root guard.

## Units

### U1. The legacy BPDU shapes

Files: `src/common/netsim/vswitch/stp/bpdu.go`, `bpdu_test.go`
After: none
Change: `BPDU.Version`, `BPDU.Type`, `BPDUType` and its three values
with `BPDUTypeRapid` the zero value, `Decode` and `Encode` as the first
Decision says; the RST BPDU decodes with the version octet received and
`Type` Rapid, and a `BPDU{}` encodes as it does today.
Tests: `bpdu_test.go`, requirement 1. Each is evidence for this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/stp`

### U2. Migration, auto-edge, the hold count, and the counters

Files: `src/common/netsim/vswitch/stp/config.go`, `layer.go`, `diff.go`,
`layer_test.go`, `config_test.go`
After: U1
Change: `Port.AutoEdge`, `Config.TxHoldCount`, `Validate` for the count;
`MigrateTime`; `sendRSTP`, `mdelayWhile`, `edgeDelayWhile`, `txCount`,
`txTick`, and the pending kinds on the port state; `Layer.Mcheck(now,
port) Effects`; `Layer.BadBPDU(port)`; the migration and detection rules
in `LinkChange`, `Receive`, and `Wake`; `recompute`'s rapid root
transition gated on `sendRSTP`; the transmit gate on every emission site
(the proposal burst, the hello loop, the inferior reply, the agreement)
and the release at the tick; `NextWake` covering `edgeDelayWhile` and a
pending tick; `PortInfo` fields; `Diff` fields; `makeBPDU` setting
`Version` and `Type` from `sendRSTP`.
Tests: `layer_test.go`, requirements 2 through 7's layer cases, the
migration case watched failing first with `sendRSTP` never cleared and
the gate case with the count never charged; `config_test.go`, the
`Validate` and `Diff` cases of requirements 6 and 10. Each is evidence
for this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/stp`

### U3. The switch and the fabric

Files: `src/common/netsim/vswitch/switch.go`, `switch_test.go`,
`src/common/netsim/fabric/stp_test.go`, `src/common/netsim/vswitch/README.md`
After: U2
Change: `Switch.Mcheck(now, port)` applying the effects; `interceptBPDU`
reports a decode failure to the layer when it mutates, before returning
`unsupported-bpdu`; the README's protocol schedule names migration,
auto-edge, the hold count, and the counters, and its rules list cites the
sources above.
Tests: `switch_test.go`, requirement 7's switch cases and a migrated
switch emitting a Configuration BPDU; `stp_test.go`, a fabric switch fed
an inferior Configuration BPDU by a host injection whose reply crossing
carries a Configuration BPDU, and a burst of three injected inferior
BPDUs under `TxHoldCount` 2 whose third reply leaves one second after
the first on the journey clock and takes the busy clock. Each is
evidence for this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim`

### U4. Schema, loader, and export

Files: `spec/proto/flowseer/net/protocol/stp/v1/bridge_state.proto`,
`port_state.proto`, `README.md`,
`src/common/netsim/vswitch/netmodel/netmodel.go`, `netmodel_test.go`,
`export.go`, `stp_test.go`
After: U3
Change: the six fields of the schema Decision with comments in the
files' shape (mirrors and absence meaning; "Must be present." for none);
`buf generate` regenerates `generated/`, which is never edited by hand;
the loader and export mapping of the Decisions; the package README's
sources gain the two entries the schema Decision names.
Tests: `netmodel/stp_test.go`, requirements 8 and 9, the export fixture
passing `protovalidate.Validate`. Each is evidence for this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/protocol/stp src/common/netsim/vswitch/netmodel`

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/protocol/stp src/common/netsim
go test -race ./src/common/netsim/...
```

## Definition of done

- [ ] Verifier green for every changed path, `buf lint` included.
- [ ] The vswitch README and the schema README match the landed API.
- [ ] This plan's `status` set with an outcome note under its title, and
      the parent's `Landed:` line for this phase filled.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

- Whether the edge delay on a shared port should be `MaxAge` in force or
  the bridge's own; the plan says in force, as OVS reads `max_age`.
- The fabric test in U3 covers a held BPDU released at a tick; a release
  that coincides with a data frame's transmission end on the same port is
  ordered by the busy clock and not asserted separately.
