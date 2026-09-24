---
title: Unpinned buf remote plugins drift the whole generated tree, so a regen for one proto change touches hundreds of files
date: 2026-09-23
category: conventions
module: buf.gen.yaml
problem_type: bug
component: codegen
severity: medium
applies_when:
  - "Running buf generate after a .proto change and seeing hundreds of generated/ files change that your proto did not touch"
  - "The verify --full diff generated/go/proto gate fails on generated files unrelated to your change"
  - "Deciding whether generated-tree churn belongs in a feature commit"
related_components: [spec/proto, generated]
tags: [buf, codegen, generated, protobuf, verify]
symptoms:
  - "buf generate reports ~175 changed .pb.go files after a one-field proto edit"
  - "verify-change.sh --full fails in gate: diff generated/go/proto on files like ruckus/sci/sci-rogue.pb.go that have nothing to do with the change"
  - "git diff of a drifted file shows only a shrunk descriptor length prefix and a dropped option byte, no Go API change"
root_cause: "buf.gen.yaml pins no version on its remote plugins, so buf generate resolves the latest published plugin; when that plugin releases, its output differs from the committed tree (which an older plugin produced) and every file drifts."
resolution_type: workaround
---

## The situation

`buf.gen.yaml` declares its code generators as versionless remote plugins:

```yaml
plugins:
  - remote: buf.build/protocolbuffers/go
    out: generated/go/proto
    opt: paths=source_relative
  - remote: buf.build/connectrpc/go
    out: generated/go/proto
```

With no `:vX.Y.Z` suffix, `buf generate` resolves whatever the registry
currently serves. The committed `generated/` tree was produced by an older
plugin, so the moment upstream ships a new release, running `buf generate`
rewrites every file — even ones whose `.proto` you never opened.

Observed on 2026-09-23 (darwin, buf 1.73.0): a single new field in
`spec/proto/flowseer/store/device/v1/service_config.proto` regenerated 176
files. The 175 unrelated ones differed only by a dropped `java_multiple_files`
option in each embedded raw descriptor (the `P\x01` byte, with the file option
block's length prefix shrinking by two), for example `B\xff\x01…P\x01Z` becoming
`B\xfd\x01…Z`. No message shape or Go API changed.

## Why it bites

The full verifier regenerates and diffs the tree:

```
FlowSeer verification FAILED (exit 1) in gate: diff generated/go/proto
```

Because the drift is repo-wide and environmental, the gate fails on `main`
too, and it fails on files your change did not touch. The trap is reading that
failure as caused by your change and either debugging the wrong thing or
sweeping the churn into your feature commit.

## How to apply

When a regen for a small proto change touches far more than the files your
proto reaches, and the extra diffs are option-byte-only:

1. Treat the churn as pre-existing plugin drift, not your change. Confirm by
   reverting your proto edit and regenerating: if the same unrelated files
   still drift, it is the plugin.
2. Do not fold it into the feature commit. Land the drift as its own
   `chore(generated): regenerate under the updated remote protobuf plugin`
   commit so the feature stays reviewable, then the `--full` gate passes.
3. The durable fix is to pin the remote plugins to explicit versions in
   `buf.gen.yaml`, which makes `buf generate` reproducible and stops the
   drift recurring. `buf.gen.yaml` is not a policy surface, but pinning
   changes generation for the whole repo, so raise it rather than doing it
   inside an unrelated feature — this solution is the workaround until then.

## What it does not cover

A regen that changes a Go type, a method set, or a descriptor's field numbers
is a real schema change, not this drift; review it as one. This entry is only
about option-byte-only churn across files a change did not touch.
