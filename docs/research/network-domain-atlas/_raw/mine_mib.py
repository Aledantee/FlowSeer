#!/usr/bin/env python3
"""Index every MIB file in spec/mib: module name, imports, root OID anchors, object counts."""
import os, re, sys, json, collections

ROOT = "spec/mib"
rows = []
mod_re = re.compile(r'^\s*([A-Za-z0-9][\w-]*)\s+DEFINITIONS\s*(?:::=)?\s*BEGIN', re.M)
imp_re = re.compile(r'\bIMPORTS\b(.*?);', re.S)
oid_assign = re.compile(r'^\s*([a-zA-Z][\w-]*)\s+OBJECT IDENTIFIER\s*::=\s*\{([^}]*)\}', re.M)
objtype = re.compile(r'^\s*([a-zA-Z][\w-]*)\s+OBJECT-TYPE', re.M)
notif = re.compile(r'^\s*([a-zA-Z][\w-]*)\s+(?:NOTIFICATION-TYPE|TRAP-TYPE)', re.M)
ident = re.compile(r'^\s*([a-zA-Z][\w-]*)\s+MODULE-IDENTITY', re.M)
enterprise = re.compile(r'::=\s*\{\s*enterprises\s+(\d+)\s*\}')

for dirpath, _, files in os.walk(ROOT):
    for f in sorted(files):
        p = os.path.join(dirpath, f)
        try:
            txt = open(p, errors="replace").read()
        except Exception:
            continue
        m = mod_re.search(txt)
        name = m.group(1) if m else None
        if not name:
            continue
        imports = []
        im = imp_re.search(txt)
        if im:
            imports = sorted(set(re.findall(r'FROM\s+([A-Za-z0-9][\w-]*)', im.group(1))))
        oids = oid_assign.findall(txt)
        ent = enterprise.findall(txt)
        mi = ident.search(txt)
        rows.append(dict(
            file=p,
            vendor_dir=os.path.relpath(dirpath, ROOT),
            module=name,
            module_identity=mi.group(1) if mi else None,
            imports=imports,
            n_objects=len(objtype.findall(txt)),
            n_notifications=len(notif.findall(txt)),
            n_oid_nodes=len(oids),
            enterprise_ids=sorted(set(ent)),
            size=len(txt),
        ))

json.dump(rows, open(sys.argv[1], "w"), indent=1)
print("modules:", len(rows))
byv = collections.Counter(r["vendor_dir"].split("/")[0] for r in rows)
for k, v in byv.most_common():
    print(f"  {v:5d}  {k}")
print("total OBJECT-TYPEs:", sum(r["n_objects"] for r in rows))
print("total NOTIFICATIONs:", sum(r["n_notifications"] for r in rows))
