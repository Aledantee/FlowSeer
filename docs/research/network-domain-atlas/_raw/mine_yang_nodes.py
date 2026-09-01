#!/usr/bin/env python3
"""Brace-tracking extraction of YANG data-node paths (container/list/leaf-list, depth<=4) plus list keys."""
import os, re, sys, json

ROOT = "spec/yang"
STMT = re.compile(r'^\s*(container|list|choice|augment|rpc|notification|grouping|uses|key|identity|typedef|feature)\s+([^\s{;]+|"[^"]*")')
MAXD = 4

def strip_comments(txt):
    txt = re.sub(r'/\*.*?\*/', '', txt, flags=re.S)
    txt = re.sub(r'//[^\n]*', '', txt)
    return txt

out = []
for dirpath, _, files in os.walk(ROOT):
    for f in sorted(files):
        if not f.endswith(".yang"):
            continue
        p = os.path.join(dirpath, f)
        raw = open(p, errors="replace").read()
        txt = strip_comments(raw)
        mm = re.search(r'^\s*(?:sub)?module\s+([\w.:-]+)', txt, re.M)
        if not mm:
            continue
        stack = []      # list of (name, depth_at_open)
        depth = 0
        nodes = []
        keys = {}
        pending = None
        i = 0
        lines = txt.split("\n")
        for line in lines:
            s = STMT.match(line)
            if s:
                kind, name = s.group(1), s.group(2).strip('"')
                if kind == "key":
                    if stack:
                        keys["/".join(n for n, _ in stack)] = name.strip('";')
                elif kind in ("container", "list", "augment", "rpc", "notification", "grouping", "choice"):
                    pending = (kind, name)
            opens = line.count("{") - line.count("}")
            if "{" in line and pending:
                kind, name = pending
                stack.append((name, depth))
                if kind in ("container", "list", "augment", "rpc", "notification") and len(stack) <= MAXD:
                    nodes.append(dict(kind=kind, path="/".join(n for n, _ in stack)))
                pending = None
                depth += line.count("{")
                depth -= line.count("}")
                while stack and stack[-1][1] >= depth:
                    stack.pop()
            else:
                depth += line.count("{")
                depth -= line.count("}")
                while stack and stack[-1][1] >= depth:
                    stack.pop()
                pending = None
        for n in nodes:
            if n["path"] in keys:
                n["key"] = keys[n["path"]]
        out.append(dict(module=mm.group(1), file=p, nodes=nodes))

json.dump(out, open(sys.argv[1], "w"), indent=1)
print("modules:", len(out), "nodes:", sum(len(o["nodes"]) for o in out))
