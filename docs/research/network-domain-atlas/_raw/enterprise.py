import json, os, collections, re
kb = os.path.dirname(os.path.abspath(__file__))
rows = json.load(open(f"{kb}/mib_index.json"))
by = collections.defaultdict(collections.Counter)
for r in rows:
    v = r['vendor_dir'].split('/')[0]
    for e in r['enterprise_ids']:
        by[v][e] += 1
for v in sorted(by):
    print(v, by[v].most_common(6))
