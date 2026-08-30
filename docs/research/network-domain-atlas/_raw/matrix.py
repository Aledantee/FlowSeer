#!/usr/bin/env python3
"""Emit the entity x source-family coverage matrix as markdown."""
import json, os, sys

KB = os.path.dirname(os.path.abspath(__file__))
ev = json.load(open(f"{KB}/entity_evidence.json"))

FAMS = [
    ("ietf", "IETF"), ("ieee", "IEEE"), ("openconfig", "OC"), ("ietf-yang", "IETFy"),
    ("cisco-iosxe", "CiscoXE"), ("cisco-smb", "CiscoSMB"),
    ("aruba-cx", "ArubaCX"), ("aruba-wireless", "ArubaW"),
    ("hp-procurve", "ProCurve"), ("hp-comware", "Comware"), ("hp-other", "HPother"),
    ("huawei", "Huawei"), ("dlink", "D-Link"),
    ("ruckus-icx", "RuckICX"), ("ruckus-wireless", "RuckW"),
    ("ubnt-edgemax", "EdgeMAX"), ("ubnt-unifi", "UniFi"), ("ubnt-airmax", "airMAX"),
    ("netgear", "Netgear"), ("mikrotik", "MikroTik"),
    ("lancom-lcos", "LCOS"), ("lancom-sx", "LCOS-SX"), ("lancom-lx", "LCOS-LX"),
]

DOMAIN_ORDER = ["L0 Platform", "L1 Interface", "L2 Switching", "L3 IP", "L3 Routing",
                "QoS", "Security", "Wireless", "Ops", "WAN"]

out = []
out.append("| entity | " + " | ".join(lbl for _, lbl in FAMS) + " |")
out.append("|" + "---|" * (len(FAMS) + 1))

for dom in DOMAIN_ORDER:
    out.append(f"| **{dom}** |" + " |" * len(FAMS))
    for eid, e in ev.items():
        if e["domain"] != dom:
            continue
        cells = []
        for key, _ in FAMS:
            m = len(e["mib"].get(key, []))
            y = len(e["yang"].get(key, []))
            n = m + y
            cells.append("" if n == 0 else ("●" if n >= 10 else ("◐" if n >= 3 else "○")))
        out.append(f"| `{eid}` | " + " | ".join(cells) + " |")

open(sys.argv[1], "w").write("\n".join(out) + "\n")
print("rows:", len(out))
