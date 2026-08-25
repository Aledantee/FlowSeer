# /// script
# requires-python = ">=3.11"
# dependencies = ["scapy>=2.5"]
# ///
"""Harvest reference L2/L3 fixtures for netpen's owned decoders.

Reproduces the exact frame bytes l2l3-audit crafts for DTP, VTP, MVRP, HSRP,
 LLMNR, and NBT-NS (its byte construction is the source of truth per KTD14),
and authors LACP, PAgP, GLBP, and EIGRP from the published wire spec
(IEEE 802.1AX / Cisco / RFC 7868 / RFC 7868) since the baseline has no
EtherChannel or GLBP/EIGRP attack — they are new per R4.

LLC-encapsulated protocols use Dot3 so the 802.3 length field is correct and
gopacket dispatches Ethernet → LLC → SNAP → protocol; the LLC+SNAP+body
bytes are identical to l2l3-audit's construction.  L3 protocols (HSRP, GLBP,
EIGRP, LLMNR, NBT-NS) ride Ethernet → IPv4 → (UDP) → protocol.

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

from scapy.all import Dot3, Ether, IP, LLC, SNAP, Raw, UDP, wrpcap

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


# --------------------------------------------------------------------------- hsrp

HSRP_DST = "224.0.0.2"  # HSRP multicast
HSRP_GROUP = 1
HSRP_VIP = "10.0.0.1"
HSRP_PRIORITY = 255
HSRP_HELLOTIME = 3
HSRP_HOLDTIME = 10
HSRP_AUTH = b"cisco\x00\x00\x00"


def hsrp_frame(
    src: str,
    opcode: int = 0,
    state: int = 16,
    group: int = HSRP_GROUP,
    priority: int = HSRP_PRIORITY,
    vip: str = HSRP_VIP,
    hellotime: int = HSRP_HELLOTIME,
    holdtime: int = HSRP_HOLDTIME,
    auth: bytes = HSRP_AUTH,
) -> bytes:
    # HSRPv1 per RFC 2281: UDP 1985, multicast 224.0.0.2.
    # Version(1) Op(1) State(1) Hellotime(1) Holdtime(1)
    # Priority(1) Group(1) Reserved(1) Auth(8) VirtualIP(4)
    vip_bytes = bytes(int(x) for x in vip.split("."))
    body = struct.pack(
        "!BBBBBBBB",
        0,  # version
        opcode,
        state,
        hellotime,
        holdtime,
        priority,
        group,
        0,  # reserved
    )
    body += auth.ljust(8, b"\x00")
    body += vip_bytes
    return (
        Ether(dst="01:00:5e:00:00:02", src=src)
        / IP(src="10.0.0.2", dst=HSRP_DST)
        / UDP(sport=1985, dport=1985)
        / Raw(body)
    )


def harvest_hsrp() -> None:
    hello = hsrp_frame(SRC_MAC, opcode=0, state=16)
    coup = hsrp_frame(SRC_MAC, opcode=1, state=16)
    resign = hsrp_frame(SRC_MAC, opcode=2, state=8)
    wrpcap(str(TESTDATA / "hsrp.pcap"), [hello, coup, resign])
    golden = {
        "protocol": "HSRP",
        "dst": HSRP_DST,
        "src": SRC_MAC,
        "udp_port": 1985,
        "group": HSRP_GROUP,
        "vip": HSRP_VIP,
        "frames": [
            {
                "name": "hello",
                "version": 0,
                "opcode": 0,
                "state": 16,
                "hellotime": HSRP_HELLOTIME,
                "holdtime": HSRP_HOLDTIME,
                "priority": HSRP_PRIORITY,
                "group": HSRP_GROUP,
                "auth": "cisco",
                "vip": HSRP_VIP,
            },
            {
                "name": "coup",
                "version": 0,
                "opcode": 1,
                "state": 16,
                "hellotime": HSRP_HELLOTIME,
                "holdtime": HSRP_HOLDTIME,
                "priority": HSRP_PRIORITY,
                "group": HSRP_GROUP,
                "auth": "cisco",
                "vip": HSRP_VIP,
            },
            {
                "name": "resign",
                "version": 0,
                "opcode": 2,
                "state": 8,
                "hellotime": HSRP_HELLOTIME,
                "holdtime": HSRP_HOLDTIME,
                "priority": HSRP_PRIORITY,
                "group": HSRP_GROUP,
                "auth": "cisco",
                "vip": HSRP_VIP,
            },
        ],
    }
    (TESTDATA / "hsrp.json").write_text(json.dumps(golden, indent=2))


# --------------------------------------------------------------------------- glbp

GLBP_DST = "224.0.0.102"  # GLBP multicast
GLBP_GROUP = 1
GLBP_PRIORITY = 100


def glbp_hello_frame(src: str, group: int = GLBP_GROUP) -> bytes:
    # GLBP per RFC 7868. Hello packet (opcode 1).
    # Fixed header (23 bytes):
    #   Version(1) Reserved(1) Opcode(1) Group(2)
    #   HelloTime(2) HoldTime(2) VirtualMAC(6)
    #   Priority(1) State(1) AddressFamily(1) Unknown(1)
    #   AuthData(2) Reserved(2)
    # Then variable-length TLVs (type 2 bytes, length 2 bytes, value).
    vmac = b"\x00\x07\xb4\x00\x01\x01"  # GLBP virtual MAC OUI 00-07-b4
    header = struct.pack(
        "!BBBHHH",
        1,  # version
        0,  # reserved
        1,  # opcode = hello
        group,
        3000,  # hello time (ms)
        10000,  # hold time (ms)
    )
    header += vmac
    header += struct.pack(
        "!BBBBHH",
        GLBP_PRIORITY,  # priority
        1,  # state = active
        1,  # address family = IPv4
        0,  # unknown
        0,  # auth data (2 bytes)
        0,  # reserved
    )
    # Timer TLV (type 1): helloTime(2) holdTime(2)
    timer_tlv = struct.pack("!HH", 1, 8) + struct.pack("!HH", 3000, 10000)
    return (
        Ether(dst="01:00:5e:00:00:66", src=src)
        / IP(src="10.0.0.2", dst=GLBP_DST)
        / UDP(sport=3222, dport=3222)
        / Raw(header + timer_tlv)
    )


def harvest_glbp() -> None:
    hello = glbp_hello_frame(SRC_MAC)
    wrpcap(str(TESTDATA / "glbp.pcap"), [hello])
    golden = {
        "protocol": "GLBP",
        "dst": GLBP_DST,
        "src": SRC_MAC,
        "udp_port": 3222,
        "group": GLBP_GROUP,
        "frames": [
            {
                "name": "hello",
                "version": 1,
                "opcode": 1,
                "group": GLBP_GROUP,
                "hello_time": 3000,
                "hold_time": 10000,
                "priority": GLBP_PRIORITY,
                "state": 1,
                "virtual_mac": "00:07:b4:00:01:01",
            },
        ],
    }
    (TESTDATA / "glbp.json").write_text(json.dumps(golden, indent=2))


# --------------------------------------------------------------------------- eigrp

EIGRP_DST = "224.0.0.10"  # EIGRP multicast (RTP)
EIGRP_AS = 1


def eigrp_frame(
    src: str,
    opcode: int = 5,
    as_num: int = EIGRP_AS,
    seq: int = 0,
    ack: int = 0,
    with_tlv: bool = True,
) -> bytes:
    # EIGRP header per RFC 7868 section 4.2:
    # Version(1)=2 Opcode(1) Checksum(2) Flags(4)
    # Seq(4) Ack(4) VRID(2) AutonomousSystem(2)
    # Then variable-length TLVs (type 2 bytes, length 2 bytes, value).
    # For the fixture we craft an IP-internal-route TLV.
    header = struct.pack(
        "!BBHI",
        2,  # version
        opcode,
        0,  # checksum (placeholder; not validated by the decoder)
        0,  # flags
    )
    header += struct.pack("!II", seq, ack)
    header += struct.pack("!HH", 0, as_num)  # VRID=0, AS

    if with_tlv:
        # TLV: Type(2) Length(2) Value
        # Type 0x0102 = IP internal routes (RFC 7868 §4.3)
        # Value: a minimal IP-internal-route entry.
        tlv_type = 0x0102
        # IP internal route: nexthop(4) delay(4) bandwidth(4) mtu(4) hopcount(1)
        #   reliability(1) load(1) reserved(1) prefix_len(1) destination(4)
        route = (
            bytes(int(x) for x in "10.0.0.1".split("."))  # nexthop
            + struct.pack("!I", 1000)  # delay
            + struct.pack("!I", 100000)  # bandwidth
            + struct.pack("!I", 1500)  # mtu
            + bytes([1, 255, 1, 0])  # hopcount, reliability, load, reserved
            + bytes([24])  # prefix length
            + bytes(int(x) for x in "10.0.0.0".split("."))  # destination
        )
        tlv = struct.pack("!HH", tlv_type, 4 + len(route)) + route
        body = header + tlv
    else:
        # Header only, no TLV tail — for the missing-TLV-tail decline test.
        body = header

    return (
        Ether(dst="01:00:5e:00:00:0a", src=src)
        / IP(src="10.0.0.2", dst=EIGRP_DST, proto=88)
        / Raw(body)
    )


def harvest_eigrp() -> None:
    hello = eigrp_frame(SRC_MAC, opcode=5, with_tlv=True)
    # Header-only frame: no TLV tail — tests the decline-without-error path.
    header_only = eigrp_frame(SRC_MAC, opcode=5, with_tlv=False)
    wrpcap(str(TESTDATA / "eigrp.pcap"), [hello])
    wrpcap(str(TESTDATA / "eigrp_no_tlv.pcap"), [header_only])
    golden = {
        "protocol": "EIGRP",
        "dst": EIGRP_DST,
        "src": SRC_MAC,
        "ip_protocol": 88,
        "as": EIGRP_AS,
        "frames": [
            {
                "name": "hello",
                "version": 2,
                "opcode": 5,
                "flags": 0,
                "seq": 0,
                "ack": 0,
                "vrid": 0,
                "as": EIGRP_AS,
                "tlvs": [
                    {"type": 258, "length": 29},
                ],
            },
        ],
    }
    (TESTDATA / "eigrp.json").write_text(json.dumps(golden, indent=2))


# --------------------------------------------------------------------------- llmnr

LLMNR_DST = "224.0.0.252"  # LLMNR multicast IPv4


def llmnr_query_frame(src: str, qname: str = "host.lab") -> bytes:
    # LLMNR per RFC 4795: UDP 5355, multicast 224.0.0.252 (IPv4).
    # Uses standard DNS wire format (RFC 1035).
    # DNS header: ID(2) Flags(2) QDCOUNT(2) ANCOUNT(2) NSCOUNT(2) ARCOUNT(2)
    dns_id = 0x1234
    flags = 0x0000  # standard query
    header = struct.pack("!HHHHHH", dns_id, flags, 1, 0, 0, 0)
    # Question: encoded name + type(2) + class(2)
    qname_bytes = b""
    for label in qname.split("."):
        qname_bytes += bytes([len(label)]) + label.encode()
    qname_bytes += b"\x00"  # root label
    question = qname_bytes + struct.pack("!HH", 1, 1)  # type A, class IN
    body = header + question
    return (
        Ether(dst="01:00:5e:00:00:fc", src=src)
        / IP(src="10.0.0.2", dst=LLMNR_DST)
        / UDP(sport=5355, dport=5355)
        / Raw(body)
    )


def llmnr_response_frame(src: str, qname: str = "host.lab") -> bytes:
    # LLMNR response with name compression pointer.
    dns_id = 0x1234
    flags = 0x8000  # response
    header = struct.pack("!HHHHHH", dns_id, flags, 1, 1, 0, 0)
    # Question with encoded name
    qname_bytes = b""
    for label in qname.split("."):
        qname_bytes += bytes([len(label)]) + label.encode()
    qname_bytes += b"\x00"
    question = qname_bytes + struct.pack("!HH", 1, 1)
    # Answer: compression pointer to offset 12 (the question name) + type A + class IN + TTL + rdlength + rdata
    answer = struct.pack("!H", 0xC00C)  # compression pointer to offset 12
    answer += struct.pack("!HHIH", 1, 1, 30, 4)  # type A, class IN, TTL 30, rdlen 4
    answer += bytes(int(x) for x in "10.0.0.1".split("."))  # rdata
    body = header + question + answer
    return (
        Ether(dst="01:00:5e:00:00:fc", src=src)
        / IP(src="10.0.0.1", dst="10.0.0.2")
        / UDP(sport=5355, dport=5355)
        / Raw(body)
    )


def llmnr_compression_loop_frame(src: str) -> bytes:
    # Adversarial fixture: compression pointer that points to itself
    # (offset of the pointer byte). This is the hard adversarial case for
    # name decompression — a naive decoder loops forever.
    # DNS header (12 bytes), then a name at offset 12 that is a compression
    # pointer pointing back to offset 12 (itself).
    dns_id = 0xDEAD
    flags = 0x0000
    header = struct.pack("!HHHHHH", dns_id, flags, 1, 0, 0, 0)
    # Name: compression pointer 0xC00C pointing to offset 12 (itself)
    question = struct.pack("!H", 0xC00C) + struct.pack("!HH", 1, 1)
    body = header + question
    return (
        Ether(dst="01:00:5e:00:00:fc", src=src)
        / IP(src="10.0.0.2", dst=LLMNR_DST)
        / UDP(sport=5355, dport=5355)
        / Raw(body)
    )


def harvest_llmnr() -> None:
    query = llmnr_query_frame(SRC_MAC, "host.lab")
    response = llmnr_response_frame(SRC_MAC, "host.lab")
    loop = llmnr_compression_loop_frame(SRC_MAC)
    wrpcap(str(TESTDATA / "llmnr.pcap"), [query, response])
    wrpcap(str(TESTDATA / "llmnr_loop.pcap"), [loop])
    golden = {
        "protocol": "LLMNR",
        "dst": LLMNR_DST,
        "src": SRC_MAC,
        "udp_port": 5355,
        "frames": [
            {
                "name": "query",
                "id": 4660,
                "qr": 0,
                "qdcount": 1,
                "questions": [{"name": "host.lab", "qtype": 1, "qclass": 1}],
            },
            {
                "name": "response",
                "id": 4660,
                "qr": 1,
                "qdcount": 1,
                "ancount": 1,
                "questions": [{"name": "host.lab", "qtype": 1, "qclass": 1}],
                "answers": [
                    {"name": "host.lab", "type": 1, "class": 1, "ttl": 30, "rdata": "10.0.0.1"},
                ],
            },
        ],
    }
    (TESTDATA / "llmnr.json").write_text(json.dumps(golden, indent=2))


# --------------------------------------------------------------------------- nbns

NBNS_DST = "224.0.0.2"  # NBNS broadcast (actually uses directed broadcast/subnet broadcast in practice)


def nbns_query_frame(src: str, qname: str = "WORKSTATION") -> bytes:
    # NBT-NS per RFC 1002: UDP 137.
    # NetBIOS name service uses DNS-like wire format but names are
    # encoded as NetBIOS scope IDs (half-ASCII, padded to 16 bytes).
    # Header: NAME_TRN_ID(2) Flags(2) QDCOUNT(2) ANCOUNT(2) NSCOUNT(2) ARCOUNT(2)
    # Question: encoded name + type(2) + class(2)
    trn_id = 0x5678
    flags = 0x0010  # broadcast, recursion desired
    header = struct.pack("!HHHHHH", trn_id, flags, 1, 0, 0, 0)

    # NetBIOS name encoding: each byte of the 16-char padded name is split
    # into two half-bytes: 'A' + high nibble, 'A' + low nibble.
    name_padded = qname.encode().ljust(15, b"\x20") + b"\x00"  # 16 bytes
    encoded = b""
    for b in name_padded:
        encoded += bytes([0x41 + (b >> 4), 0x41 + (b & 0x0F)])
    # Scope: root label only
    qname_bytes = bytes([len(encoded)]) + encoded + b"\x00"
    question = qname_bytes + struct.pack("!HH", 0x0020, 0x0001)  # type NB, class IN

    body = header + question
    return (
        Ether(dst="ff:ff:ff:ff:ff:ff", src=src)
        / IP(src="10.0.0.2", dst="10.0.0.255")
        / UDP(sport=137, dport=137)
        / Raw(body)
    )


def nbns_response_frame(src: str, qname: str = "WORKSTATION") -> bytes:
    trn_id = 0x5678
    flags = 0x8500  # response, authoritative, broadcast
    header = struct.pack("!HHHHHH", trn_id, flags, 0, 1, 0, 0)

    name_padded = qname.encode().ljust(15, b"\x20") + b"\x00"
    encoded = b""
    for b in name_padded:
        encoded += bytes([0x41 + (b >> 4), 0x41 + (b & 0x0F)])
    qname_bytes = bytes([len(encoded)]) + encoded + b"\x00"

    # Answer: name + type NB + class IN + TTL + rdlength + address entry
    answer = qname_bytes + struct.pack("!HHIH", 0x0020, 0x0001, 300, 6)
    answer += struct.pack("!HI", 0x0000, int(ipaddress.IPv4Address("10.0.0.1")))

    body = header + answer
    return (
        Ether(dst="ff:ff:ff:ff:ff:ff", src=src)
        / IP(src="10.0.0.1", dst="10.0.0.2")
        / UDP(sport=137, dport=137)
        / Raw(body)
    )


def nbns_compression_loop_frame(src: str) -> bytes:
    # Adversarial: compression pointer loop in the question name.
    trn_id = 0xBEEF
    flags = 0x0010
    header = struct.pack("!HHHHHH", trn_id, flags, 1, 0, 0, 0)
    # Name: compression pointer 0xC00C pointing to offset 12 (itself)
    question = struct.pack("!H", 0xC00C) + struct.pack("!HH", 0x0020, 0x0001)
    body = header + question
    return (
        Ether(dst="ff:ff:ff:ff:ff:ff", src=src)
        / IP(src="10.0.0.2", dst="10.0.0.255")
        / UDP(sport=137, dport=137)
        / Raw(body)
    )


def harvest_nbns() -> None:
    query = nbns_query_frame(SRC_MAC, "WORKSTATION")
    response = nbns_response_frame(SRC_MAC, "WORKSTATION")
    loop = nbns_compression_loop_frame(SRC_MAC)
    wrpcap(str(TESTDATA / "nbns.pcap"), [query, response])
    wrpcap(str(TESTDATA / "nbns_loop.pcap"), [loop])
    golden = {
        "protocol": "NBT-NS",
        "dst": NBNS_DST,
        "src": SRC_MAC,
        "udp_port": 137,
        "frames": [
            {
                "name": "query",
                "id": 22136,
                "flags": 16,
                "qdcount": 1,
                "questions": [
                    {"name": "WORKSTATION", "qtype": 32, "qclass": 1},
                ],
            },
            {
                "name": "response",
                "id": 22136,
                "flags": 34048,
                "ancount": 1,
                "answers": [
                    {"name": "WORKSTATION", "type": 32, "class": 1, "ttl": 300, "rdata": "10.0.0.1"},
                ],
            },
        ],
    }
    (TESTDATA / "nbns.json").write_text(json.dumps(golden, indent=2))


# --------------------------------------------------------------------------- main


def main() -> None:
    TESTDATA.mkdir(parents=True, exist_ok=True)
    harvest_dtp()
    harvest_vtp()
    harvest_mvrp()
    harvest_lacp()
    harvest_pagp()
    harvest_hsrp()
    harvest_glbp()
    harvest_eigrp()
    harvest_llmnr()
    harvest_nbns()
    print(f"fixtures written to {TESTDATA}")


if __name__ == "__main__":
    main()
