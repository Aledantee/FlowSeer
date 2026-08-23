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

## HSRP (hsrp.pcap)

- **Source:** `l2l3-audit` `hsrp_cmd()` (line 1950), reproduced byte-for-byte.
  The baseline crafts HSRP coup/hello/resign via scapy's `HSRP` layer with
  `auth=b"cisco\x00\x00\x00"`, `priority=255`, `state=16`.
- **Frames:** hello (opcode=0, state=16), coup (opcode=1, state=16), resign
  (opcode=2, state=8).
- **Encapsulation:** Ethernet → IPv4 → UDP 1985 → HSRP. Multicast
  224.0.0.2, source IP 10.0.0.2.
- **Determinism:** byte-for-byte. All fields (group, VIP, priority,
  hellotime, holdtime, auth) are fixed constants.

## GLBP (glbp.pcap)

- **Source:** authored from RFC 7868. The baseline does not carry a GLBP
  attack (GLBP hijack is an R4 superset attack).
- **Frames:** one hello (opcode 1).
- **Encapsulation:** Ethernet → IPv4 → UDP 3222 → GLBP. Multicast
  224.0.0.102, source IP 10.0.0.2.
- **Determinism:** byte-for-byte. Fixed 23-byte header (version, reserved,
  opcode, group, hello/hold time, virtual MAC, priority, state, address
  family, auth, reserved) plus one Timer TLV (type=1, length=8, hello/hold
  values).

## EIGRP (eigrp.pcap, eigrp_no_tlv.pcap)

- **Source:** authored from RFC 7868 §4.2. The baseline does not carry an
  EIGRP attack (EIGRP injection is an R4 superset attack).
- **Frames:** one hello (opcode 5) with an IP-internal-route TLV
  (type 0x0102). A separate `eigrp_no_tlv.pcap` contains a header-only
  frame (no TLV tail) for the missing-TLV-tail decline test.
- **Encapsulation:** Ethernet → IPv4 (proto 88) → EIGRP. Multicast
  224.0.0.10, source IP 10.0.0.2.
- **Determinism:** byte-for-byte. The 20-byte fixed header (version,
  opcode, checksum, flags, seq, ack, VRID, AS) and TLV content are all
  fixed constants. The checksum field is set to 0 (not validated by the
  decoder).

## LLMNR (llmnr.pcap, llmnr_loop.pcap)

- **Source:** `l2l3-audit` `make_poisoner()` (line 2791), reproduced
  byte-for-byte for the query/response shapes. The baseline crafts LLMNR
  via DNS wire format on UDP 5355.
- **Frames:** query (id 0x1234, "host.lab", type A), response (id 0x1234,
  "host.lab", type A, TTL 30, rdata 10.0.0.1, with compression pointer
  0xC00C to the question name).
- **Adversarial:** `llmnr_loop.pcap` contains a crafted compression-pointer
  loop (pointer 0xC00C at offset 12 pointing to offset 12 — itself). This
  is the hard adversarial case for name decompression; the decoder detects
  the cycle via a visited-offset set and reports a named structured error.
- **Encapsulation:** Ethernet → IPv4 → UDP 5355 → LLMNR (DNS wire format).
  Multicast 224.0.0.252, source IP 10.0.0.2.
- **Determinism:** byte-for-byte for queries. Responses use compression
  pointers; the serializer writes literal names (no compression), so the
  round-trip is field-level, not byte-for-byte, for response frames.

## NBT-NS (nbns.pcap, nbns_loop.pcap)

- **Source:** `l2l3-audit` `make_poisoner()` (line 2791), reproduced
  byte-for-byte for the query/response shapes. The baseline crafts NBT-NS
  via `NBNSHeader`/`NBNSQueryRequest`/`NBNSQueryResponse` on UDP 137.
- **Frames:** query (id 0x5678, "WORKSTATION", type NB 0x0020), response
  (id 0x5678, "WORKSTATION", type NB, TTL 300, rdata 10.0.0.1). Names use
  NetBIOS half-ASCII encoding (16-byte padded name → 32 encoded bytes).
- **Adversarial:** `nbns_loop.pcap` contains a crafted compression-pointer
  loop (pointer 0xC00C at offset 12 pointing to offset 12 — itself). The
  decoder detects the cycle and reports a named structured error.
- **Encapsulation:** Ethernet → IPv4 → UDP 137 → NBT-NS (DNS-family wire
  format with NetBIOS name encoding). Broadcast (ff:ff:ff:ff:ff:ff),
  source IP 10.0.0.2.
- **Determinism:** byte-for-byte. All fields are fixed constants.
