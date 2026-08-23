# /// script
# requires-python = ">=3.11"
# dependencies = ["scapy>=2.5"]
# ///
"""Harvest reference L2 fixtures for netpen's owned decoders.

Reproduces the exact frame bytes l2l3-audit crafts for DTP, VTP, and MVRP
(its byte construction is the source of truth per KTD14), and authors LACP
and PAgP from the published wire spec (IEEE 802.1AX / Cisco) since the
baseline has no EtherChannel attack — they are new per R4.

LLC-encapsulated protocols use Dot3 so the 802.3 length field is correct and
gopacket dispatches Ethernet → LLC → SNAP → protocol; the LLC+SNAP+body
bytes are identical to l2l3-audit's construction.

Each protocol writes one pcap into testdata/ and a JSON golden of the typed
values a decoder must surface.  Runs fully offline (scapy craft + wrpcap,
no sockets).
"""

from __future__ import annotations

import hashlib
import ipaddress
import json
import struct
from pathlib import Path

from scapy.all import Dot3, Ether, LLC, SNAP, Raw, wrpcap

TESTDATA = Path(__file__).resolve().parent / "testdata"
SRC_MAC = "00:11:22:33:44:55"

DTP_DST = "01:00:0c:cc:cc:cc"
VTP_DST = "01:00:0c:cc:cc:cc"
MVRP_DST = "01:80:c2:00:00:21"
LACP_DST = "01:80:c2:00:00:02"
PAGP_DST = "01:00:0c:cc:cc:cc"

CISCO_OUI = 0x00000C


# --------------------------------------------------------------------------- dtp


def dtp_tlv(t: int, v: bytes) -> bytes:
    return struct.pack("!HH", t, 4 + len(v)) + v


def dtp_frame(src: str, mode: int = 0x03) -> bytes:
    neigh = bytes(int(x, 16) for x in src.split(":"))
    body = (
        b"\x01"
        + dtp_tlv(0x0001, b"\x00")
        + dtp_tlv(0x0002, bytes([mode]))
        + dtp_tlv(0x0003, b"\xa5")
        + dtp_tlv(0x0004, neigh)
    )
    return (
        Dot3(dst=DTP_DST, src=src)
        / LLC(dsap=0xAA, ssap=0xAA, ctrl=3)
        / SNAP(OUI=CISCO_OUI, code=0x2004)
        / Raw(body)
    )


def harvest_dtp() -> None:
    desirable = dtp_frame(SRC_MAC, mode=0x03)
    trunk = dtp_frame(SRC_MAC, mode=0x81)
    wrpcap(str(TESTDATA / "dtp.pcap"), [desirable, trunk])
    golden = {
        "protocol": "DTP",
        "dst": DTP_DST,
        "src": SRC_MAC,
        "snap_pid": "0x2004",
        "frames": [
            {
                "name": "desirable",
                "version": 1,
                "tlvs": [
                    {"type": 1, "value_hex": "00"},
                    {"type": 2, "value_hex": "03"},
                    {"type": 3, "value_hex": "a5"},
                    {"type": 4, "value_hex": "001122334455"},
                ],
                "trunk_status": 3,
                "trunk_type": 165,
                "neighbor": SRC_MAC,
            },
            {
                "name": "trunk",
                "version": 1,
                "tlvs": [
                    {"type": 1, "value_hex": "00"},
                    {"type": 2, "value_hex": "81"},
                    {"type": 3, "value_hex": "a5"},
                    {"type": 4, "value_hex": "001122334455"},
                ],
                "trunk_status": 129,
                "trunk_type": 165,
                "neighbor": SRC_MAC,
            },
        ],
    }
    (TESTDATA / "dtp.json").write_text(json.dumps(golden, indent=2))


# --------------------------------------------------------------------------- vtp


def _vtp_envelope(src: str, body: bytes) -> bytes:
    return (
        Dot3(dst=VTP_DST, src=src)
        / LLC(dsap=0xAA, ssap=0xAA, ctrl=3)
        / SNAP(OUI=CISCO_OUI, code=0x2003)
        / Raw(body)
    )


def _vtp_vlan_records(vlans: list[tuple[int, str]]) -> bytes:
    out = b""
    for vid, name in vlans:
        nb = name.encode()
        npad = (len(nb) + 3) & ~3
        out += (
            struct.pack("!BBBBHHI", 12 + npad, 0, 1, len(nb), vid, 1500, 0x100000 + vid)
            + nb.ljust(npad, b"\x00")
        )
    return out


def vtp_summary(
    src: str,
    domain: str,
    revision: int,
    updater: str = "0.0.0.0",
    version: int = 1,
    followers: int = 0,
) -> bytes:
    dom = domain.encode()
    db = dom[:32].ljust(32, b"\x00")
    ts = b"260823120000"  # deterministic timestamp for fixture reproducibility
    body = (
        struct.pack("!BBBB", version, 0x01, followers, len(dom))
        + db
        + struct.pack("!II", revision, int(ipaddress.IPv4Address(updater)))
        + ts
        + b"\x00" * 16
    )
    digest = hashlib.md5(b"\x00" * 16 + body + b"\x00" * 16).digest()
    body = body[:56] + digest
    return _vtp_envelope(src, body)


def vtp_subset(
    src: str,
    domain: str,
    revision: int,
    vlans: list[tuple[int, str]],
    version: int = 1,
    seq: int = 1,
) -> bytes:
    dom = domain.encode()
    body = (
        struct.pack("!BBBB", version, 0x02, seq, len(dom))
        + dom[:32].ljust(32, b"\x00")
        + struct.pack("!I", revision)
        + _vtp_vlan_records(vlans)
    )
    return _vtp_envelope(src, body)


def vtp_request(src: str, domain: str, start_val: int = 0, version: int = 1) -> bytes:
    dom = domain.encode()
    body = (
        struct.pack("!BBBB", version, 0x03, 0, len(dom))
        + dom[:32].ljust(32, b"\x00")
        + struct.pack("!H", start_val)
    )
    return _vtp_envelope(src, body)


def harvest_vtp() -> None:
    summary = vtp_summary(SRC_MAC, "LABDOMAIN", revision=42)
    subset = vtp_subset(
        SRC_MAC, "LABDOMAIN", revision=42, vlans=[(10, "data"), (20, "voice")]
    )
    request = vtp_request(SRC_MAC, "LABDOMAIN")
    wrpcap(str(TESTDATA / "vtp.pcap"), [summary, subset, request])
    golden = {
        "protocol": "VTP",
        "dst": VTP_DST,
        "src": SRC_MAC,
        "snap_pid": "0x2003",
        "domain": "LABDOMAIN",
        "revision": 42,
        "frames": [
            {
                "name": "summary",
                "version": 1,
                "code": 1,
                "domain": "LABDOMAIN",
                "revision": 42,
                "updater": "0.0.0.0",
            },
            {
                "name": "subset",
                "version": 1,
                "code": 2,
                "seq": 1,
                "domain": "LABDOMAIN",
                "revision": 42,
                "vlans": [{"id": 10, "name": "data"}, {"id": 20, "name": "voice"}],
            },
            {
                "name": "request",
                "version": 1,
                "code": 3,
                "domain": "LABDOMAIN",
                "start_val": 0,
            },
        ],
    }
    (TESTDATA / "vtp.json").write_text(json.dumps(golden, indent=2))


# --------------------------------------------------------------------------- mvrp


def mvrp_frame(src: str, vid: int, join: bool = True) -> bytes:
    event = 2 if join else 3  # JoinIn vs LeaveEmpty
    vh = (0 << 13) | 1
    msg = (
        struct.pack("!BB", 1, 4)  # AttributeType=1(VID), AttributeLength=4
        + struct.pack("!H", vh)  # VectorHeader: LeaveAll=0, NumberOfValues=1
        + struct.pack("!H", vid)  # FirstValue (VID)
        + bytes([event << 6])  # ThreePackedEvents (event in top 2 bits)
        + b"\x00\x00"  # remaining packed event slots + padding
    )
    pdu = bytes([0]) + msg  # ProtocolVersion = 0
    return Ether(dst=MVRP_DST, src=src, type=0x88F5) / Raw(pdu)


def harvest_mvrp() -> None:
    join10 = mvrp_frame(SRC_MAC, 10, join=True)
    leave20 = mvrp_frame(SRC_MAC, 20, join=False)
    wrpcap(str(TESTDATA / "mvrp.pcap"), [join10, leave20])
    golden = {
        "protocol": "MVRP",
        "dst": MVRP_DST,
        "src": SRC_MAC,
        "ethertype": "0x88F5",
        "frames": [
            {
                "name": "join_vid10",
                "version": 0,
                "messages": [
                    {
                        "attr_type": 1,
                        "attr_len": 4,
                        "leave_all": False,
                        "first_value": 10,
                        "event": "JoinIn",
                    },
                ],
            },
            {
                "name": "leave_vid20",
                "version": 0,
                "messages": [
                    {
                        "attr_type": 1,
                        "attr_len": 4,
                        "leave_all": False,
                        "first_value": 20,
                        "event": "LeaveEmpty",
                    },
                ],
            },
        ],
    }
    (TESTDATA / "mvrp.json").write_text(json.dumps(golden, indent=2))


# --------------------------------------------------------------------------- lacp

LACP_SUBTYPE = 0x01
LACP_ACTOR_SYS_PRI = 0x8000
LACP_ACTOR_SYS = "00:11:22:33:44:55"
LACP_ACTOR_KEY = 0x0001
LACP_ACTOR_PORT = 0x0001
LACP_PARTNER_SYS_PRI = 0x8000
LACP_PARTNER_SYS = "00:aa:bb:cc:dd:ee"
LACP_PARTNER_KEY = 0x0002
LACP_PARTNER_PORT = 0x0002


def lacp_frame(src: str) -> bytes:
    actor_sys = bytes(int(x, 16) for x in LACP_ACTOR_SYS.split(":"))
    partner_sys = bytes(int(x, 16) for x in LACP_PARTNER_SYS.split(":"))
    # LACPDU per IEEE 802.1AX: fixed-layout PDU (not TLV-based in the
    # traditional sense; each block is a typed TLV but with fixed offsets).
    # Subtype(1) Version(1)
    # Actor TLV: t=0x01 len=20 sys_pri(2) sys(6) key(2) port_pri(2) port(2) state(1) reserved(3)
    # Partner TLV: t=0x02 len=20 sys_pri(2) sys(6) key(2) port_pri(2) port(2) state(1) reserved(3)
    # Collector TLV: t=0x03 len=16 max_delay(2) reserved(12)
    # Terminator TLV: t=0x00 len=0
    # Reserved padding to 110 bytes
    pdu = bytes([LACP_SUBTYPE, 0x01])

    # Actor TLV
    pdu += bytes([0x01, 20])
    pdu += struct.pack("!H", LACP_ACTOR_SYS_PRI)
    pdu += actor_sys
    pdu += struct.pack("!H", LACP_ACTOR_KEY)
    pdu += struct.pack("!H", LACP_ACTOR_SYS_PRI)  # port priority
    pdu += struct.pack("!H", LACP_ACTOR_PORT)
    pdu += bytes([0x3C])  # state: activity+aggregation+sync+collecting
    pdu += b"\x00\x00\x00"

    # Partner TLV
    pdu += bytes([0x02, 20])
    pdu += struct.pack("!H", LACP_PARTNER_SYS_PRI)
    pdu += partner_sys
    pdu += struct.pack("!H", LACP_PARTNER_KEY)
    pdu += struct.pack("!H", LACP_PARTNER_SYS_PRI)
    pdu += struct.pack("!H", LACP_PARTNER_PORT)
    pdu += bytes([0x00])
    pdu += b"\x00\x00\x00"

    # Collector TLV
    pdu += bytes([0x03, 16])
    pdu += struct.pack("!H", 0x8000)  # collector max delay
    pdu += b"\x00" * 12

    # Terminator TLV
    pdu += bytes([0x00, 0x00])

    # Reserved padding to 110 bytes
    pdu += b"\x00" * (110 - len(pdu))

    return Ether(dst=LACP_DST, src=src, type=0x8809) / Raw(pdu)


def harvest_lacp() -> None:
    frame = lacp_frame(SRC_MAC)
    wrpcap(str(TESTDATA / "lacp.pcap"), [frame])
    golden = {
        "protocol": "LACP",
        "dst": LACP_DST,
        "src": SRC_MAC,
        "ethertype": "0x8809",
        "subtype": 1,
        "version": 1,
        "frames": [
            {
                "name": "lacpdu",
                "subtype": 1,
                "version": 1,
                "actor": {
                    "system_priority": LACP_ACTOR_SYS_PRI,
                    "system": LACP_ACTOR_SYS,
                    "key": LACP_ACTOR_KEY,
                    "port_priority": LACP_ACTOR_SYS_PRI,
                    "port": LACP_ACTOR_PORT,
                    "state": 60,
                },
                "partner": {
                    "system_priority": LACP_PARTNER_SYS_PRI,
                    "system": LACP_PARTNER_SYS,
                    "key": LACP_PARTNER_KEY,
                    "port_priority": LACP_PARTNER_SYS_PRI,
                    "port": LACP_PARTNER_PORT,
                    "state": 0,
                },
                "collector_max_delay": 32768,
            },
        ],
    }
    (TESTDATA / "lacp.json").write_text(json.dumps(golden, indent=2))


# --------------------------------------------------------------------------- pagp

PAGP_VERSION = 0x01


def pagp_frame(src: str) -> bytes:
    # PAgP: 802.3/SNAP OUI 0x00000C, PID 0x0104.
    # Layout per Cisco/Wireshark packet-pagp.c:
    # Version(1) Command(1) GroupCapability(1) GroupNumber(1)
    # LocalDeviceID(6) LocalPortID(4) LocalIfindex(4)
    # LocalGroupCapability(1) Reserved(1)
    # PartnerDeviceID(6) PartnerPortID(4) PartnerIfindex(4)
    # PartnerGroupCapability(1) Reserved(1)
    # Count(1) Reserved(1) then Count * LearnTime(2) entries (0 here)
    src_bytes = bytes(int(x, 16) for x in src.split(":"))
    partner = bytes(int(x, 16) for x in "00:aa:bb:cc:dd:ee".split(":"))
    body = struct.pack(
        "!BBBB", PAGP_VERSION, 0x01, 0x01, 0x01
    )  # ver, cmd=hello, cap, group
    body += src_bytes  # local device ID (6)
    body += struct.pack("!I", 0x00000001)  # local port ID
    body += struct.pack("!I", 0x00000001)  # local ifindex
    body += bytes([0x01, 0x00])  # local group cap + reserved
    body += partner  # partner device ID (6)
    body += struct.pack("!I", 0x00000002)  # partner port ID
    body += struct.pack("!I", 0x00000002)  # partner ifindex
    body += bytes([0x01, 0x00])  # partner group cap + reserved
    body += bytes([0x00, 0x00])  # count + reserved (no learn time entries)
    return (
        Dot3(dst=PAGP_DST, src=src)
        / LLC(dsap=0xAA, ssap=0xAA, ctrl=3)
        / SNAP(OUI=CISCO_OUI, code=0x0104)
        / Raw(body)
    )


def harvest_pagp() -> None:
    frame = pagp_frame(SRC_MAC)
    wrpcap(str(TESTDATA / "pagp.pcap"), [frame])
    golden = {
        "protocol": "PAgP",
        "dst": PAGP_DST,
        "src": SRC_MAC,
        "snap_pid": "0x0104",
        "frames": [
            {
                "name": "hello",
                "version": 1,
                "command": 1,
                "group_capability": 1,
                "group_number": 1,
                "local_device_id": SRC_MAC,
                "local_port_id": 1,
                "partner_device_id": "00:aa:bb:cc:dd:ee",
                "partner_port_id": 2,
            },
        ],
    }
    (TESTDATA / "pagp.json").write_text(json.dumps(golden, indent=2))


# --------------------------------------------------------------------------- main


def main() -> None:
    TESTDATA.mkdir(parents=True, exist_ok=True)
    harvest_dtp()
    harvest_vtp()
    harvest_mvrp()
    harvest_lacp()
    harvest_pagp()
    print(f"fixtures written to {TESTDATA}")


if __name__ == "__main__":
    main()
