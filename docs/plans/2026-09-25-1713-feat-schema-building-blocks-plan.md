---
title: Schema Building Blocks - Plan
type: feat
date: 2026-09-25
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
amends: docs/architecture/2026-08-20-network-model-structure-direction.md
---

# Schema Building Blocks - Plan

## Goal

FlowSeer's protobuf schema gains the building blocks for every network
domain the atlas maps and the targeted device classes use: wireless and RF,
endpoints, platform and system, network instances and routing, L2 and L3
protocols, QoS, AAA, flow export, and cellular. All of it is written in one
schema language: one canonical unit per quantity, one key rule per key, one
network-instance key, and one naming rule for facets and table rows.
Existing packages are reshaped to that language where they deviate. The
means is eight phases, each a phase plan: phase 1 lays the shared leaves and
the language, and the rest add one domain each. The package layout and the
rules are the accepted
[schema building blocks record](../architecture/2026-09-25-schema-building-blocks-direction.md);
the evidence is [`docs/research/schema-building-blocks/`](../research/schema-building-blocks/README.md).

This plan is wrong if a canonical unit in the record's table cannot hold a
value a targeted provider reports (a precision it has and the unit loses, or
a range it overflows). A unit must not change between phases, so that
discovery re-cuts the table in the record before any later phase lands.

## Decisions

- The package layout, the unit table, the key rules, the network-instance
  key, the facet and row naming, the protocol-table rule, and the entity
  list are the record's. Why: they constrain every later phase and every
  future package, so they pass the promotion test; the record states each
  reason and the rejected alternatives.
- The phases run in the order the record's dependencies force and no
  other: phase 1 first, because every other phase imports `net/key` or
  `net/measure`; routing after network instances; endpoints after wireless,
  because the wireless attachment embeds `net/wlan` values; platform after
  wireless, because both edit `model/inventory/v1/component.proto`.
- Phase 1 writes the whole layering allowlist and both `net/` package
  README lists for the record's tree, marking packages not yet present.
  Why: every phase would otherwise edit the same map in
  `test/conformance/proto/layering_test.go` and the same list, so no two
  phases could run in parallel. A later phase edits only its own row and
  removes its own marker.
- A phase that adds or reshapes a message updates its Go consumers
  (`src/common/netsim/vswitch/netmodel`, `src/modules/localnet/snmpmap`,
  `src/modules/localnet/access`) in the same change. Why: the breaking-change
  rule in `AGENTS.md`, and `generated/` must match `spec/proto/` in every
  commit (`docs/code-style-proto.md`, Workflow).
- The user chose all domains, primitives and entities, and a required
  network-instance key (2026-09-25).

## Requirements

1. After the last phase, every package the record's tree marks new exists
   with a README, and `buf lint` passes. Example: `ls
   spec/proto/flowseer/net/wlan/v1/` lists `.proto` files and `README.md`.
2. No `float` or `double` field exists under `spec/proto/flowseer/` other
   than `Location.latitude`, `Location.longitude`, and the attribute decimal
   value. Example: a `float loss_percent` in `net/measure` fails
   `TestNoFloatingPointFields`.
3. No numeric field name ends in a non-canonical unit suffix. Example:
   `uint32 power_draw_milliwatts` fails `TestCanonicalUnitSuffixes`;
   `uint64 power_draw_nanowatts` passes.
4. Every message named `*Counters` has a `google.protobuf.Timestamp
   last_discontinuity` field. Example: `EthernetCounters` without it fails
   `TestCountersCarryDiscontinuity`.
5. Every string field whose name spells an interface name (`interface_name`,
   a `_interface_name` suffix, or its plural) or is `network_instance`
   carries the matching `net/key` predefined rule, and no interface name
   is spelled any other way (`Subinterface.parent` becomes
   `parent_interface_name`). Example: an `interface_name` with
   only `string.max_len = 64` fails `TestKeyFieldsUseKeyRules`.
6. `Vlan`, `FdbEntry`, and `Route` require `network_instance`, and `IpFacet`
   requires it. Example: an `FdbEntry` without it fails validation with
   `network_instance: value is required`.
7. The Endpoint, Wlan, and Alarm families pass the repository proto hook's
   triad and ref checks, with each deliberately absent member named in the
   family file's file-level comment.
8. `go build ./...` and `go test -race ./...` pass after each phase.

## Out of scope

- DSL, PON, DOCSIS, Fibre Channel, MPLS, EVPN/VXLAN, MACsec, ring
  protection, and tunnel packages (the record says why).
- Mappers that fill the new messages from a live source, beyond keeping
  today's mappers compiling and correct under the reshaped messages.
- Services and APIs that serve the new entities; `EntityType` admission for
  them (the record defers it to their stores).
- The protocol-blind projections (what is on a port, which gateway owns an
  address).

## Units

### U1. Phase 1: shared leaves, the schema language, and reshaping what exists

Files: docs/plans/2026-09-25-1713-feat-schema-building-blocks-phase1-plan.md
After: none
Landed: `68a18e63..ed9f129e`

### U2. Phase 2: network instances and routing

Files: docs/plans/2026-09-25-1713-feat-schema-building-blocks-phase2-plan.md
After: U1
Landed: `683c171f..840398c1`

### U3. Phase 3: wireless and RF

Files: docs/plans/2026-09-25-1713-feat-schema-building-blocks-phase3-plan.md
After: U1
Landed: `2844b9fb..f4259ff5`

### U4. Phase 4: endpoints and port access

Files: docs/plans/2026-09-25-1713-feat-schema-building-blocks-phase4-plan.md
After: U3
Landed: `f8374c58..83ddc89b`

### U5. Phase 5: platform, system, and operations

Files: docs/plans/2026-09-25-1713-feat-schema-building-blocks-phase5-plan.md
After: U3
Landed: `fec49dc1..bc5d052f`

### U6. Phase 6: L2 protocols

Files: docs/plans/2026-09-25-1713-feat-schema-building-blocks-phase6-plan.md
After: U1
Landed: `d6548fcf..a6e39988`

### U7. Phase 7: L3 protocols and IP services

Files: docs/plans/2026-09-25-1713-feat-schema-building-blocks-phase7-plan.md
After: U2
Landed: `e6802376..8d53dbc6`

### U8. Phase 8: QoS, AAA, flow export, and cellular

Files: docs/plans/2026-09-25-1713-feat-schema-building-blocks-phase8-plan.md
After: U1
Landed: `3466848d..0920748a`

Waves: U1 | U2 U3 U6 U8 | U4 U5 U7

Phases in one wave touch disjoint packages. Each also edits `CONCEPTS.md`,
one line in a `net/` README, and its own row in the layering allowlist.
Those edits sit on separate lines and merge without conflict.

## Verification

```bash
buf lint
buf generate && git status --porcelain generated/   # empty after the commit
go build ./... && go vet ./...
go test -race ./test/conformance/... ./src/...
.claude/skills/verify-change/scripts/verify-change.sh --full
```

After the last phase, the record's tree matches
`find spec/proto/flowseer -name README.md -path '*/v1/*'`, and the four
schema-language tests from phase 1 pass over every package.

## Definition of done

- [ ] Each phase plan reads `implemented` and its `Landed:` line above
      carries the commit range.
- [ ] Verifier green with `--full` after each phase.
- [ ] The schema building blocks record (accepted 2026-09-25) is amended
      wherever a phase found it wrong.
- [ ] `docs/conventions/protobuf.md`, `docs/code-style-proto.md`,
      `CONCEPTS.md`, and every touched package README match the tree.
- [ ] This plan's `status` is `implemented` with an outcome note under the
      title, and no plan label appears in code or commit messages.

## Open questions

- Whether `net/system` is the right name for resource utilization, images,
  and licenses. The atlas guessed `net/system`; the platform dossier
  proposed `net/env` for sensors, which the record folds into
  `net/measure`. Phase 5 confirms or renames before it lands, and amends
  the record if it renames.
- Whether HSRP gets a package in phase 7. It is Cisco-only; the record's
  rule gives it its own package if it comes.
  Closed in phase 7: HSRP is Cisco-only and will be introduced in its own
  package under `net/protocol/hsrp` in a future phase if needed; VRRP
  models standard first-hop redundancy.
- Which source each string bound outside `net/` comes from. The record's
  rule is that a bound names its source; 142 bounds in `model/`, `api/`,
  `edge/`, `store/`, and `runtime/` use ten different values, several with
  real sources (PEM blocks, URLs). No phase here audits them; a separate
  plan does, and until it lands a new field follows the rule and an
  existing one keeps its bound.
- Whether an Endpoint's randomized-MAC merge policy needs any schema
  support beyond a repeated observed-address list. Phase 4 decides; the
  record says the policy itself is a service concern.
