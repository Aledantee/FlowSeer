---
title: Remote Packet Capture Phase 3d, Lab Validation - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-09-1213-feat-remote-packet-capture-plan.md
---

# Remote Packet Capture Phase 3d, Lab Validation - Plan

> Re-planned 2026-09-27 against the current tree: U3c is landed
> (`4fdd8897..7144a6a6`, an ancestor of the re-plan branch), and the user
> reports the ICX7150 and a MikroTik online. The read-only investigation added
> here found that the two lab facts the phase turns on — a MikroTik that can
> emit TZSP and a proven ICX7150 SPAN destination with its cabling to an edge
> interface — are established by no evidence in the repository. The phase is
> blocked on those facts rather than on code; the code it would validate is
> landed and nothing in the tree can produce the hardware evidence.

## Goal

The capture path is validated against the two lab facts the containerised
senders in U2 and the live-central harness in U3c cannot reach: an ICX7150
local SPAN into the edge's capture interface, and a MikroTik TZSP stream to the
edge's receiver, each producing an artifact `capinfos` reads. The means is a
recorded lab run under the usual device-write approval, capturing what a
shipping mirroring ASIC puts on the wire. This phase is complete when both runs
have produced an artifact and the interoperability gap the parent's Verification
names — no shipping-ASIC bytes exercising a mirror decapsulator — is closed for
the local-SPAN and TZSP paths, and honestly restated for the ERSPAN path it
still does not cover.

## Decisions

The parent plan's Decisions apply. What this phase decides when re-planned:

- Which lab devices are in the run and what each one proves. The lab's
  ICX7150-24-POE is the FastIron model without ERSPAN, so it exercises the
  local-interface source through local SPAN; the MikroTik exercises the TZSP
  receiver. Neither produces ERSPAN, and none is borrowed for it, so ERSPAN
  interoperability stays proven only against golden pcaps and containerised
  senders — stated in the handoff rather than papered over.
- The mirror-session setup on each device is by hand and is a device write,
  so it needs the advance notice and approval the lab runbook requires; the
  capture path itself is read-only from the device's point of view.

## Requirements

Carried from the parent: R5 (loss attributable) and R6 (the stored artifact is
a pcapng `capinfos` reads), each asserted against real hardware rather than a
synthetic frame, and the parent's Verification lab check that R3's
decapsulation holds for the TZSP path on real wire.

## Out of scope

- ERSPAN against a shipping ASIC: no lab device emits it, so it stays out of
  reach and the handoff says so.
- Any device configuration beyond the by-hand mirror sessions the run depends
  on (parent Out of scope: configuring SPAN/RSPAN/ERSPAN on a managed device).

## Blockers

Both are facts the run cannot invent and no evidence in the tree supplies.

- **The TZSP sender is unidentified.** The lab inventory records two MikroTik
  devices and neither is usable as-is. LABSW02 (`172.16.0.2`) is a CSS326-24G-2S+
  running SwOS v2.18 (`docs/research/device-inventory/lab/labsw02-mikrotik-css326.md:19-20`),
  and SwOS "has no CLI, no API, and no SSH" (`docs/research/device-inventory/targets/mikrotik.md:160-162`);
  its only mirroring surface is a local port-mirror feature, configured and
  disabled (`labsw02-mikrotik-css326.md:107`), with no sniffer or TZSP field
  anywhere. Lab_SW01 is a CRS317-1G-16S+ on RouterOS 6.49.20 that appears only
  as an LLDP neighbour of LABSW03/04/05/06 (`docs/research/device-inventory/lab/labsw06-ruckus-icx7150.md:149`,
  `labsw03-huawei-s220.md:124`, `labsw04-lancom-gs2326.md:195`, `labsw05-cisco-sg220.md:156`);
  no dossier, management address, credentials, or config surface for it exists
  anywhere in the repository (there is no `labsw01-*` file), and its firmware
  version is known only from the LLDP string, not from a read. The repository's
  only TZSP-streaming evidence is a third-party OpenAPI for RouterOS **7.24**
  (`spec/openapi/mikrotik/routeros-7.24-openapi.json`, `/tool/sniffer` with
  `streaming-server`), which does not establish 6.49.20, and the target dossier
  says the binary API `/listen` is "the only native streaming mechanism"
  (`docs/research/device-inventory/targets/mikrotik.md:127-131`). The direction
  record's general claim that "MikroTik streams over TZSP"
  (`docs/architecture/2026-09-09-remote-packet-capture-direction.md:52-53`)
  names no device and settles nothing here.
- **No SPAN destination port or cabling is proven.** The ICX7150 dossier
  records port mirroring as "Not checked" (`labsw06-ruckus-icx7150.md:130`).
  The single topology fact offered — Kali `labtest` (`172.16.0.21`) on ICX port
  `1/1/12` over `eth1` (`labsw06-ruckus-icx7150.md:148`) — is an LLDP
  adjacency, not a mirror destination: a SPAN destination port does not carry
  ordinary host traffic in the way `1/1/12` does, so `eth1` on `1/1/12` cannot
  be read as the capture interface without a second, spare link. The Kali host's
  interfaces are `eth0` (on LABSW04 port 8, `labsw04-lancom-gs2326.md:195`),
  `eth1` (on LABSW06 `1/1/12`), and `eth1.999` for netpen; no spare interface or
  cable to an ICX mirror port is documented. Which ICX port would be the SPAN
  destination, and which Kali interface is cabled to it, is unknown.

Neither blocker is a code defect. The engine opens a local interface on
`interface_name` and a UDP mirror receiver on `udp_port`, both from the
operator's `CaptureSessionConfig` (`src/modules/capture/engine.go:90-104`), so
once the sender and the cabling are known the run reduces to a deployment, a
by-hand mirror session, and the two `capinfos` checks below.

## Verification

Once the blockers clear, each run proves the parent's R6 and the TZSP arm of
R3 on real wire:

- Local SPAN: an ICX7150 mirror session sends a known active port's frames to
  the destination port; the edge captures on the cabled interface; the stored
  artifact's `capinfos` reports the packet count, link type Ethernet, and the
  session's snap length, and a frame known to be on the mirrored segment
  appears in it.
- TZSP: the identified MikroTik streams the sniffed segment to the edge at
  `udp_port` 37008 with encapsulation `TZSP`; the stored artifact's `capinfos`
  reports the same three fields for the inner Ethernet frames, proving the
  receiver decapsulated on the real wire rather than on a fixture.

`.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-09-18-1423-feat-remote-packet-capture-phase3d-plan.md`
for this re-plan; the run itself changes no code.

## Open questions

The decisions needed to unblock the run, each a lab fact only a person or a
live read can supply:

- Which device is the TZSP sender, what is its management address, and does its
  firmware expose a streaming sniffer (`/tool sniffer` with `streaming-server`,
  or SwOS's HTTP `.b` API)? Lab_SW01's address and credentials are not in the
  repository, and RouterOS 6.49.20's TZSP support is unverified.
- Which ICX7150 port is the SPAN destination, and which edge interface is cabled
  to it? A second, spare link is required because the mirror destination cannot
  be the Kali host's active `eth1` port.
- Is the edge host the Kali `labtest` (`172.16.0.21`), and can it run the agent
  with the raw-socket and UDP privileges the local-interface and mirror paths
  need? The user proposed it; the repository records no interface or privilege
  check for it.
- Parked by drive: Which spare Kali interface is cabled to which ICX7150 SPAN
  destination port? Options: cable a spare Kali interface to a spare ICX port |
  name another edge host with an existing dedicated capture link. Recommended:
  cable a spare Kali interface, because Kali is already on the ICX management
  network and the active `eth1` link must remain available.
- Parked by drive: Which MikroTik can emit TZSP to the edge, and how can its
  capability be checked? Options: provide Lab_SW01's management endpoint for a
  read-only RouterOS sniffer check | identify another TZSP-capable MikroTik.
  Recommended: check Lab_SW01, because it is the lab's documented RouterOS
  device and LABSW02 runs SwOS.
