---
title: Restore Offloaded VLAN Tags Before Applying a Capture Filter
date: 2026-09-27
last_verified: 2026-09-27
category: conventions
module: src/modules/capture/rawsocket
problem_type: bug
component: packet_capture
severity: high
symptoms:
  - "tcpdump sees VLAN-tagged SPAN frames, but an AF_PACKET capture stores the same frames without their 802.1Q tag"
  - "a VLAN ID capture filter accepts zero packets while the mirrored port carries matching traffic"
root_cause: "Linux receive VLAN offload removes the 802.1Q header from packet bytes and reports its tag in PACKET_AUXDATA. A kernel-attached cBPF program evaluates the stripped bytes before an application can restore the header."
resolution_type: "receive PACKET_AUXDATA with Recvmsg, restore the VLAN header and original wire length, then evaluate the capture filter against the restored frame"
applies_when:
  - "capturing VLAN-tagged traffic with Linux AF_PACKET on an interface with receive VLAN offload enabled"
  - "a VLAN ID filter on a SPAN capture sees no packets even though tcpdump sees the tagged traffic"
  - "preserving Ethernet wire bytes and original length when packet-socket auxiliary data carries a VLAN tag"
related_components: [filter, pcapng]
tags: [af-packet, vlan, receive-offload, packet-capture, bpf]
---

# Restore Offloaded VLAN Tags Before Applying a Capture Filter

## The situation

On 2026-09-27, Kali captured ICX7150 SPAN traffic through `eth0`, whose
`rx-vlan-offload` setting was on. Tcpdump recorded 102-byte ICMP Ethernet
frames with an 802.1Q VLAN 1000 tag. FlowSeer's original `AF_PACKET` source
recorded the same frames as 98 bytes without that tag. Its kernel-attached
VLAN 1000 filter accepted no frames during a four-ping probe, although
tcpdump saw them
(`docs/plans/2026-09-18-1423-feat-remote-packet-capture-phase3d-plan.md:133-156`).

The receive path handed the application packet bytes with the VLAN header
stripped and supplied the VLAN information separately. A filter inspecting
Ethernet offsets 12 and 14 before reconstruction therefore cannot match the
missing header.

## How to apply

Enable `PACKET_AUXDATA` on the AF_PACKET socket and use `Recvmsg` to receive
the packet and its control message. When `TP_STATUS_VLAN_VALID` is set,
insert the four-byte VLAN header after the source MAC. Read the TCI and, when
`TP_STATUS_VLAN_TPID_VALID` is set, the TPID from the auxiliary data. Use
`0x8100` when no TPID is supplied. Add four to the reported original length.
For the frame in `TestLinuxLocalSource_RestoresOffloadedVLAN`, this inserts
`81 00 03 e8` between the source MAC and the IPv4 EtherType:

```text
received:  00 01 02 03 04 05 06 07 08 09 0a 0b 08 00 45
auxdata:   TP_STATUS_VLAN_VALID, TCI=1000, TPID absent
restored:  00 01 02 03 04 05 06 07 08 09 0a 0b 81 00 03 e8 08 00 45
```

Run the capture filter against the restored bytes and preserve the adjusted
original length in the emitted frame.

## Evidence

- `src/modules/capture/rawsocket/local_linux.go:125` enables auxiliary data
  with `unix.SetsockoptInt(fd, unix.SOL_PACKET, unix.PACKET_AUXDATA, 1)`.
- `src/modules/capture/rawsocket/local_linux.go:252-260` restores bytes before
  the filter: `data, originalLength, err := restoreVLAN(buf[:captured], n,
  oob[:oobn])`, followed by `accepted, err := s.vm.Run(data)`.
- `src/modules/capture/rawsocket/local_linux_test.go:82-157` checks the
  restored `0x8100` tag and that the VLAN 1000 filter accepts the tagged frame.
- The 2026-09-27 Kali run captured all 24 ICMP frames through that filter,
  and every matching Ethernet frame was byte-identical to tcpdump, including
  VLAN 1000
  (`docs/plans/2026-09-18-1423-feat-remote-packet-capture-phase3d-plan.md:158-169`).

## Limits

Moving the filter after receive means every frame reaches the capture process.
The lab run reported zero interface and transport drops, but it did not
measure drop rates on a busy SPAN port. Keep that performance question
separate from byte fidelity. The packet-socket auxiliary data used here
describes one stripped VLAN header; this run did not validate devices that
strip more than one tag.
