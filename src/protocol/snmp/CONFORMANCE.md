# SNMP Conformance Coverage Map

Generated from `conformance_corpus_test.go`. Do not edit by hand —
run `UPDATE_CONFORMANCE=1 go test ./src/protocol/snmp/ -run TestConformanceMatrixUpToDate`.

**Status:** 33 covered · 3 accepted-risk · 0 pending · 36 total

## Encoding / type conformance (RFC 2576/2578/2579/3417, X.690)

| id | status | clause | provenance | behavior |
|---|---|---|---|---|
| `enc-counter64-v1` | covered | RFC 2576 §3 | gosnmp (no check) | Counter64 in a v1 response: decode + typed warning (data preserved), never panic/type-confusion |
| `enc-zerolen-int` | covered | X.690 §8.3.1 | gosnmp #241 | zero-length signed INTEGER (02 00) errors; zero-length unsigned counter tolerated as 0 (recorded leniency) |
| `enc-nonminimal-int` | covered | X.690 §8.3.2 | gosnmp #371 | encode minimal (-1 -> FF); decode tolerates non-minimal |
| `enc-maxrep-signed` | covered | RFC 3416 | gosnmp #293 | GETBULK max-repetitions 128-255 encode unsigned, not negative |
| `enc-id-range` | covered | RFC 3412 | gosnmp #272 | msgID/request-id stay in 0..2^31-1, no overflow to negative |
| `enc-ipaddr-longform` | covered | X.690 §8.1.3 | gosnmp #544 | long-form BER length on IpAddress (40 81 04 ..) decodes to the 4-byte address |
| `enc-ipaddr-8byte` | covered | RFC 2578 §7.1.5 | gosnmp #544 | IpAddress with 8 content bytes -> typed error, not panic (no sane IP) |
| `enc-opaque-unknown` | covered | RFC 2856 | gosnmp #374 | unknown Opaque sub-type (0x7a) -> raw bytes, not nil |
| `enc-opaque-zero` | covered | RFC 2856 | gosnmp #453 | Opaque float/double 0.0 -> full 4/8-byte IEEE-754, not truncated |
| `enc-unsigned-as-signed` | accepted-risk | RFC 2578 §7.1.6 | Check Point sk115119; IBM IZ77427 | INTEGER-tagged (0x02) value with MSB set decodes to a negative Integer32Var; coercion to uint32 is rejected as ErrLossyConversion (reject, not reinterpret) |
| `enc-neg-length` | covered | X.690 §8.1.3 | gosnmp #552 | length byte sign-extending to negative int64 -> no negative-index panic (named fuzz seed) |
| `enc-dateandtime-lens` | covered | RFC 2579 | snmp_exporter #321 | DateAndTime 0-byte (unknown) and 7/other-byte variants handled distinctly |
| `enc-bits-padding` | covered | RFC 2579 | RFC 2579 | BITS with trailing zero padding / short value tolerated |
| `enc-timeticks-range` | covered | RFC 2578 | RFC 2578 | TimeTicks 5-byte unsigned / out-of-range masked to 32 bits (scoped to TimeTicks) |
| `enc-v1trap-spectrap` | covered | RFC 2576 | gosnmp #182 | SNMPv1 Trap specific-trap > 127 not byte-truncated in v1->v2c translation |

## Walk / transport behavior (RFC 3416)

| id | status | clause | provenance | behavior |
|---|---|---|---|---|
| `walk-toobig-fallback` | covered | RFC 3416 | MikroTik(>50), Cisco/IOS-XR, Nokia, F5 | tooBig -> halve max-repetitions -> fall back to GETNEXT-per-OID; full table, no abort |
| `walk-mid-pdu-eomv` | covered | RFC 3416 | dense carrier tables | single-chain GETBULK: yield all preceding values, terminate at the EndOfMibView varbind (no data loss) |
| `walk-nosuch-semantics` | covered | RFC 3416 | RFC 3416 | noSuchInstance = skip & continue (advance cursor); noSuchObject on subtree root = abort that subtree; classified before the cycle guard |
| `walk-mutate-midwalk` | covered | RFC 3416 | Ruckus/Aruba/UniFi WLAN | rows added/dropped mid-walk -> self-consistent snapshot tolerating index gaps + duplicate indices, no loop/error |
| `walk-cycling-oid` | covered | RFC 3416 | gosnmp #401 Juniper; Cisco CSCuf16921 | non-increasing/cycling OIDs -> bounded skip-forward, no infinite loop/OOM; cycle distinct from single regress |
| `walk-leaf-start` | covered | RFC 3416 | gosnmp #170 | walk starting on a leaf OID returns the next OID, not nothing |
| `walk-large-value-hang` | covered | RFC 3416 | gosnmp #408 | BulkWalk must not hang on a varbind with a >1KB OctetString value |

## Transport demux (RFC 3416)

| id | status | clause | provenance | behavior |
|---|---|---|---|---|
| `txp-dup-response` | covered | RFC 3416 | gosnmp #417 | duplicate/retransmitted response dropped by request-id demux; later genuine reply still resolves |
| `txp-subtree-exit` | covered | RFC 3416 | RFC 3416 | returned OID outside requested subtree prefix -> normal (non-error) termination |

## Raw fast path fused/fallback boundary (R25, X.690 §8.19)

| id | status | clause | provenance | behavior |
|---|---|---|---|---|
| `raw-wrong-typed-column` | covered | RFC 2578 §7.1.6 | telegraf #14598; snmp_exporter #338 (proprietary/buggy agents reporting types diverging from the MIB declaration) | column value whose wire tag diverges from the MIB-declared Kind: the fused arm declines (ok=false, never an error) and the generic decoder's coercion rules apply — values and errors identical to the pre-R25 path |
| `raw-noncanonical-oid-arc` | covered | X.690 §8.19.2 | chemist/snmp #17 (agents emitting BER that is valid but not shortest-form) | response name OID carrying a zero-padded (0x80-prefixed) sub-identifier: mirror validation refuses raw delivery and the read loop decodes eagerly — the walk yields identical data via pre-decoded varbinds, and the byte-order walk guards never see a non-canonical arc |

## SNMPv3 / USM (RFC 3412/3414/3826)

| id | status | clause | provenance | behavior |
|---|---|---|---|---|
| `usm-authbit-bypass` | covered | RFC 3414 §3.2 | gosnmp #496 | auth/priv downgrade matrix: cleared auth/priv bit on a configured session -> ErrUSMDowngrade before HMAC; Report authNoPriv-on-authPriv allowed |
| `usm-report-randomid` | covered | RFC 3414 §4 | gosnmp #139 | unsolicited/mismatched-id Report dropped-and-counted, never aborts a waiter; genuine reply still resolves |
| `usm-aes-keyext` | covered | RFC 3826; draft-reeder | gosnmp #424; Cisco/Extreme | AES-192/256 Blumenthal vs Reeder (C) key-extension vectors; distinct AES192 vs AES192C keys (unit-level oracle; no net-snmp cell) |
| `usm-keycache-passphrase` | accepted-risk | RFC 3414 §2.6 | gosnmp #424 | no passphrase-keyed cache exists; keys derived per (engineID,user) from passphrase material |
| `usm-3step-discovery` | covered | RFC 3414 §4 | gosnmp #511 | initial discovery performs the authenticated boots/time resync before the first real request |
| `usm-trap-reportable` | covered | RFC 3412 §6.4 | gosnmp #391 | reportable-flag handling correct on received v3 traps vs informs |
| `usm-timewindow-rollback` | covered | RFC 3414 §2.2.3 | deepening (security-lens) | polling-side engineBaseline.update rejects a boots/time pair that would decrease boots or move time backward |
| `usm-msgid-predictability` | covered | RFC 3412 | deepening (security-lens) | msgID drawn fresh from the CSPRNG per message (not last+1), removing prediction in the unauthenticated window |
| `usm-inform-timewindow` | covered | RFC 3414 §3.2 | deepening (security-lens) | authoritative-role authoritativeTimeOK enforces the ±150s engineTime window + post-restart quarantine; int32 boundary safe (int64 diff) |
| `usm-authoritative-boots-pinned` | accepted-risk | RFC 3414 §3.2 | deepening (security-lens) | authoritativeBoots pinned to 2^31-1 -> §3.2 boots-sequence check disabled for the listener role; quarantine reduces but does not close cross-restart inform replay |

## Accepted-risk allowlist

A row may carry `accepted-risk` status only if it appears here (a reviewable diff).

- `enc-unsigned-as-signed`
- `usm-authoritative-boots-pinned`
- `usm-keycache-passphrase`
