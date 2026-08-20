# gNMI library conformance corpus

<!-- Generated from conformance_corpus_test.go; run the corpus golden test with -update-conformance to refresh. Do not edit by hand. -->

Rows: 6 covered, 0 accepted-risk, 1 pending.

## gNMI encoding, Set, and Subscribe behavior

| ID | Clause | Provenance | Behavior | Status |
| --- | --- | --- | --- | --- |
| `gn-proto-only-encoding` | gNMI spec §2.2.3 | KTD5 encoding negotiation: not every peer offers JSON_IETF | a peer advertising only PROTO still round-trips Get; JSON_IETF is preferred when offered | covered |
| `gn-per-path-set-error` | gNMI spec §3.4.2 | deprecated UpdateResult.Message is the only per-path failure channel several implementations use | a Set response carrying a per-path error surfaces which path failed as an attribute | covered |
| `gn-stream-termination-latch` | gNMI spec §3.5 | session-lifecycle HTD: a dropped stream never resumes itself | abrupt server termination of a STREAM subscription latches Stream.Err; buffered events stay readable; ONCE completion is clean | covered |
| `gn-presync-buffering` | gNMI spec §3.5.1.4 | KTD5: sync_response is the cold-start-complete signal | updates before sync_response buffer as initial state and emit as Added at sync; each post-sync notification batch emits at most one event per affected row | covered |
| `gn-subscribe-once-snapshot` | gNMI spec §3.5.1.5.1 | FlowSeer reference target (t1) | Subscribe ONCE assembles the snapshot's leaf updates into typed rows through the generated descriptor and ends cleanly at sync | covered |
| `gn-stream-sync-cold-start` | gNMI spec §3.5.1.5.2 | FlowSeer reference target (t1) | STREAM cold start emits Added per row at sync, then the target's periodic leaf change arrives as one Modified per batch | covered |
| `gn-aruba-set-capability` | gNMI spec §3.4 | R14: external research suggests AOS-CX gNMI is telemetry-oriented | whether Aruba CX accepts config writes via Set is verified early on lab hardware; if not, the write criterion converts per R14 with the fallback recorded | pending |

