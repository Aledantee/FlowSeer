# NETCONF library conformance corpus

<!-- Generated from conformance_corpus_test.go; run the corpus golden test with -update-conformance to refresh. Do not edit by hand. -->

Rows: 6 covered, 0 accepted-risk, 0 pending.

## NETCONF session and edit behavior

| ID | Clause | Provenance | Behavior | Status |
| --- | --- | --- | --- | --- |
| `nc-candidate-running-readonly` | RFC 6241 §8.3 | Cisco IOS-XE 17.x programmability guide: candidate mode disables writes to running | edit target is capability-driven per session: candidate wins over writable-running; neither → typed unsupported error | covered |
| `nc-lock-denied-retryable` | RFC 6241 §7.5 | RFC 6241 lock-denied semantics; multi-manager labs | lock-denied rpc-error maps to the retryable netconf/lock-denied code, never a terminal session error | covered |
| `nc-validate-fail-discard-unlock` | RFC 6241 §8.6 | AE2: rejected candidate must leave running unchanged | a validate/commit failure triggers discard-changes plus unlock before surfacing the device error; commit never runs | covered |
| `nc-dead-transport-latch` | RFC 6241 §2 | session-lifecycle HTD: dead SSH transports must latch, not block | a peer that stops responding trips the keepalive guard within the configured deadline and latches Err; later RPCs fail fast | covered |
| `nc-candidate-edit-cycle` | RFC 6241 §8.3/§8.4 | netopeer2 t1 reference server | lock → edit-config → validate → commit → unlock round-trips a fixture edit into running, proven by read-back walk | covered |
| `nc-invalid-edit-discard` | RFC 7950 §9.2.4 | netopeer2 t1 reference server (sysrepo range validation) | an out-of-range leaf fails the edit; the library discards and unlocks; read-back shows running unchanged and the candidate lock is free | covered |

