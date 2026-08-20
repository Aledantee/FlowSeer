# RESTCONF library conformance corpus

<!-- Generated from conformance_corpus_test.go; run the corpus golden test with -update-conformance to refresh. Do not edit by hand. -->

Rows: 6 covered, 0 accepted-risk, 4 pending.

## RESTCONF discovery, read, and write behavior

| ID | Clause | Provenance | Behavior | Status |
| --- | --- | --- | --- | --- |
| `rc-host-meta-discovery` | RFC 8040 §3.1 | clixon t1 reference server | the API root is discovered from /.well-known/host-meta's restconf link, with a /restconf probe fallback; neither → typed discovery error | covered |
| `rc-nonconformant-error-body` | RFC 8040 §7.1 | Ruckus FastIron RESTCONF Programmers Guide 09.0.10 (nonconformant error payloads expected on ICX) | a conformant ietf-restconf:errors body decodes to typed attributes; a malformed body is preserved raw on the error for the corpus | covered |
| `rc-stale-write-conflict` | RFC 8040 §3.4.1.2 | KTD9 conditional-write discipline | a 412 on If-Match maps to the retryable restconf/conflict code | covered |
| `rc-etag-absent-unconditional` | RFC 8040 §3.4.1.2 | KTD9: peers without ETag support | missing ETag support degrades to an unconditional write, and the read-back verification still runs | covered |
| `rc-absent-resource-404` | RFC 8040 §4.3 | watcher semantics: an absent optional subtree is data | a 404 on a data resource is (nil, nil), not an error; the Watcher turns it into Removed events | covered |
| `rc-edit-read-back` | RFC 8040 §4.5/§4.7 | clixon t1 reference server | PUT creates and DELETE removes, each proven by re-reading the resource rather than trusting the status code (R12) | covered |
| `rc-depth-fields-unverified` | RFC 8040 §4.8.2/§4.8.3 | plan assumption: ICX support for depth/fields is unconfirmed | depth and fields are sent only on request; peers ignoring them still get correct results via client-side pruning — to be verified on ICX lab hardware | pending |
| `rc-t4-identity` | AE1 | Ruckus ICX lab device (pending) | hostname, serial, model, and OS version return as typed values via the generated openconfig-system and openconfig-platform bindings | pending |
| `rc-t4-reversible-edit` | R12 | Ruckus ICX lab device (pending) | a reversible login-banner edit and its revert are proven by read-back | pending |
| `rc-t4-depth-fields` | RFC 8040 §4.8.2/§4.8.3 | Ruckus ICX lab device (pending; resolves rc-depth-fields-unverified) | whether ICX honors depth/fields is measured and recorded; either way the client behaves correctly | pending |

