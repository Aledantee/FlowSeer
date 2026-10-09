---
title: A Conformance Gate That Lists Files Through a Child Process Reports a Cached Pass Unless It Reads the Directories Itself
date: 2026-10-09
last_verified: 2026-10-09
category: conventions
module: test/conformance/scripting
problem_type: convention
component: conformance-gates
severity: medium
applies_when:
  - "Writing a Go test under `test/conformance/` that learns which files exist from a child process such as `git ls-files`."
  - "A conformance gate passes with `(cached)` after a file it should reject was created."
  - "Adding a rule to a gate that runs at Stop, where the hook runs `go test` without `-count=1`."
related_components: [stop-hook, verify-change]
tags: [conformance-gate, go-test, test-cache, git-ls-files, stop-hook]
---

`go test` caches a pass and reruns only when an input it recorded changes. It
records the files a test opens and the directories it reads, not the output of
a child process. A gate that asks `git ls-files` what exists therefore keeps
its cached pass after a forbidden file appears, and the Stop hook, which runs
`go test` without `-count=1`, reports that pass.

## Measured

2026-10-09 on darwin/arm64, Go 1.27.1, in a scratch module whose test fails
when `git ls-files --others` lists `x.sh`:

```text
$ go test ./...
ok  	probe	0.179s
$ go test ./...
ok  	probe	(cached)
$ touch x.sh; go test ./...
ok  	probe	(cached)
```

The same sequence in this tree with the scripting gate, adding
`tools/scripts/zz-probe.sh` between two runs without `-count=1`, failed on
the second run with the file named.

## Rule

Make the directory listing an input. `os.ReadDir` on a directory puts its
entry names into the cache key, so a file created there reruns the test. Read
the repository root and every directory on the path of each listed file:

```go
for _, dir := range pathDirs(files) {
	if _, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir))); err != nil {
		t.Fatalf("reading directory %q: %v", dir, err)
	}
}
```

Fail the gate when `git` fails or lists nothing, since a skipped gate reads as
a pass (see
[a gate selected by name](a-gate-selected-by-name-stops-running-silently.md)).

## Evidence

- `test/conformance/scripting/scripting_test.go:112-121`: the reads and the
  comment that states why. `:105-108`: the empty listing is fatal.
  `:171`: `pathDirs`.
- `tools/scripts/hooks/stop_check.py:83`: the Stop hook runs
  `go test ./test/conformance/<name>/...` with no `-count=1`.
- `tools/scripts/verify/change.py:1053`: the verifier runs the gate with
  `-count=1`, which catches what the reads cannot.
- The plan's Decisions (`docs/plans/2026-10-05-2216-refactor-uv-python-scripts-phase7-plan.md`)
  cite the Go source for the cache key: `cmd/go/internal/test/test.go:2056`
  to `:2115` in Go 1.27.1.

## What it does not cover

One case stays cached: a file created in a directory that held no listed file,
when its parent's entries do not change, such as a directory holding only
ignored files. The verifier's `-count=1` run and any Stop after a listed
change catch it. A gate that walks the tree with `filepath.WalkDir` already
reads every directory and needs none of this.
