# Architecture records

Read the accepted record for the area before designing a change. A record is
authoritative when its frontmatter says `status: accepted-direction`.
Research supplies evidence for a decision but does not override an accepted
record or a binding convention.

| Record | Status | Read when |
| --- | --- | --- |
| [Device Service, Integrations, and Inventory](2026-08-20-device-service-and-inventory-direction.md) | Accepted direction | Working on the device service, integrations, inventory, discovery, ingestion, attachment, or the transport fabric. |
| [Network Model Structure](2026-08-20-network-model-structure-direction.md) | Accepted direction | Adding or moving FlowSeer-owned protobuf packages, network primitives, entities, refs, or device-facing capability models. |
| [Net Core Package Research](2026-08-26-net-core-package-research.md) | Supporting research | Checking the evidence behind the `net/phy`, `net/packet`, `net/switching`, and `net/ip` boundaries. |
| [Error Wire Design](2026-09-04-error-wire-design-direction.md) | Accepted direction | Putting `src/common/errs` errors on a wire — Connect RPC, a broker, or any other cross-process hop. |

Current source and tests show which parts of a direction have landed. When the
tree and an accepted record disagree, do not silently choose one. Update or
amend the record in the same change that resolves the mismatch.
