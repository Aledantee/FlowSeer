---
title: Device Service and Inventory - Direction
type: direction
date: 2026-08-20
topic: device-service-and-inventory
status: accepted-direction
---

# Device Service and Inventory - Direction

The overall direction for how FlowSeer talks to devices, discovers them, ingests
from them, and models where they are. This is a direction record, not a plan:
it fixes the shape and the rules so later brainstorms and plans build toward one
target instead of re-deciding it. It was formed on 2026-08-20 and grounded
against the repository and external sources (see Sources).

## Decision in one paragraph

FlowSeer gets a **central device service** — a ConnectRPC/gRPC service with
fully FlowSeer-owned, typed protobuf definitions — that abstracts *devices and
their capabilities*, not protocols. It serves **live, request/response
interactions** (reads-now and configuration writes). It **routes** each request
to the **executor** that can reach the device — usually an **edge agent** inside
the tenant's network running the protocol libraries (SNMP, NETCONF, RESTCONF,
gNMI), or a cloud/controller adapter — using a central **inventory**. Device
**discovery** and **async ingestion** (traps, webhooks, pollers) are separate
planes that feed that inventory; they are not features of the device service.
Inventory is modelled as five entities with one lifecycle each: Device,
ManagementDomain, Binding, Placement, Executor.

## Why this and not the alternatives

- **A protocol-transparent session facade** (`Get(path)`/`Set(path)` over SNMP,
  NETCONF, RESTCONF, gNMI) is rejected; this confirms the YANG plan's KD4. The
  protocols diverge exactly where it matters (candidate/commit vs. immediate
  PATCH vs. gNMI Set; OIDs vs. YANG paths) and a common session either lies
  about transaction semantics or re-exposes every protocol through escape
  hatches. The device service is the "thin facade later, when a real caller
  wants one" that KD4 allowed for — but at the *device/capability* level, which
  is a different abstraction.
- **A library-level device facade in `src/common/`** is rejected. Making the
  abstraction a service with owned protos keeps its surface a domain model we
  control, puts the protocol libraries behind a process boundary, and reuses the
  decisions already on the books (Connect as the web↔Go transport, the `errs`
  package's client-facing payload, the edge/backend layout in
  `docs/code-style.md`).
- **A separate "cloud facade"** for Meraki/RUCKUS One-style platforms is
  rejected. Cloud platforms and on-prem controllers (SmartZone, UniFi, LMC) are
  one category — controller-mediated management — differing in latency, rate
  limits, and tenant-scoped credentials. Splitting the model along the vendor's
  deployment choice instead of along what the operator is doing would fork it.
- **Streaming as the primary API shape** is rejected for the device service.
  Live events arrive through ingestion (traps, webhooks, telemetry, polling);
  the device service is for "ask this device now". Streaming may appear as an
  implementation detail of a long-running RPC, never as the shape of the API.

## The three planes

| | Discovery | Device service (live facade) | Ingestion |
|---|---|---|---|
| Target | Address / range / neighbor table, no known identity | A `DeviceRef` with verified bindings (or an explicit address for promotion) | Whatever arrives, plus scheduled targets |
| Trigger | Job, or one-shot "scan this" | A user or workflow, now | Continuous |
| Shape | Job producing candidates | Unary request/response | Streams, pollers, receivers |
| Result | Candidates (sighting + fingerprint) | Typed State / Apply result, fresh from the wire | State/Event updates |
| Credentials | Tries profiles speculatively | The binding's known credential | The binding's known credential |
| Failure | Normal (most addresses aren't devices) | Meaningful (the device should have answered) | Degradation, Fallback |
| Side effects | None, by rule | Writes allowed; the only place they happen | None |
| Tenant | From the collector doing the scanning | From the device record | From the binding |

**Inventory is the spine** all three read and write: discovery appends
candidates; the device service promotes candidates to devices, records and
verifies bindings, and performs writes; ingestion updates State and binding
health. Each plane's failure modes stay local: a noisy sweep cannot degrade a
live read, a flapping trap stream cannot block a config apply, a cloud rate
limit is a domain budget all three draw from with the facade taking priority
because a human is waiting.

Discovery sources, in order of signal per unit of noise: operator seeds (IP or
CIDR + credential profiles); neighbor crawl from known devices (LLDP/CDP — best
signal, gives topology for free, makes one seed per site usually enough); tables
on known devices (ARP/ND, FDB, DHCP leases — broad, noisy, OUI-prefiltered);
active sweep of a range (opt-in per range, rate-limited, never silent).
Candidates are promoted through the device service's probe/identity call; the
first action on a newly enrolled standalone device is, via the device service,
pointing its traps/syslog/telemetry at the collector. Discovery finds the silent
device, the facade makes it talk, ingestion takes over.

## Central routes, edge executes

```
web / workflows
      │  Connect (typed device API; OpenFGA-guarded)
      ▼
central device service ── inventory: devices, bindings, candidates, domain budgets
      │ routes per binding
      ├──► edge agent A ── runs protocol libraries against direct bindings
      ├──► edge agent B
      └──► cloud/controller adapters ── placed by policy: central for cloud APIs,
                                         edge for on-prem controllers
```

- A directly bound device is reachable only from inside the tenant's network,
  so the wire call runs on the edge agent that owns the binding. The central
  service does `DeviceRef → binding → executor → dispatch` and relays the typed
  result. Routing reads only Binding and Executor, never domain kind.
- The edge dials out, never in (sites are NAT'd); the central service sends
  work down a persistent Go↔Go connection. The edge receives what a request
  needs (or a scoped copy), never the tenant's credential store.
- The edge implements the same device-API protos the central service exposes;
  central adds auth, tenancy, budgets, and the inventory lookup. One model,
  testable at both hops.
- Edge offline means bindings unavailable, surfaced as a typed retryable
  "executor unreachable" — not device failure. Last known State from ingestion
  remains.

## The inventory model

Five entities, each changing for exactly one reason. Relationships are refs,
never embedding.

| Entity | Means | Changes when |
|---|---|---|
| **Device** | This physical box. Identity = serial (+ base MAC). | The box changes. |
| **ManagementDomain** | A controller or cloud tenant (vSZ, Meraki org, RUCKUS One tenant, LMC, UniFi controller). Kind, credentials, region, request budget. | A domain is added/retired. Independent of any device. |
| **Binding** | "Device X is reachable via Y": *direct* (endpoint, protocols, executor) or *mediated* (domain + platform-side id). Many per device. Status CANDIDATE / VERIFIED / DEGRADED / RETIRED. | Reachability changes; moves between controllers. |
| **Placement** | "Device X belongs to tenant/site/zone Z" from a point in time. Source OPERATOR or DERIVED_FROM_DOMAIN; operator wins. | Moves between sites. Append-only. |
| **Executor** | An edge agent, or the central cloud-adapter host. Bindings name their executor. | An agent is enrolled/retired. Orphans bindings, not devices. |

What the expected churn becomes:

- *Add a controller or cloud tenant:* create a ManagementDomain; its adapter's
  inventory listing emits candidates; correlation by serial merges onto
  existing Devices (adding a mediated Binding) or creates new ones.
- *Remove it:* retire the domain; its bindings retire. Devices with another
  binding keep working; those with none stay known-but-unreachable with their
  history and placement. Re-adding rebinds.
- *Move a device between controllers:* old binding retired, new one created.
- *Move between sites:* new Placement row; old one closes.
- *Add a new platform kind:* a new `kind` arm plus an adapter; no change to
  Device, Binding, or Placement.

Identity correlation: a new sighting is a *candidate* until an identity read
through it yields a serial that matches (merge) or does not (new device). IP,
hostname, and platform id are binding data and never identity; they move.

Physical/topological location is State with provenance, not resolution: site
from the domain hierarchy or operator assignment; topological place from the
LLDP neighbor data ingestion already collects; physical place (`sysLocation`,
platform address fields) stored, never trusted for routing.

## Rules the contract must keep

1. **Capabilities, never protocols.** The API vocabulary is entities and verbs
   (`GetIdentity`, `GetInterfaces`, `ApplyVlanConfig`). The backing protocol and
   model appear only as diagnostic provenance. No `RawGet(path)` ever lands in
   this service; raw access, if needed, is a separate diagnostics service.
2. **Capabilities are discovered per binding and exposed as a queryable
   matrix** — not declared per vendor kind, not assumed uniform. Core messages
   stay tight; vendor enrichments live in per-vendor extension messages. This
   is the rule that keeps the service from re-enacting NAPALM's
   lowest-common-denominator failure in protobuf.
3. **Freshness is explicit.** Every live response carries `observed_at` and
   which binding answered. A cloud-mediated read is "live as the platform sees
   it", and says so.
4. **Writes carry their guarantee level.** `Apply*` supports `validate_only`;
   the result states atomicity (transactional / best-effort-verified /
   accepted-by-platform-converging) and a read-back diff. Nobody is lied to;
   nothing pretends to be atomic that is not.
5. **Edition-2024 presence distinguishes unsupported from zero.** Unset means
   "this device does not provide it".
6. **Errors cross the boundary through `errs`:** codes → Connect codes, Retry
   Disposition → code choice, User Message/Hint and Public Attributes as the
   client detail, internal message to the log.
7. **Nothing cascade-deletes.** Retire, don't remove; correlation by serial
   makes reappearance a merge. Binding and Placement changes are events; current
   state is a projection.
8. **Per-domain request budgets are a facade concern.** Cloud APIs are
   rate-limited per org *and* per source IP; discovery, facade, and pollers draw
   from one budget with the facade first.

## Sequencing

1. **Land the owned proto foundation first.** `spec/proto/flowseer/` is
   unpopulated in this repository and there is no `docs/conventions/`; the
   entity conventions this model assumes (per-entity Config/State/Event
   families, Local/Global refs, device refs) must exist before the inventory
   protos can be written against them.
2. **Write the device-API contract as a spec now** — capability matrix as a
   hard rule, binding/executor routing, error mapping, per-domain budgets,
   guarantee levels — so the protocol-library work knows what it is feeding.
3. **Implement after the first protocol library is real and the R11 identity
   read has been done by hand once.** That is the "real caller" KD4 asked for;
   building the facade before a single candidate/commit path exists would
   encode guesses into a contract that is costly to bend.
4. **v1 scope:** Device, ManagementDomain, Binding, Placement, Executor as
   separate entities (history is cheap to keep, expensive to retrofit), but
   only Device and Binding need their own RPC surface in v1. v1 capabilities are
   chosen bottom-up from what the vendored fleet can deliver broadly: identity,
   interfaces, neighbors/LLDP, VLANs, trap/telemetry destination config.
5. **Grow by adding entity files and capability arms**, never by revving the
   service.

## Open questions

- Meraki single (non-batched) config writes: whether dashboard acceptance
  precedes device application is plausible but unverified; only action-batch
  sync/async semantics were confirmed. Affects the converging guarantee level.
- RUCKUS One numeric rate limits — not yet retrieved.
- Whether trap/telemetry destination configuration is the facade's (it is a
  write on a device — current position: yes) or the ingestion plane's.
- Credential handling between central and edge: scoped copy vs. per-request
  delivery.

## Sources

- Repository: `docs/code-style.md` §Project layout (`src/backend`, `src/edge`
  named); `docs/plans/2026-08-17-2254-refactor-internal-errs-package-plan.md`
  (edge/backend split, Connect RPC, cross-boundary errors); `src/common/errs/doc.go`
  (wire payload: code, safe attributes, user message, hint, retry disposition);
  `src/common/snmp/doc.go` (Collection Primitives lifecycle contract);
  `CONCEPTS.md` §Flagged ambiguities (no backend/driver abstraction);
  `docs/plans/2026-08-20-1245-feat-yang-protocol-libraries-plan.md` on branch
  `worktree-brainstorm-netconf-restconf` (KD4, KD7, R10, R11, KTD4, KTD5);
  `spec/openapi/README.md`, `spec/yang/README.md` (which vendors are
  controller/cloud-mediated).
- External: Cisco Meraki Dashboard API — rate limits (10 req/s per org, 100
  req/s per source IP), Organization→Network→Device with serial-keyed devices,
  webhooks, action batches; Meraki SNMP overview (local polling alongside
  dashboard SNMP); RUCKUS One API docs (JWT, `api.eu.ruckus.cloud`, async
  `requestId`, webhooks); Nautobot `Controller` /
  `ControllerManagedDeviceGroup` data model (capabilities field, 2.4);
  NAPALM multi-vendor critique (karneliuk.com, 2021); ConnectRPC FAQ
  (browser clients: server streaming only).
