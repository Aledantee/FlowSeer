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

## 2026-09-09 code-style-proto: the style doc and a landed message disagree on counters
Skill or agent: `docs/code-style-proto.md`, the presence section, against
`spec/proto/flowseer/net/interface/v1/interface_counters.proto`.
What happened: a worker set `features.field_presence = IMPLICIT` on the five
fields of a new counters message and documented each in the field comment.
That is the style doc followed exactly: `docs/code-style-proto.md:117` says to
use IMPLICIT "only where that collapse is genuinely correct (a counter, a flag
whose false *is* its default), and say so in the field comment", and its
worked example is a counter with that comment shape. The coordinator reverted
it anyway, citing `interface_counters.proto:6`, whose file comment says "an
absent counter means the source does not report it — never a zero" and which
uses explicit presence throughout. Both are in the tree and they disagree: no
message under `spec/proto/flowseer/` sets `field_presence`, so the style doc
names a case the schema has never taken. The distinction that would resolve it
is not written down anywhere — a device-reported counter has a real "not
reported" state, an engine-internal counter does not — so an agent reading
either source alone reaches a different answer and each thinks it followed the
rule. This is the "read and still produced the wrong result" case.
Suggested change: `docs/code-style-proto.md` says which counters take IMPLICIT
and which do not, and `interface_counters.proto`'s comment says why it is the
second kind, or the doc drops "a counter" from its example and names a flag
instead. Until then a reviewer cannot call either choice wrong.

## 2026-09-09 delegate: a ledger note carried a correction the tree did not support
Skill or agent: `.claude/skills/delegate/SKILL.md`, "Write the brief", item 5.
What happened: the coordinator reverted a worker's `field_presence = IMPLICIT`
and wrote the reversal into the unit's ledger `note` as though it were a
convention. Two units later the brief carried that note verbatim and the same
model set the same feature again. The coordinator read the recurrence as a
worker ignoring an instruction; the entry above shows the instruction
contradicted `docs/code-style-proto.md`, which the worker had been told to
read. A ledger note travels with the authority of a convention while having
none, and nothing in the brief tells a worker which of two conflicting sources
wins.
Suggested change: a `note` records what the next unit needs to know about what
landed, not a rule. A correction that generalizes past its own unit belongs in
the convention doc that governs the file type, edited before the next
dispatch; if the coordinator is not willing to edit that doc, the correction
is a preference and the brief should not carry it as a prohibition.
