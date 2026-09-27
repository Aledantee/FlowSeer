#!/usr/bin/env python3
"""Compare the registry's models against live catalogues.

Usage: catalogue.py REGISTRY [OVERRIDE...]

Pass the machine-wide registry first, then the project override; a model in
a later file replaces the model of the same id in an earlier one. A missing
file is skipped.

Reads models.dev (vendor list prices) and OpenRouter (broker prices, creation
dates), prints one line per registry model with both feeds' price and context
beside the registry's, then lists recent ids the registry lacks. No YAML
library is needed: the registry's model ids are the two-space-indented keys
under `models:`.
"""

import json
import os
import re
import subprocess
import sys
import time

MODELS_DEV = "https://models.dev/api.json"
OPENROUTER = "https://openrouter.ai/api/v1/models"
VENDOR_PREFIX = {"zai": "z-ai", "alibaba": "qwen"}  # OpenRouter's slug where it differs


def fetch(url):
    # curl rather than urllib: models.dev answers urllib's default
    # User-Agent with 403 and truncates its 4.5 MB body through urllib.
    out = subprocess.run(["curl", "-sS", "--fail", "--max-time", "60", url],
                         capture_output=True, check=True)
    return json.loads(out.stdout)


def registry_models(paths):
    out = {}
    for path in paths:
        if os.path.exists(path):
            out.update(registry_file_models(path))
    return out


def registry_file_models(path):
    out, section = {}, None
    for line in open(path):
        if re.match(r"^\w", line):
            section = line.split(":")[0]
            continue
        m = re.match(r"^  ([A-Za-z0-9.\-]+): \{(.*)\}", line)
        if section == "models" and m:
            fields = dict(re.findall(r"(\w+): (\[[^\]]*\]|[^,}]+)", m.group(2)))
            out[m.group(1)] = fields
    return out


def norm(s):
    return re.sub(r"[^a-z0-9]", "", s.lower())


def main(paths):
    reg = registry_models(paths)
    md = fetch(MODELS_DEV)
    orr = {m["id"]: m for m in fetch(OPENROUTER)["data"]}

    print(f"{'model':22} {'registry $':>12} {'models.dev $':>14} {'openrouter $':>14}  ctx(md/or)")
    for mid, f in reg.items():
        vendor = f.get("vendor", "")
        prov = md.get(vendor, {}).get("models", {})
        md_hit = next((v for k, v in prov.items() if norm(k) == norm(mid)), None)
        slug = f"{VENDOR_PREFIX.get(vendor, vendor)}/{mid}"
        or_hit = orr.get(slug) or next((v for k, v in orr.items() if norm(k) == norm(slug)), None)
        md_p = f"{md_hit['cost']['input']}/{md_hit['cost']['output']}" if md_hit else "-"
        or_p = (f"{float(or_hit['pricing']['prompt'])*1e6:.2f}/{float(or_hit['pricing']['completion'])*1e6:.2f}"
                if or_hit else "-")
        ctx = f"{md_hit['limit']['context'] if md_hit else '-'}/{or_hit['context_length'] if or_hit else '-'}"
        print(f"{mid:22} {f.get('price','-'):>12} {md_p:>14} {or_p:>14}  {ctx}")

    cutoff = time.time() - 60 * 86400
    known = {norm(m) for m in reg}
    vendors = {VENDOR_PREFIX.get(f.get("vendor", ""), f.get("vendor", "")) for f in reg.values()}
    fresh = sorted(
        (m for m in orr.values()
         if m.get("created", 0) > cutoff and m["id"].split("/")[0] in vendors
         and ":batch" not in m["id"] and norm(m["id"].split("/", 1)[1]) not in known),
        key=lambda m: -m["created"])
    print("\nOn OpenRouter in the last 60 days, not in the registry:")
    for m in fresh[:40]:
        p = m["pricing"]
        print(f"  {m['id']:44} {float(p['prompt'])*1e6:6.2f}/{float(p['completion'])*1e6:6.2f}  ctx={m.get('context_length')}")


if __name__ == "__main__":
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    main(sys.argv[1:])
