# gNMI library conformance corpus

<!-- Generated from conformance_corpus_test.go; run the corpus golden test with -update-conformance to refresh. Do not edit by hand. -->

Rows: 6 covered, 0 accepted-risk, 5 pending.

## gNMI encoding, Set, and Subscribe behavior

| ID | Clause | Provenance | Behavior | Status |
| --- | --- | --- | --- | --- |
| `gn-proto-only-encoding` | gNMI spec §2.2.3 | encoding negotiation: not every peer offers JSON_IETF | a peer advertising only PROTO still round-trips Get; JSON_IETF is preferred when offered | covered |
| `gn-per-path-set-error` | gNMI spec §3.4.2 | deprecated UpdateResult.Message is the only per-path failure channel several implementations use | a Set response carrying a per-path error surfaces which path failed as an attribute | covered |
| `gn-stream-termination-latch` | gNMI spec §3.5 | session lifecycle: a dropped stream never resumes itself | abrupt server termination of a STREAM subscription latches Stream.Err; buffered events stay readable; ONCE completion is clean | covered |
| `gn-presync-buffering` | gNMI spec §3.5.1.4 | sync_response is the cold-start-complete signal | updates before sync_response buffer as initial state and emit as Added at sync; each post-sync notification batch emits at most one event per affected row | covered |
| `gn-subscribe-once-snapshot` | gNMI spec §3.5.1.5.1 | FlowSeer reference target (t1) | Subscribe ONCE assembles the snapshot's leaf updates into typed rows through the generated descriptor and ends cleanly at sync | covered |
| `gn-stream-sync-cold-start` | gNMI spec §3.5.1.5.2 | FlowSeer reference target (t1) | STREAM cold start emits Added per row at sync, then the target's periodic leaf change arrives as one Modified per batch | covered |
| `gn-aruba-set-capability` | gNMI spec §3.4 | Aruba CX lab capability check (pending) | the lab check records whether the device supports config writes through Set and captures the rejection when it does not | pending |
| `gn-t4-identity` | device identity reads | Aruba CX lab device (pending) | hostname, software version, serial, and part number return via Get over the advertised OpenConfig models | pending |
| `gn-t4-set-verdict` | config write capability | Aruba CX lab device (pending) | a reversible login-banner Set is verified by Get; if the device rejects the write, the check records the rejection | pending |
| `gn-t4-stream` | interface state subscription | Aruba CX lab device (pending) | a STREAM subscription over interface state delivers sync and keeps flowing on hardware | pending |
| `gn-t4-revision-drift` | model revision comparison | Aruba CX lab device (pending) | advertised model versions diff against the committed lockfile; drift surfaces as warnings | pending |

