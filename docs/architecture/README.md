# Architecture records

Read the accepted record for the area before designing a change. A record is
authoritative when its frontmatter says `status: accepted-direction`.
Research supplies evidence for a decision but does not override an accepted
record or a binding convention.

A direction record is this repository's architecture decision record. The
`plan` skill drafts one with `status: proposed-direction` when a plan
decision outlives its task (see the promotion test in
`.claude/skills/plan/SKILL.md`). A proposed record binds nothing until a
person reads it and changes the status to `accepted-direction`; a record
that is replaced gets `status: superseded` and a `superseded_by` path rather
than an edit.

A record never links a plan. `land` deletes a plan under `docs/plans/` once
its work lands, so a record names landed work by the date it landed and its
scope, with the packages or schema paths it touched:

```markdown
Landed 2026-09-18: exact state comparison in `src/common/netsim/compare`,
covering port tables and VLAN membership.
```

| Record | Status | Read when |
| --- | --- | --- |
| [Device Service, Integrations, and Inventory](2026-08-20-device-service-and-inventory-direction.md) | Accepted direction | Working on the device service, integrations, inventory, discovery, ingestion, attachment, or the transport fabric. |
| [Network Model Structure](2026-08-20-network-model-structure-direction.md) | Accepted direction | Adding or moving FlowSeer-owned protobuf packages, network primitives, entities, refs, or device-facing capability models, or a README under `spec/proto`. |
| [YANG Protocol Libraries](2026-09-28-yang-protocol-libraries-direction.md) | Accepted direction | Working on the YANG runtime, the `yanggen` generator, the generated `generated/go/yang` bindings, or the NETCONF, RESTCONF, and gNMI client libraries. |
| [YANG Augment Namespaces](2026-09-30-yang-augment-namespaces-direction.md) | Accepted direction | Changing how `yanggen` places augmented nodes, the runtime's group field kind, or code that reads a node another YANG module augments into a binding. |
| [SMI Parser and MIB Bindings](2026-09-28-smi-mib-toolchain-direction.md) | Accepted direction | Working on the SMI parser, `mibgen`, the generated `generated/go/mib` bindings, generated table walks, or MIB-derived identity and row keys. |
| [netpen Lab Vendor Validation](2026-09-23-netpen-lab-vendor-validation-direction.md) | Accepted direction | Producing netpen vendor behavioral truth: injecting an attack from a Linux host and reading the target device's own protocol state over SSH, defining a per-behavior expected finding, or wiring the `netpen_t1`/`netpen_t2` tiers to a live lab. |
| [Net Core Package Research](2026-08-26-net-core-package-research.md) | Supporting research | Checking the evidence behind the `net/phy`, `net/packet`, `net/switching`, and `net/ip` boundaries. |
| [Error Wire Design](2026-09-04-error-wire-design-direction.md) | Accepted direction | Putting `src/common/errs` errors on a wire — Connect RPC, a broker, or any other cross-process hop. |
| [Verified Device Access](2026-09-05-verified-device-access-direction.md) | Accepted direction | Planning or implementing device reads and writes through the local-network integration: routing, the device lane, the mutation journal and barrier, credential delivery, or the device-access boundary packages. |
| [Secret Material Carrying](2026-09-06-secret-material-carrying-direction.md) | Proposed direction | Adding a field, option, or config that carries a password, passphrase, or private key, or deciding how a value is kept out of logs and errors. |
| [Mutation Shadow Projection](2026-09-09-mutation-shadow-projection-direction.md) | Proposed direction, deferred | Gating a Mutation Intent before apply once the Interface triad and a full-network view exist; shaping typed effects in the device-access schemas so a projection can apply them later. |
| [Network Simulation Prior Art Research](2026-09-10-network-simulation-prior-art-research.md) | Supporting research | Checking what Packet Tracer, Batfish, ns-3, INET, bmv2, the Linux bridge, and the 802.1Q YANG model do that `src/common/netsim` borrows or rejects. |
| [Virtual Device](2026-09-10-virtual-device-direction.md) | Proposed direction | Building or consuming a simulated device or network: the port table, capability packages under `src/common/netsim`, frame forwarding over a typed configuration, links and hosts in a fabric, or comparing a current and an expected state. |
| [Streaming Frame Transport](2026-09-09-streaming-frame-transport-direction.md) | Proposed direction | Adding a streaming RPC, a chunked payload, or a producer that can outrun its consumer, or deciding how a long-lived edge stream stays authorized. |
| [Operator Authorization](2026-09-30-operator-authorization-direction.md) | Accepted direction | Authenticating operators, authorizing an operator or admin RPC, adding an RPC to a service under `spec/proto/flowseer/api/`, tenancy and membership, or choosing the authorization engine. |
| [Remote Packet Capture](2026-09-09-remote-packet-capture-direction.md) | Proposed direction | Working on packet capture, mirrored traffic, ERSPAN or other mirror encapsulations, capture filters, or the handling of captured payload. |
| [Supervised Goroutine Spawn](2026-09-15-supervised-goroutine-spawn-direction.md) | Proposed direction | Writing a `go` statement in non-test `src/`, or deciding where a panic in a spawned goroutine is recovered, reported, and attributed. |
| [Operator Authorization](2026-09-28-operator-authorization-direction.md) | Superseded by the 2026-09-30 record | Historical identity, tenancy, relationship, and operator-action direction. |
| [Local Network Analysis](2026-09-16-local-network-analysis-direction.md) | Proposed direction | Adding packet filtering, a routed sub-interface, or an endpoint that reacts to traffic under `src/common/netsim`, or deciding how a stateful firewall or an mDNS reflector is simulated. |
| [Offered-Load Streams](2026-09-18-offered-load-streams-direction.md) | Proposed direction | Stating traffic load in a simulation: streams, field variation and seeds, egress buffers and tail drop, journey retention and per-flow statistics, a capture file as a source, or an on-wire transmitter that runs the same stream. |
| [Simulation Package Shape](2026-10-01-simulation-package-shape-direction.md) | Accepted direction | Adding, moving, or importing a package under the simulator tree: the `sim` root, capability layers, the device and medium seams, the capability contract, or protocol conformance. |
| [Web Design System](2026-09-26-web-design-system-direction.md) | Accepted direction | Styling, theming, or adding a component in `frontend/web/`: design tokens, Tailwind, Reka UI primitives, or Storybook stories. |
| [Web Component Contract](2026-09-28-web-component-contract-direction.md) | Accepted direction | Adding or changing any component in `frontend/web/`: composition, i18n and locale files, the `ai` prop and generative UI catalog, overlays (portals, stacking, dismissal, focus), or animation. |
| [Schema Building Blocks](2026-09-25-schema-building-blocks-direction.md) | Accepted direction | Adding any FlowSeer-owned protobuf package or message: canonical units, key rules, the network-instance key, facet and table naming, protocol packages, and the Endpoint, Wlan, and Alarm entities. |
| [Dependency Admission](2026-10-01-dependency-admission-direction.md) | Proposed direction | Adding, upgrading, or removing a Go module, npm package, container image, buf module or plugin, or toolchain pin, or reviewing a dependency version. |

Current source and tests show which parts of a direction have landed. When the
tree and an accepted record disagree, do not silently choose one. Update or
amend the record in the same change that resolves the mismatch.
