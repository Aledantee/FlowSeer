#!/usr/bin/env python3
"""Compact per-domain evidence brief."""
import json, sys, os, re
KB = os.path.dirname(os.path.abspath(__file__))
ev = json.load(open(f"{KB}/entity_evidence.json"))
dom = sys.argv[1]
NT = int(sys.argv[2]) if len(sys.argv) > 2 else 6
for eid, e in ev.items():
    if e["domain"] != dom:
        continue
    print(f'\n## {eid} — {e["name"]}')
    for fam in sorted(e["mib"]):
        seen, items = set(), []
        for t in e["mib"][fam]:
            k = f'{t["module"]}::{t["table"]}'
            if k in seen:
                continue
            seen.add(k)
            ix = "|".join(t["index"]) if t.get("index") else (f'A:{t["aug"]}' if t.get("aug") else "")
            items.append(f'{t["table"]}[{ix}]')
            if len(items) >= NT:
                break
        mods = ",".join(e["mib_modules"].get(fam, [])[:6])
        print(f'  M:{fam} <{mods}> :: ' + " ".join(items))
    for fam in sorted(e["yang"]):
        seen, items = set(), []
        for n in e["yang"][fam]:
            p = n["path"]
            if p in seen:
                continue
            seen.add(p)
            items.append(f'{n["module"]}:{p}' + (f'(k={n["key"]})' if n.get("key") else ""))
            if len(items) >= NT:
                break
        print(f'  Y:{fam} :: ' + " ".join(items))
