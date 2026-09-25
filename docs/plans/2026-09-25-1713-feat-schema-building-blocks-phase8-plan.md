---
title: Schema Building Blocks Phase 8, QoS, AAA, Flow Export, and Cellular - Plan
type: feat
date: 2026-09-25
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-25-1713-feat-schema-building-blocks-plan.md
---

# Schema Building Blocks Phase 8, QoS, AAA, Flow Export, and Cellular - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

The edge services a switch or gateway runs have a place: `net/qos/v1`
(trust mode, classifier terms, queues), `net/aaa/v1` (RADIUS and TACACS+
server identity), `net/flow/v1` (sFlow, NetFlow, and IPFIX export
settings), `net/cellular/v1` (radio technology, band, identifiers, signal
quality), and L2 match terms in `net/filter`.

## Decisions

The record's rules 1 and 6 govern. From dossier 07, to be confirmed in the
re-plan:

- The QoS trust mode (untrusted, CoS, DSCP) is the one QoS fact every
  vendor reports; classifier terms reuse `net/filter` match atoms rather
  than a second match type.
- Server secrets are credential material and never appear in `net/aaa`.
- Flow export is collector configuration; decoding NetFlow or IPFIX
  records is out of this phase.
- Cellular signal quality is its own message (RSRP and RSSI in milli-dBm,
  RSRQ and SINR in milli-dB); Wi-Fi keeps its own in `net/wlan`. The record
  shares units, not a message, because the two field sets overlap in RSSI
  alone. The 3GPP ranges come from a secondary source in dossier 07; the
  re-plan fetches TS 36.133 or TS 38.133 before writing validation bounds.
- WAN path quality uses `measure.v1.PathQuality` from phase 1.

## Requirements

1. A RADIUS server with no address fails.
2. A cellular signal with `rsrq_millidb = -19500` passes (RSRQ −19.5 dB).
3. A QoS classifier term that sets a DSCP of 64 fails.
4. A filter rule matching destination MAC and EtherType passes validation.
