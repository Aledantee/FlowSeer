---
title: Move generated YANG bindings into a nested module - Plan
type: perf
date: 2026-09-26
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
amends: docs/plans/2026-08-20-1245-feat-yang-protocol-libraries-plan.md
---

# Move generated YANG bindings into a nested module - Plan

## Goal

A verifier run for a change outside the YANG stack no longer compiles the
1,051 packages under `generated/go/yang` (199 MB of source). `generated/go/yang`
becomes its own Go module with the same module path prefix, so every import
path stays unchanged and the root module's `go build ./...` and `go vet ./...`
stop descending into it. Stop condition: if a production package under `src/`
already imports most of the binding tree, the root build would compile it
anyway and the split buys nothing; today no non-test file does.

## Evidence

- The Go build cache on the development host reached about 400 GB. Entries of
  1.4 GB and 330-350 MB were written daily from 2026-09-23 to 2026-09-25, and
  only this tree produces objects that large.
- `.claude/skills/verify-change/scripts/verify-change.sh` runs
  `go build ./...` on the root module on every Go change (line 453, and at
  lines 572-574 for dependent modules). `./...` includes `generated/go/yang`.
- Without `-trimpath`, the cache key includes the package directory.
  `go list -export -f '{{.Export}}' ./src/common/errs` returned different
  entries in this worktree and in `~/Projects/FlowSeer` (`975f…` and `be7d…`)
  and the same entry with `-trimpath` (`7177…`). With 14 worktrees, each one
  stored its own copy per flag set. A user-level `GOFLAGS=-trimpath` was tried
  and reverted: `runtime.Caller` then returns module-relative paths, so the
  tests that locate the repository from their own file broke
  (`test/conformance/dependencies/no_as_test.go` among them). This plan removes
  the compile itself.
- Importers outside the tree: `src/protocol/gnmi/watch_test.go` (untagged,
  one package, `cisco-iosxe/openconfiginterfaces`), plus four files behind the
  `yang_integration_t4` tag under `src/protocol/{netconf,restconf,gnmi}/test/integration/`.
- Generated files import only `go.aledante.io/FlowSeer/src/protocol/yang`
  (651 import sites, no third-party imports).

## Decisions

- The module path is `go.aledante.io/FlowSeer/generated/go/yang`. Why: import
  paths stay the same, so no importer or generated file changes.
- Split the module rather than filter `generated/go/yang/...` out of the
  verifier's package list. Why: the filter fixes only the verifier, and a plain
  `go test -race ./...` would still compile all 1,051 packages. Future
  production consumers will import a few vendor models each, and with the split
  only those models enter the root build.
- `yanggen` writes `generated/go/yang/go.mod` and produces its `go.sum` by
  running `go mod tidy` in the output directory after emitting. Why:
  `generated/` is generator-owned, and a hook denies hand-edits there.
  `yanggen` is not `buf`, so it can own the whole output including the module
  files. The go.mod carries `replace go.aledante.io/FlowSeer => ../../..`, as
  `src/edge/netpen/go.mod` does.
- The root `go.mod` requires the nested module with
  `replace go.aledante.io/FlowSeer/generated/go/yang => ./generated/go/yang`.
  Why: the unit test and the tagged lab tests keep their imports, and a build
  compiles only the binding packages it actually imports. Go allows the
  resulting mutual requirement. The alternative, moving those tests into
  another nested module, splits one package's tests across two modules for no
  gain.
- The verifier selects a dependent module only when a changed root package is
  in that module's `go list -deps ./...`. Why: under today's rule, every root
  change would build and vet the new module, which costs the same as now. The
  listing loads packages without compiling them.
- A module under `generated/` gets `go build ./...` in the verifier. It gets no
  race run, and lint covers only the three sample packages the
  generated-code-style plan names (`ruckus-icx/openconfigvlan`,
  `ruckus-icx/openconfigsystem`, `aruba-cx/openconfignetworkinstance`). Why:
  a full-tree lint ran for over an hour (`docs/agent-observations.md`,
  2026-09-25), and the tree has no tests.
- `generated/go/mib` (33 packages, 37 MB) and `generated/go/proto` (41
  packages, 6.2 MB) stay in the root module. Why: they are about 5% of the YANG
  tree's size, and production code under `src/` imports them, so a split would
  remove no compile work.

## Requirements

1. The root module's `go list ./...` contains no package under
   `generated/go/yang`. Example: `go list ./... | grep -c generated/go/yang`
   prints `0`.
2. `go test ./src/protocol/gnmi` passes and compiles only
   `cisco-iosxe/openconfiginterfaces` from the tree. Example:
   `go list -deps -test ./src/protocol/gnmi | grep generated/go/yang` lists
   that one package.
3. `go -C generated/go/yang build ./...` succeeds from a clean regeneration.
   Example: `go run ./src/protocol/yang/cmd/yanggen -update` followed by
   `git status --short generated/go/yang` shows no diff.
4. For a change to `src/common/errs/errs.go`, the verifier does not build the
   YANG module. Example: its output has no `== Dependent module: generated/go/yang` line.
5. For a change to `src/protocol/yang/schema.go`, the verifier builds the YANG
   module. Example: its output shows `== Dependent module: generated/go/yang`
   followed by `go build ./...`.
6. For a changed file under `generated/go/yang`, the verifier runs
   `go build ./...` in that module and lints only the three sample packages.

## Out of scope

- CI-side generation or not committing the tree.
- Splitting `generated/go/mib` or `generated/go/proto` (see Decisions).
- Cache size limits or scheduled `go clean`.

## Units

### U1. Generator emits the module; root requires it
Files: `src/protocol/yang/cmd/yanggen/` (main.go, a new module-file emitter,
golden tests), `generated/go/yang/go.mod`, `generated/go/yang/go.sum`,
`go.mod`, `go.sum`
After: none
Change: after emitting packages, `yanggen -update` writes `go.mod` (module
path, root `go` directive, `require go.aledante.io/FlowSeer v0.0.0-00010101000000-000000000000`,
the replace to `../../..`) and runs `go mod tidy` in the output directory.
`yanggen -check` also reports drift in `go.mod`. The root `go.mod` gains the
require and replace for the nested module, and `go mod tidy` at the root
keeps them.
Tests: a golden test for the emitted `go.mod` text. A `-check` case where a
hand-altered `go.mod` fails with exit 1. Requirements 1-3 run as commands.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/yang/cmd/yanggen go.mod go.sum`

### U2. Verifier selection for the nested module
Files: `.claude/skills/verify-change/scripts/verify-change.sh`,
`tools/hooks/tests/run.sh`
After: U1
Change: dependent modules are selected by the dependency intersection in
Decisions. A module whose directory is under `generated/` gets build-only
treatment with sample-package lint. Changed `.go` files under `generated/`
still select no root targets, but they do select their owning module when it
is nested.
Tests: `tools/hooks/tests/run.sh` selection cases for Requirements 4-6, plus
a netpen case: a change to a root package netpen imports still selects
`src/edge/netpen`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/verify-change/scripts/verify-change.sh tools/hooks/tests/run.sh`

### U3. Conformance gates and docs
Files: `test/conformance/panic/panic_policy_test.go`,
`test/conformance/dependencies/no_as_test.go`, `src/protocol/README.md`,
`docs/code-style.md`, `src/protocol/yang/cmd/yanggen/doc.go`
After: U1
Change: the panic gate's nested-module walk still skips `generated/`. The
`no_as` dependency gate lists the new `go.mod`. `src/protocol/README.md` names
`generated/go/yang` as a nested module and says why. The regeneration line in
`docs/code-style.md` states that `yanggen` also tidies the module.
Tests: `go test ./test/conformance/...` passes, and the panic gate reports no
file under `generated/go/yang`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- test/conformance src/protocol/README.md docs/code-style.md src/protocol/yang/cmd/yanggen/doc.go`

Waves: U1 | U2 U3

## Verification

- Requirements 1-6 as listed.
- `go test -race ./src/protocol/...` at the root.
- `go vet -tags yang_integration_t4 ./src/protocol/netconf/test/integration ./src/protocol/restconf/test/integration ./src/protocol/gnmi/test/integration`
  compiles the lab tests against the nested module.
- Do not run `verify --full`, because it builds and race-tests the whole tree.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `src/protocol/README.md` and `docs/code-style.md` updated in the same change.
- [ ] This plan's `status` set, with an outcome note under the title.
- [ ] No plan labels in code or commit messages.

## Open questions

- Whether `yanggen` should shell out to `go mod tidy` or compute `go.sum`
  itself. Shelling out is the default. Change it only if the generator's
  golden tests cannot run hermetically with the shell-out.
