---
title: Schema Building Blocks Phase 3, Wireless and RF - Plan
type: feat
date: 2026-09-25
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-25-1713-feat-schema-building-blocks-plan.md
---

# Schema Building Blocks Phase 3, Wireless and RF - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

FlowSeer can hold what a controller or cloud API reports about radios,
BSSs, and the RF environment: `net/wlan/v1` primitives, a radio component
kind whose state carries a radio facet, and the `Wlan` entity for the
configured wireless network in `model/wireless/v1`.

## Decisions

The record's entity list (radio as a component, Wlan top-level with a full
triad), rule 1 (MHz, milli-dBm, basis points), and rule 8 (SSID as bytes)
govern. From dossiers 01, 02, and 09, to be confirmed in the re-plan:

- `WifiBand` (2.4, 5, 6, 60 GHz) and `Dot11Standard` (a, b, g, n, ac, ax,
  be) are normalized enums; Wi-Fi generation names are a display concern.
  900 MHz S1G is left out: no targeted provider reports it.
- A radio facet carries band, primary channel, the 80+80 secondary channel,
  width in MHz, the operating class as a validated `uint32` (6 GHz reuses
  2.4 GHz channel numbers, and the band field disambiguates them; the
  class is for 802.11k/v candidate lists), tx power, max tx power, EIRP,
  antenna gain, noise floor, admin and oper status (reusing
  `net/interface` status enums would import a higher layer; the re-plan
  decides between that import and radio-local enums), and a channel
  utilization breakdown in basis points.
- `WlanSecurity` is a normalized enum grounded in the WPA3 specification
  v3.5 (dossier 09 §4): open, OWE, WPA2-Personal, WPA3-Personal,
  WPA3-Personal transition, WPA2-Enterprise, WPA3-Enterprise,
  WPA3-Enterprise-192, WEP. OWE transition is a pair of BSSs, not a mode of
  one. Raw AKM and cipher suite pass-through enums wait for a producer that
  reports suite selectors.
- PMF is a three-state enum (disabled, optional, required).
- Neighbor-scan and rogue rows are a device table with a classification
  enum; first- and last-seen times are not on the primitive (the record's
  time rule), they belong to the entity or event that stores sightings.
- Country is `country_code` (ISO 3166-1 alpha-2) plus the 802.11d
  environment octet as its own enum.

## Requirements

1. A `Bss` with an SSID of 33 octets fails; one with the non-UTF-8 octets
   `0xff 0xfe` passes.
2. A radio facet with `channel_width_mhz = 320` passes and `60` fails.
3. A Ruckus `ap_status.proto` radio report maps to a radio facet in a
   fixture test with its tx power in milli-dBm.
4. `WlanConfig`, `WlanState`, and `WlanEvent` pass the proto hook's family
   check.
