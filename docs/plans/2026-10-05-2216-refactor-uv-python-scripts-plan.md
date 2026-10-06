---
title: Python Scripts Under uv - Plan
type: refactor
date: 2026-10-05
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Python Scripts Under uv - Plan

## Goal

A host needs one runtime, uv, to run every hook, the verifier, and every
skill script, on macOS, Linux, and Windows. The means: the host-side shell
(about 5,400 lines once the embedded Python is set aside) is ported to
Python, the existing Python moves with it under `tools/scripts/` behind one
entry point, and hooks, verifier, and skills share one library there.

**Stop condition:** a hook or gate cannot be started as `uv run` by an agent
runtime the repository supports (Claude Code or Codex) on one of the three
platforms. The entry point and the registration shape in U3 are then wrong.

## Branch

Every phase's work collects on the branch
`worktree-agent-a661d9cff38d208c7`, based on `83069173`. Each stage of
each phase commits there, and no stage branch is merged after its stage.
The work is merged once, at the end. The state file has no field for the
branch yet, so this section holds it.

## Decisions

The [repository scripting record](../architecture/2026-10-05-repository-scripting-direction.md)
holds the rules that outlive this plan: the runtime, the `tools/scripts/`
layout, the single entry point, the policy surfaces, what stays outside,
and the alternatives that lost. It is proposed and binds nothing until a
person accepts it. The decisions below restate the user's choices or are
local to the work.

- Python launched by uv, chosen over TypeScript on Node or Bun. Why: 9,200
  lines of Python and its tests already exist and stay, so the port covers
  the shell only. (decided by the user, 2026-10-05)
- The frontend keeps Node and pnpm. Why: the goal is one runtime for
  scripts and hooks, and moving the web toolchain is a separate risk with an
  unverified premise. (decided by the user, 2026-10-05)
- The research scripts under `docs/research/network-domain-atlas/_raw/` are
  left as they are. Why: nothing invokes them. (decided by the user,
  2026-10-05)
- Scripts are normalized into one directory and hooks reuse the utilities
  there. Why: scripts already import each other across skills by path
  (`.agents/skills/tune/scripts/field.py:16`). (decided by the user,
  2026-10-05)
- That directory is `tools/scripts/`, with one entry point `run.py`. Why:
  `tools/` already holds `hooks`, `deps`, and `check-guarantees`, and one
  entry point declares the interpreter once and needs no import-path edits.
  Unconfirmed, repeated under Open questions.
- `src/edge/netpen/layers/harvest.py` is not touched. Why: it is already a
  uv script, and scapy is the second source its fixtures need (`AGENTS.md`,
  Investigation discipline).
- Each port is proven by tests that ran against the shell first. A phase
  that replaces a script first makes its test suite take the command under
  test as a parameter, runs the suite against the shell script, then against
  the Python command, and deletes the shell only when both pass. Why: the
  hooks deny and allow edits, and a port that changes a verdict is a policy
  change nobody asked for. `tools/hooks/tests/run.sh` is itself shell, so
  its cases are ported to unittest before the hooks are.
- No shim is left behind. A ported script's old path is deleted in the
  phase that ports it, and every reference moves in the same change. Why:
  `AGENTS.md`, Agent behavior, rules out compatibility paths.
- Subprocesses are started with an argument list, never through a shell
  string. Why: a shell string needs a shell, which is the dependency this
  plan removes, and quoting differs between platforms.
- No Python linter or formatter is added. The verifier compiles every
  module and runs the unittest suites, as it does today. Why: a linter is a
  new dependency with its own statement. Unconfirmed, repeated under Open
  questions.
- Seven phases, cut along what they touch. Why: the foundation has to land
  first, the hooks and the verifier are policy surfaces that a person
  reviews edit by edit, and the package-local scripts share no file with the
  rest.

## Requirements

1. Every host-side command starts as `uv run tools/scripts/run.py <group>
   <command>`. Example: `uv run tools/scripts/run.py hook worktree-guard`
   with a `PreToolUse` event on standard input prints the same decision
   JSON `tools/hooks/worktree-guard.sh` prints for that event.
2. An unknown command fails loudly. Example: `uv run tools/scripts/run.py
   hook no-such-hook` exits 2 and prints the registered names.
3. No tracked shell script remains outside the paths the record exempts.
   Example: adding `tools/scripts/x.sh` fails the layout gate with the path
   named. `src/protocol/snmp/test/integration/testdata/snmpd/pass-scripts/endofmibview.sh`
   passes.
4. No instruction or configuration starts Python as `python3` or `python`.
   The scanned set is every file under `.agents/`, `.claude/`, `.codex/`,
   and `tools/`, every `Taskfile.yml`, `AGENTS.md`, `CONTRIBUTING.md`, and
   `docs/runbooks/`. Prose that describes history (`docs/architecture/`,
   `docs/solutions/`, `docs/research/`) is outside it. Example: a
   `SKILL.md` line `python3 .claude/skills/land/scripts/merge-check.py`
   fails the gate. `uv run tools/scripts/run.py land merge-check` passes.
5. Hook verdicts are unchanged by the port. Example: every case of
   `tools/hooks/tests/run.sh`, ported to unittest, passes against the shell
   hook and against the Python hook before the shell hook is deleted.
6. The verifier's verdicts are unchanged by the port. Example:
   `test_verify_paths.py` and `test_ledger.py` pass against
   `verify-change.sh` and against the Python verifier.
7. No module under `tools/scripts/` imports a POSIX-only module at import
   time. Example: `fcntl` in today's
   `.agents/skills/delegate/scripts/runlog.py` is reached only through
   `lib/lock.py`, which selects `msvcrt` on Windows.
8. uv has a minimum version and the interpreter a fixed minor version.
   Example: a host whose uv is older
   than `required-version` in `uv.toml` gets `Required uv version` and a
   non-zero exit from any command.
9. A command runs inside the Claude Code sandbox. Example: `uv run
   tools/scripts/run.py list` exits 0 from a sandboxed Bash call, where
   today it exits 2 with `Failed to initialize cache`.
10. The hooks, the verifier, and the test suites pass on Windows without
    Git Bash. Example: on a Windows host with uv, git, and Go installed,
    `uv run tools/scripts/run.py test` reports no failure and a hook
    registered in exec form denies an edit under `generated/`.

## Out of scope

- The frontend toolchain. Node and pnpm stay, and
  `frontend/web/pnpm-lock.yaml` is untouched.
- Scripts the record exempts: in-container scripts, vendored files under
  `spec/`, and the research scripts.
- Orca worker lanes on Windows. The delegate scripts drive `orca`, `claude`,
  `codex`, `agy`, and `omp`. U5 ports the scripts. Whether those CLIs run on
  Windows is unverified and not this plan's to fix.
- Symbolic links in the checkout. `CLAUDE.md` and `.claude/skills` are
  links, and a Windows checkout needs `core.symlinks` for them. The scripts
  stop depending on the second link once they live under `tools/scripts/`.
  The links themselves stay.
- The scripts read hook events from the agent runtime and files from the
  repository's own tree, written by contributors. Both authors are trusted.
  Text a command reads from a device, a capture, or a worker's screen stays
  data, as it is today.

## Units

### U1. Entry point, shared library, and uv version

Files: docs/plans/2026-10-05-2216-refactor-uv-python-scripts-phase1-plan.md

`tools/scripts/run.py` dispatches registered commands, `lib/` holds the
first shared modules, uv is version-bound and usable in the sandbox, the verifier
runs the suites under `tools/scripts/tests/`, and the Python convention doc
exists. One existing check moves as the pilot. Claims requirements 2, 8,
and 9.

### U2. Existing Python moves under `tools/scripts/`

Files: docs/plans/2026-10-05-2216-refactor-uv-python-scripts-phase2-plan.md

Every Python script under `.agents/skills/*/scripts/` becomes a registered
command with its tests, the plan-state reader and unit headings are one
module in `lib/`, and the POSIX-only calls are behind `lib/`. Claims
requirement 7.

### U3. Hooks

Files: docs/plans/2026-10-05-2216-refactor-uv-python-scripts-phase3-plan.md

The hooks under `tools/hooks/` and their test suite are Python under
`tools/scripts/hooks/`, registered in exec form in `.claude/settings.json`
and in `.codex/hooks.json`. The policy-surface paths in `AGENTS.md` and in
the policy hook name the new locations. The shell verifier still runs in
this phase, so its references to `tools/hooks/` move here too
(`verify-change.sh:297`, `:336`, `:929` to `:933`, and `:1043` to `:1050`,
where a missing `tree-state.sh` is skipped without a word and the dirty
marker then re-marks verified files). Claims requirements 1 and 5.

### U4. Verifier

Files: docs/plans/2026-10-05-2216-refactor-uv-python-scripts-phase4-plan.md

`verify-change.sh` and `tools/test/service-otel-integration.sh` are Python
under `tools/scripts/verify/`, and every document that names the verifier
command names the new one. Claims requirement 6.

### U5. Delegate, drive, and tune scripts

Files: docs/plans/2026-10-05-2216-refactor-uv-python-scripts-phase5-plan.md

`orca-worker.sh`, `pool-usage.sh`, `successor.sh`, `bench.sh`,
`discover-host.sh`, `check-tools.sh`, and `hitl-loop.sh` are registered
commands, and the Python they embedded in here-documents is ordinary module
code.

### U6. Package-local scripts

Files: docs/plans/2026-10-05-2216-refactor-uv-python-scripts-phase6-plan.md

The two bench gates, the four lab scripts under `deploy/lab/`, and
`capture-snmprec.sh` are standalone uv scripts in place, and the Go tests,
task files, and runbooks that run or read them follow.

### U7. Layout gate and Windows run

Files: docs/plans/2026-10-05-2216-refactor-uv-python-scripts-phase7-plan.md

A conformance gate fails a host-side shell script or a bare `python3`
invocation, `jq` and `shellcheck` leave the verifier's required tools,
`CONTRIBUTING.md` names uv as the one script runtime, and the suites and
hooks run on a Windows host. Claims requirements 3, 4, and 10.

Waves: U1 | U2 U6 | U3 U5 | U4 | U7

## Verification

- `uv run tools/scripts/run.py test` runs every suite under
  `tools/scripts/tests/` and reports the count.
- `uv run tools/scripts/run.py verify change --full` passes on the merged
  result of the last phase.
- `go test ./test/conformance/...` passes with the layout gate in place.
- `git ls-files '*.sh'` lists only the exempt paths.
- On a Windows host: the three commands above, and one denied edit under
  `generated/` in a Claude Code session.

## Definition of done

- [ ] Every phase plan reads `implemented` and its `Landed:` line is filled.
- [ ] The verifier is green for every changed path of each phase.
- [ ] `AGENTS.md`, `CONTRIBUTING.md`, `docs/agent-steering.md`, and every
      `SKILL.md` name the new commands, changed in the phase that moved
      them.
- [ ] The repository scripting record carries a `Landed` line for each
      phase.
- [ ] This plan's `status` is set, with an outcome note under its title.
- [ ] No plan labels in code.

## Open questions

- Is `tools/scripts/` with a single `run.py` the layout wanted, or separate
  executable files that import `lib/` through a path prelude? Unconfirmed.
  The plan takes the single entry point.
- Should a Python linter and formatter gate the scripts? Unconfirmed. The
  plan adds none. Adding one later is a dependency statement and one gate.
- How does Codex start a hook command on Windows? Unverified.
  `.codex/hooks.json` holds POSIX shell strings today. U3 decides the
  registration once this is read from the Codex documentation or source.
- Who runs requirement 10? No Windows host is reachable from the
  development machines. U7 needs a person with one.
- Where do the statements for uv and the interpreter live?
  `docs/dependencies/statements/` holds `go/` and `npm/` only, and the
  dependency admission work that records "other inputs" has not landed. U1
  pins both and leaves the statement to that work unless it has landed by
  then.
