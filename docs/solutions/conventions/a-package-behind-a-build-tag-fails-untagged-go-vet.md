---
title: A Package Behind a Build Tag Fails Untagged go vet as a Constraint Exclusion
date: 2026-09-25
last_verified: 2026-09-25
category: conventions
module: .claude/skills/verify-change
problem_type: bug
component: conformance-gates
severity: medium
symptoms:
  - "verify-change.sh fails during targeted Go package verification with: build constraints exclude all Go files in <path>"
  - "Untagged go vet ./path/to/pkg exits with code 1 while go vet -tags=<tag> ./path/to/pkg succeeds"
root_cause: "verify-change.sh executes run go vet \"${targets[@]}\" without build tags before running vet_tagged. When all source files in a package declare a build constraint tag, untagged go vet finds no matching source files and fails with a constraint exclusion error."
resolution_type: workaround
applies_when:
  - "Investigating a verify-change.sh failure where untagged go vet fails on a build-tag-gated package with 'build constraints exclude all Go files'"
  - "Structuring a Go package where every source file lives behind a build tag constraint (such as netpen_t1, netpen_t2, or snmp_integration_t4)"
  - "Deciding the authoritative verification gate for a tagged test tier when targeted verification reports an untagged vet failure"
related_components: [netpen, snmp]
tags: [build-tags, go-vet, verification, verify-change, testing, false-positive]
---

# A package behind a build tag fails untagged go vet as a constraint exclusion

## The situation

When a Go package isolates every source file behind a build constraint tag, untagged `go vet` fails. In `src/edge/netpen/test/integration/lab/`, every source file (`iosxe.go`, `iosxe_test.go`, `lab.go`, `lab_test.go`) begins with:

```go
//go:build netpen_t2
```

Targeted verification (`.claude/skills/verify-change/scripts/verify-change.sh:511-512`) compiles and vets targeted packages in two passes:

```bash
run go vet "${targets[@]}"
vet_tagged "${targets[@]}"
```

The first invocation passes `${targets[@]}` directly to `go vet` without tags. When `targets` includes `go.aledante.io/FlowSeer/src/edge/netpen/test/integration/lab`, `go vet` encounters a package where no file matches default build constraints:

```
== Go module: src/edge/netpen ==
+ go build ./...
Targeted packages: 1 (changed: go.aledante.io/FlowSeer/src/edge/netpen/test/integration/lab)
+ go vet go.aledante.io/FlowSeer/src/edge/netpen/test/integration/lab
package go.aledante.io/FlowSeer/src/edge/netpen/test/integration/lab: build constraints exclude all Go files in /Users/aledante/orca/workspaces/FlowSeer/netpen-t2-compound/src/edge/netpen/test/integration/lab
FlowSeer verification FAILED (exit 1) in gate: go vet
```

The command exits 1. `verify-change.sh` reports failure in the Go module gate even though the subsequent `vet_tagged` pass and the tagged test suites both pass.

## Why it bites

The error message mimics a broken package or misplaced source files. An agent seeing `build constraints exclude all Go files` may assume the package structure is invalid, attempt to remove the tag, or introduce dummy untagged files.

The failure is an artifact of running untagged `go vet` against a package whose files are completely conditional. While `go test` without tags simply reports `[no test files]` and exits 0, `go vet` treats a package directory with zero eligible source files as a hard failure.

## The authoritative gate

The authoritative gate for a tag-gated package or test tier is the command run with its explicit tags:

```bash
go -C src/edge/netpen vet -tags=netpen_t2 ./test/integration/...
go -C src/edge/netpen test -race -short -tags=netpen_t2 ./test/integration/...
```

When verifying changes in a tag-gated package (such as `netpen_t1`, `netpen_t2`, or `snmp_integration_t4`), verify that the tagged commands exit 0. If `verify-change.sh` flags an untagged constraint exclusion on that package, confirm that all files in the package intentionally share the tag and treat the tagged invocation as the passing check.

## What this does not cover

This does not apply to packages that contain a mix of tagged and untagged files (for instance, platform-specific files like `src/edge/netpen/link/link_linux.go` and `link_other.go`). In mixed packages, untagged `go vet` evaluates the files matching default platform tags and catches real defects.
