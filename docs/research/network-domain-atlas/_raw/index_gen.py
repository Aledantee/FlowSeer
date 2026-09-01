#!/usr/bin/env python3
"""Emit the alphabetical entity index rows for INDEX.md."""
import json, os, sys
KB = os.path.dirname(os.path.abspath(__file__))
ev = json.load(open(f"{KB}/entity_evidence.json"))
PART = {"L0 Platform": "01-platform", "L1 Interface": "02-interface",
        "L2 Switching": "03-switching", "L3 IP": "04-ip", "L3 Routing": "05-routing",
        "QoS": "06-qos", "Security": "07-security", "Wireless": "08-wireless",
        "Ops": "09-ops", "WAN": "10-wan-access"}
lines = ["| entity | name | domain | record |", "|---|---|---|---|"]
for eid in sorted(ev):
    e = ev[eid]
    lines.append(f'| `{eid}` | {e["name"]} | {e["domain"]} | [→](entities/{PART[e["domain"]]}.md#{eid}) |')
open(sys.argv[1], "w").write("\n".join(lines) + "\n")
print(len(ev), "entities")
