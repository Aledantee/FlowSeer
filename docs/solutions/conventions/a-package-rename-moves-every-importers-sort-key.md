---
title: A Package Rename Moves Every Importer's Import Sort Key, and Reformatting Needs the Repo's Local Prefix
date: 2026-09-18
last_verified: 2026-09-18
category: conventions
module: generated/go/proto
problem_type: convention
component: conformance-gates
applies_when:
  - "Renaming or moving a protobuf package, so every Go file importing its generated code takes a new import path"
  - "A rename that compiles and tests clean but fails golangci-lint on goimports in files the change never touched by hand"
  - "Running gofumpt or goimports by hand over the files a mechanical change touched"
severity: medium
related_components: [protobuf, verify-change]
tags: [goimports, gofumpt, golangci-lint, protobuf, rename, formatting]
---

# A package rename moves every importer's import sort key

## The situation

Moving a protobuf package changes the generated Go import path, and an import's
path is its sort key. A file that imported
`.../proto/flowseer/integration/device/v1` and now imports
`.../proto/flowseer/edge/dispatch/v1` belongs earlier in its group, but a
rename done with `sed` or by an editor leaves it where the old path sat. Nothing
downstream notices: the package compiles, `go vet` is quiet, and the tests pass.
`golangci-lint` is the only gate that fails, and it fails in files the change
never opened on purpose:

```
src/edge/agent/internal/dispatch/registry.go:8:1: File is not properly formatted (goimports)
```

Two package moves in the same refactor hit this, 29 files the first time and 48
the second, every one of them a pure import-ordering diff.

## What to do

Reformat every Go file the move touched, and pass the repository's local prefix.
`.golangci.yml:76-78` sets it:

```yaml
goimports:
  local-prefixes:
    - go.aledante.io/FlowSeer
```

A bare `goimports -w` does not read that file, so it groups
`go.aledante.io/FlowSeer` with the third-party block and reports the file clean
while the gate still rejects it. The flag is not optional:

```bash
git diff --name-only <base>..HEAD | grep '\.go$' | grep -v '^generated/' | tr '\n' '\0' > "$TMPDIR/moved.txt"
xargs -0 gofumpt -w < "$TMPDIR/moved.txt"
xargs -0 goimports -local go.aledante.io/FlowSeer -w < "$TMPDIR/moved.txt"
```

The editor hooks in `tools/hooks/` run both formatters on a file they see
edited, which is why a hand-written change rarely meets this and a mechanical
one always does: the hook never saw the file.

## Evidence

- `.golangci.yml:76-78` sets `local-prefixes: go.aledante.io/FlowSeer`, and
  `.claude/skills/verify-change/scripts/verify-change.sh:388-398` runs
  `goimports -d` over the changed files.
- The envelope move's verifier run named
  `src/edge/agent/internal/dispatch/registry.go:8:1` and
  `src/services/device/internal/dispatchapi/subscribe_e2e_test.go:10:1` as not
  properly formatted after a `goimports -w` without the flag had already run
  over both and reported nothing.

## What this does not cover

It says nothing about which group an import belongs in, only that a moved path
changes its position within one. It also does not apply to `generated/`, which
`buf generate` writes and the lint gate excludes.
