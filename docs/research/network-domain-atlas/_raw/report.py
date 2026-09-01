#!/usr/bin/env python3
"""Dump per-entity evidence: which source families carry it, with representative tables+keys."""
import json, sys, os
KB = os.path.dirname(os.path.abspath(__file__))
ev = json.load(open(f"{KB}/entity_evidence.json"))

want = sys.argv[1:] or list(ev)
for eid in want:
    e = ev[eid]
    print("=" * 78)
    print(f'{eid}  [{e["domain"]}]  {e["name"]}')
    for fam in sorted(e["mib"]):
        tl = e["mib"][fam]
        print(f'  MIB/{fam} ({len(tl)}):')
        seen = set()
        for t in tl:
            k = (t["module"], t["table"])
            if k in seen:
                continue
            seen.add(k)
            if len(seen) > 22:
                print("      ...")
                break
            ix = ",".join(t["index"]) if t.get("index") else (f'AUG {t["aug"]}' if t.get("aug") else "-")
            print(f'      {t["module"]}::{t["table"]}  [{ix}]')
    for fam in sorted(e["yang"]):
        nl = e["yang"][fam]
        print(f'  YANG/{fam} ({len(nl)}):')
        seen = set()
        for n in nl:
            if n["path"] in seen:
                continue
            seen.add(n["path"])
            if len(seen) > 22:
                print("      ...")
                break
            print(f'      {n["module"]}: {n["path"]}' + (f'  key={n["key"]}' if n.get("key") else ""))
    only = {k: v for k, v in e["mib_modules"].items()}
    print("  modules:", json.dumps({k: v[:14] for k, v in only.items()}))
