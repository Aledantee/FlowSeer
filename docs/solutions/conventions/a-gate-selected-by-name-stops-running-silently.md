---
title: A Gate Selected By Name Stops Running Silently; Enumerate It and Count What Ran
date: 2026-09-15
category: conventions
module: tools/hooks
problem_type: convention
component: conformance-gates
severity: high
applies_when:
  - "Wiring a test package into a hook, a script, or CI by naming it, or selecting tests inside it with `go test -run`."
  - "Adding an entry to a list of checks that some runner iterates, where a rename elsewhere would not update the list."
  - "A gate reports success and you are deciding whether it ran."
  - "Reviewing a fix for a check that silently stopped checking, to see whether the fix removed the failure or only moved it one level up."
related_components: [verify-change, stop-hook]
tags: [conformance-gate, go-test, hooks, silent-failure, merge-gate]
---

`go test -run` exits 0 when its pattern matches nothing:

```
$ go test -run TestNameThatDoesNotExist ./test/conformance/panic/
ok      go.aledante.io/FlowSeer/test/conformance/panic  0.289s [no tests to run]
```

So a runner that selects a gate's tests by name stops running that gate the day
somebody renames or splits it, and reports success while doing so. The same is
true one level up for a runner that selects a gate's package by path: a listed
path that no longer exists is skipped, usually by a `[ -d ... ] || continue`
written for a fixture that legitimately lacks it.

The panic gate hit this at four axes in one change, each fix exposing the next:
the test name, the package directory name, a package nested one level below a
listed child, and the root `test/conformance/` directory itself. A hook test
guarding the first axis could not catch the second, because its fixture
hardcoded the same name the list did, so list and fixture drifted together.

## The rule

Name nothing that can be renamed. Enumerate the tree and count what ran, so a
gate that vanishes is a failure rather than a silent pass.

```bash
for package in "$root"/test/conformance/*/; do
  [ -d "$package" ] || continue        # unmatched glob, not a per-gate skip
  if ! output=$(cd "$root" && go test "./${package#"$root/"}/..." 2>&1); then
    gates_ok=false
    break
  fi
  gates_run=$((gates_run + 1))
done
```

Each part earns its place. Running the package whole removes the `-run` axis.
The trailing `/...` reaches a gate that lands one level down, and a child holding
no Go package then fails loudly with `no packages to test` rather than counting
as a pass. The surviving `[ -d ]` covers only the unmatched literal glob when
`test/conformance/` is absent, which a fixture or an older tree can be. And
`gates_run` is what closes the root axis: zero gates in a checkout of this
repository, which a root `go.mod` identifies, is itself a failed gate.

`tools/hooks/stop-check.sh:26-45` carries this reasoning in full, including the
axes, so the next person to add a gate does not reintroduce a list.

## The same defect wears a second costume in the verifier

`verify-change.sh` forces repository-wide gates into a targeted run, because a
change that violates one of them touches none of those packages and the importer
fixpoint never selects them. That force-append was a hard-coded trio, and it had
already fallen behind: `test/conformance/dependencies` existed and was not in it,
so a targeted run passed what `--full` and the Stop hook both refused. It was
also nested inside a root-module guard, so a change in `src/edge/netpen` or any
bench module got no gate at all, which matters precisely because the panic gate
walks those modules by path and is the only thing that does.

Both are the same lesson: the selection is now `./test/conformance/...`, run once
per targeted verification from the repository root
(`.claude/skills/verify-change/scripts/verify-change.sh:478-498`).

## How to tell whether a gate runs

Do not read the wiring. Plant the violation the gate exists to catch, in a leaf
package so the importer fixpoint does not pull in hundreds of packages and drown
the signal, and run the entry point you are asking about. A gate that does not
name your file and line did not run, whatever its exit status said.

## What this does not cover

Enumeration makes every subdirectory of `test/conformance/` a gate at every entry
point, so a gate added there inherits the Stop hook's latency budget without
anyone deciding that. Measured on 2026-09-15 on darwin/arm64, the three gates cost
about 3.1s warm and 3.6s after a source edit against a 30s hook timeout, which a
concurrently loaded machine can push to roughly 23s.
