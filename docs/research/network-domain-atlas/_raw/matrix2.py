#!/usr/bin/env python3
"""Emit matrices README with coverage matrix, entity index, and top-tables-per-entity."""
import json, os, sys

KB = os.path.dirname(os.path.abspath(__file__))
ev = json.load(open(f"{KB}/entity_evidence.json"))
body = open(sys.argv[1]).read()

DOMAIN_ORDER = ["L0 Platform", "L1 Interface", "L2 Switching", "L3 IP", "L3 Routing",
                "QoS", "Security", "Wireless", "Ops", "WAN"]
PART = {"L0 Platform": "01-platform", "L1 Interface": "02-interface",
        "L2 Switching": "03-switching", "L3 IP": "04-ip", "L3 Routing": "05-routing",
        "QoS": "06-qos", "Security": "07-security", "Wireless": "08-wireless",
        "Ops": "09-ops", "WAN": "10-wan-access"}

out = []
out.append("---")
out.append("title: Matrices — entity coverage and evidence volume")
out.append("date: 2026-08-30")
out.append("---\n")
out.append("# Matrices\n")
out.append("Generated from the corpus by `../_raw/classify.py` and `../_raw/matrix.py`.")
out.append("Regenerate after changing `spec/` or the taxonomy.\n")
out.append("## Legend\n")
out.append("Symbols count matched anchors (SNMP tables + YANG nodes) for that entity")
out.append("in that source family. They measure *how much the family says about the")
out.append("entity*, not quality:\n")
out.append("| | matches |")
out.append("|---|---|")
out.append("| ● | 10 or more |")
out.append("| ◐ | 3–9 |")
out.append("| ○ | 1–2 |")
out.append("| *(blank)* | none |\n")
out.append("Read a blank as \"this family does not model this entity in the vendored")
out.append("specs\" — not as \"the product cannot do it\". Several vendors expose")
out.append("features only through a REST API with no vendored schema; see the")
out.append("[vendor dossiers](../vendors/README.md).\n")
out.append("Classifier precision caveats are in")
out.append("[00-methodology](../00-methodology-and-lineages.md#precision-caveat).\n")
out.append("## Entity × source-family coverage\n")
out.append(body)

out.append("\n## Evidence volume by entity\n")
out.append("Total matched anchors per entity, largest first — a rough measure of how")
out.append("much the industry has written about each thing.\n")
out.append("| entity | domain | MIB tables | YANG nodes | total | record |")
out.append("|---|---|---|---|---|---|")
rows = []
for eid, e in ev.items():
    m = sum(e["counts"]["mib_tables"].values())
    y = sum(e["counts"]["yang_nodes"].values())
    rows.append((m + y, eid, e["domain"], m, y))
for tot, eid, dom, m, y in sorted(rows, reverse=True):
    out.append(f"| `{eid}` | {dom} | {m} | {y} | {tot} | [→](../entities/{PART[dom]}.md#{eid}) |")

out.append("\n## Source-family breadth\n")
out.append("How many of the 101 entities each family says anything about.\n")
fams = {}
for e in ev.values():
    for k in list(e["mib"]) + list(e["yang"]):
        fams[k] = fams.get(k, 0) + 1
out.append("| family | entities covered |")
out.append("|---|---|")
for k, v in sorted(fams.items(), key=lambda x: -x[1]):
    out.append(f"| {k} | {v} |")

open(sys.argv[2], "w").write("\n".join(out) + "\n")
print("ok", len(out))
