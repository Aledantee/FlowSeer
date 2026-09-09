# Agent observations

Entries here record where a FlowSeer skill, agent definition, or hook did not
fit the work: a correction the user had to make to the procedure, a step that
was skipped or done by hand, a brief that missed something, a sequence that
recurs with no skill behind it. The `compound` skill's Observe mode appends
them. Nothing reads this file on its own; the `steer` skill works the queue
when a maintainer asks for it.

`steer` applies or rejects each entry through the change process in
[`agent-steering.md`](agent-steering.md) and deletes it. Edits to a hook, a
hook registration, or `AGENTS.md` stop at a staged diff for a person's
review, since those are policy surfaces. An entry that sits here is not a
rule; the skill or hook it names stays authoritative until it changes. A
lesson about the code belongs under [`solutions/`](solutions/README.md),
not here.

Entry format:

```markdown
## <YYYY-MM-DD> <skill>: <one-line title>
Skill or agent: <path and step>, or `new skill candidate: <working name>`.
What happened: <what was corrected or did not fit, and whether the step
was followed as written>.
Suggested change: <smallest edit to the skill, agent, or hook>.
```

## Entries

## 2026-09-06 verify-change: the gate misses tagged files and a nested module
Skill or agent: `.claude/skills/verify-change/SKILL.md` and
`scripts/verify-change.sh`.
What happened: a breaking type change across the protocol libraries passed
the verifier on every changed path while two files did not compile —
`src/protocol/snmp/bench/macro_test.go` (behind `snmp_bench_macro`, in the
nested bench module) and `src/protocol/snmp/usm_parity_test.go` (behind
`snmp_parity`). The verifier builds untagged targets only, so nothing in the
default gate reaches either. Both were found later by sweeping build tags by
hand. Separately, one invocation whose paths spanned the main module and
`src/protocol/snmp/bench` put the bench package in the main module's target
list and failed with "main module does not contain package
go.aledante.io/FlowSeer/src/protocol/snmp/bench"; splitting the invocation per
module works. The steps were followed as written.
Suggested change: group changed paths by their nearest `go.mod` before
building the target list, and add a step that names the build tags touching
the changed packages (`grep -rh '^//go:build'`) and vets each one after a
change to an exported signature or field type.

## 2026-09-06 implement: Bash edits force a full verifier run
Skill or agent: `.claude/skills/implement/SKILL.md`, step 2 and Finish.
What happened: several unit edits went through `sed` and a Python
heredoc in Bash. The Bash hook recorded a `<Bash mutation; verify with
--full>` marker, so a documentation-only change ended in a full module
race run of about twenty minutes, and the first attempt aborted because
another worktree's golangci-lint was running. The step was followed as
written; nothing in it says which tool to edit with.
Suggested change: in step 2, say that edits go through the editor tools
and that a Bash write to a source file costs a `--full` run at Finish.

## 2026-09-09 verify-change: a new proto file fails the breaking gate
Skill or agent: `.claude/skills/verify-change/scripts/verify-change.sh`,
the `proto` block.
What happened: a targeted run over two newly added files under
`spec/proto/flowseer/net/capture/v1/` failed with `Failure: no .proto files
were targeted.` from `buf breaking --against '.git#branch=master' --path
<file> --path <file>`. Both files are new on the branch, so neither exists in
the baseline, and `--path` then selects nothing in the against-ref. The check
has nothing to say about them either way: `buf.yaml` ignores the whole
`spec/proto/flowseer` module for breaking until the first stable release.
Two smaller edges in the same block: a directory argument is dropped, since
the collector matches `*.proto` and tests `-f`, so `-- spec/proto/<pkg>` runs
no schema gate at all and still reports "verification passed"; and the phase
plan's own Verification line (`-- spec/proto docs CONCEPTS.md generated`) is
written that way.
Suggested change: skip `buf breaking` when every targeted path is absent from
the against-ref, or drop `--path` for that one command and let `buf.yaml`'s
ignore do the selecting. Separately, expand a directory argument to the
`.proto` files under it, or fail loudly when a passed path selects no gate.

## 2026-09-09 plan: a net package's unit missed the executable import order
Skill or agent: `.claude/skills/plan/SKILL.md`, step 3, and the unit that adds
a package under `spec/proto/flowseer/net/`.
What happened: a phase plan added `flowseer/net/capture/v1` and had one unit
amend the package tree and import order in
`docs/architecture/2026-08-20-network-model-structure-direction.md`. That
record says its own order's "home for automated checking is
`test/conformance/proto/`", but no unit named
`test/conformance/proto/layering_test.go`, so `importOrder` never gained the
package. Every targeted per-unit check passed; only the `--full` run failed,
with `TestNetImportOrder` reporting `package net/capture declares no layer in
importOrder` once per import and `TestNetImportOrderCoversEveryPackage`
reporting the package outright. The plan was followed as written.
Suggested change: when a plan adds a package under `spec/proto/flowseer/net/`,
its unit files list `test/conformance/proto/layering_test.go` beside the
architecture record, because the record's import-order block is prose and that
table is the executable copy. The two are a mirrored pair and belong under the
same message-sync habit as the triad and the ref pair.

## 2026-09-09 delegate: a ledger note did not stop a repeated convention slip
Skill or agent: `.claude/skills/delegate/SKILL.md`, "Write the brief", item 5.
What happened: a worker set `features.field_presence = IMPLICIT` on the
counter fields of a new message, which no file under `spec/proto/flowseer/`
does; the coordinator reverted it and put the correction in the unit's ledger
`note`. Two units later the brief carried that note verbatim and the same
model set the same feature on two boolean fields of a different message. On an
authorization record the cost was real: implicit presence collapses "the
operator did not say" and "the operator declined full payload" into `false`.
Suggested change: a correction that generalizes past its own unit is not a
ledger note. Either the coordinator writes it into the convention doc that
governs the file type before the next dispatch, or the brief states it as a
prohibition in its own line rather than inside a list of prior-unit notes.
