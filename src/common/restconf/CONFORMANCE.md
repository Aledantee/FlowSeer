# RESTCONF library conformance corpus

<!-- Generated from conformance_corpus_test.go; run the corpus golden test with -update-conformance to refresh. Do not edit by hand. -->

Rows: 10 covered, 0 accepted-risk, 0 pending.

## RESTCONF discovery, read, and write behavior

| ID | Clause | Provenance | Behavior | Status |
| --- | --- | --- | --- | --- |
| `rc-host-meta-discovery` | RFC 8040 §3.1 | clixon t1 reference server | the API root is discovered from /.well-known/host-meta's restconf link, with a /restconf probe fallback; neither → typed discovery error | covered |
| `rc-nonconformant-error-body` | RFC 8040 §7.1 | Ruckus FastIron RESTCONF Programmers Guide 09.0.10 (nonconformant error payloads expected on ICX) | a conformant ietf-restconf:errors body decodes to typed attributes; a malformed body is preserved raw on the error for the corpus | covered |
| `rc-stale-write-conflict` | RFC 8040 §3.4.1.2 | ETag conditional-write semantics | a 412 on If-Match maps to the retryable restconf/conflict code | covered |
| `rc-etag-absent-unconditional` | RFC 8040 §3.4.1.2 | peers without ETag support | missing ETag support degrades to an unconditional write, and the read-back verification still runs | covered |
| `rc-absent-resource-404` | RFC 8040 §4.3 | watcher semantics: an absent optional subtree is data | a 404 on a data resource is (nil, nil), not an error; the Watcher turns it into Removed events | covered |
| `rc-edit-read-back` | RFC 8040 §4.5/§4.7 | clixon t1 reference server | PUT creates and DELETE removes, each proven by re-reading the resource | covered |
| `rc-depth-fields-unverified` | RFC 8040 §4.8.2/§4.8.3 | Ruckus ICX7150-24P, FastIron 10.0.10g, lab device 172.16.0.6 | resolved on hardware: FastIron 10.0.10g honors the depth parameter, so the depth/fields path is exercised for real; the client-side-pruning fallback remains for peers that ignore it (see rc-t4-depth-fields) | covered |
| `rc-t4-identity` | device identity reads | Ruckus ICX7150-24P, FastIron 10.0.10g, lab device 172.16.0.6 | hostname is read as a typed value end to end (dial → host-meta discovery → GET → RFC 7951 decode), from /system/config/hostname because FastIron mirrors it there and leaves /system/state empty. Documented FastIron surface gap: serial-no, part-no, and software-version are not populated in this device's openconfig RESTCONF surface (CLI-only); model appears only under icx-openconfig-platform-aug:switch-model, an augmentation absent from the vendored 9.0.x YANG corpus (device/corpus version skew), so the typed bindings cannot surface it. The ICX system deviation removes only dns/server port, so these are unimplemented runtime state, not modeled deviations. | covered |
| `rc-t4-reversible-edit` | reversible config edit | Ruckus ICX7150-24P, FastIron 10.0.10g, lab device 172.16.0.6 | a reversible interface-description edit (PATCH) and its revert are each proven by read-back, using a conditional If-Match write. FastIron rejects writes to openconfig-system config leaves (login-banner/hostname return "invalid internal value"), so the device's documented, non-disruptive writable leaf is used; the port's enabled state is captured and restored so forwarding is never touched. | covered |
| `rc-t4-depth-fields` | RFC 8040 §4.8.2/§4.8.3 | Ruckus ICX7150-24P, FastIron 10.0.10g, lab device 172.16.0.6 (resolves rc-depth-fields-unverified) | measured on hardware: FastIron 10.0.10g honors depth — a GET of /openconfig-system:system returned 899 bytes full versus 80 bytes at depth=2 — so the depth path is verified against a real device; the client-side-pruning fallback still covers peers that ignore it. | covered |

