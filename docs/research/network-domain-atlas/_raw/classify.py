#!/usr/bin/env python3
"""Match the taxonomy against MIB tables/modules and YANG nodes -> per-entity, per-source-family evidence."""
import json, re, sys, os, collections
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from taxonomy import TAXONOMY

KB = os.path.dirname(os.path.abspath(__file__))
mib_tables = json.load(open(f"{KB}/mib_tables.json"))
mib_index = json.load(open(f"{KB}/mib_index.json"))
yang_nodes = json.load(open(f"{KB}/yang_nodes.json"))
yang_index = {r["file"]: r for r in json.load(open(f"{KB}/yang_index.json"))}


def family_mib(vendor_dir, module):
    v = vendor_dir.split("/")[0]
    if v in ("ietf", "ieee"):
        return v
    if v == "cisco":
        return "cisco-smb" if "smb" in vendor_dir else "cisco-ent"
    if v == "hp":
        return {"hh3c": "hp-comware", "procurve": "hp-procurve"}.get(vendor_dir.split("/")[-1], "hp-other")
    if v == "aruba":
        return "aruba-cx" if "cx" in vendor_dir else "aruba-wireless"
    if v == "ruckus":
        return "ruckus-icx" if "icx" in vendor_dir else "ruckus-wireless"
    if v == "ubiquiti":
        return "ubnt-" + vendor_dir.split("/")[-1]
    if v == "lancom":
        return "lancom-" + (vendor_dir.split("/")[1] if "/" in vendor_dir else "x")
    return v


def family_yang(path):
    p = path.replace("spec/yang/", "")
    if p.startswith("openconfig"):
        return "openconfig"
    if p.startswith("ietf"):
        return "ietf-yang"
    if p.startswith("cisco"):
        return "cisco-iosxe"
    if p.startswith("ruckus"):
        return "ruckus-icx"
    if p.startswith("aruba"):
        return "aruba-cx"
    return "other"


ents = {}
for eid, domain, name, pats in TAXONOMY:
    ents[eid] = dict(id=eid, domain=domain, name=name,
                     rx=re.compile("|".join(pats), re.I),
                     mib={}, yang={}, mib_modules=collections.defaultdict(set))

for m in mib_tables:
    fam = family_mib(m["vendor"], m["module"])
    for t in m["tables"]:
        hay = f'{m["module"]} {t["table"]}'
        for e in ents.values():
            if e["rx"].search(t["table"]) or e["rx"].search(m["module"]):
                e["mib"].setdefault(fam, []).append(
                    dict(module=m["module"], table=t["table"], index=t.get("index"), aug=t.get("augments")))
                e["mib_modules"][fam].add(m["module"])

for m in mib_index:
    fam = family_mib(m["vendor_dir"], m["module"])
    for e in ents.values():
        if e["rx"].search(m["module"]):
            e["mib_modules"][fam].add(m["module"])

for mod in yang_nodes:
    fam = family_yang(mod["file"])
    for n in mod["nodes"]:
        leaf = n["path"].split("/")[-1]
        if ents and (any(True for _ in [0])):
            pass
        for e in ents.values():
            if e["rx"].search(leaf) or e["rx"].search(mod["module"]):
                e["yang"].setdefault(fam, []).append(
                    dict(module=mod["module"], path=n["path"], kind=n["kind"], key=n.get("key")))

out = {}
for eid, e in ents.items():
    out[eid] = dict(
        id=eid, domain=e["domain"], name=e["name"],
        mib={k: v[:400] for k, v in e["mib"].items()},
        mib_modules={k: sorted(v) for k, v in e["mib_modules"].items()},
        yang={k: v[:400] for k, v in e["yang"].items()},
        counts=dict(mib_tables={k: len(v) for k, v in e["mib"].items()},
                    yang_nodes={k: len(v) for k, v in e["yang"].items()}),
    )
json.dump(out, open(f"{KB}/entity_evidence.json", "w"), indent=1)

print(f'{"entity":26s} {"domain":14s} MIBfam YANGfam  tables  nodes')
for eid, e in out.items():
    print(f'{eid:26s} {e["domain"]:14s} {len(e["mib_modules"]):5d} {len(e["yang"]):6d} '
          f'{sum(e["counts"]["mib_tables"].values()):7d} {sum(e["counts"]["yang_nodes"].values()):6d}')
