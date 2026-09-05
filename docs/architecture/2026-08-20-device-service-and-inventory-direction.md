---
title: Device Service, Integrations, and Inventory - Direction
type: direction
date: 2026-08-20
updated: 2026-09-03
topic: device-service-and-inventory
status: accepted-direction
---

# Device Service, Integrations, and Inventory - Direction

The overall direction for how FlowSeer talks to devices, discovers them, ingests
from them, models where they are, and attaches the adapters that do the work.
This is a direction record, not a plan: it fixes the shape and the rules so
later brainstorms and plans build toward one target instead of re-deciding it.
Formed and grounded on 2026-08-20 against the repository and external sources
(see Sources); revised the same day to fold in the integration model, the
transport fabric, and the edge attachment decisions.

## Decision in one paragraph

FlowSeer gets a **central device service** — a ConnectRPC service with fully
FlowSeer-owned, typed protobuf definitions — that abstracts *devices and their
capabilities*, not protocols, and serves **live, request/response
interactions** (reads-now and configuration writes). Everything that reaches a
device does so through an **integration**: an adapter *kind* (code) configured
as an *instance* (data) — "this Meraki org", "this vSZ", **"this site's edge
agent"**. The edge agent is itself an integration of kind *local network*,
running the protocol libraries (SNMP, NETCONF, RESTCONF, gNMI) and discovering
devices locally. Integrations attach over one contract — announce, execute,
events — carried by **NATS** as the integration fabric (edge agents connect
directly via an embedded leaf node), with Connect kept for web↔central,
enrollment, and an optional HTTPS attach. **Discovery** and **async ingestion**
(traps, webhooks, pollers) are separate planes that feed a central
**inventory** modelled as four entities with one lifecycle each: Device,
Integration, Binding, Placement.

## Why this and not the alternatives

- **A protocol-transparent session facade** (`Get(path)`/`Set(path)` over SNMP,
  NETCONF, RESTCONF, gNMI) is rejected; this confirms the YANG plan's KD4. The
  protocols diverge exactly where it matters (candidate/commit vs. immediate
  PATCH vs. gNMI Set; OIDs vs. YANG paths) and a common session either lies
  about transaction semantics or re-exposes every protocol through escape
  hatches. NAPALM's documented limit — normalized getters cap at the common
  subset and everything else drops to vendor-specific paths — is the concrete
  failure mode. The device service is the "thin facade later, when a real
  caller wants one" that KD4 allowed for, at the *device/capability* level.
- **A library-level device facade in `src/common/`** is rejected. A service
  with owned protos keeps the surface a domain model we control, puts the
  protocol libraries behind a process boundary, and reuses decisions already on
  the books (Connect as the web↔Go transport, the `errs` package's
  client-facing payload, the edge/backend layout in `docs/code-style.md`).
- **A separate "cloud facade"** for Meraki/RUCKUS One-style platforms is
  rejected. Cloud platforms, on-prem controllers (SmartZone, UniFi, LMC), and
  the edge agent's own local network are one category — an integration —
  differing in reach, latency, rate limits, and credential scope. Nautobot's
  `Controller`/`ControllerManagedDeviceGroup` model is the precedent for
  separating device identity from the thing that manages it.
- **Streaming as the primary API shape** is rejected for the device service.
  Live events arrive through ingestion; the device service is for "ask this
  device now". Streaming may appear as an implementation detail of a
  long-running RPC, never as the shape of the API.
- **In-process plugins** (Go `plugin`) are rejected: same toolchain, build
  tags and flags required across host and plugin, Linux/FreeBSD/macOS only, a
  plugin "cannot be closed". HashiCorp's `go-plugin`, the mature Go
  alternative, is an out-of-process gRPC subprocess protocol documented as
  local-network-only — i.e. a second RPC contract next to the one FlowSeer
  already has. Out-of-process integrations speak the integration contract
  instead.
- **A generic "REST connector" kind** (base URL + JSON mapping) is rejected. It
  moves the adapter into untyped config where it cannot be tested, versioned,
  or given capabilities honestly.
- **Routing live requests through central over a Connect stream** (central
  proxies edge traffic onto the fabric) is rejected for steady state. It puts
  central back in the data path, needs a registry of which replica holds which
  edge's stream (Teleport's reverse-tunnel model does exactly this), and makes
  central the availability gate for ingestion. Direct broker attachment
  removes all three.

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
| Tenant | From the integration doing the scanning | From the device record | From the binding |

**Inventory is the spine** all three read and write: discovery appends
candidates; the device service promotes candidates to devices, records and
verifies bindings, and performs writes; ingestion updates State and binding
health. Each plane's failure modes stay local: a noisy sweep cannot degrade a
live read, a flapping trap stream cannot block a config apply, a cloud rate
limit is an integration budget all three draw from with the facade taking
priority because a human is waiting.

Every integration kind has a discovery step — for Meraki it is the org
inventory, for the local-network kind it is, in order of signal per unit of
noise: operator seeds (IP or CIDR + credential profiles); neighbor crawl from
known devices (LLDP/CDP via SNMP — best signal, gives topology for free, and
is how Auvik and LibreNMS build theirs); tables on known devices (ARP/ND, FDB,
DHCP leases — broad, noisy, OUI-prefiltered); active sweep of a range (opt-in
per range, rate-limited, never silent — PRTG's ICMP→ARP→SNMP sweep is the
common shape). All emit candidates on the same event channel. Candidates are
promoted through the device service's probe/identity call; the first action on
a newly enrolled standalone device is, via the device service, pointing its
traps/syslog/telemetry at the collector. Discovery finds the silent device, the
facade makes it talk, ingestion takes over.

## Integrations: kinds are code, instances are data

An **integration kind** is an adapter compiled into a FlowSeer binary (central
or edge) or shipped as a separate process. Kinds ship with FlowSeer for
Meraki, RUCKUS One, SmartZone/vSZ, UniFi, LANCOM LMC, and the local network;
adding a kind is development work and is never pretended otherwise. An
**integration** (instance) is a runtime row an operator creates: this Meraki
org with this API key in the EU region for tenant X; this edge agent at site
Y with these seed ranges.

Kinds **describe themselves at runtime**. Each answers `ListIntegrationKinds`
with: display name, the typed config message it takes, credential shape (API
key / OAuth client / user+password+TLS / enrollment token), where it can run
(cloud API → central by default; on-prem controller or local network → an edge
process inside the network), webhook support, and capability *hints*. Actual
capabilities are still discovered per binding.

Typing has two tiers and no third:

- **First-party kinds** are compiled `oneof` arms of `IntegrationConfig`
  (`MerakiConfig`, `RuckusOneConfig`, `SmartZoneConfig`, `UnifiConfig`,
  `LmcConfig`, `LocalNetworkConfig`). The web renders the "add" form from the
  typed message. Adding a kind = new arm + adapter + form.
- **Third-party kinds** advertise the `FileDescriptorSet` of their config
  message when they announce. Central builds the type from the descriptor
  (`protodesc.NewFiles` + `dynamicpb`; protovalidate works on dynamic messages)
  and the web renders the form from the same descriptor (protobuf-es
  `createFileRegistry`). Typed at runtime, never free-form. Terraform's
  `GetProviderSchema` is the precedent; Telegraf's execd ("no discoverability")
  is the counter-example.

**Lifecycle** — adding an integration is a state machine, not an insert:

```
Create(config, credential)        → CANDIDATE
  credential → secret store; inventory holds only a credential_ref
  host chosen: central for cloud kinds; for on-prem/local kinds the operator
  picks an edge process (or the system probes reachability from the tenant's edges)
Verify                            → VERIFIED | FAILED
  adapter authenticates and reads the platform's own identity
  (org id/name, region, controller version) into IntegrationState
Initial sync                      → bindings and scopes appear
  ListDevices → candidates → serial correlation → bindings
  ListScopes  → the platform's hierarchy (Meraki network, vSZ zone, UniFi site, a site's subnets)
  RegisterWebhooks(receiver URL, secret) where supported; pollers scheduled under the budget
Retire                            → deregister webhooks, stop pollers, retire bindings,
                                    revoke the secret, keep the row and its history
```

Operators enter what comparable products ask for — Auvik's and Datadog's
Meraki integrations take an API key plus org selection (and an IP allow-list
where the platform restricts API access by source IP) — so the add-form is
familiar.

An integration may reference the **Device that hosts it** (a vSZ appliance is
SNMP-pollable in its own right): the appliance's health is ordinary device
State; the integration is only its management role.

The **plugin boundary is the integration contract.** Announce (kind, version,
config descriptor, capabilities, tenant scope, health), execute (the device
service's typed device/scope RPCs), events (candidates, state updates,
traps/webhooks). Where adapter code lives — compiled into central, into the
edge binary, or a third-party process — is deployment, not architecture.

## The inventory model

Four entities, each changing for exactly one reason. Relationships are refs,
never embedding.

| Entity | Means | Changes when |
|---|---|---|
| **Device** | This physical box. Identity = serial (+ base MAC). | The box changes. |
| **Integration** | A configured adapter instance: cloud tenant, controller, or a site's edge agent. Kind, config, credential ref, region, request budget, host process. | Added/verified/retired; host moves. Independent of any device. |
| **Binding** | "Device X is reachable via integration Y at integration-local address Z" (platform id, or IP:port+protocol for the local kind). Many per device. Status CANDIDATE / VERIFIED / DEGRADED / RETIRED, verified capabilities, last success. | Reachability changes; moves between controllers. |
| **Placement** | "Device X belongs to tenant/site/zone Z" from a point in time. Source OPERATOR or DERIVED_FROM_SCOPE; operator wins. | Moves between sites. Append-only. |

Supporting rows: **IntegrationScope** `(integration, platform_scope_id,
kind-specific type label, name, parent)` — the platform's own hierarchy,
modelled generically; Placement can derive from a scope, and scope-level
capabilities (an SSID on a Meraki *network*) target a `ScopeRef` rather than a
device. The **host process** (an edge agent process, or the central
adapter host) is routing/health metadata on the integration, not an entity of
its own.

Every binding is mediated by an integration; "direct" vs "mediated" is a
property of the kind, not a second binding shape. Routing is therefore
uniform: `DeviceRef → binding → integration → host → dispatch`, and it never
reads the kind.

What the expected churn becomes:

- *Add a controller or cloud tenant:* create an Integration; its discovery
  step emits candidates; correlation by serial merges onto existing Devices
  (adding a Binding) or creates new ones.
- *Remove it:* retire the integration; its bindings retire. Devices with
  another binding keep working; those with none stay known-but-unreachable with
  history and placement. Re-adding rebinds.
- *Move a device between controllers:* old binding retired, new one created.
- *Move between sites:* new Placement row; old one closes.
- *Add a new platform kind:* new `oneof` arm (or an advertised descriptor) plus
  an adapter; no change to Device, Binding, or Placement.
- *Enroll an edge agent:* create a local-network Integration — same lifecycle
  as a Meraki org, with seeds/ranges as its config and an enrollment token as
  its credential.

Identity correlation: a new sighting is a *candidate* until an identity read
through it yields a serial that matches (merge) or does not (new device). IP,
hostname, and platform id are binding data and never identity; they move.

Physical/topological location is State with provenance, not resolution: site
from a scope or operator assignment; topological place from the LLDP neighbor
data ingestion already collects; physical place (`sysLocation`, platform
address fields) stored, never trusted for routing.

### Offline is not removed

The wire cannot tell a powered-off device from a decommissioned one, so the
model never infers "removed". Reachability and lifecycle are two axes, and the
same word never appears on both:

- **Binding reachability** (machine-owned, per path): `VERIFIED → DEGRADED →
  UNREACHABLE`, with `last_success`, `unreachable_since`, and the failure kind
  (timeout, auth failure, ICMP-alive-but-management-dead — they mean different
  things). This is "offline". It flaps and heals on its own; nobody acts.
- **Device lifecycle** (operator-owned): `ACTIVE → MISSING → RETIRED`.
  `MISSING` is the only system-set state and is a *suspicion* about our
  knowledge — every binding unreachable past the tenant's threshold — not a
  claim about the box; it may be running elsewhere. `RETIRED` is the only state
  that means "removed" and is set by a person or an explicit policy. Retired
  devices keep history, bindings, and placements; reappearance un-retires with
  a note and never creates a duplicate. The word is "missing" rather than
  "offline" because the state asks someone to look, whereas "offline" promises
  it will come back by itself.

Evidence the local-network kind gathers to sharpen the suspicion, all from
data ingestion already collects: the device's LLDP neighbor entry vanished from
its uplink switch and that port is now down (unplugged); the same port now
shows a different chassis ID (replaced, plus a new candidate); its MAC is gone
from FDB/ARP on the segment; its serial appeared via another integration or
site (a *move* — merge, new Placement, old binding retired); the same IP now
answers with a different serial (replaced — retire the old binding);
ICMP answers but the management protocol does not (a credential or config
problem, never counted toward MISSING). The device service surfaces this as
"last seen on switch X port Y at T; port now down".

Policy, per tenant, conservative by default: unreachable longer than the
threshold (default 24 h) → `MISSING` and an alert; DEGRADED alone fires
nothing; maintenance windows suppress the transition; auto-retire of
local-network devices is opt-in, needs a long window and corroborating
evidence, and is off by default — a ghost in the inventory is annoying, a core
switch silently retired after a long weekend is worse. Never delete.

### Retiring, by hand and by platform

**Operator retire** is an explicit, small, reversible action: from the device
view or the Missing list (bulk-selectable), *Retire* → reason (decommissioned /
replaced by … / moved out of scope / unknown) + optional effective date →
confirm; one inventory RPC, `RetireDevice(ref, reason, effective_at)`. Effects:
lifecycle `RETIRED`; all bindings `RETIRED`; the open Placement closes;
monitoring, alerts, pollers and watchers drop it; the integration's
credentials are untouched; history, events and topology-over-time stay; an
audit event records who, when, why. *Unretire* restores bindings to CANDIDATE
and re-verifies. Retire is not purge: hard deletion exists only as a
tenant-level "forget" for offboarding or data-protection requests — destructive,
separately authorized, never a per-device button.

**Platform removal auto-retires**, because a controller's or cloud tenant's
inventory is authoritative for what it manages — its absence is a statement,
where a local network's absence is silence. On a successful, complete sync in
which the platform no longer lists serial S: retire that binding at once; if
it was the device's only binding, auto-retire the device with reason "removed
from <integration>"; if other bindings exist the device stays ACTIVE with the
binding retired and a note ("no longer managed by …"). Guards, without which
this is the most dangerous feature in the product:

- Only a complete sync counts. A failed, partial, rate-limited, or
  permission-denied inventory listing retires nothing.
- A mass-drop threshold (e.g. more than 20 % of an integration's devices gone
  in one sync, or the API key losing org access) holds everything, marks the
  integration DEGRADED, and alerts; an operator confirms before anything
  retires. An expired API key must not empty a tenant.
- *Unassigned* is not *removed*: a device pulled from a Meraki network but still
  in the org inventory is a scope/Placement change; only "gone from the
  platform's inventory" triggers retire.
- A serial that reappears elsewhere within a short window is a *move*: the
  reason flips from removed to moved, a binding is created on the new
  integration, and Placement is updated.

## Transport: NATS as the integration fabric

```
web / workflows
      │  Connect (typed device API; OpenFGA-guarded)
      ▼
central device service ── inventory: devices, integrations, bindings, candidates, budgets
      │  publishes exec.<integration>, subscribes events.<tenant>.>
      ▼
NATS cluster (per-tenant accounts, JetStream)  ◄── wss/TLS on 443 ──  edge agent
      ▲                                                                  (embedded leaf node,
      └── cloud/controller adapters hosted centrally                      local JetStream buffer,
                                                                           protocol libraries)
```

- **Announce** — heartbeat subjects; a live registry of integrations, their
  kinds, capabilities, and host health. One source of truth for "who's alive".
- **Execute** — core request/reply on `exec.<integration-id>`; any central
  replica can publish, so horizontal scaling of central needs no connection
  registry. Core NATS is at-most-once and deadlines are client-side
  conveniences, so every execute carries a deadline header and every *write*
  carries a client-generated idempotency key; the integration caches the first
  result under the key and replays it on retry (Stripe's model: UUID key,
  identical response replayed, parameter-mismatch rejected, ~24 h TTL).
- **Events** — JetStream on `events.<tenant>.<integration>.>`; multiple
  consumers (state store, alerting, time-series, replay), durable, buffered
  when central is down.
- **Connect** keeps three jobs: web↔central; enrollment/bootstrap over HTTPS;
  an optional HTTPS attach for integrations that cannot speak NATS, which
  central bridges onto the same subjects (bidirectional Connect streams
  require HTTP/2 end to end; unary and one-way streams do not).

Load shape at ~5 000 devices, for calibration: announce is a few hundred
integrations at one message per few seconds; execute is tens to low hundreds of
requests per second, bursty, human-driven; ingestion is thousands of messages
per second sustained with 10× bursts. Comparable products size a collection
unit in the low thousands of devices (Auvik ≈1 500 devices per collector,
LibreNMS ≈1 000+ devices per poller, PRTG ≈5 000–10 000 SNMP sensors per core,
Zabbix ≈1 500–1 750 new values/s per proxy); NATS core handles millions of
messages per second and "several thousand" connections per server by default.
Connections scale with integrations, not devices. None of this is the
bottleneck; the shape is. Ingestion volume must stay proportional to *change*,
not fleet size: Adaptive Watch's indicator-gated fetches and delta events, not
snapshots.

## Edge attachment and enrollment

- The edge agent **embeds a NATS leaf node** (`nats-server` as a Go library,
  `server.LeafNodeOpts`/`RemoteLeafOpts`) and talks to it on localhost; the
  leaf connects **outbound** to the central cluster over `wss://` on 443 —
  leaf nodes over WebSocket behind a TLS-terminating 443 ingress is a
  documented NATS edge pattern. Same firewall posture as HTTPS.
- **Store-and-forward** is local JetStream in its own *domain* (a leaf without
  a distinct domain silently extends the hub's JetStream — documented pitfall),
  with a *source* stream on the hub aggregating the edge streams; both sides
  keep independent copies and recover from link loss (documented reconnect
  sync of 10–20 s). This is what Zabbix proxies (disk-backed, configurable
  offline buffer in hours) and PRTG probes (RAM-backed, ~500 000 results —
  ~3 days at 100 sensors/min, ~52 min at 10 000) give operators, and what Auvik
  does not (WAN loss = gap in history). Buffer on disk, bound by size and age,
  and surface "edge buffering since …" in the UI.
- **Multi-tenancy** is enforced by the broker: one NATS **account** per tenant
  (accounts are isolated subject namespaces; cross-account needs explicit
  import/export); per-edge **NKey/JWT** user credentials with publish/subscribe
  allow-lists scoped to the edge's own subjects. An edge physically cannot
  publish into another tenant's subjects. Revocation is a per-user JWT
  revocation pushed to the account; auth callout is available if credential
  issuance should be delegated to Zitadel.
- **Enrollment**: operator creates the edge in the web app → one-time,
  short-TTL, revocable token (the Teleport join-token / Tailscale auth-key
  shape; outbound-only like Auvik/Datadog agents) → edge calls central
  `Enroll` over Connect/HTTPS → receives its NATS account, JWT/NKey seed,
  subject map, and cluster URLs → connects. Credentials never leave the secret
  store except to the host running the integration, per request or as a
  scoped lease.
- **Exposing NATS** (even over wss/443) is a **second public surface** next to
  the web app's. Accepted deliberately: TLS + per-edge JWTs + accounts is how
  NATS is designed to be run on the internet; it is recorded here so it is a
  deployment decision, not a discovery.
- **Durability hardening is mandatory, not default.** Jepsen (NATS 2.12.1,
  Dec 2025) showed JetStream can lose acknowledged data or split-brain under
  crash/power loss because the default `sync_interval` is 2 minutes (lazy
  fsync). The documented remedy is `sync_interval: always` (fsync before ack,
  at a throughput cost this workload can afford) plus R3 replicas on the hub.
  Edge leaf buffers may run lazier — telemetry lost on an edge power cut is a
  gap in history, not a correctness failure — and writes never travel over
  JetStream.
- **License**: nats-server stays Apache-2.0 under CNCF after the April–May
  2025 Synadia dispute (stewardship and trademarks assigned to the Linux
  Foundation); two active release branches with ~monthly patches. Passes the
  OSS / fully self-hostable / EU-deployable rule.

### Local service runtime boundary

The shared `src/common/service` runtime owns process-local module supervision
and an optional durable bus. A service declares a non-empty tree of leaves and
branch supervisors. Leaf setup constructs fresh attempt state after a failure;
branch policy decides which children are reconstructed. Module gates and stable
slash-separated paths are evaluated before setup, so a bad declaration or an
empty enabled tree has no partial module side effects.

The local bus is distinct from the edge leaf-node and central integration
fabric above. It is listener-free, has no central credentials, and persists
protobuf envelopes only for modules in one service process. Its embedded NATS
server follows the fsync policy the service declares, and there is no implicit
policy: a configuration that names none fails `Run` with `service/bus-config`
before the store is locked or opened. Periodic sync runs on a five-second
target interval. A process crash does not lose acknowledged records under
either policy because the writes remain in the operating system's page cache.
Services without local messaging do not start NATS or create a store.

A power loss may lose recent message or settlement records written since the
last completed periodic sync. The configured cadence is not a guaranteed
maximum loss window because operating-system scheduling and storage delays can
postpone completion. If a message survives but its settlement does not, its
handler may run again under the bus's at-least-once delivery contract. Only
`BusFsyncPerMessage` keeps acknowledged records across a power loss. Because
the policy must be declared, an upgrade cannot change durability silently: the
new binary refuses to start against an undeclared configuration and leaves the
store as the previous binary wrote it, and the author chooses
`BusFsyncPerMessage` to keep the old guarantee or `BusFsyncPeriodic` to trade
it for throughput. A store written under either policy opens under the other
without migration.

The default logical budget is 1 GiB: 768 MiB for module mailboxes, 64 MiB for
runtime metadata, and 192 MiB reserved for broker state and atomic publication.
These limits reject new work before evicting accepted records, but do not cap
filesystem metadata or temporary allocation. Deployments needing a physical
ceiling must place the store on a quota or bounded volume. A filesystem call
stuck in the kernel also remains an external process-supervisor or device-reboot
recovery boundary; runtime contexts can bound publication and health failure,
not force that syscall to return.

Module paths, protobuf full names, subject encoding, the envelope, and the NATS
server pin are storage identities. Compatible additions reconcile through a
durable protobuf journal. A queued removal or rename requires an explicit
migration, and a future NATS pin must preserve a store backup before the new
server version opens it. Subscription aliases are the compatibility mechanism
for protobuf type renames.

## Rules the contract must keep

1. **Capabilities, never protocols.** The API vocabulary is entities and verbs
   (`GetIdentity`, `GetInterfaces`, `ApplyVlanConfig`). Backing protocol and
   model appear only as diagnostic provenance. No `RawGet(path)` ever lands in
   this service; raw access, if needed, is a separate diagnostics service.
2. **Capabilities are discovered per binding and exposed as a queryable
   matrix** — not declared per kind, not assumed uniform. Core messages stay
   tight; vendor enrichments live in per-vendor extension messages. This is the
   rule that keeps the service from re-enacting NAPALM's
   lowest-common-denominator failure in protobuf.
3. **Freshness is explicit.** Every live response carries `observed_at` and
   which binding answered. A cloud-mediated read is "live as the platform sees
   it", and says so.
4. **Writes carry their guarantee level.** `Apply*` supports `validate_only`;
   the result states atomicity (transactional / best-effort-verified /
   accepted-by-platform-converging) and a read-back diff. Every write carries
   an idempotency key. Nothing pretends to be atomic that is not.
5. **Edition-2024 presence distinguishes unsupported from zero.** Unset means
   "this device does not provide it".
6. **Errors cross the boundary through `errs`:** codes → Connect codes, Retry
   Disposition → code choice, User Message/Hint and Public Attributes as the
   client detail, internal message to the log; the same envelope rides the
   broker.
7. **Nothing cascade-deletes.** Retire, don't remove; correlation by serial
   makes reappearance a merge. Binding and Placement changes are events;
   current state is a projection.
8. **Per-integration request budgets are a facade concern.** Cloud APIs are
   rate-limited per org *and* per source IP (Meraki: 10 req/s per org, 100
   req/s per source IP); discovery, facade, and pollers draw from one budget
   with the facade first.
9. **Kinds are typed or not shipped.** Compiled `oneof` for first-party kinds,
   advertised descriptor for third-party kinds, never free-form config.
10. **Routing reads only Binding and host.** New kinds need no routing changes.

## Sequencing

1. **Treat the owned proto foundation as the first dependency.** The schemas
   under `spec/proto/flowseer/` and the
   [protobuf conventions](../conventions/protobuf.md) define the per-entity
   Config/State/Event families, Local/Global refs, and device refs that inventory
   additions build on. The package partition, the primitive/entity split, and
   the interface shape are fixed in
   [the network model structure direction](2026-08-20-network-model-structure-direction.md).
2. **Write the device-API and integration contracts as specs now** —
   capability matrix as a hard rule, announce/execute/events, binding routing,
   error mapping, budgets, guarantee levels, idempotency — so the
   protocol-library work knows what it is feeding.
3. **Implement after the first protocol library is real and the R11 identity
   read has been done by hand once.** That is the "real caller" KD4 asked for.
4. **Stand up NATS with the hardened profile from day one** (accounts,
   JWT auth, `sync_interval: always`, R3 on the hub, distinct JetStream domain
   per edge) — retrofitting durability and tenancy into a running fabric is
   the expensive path.
5. **v1 scope:** Device, Integration, Binding, Placement as separate entities;
   the local-network kind and one cloud kind (Meraki or SmartZone) end to end;
   capabilities chosen bottom-up from what the vendored fleet can deliver
   broadly: identity, interfaces, neighbors/LLDP, VLANs, trap/telemetry
   destination config. Third-party descriptor-advertised kinds after the
   first-party path is proven.
6. **Grow by adding entity files, capability arms, and kinds**, never by
   revving the service.

## Open questions

- Meraki single (non-batched) config writes: whether dashboard acceptance
  precedes device application is plausible but unverified; only action-batch
  sync/async semantics were confirmed. Affects the converging guarantee level.
- RUCKUS One numeric rate limits — not yet retrieved.
- protobuf-es Editions-descriptor support for dynamic forms is reported but
  not confirmed by a direct read of its manual; check before the third-party
  path is built.
- Embedded leaf-node mode via `server.Options` is supported by the types but
  no official example was found; prove it in a spike before committing the
  edge binary layout.
- Whether trap/telemetry destination configuration is the facade's (a write on
  a device — current position: yes) or the ingestion plane's.
- Credential handling between central and edge: scoped lease vs. per-request
  delivery.
- Published sweep-rate throttles or consent UX for active discovery have no
  found precedent; FlowSeer's opt-in-per-range rule stands on its own.

## Sources

- Repository: `docs/code-style.md` §Project layout (`src/backend`, `src/edge`
  named); `docs/plans/2026-08-17-2254-refactor-internal-errs-package-plan.md`
  (edge/backend split, Connect RPC, brokers, cross-boundary errors);
  `src/common/errs/doc.go` (wire payload: code, safe attributes, user message,
  hint, retry disposition); `src/protocol/snmp/doc.go` (Collection Primitives
  lifecycle contract); `CONCEPTS.md` §Flagged ambiguities (no backend/driver
  abstraction); `docs/plans/2026-08-20-1245-feat-yang-protocol-libraries-plan.md`
  on branch `worktree-brainstorm-netconf-restconf` (KD4, KD7, R10, R11, KTD4,
  KTD5); `spec/openapi/README.md`, `spec/yang/README.md`.
- Cloud platforms: Cisco Meraki Dashboard API — rate limits, Org→Network→Device
  with serial-keyed devices, webhooks, action batches
  (developer.cisco.com/meraki); Meraki SNMP overview (local polling alongside
  dashboard SNMP; documentation.meraki.com); RUCKUS One API docs (JWT,
  `api.eu.ruckus.cloud`, async `requestId`, webhooks; docs.ruckus.cloud).
- Data-model and UX precedent: Nautobot `Controller` /
  `ControllerManagedDeviceGroup` (docs.nautobot.com); NAPALM multi-vendor
  critique (karneliuk.com, 2021); Auvik Meraki integration and collector
  architecture (support.auvik.com); Datadog Meraki integration and SNMP
  autodiscovery (docs.datadoghq.com); PRTG auto-discovery and remote-probe
  buffering (paessler.com, kb.paessler.com); Zabbix proxy offline buffer and
  NVPS sizing (zabbix.com, blog.zabbix.com); LibreNMS distributed poller and
  xDP discovery (docs.librenms.org); Teleport join tokens and reverse tunnels
  (goteleport.com); Tailscale auth keys (tailscale.com).
- Transport: NATS docs — WebSocket leaf nodes, JetStream on leaf nodes and
  domains, sources/mirrors, accounts, NKey/JWT auth, revocation, auth callout,
  `sync_interval` (docs.nats.io); nats-server Go package (`server.Options`,
  `LeafNodeOpts`, `RemoteLeafOpts`; pkg.go.dev); Jepsen analysis of NATS
  2.12.1 (jepsen.io, 2025-12-08) and Synadia's response; CNCF/Synadia
  agreement (cncf.io, 2025-05-01); ConnectRPC protocol (bidi requires
  HTTP/2; connectrpc.com); Stripe idempotent requests (docs.stripe.com).
- Extensibility: Go `plugin` package caveats (pkg.go.dev/plugin);
  hashicorp/go-plugin README (local-network-only, coarse protocol version);
  protobuf-es registries/reflection (protobufes.com); Go `protodesc`,
  `dynamicpb` (pkg.go.dev); protovalidate-go dynamic-message support;
  Terraform `GetProviderSchema` (developer.hashicorp.com); Telegraf external
  plugins ("no discoverability"; github.com/influxdata/telegraf).
