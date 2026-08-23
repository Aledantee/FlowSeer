# Fixture provenance

All fixtures harvested from `harvest.py` (offline scapy craft, no sockets).
The harvest script reproduces the exact byte construction of `l2l3-audit`
for the protocols it owns, and authors new craft for the protocols the
baseline does not carry.

## DTP (dtp.pcap)

- **Source:** `l2l3-audit` `dtp_frame()` (line 2002), reproduced byte-for-byte.
- **Frames:** desirable (mode=0x03), trunk (mode=0x81).
- **Encapsulation:** 802.3/LLC/SNAP (OUI 0x00000C, PID 0x2004). Uses Dot3
  for a valid 802.3 length field; the LLC+SNAP+body bytes are identical to
  the baseline's construction.
- **Determinism:** byte-for-byte (MAC, mode, TLVs all deterministic).

## VTP (vtp.pcap)

- **Source:** `l2l3-audit` `vtp_summary()`, `vtp_subset()`, `vtp_request()`
  (lines 1791-1821), reproduced byte-for-byte.
- **Frames:** summary advertisement (code 0x01, domain "LABDOMAIN", rev 42),
  subset advertisement (code 0x02, VLANs 10/20), advertisement request
  (code 0x03).
- **Encapsulation:** 802.3/LLC/SNAP (OUI 0x00000C, PID 0x2003).
- **Determinism:** byte-for-byte. The timestamp field is set to a fixed
  value ("260823120000") for reproducibility; the real baseline uses
  `time.strftime` (per-run randomness). The MD5 digest is computed over the
  fixed body, so it is also deterministic.

## MVRP (mvrp.pcap)

- **Source:** `l2l3-audit` `mvrp_cmd()` (line 1662), reproduced byte-for-byte.
- **Frames:** JoinIn for VLAN 10, LeaveEmpty for VLAN 20.
- **Encapsulation:** Ethernet EtherType 0x88F5.
- **Determinism:** byte-for-byte.
- **Note:** The baseline sets AttributeLength=4 but writes only 2 bytes of
  first value (VID). The decoder uses the type-specific first-value length
  (2 for VID) rather than the AttributeLength field to locate packed events.
  This is a baseline quirk recorded here, not a spec deviation in netpen.

## LACP (lacp.pcap)

- **Source:** authored from IEEE 802.1AX spec. The baseline does not carry
  an EtherChannel attack (LACP/PAgP are new per R4).
- **Frames:** one LACPDU (subtype 0x01, version 0x01).
- **Encapsulation:** Ethernet EtherType 0x8809 (slow protocols).
- **Determinism:** byte-for-byte. Actor/partner system, key, port, state
  are all fixed constants.
- **PDU layout:** fixed-layout TLVs (actor 20B, partner 20B, collector 16B,
  terminator 2B), padded to 110 bytes per 802.1AX.

## PAgP (pagp.pcap)

- **Source:** authored from Cisco/Wireshark `packet-pagp.c`. The baseline
  does not carry an EtherChannel attack (LACP/PAgP are new per R4).
- **Frames:** one hello (command 0x01).
- **Encapsulation:** 802.3/LLC/SNAP (OUI 0x00000C, PID 0x0104).
- **Determinism:** byte-for-byte. Local and partner device IDs, port IDs,
  and ifindices are all fixed constants.
