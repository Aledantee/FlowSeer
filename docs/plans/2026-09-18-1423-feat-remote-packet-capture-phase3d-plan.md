---
title: Remote Packet Capture Phase 3d, Lab Validation - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
compound: docs/solutions/conventions/restore-offloaded-vlan-before-capture-filter.md
execution: code
parent: docs/plans/2026-09-09-1213-feat-remote-packet-capture-plan.md
---

# Remote Packet Capture Phase 3d, Lab Validation - Plan

> Re-planned 2026-09-27 against the current tree: U3c is landed
> (`4fdd8897..7144a6a6`, an ancestor of the re-plan branch), and the
> ICX7150 and a MikroTik are online. A later live LLDP read confirmed
> Kali `eth0` is cabled to ICX7150 port `1/1/2`, while `eth1.1000` keeps Kali's
> management address. A temporary physical SPAN session produced a valid local
> pcapng through the FlowSeer capture module. A parallel tcpdump comparison
> exposed missing 802.1Q tags and a VLAN-filter failure; the local source was
> corrected and the physical comparison repeated successfully. EVE-NG's
> virtual RouterOS streamed TZSP to Kali; all 36 decoded frames matched
> tcpdump's TZSP payloads byte for byte. This satisfies the TZSP protocol
> check. Compatibility with a particular physical MikroTik
> and firmware release is a separate question.

## Goal

Validate two capture paths the containerised senders in U2 and the
live-central harness in U3c cannot directly observe: a physical ICX7150
local SPAN into Kali's capture interface, and a RouterOS TZSP stream over
the lab network into Kali's receiver. Each run produces a local pcapng artifact
that `capinfos` reads, and an independent tcpdump capture agrees on the
corresponding Ethernet frames. The physical ICX run proves the local SPAN
path; EVE-NG's virtual RouterOS proves the TZSP protocol path. Neither
establishes compatibility with a particular physical MikroTik firmware
release or a hardware ERSPAN sender.

## Decisions

The parent plan's Decisions apply. What this phase decides when re-planned:

- Which lab devices are in the run and what each one proves. The lab's
  ICX7150-24-POE is the FastIron model without ERSPAN, so it exercises the
  local-interface source through local SPAN. EVE-NG's virtual `LABSW34` runs
  RouterOS 7.17 and exercises the TZSP receiver. Neither produces ERSPAN;
  its interoperability stays proven only against fixtures and containerised
  senders.
- The ICX mirror session and RouterOS sniffer setup are device writes, so
  each needs approval before the live run. The capture path itself is
  read-only from each device's point of view.
- Kali `labtest` is the proposed edge host. On 2026-09-27 its `eth0`
  cable moved to ICX7150 port `1/1/2`, and a live LLDP read confirmed that link,
  while `eth1.1000` remained the management route. The local SPAN run uses
  `eth0` as the capture interface so its destination is separate from the
  management path.
- Ruled: the local capture source applies cBPF after restoring a VLAN tag
  stripped by receive offload, replacing Phase 2's kernel-attached filter.
  Why: the live VLAN 1000 filter accepted zero packets when attached before
  reconstruction, and captured the mirrored traffic after the change. Cost if
  wrong: filtering in the capture process may raise drop rates on a busy SPAN
  port; an offload-aware kernel filter would be needed to recover that margin.
- A successful EVE-NG RouterOS run satisfies
  this phase's TZSP protocol check. Physical CRS317 validation is a separate
  compatibility check if support for its RouterOS 6.49 firmware is claimed.

## Requirements

For the lab portions of the parent's R5 and R6, record source drop counters
and check that each local pcapng is readable by `capinfos`. U3c verifies the
central artifact and streaming counters through its live service and agent
test. For R3, compare the entire decoded Ethernet frame with the
corresponding TZSP payload on the wire.

## Out of scope

- ERSPAN against a shipping ASIC: no lab device emits it, so it stays out of
  reach and the handoff says so.
- Any device configuration beyond the by-hand mirror sessions the run depends
  on (parent Out of scope: configuring SPAN/RSPAN/ERSPAN on a managed device).
- Physical CRS317 TZSP interoperability and a live lab upload to central.
  U3c covers the edge-to-central round trip in its host integration test;
  this phase checks live packet sources and local artifact fidelity.

## Device-specific follow-up

The following lab fact matters only if support for the physical CRS317
running RouterOS 6.49.20 is claimed:

- **The physical TZSP sender is unidentified.** The lab inventory records two MikroTik
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

The engine opens a UDP mirror receiver on `udp_port` from the operator's
`CaptureSessionConfig` (`src/modules/capture/engine.go:90-104`). The virtual
RouterOS run establishes TZSP decoding without resolving that physical
device's management address or sniffer capability.

## Lab progress (2026-09-27)

A temporary ICX7150 SPAN session was set up for this lab run. Kali's `eth0` was linked to
ICX port `1/1/2` by LLDP; its management address and default route stayed on
`eth1.1000` via port `1/1/12`. The switch mirrored both directions of `1/1/12`
to `1/1/2`. A Linux build of the FlowSeer capture module ran on Kali with
`interface_name=eth0`, a 128-byte snap length, and a 100-packet limit. It
reported 100 accepted packets and zero interface or transport drops. The
artifact is `/tmp/flowseer-capture-lab-20260927/icx-span-3.pcapng` on Kali.
`capinfos` recognized it as pcapng with 100 packets and an inferred 128-byte
packet limit. Standard `tcpdump -nn -r` independently decoded nine consecutive
ICMP request/reply pairs between Kali (`172.16.0.21`) and its gateway
(`172.16.0.1`). The ping sent 12 requests successfully; the capture stopped at
its 100-packet limit, so nine pairs in the artifact do not establish loss.
The mirror source and destination were removed after the run, and a read-only
`show mirror` plus running-config check found no mirror configuration.

A second approved mirror run captured both paths in parallel:
`/tmp/flowseer-capture-lab-20260927/icx-span-tcpdump-live.pcap` (tcpdump) and
`/tmp/flowseer-capture-lab-20260927/icx-span-flowseer-parallel.pcapng`
(FlowSeer). Tcpdump recorded 24 ICMP frames (12 request/reply pairs), with
zero kernel drops. FlowSeer recorded 100 total frames at its packet limit,
including the first 22 of those ICMP frames (11 pairs), with zero reported
interface or transport drops. Tcpdump and tshark decoded matching source,
destination, ICMP identifier, type, and sequence for all 22 shared frames.
Byte comparison found each tcpdump frame 102 bytes with an 802.1Q VLAN 1000
tag, and each FlowSeer frame 98 bytes without it. All 22 shared Ethernet
frames became byte-identical after removing that four-byte tag from the
tcpdump copy. The missing final pair is explained by FlowSeer's 100-packet
limit; the ping completed all 12 requests without loss. The mirror was again
removed, and `show mirror` returned empty.

The first comparison exposed two linked defects: Kali's `ethtool -k eth0`
reported `rx-vlan-offload: on`, while FlowSeer's `AF_PACKET`/`Recvfrom` source
had no `PACKET_AUXDATA` handling, and its kernel BPF filter ran before any
tag could be restored. A VLAN 1000 filter captured zero packets during a
four-ping live probe even though tcpdump saw the frames. The local source now
reads the auxiliary VLAN metadata with `Recvmsg`, restores the tag and wire
length, then evaluates its BPF program against the restored Ethernet frame.

The final approved mirror run captured tcpdump and the corrected FlowSeer
source in parallel. Tcpdump recorded 24 ICMP frames (12 request/reply pairs),
zero kernel drops, in
`/tmp/flowseer-capture-lab-20260927/icx-span-tcpdump-final.pcap` on Kali.
FlowSeer used a VLAN 1000 filter and recorded 129 frames, including all 24
ICMP frames, with zero interface or transport drops, in
`/tmp/flowseer-capture-lab-20260927/icx-span-flowseer-final.pcapng`.
`capinfos` recognized the pcapng and inferred its 128-byte packet limit;
tcpdump and tshark decoded all 24 ICMP frames in each artifact. The full
Ethernet bytes of every matching ICMP frame were identical, including the
VLAN 1000 tag. The ping reported 12/12 replies, and `show mirror` returned
empty after the session was removed.

This validates the physical SPAN-to-local-capture path, VLAN fidelity, and
local pcapng output. The full edge-to-central stored-artifact path has not
been run in this lab, and no physical TZSP stream has been captured. Local
filters now run after receive in the capture process so they see restored
headers; the effect on drop rates under a busy SPAN port has not been measured.

A short run followed against EVE-NG's virtual
RouterOS 7.17 `LABSW34` (`172.16.0.34`). Two attempts with
`filter-stream=yes` emitted no packets to Kali, even though ICMP probes
succeeded. With `filter-interface=all`, `filter-ip-protocol=icmp`, and
`filter-stream=no`, RouterOS recorded 36 sniffed packets and streamed 36 TZSP
datagrams. Kali's tcpdump recorded all 36 outer datagrams on `eth1.1000` with
zero kernel drops in
`/tmp/flowseer-capture-lab-20260927/eve-labsw34-tzsp-final-outer.pcap`.
FlowSeer's TZSP receiver accepted 36 packets with zero reported transport
drops and wrote
`/tmp/flowseer-capture-lab-20260927/eve-labsw34-tzsp-final.pcapng`.
`capinfos` recognized both files, and tshark decoded the outer TZSP and inner
ICMP frames. All 36 decapsulated Ethernet frames matched the corresponding
tcpdump TZSP payload byte for byte. The two failed settings also differed in
interface or address filters, so this run does not isolate the effect of
`filter-stream`. The sniffer was stopped and its original disabled settings
restored. This proves the RouterOS-to-FlowSeer TZSP protocol path. It does not
establish compatibility with the physical CRS317 or RouterOS 6.49.20.

## Verification

The completed runs prove local pcapng fidelity and the TZSP arm of R3 on the
lab network. U3c's integration test covers the parent's central stored-artifact
requirement:

- Local SPAN: an ICX7150 mirror session sends a known active port's frames to
  the destination port; the edge captures on the cabled interface; the stored
  local artifact's `capinfos` reports the packet count, link type Ethernet, and the
  session's snap length, and a frame known to be on the mirrored segment
  appears in it.
- TZSP: virtual `LABSW34` streamed sniffed frames to Kali at UDP port 37008;
  `capinfos` recognized both tcpdump's outer pcap and FlowSeer's inner pcapng,
  and all 36 inner frames matched the corresponding TZSP payload bytes.

Run the diff-aware verifier for the plan and changed local source paths.

## Open questions

- If FlowSeer is to claim compatibility with Lab_SW01's RouterOS 6.49.20,
  identify its management address and confirm its sniffer capability in a
  separate device-specific run.
- A live lab edge-to-central upload remains untested. U3c's host integration
  test covers the upload and download path with a live service and agent.
