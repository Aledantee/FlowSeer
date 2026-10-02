---
title: Dependency Admission - Direction
type: direction
date: 2026-10-01
topic: dependency-admission
status: proposed-direction
---

# Dependency Admission - Direction

FlowSeer runs code it did not write: 206 Go module versions across nine
`go.sum` files and 686 npm package versions in
`frontend/web/pnpm-lock.yaml`, plus container base images, two buf modules,
two remote buf plugins, the Go toolchain, and pnpm. None of it has a recorded
review. This record fixes how a version of someone else's code gets into the
tree, how it stays there, and what a reader can check about it.

## Context

Lockfiles already pin bytes. A `go.sum` line holds a hash of the module zip,
and the Go command refuses a download that does not match it
(https://go.dev/ref/mod, "Authenticating modules"). A `pnpm-lock.yaml` entry
holds a `sha512` integrity value. What the tree lacks is a statement that
anyone looked at those bytes, a reason each dependency exists, and a rule for
when a pin may move.

Three facts from outside the repository shape the rules:

- Most published supply-chain attacks are caught within days. Of ten attacks
  surveyed in
  https://blog.yossarian.net/2025/11/21/We-should-all-be-using-dependency-cooldowns,
  eight had a window under one week, and a 14-day wait would have missed only
  xz-utils.
- The forge is not the artifact. `github.com/boltdb-go/bolt` served a
  backdoored version from the Go module proxy for over three years while its
  GitHub tag pointed at clean code
  (https://socket.dev/blog/malicious-package-exploits-go-module-proxy-caching-for-persistence).
- Reviewing each version from scratch does not scale, and reviewing the diff
  from an already reviewed version does. cargo-vet records both kinds of
  audit and starts a project from a list of exemptions that shrinks
  (https://mozilla.github.io/cargo-vet/how-it-works.html).

## Decision

### A review is bound to a name, a version, and a content hash

The unit of trust is one version's bytes. A record names the dependency, the
version, and the hash the lockfile holds for it. A version change is a new
unit and inherits nothing: the new version needs its own record before it
merges.

For Go the set is every `go.sum` line that carries a zip hash. A line ending
in `/go.mod` with no zip line beside it pins a `go.mod` file read during
version selection, and no source of that version is downloaded or compiled,
so it needs no record. For npm the set is every key under `packages:` in the
lockfile.

### A pin is a hash wherever the input allows one

A version number is a name its publisher can point elsewhere, and a hash is
not. Each kind of input is pinned by the strongest hash it supports:

| Input | Pin | Also recorded |
| --- | --- | --- |
| Go module | zip hash in `go.sum` | the commit the version resolved to |
| npm package | `sha512` integrity in the lockfile | none, a registry tarball has no checkout |
| Container image | `@sha256:` digest | the tag it was read from |
| buf module | commit and digest in `buf.lock` | none |
| Source fetched in a build step (`git clone`) | commit hash | the tag |
| Python package in a fixture image | `--require-hashes` | the version |

A Go requirement cannot name a commit once that commit has a version tag:
a commit query "selects that version" (https://go.dev/ref/mod, "Version
queries"). The zip hash is stronger than a commit hash in any case, since it
covers the bytes the build reads. The record still holds the commit the
proxy reports for the version, so a tag that later moves on the forge shows
as a mismatch. An operating-system package installed by `apt-get` in a test
image has no practical hash pin, and the image digest of the result is not
reproducible either. Those lines are recorded as unpinned, by name.

### Two review criteria

`deploy` applies to code that ships: a Go module that supplies a package to
the non-test build of a library, service, or edge package under `src/` or
`generated/`, and an npm package in the lockfile closure of `dependencies`.
Packages under a `test/` directory, bench modules, generator commands such
as `mibgen` and `yanggen`, and everything under `tools/` do not ship.
Libraries count although no binary links some of them yet, because services
are assembled from them. The reviewer reads the whole source on first use and the
whole diff on every upgrade.

`run` applies to everything else: test, bench, generator, lint, and build
tooling. The reviewer looks for behavior that harms the machine it runs on:
install scripts, network connections, process execution, reads of
credentials or environment, writes outside the working tree, and encoded or
obfuscated payloads. The definitions follow cargo-vet's `safe-to-deploy` and
`safe-to-run`
(https://mozilla.github.io/cargo-vet/built-in-criteria.html).

A `run` review does not look for logic defects. A bug in a linter can
therefore go unseen. Malicious behavior on the workstation is what it is for.

### The review reads the bytes that build

A Go review reads the module cache directory that `go mod download` verified
against `go.sum`. An npm review reads the registry tarball whose integrity
matches the lockfile. A review of the project's repository on its forge
proves nothing about either.

### Known advisories are checked per version, and each hit is ruled on

Every record carries the date of its last advisory lookup against OSV
(https://google.github.io/osv.dev/post-v1-querybatch/) and a ruling for each
advisory returned. OSV matches on version, so it reports advisories for
packages FlowSeer never imports. On 2026-10-01 the pinned versions returned
two: `GO-2026-5932` for `golang.org/x/crypto` (the `openpgp` packages, which
nothing here imports) and `GO-2026-6443` for `google.golang.org/grpc`
v1.84.0 (the xDS server path, which nothing here imports). A ruling states
which case holds and why.

### A version waits 14 days before it may be adopted

No version is adopted until 14 days after its publication. A version that
fixes an advisory ruled as affecting FlowSeer may be adopted sooner, with
the same review and with the advisory named in its record.

pnpm enforces the wait at resolution through `minimumReleaseAge`
(https://pnpm.io/settings/dependency-resolution). Go has no such setting.
The Go module proxy reports a version's `Time`, which
https://go.dev/ref/mod defines as the commit time, and a commit time is
whatever its author wrote. The Go wait is therefore a convention checked at
review, and the source review is what catches a backdated release.

### A pin moves only for a recorded reason

An upgrade names one of three reasons: it fixes an advisory, it carries a
fix or feature the code needs, or the pin is stale. A pin is stale when the
newest version past the wait is at least 90 days newer than the pinned one.
The target is the newest version past the wait with no open advisory, never
the newest release.

### A dependency states why it exists, and a person approves it

Each direct dependency has a statement with three parts: why it is required,
why it is safe, and why owned code does not do the job. A person approves the
statement before the manifest changes. The dependencies that predate this
record are approved in one ruling over the whole list, with each proposed
removal ruled on by itself. A transitive dependency names the
direct dependencies that pull it in and carries its own per-version record.
The size of the tree a direct dependency pulls in counts against it in its
own statement.

### Existing dependencies are cut before they are baselined

Statements are written for every existing direct dependency first. Those
whose third part does not hold are removed. What remains is recorded as
unreviewed, and that list only shrinks: shipped code first.

### Other inputs are pinned by digest and recorded

A container base image is referenced by digest. A buf module is pinned by
`buf.lock`, a remote plugin by version and revision, the Go toolchain by a
`toolchain` line with `GOTOOLCHAIN=local` in the verifier, and pnpm by
`packageManager`. Each has a statement. An image has no source to read, so
its record says who publishes it and stops there.

## Alternatives

- Vendoring Go modules with `go mod vendor` would make every version change
  a readable diff in the repository. It copies about 79 MB for the root
  module alone and duplicates what `go.sum` hashes already pin. The record
  bound to the hash gives the same guarantee without the copy.
- `govulncheck` reports only advisories on functions the code calls
  (https://go.dev/doc/security/vuln/), which would spare the two rulings
  above. It is itself a dependency tree this policy would then have to
  review, it does not cover npm, and a ruling written once per advisory
  costs less than that tree.
- A capability scanner such as https://github.com/google/capslock would list
  which Go packages reach the network or execute processes. It was left out
  for the same reason. A reviewer greps for the same imports.
- A cooldown of 7 days covers eight of the ten surveyed attacks. 14 covers
  nine, and nothing here needs a release in its first two weeks.
- A full source read for dev tooling would cover 481 dev-only npm versions at
  the depth of shipped code. It was rejected as roughly five times the review
  volume for code that never ships.

## Consequences

- Adding or upgrading a dependency costs a review and a record. That cost is
  the point: it is paid before the code runs.
- A security fix still needs its diff read. The wait is the only step it
  skips.
- The check proves a statement and a record exist and match the lockfile. It
  cannot prove who approved a statement. Approval rests on the edit prompt
  for the statements directory and on review.
- The advisory lookup sends package names and versions to `api.osv.dev`. The
  Go module proxy and the npm registry already receive the same names when
  the packages are fetched.
- Until the unreviewed list is empty, FlowSeer runs code nobody here has
  read. The list makes that visible and countable.
