import json, collections, os, re
kb = os.path.dirname(os.path.abspath(__file__))
t = json.load(open(f"{kb}/mib_tables.json"))

def tabs(pred):
    s = set()
    for m in t:
        if pred(m):
            s |= {x['table'] for x in m['tables']}
    return s

fp = tabs(lambda m: m['module'].startswith('FASTPATH'))
edge = tabs(lambda m: m['module'].startswith('EdgeSwitch'))
ng = tabs(lambda m: m['module'].startswith('NETGEAR'))
lcs = tabs(lambda m: m['module'].startswith('LCOS-SX'))
print("FASTPATH", len(fp), "EdgeSwitch", len(edge), "NETGEAR", len(ng), "LCOS-SX", len(lcs))
print("FP&Edge", len(fp & edge), "FP&NG", len(fp & ng), "Edge&NG", len(edge & ng), "FP&LCOS-SX", len(fp & lcs))
print("sample FP&Edge:", sorted(fp & edge)[:12])
print("sample Edge&NG:", sorted(edge & ng)[:12])

fo = tabs(lambda m: m['module'].startswith('FOUNDRY'))
hpsn = tabs(lambda m: m['module'].startswith('HP-SN'))
print("FOUNDRY", len(fo), "HP-SN", len(hpsn), "shared", len(fo & hpsn))
print("sample:", sorted(fo & hpsn)[:10])

def strip(n):
    return re.sub(r'^(hw|hh3c|h3c)', '', n, flags=re.I).lower()

hw = {strip(x) for x in tabs(lambda m: m['module'].startswith('HUAWEI'))}
h3 = {strip(x) for x in tabs(lambda m: m['module'].startswith('HH3C'))}
print("Huawei", len(hw), "HH3C", len(h3), "shared-after-strip", len(hw & h3))
print("sample:", sorted(hw & h3)[:18])

ar = tabs(lambda m: m['module'].startswith('ARUBAWIRED'))
hpicf = tabs(lambda m: m['module'].startswith('HP-ICF'))
print("ARUBAWIRED", len(ar), "HP-ICF", len(hpicf), "shared", len(ar & hpicf))
