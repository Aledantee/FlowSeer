# Working files

The extraction pipeline behind the atlas, plus the intermediate evidence it
produced. Kept so any claim can be checked and the whole thing regenerated after
`spec/` changes.

## Pipeline

| Script | Reads | Writes |
|---|---|---|
| `mine_mib.py` | `spec/mib/**` | `mib_index.json` — one record per MIB module: name, imports, object/notification counts, enterprise OIDs |
| `mine_tables.py` | `spec/mib/**` | `mib_tables.json` — every SNMP conceptual table with its `INDEX` or `AUGMENTS` clause (28,454 of them) |
| `mine_yang.py` | `spec/yang/**` | `yang_index.json` — module metadata: namespace, prefix, revisions, organisation, imports |
| `mine_yang_nodes.py` | `spec/yang/**` | `yang_nodes.json` — brace-tracked data-node paths to depth 4 with list keys (26,490) |
| `taxonomy.py` | — | the 101-entity taxonomy: id, domain, name, and the regex patterns that match it |
| `classify.py` | the four JSONs + taxonomy | `entity_evidence.json` — per entity, which source families carry it and with which tables/nodes |
| `brief.py` | `entity_evidence.json` | the `evidence_*.txt` dumps, one per domain |
| `report.py` | `entity_evidence.json` | a verbose per-entity dump for reading one entity closely |
| `matrix.py` | `entity_evidence.json` | the coverage-matrix table body |
| `matrix2.py` | matrix body + evidence | `matrices/README.md` |
| `index_gen.py` | `entity_evidence.json` | the alphabetical entity index for `INDEX.md` |
| `lineage.py` | `mib_tables.json` | the shared-table-name comparisons behind the lineage findings |
| `enterprise.py` | `mib_index.json` | enterprise OIDs per vendor |

The scripts expect the four JSON files to sit in the same directory as
themselves. They are not committed — regenerate them; together they are tens of
megabytes.

Run order and exact invocations are in
[`../INDEX.md`](../INDEX.md#regenerating).

## Evidence dumps

`evidence_<domain>.txt` — for each entity in that domain, the matched tables and
YANG nodes per source family, with keys. These are what the entity records were
written from. Regenerate with `brief.py`.

## Notes

`prior-art-notes.md` — the raw reading notes on SNMP::Info, NAPALM, LibreNMS,
Netdisco, SuzieQ, NetBox and RFC 8343, before they were worked up into
[`../01-prior-art.md`](../01-prior-art.md). Kept because they contain verbatim
method and field lists that the finished document summarises.

## Caveats

The classifier is regex-based and over-matches. Known false positives are listed
in [`../00-methodology-and-lineages.md`](../00-methodology-and-lineages.md#precision-caveat)
and each affected entity record says which of its matches it discounted. Treat
the matrices as a measure of *how much a source family says about an entity*,
never as a support statement.
