---
title: Network Simulation Analysis Completeness, Phase 3d - Plan
type: feat
date: 2026-09-15
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 3d: Multiple spanning tree instances - Plan

> Implemented. 6 units, 2026-09-15T16:47Z to 2026-09-15T21:02Z.

## Goal

A simulated bridge answers "which link does VLAN 20 block" when VLANs run on
different spanning trees within a region, and answers it consistently across a
region boundary. The means are MST BPDU encoding and decoding, a region digest,
CIST and MSTI priority vectors over the tree structure phase 3b landed, boundary
roles, hop aging, and per-instance flushing. It claims parent R14a, R14b, and
R14e, and extends R9 and R39.

## Decisions

Citations are to `spec/mib/ieee/`: `MSTP` is `IEEE8021-MSTP-MIB-202211080000Z.mib`,
`TC` is `IEEE8021-TC-MIB-202211080000Z.mib`, `ST` is
`IEEE8021-SPANNING-TREE-MIB-202211080000Z.mib`. They describe managed objects,
not the BPDU octet layout.

- **The configuration identifier is format selector 0 (`MSTP:1352`, `Integer32
  (0..0)`, "the format specified in IEEE Std 802.1Q"), a 32-octet name
  (`MSTP:1363`), a 16-bit revision (`MSTP:1373`), and a 16-octet digest
  (`MSTP:1383`). The digest is HMAC-MD5 over the 4096-entry VID-to-MSTID table,
  two octets big-endian per VID, VID 0 through 4095, keyed with these bytes:**

  ```text
  13 AC 06 A6 2E 47 FD 51 F9 5D 2B A2 43 CD 03 46
  ```

  `MSTP:1163` names the table the digest is taken over; the construction, key
  included, is in no in-repo file, so reproduction is what proves it. The
  all-zero table digests to `ac36177f50283cd4b83821d8ab26de62`, the value every
  vendor reports for an unconfigured region, recomputed with Python's `hmac` on
  2026-09-15, which a wrong key or table width could not produce.
- **An MSTI identifier is 1 through 4094 (`TC:317`) and MSTID 0 is the CIST**
  (`MSTP:1143` admits 0 in the FID-to-MSTID allocation); a VID no instance
  claims maps to MSTID 0. **MSTP is protocol version 3:** `ST:378` enumerates
  `stp(0)`, `rstp(2)`, `mstp(3)`, "the values are directly from the IEEE
  standard", so `Decode` gains MST support for version 3, replacing the
  CIST-prefix reading phase 3b left at `stp/bpdu.go:364`.
- **The CIST vector has six components and the MSTI vector four, and one type
  carries both.** `MSTP:211` gives the CIST path cost as the cost "to the CIST
  Regional Root" (13.9:d), `MSTP:723` the CIST regional root (13.9:c), and
  `MSTP:955` the designated port "of the Port's MSTI port priority vector, as
  defined in 13.11". The landed `priorityVector`
  (`stp/layer.go:163`) gains `externalRootPathCost` and `regionalRootID`,
  ordered root, external cost, regional root, internal cost, designated bridge,
  designated port. An RSTP tree sets the regional root from the root and the
  internal cost zero, an MSTI the root from the regional root and the external
  cost zero; constant leading components are neutral lexicographically, so the
  first reduces to the landed four-component order and the second to 13.11's,
  with no branch. A second type would duplicate the comparator.
- **An MSTI bridge identifier carries the MSTID, and Backup detection compares
  the tree's own identifier.** `MSTP:379` makes the MSTI bridge priority "the
  most significant 4 bits of the Bridge Identifier for the MSTI", leaving that
  field's other 12 bits to the MSTID (`MSTP:306`, referencing 13.26.2). `tree`
  gains a `bridgeID`, the CIST's being the landed one with a zero extension, and
  `designatedOrBlocked` (`layer.go:808`) compares it rather than `l.bridgeID`,
  still by equality: two ports of one bridge on a segment share the MSTID.
- **netsim reports no Master role.** Both role enums, CIST (`MSTP:696`) and MSTI
  (`MSTP:965`), list exactly `root`, `alternate`, `designated`, `backup`. On a
  boundary port an MSTI role equals the CIST role, Root included, so netsim
  reports Root where the standard's text says Master: the same forwarding
  answer, under the label the MIB can express.
- **Internal information ages by remaining hops, external by message age.**
  `MSTP:224` gives `ieee8021MstpCistMaxHops` as `Integer32 (6..40)` (13.26.4).
  Information from this bridge's own region is accepted while
  `remainingHops > 1` and re-originated with one fewer; external information
  keeps the landed `MessageAge + 1 <= MaxAge` test (`layer.go:1034`). Both keep
  the landed `3 × HelloTime` silence bound, and the MSTIs use the CIST timers
  with their own hop count. netsim defaults MaxHops to 20.
- **A port is internal when the BPDU it received carries this bridge's
  configuration identifier, and a boundary port otherwise** — an RST or STP BPDU
  always external — where the CIST alone computes and the MSTIs follow its
  roles, loop guard included.
- **One MST BPDU per port carries the CIST and every MSTI, so the transmit
  budget stays per port and no emission kind becomes per instance.** The MSTI
  port table (`MSTP:800` through `MSTP:1008`) has no transmission object at all;
  `ieee8021MstpCistPortEnableBPDUTx` (`MSTP:765`) is a CIST-port object. So
  `txCount`, `txTick`, `pendingDesignated`, and `pendingAgreement`, today on the
  per-tree `portState` (`layer.go:126`), move to a bridge-global per-port record
  beside `portNames` (`layer.go:79`).
- **The STP scope gains no tree component.** `protocolScope`
  (`vswitch/metadata.go:197`) already passes the literal `"0"`, the CIST's
  MSTID. `protocol-link-unknown` stays per port (`switch.go:2422`): an unknown
  link state or duplex is identical for every tree on the port. The gate scope
  is installed once per bridge (`bridge/bridge.go:128`), so a per-tree value
  needs a new `Gate` contract for an answer no caller asks; which instance
  blocks a port is in the per-instance port fact.
- **Per-tree FDB flushing arrives here,** which phase 3b deferred.
  `stp.Effects.Flush` (`layer.go:24`) becomes port and FID-set pairs and
  `Bridge.FlushPorts` (`bridge/bridge.go:158`) respects them; the FDB is keyed
  by `fdbKey{fid, mac}` (`bridge/fdb.go:53`). A change on a tree flushes, on the
  other ports, only the FIDs of VLANs mapped to it; a CIST change on a boundary
  port applies to every tree.
- **Multiple regions are modeled** (user-directed 2026-09-15), which is what the
  external vector and the boundary roles are for. **netmodel stays RSTP-only:**
  `netmodel.go:1165` refuses any other version, so MSTP reaches the layer
  through `ConstructionSpec` only.

Ruled: an MST bridge emits one MST BPDU on every up port, boundary ports
included, and only the CIST drives emission, with `makeBPDU` gathering the MSTI
records from the other trees. Why: a port is internal only once it has received
this region's configuration identifier, so "one MST BPDU per internal port"
never sends the first one and two bridges in a region would not find each other;
and letting each tree emit would spend the per-port transmit budget once per
instance, which this section reserves for one BPDU per port. Cost if wrong: a
boundary port carries MSTI records the neighbouring region discards on the
internal test, which costs octets and nothing else.

The plan this replaces cited `mstp.c`, `mstp.h`, and a Wireshark dissector, none
of them in this repository and none opened, so every claim resting on them is
regrounded above or dropped. Its requirement 4, labelled `R15a-hops`, is folded
into R14b, where the parent puts hop-count aging.

## Requirements

1. **R14a:** MSTP instances select independent trees. **Acceptance example:**
   two switches in one region joined by L1 and L2, VLAN 10 on MSTI 1 and VLAN 20
   on MSTI 2, the non-root switch configured `PathCost: 200_000` on L1 in MSTI 1
   and on L2 in MSTI 2. It roots on L2 for MSTI 1 and on L1 for MSTI 2, so a
   VLAN 10 frame crosses L2 and a VLAN 20 frame crosses L1. It needs the
   per-instance per-port path cost (`MSTP:990`) U1 defines: per-instance bridge
   priority alone cannot split two links between one pair of bridges.
2. **R14b:** Region boundaries follow the CIST and internal information ages by
   hops. **Acceptance example:** switch A in region R1 and switch B in region
   R2, same name and different revision, joined by two links: both MSTIs block
   the link the CIST blocks, and the CIST root and external cost cross the
   boundary. In a four-switch ring in one region whose root is removed, the
   circulating information expires on `remainingHops`, not on message age.
3. **R14e:** Per-tree topology change and per-instance flushing. **Acceptance
   example:** a topology change on MSTI 1, which carries VLAN 10, flushes the
   VLAN 10 entries on the other ports and leaves VLAN 20's in place; a CIST
   change on a boundary port flushes both.
4. **R14a-wire:** The MST BPDU round-trips. **Acceptance example:** the all-zero
   table digests to `ac36177f50283cd4b83821d8ab26de62`, and VID 10 on MSTID 1
   with VID 20 on MSTID 2 to `9357ebb7a8d74dd5fef4f2bab50531aa`. A BPDU with two
   records encodes to a `102 + 16 × 2` octet body and decodes back field for
   field; one whose identifier differs marks the port external.
5. **R9:** Every MSTP configuration field participates in validation,
   normalization, `Clone`, the canonical facts, and `Diff`. **Acceptance
   example:** changing the region revision gives exactly one change, on field
   `mst.revision`; moving VLAN 20 from MSTI 2 to MSTI 1 gives one on each
   instance's `vlans`; a per-instance port cost changes the canonical fact.
6. **R39:** Two corpus cases, each naming its false answer:
   `planning/mstp-vlan-instances-diverge`, both VLANs block the same link, and
   `topology-shadowing/mst-region-boundary`, the MSTIs ignore the boundary.

## Out of scope

- SPT and SPB BPDUs, version 4, agreement digests, L2GP, and Cisco pre-standard
  MSTI encoding, which decode as unsupported; per-VLAN RSTP and SSTP, phase 3e.
- netmodel loading of MSTP, any schema change, a `Master` port role, and per-tree
  state retained across `Derive`, which is phase 5.

## Units

### U1. Region configuration and the digest

Files, in `src/common/netsim/vswitch/stp/`: `mst.go` and `mst_test.go` (new),
`config.go`, `diff.go`, `config_test.go`
After: none
Change: `Config` gains `MST *MST`, whose presence selects MSTP. `MST` holds
`Name string`, `Revision uint16`, `MaxHops uint8`, and
`Instances map[MSTID]Instance`; `Instance` holds `Priority uint16`,
`VLANs []vlan.ID`, and `Ports map[string]InstancePort`; `InstancePort` holds
`Priority uint8`, `PriorityPresent bool`, and `PathCost uint32`. `ConfigID`
holds selector, name, revision, and digest, computed by `MST.ConfigID()` with
the key and table above. `Validate` refuses an MSTID outside 1..4094, a
`MaxHops` outside 6..40, an instance priority that is not a multiple of 4096, a
name over 32 octets, a VID claimed twice, and an unknown instance port, each
with an `mst.instances.<id>.<field>` path. `Normalize` fills `MaxHops` 20 and
the priority defaults and sorts each `VLANs`; `Clone`, `Canonical`, and `Diff`
carry every field, one `trace.Change` each.
Tests: the R14a-wire digest vectors, the R9 example, each refusal by its field
path, `Normalize` idempotent, a `Config` with no `MST` left unchanged by it.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/stp`

### U2. The MST BPDU

Files: `src/common/netsim/vswitch/stp/{bpdu.go,bpdu_test.go}`
After: U1
Change: `BPDU` gains `ConfigID *ConfigID`, `RegionalRootID BridgeID`,
`InternalRootPathCost uint32`, `RemainingHops uint8`, and `MSTIs []MSTIRecord`,
where `MSTIRecord` holds `MSTID`, `Flags`, `RegionalRootID`,
`InternalRootPathCost`, `BridgePriority uint8`, `PortPriority uint8`, and
`RemainingHops uint8`. `Encode` writes the version 3 body when `ConfigID` is
set: the landed 35-octet CIST prefix, version 1 length 0, a version 3 length of
`64 + 16 × len(MSTIs)`, the 51-octet configuration identifier, the internal root
path cost, the CIST bridge identifier, the CIST remaining hops, then 16 octets
per record, for a body of `102 + 16 × len(MSTIs)` octets; the LLC length and the
pad to 60 octets follow, replacing the fixed `minDataLength` (`bpdu.go:279`).
`Decode` reads that layout at version 3, checks the version 3 length against the
record count, and refuses a partial trailing record with
`ReasonUnsupportedBPDU`. A version 3 BPDU too short for the MST body still
decodes as its RST prefix, which a truncated capture and an RSTP peer need.
Tests: the R14a-wire round-trip byte for byte with zero, one, and two records,
the body lengths, a partial record refused.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/stp`

### U3. Per-tree identity, the six-component vector, per-port transmit budget

Files: `src/common/netsim/vswitch/stp/{layer.go,tree.go,layer_test.go,tree_internal_test.go}`
After: none
Change: `priorityVector` gains `externalRootPathCost` and `regionalRootID` and
`compareVectors` compares the six in the Decisions' order. `tree` gains
`bridgeID`, the layer's for the CIST, and `designatedOrBlocked` (`layer.go:793`)
reads it. `txCount`, `txTick`, `pendingDesignated`, and `pendingAgreement` move
off `portState` into a `portTx` map on `Layer` keyed by port name, which `emit`,
`Wake`, and `NextWake` read. Every RSTP construction site sets `regionalRootID`
from `rootID` and leaves the external cost zero, which makes this a refactor.
Tests: the landed RSTP suite passes unchanged; a table test asserts the
comparator reproduces the landed order for vectors with equal regional root and
zero internal cost, and 13.11's for equal root and zero external cost.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/stp`

### U4. Instances, boundary roles, and hop aging

Files: `src/common/netsim/vswitch/stp/{layer.go,mst.go,fact.go,layer_test.go,mst_test.go}`
After: U1, U2, U3
Change: `newLayer` builds one `tree` per configured MSTI beside the CIST, each
with a `bridgeID` carrying the MSTID in the system-ID extension, and fills
`vidToTree` (`tree.go:59`) from the instance VLAN lists. `Receive` classifies
the port internal or external by comparing the received `ConfigID` with the
layer's, stores the CIST message on every port and the MSTI records on internal
ports only, and ages internal information by `RemainingHops > 1` while external
information keeps the landed message-age test. `recompute` runs per tree, and on
a boundary port an MSTI port takes the CIST port's role. `makeBPDU` emits one
MST BPDU per internal port, and the landed RST BPDU with no `MST`.
`InstancePortInfo(mstid, port)` joins `PortInfo`, which keeps answering for the
CIST, and `portInfoSnapshot` (`fact.go:69`) carries the MSTID so a corpus case
can name the blocking instance. `ForwardingFact` (`fact.go:39`) reads the tree
`treeFor(vid)` resolved rather than the CIST, because the gate already answered
from that tree: rendering the CIST's role and state beside a `forwards=false`
the gate took from an MSTI would put a fact and its own decision in the same
step contradicting each other.
Tests: the R14a and R14b examples at the layer and in `fabric/stp_test.go`; a
BPDU from a region whose revision differs marks the port external and leaves the
MSTIs on the CIST role; an internal BPDU with `RemainingHops` 1 is discarded and
one with 2 is stored; a bridge with no `MST` behaves as the landed suite says.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/stp src/common/netsim/fabric`

### U5. Per-instance flushing through the bridge

Files: `stp/{layer.go,layer_test.go,guard_test.go}`,
`bridge/{bridge.go,bridge_test.go}`, `switch.go`, `switch_test.go`, all under
`src/common/netsim/vswitch/`
After: U4
Change: `Effects.Flush` becomes `[]FlushTarget{Port string, FIDs []vlan.ID}`,
an empty `FIDs` meaning every FID, which a link down and a CIST change on a
boundary port produce. `raiseTopologyChange` (`layer.go:568`) fills the FIDs
from the tree's VLAN list and keeps walking `portNames`, so the caller's order
is unchanged. `Bridge.FlushPorts` becomes `Flush([]bridge.FlushTarget)` filtering
on `fdbKey.fid`, and `applySTPEffects` (`switch.go:1901`) passes it through.
Tests: the R14e example; a link down still flushes every FID on the port; a
CIST change on a boundary port flushes both instances' FIDs.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch`

### U6. Corpus and documentation

Files: `src/common/netsim/internal/netsimtest/{stp_cases.go,corpus_test.go,README.md}`,
`src/common/netsim/vswitch/stp/README.md`,
`docs/architecture/2026-09-10-virtual-device-direction.md`,
`docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md`
After: U4, U5
Change: the two R39 cases join `RegisterSTPCases` (`stp_cases.go:155`), the case
count at `corpus_test.go:579` goes from 18 to 20, and the per-use-case lists
gain the two IDs. The `stp` README replaces its "Not modeled" MSTP bullets with
the region, the digest and its key, the two vectors, the boundary rule, hop
aging, and the absence of a Master role, and the direction record's
spanning-tree subsection gains the same. The parent's R14a, R14b, and R14e get
their `Landed:` lines.
Tests: corpus admission, the count, ordering, deterministic re-execution.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim docs/architecture/2026-09-10-virtual-device-direction.md docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md`

Waves: U1 U3 | U2 | U4 | U5 | U6. U3, U4, and U5 are serial because all three
edit `stp/layer.go`; U2 waits on U1 only because the BPDU carries `ConfigID`.

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim docs/
```

## Definition of done

- [ ] Verifier green for every changed path, every requirement example covered
      by a named test, and both digest vectors reproduced with the key in source.
- [ ] The landed RSTP suite passes with no behavior change from U3, and a bridge
      with no `MST` behaves as it did.
- [ ] The `stp` and netsim READMEs and the direction record updated in the same
      change; this plan's `status` set and the parent's `Landed:` line filled.

## Open questions

- **Whether a boundary port's MSTIs should report a distinct role,** which needs
  a ruling on whether `Role` may carry a value no MIB lists. An operator reading
  "Root" on an MSTI port whose region ends there learns less than the standard
  would tell them.
- **The MST BPDU octet layout is verifiable from no in-repo file,** nor is the
  MaxHops default of 20 (`MSTP:224` bounds it to 6..40). U2 writes the offsets
  out so the implementer does not guess; the tests prove only the round-trip and
  the body lengths, and no peer capture is available here.
