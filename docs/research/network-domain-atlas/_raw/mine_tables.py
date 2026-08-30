#!/usr/bin/env python3
"""Extract SNMP conceptual tables (xxxTable + INDEX clause) from every MIB."""
import os, re, sys, json

ROOT = "spec/mib"
tbl_re = re.compile(
    r'^\s*([a-zA-Z][\w-]*)\s+OBJECT-TYPE\s+SYNTAX\s+SEQUENCE OF\s+([\w-]+)', re.M | re.S)
entry_re = re.compile(
    r'^\s*([a-zA-Z][\w-]*)\s+OBJECT-TYPE(.*?)::=\s*\{\s*([\w-]+)\s+1\s*\}', re.M | re.S)
index_re = re.compile(r'\bINDEX\s*\{([^}]*)\}', re.S)
aug_re = re.compile(r'\bAUGMENTS\s*\{([^}]*)\}', re.S)
mod_re = re.compile(r'^\s*([A-Za-z0-9][\w-]*)\s+DEFINITIONS', re.M)

out = []
for dirpath, _, files in os.walk(ROOT):
    for f in sorted(files):
        p = os.path.join(dirpath, f)
        txt = open(p, errors="replace").read()
        m = mod_re.search(txt)
        if not m:
            continue
        mod = m.group(1)
        # normalize whitespace for the entry scan
        tables = {}
        for tm in tbl_re.finditer(txt):
            tables[tm.group(1)] = dict(table=tm.group(1), seq=tm.group(2), index=None, augments=None)
        # find entry definitions to grab INDEX/AUGMENTS
        for em in re.finditer(r'^\s*([a-zA-Z][\w-]*)\s+OBJECT-TYPE(.*?)\n\s*::=\s*\{\s*([\w-]+)\s+1\s*\}', txt, re.M | re.S):
            body, parent = em.group(2), em.group(3)
            if parent in tables:
                ix = index_re.search(body)
                ag = aug_re.search(body)
                if ix:
                    tables[parent]["index"] = [x.strip() for x in re.sub(r'IMPLIED\s+', '', ix.group(1)).replace('\n', ' ').split(',') if x.strip()]
                if ag:
                    tables[parent]["augments"] = ag.group(1).strip()
        if tables:
            out.append(dict(module=mod, file=p, vendor=os.path.relpath(dirpath, ROOT),
                            tables=list(tables.values())))

json.dump(out, open(sys.argv[1], "w"), indent=1)
n = sum(len(o["tables"]) for o in out)
print("modules with tables:", len(out), "tables:", n)
