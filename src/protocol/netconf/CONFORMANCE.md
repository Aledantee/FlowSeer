# NETCONF library conformance corpus

<!-- Generated from conformance_corpus_test.go; run the corpus golden test with -update-conformance to refresh. Do not edit by hand. -->

Rows: 12 covered, 0 accepted-risk, 0 pending.

## NETCONF session and edit behavior

| ID | Clause | Provenance | Behavior | Status |
| --- | --- | --- | --- | --- |
| `nc-candidate-running-readonly` | RFC 6241 §8.3 | Cisco IOS-XE 17.x programmability guide: candidate mode disables writes to running | edit target is capability-driven per session: candidate wins over writable-running; neither → typed unsupported error | covered |
| `nc-lock-denied-retryable` | RFC 6241 §7.5 | RFC 6241 lock-denied semantics; multi-manager labs | lock-denied rpc-error maps to the retryable netconf/lock-denied code, never a terminal session error | covered |
| `nc-validate-fail-discard-unlock` | RFC 6241 §8.6 | candidate validation must complete before commit | a validation failure triggers discard-changes plus unlock before surfacing the device error; commit never runs | covered |
| `nc-dead-transport-latch` | RFC 6241 §2 | session lifecycle: dead SSH transports latch a terminal error | a peer that stops responding trips the keepalive guard within the configured deadline and latches Err; later RPCs fail fast | covered |
| `nc-candidate-edit-cycle` | RFC 6241 §8.3/§8.4 | netopeer2 t1 reference server | lock → edit-config → validate → commit → unlock round-trips a fixture edit into running, proven by read-back walk | covered |
| `nc-invalid-edit-discard` | RFC 7950 §9.2.4 | netopeer2 t1 reference server (sysrepo range validation) | an out-of-range leaf fails the edit; the library discards and unlocks; read-back shows running unchanged and the candidate lock is free | covered |
| `nc-t4-identity` | device identity reads | Cisco CSR1000v running IOS-XE 17.3.2, lab device 2026-09-18 | hostname, serial, model, and OS version return as typed values via the generated native and device-hardware bindings | covered |
| `nc-t4-invalid-rollback` | rejected edit leaves running unchanged | Cisco CSR1000v running IOS-XE 17.3.2, lab device 2026-09-18 | an out-of-range leaf is rejected with an rpc-error the library surfaces as its RPC error code; a read-back diff of the whole native subtree proves running unchanged. This device advertises writable-running and no candidate datastore, so the edit targets running directly and the proof is the read-back diff rather than a candidate discard | covered |
| `nc-t4-reversible-edit` | reversible config edit | Cisco CSR1000v running IOS-XE 17.3.2, lab device 2026-09-18 | a create and its delete each round-trip against the running datastore, both proven by read-back; the device is left without the fixture username | covered |
| `nc-t4-interface-walk` | typed interface state walk | Cisco CSR1000v running IOS-XE 17.3.2, lab device 2026-09-18 | the interface-state Walker completes with typed rows on hardware | covered |
| `nc-t4-watch-induced` | interface change detection | Cisco CSR1000v running IOS-XE 17.3.2, lab device 2026-09-18 (operator-induced toggle) | an interface state change between ticks emits exactly one Modified for that row on hardware | covered |
| `nc-t4-revision-drift` | model revision comparison | Cisco CSR1000v running IOS-XE 17.3.2, lab device 2026-09-18 | device-advertised module revisions diff against the committed lockfile; drift surfaces as warnings | covered |

