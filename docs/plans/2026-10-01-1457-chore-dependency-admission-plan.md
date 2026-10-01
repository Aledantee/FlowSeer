---
title: Dependency Admission - Plan
type: chore
date: 2026-10-01
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
---

# Dependency Admission - Plan

## Goal

Every version of third-party code FlowSeer builds or runs has a record bound
to its content hash that says who reviewed it, at which depth, and what the
advisory lookup returned. Every direct dependency has an approved statement
of why it exists. A check fails any change that moves a pin without both.
The means: statements and a cut of what owned code can replace, then a
baseline of unreviewed versions that only shrinks, a conformance gate over
the lockfiles, and a skill that carries the add, upgrade, and review
procedures.

**Stop condition:** after the cuts in U2, the `deploy` set still holds
modules too large to read in full (the NATS server, the OpenTelemetry SDK)
and a `run` review is not accepted for them. The two criteria are then one
short, and the record shape and the gate change.

## Decisions

The [dependency admission record](../architecture/2026-10-01-dependency-admission-direction.md)
holds the rules that outlive this plan: what a review is bound to, the two
criteria, the advisory ruling, the wait, the reasons a pin may move, and the
alternatives that lost. It is proposed, and binds nothing until a person
accepts it. The decisions below restate the user's choices or are local to
the work.

- Existing dependencies are cut before they are baselined. Why: a removed
  dependency needs no review, and writing "why not owned code" for each one
  is what surfaces the removals. (decided by the user, 2026-10-01)
- Two review criteria, `deploy` and `run`, as the record defines them. Why:
  481 of the 686 npm versions are dev-only, and reading them at the depth of
  shipped code is about five times the volume. (decided by the user,
  2026-10-01)
- A direct dependency carries the full three-part statement and the
  approval. A transitive one names the direct dependencies that pull it in
  and carries its own per-version record. Why: "why not owned code" is a
  choice the repository makes only for what it requires directly. (decided
  by the user, 2026-10-01)
- Every input is pinned by a hash where it supports one, and a commit hash
  is preferred over a version number, as the record's pin table lays out.
  For a Go module the pin is the `go.sum` zip hash, since `go.mod` resolves a
  tagged commit back to its tag (https://go.dev/ref/mod, "Version queries"),
  and the record adds the commit the version resolved to. Why: a version is
  a name the publisher can move. (decided by the user, 2026-10-01)
- The wait is 14 days and a stale pin is one at least 90 days behind the
  newest version past the wait. Why: the record's survey for the first, and
  a quarterly rhythm for the second. Unconfirmed, repeated under Open
  questions.
- Statements live at `docs/dependencies/statements/<ecosystem>/<name>.md`
  and records at `docs/dependencies/records/<ecosystem>/<name>.json`, one
  file per dependency name. Why: reviewers work in parallel without sharing
  a file, and one name's file holds its versions in order, which is the
  chain an upgrade review diffs along. Statements are prose and run through
  the prose check. Records are data the gate parses with `encoding/json`.
- The tooling is `tools/deps`, a Go program in the root module beside
  `tools/check-guarantees`, with its parsing in an importable package
  `tools/deps/inventory`. It uses the standard library, `gopkg.in/yaml.v3`,
  and `golang.org/x/mod`, which the root `go.mod` already requires. Why: a
  dependency policy enforced by a new dependency defeats itself.
- The gate is `test/conformance/dependencies`, which already holds
  `no_as_test.go`. Why: `tools/hooks/stop-check.sh` and
  `.claude/skills/verify-change/scripts/verify-change.sh` run every package
  under `test/conformance/` whole, so the gate needs no new wiring.
- The gate is offline. It compares files in the tree. Publication dates and
  advisory results are fetched by `tools/deps` at review time and written
  into the record, where the gate checks them. Why: a gate that needs the
  network fails in the sandbox and at the Stop hook.
- `docs/dependencies/statements/` becomes a policy surface in
  `tools/hooks/pre-tool-policy.sh`, so an edit prompts the person. Why: it
  is the one place approval is written. The limit is stated in the record's
  Consequences.
- `frontend/web/package.json` declares exact versions. Why: a `^` range
  says the next resolve may pick a version nobody approved, and the manifest
  should read as what was approved.
- Five phases, cut along what they touch: the inventory tool and statements,
  the removals, the records and gate, the skill, and the review backlog.
  Why: U2's scope is unknown until the statements are ruled on, and U5 is
  volume work that needs U3's records and U4's procedure.

## Requirements

1. Every code-bearing version in a lockfile has a record with the same hash.
   Example: a `go.sum` line `example.com/y v1.0.0 h1:AAAA=` with no file
   `docs/dependencies/records/go/example.com/y.json` holding `v1.0.0` fails
   the gate with `example.com/y v1.0.0: no record`.
2. A record whose hash differs from the lockfile's fails. Example: the
   record for `github.com/google/uuid` `v1.6.0` holding `h1:BBBB=` while
   `go.sum` holds `h1:NIvaJDMOsjHA8n1jAhLSgzrAzy1Hgr+hNrb57e+94F0=` fails
   with both hashes named.
3. A record version no lockfile holds fails. Example: a record for
   `reka-ui` `2.10.4` after the lockfile moved to `2.10.5` fails as stale
   unless `2.10.4` is the `from` of the newer version's review.
4. A version may be `unreviewed` only when `docs/dependencies/baseline.txt`
   lists it. Example: a new `go.sum` line whose record says `unreviewed`
   and whose key is absent from the baseline fails.
5. Every direct requirement in a `go.mod` or in
   `frontend/web/package.json` has a statement with three non-empty
   sections, a `verdict`, and an `approved` date. Phase 1 lands the check
   with an empty `approved` allowed, and U3 requires the date. Example: a `require
   example.com/z v1.2.0` line with no statement fails with
   `example.com/z: no statement`.
6. A reviewed version's criteria are at least what its use requires.
   Example: a module the non-test build of the root module imports, recorded
   with a `run` review, fails.
7. A reviewed version was at least 14 days old at review, or its record
   names the advisory that justified the exception. Example: `published
   2026-09-21`, `reviewed 2026-09-28`, no exception: fails.
8. Every advisory OSV returns for a recorded version has a ruling.
   Example: `go run ./tools/deps advisories` exits non-zero for
   `google.golang.org/grpc` `v1.84.0` while its record has no ruling for
   `GO-2026-6443`.
9. A version of a name that has an older reviewed version records what it
   was diffed from and why the pin moved. Example: `reka-ui` `2.11.0` with
   `2.10.5` reviewed and no `from` fails.
10. pnpm refuses to resolve a version younger than 14 days. Example:
    `frontend/web/pnpm-workspace.yaml` holds `minimumReleaseAge: 20160`.
11. No input floats, and each is pinned by hash where the record's pin
    table gives one. Example: a `Dockerfile` line `FROM
    clixon/clixon-example:latest` fails for lack of a `@sha256:` digest, as
    do `"vue": "^3.5.43"` in `package.json` and `pip install
    snmpsim-lextudio` without `--require-hashes`.
12. A Go record holds the commit its version resolved to. Example: the
    record for `github.com/google/uuid` `v1.6.0` holds
    `0f11ee6918f41a04c201eceeadf612a377bc7fbc`, and a lookup that returns a
    different commit for `v1.6.0` fails.

## Out of scope

- Reviewing any dependency's source. U5 does that. U1 to U4 build what makes
  the review recordable.
- Vendored specifications under `spec/`. They are data with their own
  provenance files, and nothing executes them.
- The agent runtime's own tooling outside the repository (`npx ctx7@latest`
  in a personal rule file, globally installed plugins, `golangci-lint` on
  the host). They run unpinned code on the same machine and this plan does
  not reach them.
- A license review. The statement template has no license part.
- `tools/deps` reads `go.mod`, `go.sum`, `package.json`, and
  `pnpm-lock.yaml` from the repository's own tree, written by the Go and
  pnpm tools and committed by contributors. Their authors are trusted: the
  parsers are not hardened against hostile lockfiles. The versions those
  files pin are what the review distrusts.

## Units

### U1. Inventory, statements, and the cut list

Files: docs/plans/2026-10-01-1457-chore-dependency-admission-phase1-plan.md
After: none
Landed:

`tools/deps` lists every dependency version with its hash, criteria, and
the direct dependencies that pull it in, and looks up advisories. Every
direct dependency has a statement with a proposed verdict. pnpm's wait and
trust settings are on. Claims requirements 5, 8 (the lookup), 10, and 12
(the lookup).

### U2. Removals

Files: docs/plans/2026-10-01-1457-chore-dependency-admission-phase2-plan.md
After: U1
Landed:

Each dependency ruled `cut` in U1 is replaced by owned code or dropped, and
build-only npm packages move to `devDependencies`.

### U3. Records, baseline, and the gate

Files: docs/plans/2026-10-01-1457-chore-dependency-admission-phase3-plan.md
After: U1, U2
Landed:

Every remaining version has a record, the unreviewed ones are listed in the
baseline, and the gate enforces requirements 1 to 7, 9, 11, and 12. Images,
buf inputs, the toolchain, pnpm, and the fetches inside fixture Dockerfiles
are pinned by hash and recorded. The `tools/hooks/` edit lands here.

### U4. The dependency skill

Files: docs/plans/2026-10-01-1457-chore-dependency-admission-phase4-plan.md
After: U3
Landed:

A project skill carries the procedures: add, upgrade, review one version,
and the monthly pass that re-checks advisories and proposes stale pins.

### U5. Review backlog

Files: docs/plans/2026-10-01-1457-chore-dependency-admission-phase5-plan.md
After: U3, U4
Landed:

The baseline is reviewed down to empty, `deploy` versions first.

Waves: U1 | U2 | U3 | U4 | U5

The graph is a chain because each phase consumes what the one before
produced: verdicts, then the reduced tree, then records, then the
procedure.

## Verification

```sh
.claude/skills/verify-change/scripts/verify-change.sh -- tools/deps test/conformance/dependencies docs/dependencies
go test -race ./tools/deps/... ./test/conformance/dependencies/...
go run ./tools/deps advisories
```

The last command needs the network and is run unsandboxed. The verifier is
never run with `--full`, which builds all of `generated/go/yang` and
exhausts host memory.

## Definition of done

- [ ] Verifier green for every changed path of every phase.
- [ ] `docs/dependencies/baseline.txt` is empty.
- [ ] `docs/conventions/dependencies.md`, `AGENTS.md`, `docs/README.md`, and
      `CONTRIBUTING.md` describe the procedure in the change that lands it.
- [ ] The direction record is accepted by a person or amended.
- [ ] This plan's `status` is `implemented` with an outcome note under its
      title, and every `Landed:` line holds a commit range.
- [ ] No plan labels in code.

## Open questions

- The 14-day wait and the 90-day staleness bound are unconfirmed. They are
  parameters in one convention document and one pnpm setting, so a different
  answer changes two numbers.
- Whether `api.osv.dev` is acceptable for the advisory lookup under the
  rule that third-party services are self-hostable and deployed in the EU.
  The alternative is matching against OSV's downloadable data locally, which
  moves version-range matching into owned code.
- Other open plans change the dependency set:
  `docs/plans/2026-09-28-1844-refactor-web-component-contract-migration-phase2-plan.md`
  swaps `motion` for `motion-v` and adds `@vueuse/core`, and its phase 3
  adds `vue-i18n`. Whichever lands second writes or deletes the statements.
  Once the record is accepted, those additions need an approved statement
  like any other.
- The host runs pnpm 12.6.0 and `frontend/web/package.json` pins
  `pnpm@11.25.0`. Which one installs decides which settings documentation
  applies. U1 measures it.
