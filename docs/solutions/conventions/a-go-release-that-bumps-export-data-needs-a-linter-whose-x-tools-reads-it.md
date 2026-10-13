---
title: A Go release that bumps the export-data version needs a linter whose x/tools reads it, and the linter's tool module keeps a go line no linted module is below
date: 2026-10-13
category: conventions
module: tools/golangci-lint
problem_type: bug
component: verify
severity: high
applies_when:
  - "A Go toolchain bump makes golangci-lint fail with `could not load export data` or `export data version N is greater than maximum supported version M`"
  - "Adding or editing a tool module run through `go tool -modfile=...`, such as tools/golangci-lint or tools/buf, and choosing its go line"
  - "Linting a module from inside its directory fails with `tools/<tool>/go.mod requires go >= X (running go Y)`"
related_components: [tools/buf, .agents/skills/verify-change]
tags: [golangci-lint, go-toolchain, export-data, go-tool, tool-module, verify]
symptoms:
  - "Every lint run fails with `typecheck: could not load export data ... export data version 5 is greater than maximum supported version 4` after the root module moved to go 1.27.2"
  - "Rebuilding the same golangci-lint release with the new toolchain fails the same way"
  - "With the tool module at go 1.27.2, linting the gnmitarget testdata module fails with `tools/golangci-lint/go.mod requires go >= 1.27.2 (running go 1.27.1)`"
root_cause: "golangci-lint reads dependency export data through golang.org/x/tools, and the x/tools version it links fixes the highest export-data version it can decode. Separately, go tool selects its toolchain from the go.mod of the working directory and then rejects a -modfile whose go line is newer than that toolchain."
resolution_type: fix
---

## The situation

The verifier used to run whatever `golangci-lint` sat on `PATH`. Once the
root module required `go 1.27.2`, golangci-lint v2.13.2 failed every lint run
with `export data version 5 is greater than maximum supported version 4`.
Rebuilding v2.13.2 with go1.27.2 did not help, as commit `b06c8bab` records:
the building toolchain does not decide which export data the linter reads.

## Why: x/tools decides the readable version

golangci-lint loads every non-initial package from the compiler's export data
(`pkg/goanalysis/runner_loadingpackage.go:254` and `:272` in v2.13.2):

```go
	r, err := gcexportdata.NewReader(f)
	...
	tpkg, err := gcexportdata.Read(r, pkg.Fset, view, pkg.PkgPath)
```

and turns a failure into the error the verifier shows (`:342`):

```go
				Msg:  fmt.Sprintf("could not load export data: %s", err),
```

`gcexportdata.Read` reaches `pkgbits.NewPkgDecoder`
(`internal/gcimporter/ureader.go:78`), which refuses newer data in
`golang.org/x/tools/internal/pkgbits/decoder.go:85`, identical in v0.49.0 and
v0.50.0:

```go
	if pr.version >= numVersions {
		panic(fmt.Errorf("cannot decode %q, export data version %d is greater than maximum supported version %d", pkgPath, pr.version, numVersions-1))
```

Only `internal/pkgbits/version.go` differs. v0.49.0 ends at `V4` and sets
`numVersions = iota` on line 40. v0.50.0 adds (lines 40 to 44):

```go
	// V5: encodes the index of methods to preserve relative order
	// of nongeneric and generic methods (go.dev/issue/81188).
	V5

	numVersions = iota
```

golangci-lint v2.13.2 requires `golang.org/x/tools v0.49.0` and v2.14.0
requires `v0.50.0` (line 156 of each release's `go.mod`). So the fix is a
linter release that links the newer x/tools. Commit `274b3726` pins v2.14.0 in
`tools/golangci-lint/go.mod`, and the verifier runs it as
`go tool -modfile=<root>/tools/golangci-lint/go.mod golangci-lint`
(`tools/scripts/verify/change.py:928`).

## Why the tool module's go line stays at 1.27

`go tool` picks its toolchain from the go.mod in the working directory, never
from `-modfile`. `modGoToolchain` in `cmd/go/internal/toolchain/select.go:527`
(go1.27.2) reads `modload.FindGoMod(wd)` and returns that file's `go` and
`toolchain` lines. After switching, loading the `-modfile` checks its own go
line against the running toolchain and fails with the message from
`cmd/go/internal/gover/toolchain.go:84`:

```go
	return fmt.Sprintf("%v requires go >= %v (running go %v%v)", e.What, e.GoVersion, Local(), explain)
```

The verifier lints each module from inside its directory (`cwd=module`,
`tools/scripts/verify/change.py:915`).
`src/protocol/yang/test/integration/testenv/testdata/gnmitarget/go.mod` says
`go 1.26.5`, so the local toolchain runs there. Checked on 2026-10-13 on
darwin/arm64 with a local go1.27.1, from that directory:

```text
$ go version
go version go1.27.1 darwin/arm64
$ go tool -modfile=<copy with go 1.27.2>/go.mod golangci-lint version
go: <copy>/go.mod requires go >= 1.27.2 (running go 1.27.1)
$ go tool -modfile=tools/golangci-lint/go.mod golangci-lint version
golangci-lint has version 2.14.0 built with go1.27.1 ...
```

A go line of `go 1.27` admits every toolchain that can read the repository's
modules. `tools/buf/go.mod` follows the same rule.

## How to apply

- On an export-data version error after a Go bump, find the golangci-lint
  release whose `go.mod` requires an x/tools with the new `pkgbits` version,
  and bump `tools/golangci-lint/go.mod` to it. Do not rebuild the old release.
  The verifier asks for `--full` when that module changes.
- Keep a tool module's go line at or below the go line of every module it
  runs in. If an upgrade raises it, lower it again before the full verifier
  run.

## What it does not cover

A linter that type-checks dependencies from source instead of export data
does not hit the first failure. Language features the linter's `go/types`
rejects are a different error (`tooNew` in `runner_loadingpackage.go:30`).
