---
title: Move the Simulator Tree to src/common/sim - Plan
type: refactor
date: 2026-10-01
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-plan.md
---

# Move the Simulator Tree to src/common/sim - Plan

> Implemented. 2 units, 2026-10-02T08:46Z to 2026-10-02T08:52Z.

## Goal

Every simulator package sits at the path the
[package shape record](../architecture/2026-10-01-simulation-package-shape-direction.md)
gives it, and every document names that path. The means is `git mv` plus a
rewrite of import paths and three package names. One function body changes:
the corpus's walk over the tree, which reads the directory layout.

Stop condition: if a move creates an import cycle that only a code change
can break, the target tree is wrong for that package. Stop and report the
cycle instead of editing code to fit.

## Decisions

- The parent's Decisions apply
  (`docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-plan.md`).
- One unit moves all Go packages. Why: every move rewrites imports in the
  same files, so two move units could never run at once.
- Packages that do not exist yet are not created: `sim/layer` (the contract
  types), `sim/device` (the interface), `sim/device/host`, and
  `sim/medium/cable`. Why: each arrives with its code in a later phase. The
  directories `sim/layer/` and `sim/device/` exist here only as parents.
- Trace layer names, rule identifiers, fact type identifiers, and issue
  codes keep their strings (`"vswitch.mcast_membership"`, `"fabric.runtime"`,
  `"netmodel.skipped.unsupported_facet"`). Why: they are behaviour, and this
  phase changes none. Phase 2 owns them.
- The edge tool's own identifiers follow its rename: binary `simload`, build
  tags `simload_lab` and `simload_linktest`, error codes `simload/...`, and
  the report contract `simload/report/v1`. Why: a tool named `simload` that
  reports as `netsimload` is the half-rename the breaking-change rule
  forbids.
- Proposed records are edited in place. An accepted record gets a dated line
  under its Amendments heading. Why: `docs/architecture/README.md` says an
  accepted record changes only by amendment.
- The corpus's diff-coverage gate follows the layout. Why:
  `diffGoPackagePaths` in `internal/netsimtest/diffcoverage.go` walks the
  switch directory and its immediate children, and `diffCoveredPackages`
  lists twelve paths. After the move the layers are no longer children of
  the switch, so `TestEveryDiffPackageIsCovered` fails unless the walk
  changes. The invariant it guards stays: every package with a `diff.go` is
  listed, and every listed package has one.
- `CONTRIBUTING.md` keeps its example commit subjects
  (`feat(netsim/fabric): ...`). Why: they quote history.

## Requirements

R1. The mapping below is applied exactly.

| From | To | Package clause |
| --- | --- | --- |
| `src/common/netsim/vswitch/port` | `src/common/sim/port` | unchanged |
| `src/common/netsim/vswitch/{phy,bridge,lag,stp,loopprotect,mcast,routing,filter,traffic}` | `src/common/sim/layer/<same>` | unchanged |
| `src/common/netsim/vswitch/netmodel` | `src/common/sim/netmodel` | unchanged |
| `src/common/netsim/vswitch` (its own files) | `src/common/sim/device/vswitch` | unchanged |
| `src/common/netsim/internal/netsimtest` | `src/common/sim/internal/simtest` | `simtest` |
| `src/common/netsim/{trace,analysis,stream,fabric,search}` | `src/common/sim/<same>` | unchanged |
| `src/common/netsim` (`README.md`, `fact_contract_test.go`) | `src/common/sim` | `sim_test` |
| `src/edge/netsimload` (with `packetio`, `test/integration`) | `src/edge/simload` | `simload` |
| `src/edge/netsimload/cmd/netsimload` | `src/edge/simload/cmd/simload` | `main` |

Example: `go list ./src/common/sim/...` prints 19 packages, among them
`.../sim/layer/stp` and `.../sim/device/vswitch`, and `ls src/common/netsim`
fails.

R2. No simulator behaviour changes. Example: `git diff -M --stat` for the Go
move shows only renames, import lines, package clauses, the `simtest.` and
`simload.` qualifiers, build tags, the `simload` strings named in Decisions,
and the diff-coverage walk.

R3. No current reference names an old path. Example: `grep -rnE
'common/netsim|edge/netsimload|netsim/(vswitch|fabric|trace|analysis|stream|search|internal)'
--include='*.go' --include='*.md' src docs CONCEPTS.md GOALS.md | grep -v
'^docs/plans/'` prints only these: the amendment lines that state the
rename, the body of the accepted schema-building-blocks record (which an
amendment corrects), and the package shape record, whose Context and
Alternatives describe the tree before the move.

R4. The suites that passed before pass after. Example: `go test
./src/common/sim/... ./src/edge/simload/...` reports `ok` for the 22
packages that report `ok` at `61775c73`.

## Out of scope

- Any change to an exported name, a signature, or a file's contents beyond
  R2. The capability contract is phase 2.
- `.agents/skills/tune/references/calibration/`: its tasks check out fixed
  base commits where the old paths exist.
- Splitting or renaming files inside a package.

## Units

### U1. Move the Go packages
Files: src/common/netsim/, src/common/sim/, src/edge/netsimload/, src/edge/simload/
After: none
Change: the packages sit at the paths of R1. Children of `vswitch` move
first (`port`, the nine capability packages, `netmodel`), then `vswitch`
itself, then the leaves and the root, then the edge tool. Import paths are
rewritten across both trees, longest prefix first so
`netsim/vswitch/port` is rewritten before `netsim/vswitch`. Package clauses
change for `netsimtest` (and `netsimtest_test`), `netsim_test`, and
`netsimload` (and its `_test` packages), with their qualifiers at every use.
Build tags `netsimload_lab` and `netsimload_linktest`, the `spawn.Go` name
in `run.go`, the usage line in `cmd/.../main.go`, the two `errs.NewCode`
strings and the hint in `packetio/sender.go`, and `reportContract` in
`report.go` take the `simload` name. Comments that name an old path are
updated in the same pass. In `internal/simtest/diffcoverage.go`,
`diffCoveredPackages` lists `src/common/sim/device/vswitch`,
`src/common/sim/port`, the nine `src/common/sim/layer/<name>` packages, and
`src/common/sim/fabric`, and the walk covers `device/vswitch`, `port`,
`fabric`, and each immediate child of `layer/`. Its doc comments and the
temporary-tree cases in `diffcoverage_test.go` follow. One commit per row group of R1 keeps each diff
readable.
Tests: no new test. `go build ./... `, `go vet`, and `go test` over
`./src/common/sim/... ./src/edge/simload/...` pass. `GOOS=linux go vet
./src/edge/simload/...` and `go vet -tags simload_lab
./src/edge/simload/test/integration/` compile the tagged files, and
`GOOS=linux go vet -tags simload_linktest ./src/edge/simload/packetio`
compiles the link tier, which needs both. They are run because
`docs/solutions/conventions/go-list-deps-misses-imports-behind-build-tags.md`
records that an untagged build skips them.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim src/edge/simload`

### U2. Name the new paths in the documentation
Files: src/common/sim/README.md, src/common/sim/*/README.md, src/common/sim/layer/*/README.md, src/common/sim/device/vswitch/README.md, src/common/sim/netmodel/README.md, src/common/sim/internal/simtest/README.md, src/edge/simload/README.md, src/edge/simload/test/integration/README.md, src/common/README.md, src/edge/README.md, src/common/net/lacp/README.md, spec/proto/flowseer/net/protocol/stp/v1/README.md, CONCEPTS.md, GOALS.md, docs/architecture/README.md, docs/architecture/2026-09-10-virtual-device-direction.md, docs/architecture/2026-09-16-local-network-analysis-direction.md, docs/architecture/2026-09-18-offered-load-streams-direction.md, docs/architecture/2026-09-10-network-simulation-prior-art-research.md, docs/architecture/2026-09-09-mutation-shadow-projection-direction.md, docs/architecture/2026-09-25-schema-building-blocks-direction.md, docs/solutions/README.md, docs/solutions/architecture-patterns/*.md, docs/solutions/conventions/*.md
After: U1
Change: every path, package table, and relative link names the tree of R1.
`src/common/sim/README.md` carries the package table of the new tree, with
the `../net/` codec rows unchanged, and corrects its last paragraph: the
corpus is imported by the diff-coverage tests of every layer package and of
`fabric`, not only by `vswitch_test` and `netmodel_test`. The virtual-device
record gains an amendment dated with the landing day that states the move,
names the package shape record, and lists `filter`, `loopprotect`, and
`stream`, which its Decision omits. The schema-building-blocks record, which
is accepted, gains one amendment line stating the rename. The other records
listed are proposed or research and are edited in place. The 19 solution
files that name a simulator path are updated by path only. Their `applies_when`
frontmatter is updated where it holds a path, and a `component:` value of
`netsim` becomes `sim`.
Tests: `python3 .agents/skills/prose/scripts/check-prose.py` on each changed
Markdown file reports no new finding against its state at `61775c73`. The
grep of R3 prints only amendment lines. Every relative link in the moved
READMEs resolves (`../net/igmp` from `src/common/sim/README.md` still does,
since the depth is unchanged. Links from `layer/<name>/README.md` to the
switch README change depth.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim src/edge/simload src/common/README.md src/edge/README.md CONCEPTS.md GOALS.md docs/architecture docs/solutions spec/proto/flowseer/net/protocol/stp/v1/README.md src/common/net/lacp/README.md`

Waves: U1 | U2

## Verification

```bash
go build ./... 2>&1 | grep -v '^generated/' ; go vet ./src/common/sim/... ./src/edge/simload/...
go test -race ./src/common/sim/... ./src/edge/simload/... ./test/conformance/...
golangci-lint run ./src/common/sim/... ./src/edge/simload/...
grep -rn 'common/netsim\|edge/netsimload' --include='*.go' src ; test $? -eq 1
```

`go build ./...` compiles `generated/go/yang` and is slow. The verifier's
targeted run over the changed paths is the gate, never `--full`.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] R1 to R4 hold, each checked with its example.
- [ ] The parent's `Landed:` line for U1 holds this phase's commit range.
- [ ] This plan's `status` is set, with an outcome note under its title.
- [ ] No plan label in code, comments, or commit messages.

## Open questions

- `spec/proto/flowseer/net/protocol/stp/v1/README.md` and
  `src/common/net/lacp/README.md` matched a search for `vswitch/`. Confirm
  each names a simulator path before editing it.
