# gNMI library conformance corpus

<!-- Generated from conformance_corpus_test.go; run the corpus golden test with -update-conformance to refresh. Do not edit by hand. -->

Rows: 12 covered, 1 accepted-risk, 0 pending.

## gNMI encoding, Set, and Subscribe behavior

| ID | Clause | Provenance | Behavior | Status |
| --- | --- | --- | --- | --- |
| `gn-proto-only-encoding` | gNMI spec §2.2.3 | encoding negotiation: not every peer offers JSON_IETF | a peer advertising only PROTO still round-trips Get; JSON_IETF is preferred when offered | covered |
| `gn-leaf-list-typed-value` | gNMI spec §2.2.3 (leaflist_val) | Arista vEOS-lab 4.33.1.1F serves /interfaces/interface/ethernet/state/supported-speeds as a leaf-list | a leaflist_val update decodes into the update's Values in wire order, an empty leaf-list included, and the scalar Value stays nil; the row codec renders those values as the JSON array a LeafList field expects, and a leaf-list written through PathValue.Values encodes back to leaflist_val | covered |
| `gn-banner-newline-normalization` | gNMI spec §3.4 (Set/Get round-trip) | Arista vEOS-lab 4.33.1.1F, lab device 2026-09-18 | a device may normalize a written leaf rather than store it verbatim; EOS appends a trailing newline to /system/config/login-banner, so a Set/Get round-trip compares modulo that normalization instead of by exact equality | covered |
| `gn-per-path-set-error` | gNMI spec §3.4.2 | deprecated UpdateResult.Message is the only per-path failure channel several implementations use | a Set response carrying a per-path error surfaces which path failed as an attribute | covered |
| `gn-stream-termination-latch` | gNMI spec §3.5 | session lifecycle: a dropped stream never resumes itself | abrupt server termination of a STREAM subscription latches Stream.Err; buffered events stay readable; ONCE completion is clean | covered |
| `gn-presync-buffering` | gNMI spec §3.5.1.4 | sync_response is the cold-start-complete signal | updates before sync_response buffer as initial state and emit as Added at sync; each post-sync notification batch emits at most one event per affected row | covered |
| `gn-subscribe-once-snapshot` | gNMI spec §3.5.1.5.1 | FlowSeer reference target (t1) | Subscribe ONCE assembles the snapshot's leaf updates into typed rows through the generated descriptor and ends cleanly at sync | covered |
| `gn-stream-sync-cold-start` | gNMI spec §3.5.1.5.2 | FlowSeer reference target (t1) | STREAM cold start emits Added per row at sync, then the target's periodic leaf change arrives as one Modified per batch | covered |
| `gn-aruba-set-capability` | gNMI spec §3.4 | Aruba CX lab capability check — no lab device serves gNMI | records that Aruba CX gNMI write capability is unverifiable in this lab, and where the config write goes instead | accepted-risk |
| `gn-t4-identity` | device identity reads | Arista vEOS-lab 4.33, lab device 2026-09-18 | hostname, software version, serial, and part number return via Get over the advertised OpenConfig models | covered |
| `gn-t4-set-verdict` | config write capability | Arista vEOS-lab 4.33, lab device 2026-09-18 | a reversible login-banner Set is verified by Get; EOS normalizes the banner with a trailing newline, so the round-trip compares modulo that normalization, and the banner is restored | covered |
| `gn-t4-stream` | interface state subscription | Arista vEOS-lab 4.33, lab device 2026-09-18 | a STREAM subscription over interface state delivers sync and keeps flowing on hardware | covered |
| `gn-t4-revision-drift` | model revision comparison | Arista vEOS-lab 4.33, lab device 2026-09-18 | advertised model versions diff against the committed lockfile; drift surfaces as warnings | covered |

