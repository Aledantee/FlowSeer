---
name: Agent steering
last_updated: 2026-09-18
---

# Agent steering

FlowSeer keeps the always-loaded instruction layer small and stores detailed
knowledge beside the code or decision it explains. The aim is to give an agent a
reliable map without displacing the task, source, and tests from its context.

This document describes how to maintain that map. It is not itself an
always-loaded instruction file.

## Steering surfaces

| Concern | Authoritative location |
| --- | --- |
| Cross-tool rules needed on most tasks | Root [`AGENTS.md`](../AGENTS.md) |
| Human orientation and first successful commands | Root [`README.md`](../README.md) |
| Area-specific engineering rules | The relevant convention or paired scoped agent entry points |
| Accepted architectural direction | [`architecture/`](architecture/) |
| Reusable lessons from verified work | [`solutions/`](solutions/) |
| Repeatable multi-step procedures | A checked-in skill with its scripts |
| Bounded delegation behavior | A checked-in agent definition |
| Deterministic prohibitions and formatting | Hooks, linters, schema checks, and tests |
| Personal preferences or tentative notes | Tool-local memory outside the repository |

`CLAUDE.md` is a symlink to `AGENTS.md`, so Codex and Claude receive the same
project contract without duplicated text. Tool-specific settings may wire that
contract into a runtime, but must not restate it.

## Write instructions for the right scope

Keep a rule in the root `AGENTS.md` only when it matters across the repository or
when every task needs it to find the next source. Put language and schema details
in the existing style guides. A subtree should gain its own `AGENTS.md` only when
it has stable local rules that would otherwise burden unrelated work. Add a
matching `CLAUDE.md` symlink or import beside it so Codex and Claude load the same
local contract.

An instruction should be:

- specific enough to verify, with an exact command or path where one exists;
- short enough to scan in context;
- explicit about its scope and any safe exception;
- backed by a reason when the constraint is surprising;
- written once, with links from other entry points.

Do not add dependency lists, file inventories, or architecture summaries that an
agent can recover cheaply from the repository. Keep only the routing fact or
boundary that changes how work should proceed. Do not store current task state,
branch names, absolute machine paths, credentials, or unverified guesses in a
shared instruction file.

## Separate judgment from enforcement

Natural-language guidance is appropriate for choices that require context:
where a domain type belongs, what evidence is persuasive, or why an apparent
shortcut violates an architectural boundary.

Use a deterministic mechanism when compliance must not depend on model behavior.
FlowSeer uses hooks to deny edits to generated sources, keep writes out of the
primary checkout, format supported files, and run fast layout checks. Linters
and tests remain authoritative. The prose explains the boundary and the tool
enforces what can be checked mechanically. A hook that cannot tell a decision
from a mistake reports instead of denying: the policy-surface prompt, the
suppression notice, and the unverified-edit message at Stop all leave the
call to the person. A guard earns its deny by being precise: the Bash guard
judges the operands of a mutating verb rather than any command that names
`generated/`, the edit guard resolves paths whose parent directory does not
exist yet, and the message-sync note stays silent about members the
file-level comment names. A guard that fires more often on legitimate work
than on the mistake it was written for gets narrowed, not explained.

Do not copy a linter manual into `AGENTS.md`. Name the command and document only
the project-specific judgment that the tool cannot express.

## Skills and delegated agents

A skill is appropriate for a conditional procedure with several steps, such as
diff-aware verification. Its description must say when it applies, and its body
must define inputs, observable completion, and failure behavior. Keep scripts
with the skill when exact execution matters.

Two authoring rules hold for every skill and agent file. A body carries only
what changes behavior on each invocation; material for one kind of episode
goes to a `references/` file whose pointer says when to load it, because a
pointer without a trigger reads as optional and a body loaded every time is
fixed overhead. And every command embedded in the text is run once,
verbatim, from a fresh shell before the file is saved: a prose rule is
reinterpreted each time it is read, while a wrong command runs unattended
and reads as correct on every re-read.

A delegated agent should have one bounded responsibility, the least tool access
needed for it, and an output contract the caller can inspect. Read-only research
and independent review are useful examples because their permissions match their
jobs. The coordinating agent owns integration and authoritative verification;
delegation does not transfer responsibility for the final result.

## Project skills

FlowSeer ships eight workflow skills under `.claude/skills/`: `next`,
`plan`, `implement`, `review`, `compound`, `close`, `drive`, and `steer`,
next to the
`verify-change` gate and the `delegate` routing skill that the others load
before dispatching an agent. The decisions below were taken against published
measurements, the research listed at the end of this document, and this
project's own session history; revisit them when that evidence changes.

Keep only the workflows the project uses. Session transcripts for this
repository showed six steps carrying every invocation of the 33-skill set
the repository used before 2026-09-05, and `/skill-doctor` showed 19 of
those skills never invoked on this machine. Each unused skill still cost
listing tokens on every turn. The first four skills map onto the six used
steps: brainstorming folds into `plan`, doc review into `plan` and
`review`, refreshing solutions into `compound`. `close` was added on
2026-09-05 for a step every session repeated by hand.

Gate the merge on evidence, not on the conversation. `close` is the one
skill whose action reaches every other worktree, and a session cannot see
which skills ran before it, so `implement`, `review`, and `compound` each
leave a checkpoint that `close` reads: the plan's `status`, `review`, and
`compound` fields, the verifier receipt under the git dir, and in Orca the
card's status and comment. Work that skipped the plan has no frontmatter,
so the same three lines go to a `flowseer-checkpoints` file beside the
receipt, with the commit range standing in for the plan. Every checkpoint
is on disk because an answer given in the conversation is unreadable to
a later session or a re-run, so `close` stops on a verdict it cannot read
from a file rather than asking for one.
A missing checkpoint pauses the merge, and `close` asks whether to run
the missing skill now; on yes it dispatches the skill to a worker in a
child worktree, or to a subagent with worktree isolation where no runtime
is reachable, merges that branch, and re-reads the checkpoint from disk.
The gate is still the file and not the answer, the closing session's
context stays on the merge, and the review is read by a session that did
not watch the work. The skill
leaves the worktree ready for
`orca worktree rm` and stops there: that command kills the terminal that
issues it and discards the workspace's terminal history, so it stays a
person's action taken after reading the report. Child worktrees a session
created for its workers are the opposite case, and `delegate` has the
coordinator remove each one in the turn its branch lands. A run on
2026-09-05 left five merged children under one worktree, each with its
terminals open and its branch listed as live work, because
`worker-release` closes only the agent terminal and no skill said who
removes the rest; the coordinator had already read everything those
terminals held.

Merge from the worktree, and leave `main` one fast-forward away. Claude
Code refuses a worktree-isolated session every git command that names
another checkout, reads included; the refusal is the harness's, not the
repository guard's, which is passive for Bash in a linked worktree, and
disabling the sandbox does not lift it. So `close` merges `main` into the
branch inside the worktree, where the tests and the verifier already are,
verifies the union with `--base main`, and emits the primary checkout's
`git merge --ff-only <branch>` for the person; `--ff-only` lands exactly
the verified commit and refuses if `main` moved again. The sandbox's deny
of writes under `.claude/skills/` also covers git replaying a committed
change, so that merge needs the bypass whenever `main` touched `.claude/`;
that deny list, like the isolation guard, is Claude Code's own and not a
repository policy surface, which is why the owner's direction that a
session may merge in both directions and remove its own worktrees is met
by the skill's shape rather than by a hook change: the repository already
permits it, the merge half is met by merging in the worktree, and the
removal half stays with the person because the harness refuses a
`git worktree remove` naming another checkout and the repository cannot
lift that; the report carries the command.

Keep each skill short and specific to this repository. Anthropic's authoring
guidance caps a `SKILL.md` body at 500 lines and says a skill that restates
what the model does by default adds context without value. Measured evidence
agrees: SWE-Skills-Bench found 39 of 49 public skills gave no pass-rate gain,
and Vercel found skills never fired in 56% of eval cases while a compressed
always-on index did. The planning skill this repository used before was
111 KB and loaded a further 110 KB of references per run. The FlowSeer
skills aim at about 150 lines each and contain only the procedure, the
file layout, and the repository rules an agent cannot infer from the tree;
episodic material goes to `references/` files behind a triggered pointer.
After the 2026-09-18 pass the workflow skills sit between 130 and 260
lines, `review` and `implement` the longest because each ends with the
option table its outcomes leave and `implement`'s Finish step names the
scripts that read deviations and test changes off the tree, and `delegate`
at about 245: its runtime lanes and quota rules are each
conditional on the host rather than on the task, and a coordinator that
loads the skill needs all of them in the same turn; the Orca procedures
moved to `references/orca.md` on 2026-09-10.

Use one reviewer, split by file group, never a persona panel. This
repository's transcripts showed the earlier persona-panel review dispatching
8.5 subagents per call on average, with a peak of 14, and reviewers
exhausting their context before they reported. Anthropic's research-system
report puts a multi-agent run at about 15 times the tokens of a chat turn,
which a review after every implementation does not earn back. `review`
dispatches `independent-reviewer` once,
reads the diff itself with a fixed checklist, and verifies every finding
before reporting it.

Skip ceremony when the work is small. Anthropic's best-practices guide says
to plan when the approach is uncertain or the change spans files and to skip
it when the diff fits in a sentence; a step that runs itself after every
other step produces artifacts nobody asked for. `plan` opens with a skip
rule, `implement` offers a review and runs none on its own, and
`compound` opens with a
gate that refuses one-off or derivable lessons, so that `docs/solutions/`
stays a set of lessons rather than a log of every session.

Promote lasting decisions out of the plan, from the plan. A plan under
`docs/plans/` is the planning artifact and goes stale once the work lands,
while a record under `docs/architecture/` is the decision record that later
plans read as a constraint. Before 2026-09-05 nothing in the workflow
produced a record; the four that exist were written by hand after a
brainstorm, and a cross-cutting decision taken inside a plan stayed there
where the next plan would not look. `plan` now carries a promotion test
(hard to reverse, constrains other packages or a wire contract, amends an
accepted record, or has been decided before) and drafts a record with
`status: proposed-direction`. Acceptance stays a person's action because an
agent-drafted record captures what was decided and tends to invent why.
`compound` refuses to carry a decision in a solution for the same reason a
solution never restates a convention.

Keep plan labels out of code. Commit `7b0c5cd8` stripped plan identifiers
that earlier tooling had told the implementer to cite in comments. `plan`
and `implement` both state the rule; `docs/code-style.md` enforces it in
review.

Leave policy surfaces to people. Earlier tooling edited `AGENTS.md`,
`CLAUDE.md`, and `CONCEPTS.md` after a chat consent. `compound` proposes
vocabulary and never edits an instruction file.

Enforce with the verifier, not with prose. Every skill ends by running
`verify-change` on the changed paths. Anthropic's guidance is explicit that
an instruction in a skill is a request and a hook is a guarantee.

Verify once, on the integrated result, at the scope the change can reach.
A test run of the workflow on 2026-09-05 (two 25-line example tests, two
Orca workers) produced three module-wide race runs of about ten minutes
each, because every worker and then the coordinator ran the verifier and
the verifier raced the whole module for any Go path. The verifier now
vets, tests, and lints the changed packages and their importers (a
fixpoint over `go list` dependency and test-import data) and keeps the
module-wide scope for `--full`; workers run their package's focused tests
and the coordinator runs the verifier once after merging.

Make a gate's silence impossible to read as a pass. Between 2026-09-05 and
2026-09-10 the observation queue collected five entries about the verifier
reporting success for a reason unrelated to the code: a directory argument
selecting no gate, a no-gate exit clearing its own marker, a cached corpus
pass, a `--path` naming a file the baseline lacks, and a background wrapper
whose `tail` replaced the script's exit code. Each was fixed in the script
rather than in prose, since a rule that was read and broken wants
enforcement: a directory expands to its files, a no-gate run exits non-zero
before touching the receipt, the corpus tier carries `-count=1`,
`buf breaking` targets only files `main` holds, and the last line of every
run names the verdict. A sixth entry, on 2026-09-11, had a `--full` run
block forever on a Docker daemon that had stopped answering, and the
verdict line, once the probe was killed by hand, did not say which gate
had failed; the wrapper now bounds its `docker info` probe, and the
verdict line names the gate that was running. The two invariant packages (`src/common/errs`,
`test/conformance/proto`) run on every targeted root-module run for the
same reason: a per-package gate cannot see a repository-wide namespace,
and a rule asking the implementer to remember that had already failed
twice. `--full` bounds `go test -p` because a gate that fails for reasons
the diff cannot cause teaches its readers to discount it. Three more
failures of that kind live in the script rather than in prose: the lint
gate passes `--allow-serial-runners`, because `golangci-lint` holds one
lock per machine and would otherwise die on any concurrent run; the
script searches `$(go env GOPATH)/bin` and checks every tool the selected
gates need before the first gate, because a session's PATH must not
decide whether the tree verifies and a late tool failure reads as a gate
result; and a passing run prints the dirty-marker lines it could not
clear, because a targeted run rewrites the marker in the same second as
the receipt and a silent survivor reads as an artifact, so `close` takes
the marker's content as its remedy. The marker hook itself was the last
of these. It guessed from a Bash command's text whether the command wrote
a file, and measured against one session's commands the pattern missed a
Python rewrite of two documents and any `cp` or `tee`, while it flagged
`git log | grep patch` and a redirect to a scratch `.json`; each false
flag asked for a module-wide race run. The hook now reads what changed off
the tree, by content hash of the dirty paths against the listing stored
after the previous Bash call, and marks those paths like editor edits;
only a change under `generated/` or to the module graph keeps the
`--full` line. It needs no snapshot before the command, which two
parallel Bash calls would have raced on, and the verifier rewrites the
listing after a pass so verified content is not marked again.

Watch a test fail against the defect. One plan produced three tests that
read as proof and asserted nothing, each found only by reverting the fix;
a later session hit the same trap twice more and named the sharper rules,
that a test asserts what the fix causes rather than what it prevents, and
asserts the state it depends on before the outcome. `implement` states
them at the test step, with the undo as a file copy after a reversal's
`git checkout` took an unfinished unit with it. The rules stay prose
because a reversal is a judgment about which line carries the property;
what can be enforced, the fixture validity of wire messages, names
`protovalidate.Validate` instead. The rule was then cited in every brief of
a second plan and broken five more times, and three new
codec packages landed seven tests that passed against a broken decoder,
since "changes behavior" exempted new code. A conformance gate for
negative-only assertions was considered and not built: none of the five
instances had that shape (a fixture missing the capability, an assertion
behind an admin-down port, a helper returning one value for two states),
and only a reversal found any of them. So the reversal became an artifact
instead of an instruction: `implement` writes a mutation and the quoted
`--- FAIL` line per new test into the unit's commit body, the one place a
later review session can read, and the coordinator or `review` runs the
mutation itself for any new test whose commit lacks one. A quoted failure
can be checked by the next reader; "I watched it fail" cannot.

Hand the class across the seam, and judge the remedy. Four observations
had one shape: a rule held inside one step and
was lost at the handoff to the next. `review` named a finding's class in
step 4 and briefed the fix with the instance; it verified a finding and
passed its proposed fix through unchecked; it asked for an executable
property and then reviewed the code rather than the property, which
omitted ten of nineteen rules. `delegate`'s stop rule did not read as
applying to a requirement that was only unachievable, so a worker
weakened it and reported success. Each fix puts the rule at the handoff:
the fix brief carries the mechanism, a fix resting on a claim about the
code is verified or reported as a direction, the next round's primary
subject is the new invariant, and a brief quotes plan requirements as not
the worker's to restate. A ruling in `implement` is provisional until its
unit lands, for the same reason: comments written from a falsified ruling
cited it as though it were the source.

Split large plans into phases and carry progress in a ledger, not in the
conversation. Long-horizon coding degrades measurably: SWE-Bench Pro
reports frontier models resolving far fewer multi-file tasks than on
SWE-Bench Verified, and the Claude Code issue tracker records the
mechanism from the user's side, where a compacted session retries a
rejected approach and forgets the tracker files it wrote. The measured
mitigations agree on the shape: milestone-triggered, structured context
reset beats append-only context and free-running summarization
(Context-Folding, "Context as a Tool"), a summary that reads well can
still break the next step (Slipstream), and Anthropic's harness for
long-running agents keeps a JSON feature list with a pass field because
the model overwrites JSON less readily than Markdown. So `implement`
keeps `flowseer-plan-status.json` in the worktree's git directory, beside
the verifier receipt: per unit an id, status, commit, verifier time, and a
one-line note for a decision the next unit needs. The verifier validates
it on every run, `close` gates the merge on every unit `passed` and removes
it after the merge, and a resumed session checks each recorded commit
against `HEAD` before editing. The skills write it and the checkpoints
file through `ledger.py` in the verifier's scripts rather than by hand:
that directory sits under the parent checkout's `.git/`, which a
worktree-isolated session can read but not write through a redirect or
the Write tool, so the script that resolves the path is the one writer,
as the verifier is for its receipt. The ledger is temporary by construction;
a STATE.md or per-wave directory would be a second artifact format, and
the strongest community counter-signal is ceremony fatigue with
multi-artifact frameworks. Phase boundaries follow dependency cohesion:
cohesion-aware partitioning gained 11 to 14 points over naive splitting,
and naive parallel splitting scored below sequential execution, so `plan`
clusters units by the files they touch and the `After` edges between
them, and only the first phase is written implementation-ready, since
as-needed decomposition (ADaPT) beats fixed baselines. Units of a
phase plan run one at a time in fresh worker contexts; the CAID and STORM
results disagree on isolation versus shared state for parallel workers,
and Cognition's case against multi-agents concerns parallel work on one
deliverable, which sequential units avoid. The six-unit trigger is a
starting value from community reports of three to five phases per plan;
no controlled study varies wave size.

Drop what the repository cannot use. There is no remote, so pull-request,
CI-watching, and push skills are inert here. Cross-model review would send
diffs to an external CLI by default. The skills rely on `git`, `go`, `buf`,
and the existing agents only.

One artifact format each. Every plan under `docs/plans/` carries
`artifact_contract: flowseer-plan/v1` and the `artifact_readiness` field that
`docs/README.md` documents. Every solution carries `applies_when` frontmatter
and a row in `docs/solutions/README.md`. The skills describe these formats
and nothing else.

Name the model for every delegate. `repo-researcher` is pinned to Sonnet and
`independent-reviewer` to Opus, `delegate` sends pure lookups to `Explore`
on Haiku and resolves editing workers from the registry's fit set by pool
headroom, and no agent uses `inherit` any more: the coordinating session
may run the most expensive model, and none of the delegated work needs it. Anthropic's subagent guide recommends Haiku
for read-only exploration; its research-system report measured an Opus lead
with Sonnet workers beating a single Opus agent by 90.2% on its internal
eval, while a multi-agent run costs about 15 times a chat turn; ProgRouter
arrives at the same shape by routing each workflow step to the cheapest
model that still makes progress. Users describe the failure mode from the
other side: a task that spawned seven subagents on the session model and
exhausted a budget before one of them finished, cured by naming a smaller
model for them. `delegate` caps concurrent workers at three for the same
reason.

Send editing workers to a Herdr worker when a Herdr server runs, and to
Orca when only its runtime is reachable. The asynchronous-agent study
behind CAID found that isolated workspaces, a central integrator, and
test-based verification at merge improved paper reproduction by 25.6
points and library development by 14.7. Both runtimes provide that: a
child worktree per worker, a named model per launch, and a report the
coordinator waits on. Herdr took the first place on 2026-09-10 for three
measured reasons (`docs/research/herdr-trial-2026-09-10.md`): it starts and
tracks `claude`, `codex`, `agy`, and `opencode` alike, where Orca's
`worker-start` pins Claude, Codex, and Cursor ids only and a dispatch into
an `agy` or `opencode` terminal sits unsubmitted; its `wait` returns the
agent's own settled state once, where Orca's `check --wait` is re-armed by
every heartbeat; and a worker is a pane and a branch, with no dispatch
capability token for a context compaction to lose. Read-only delegates
stay native subagents, which load their definition and nothing else, where
a runtime worker is a full agent session. Outside both, `delegate` falls
back to native subagents with worktree isolation. The Orca command surface
is version-matched and served by the binary (`orca skills get orca-cli`,
`orca skills get orchestration`), so the skills show the shape of the loop
and defer to that guide for flags.
Two facts found on 2026-09-05 shape the skill's wording: the CLI reaches the
app over a local socket that Claude's Bash sandbox blocks, so a sandboxed
`orca status` reports the app as not running from inside an Orca terminal;
and `orca account list` reports which providers are signed in and how much
of each rate-limit window is used, which is why `delegate` discovers the
worker agent and provider per session instead of assuming Claude and picks
the provider for each wave by remaining quota. The 85% threshold is a
starting point chosen so that a three-worker wave cannot push a window
over its limit mid-run; tune it when a wave gets cut off or when quota sits
idle.

Orca reads usage only for the providers it has credentials for. On
2026-09-18 a selection dropped `google` and `go` because `orca account list`
showed `antigravity` and `opencodeGo` as `unavailable`, although both pools
were signed in and nearly idle: the status describes Orca's view, not the
pool. `delegate/scripts/pool-usage.sh` therefore reads each pool from its
own source (`agy -p /quota` answers from the quota service without a model
turn, and opencode's database records the dollar cost of every `opencode-go`
message), and the skill forbids dropping a pool on Orca's word alone.

Look up third-party library docs through Context7 when it is connected, and
nowhere else through a dedicated skill. `plan` and `implement` name the
Context7 tools and the `ctx7` CLI as a fallback; Go dependencies stay with
`go doc` and the module cache. The scope follows the evidence: retrieving API documentation improved
code generation on rarely seen libraries by 83 to 220 percent, with the code
examples carrying almost all of the gain, while a noisy retriever hurt on
well-known APIs. Context7 was chosen over Ref because its server is MIT and
self-hostable, it ships as an official Claude Code plugin, and it costs
nothing to trial; Ref's index is a closed API with a one-time free tier and
almost no independent user reports. The repository checks in no
`.mcp.json`: each machine connects the server through its own config, and
the skills fall back cleanly when it is absent. No web-research or academic-research skill was
added. Claude Code's first-party deep research skill covers the fan-out,
papers have appeared in one document here, and the measured failure of
research agents is fabricated citations (3 to 13 percent of URLs even with
web search), which the plan skill's rule to fetch every cited URL addresses
more cheaply than a procedure would.

The skill frontmatter uses two Claude Code fields outside the portable
Agent Skills key set, `argument-hint` and `user-invocable`. Anthropic's
`skill-creator` validator flags them; Claude Code documents them and Codex
ignores unknown keys, so they stay.

Brief the reviewer without the author's claims. A study of confirmation bias
in LLM code review found that framing a diff as bug-free cut detection
sharply, and that redacting such metadata plus an explicit neutral
instruction restored it in every affected case. Anthropic's best-practices
guide adds that a reviewer asked for gaps reports some even when the work is
sound, so `independent-reviewer` is told to report only what affects
correctness, the stated requirements, or a repository rule.

Keep the plan explicit and keep re-reading it. An analysis of 21,120
SWE-agent trajectories found that an explicit plan raises resolution, that
periodic reminders of the plan cut violations, and that a poor plan hurts
more than none. `plan` therefore ends with an implementer's read and an
independent review, and `implement` re-reads each unit before starting it.
Units carry an `After` line so that `implement` can run independent units
in parallel without guessing.

Log process corrections in one place, apply them on request. Task Observer,
a widely used meta-skill, keeps an observation log of corrections and skill
gaps that a person reviews on a schedule, and runs as an always-on monitor
from the first tool call. FlowSeer takes the log and the review and leaves
the monitor: `compound`'s Observe mode appends to
`docs/agent-observations.md` when a workflow skill ends with a process
correction, and `steer` works the queue when a maintainer asks, applying
the change process below to one entry at a time. An always-on observer
would spend context on every session for a signal that appears at the end
of a few. `steer` edits skills, agents, and this document in place and
stages hook, hook-registration, and `AGENTS.md` changes for a person, the
policy-surface line that `compound` also keeps. It is subject to its own
queue: an observation about `steer` is worked by `steer`. Two of Task
Observer's signals carry over as its decision rule: a step that was
followed as written and still failed wants a hook or a verifier check, not
louder prose, and a step that never fires is removed rather than kept in
case. Its audit step closes the gap the hook tests leave: those pin each
runtime's registrations but cannot say whether a rule in `AGENTS.md` has
an enforcer at all, or whether a hook added for Claude was also registered
for Codex.

Ask before ruling on what other units depend on; rule and record the rest.
HiL-Bench measured the gap that matters here: given full information,
models pass 64 to 88% of software tasks, and when they must decide
whether to ask first, success falls to 12%, with Claude's recall of real
blockers on software tasks at 35%. This repository's plans show the
local shape of it: four landed plans filed decisions taken without the
user under Open questions after the fact, where a reviewer reads them as
unresolved and the next implementer as settled. `implement` keeps the
stop rule (ask when the answer changes other units, the wire, or an
accepted record) and otherwise writes a `Ruled:` line into Decisions at
the moment of the call, with the reason and the cost if wrong, the shape
the superpowers `subagent-driven-development` skill uses, and leads its
report with them.

Read deviations off the tree. A study of 5,851 real agent sessions found
a completion report references about one action in eleven and drifts
toward the plan it was given exactly when execution diverged from it;
`delegate` already has the coordinator check a worker's tree before its
report, and the same applies to the coordinator's own report. `implement`
runs `scripts/plan-deviations.py`, which lists the changed paths no unit
names and the unit files that did not change, and the report carries a
reason per line.

Report test changes, do not block them. SpecBench measured agents
passing the tests they can see at near 100% while held-out pass rates
fell with codebase size, a gap of about 27 points per tenfold increase in
lines, and more refinement widened it; the reward-hacking benchmarks list
deleting a test, skipping it, and rewriting the expected output as the
usual moves. Anthropic's long-running-agent harness forbids editing a
test outright. FlowSeer breaks APIs on purpose, so a removed test is
sometimes right, and a gate that cannot tell a decision from a mistake
reports: the verifier prints `Test changes to account for:` from
`check-test-integrity.py`, `implement` quotes it with a reason per line,
and `review` reads the reasons. Only an oracle-exact check blocks; a
pre-action verification study got 100% recall at zero false positives on
exact checks and recommends demoting the rest to warnings.

Stop a red unit after three verifier rounds. The seven-rounds-of-patching
observation and SpecBench's finding that extra refinement optimizes the
visible test agree on the mechanism; superpowers caps the fix loop at
five rounds and escalates. `implement` marks the unit `blocked` and sends
the work back to `plan`, where the requirement lives.

Run independent units at once by default. Until 2026-09-15 a wave ran in
parallel only when the user asked, and most plans ran serially; the
parent plans' phases ran one at a time even where their packages were
disjoint. Co-Coder measured cohesion-aware partitioning at 1.8 to 2.1
times faster with 11 to 14 points more passes than sequential, and naive
file-level splitting at 60% more cost for 3 points; uncoordinated
parallel agents were fastest and worst. So `implement` groups units into
waves from their `After` lines and dispatches a wave of two or more to
workers, three at once, through a coordinator that merges and verifies;
`plan` writes `After` for real dependencies only, lists the waves, and
lets disjoint phases run in separate worktrees. The cap of three stays,
for the budget reason above.

Prove a phase's prerequisites are in the tree. On 2026-09-14 phase 2 of
the analysis-completeness plan was implemented twice, in two worktrees
forked from different points of `main`, each session re-planning "against
the landed tree" and finding the phase absent; one landing merged, the
other sits on `worktree-netsim-phase2-replan` with six units passed.
`check-plan-status.py` now fails a ledger naming a phase plan when a
phase its parent's `After:` names has no `Landed:` commit that is an
ancestor of `HEAD`, or when the parent on `main` already shows this
phase landed. The `Landed:` line therefore carries the commit range.

Keep test conventions in the convention doc. The instruction-position
studies measured 30 to 50% lower compliance for a rule in the middle of a
prompt than at its start or end, and `implement`'s test step had grown
into one 30-line paragraph of rules the observation queue added one at a
time. The rules about what a test asserts, fake peers, host contracts,
holding-side tests, and degraded paths moved to the Testing section of
`docs/code-style.md`, which `review` reads as well; the skill keeps the
procedure and the reversal rule.

Hand a multi-wave plan to a fresh session and re-ground after
compaction. Claude Code issue #24686, a plan denied after compaction
while its file sits on disk, was closed as not planned, and users of the
compound-engineering and GSD workflows clear context between planning
and execution by hand. `plan` says so at handoff and `implement` re-reads
the plan and the ledger before trusting a summary.

Tune the phase size from data. The six-unit trigger came from community
reports; each outcome note `implement` writes now carries the unit count
and the span of the ledger's `verified_at` values, and `steer`'s audit
reads them before the trigger changes. As of 2026-09-16 the notes show
phases of three to six units, each inside one session; the data does not
yet say, and the trigger stays.

Fix-and-re-review rounds belong to the coordinator. `review` carries the
loop as a step the user asks for, because the coordinator is the only
party that holds the rounds' history and so the only one that can see a
round undo the previous round's fix: fixes are dispatched through
`delegate`, the verifier runs on the union before each review round, the
loop stops at a round with no correctness findings, and after three
rounds on one mechanism the work goes to `plan`, the cap `implement` puts
on a red unit.

Sequence a parent plan's stages from the files, in a skill that owns only
the order. Twice, on 2026-09-11 and 2026-09-17, the user typed the loop by
hand ("plan -> implement -> review -> compound loop for each phase, review
and fix multiple times", then "use sub worktrees for all stages"), and
drove it afterwards with "status", "resume phase 3", and "pause after
phase 2"; "what plan is not finished yet" was asked in two sessions on one
day. The improvised loop also drifted: over the first two phases of the
protobuf tree refactor the coordinator loaded `implement`, `delegate`, and
`compound` once each and never `plan` or `review`, whose work went to
workers as hand-written briefs, and both phases were reviewed and fixed
with no `review` field written, the verdict `close` refuses to merge
without. `drive` therefore names the stage and loads the skill that owns it,
restating none of their rules, so a correction to a stage still has one
place to go. Its state is `plan-state.py` over the parent's `Landed:`
lines and the phase plans' frontmatter, the fields the other skills
already write, for the reason the ledger exists: that coordinator's
transcript reached 5 MB, and a resumed session has to find its place
without it. A phase whose last commit is on `main` needs no stage, since
`close` gated it there and older phases predate the `review` and
`compound` fields. The skill stops before `close`, which stays a person's
request like every other merge into `main`.

The integration branch is `main`. The skills named `master` until
2026-09-15, so `--base master` and `master..HEAD` failed in this
repository, and sessions passed explicit paths instead.

End a report with a question, not with an offer. A scan of 2,390 session
transcripts for this repository found 89 turns where a report ended on an
open statement and the user typed the obvious next step by hand: 43 times
"merge" or "commit and merge" after "the verifier passed, nothing is
committed", 13 times "ok" after "confirm and I'll write the plan", and
about 28 times "go" or "continue" after "say the word". Another 46
questions were asked in prose and answered with a number or a word. The
question tool was already in use where a skill named it and absent where
the skill said "the user asks for `review`". So the rule sits once in
`AGENTS.md`, and each skill's last step names the options its outcome
leaves, the recommended one first: `plan` offers implementation or a
fresh session, `implement` offers `review`, `review` offers `compound` or
the fix loop by verdict, `compound` offers `close`, and `close` offers to
run a missing checkpoint's skill. Nothing runs on its own: a step that
runs itself after every other step produces work nobody asked for, and
the five cases in the scan where the user redirected instead of accepting
are the reason each question keeps a "stop here" option. A delegated
worker never asks, because a worker waiting on an answer looks like one
that is working.

Pick the next work from files, and finish before starting. `next` exists
because the question "what now" was being answered from a session's memory
of plans it had read, across 75 plan files in two unit formats. The tools
that answer it well agree on the shape: Task Master's `next` and Beads'
`bd ready` compute the set whose dependencies are met from a store, never
from the model's recall, and rank inside it; both ship the listing as a
command because a model re-reading every file is slow and drifts. So
`plan-queue.py` reads frontmatter, a parent's `After:` and `Landed:`
lines, the ledger, and the unmerged branches that touch a plan, and the
skill reads its output. Work in progress outranks ready work, the Kanban
rule of limiting what is open; a plan another branch already changes is
flagged, since a phase was once implemented twice from two worktrees. The
prior art has no answer for "nothing is planned": none of the surveyed
tools compares plans with stated goals, and that comparison is where an
agent invents a roadmap. `GOALS.md` is the guard: one line per decided
goal with its record, no status, and `next` proposes a gap only for a
goal that file states.

Run each stage of a drive in a session of its own, and drive a plan
without phases the same way. `drive` first sequenced a parent's phases
from one coordinating session that loaded `plan`, `implement`, `review`,
and `compound` in turn. It now hands each stage to a worker session, the
two properties GSD's `auto` and the Ralph pattern share being a fresh
context per step and progress read from files at the start of every
round; the coordinator keeps the state command's output, the merges, and
the verifier. A plan without phases goes through the same four stages,
because the loop typed by hand was the same for it. The per-unit ledger
lives in the implement worker's git directory, so `drive` reads it before
the child worktree goes and reports it as the gate `close` would have
read. The stage worker counts against `delegate`'s three and may hold two
of its own, which is why one phase is driven at a time. A decision that
is the user's parks that plan in its Open questions and lets independent
phases continue; the questions are asked together when the drive stops.

Write hot-path text as procedure, and keep the story here. A pass over
the skills and agent definitions against Anthropic's skill, subagent, and
memory guidance and the Claude 5 prompting guides found no emphasis
markers, no over-verification scaffolding, and descriptions inside the
length limit. What it changed was shape: a rule buried in the middle of a
paragraph became a numbered step or a table row, an output contract
stated at the top and the bottom of an agent definition became one
section at the end, and an incident became its one-clause reason. Two
incidents left `verify-change` that way: the gate list is closed because
a coordinator restating it from memory once left lint out, and the
script is the last command of a background invocation because a session
announced a green verifier over a log holding two `FAIL` lines, the
trailing `tail` having supplied the exit code. Dates stay out of skills
and agent definitions except in format examples; this document keeps
them, since they say when a decision's evidence was last checked.

Report outcome first. Each skill's report step leads with the verdict or
result and keeps the rest to a short ordered list, which is what readers of
agent output ask for and what the `i-have-adhd` skill codifies.

Trim narration, not evidence. Anthropic's Opus 5 guide says the model's
responses run longer than earlier Opus models', that effort does not
shorten them, and that an explicit instruction placed near the end of the
prompt does; `independent-reviewer` runs on Opus, so its definition ends
with one. The instruction names the parts to leave out (preamble, a
restatement of the brief, a closing summary) rather than asking for
brevity, for two reasons. Giskard's Phare study found that "answer briefly"
instructions cut hallucination resistance by up to 20%: models keep the
claim and drop the explanation, which for a reviewer means the failure
scenario. And the Opus 5 and Sonnet 5 guides both warn that a review prompt
saying "be conservative" is followed literally and lowers recall; the
reviewer already filters to correctness, and a length cap would compound
that filter. The rule stays out of `AGENTS.md` because the Fable 5.1 guide
says the coordinating model already writes too few progress updates and
that instructions to keep that text brief should be removed.

## Change and review process

Treat changes to the policy surfaces named in `AGENTS.md` as policy changes, even
when they only edit Markdown. Use the same review discipline for another
steering surface when it changes permissions, enforcement, or cross-tool
behavior:

1. Start from an observed repeated failure, a new stable boundary, or a missing
   onboarding fact.
2. Choose the narrowest surface that will load when the fact matters.
3. Search all instruction and convention files for conflicting or duplicated
   guidance.
4. Include one concrete example when wording could be interpreted two ways.
5. Verify instruction discovery from the repository root and any affected
   subtree. Run the repository verifier for the changed files.
6. Request an independent guardrail review when the change meets the policy or
   cross-tool threshold above.

Remove a rule when the code or toolchain makes it obvious, when a narrower source
now owns it, or when the repository no longer behaves as described. A short file
that stays correct is more useful than an exhaustive one.

## Research basis

These sources were checked on 2026-09-03:

- [OpenAI, “Harness engineering: leveraging Codex in an agent-first world”](https://openai.com/index/harness-engineering/)
  describes a short root map, a versioned documentation system of record, and
  mechanical enforcement of invariants.
- [OpenAI Codex, “Custom instructions with AGENTS.md”](https://learn.chatgpt.com/docs/agent-configuration/agents-md)
  documents root-to-leaf discovery, nearer-scope precedence, size limits, and
  ways to verify which instructions loaded.
- [AGENTS.md](https://agents.md/) distinguishes the human README from the
  agent-focused instruction file and recommends exact setup and test commands.
- [Anthropic, “How Claude remembers your project”](https://code.claude.com/docs/en/memory)
  recommends concise, specific instructions, path-scoped rules for local facts,
  skills for conditional procedures, and hooks for enforced behavior.
- [Anthropic, “Create custom subagents”](https://code.claude.com/docs/en/sub-agents)
  recommends focused roles, precise routing descriptions, limited tools, and
  version-controlled project agents.
- [GitHub, “About customizing GitHub Copilot responses”](https://docs.github.com/en/copilot/concepts/prompting/response-customization)
  recommends short, self-contained instructions and warns against conflicts
  across instruction scopes.

Sources checked on 2026-09-05 for the project skills:

- [Anthropic, “Skill authoring best practices”](https://platform.claude.com/docs/en/agents-and-tools/agent-skills/best-practices):
  500-line body ceiling, description as the selection signal, context as a
  shared budget.
- [Anthropic, “Extend Claude with skills”](https://code.claude.com/docs/en/skills)
  and [“Create plugins”](https://code.claude.com/docs/en/plugins): the 1%
  listing budget, `/skill-doctor`, and `.claude/` for project-specific work
  with plugins reserved for distribution.
- [Anthropic, “How we use skills”](https://claude.com/blog/lessons-from-building-claude-code-how-we-use-skills):
  a skill that restates default behavior adds context without value.
- [SWE-Skills-Bench](https://arxiv.org/abs/2603.15401): 39 of 49 public skills
  gave zero pass-rate improvement; token cost rose up to 451%.
- [Vercel, “AGENTS.md outperforms skills in our agent evals”](https://vercel.com/blog/agents-md-outperforms-skills-in-our-agent-evals):
  skills never invoked in 56% of cases.

Sources checked on 2026-09-05 for delegation, model choice, and review
framing:

- [Anthropic, "Create custom subagents"](https://code.claude.com/docs/en/sub-agents):
  the `model` and `effort` fields, Haiku for read-only exploration, a 15,000
  token ceiling for combined agent descriptions.
- [Anthropic, "Best practices for Claude Code"](https://code.claude.com/docs/en/best-practices):
  plan when the approach is uncertain or the change spans files, skip it
  when the diff fits in a sentence; a reviewer asked for gaps reports some
  regardless; subagents keep investigation out of the main context.
- [Anthropic, "How we built our multi-agent research system"](https://www.anthropic.com/engineering/multi-agent-research-system):
  Opus lead with Sonnet workers beat single Opus by 90.2%; multi-agent runs
  use about 15 times the tokens of a chat; scale agent count to task
  complexity.
- [Effective Strategies for Asynchronous Software Engineering Agents](https://arxiv.org/abs/2603.21489):
  CAID, isolated workspaces with branch-and-merge integration and test-based
  verification, +25.6 points on PaperBench and +14.7 on Commit0.
- [Evaluating Plan Compliance in Autonomous Programming Agents](https://arxiv.org/abs/2604.12147):
  21,120 trajectories; explicit plans help, reminders cut violations, a poor
  plan hurts more than none.
- [Measuring and Exploiting Confirmation Bias in LLM-Assisted Security Code Review](https://arxiv.org/abs/2603.18740):
  bug-free framing suppresses detection; metadata redaction plus neutral
  instructions restore it.
- [ProgRouter](https://arxiv.org/abs/2608.25992): step-wise routing of a
  multi-agent workflow to the cheapest model that still makes progress.
- [Hacker News thread on subagent token cost](https://news.ycombinator.com/item?id=48883796):
  seven subagents on the session model exhausted a budget; naming a smaller
  subagent model and limiting concurrency fixed it.

Sources checked on 2026-09-05 for direction records:

- [Spotify Engineering, "When Should I Write an Architecture Decision Record"](https://engineering.atspotify.com/2020/04/when-should-i-write-an-architecture-decision-record):
  a record for every decision of significant impact, with the team defining
  significant; backfill undocumented blessed solutions.
- [Catio, "Architecture Decision Records: The 2026 Guide"](https://www.catio.tech/blog/architecture-decision-record):
  write one when the decision is hard to reverse, crosses components, or
  has been debated before; a design doc yields several records that outlive
  it because nobody edits a record after acceptance.
- [Codex Knowledge Base, "Architecture Decision Records with Codex CLI"](https://codex.danielvaughan.com/2026/04/28/codex-cli-architecture-decision-records-adr-automated-governance/):
  agent-drafted records capture what was decided and tend to invent why;
  require human review before acceptance and point agents at the record
  directory before they propose.

Sources checked on 2026-09-05 for documentation lookup and research skills:

- [When LLMs Meet API Documentation](https://arxiv.org/abs/2503.15231):
  retrieval improved code generation on 1,017 rarely seen APIs by 83 to 220
  percent; code examples carried the gain, descriptions and parameter lists
  did not.
- [On Mitigating Code LLM Hallucinations with API Documentation](https://arxiv.org/abs/2407.09726):
  documentation retrieval helped low-frequency APIs and hurt high-frequency
  ones under a noisy retriever; trigger it selectively.
- [Context7 vs DeepWiki vs Ref vs Docfork](https://mcp.directory/blog/context7-vs-deepwiki-vs-ref-vs-docfork-2026):
  licensing, hosting, and free-tier comparison; declined to publish one-shot
  numbers because region and prompt shape swamped the differences.
- [Upstash, Context7 vs web search benchmark](https://upstash.com/blog/context7-vs-web-search-benchmark):
  vendor benchmark of 100 queries, 37 percent fewer total tokens than
  WebSearch, accuracy deferred to a later study.
- [Detecting and Correcting Reference Hallucinations in LLMs and Deep Research Agents](https://arxiv.org/abs/2604.03173):
  3 to 13 percent of cited URLs never existed even with web search; a URL
  liveness check cut non-resolving citations below 1 percent.
- [LiveMCPBench](https://arxiv.org/abs/2508.01780): tool retrieval errors
  cause nearly half of agent failures across 527 tools; every connected
  server adds selection noise.
- [Task Observer](https://github.com/rebelytics/one-skill-to-rule-them-all):
  an observation log of corrections and skill gaps, reviewed by a person on
  a schedule.
- [HyperFrames skills](https://github.com/heygen-com/hyperframes): a router
  skill that confirms the brief up front and loads domain skills on demand,
  the same shape as `AGENTS.md` plus scoped skills here.
- [i-have-adhd](https://github.com/ayghri/i-have-adhd): action-first
  responses, numbered steps, no preamble or recap.
- [Anthropic, "Prompting Claude Opus 5"](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-opus-5):
  longer default responses, effort does not shorten them, a conciseness
  line repeated near the end of the prompt does; "be conservative" in a
  review prompt lowers recall.
- [Anthropic, "Prompting Claude Sonnet 5"](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-sonnet-5):
  length already tracks task complexity; literal instruction following;
  positive examples beat "don't" lists.
- [Anthropic, "Prompting Claude Fable 5.1"](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-fable-5-1):
  fewer progress updates by default; remove instructions that keep
  user-facing text brief.
- [Giskard, Phare hallucination analysis](https://huggingface.co/blog/davidberenstein1957/phare-analysis-of-hallucination-in-leading-llms):
  conciseness instructions cut hallucination resistance by up to 20%; the
  explanation goes before the claim does.
- Orca CLI and orchestration guides, served version-matched by the binary
  through `orca skills get orca-cli` and `orca skills get orchestration`.

Sources checked on 2026-09-06 for phase plans and the status ledger:

- [Anthropic, "Effective harnesses for long-running agents"](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents):
  an initializer writes a feature-list JSON with a pass field and a
  progress file; JSON because the model overwrites it less readily than
  Markdown; one feature per session, verified end to end before it is
  marked done.
- [Anthropic, memory tool](https://platform.claude.com/docs/en/agents-and-tools/tool-use/memory-tool):
  the multisession pattern reads memory files first, works one feature
  at a time, and updates the progress log at session end.
- [OpenAI, "Run long horizon tasks with Codex"](https://developers.openai.com/blog/run-long-horizon-tasks-with-codex):
  a plan file as source of truth, milestones small enough for one loop,
  validation and repair before the next milestone.
- ["Context as a Tool"](https://arxiv.org/abs/2512.22087): milestone-
  triggered compression into stable facts, condensed memory, and recent
  turns; 57.6% on SWE-Bench Verified over append-only baselines.
- ["Scaling Long-Horizon LLM Agent via Context-Folding"](https://arxiv.org/abs/2510.11967):
  folding a subtask into an outcome summary matches ReAct with a tenfold
  smaller active context and beats summarization-based management.
- [Slipstream](https://arxiv.org/abs/2605.08580): compaction validated by
  whether later steps still succeed; a coherent summary can still carry
  incorrect behavior forward.
- [ADaPT](https://arxiv.org/abs/2311.05772): decomposition only where the
  executor fails, with large gains over fixed strong baselines.
- ["When Parallelism Pays Off"](https://arxiv.org/abs/2606.00953):
  cohesion-aware partitioning along the dependency graph gained 11 to 14
  points over naive splitting on DevEval and CodeProjectEval; naive
  file-based parallelism scored below sequential execution.
- [SWE-Bench Pro](https://arxiv.org/abs/2509.16941): multi-file,
  long-horizon tasks; resolution rates far below SWE-Bench Verified.
- [CAID](https://arxiv.org/abs/2603.21489) and
  [STORM](https://arxiv.org/abs/2605.20563): isolated worktrees with a
  test-gated integrator improve PaperBench and Commit0; a shared workspace
  with write-time conflict detection beats that isolation on Commit0-Lite.
  The disagreement concerns parallel workers, which sequential units avoid.
- [Cognition, "Don't Build Multi-Agents"](https://cognition.com/blog/dont-build-multi-agents):
  parallel subagents on one deliverable make conflicting implicit
  decisions; share full context, compress with a dedicated model.
- [Manus, "Context Engineering for AI Agents"](https://manus.im/blog/Context-Engineering-for-AI-Agents-Lessons-from-Building-Manus):
  a recited todo file keeps the plan in recent attention; the file system
  as restorable context.
- [Claude Code issue #29890](https://github.com/anthropics/claude-code/issues/29890):
  after compaction the session retries rejected approaches and forgets
  the tracker files it wrote to survive compaction.

Sources checked on 2026-09-15 for the `plan` and `implement` pass:

- [HiL-Bench](https://arxiv.org/abs/2604.09408): full-information pass
  rates of 64 to 88% on software tasks fall to 12% when the agent must
  decide whether to ask; Claude's blocker recall on software tasks 35%.
- ["Plans They Abandon, Reports They Author"](https://arxiv.org/abs/2609.12205):
  5,851 sessions; a completion report references about one action in
  eleven and drifts toward the stated plan as execution diverges.
- [SpecBench](https://arxiv.org/abs/2605.21384): visible-test pass rates
  saturate while held-out rates fall about 27 points per tenfold
  increase in codebase size; more refinement widens the gap.
- ["Look Before You Leap"](https://arxiv.org/abs/2609.11957): oracle-exact
  blocking checks at 100% recall and no false positives; softer checks
  demoted to warnings.
- [Co-Coder](https://arxiv.org/abs/2606.00953): cohesion-aware
  partitioning 1.8 to 2.1 times faster and 11 to 14 points better than
  sequential; naive file-level parallelism 60% costlier for 3 points.
- ["The Instruction Gap"](https://arxiv.org/abs/2601.03269): rules in the
  middle of a prompt lose 30 to 50% compliance against start or end.
- [Agentic Context Management](https://arxiv.org/abs/2607.23809) and
  [Self-Compacting Agents](https://arxiv.org/abs/2606.23525):
  agent-triggered or rubric-triggered compaction beats a fixed schedule.
- [Skill presentation granularity](https://arxiv.org/abs/2605.31408):
  focused two-to-three-module skills beat comprehensive documentation.
- [TDD-Agent](https://arxiv.org/abs/2608.16742): tests written first and
  refined with the code beat tests written once and frozen.
- [Claude Code issue #24686](https://github.com/anthropics/claude-code/issues/24686):
  a plan denied after compaction while its file exists; closed as not
  planned.
- [superpowers, subagent-driven-development](https://github.com/obra/superpowers/blob/main/skills/subagent-driven-development/SKILL.md):
  rulings logged as what, why, and cost if wrong; a five-round fix cap
  with escalation.

Sources checked on 2026-09-18 for the wording pass:

- [Anthropic, "Prompting best practices"](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/claude-prompting-best-practices):
  say what to do rather than what not to do, give the reason so the rule
  generalizes, and replace "CRITICAL: you MUST" with plain wording, which
  recent models over-trigger on.
- [Anthropic, "How Claude remembers your project"](https://code.claude.com/docs/en/memory):
  under 200 lines per instruction file; emphasis on many lines leaves none
  standing out.
- [Revisiting the Reliability of Language Models in Instruction-Following](https://arxiv.org/abs/2512.14754):
  compliance falls as concurrent instructions rise, negative instructions
  fare worse in multi-instruction prompts, and earlier positions do better.
  The same check could not confirm the 30 to 50% figure this document
  takes from "The Instruction Gap"; treat that number as unverified.

The common recommendation is progressive disclosure. The inference for
FlowSeer is to keep `AGENTS.md` near its current size, add scoped steering only
when a real local rule appears, and invest in executable checks before adding
more prose.
