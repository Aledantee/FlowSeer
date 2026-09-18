---
title: A Protobuf Package Rename Breaks the Names You Persisted, Not the Records You Encoded
date: 2026-09-18
last_verified: 2026-09-18
category: architecture-patterns
module: src/common/service
problem_type: architecture_pattern
component: service_layer
severity: high
applies_when:
  - "Renaming a protobuf package or message whose records are already written to a store, a queue, or a file"
  - "Deciding whether such a rename needs a data migration, and what a pre-rename store does on the next start"
  - "Writing the amendment or release note that tells an operator what a rename costs them"
  - "A service refusing to start against an existing store with service/bus-migration-required"
related_components: [messaging, data_model, protobuf]
tags: [protobuf, rename, migration, wire-format, runtime-manifest, local-bus]
---

# A package rename breaks the names you persisted, not the records you encoded

## The situation

Renaming `flowseer.service.v1` to `flowseer.runtime.v1` looks like it should
invalidate every record already in the local bus store. It does not. A
serialized protobuf message carries field numbers and wire types, and no
package name, message name, or descriptor. A record written before the rename
decodes into the renamed Go type unchanged:

```go
message := &runtimev1.Message{}
if err := proto.Unmarshal(data, message); err != nil { ... }
// message.ProtoReflect().Descriptor().FullName() == "flowseer.runtime.v1.Message"
```

`src/common/service/message_compat_test.go:45-54` runs exactly that against
`src/common/service/testdata/message_v1.bin`, a fixture captured before the
rename, and it passes without the fixture being regenerated.

What does break is every place the full name was written down *as data*. In
this repository that is one field: `RuntimeManifest.envelope_type`, set to the
literal `"flowseer.runtime.v1.Message"` at `src/common/service/manifest.go:149`.
On the next start, `reconcileRuntimeManifest` compares the manifest it finds
against the one this build would write, and a changed persisted identity is
refused rather than reconciled (`manifest.go:244`, reporting
`errs.NewCode("service/bus-migration-required")` from `manifest.go:33`).

## What follows for a rename

Say the two halves separately, because an operator acts on them differently:

- The queued records are fine. Nothing needs re-encoding, and nothing is lost
  by leaving them in place.
- The manifest refuses, loudly and at start, with a named error code. The store
  is either discarded or its manifest journal rewritten; the refusal is not a
  corruption and not a silent downgrade.

An amendment that says "the queued records no longer resolve" reads as data
loss and invites an operator to delete a store that would have worked. The
first draft of this rename's amendment said that, and a review caught it
against the compatibility test above.

Whether a rename of this kind ships a migration is a separate decision: this
one shipped none, because the repository is pre-stability and `AGENTS.md`
prefers a breaking change to a compatibility shim. That decision belongs in the
direction record, not in the schema comment — `docs/code-style-proto.md` keeps
rationale out of `.proto` files.

## Evidence

- `src/common/service/message_compat_test.go:45-54` decodes the pre-rename
  fixture into `runtimev1.Message` and asserts the new full name; it passed
  across the rename with the fixture untouched.
- `src/common/service/manifest.go:149` writes `envelope_type` as a literal
  string, the only persisted copy of the full name.
- `src/common/service/manifest.go:33,244` refuse a changed persisted identity
  with `service/bus-migration-required`.

## What this does not cover

It says nothing about a field number or enum number change, which does break
encoded records and which `message_compat_test.go:41-43` guards separately. It
also does not apply to a format that stores type names with the data, such as
`google.protobuf.Any`, JSON with a type field, or a prototext file whose
comment names the type: those carry the name and change with it.
