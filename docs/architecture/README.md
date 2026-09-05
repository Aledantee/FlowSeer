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

| Record | Status | Read when |
| --- | --- | --- |
| [Device Service, Integrations, and Inventory](2026-08-20-device-service-and-inventory-direction.md) | Accepted direction | Working on the device service, integrations, inventory, discovery, ingestion, attachment, or the transport fabric. |
| [Network Model Structure](2026-08-20-network-model-structure-direction.md) | Accepted direction | Adding or moving FlowSeer-owned protobuf packages, network primitives, entities, refs, or device-facing capability models. |
| [Net Core Package Research](2026-08-26-net-core-package-research.md) | Supporting research | Checking the evidence behind the `net/phy`, `net/packet`, `net/switching`, and `net/ip` boundaries. |
| [Error Wire Design](2026-09-04-error-wire-design-direction.md) | Accepted direction | Putting `src/common/errs` errors on a wire — Connect RPC, a broker, or any other cross-process hop. |
| [Verified Device Access](2026-09-05-verified-device-access-direction.md) | Accepted direction | Planning or implementing device reads and writes through the local-network integration: routing, the device lane, the mutation journal and barrier, credential delivery, or the device-access boundary packages. |
| [Secret Material Carrying](2026-09-06-secret-material-carrying-direction.md) | Proposed direction | Adding a field, option, or config that carries a password, passphrase, or private key, or deciding how a value is kept out of logs and errors. |
| [Mutation Shadow Projection](2026-09-09-mutation-shadow-projection-direction.md) | Proposed direction, deferred | Gating a Mutation Intent before apply once the Interface triad and a full-network view exist; shaping typed effects in the device-access schemas so a projection can apply them later. |

Current source and tests show which parts of a direction have landed. When the
tree and an accepted record disagree, do not silently choose one. Update or
amend the record in the same change that resolves the mismatch.
