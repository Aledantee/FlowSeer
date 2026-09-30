---
title: The buf CLI version decides one option byte in every generated descriptor, so the generated tree flips between machines
date: 2026-09-23
last_verified: 2026-09-30
category: conventions
module: buf.gen.yaml
problem_type: bug
component: codegen
severity: medium
applies_when:
  - "Running go tool -modfile=tools/buf/go.mod buf generate after a .proto change and seeing hundreds of generated/ files change that your proto did not touch"
  - "The verifier's diff generated/go/proto gate fails on generated files unrelated to your change, on a branch or on main"
  - "Deciding whether generated-tree churn belongs in a feature commit"
  - "Pinning buf.gen.yaml plugins, or the buf CLI, to make go tool -modfile=tools/buf/go.mod buf generate reproducible"
related_components: [spec/proto, generated]
tags: [buf, codegen, generated, protobuf, verify]
symptoms:
  - "go tool -modfile=tools/buf/go.mod buf generate reports 175 to 308 changed .pb.go files after a one-field proto edit, or with no proto edit at all"
  - "verify-change.sh fails in gate: diff generated/go/proto on files like ruckus/sci/sci-rogue.pb.go that have nothing to do with the change"
  - "git diff of a drifted file shows only a descriptor length prefix moving by two and a P\\x01 byte appearing or disappearing, no Go API change"
root_cause: "buf managed mode writes the java_multiple_files file option into each descriptor it hands the plugins, and whether it does depends on the buf CLI release. Before the pin, each contributor's local buf decided the byte, and the committed tree matched whichever version last regenerated it."
resolution_type: fix
---

## The situation

`buf.gen.yaml` turns managed mode on and pins the remote plugins used for Go output:

```yaml
managed:
  enabled: true
plugins:
  - remote: buf.build/protocolbuffers/go:v1.36.12
    out: generated/go/proto
    opt: paths=source_relative
  - remote: buf.build/connectrpc/go:v1.20.0
    out: generated/go/proto
    opt: paths=source_relative
```

Managed mode rewrites file options before the plugins see the descriptors, and
`protoc-gen-go` embeds each descriptor verbatim in the `.pb.go` file. One of
those options, `java_multiple_files` (field 10, encoded `P\x01`), is set by some
buf CLI releases and not by others. So the embedded bytes in every generated
file follow the CLI version, not the schema. The repository now runs the pinned
Buf CLI v1.73.0 through `go tool -modfile=tools/buf/go.mod buf`.

The repository has recorded both directions:

- `c106d8bc` (2026-09-23, darwin, buf 1.73.0) removed the option from 175 files:
  `B\xff\x01…P\x01Z` became `B\xfd\x01…Z`.
- `089a2330` (2026-09-29, darwin, buf 1.70.0 from Homebrew) added it back to 308
  files, the reverse byte change. Its message: "managed mode now sets
  java_multiple_files, which lands in every raw descriptor. No schema changed."
- `9c18947d` reverted it on the same branch and regenerated under buf 1.73.0
  (`go tool -modfile=tools/buf/go.mod buf generate`), because the tree
  on `main` came from 1.73.0 and the 1.70.0 output was the older CLI's, not a
  fix.

The first capture of this lesson blamed the unpinned remote plugins. The flip
back under an older CLI shows the option comes from the CLI, so pinning the
plugins alone would not stop it.

## Why it bites

The verifier regenerates and diffs on every change that touches a `.proto`
file, not only under `--full`:

```bash
  run "${buf_cmd[@]}" generate -o "$generated_dir"
  run diff -qr generated/go/proto "$generated_dir/generated/go/proto"
```

(`.agents/skills/verify-change/scripts/verify-change.sh:800`). With a
CLI that disagrees with the last regeneration, that gate fails on `main` and
on every branch, on files the change never reached. The trap is reading the
failure as caused by your change, or sweeping the churn into the feature
commit.

## How to apply

When a regen touches far more files than your proto reaches and the extra
diffs are option-byte-only:

1. Run `go tool -modfile=tools/buf/go.mod buf --version` and compare it with
   the version named in the last `chore(generated): regenerate` commit. Confirm
   by regenerating at the parent commit with your schema edit absent: if the
   same files drift, it is the CLI.
2. Land the drift as its own commit before the feature, naming the buf
   version, as `089a2330` did, so the feature's generated diff holds only
   what its schema produced.
3. The repository pins Buf v1.73.0 in `tools/buf/go.mod` and pins the remote
   plugins in `buf.gen.yaml`. Run `go tool -modfile=tools/buf/go.mod buf
   generate` so every machine produces the same bytes.

## What it does not cover

A regen that changes a Go type, a method set, or a descriptor's field numbers
is a real schema change, not this drift, and is reviewed as one. This entry is
only about the managed-mode option byte across files a change did not touch.
