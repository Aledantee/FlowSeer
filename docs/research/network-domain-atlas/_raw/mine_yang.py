#!/usr/bin/env python3
"""Index every YANG module: name, kind, namespace, prefix, revision, org, imports, top-level nodes."""
import os, re, sys, json, collections

ROOT = "spec/yang"
rows = []
head = re.compile(r'^\s*(module|submodule)\s+([\w.:-]+)\s*\{', re.M)
ns = re.compile(r'^\s*namespace\s+"([^"]+)"', re.M)
pfx = re.compile(r'^\s*prefix\s+"?([\w.:-]+)"?\s*;', re.M)
rev = re.compile(r'^\s*revision\s+"?(\d{4}-\d{2}-\d{2})"?', re.M)
org = re.compile(r'^\s*organization\s*\n?\s*"([^"]*)"', re.M | re.S)
imp = re.compile(r'^\s*import\s+([\w.:-]+)', re.M)
belongs = re.compile(r'^\s*belongs-to\s+([\w.:-]+)', re.M)
# top-level data nodes (indent 2 spaces typical, but be permissive: node at depth 1)
node = re.compile(r'^\s{1,4}(container|list|leaf|leaf-list|choice|grouping|augment|rpc|notification|typedef|identity|feature)\s+([\w.:-]+|"[^"]+")', re.M)

for dirpath, _, files in os.walk(ROOT):
    for f in sorted(files):
        if not f.endswith(".yang"):
            continue
        p = os.path.join(dirpath, f)
        txt = open(p, errors="replace").read()
        h = head.search(txt)
        if not h:
            continue
        revs = rev.findall(txt)
        counts = collections.Counter(k for k, _ in node.findall(txt))
        rows.append(dict(
            file=p,
            dir=os.path.relpath(dirpath, ROOT),
            kind=h.group(1),
            name=h.group(2),
            namespace=(ns.search(txt).group(1) if ns.search(txt) else None),
            prefix=(pfx.search(txt).group(1) if pfx.search(txt) else None),
            belongs_to=(belongs.search(txt).group(1) if belongs.search(txt) else None),
            revisions=revs[:3],
            latest_revision=(max(revs) if revs else None),
            organization=(re.sub(r'\s+', ' ', org.search(txt).group(1)).strip()[:80] if org.search(txt) else None),
            imports=sorted(set(imp.findall(txt))),
            counts=dict(counts),
            size=len(txt),
        ))

json.dump(rows, open(sys.argv[1], "w"), indent=1)
print("yang files:", len(rows))
for k, v in collections.Counter(r["dir"].split("/")[0] for r in rows).most_common():
    print(f"  {v:5d}  {k}")
print("modules:", sum(1 for r in rows if r["kind"] == "module"),
      "submodules:", sum(1 for r in rows if r["kind"] == "submodule"))
