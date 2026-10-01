---
title: Dependency Admission Phase 1, Inventory, Statements, and the Cut List - Plan
type: chore
date: 2026-10-01
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
parent: docs/plans/2026-10-01-1457-chore-dependency-admission-plan.md
---

# Dependency Admission Phase 1, Inventory, Statements, and the Cut List - Plan

## Goal

`go run ./tools/deps inventory` lists every dependency version the tree
pins, with its hash, its criteria, and the direct dependencies that pull it
in. Every direct dependency has a statement a person has ruled on as `keep`
or `cut`, and a conformance test fails when one is missing. pnpm waits 14
days before it resolves a new version. The means: a lockfile reader and two
lookups in `tools/deps`, 86 statement files, and four pnpm settings.

**Stop condition:** a module that a build or test loads has no zip line in
its module's `go.sum`. The lockfiles then cannot say which versions carry
code, and the parent's scope decision goes back to `plan`.

## Decisions

The parent plan's Decisions and the
[dependency admission record](../architecture/2026-10-01-dependency-admission-direction.md)
apply. Local ones:

- The lockfile layer reads files only. Classification is a second layer
  that runs `go`. Why: the statement gate needs only the first, and the Stop
  hook has no network.
- Classification runs `go` with `GOWORK=off` and `-mod=readonly`, as
  `docs/conventions/guarantees.md` prescribes. Why: `GOFLAGS=-mod=mod go
  list -m all` adds `/go.mod` hash lines to `go.sum`. A `go` failure fails
  the command and names the module.
- A Go module is `deploy` when it supplies a package to `go list -deps`
  (without `-test`) of the shipping packages for `GOOS` `linux`, `darwin`,
  or `windows`. The shipping packages are those under `src/` in the root
  module and in `src/edge/netpen`, minus any package with a `test` path
  element and minus the generator commands `src/protocol/snmp/cmd/mibgen`,
  `src/protocol/yang/cmd/yanggen`, and
  `src/protocol/smi/internal/catalog/gen`. `tools/` and the bench,
  differential, and `gnmitarget` modules ship nothing. Every code-bearing
  entry of `generated/go/yang/go.sum` is `deploy` without loading its
  packages. An entry of the root or netpen `go.sum` that appears in no
  closure, test closures included, is `deploy`. Everything else is `run`.
  Why: the record counts libraries as shipping and generators as tooling,
  one host's `go list` misses files behind other build constraints
  (`docs/solutions/conventions/go-list-deps-misses-imports-behind-build-tags.md`),
  the YANG bindings are too large to load whole, and an unexplained entry
  gets the stricter review.
- A module named by a `tool` directive is direct: `tools/buf/go.mod`
  requires `github.com/bufbuild/buf` only as `// indirect` and runs it.
- An npm package is `deploy` when it is in the lockfile closure of the
  importer's `dependencies`, and `run` otherwise. A snapshot key such as
  `@vue-flow/core@1.48.2(vue@3.5.43(typescript@6.0.3))` names the package
  `@vue-flow/core` at `1.48.2`. The peer suffix selects an install variant
  of the same bytes.
- The advisory lookup posts to `https://api.osv.dev/v1/querybatch` in
  batches of 200 with ecosystem `Go` or `npm`. A Go version is sent without
  its leading `v`. Checked on 2026-10-01: that form returned `GO-2026-6443`
  for `google.golang.org/grpc` `1.84.0`, and batches of 200 covered all 206
  Go and 686 npm versions. Whether a Go pseudo-version matches in that form
  is unverified. A `next_page_token` in a result is followed
  (https://google.github.io/osv.dev/post-v1-querybatch/).
- The publication date comes from
  `https://proxy.golang.org/<escaped path>/@v/<version>.info` field `Time`
  for Go and from `https://registry.npmjs.org/<name>` field
  `time[<version>]` for npm. The path is escaped with
  `golang.org/x/mod/module.EscapePath`, from a module the root `go.mod`
  already requires. https://go.dev/ref/mod defines the Go `Time` as the
  commit time, so the record's caveat about a backdated release applies.
- The same `.info` answer carries the commit a Go version resolved to. On
  2026-10-01 the answer for `github.com/google/uuid` `v1.6.0` held
  `"Origin":{"VCS":"git","URL":"https://github.com/google/uuid","Ref":"refs/tags/v1.6.0","Hash":"0f11ee6918f41a04c201eceeadf612a377bc7fbc"}`.
  https://go.dev/ref/mod documents only `Version` and `Time` and says more
  fields may be added, so `Origin` is optional and its absence is printed.
- A statement's safety section states what is known without a source
  review: the publisher, the pinned version's age, the advisory result with
  its date, and the tree size. It ends with "Source not yet reviewed.",
  which the parent's U5 replaces with a pointer to the record.
- `approved` in a statement is the date a person ruled on its verdict, for
  `cut` as well as `keep`. It is empty until then, and this phase's gate
  accepts an empty value so the tree stays green while the ruling is
  pending. A worker writes statements with proposed verdicts and stops. The
  coordinator asks through the question tool: each proposed cut is its own
  question, and the keeps are ruled on as one list per ecosystem, shown as a
  table with tree sizes. The coordinator then writes the dates. The parent's
  U3 makes the date required.

## Requirements

1. The inventory lists exactly the code-bearing versions. Example: for a
   `go.sum` holding the zip and `/go.mod` lines of `github.com/google/uuid`
   `v1.6.0` and only the `/go.mod` line of `github.com/go-logr/logr`
   `v1.2.2`, it lists one entry with hash
   `h1:NIvaJDMOsjHA8n1jAhLSgzrAzy1Hgr+hNrb57e+94F0=`.
2. The inventory lists each lockfile package once with its integrity.
   Example: the two keys `reka-ui@2.10.5` and
   `reka-ui@2.10.5(vue@3.5.43(typescript@6.0.3))` yield one entry whose hash
   starts `sha512-/8o0y9BThE0hcHKFg+3770tW`.
3. A dependency reached only through test files is `run`, one reached
   through a non-test file is `deploy`, and one behind `//go:build windows`
   is `deploy` on any host.
4. `go run ./tools/deps tree` prints, per direct dependency, how many
   versions it pulls in and how many only it pulls in.
5. `go run ./tools/deps advisories` prints one line per advisory with the
   dependency, version, id, summary, and affected import paths, and exits
   non-zero only when a lookup fails. Example: `GO-2026-6443` for
   `google.golang.org/grpc` `v1.84.0`.
6. `go run ./tools/deps age` prints each version's publication date, marks
   those under 14 days, and prints the origin commit of a Go version.
   Example: `reka-ui` `2.10.5`, published `2026-09-21T13:18:23.018Z`, is
   marked until 2026-10-05, and `github.com/google/uuid` `v1.6.0` prints
   `0f11ee6918f41a04c201eceeadf612a377bc7fbc`.
7. Parent requirement 5 without the date: every direct requirement has a
   statement with three non-empty sections and a verdict, `approved` is
   empty or a date, and no statement exists without a direct requirement.
8. Parent requirement 10.

## Out of scope

- Records, the baseline, and the version gate (parent U3).
- Removing anything, or changing `frontend/web/package.json` or a `go.mod`.
- Hardening against hostile input: the parent's Out of scope names whose
  files `tools/deps` reads. The two lookups read responses from
  `api.osv.dev`, `proxy.golang.org`, and `registry.npmjs.org` over TLS. A
  malformed response is an error, and a well-formed false one is not
  defended against.

## Units

### U1. Inventory library and command

Files: tools/deps/main.go, tools/deps/main_test.go, tools/deps/inventory/gosum.go, tools/deps/inventory/gomod.go, tools/deps/inventory/pnpmlock.go, tools/deps/inventory/classify.go, tools/deps/inventory/tree.go, tools/deps/inventory/inventory.go, tools/deps/inventory/*_test.go, tools/deps/inventory/testdata/
After: none
Change: Package `inventory` finds every `go.mod` under the root (skipping
`.git`, `node_modules`, `.claude/worktrees`, and `.codex/worktrees`, as
`verify-change.sh` does, so the module under
`src/protocol/yang/test/integration/testenv/testdata/gnmitarget` is
found) and `frontend/web/pnpm-lock.yaml`, and returns entries of ecosystem,
name, version, hash, the manifests that hold it, and whether it is direct.
Direct means a `require` line without `// indirect`, a module a `tool`
directive names, or a key of `dependencies` or `devDependencies`. A
requirement on `go.aledante.io/FlowSeer` or a path under it is the
repository itself and is skipped. `Classify` adds criteria and `via` by
running `go list -deps`, `go list -deps -test`, and `go mod graph` per
module and by walking the lockfile's `snapshots`. The command has the
subcommands `inventory` and `tree`, each with `--json`, and follows
`tools/check-guarantees/main.go` for flag handling and exit codes.
Tests: `gosum_test.go` and `pnpmlock_test.go` cover requirements 1 and 2
with fixtures cut from the real files. `classify_test.go` covers requirement
3 with a module the test assembles in `t.TempDir()` from files under
`testdata/classify`, its three dependencies local directories joined by
`replace`: one imported from a non-test file, one from a `_test.go` file,
one from a file under `//go:build windows`. The fixture's module file is
stored as `go.mod.fixture`, because discovery and the verifier both treat
any `go.mod` in the tree as a module. `tree_test.go` covers requirement 4 on a hand-written graph where one
transitive entry is reachable from two direct ones. `TestDiscoversEveryModule`
runs discovery on the repository and fails when its set differs from what
`git ls-files '*go.mod'` reports, so a dropped or doubled module is seen.
Nothing in this unit proves the `deploy` rule correct for the real tree.
The implementer's report lists every module of the root and netpen that
the inventory calls `run`, each with one file that imports it, for a
person to scan.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- tools/deps`

### U2. Advisory and publication-date lookups

Files: tools/deps/lookup/osv.go, tools/deps/lookup/age.go, tools/deps/lookup/*_test.go, tools/deps/lookup/testdata/, tools/deps/main.go, tools/deps/main_test.go
After: U1
Change: Package `lookup` takes inventory entries and returns advisories and
publication dates, with the base URLs as parameters. `Advisories` batches
the query, follows page tokens, and fetches
`https://api.osv.dev/v1/vulns/<id>` once per distinct id for the summary and
the `affected[].ecosystem_specific.imports[].path` values. `Age` fetches one
date per entry. The command gains `advisories` and `age`. Both use the
offline layer of U1 only.
Tests: fixtures under `testdata/` are response bodies saved from the live
services, each beside a text file holding the `curl` command that produced
it: the batch answer for `google.golang.org/grpc` `1.84.0`,
`golang.org/x/crypto` `0.57.0`, and `reka-ui` `2.10.5` (one id, one id,
none), the `GO-2026-6443` record, the `.info` answer for
`github.com/google/uuid` `v1.6.0` (`2024-01-23T18:54:04Z`, commit
`0f11ee69…`), and the registry document for `reka-ui`. Tests serve them from `httptest` and assert the
parsed ids, import paths, dates, and origin commit, an `.info` answer
without `Origin` reported as such, the `v` stripped from the Go version in
the request body, a path with capitals escaped (`github.com/Azure/go-ansiterm`
requested as `github.com/!azure/go-ansiterm`), a 500 answer returned as an
error, and a page token followed. The tests need a listener, so they and
the Verify line run unsandboxed. Nothing here shows that OSV matches a
pseudo-version. The implementer queries one with a known advisory and
records the result in the package's doc comment, or reports none found.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- tools/deps`

### U3. Convention document and entry points

Files: docs/conventions/dependencies.md, AGENTS.md, docs/README.md, CONTRIBUTING.md
After: U1, U2, U5
Change: `docs/conventions/dependencies.md` states the rules that bind from
this phase on, each with a link to the record for its reason: a direct
dependency is added only after a person approves its statement, a pin moves
only for one of the three reasons, a version waits 14 days, and the target
is never the newest release. It gives the statement format with
`github.com/google/uuid` as the worked example, and the four `tools/deps`
commands with their output. It says that records and the version gate do
not exist yet. `AGENTS.md` gains one entry in its Conventions list.
`docs/README.md` gains a row for the convention, a row for
`docs/dependencies/`, and a bullet under "Where a new document belongs"
that sends a statement there. `CONTRIBUTING.md` points at the convention
where it names the toolchain. `AGENTS.md` is a policy surface: the edit
prompts the person, and a worker's brief leaves it to the coordinator.
Tests: the verifier's link check and prose check over the four files.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/conventions/dependencies.md AGENTS.md docs/README.md CONTRIBUTING.md`

### U4. pnpm resolution settings

Files: frontend/web/pnpm-workspace.yaml
After: none
Change: the file gains `minimumReleaseAge: 20160`, `trustPolicy:
no-downgrade`, `blockExoticSubdeps: true`, and `strictDepBuilds: true`, each
with a one-line reason, beside the existing `allowBuilds`. The setting names,
units, and defaults are from https://pnpm.io/settings/dependency-resolution
and https://pnpm.io/settings/build as fetched on 2026-10-01: the age is in
minutes, and the last two are already the default in pnpm 11 and are written
out so a default that changes cannot loosen them. When `trustPolicy` refuses
a locked version, the unit stops and reports the version. An entry under
`trustPolicyExclude` is an exclusion a person approves by itself
(`AGENTS.md`, Hard boundaries), and it names an exact version.
Tests: `pnpm install --frozen-lockfile` in `frontend/web` succeeds,
unsandboxed. In a copy of `frontend/web` under `$TMPDIR`, `pnpm add
--lockfile-only <name>@<version>` for a version `tools/deps age` marks as
under 14 days is refused. Whether a frozen install re-checks age or trust
for locked versions is unverified. The implementer reports what pnpm does
and which pnpm ran (`pnpm --version` printed 12.6.0 on the host while
`package.json` pins `pnpm@11.25.0`).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/pnpm-workspace.yaml`

### U5. Statements and their gate

Files: docs/dependencies/README.md, docs/dependencies/statements/go/, docs/dependencies/statements/npm/, tools/deps/inventory/statements.go, tools/deps/inventory/statements_test.go, tools/deps/inventory/testdata/statements/, test/conformance/dependencies/statements_test.go
After: U1, U2
Change: one statement per direct dependency, at
`docs/dependencies/statements/<ecosystem>/<name>.md`, the name used as a
path (`go/github.com/google/uuid.md`, `npm/@vue-flow/core.md`). The
frontmatter holds `name`, `ecosystem`, `required_by` (the manifests),
`criteria`, `verdict` (`keep` or `cut`), and `approved`. The body has three
sections headed "Why it is required", "Why it is safe", and "Why not owned
code". The first names the packages that import it. The third says what
owned code would have to do and why that is worse, and gives the tree size
from `tools/deps tree`. A dependency whose third section does not hold gets
`verdict: cut`. `CheckStatements` returns a finding for a direct dependency
without a statement, a statement without a direct dependency, an empty
section, a `verdict` or `criteria` outside its values, and an `approved`
that is neither empty nor a date. `docs/dependencies/README.md` explains
the layout and holds no list. The count on 2026-10-01 is 86: 45 Go modules
required directly across the nine `go.mod` files, the buf tool, and 40 npm
packages. The files are disjoint and may be written in parallel.
Tests: `statements_test.go` in `tools/deps/inventory` runs `CheckStatements`
on a passing fixture tree and on one fixture per finding, each differing
from the passing tree in that one respect (manifests stored as
`go.mod.fixture` and assembled in `t.TempDir()`), as
`docs/solutions/conventions/a-refusal-test-needs-an-input-only-the-refusal-rejects.md`
requires. `TestDirectDependenciesHaveStatements` in
`test/conformance/dependencies` runs it on the repository and also fails
when it checked zero dependencies, per
`docs/solutions/conventions/a-gate-selected-by-name-stops-running-silently.md`.
Nothing checks that a statement's reasoning is true. The person rules on it.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/dependencies tools/deps test/conformance/dependencies`

Waves: U1 U4 | U2 | U5 | U3

## Verification

```sh
.claude/skills/verify-change/scripts/verify-change.sh -- tools/deps test/conformance/dependencies docs/dependencies docs/conventions/dependencies.md AGENTS.md docs/README.md CONTRIBUTING.md frontend/web/pnpm-workspace.yaml
go run ./tools/deps inventory | wc -l
go run ./tools/deps advisories
go run ./tools/deps age
git status --short
```

The lookups and the first `inventory` run on a cold module cache need the
network and run unsandboxed. `git status --short` after them shows no
`go.sum` change. Never run the verifier with `--full`.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] Every statement carries an `approved` date from the person's ruling.
- [ ] The report lists the `cut` verdicts, the advisories returned, and the
      versions under 14 days, for the parent's U2 and U3 to plan from.
- [ ] This plan's `status` is set with an outcome note under its title, and
      the parent's `Landed:` line for U1 holds the commit range.
- [ ] No plan labels in code.

## Open questions

- Whether OSV matches Go pseudo-versions sent without the `v` (U2 measures).
- Whether pnpm applies `minimumReleaseAge` and `trustPolicy` to a frozen
  install. If it does, `reka-ui` `2.10.5` blocks installs until 2026-10-05
  and U4 waits or lands after that date.
