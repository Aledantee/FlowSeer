---
name: review
description: Reviews a FlowSeer change (working tree, branch, commit, or paths) or a named subject (a package, mechanism, contract, or cross-package concern) for correctness, regressions, missing tests, and convention violations, and reports verified findings by severity. Use when asked to review code, check a diff, judge a change before commit, or audit standing code; a multi-unit subject is reviewed unit by unit, then at its seams. Report-only unless the user asks to apply fixes.
argument-hint: "[base ref | commit | paths | subject]"
---

# Review a FlowSeer change

## 1. Resolve the scope

| Request | Scope |
| --- | --- |
| no target given | `git diff HEAD` plus untracked files |
| a base ref or "the branch" | `git diff <base>...HEAD` |
| a commit | `git show <sha>` |
| paths | those files, whole |
| a subject: a package, a mechanism, a contract, a concern that crosses packages | the code that subject reaches, resolved below |

List the changed files. Read the intended behavior from the plan under
`docs/plans/`, the commit message, or the user's words.

Read the record of open follow-ups and blocking false-test items an earlier
review left: the plan's
`## Review gaps` section, or for planless work the last `gaps:` line of
`$(git rev-parse --git-dir)/flowseer-checkpoints`. Read the recorded
round count beside it: for a plan with
`.claude/skills/plan/scripts/plan_record.py show <plan>`, or for planless
work the last `rounds:` line of that file. A run on a scope with a recorded verdict is a new review that
continues that count (`references/fix-loop.md`, When to stop, says when it
starts at zero again). Step 3 carries the record's items into the reviewer
briefs as the previous round's findings, so a fresh session judges them
instead of rediscovering them. A recorded blocking false test keeps its class
and mutation metadata in a later review until the coordinator closes it from
the record.
Also read each plan
Decision ending `decided by the user` that settles a Requirement question.
Judge that Requirement by the Decision's text, and remove the matching item
from the plan's Open questions.

When the scope is a subject, load `references/subject-review.md` now; it
resolves units and seams and changes steps 3, 4, and 5 for that scope.

## 2. Load the applicable rules

Read only the conventions the touched files need: `docs/code-style.md` for
Go, `docs/code-style-proto.md` and `docs/conventions/protobuf.md` for schema,
`docs/conventions/observability.md` for telemetry, `docs/doc-style.md` for
prose and comments. Match the "Read when" column of
`docs/solutions/README.md` and read the guidance of matching solutions. When
the area has a `docs/architecture/` record, read its boundaries. When a
convention or solution is itself in the diff, take the rule from `HEAD` and
review the new version as prose.

## 3. Dispatch the reviewer

`independent-reviewer` has no shell. Write the diff to a file in the
scratchpad directory and brief the agent as `delegate` describes: that path,
the file list, the plan path, the intended behavior as a specification, the
convention paths, the matched solutions, and the pinned version of every
external convention or library a finding could cite (`go.mod`, `buf.lock`,
the semantic-convention version `docs/conventions/observability.md` names),
so the reviewer checks rather than recalls, and the items of the record
(step 1) as the previous round's findings. When
`.claude/skills/plan/scripts/plan_record.py is <plan> status=planned`
succeeds because work is mid-flight, say so in the brief. State
whose input the change reads and whether that author is trusted, quoting
the plan's Out of scope (`plan`, step 3). Without one, files contributors
and agents write in this repository are trusted, and input from a network
peer, a device, or a runtime user is not. A way to defeat the change that
needs a hostile author of trusted input is a note, not a finding: a
checker for honest mistakes (`docs/conventions/guarantees.md`) is not
reworked for crafted files. When the change handles input from one of
those untrusted authors, load `references/security.md` and put its
finding table in the brief. Ask for findings that affect correctness, the stated requirements, or a
repository rule, ordered by severity, each with path and line, the failure
scenario, and the smallest safe fix, or, when the fix rests on a claim about
code the reviewer did not open, a direction and the claim left unchecked.

One reviewer covers about 1,500 changed lines or one subsystem, its docs
included. Above that, dispatch one reviewer per subsystem with its own diff
file, in parallel. Split by file group, never by persona. When vendor
independence and pool headroom leave one eligible model, keep the split: run
lanes in parallel up to that pool's slots and the rest in rounds. A hot pool
does not make a 2,400-line diff one reviewer's assignment.

### The coordinator's own reading

This pass runs for every scope, a plain diff included. While the reviewer
runs, read the hunks that change behavior in full and skim the rest. Read
each hunk's code before the comment above it, decide what the code does,
then compare, since a comment stating intent primes a reader to see that
intent in code doing the opposite. Ask:

- With a plan: what is the verdict on each numbered Requirement, taken one
  at a time? `implemented` names the line that enforces it. `partial` holds
  on the tested path and fails on another. `contradicted` and `absent` are
  findings, and `absent` lists the paths and patterns searched, since
  enforcement often lives in a caller the search did not reach. `stronger`
  is a constraint the plan does not state, and `undecidable` is a finding
  against the plan's wording. Passing tests are not a verdict.
- Does every behavior change have a test that would fail without it, and
  would that test still fail if the check moved to the wrong place? Commit
  bodies carry a mutation and a quoted `--- FAIL` line per new test
  (`implement`, step 2.3). For each behavior change, load
  `references/mutation-check.md` and run one mutation of your own choosing
  against it, whether or not its commit quotes a failure, since the
  author's mutation shows only the fault the author thought of. A test that
  passes against the defect it names is a false test. A surviving mutation
  no test states is a gap (`references/fix-loop.md`, which decides the
  boundary between the two and which of them block).
- Does any comment narrate process, cite history, or carry a plan label?
- For each line the verifier printed under `Test changes to account for:`,
  does the implementer's reason hold against the diff, and does the suite
  still prove what that test proved?
- Does a README, convention doc, or schema comment now disagree with the
  code? When the change amends a `docs/architecture/` record, check the
  record's premises (what it says exists or is absent) against the tree,
  not only the paths it cites, since fresh paths make a reader trust a
  premise an earlier change made false.
- Does code or a comment assert how a library, protocol, or device behaves?
  Open that claim against its source (`AGENTS.md`, Investigation
  discipline). A test whose fixture was authored from the same document as
  the code is no evidence for it.
- Is anything added that has one caller, one implementation, or no caller?
- Does the change move a boundary an accepted direction record fixes?
- For schema: do the Config/State/Event triad and each LocalRef/GlobalRef
  pair still match, and are required fields documented as "Must be present."?

Run `go vet` and the existing tests of the touched packages when they take
under a few minutes. Rerun tests that bind a listener or need Docker
unsandboxed.

## 4. Verify before reporting

Open the code for each finding and confirm the failure is real in the
current tree and introduced by this change. Drop findings that rest on a
misreading; mark the ones you could not confirm as unverified. List problems
that predate the change under "pre-existing", verified to the same standard.
A hunk unrelated to the change inside an in-scope file is a note, not a
finding. When reading is not enough, prove a point with a throwaway test in
the scratchpad directory; never add files to the repository during a review.
Never pad the list.

Four findings need more than a re-read:

- One citing an external convention, API, or spec is a claim about the
  version the repository pins; check it against the pinned artifact.
- A negative probe result (the attack did not land, the path was not taken)
  stays open until the code that refuses it is pointed at. Check a probe's
  wire detail (a header, a subject, a frame shape) against the
  implementation before trusting either result.
- A seam finding discharged by an invariant in another unit or service is
  not dropped: it stays in the report as a question for the reviewer that
  holds those files, since the coordinator has read less of the neighbour.
- A finding is a sample of a class until shown otherwise: name what else
  engages and clears the same mechanism, so the fix covers the class. When
  a round finds a defect in the previous round's fix for the same mechanism,
  stop patching: state the property the mechanism must hold and make it
  executable (a generated state space, an invariant assertion the suite can
  fail on) instead of reviewing the next rewrite. See
  [references/fix-loop.md](references/fix-loop.md) for the round limit and
  who notices this across rounds.

When the smallest fix rests on a claim about the code ("nothing else
produces this", "no caller does that", "this path is unreachable"), open the
code the claim is about. Report a fix you could not verify as a direction
that names the unchecked claim, not as a patch.

Give each settled finding its class from the table in
`references/fix-loop.md`, Which findings block: security, behavior, false
test, gap, convention, or hardening. The class decides whether the finding
holds the verdict, so a finding without one reads as mandatory. Before
classing a finding whose failing input someone has to craft, name who can
supply that input and what that principal can already do without it. When
it can already cause the finding's result, the finding is hardening. A security
finding is a behavior finding that names all six parts
`references/security.md` asks for, and a concern missing one is hardening.
Security and behavior findings hold the verdict and get a reviewed round.
Blocking false tests hold the verdict and get no reviewer round.

Once every finding is settled, log how each reviewer's findings fared, so
`tune` can rank reviewers by what held: one call per reviewer step 4
judged, zeros included, sandbox disabled (the log lives under
`~/.claude/models`).

```bash
python3 .claude/skills/delegate/scripts/runlog.py review --model claude-opus-5-5 \
  --role review-unit --agent <agent id or run> --plan docs/plans/<plan>.md \
  --findings 4 --held 3 --unverified 1
```

| Flag | Value |
| --- | --- |
| `--model` | the registry id the reviewer ran on, never an alias such as `opus` or `inherit` |
| `--role` | `review-seam` for a reviewer dispatched for the interplay, `review-unit` for every other |
| `--agent` | the agent id the Agent tool returned for a native reviewer; for a reviewer on a pool's CLI, the `run` from the JSON line `orca-worker.sh start` printed, because `stop` deletes the state file that also holds it |
| `--plan` | the plan path; left out when the scope has no plan |
| `--findings` | what the reviewer returned; a finding dropped as a misreading counts here alone |
| `--held` | those step 4 kept, a finding moved to "pre-existing" included |
| `--unverified` | those marked unverified |

The coordinator's own reading logs nothing, since that would be the
coordinator grading itself. A failed call prints `runlog: <reason>`; quote
it in the report and carry on.

## 5. Report

Verdict first (accept, fixes needed, rework), then the findings that hold the
verdict. List security before behavior before blocking false tests: title, class,
`path:line`, what goes wrong and when, and the smallest fix or the
direction with its unchecked claim (step 4). Then the follow-ups under
their own heading, whatever the verdict, one item each in the format below:
the other false tests, the gaps, and the convention and hardening findings.
The heading says they do not hold the verdict. The reviewer's residual
testing note, what its mutations and tests did not reach, follows as its
own paragraph. It is not a gap and is not recorded. A gap dropped as
restating the implementation is reported with that reason and is not an
item (`references/fix-loop.md`).

An item is one line:

```text
- <path:line>: <mutation or the wrong text>; fails: <the case that would fail>; class: <class>
```

A gap or false test names the surviving mutation, a convention or hardening
finding the wrong text. A blocking false-test item uses this full form:

```text
- <path:line>: <mutation>; fails: <the case that would fail>; class: false test, blocking; runs: <n> when flaky; review-fix-test: yes|no
```

| Verdict | When |
| --- | --- |
| `accept` | no security, behavior, blocking false-test, or Requirement-change finding is open |
| `fixes needed` | one of those is open and can be fixed in place, or a blocking false test remains after two rounds |
| `rework` | a security or behavior finding is too large to fix in place, a security or behavior finding is open when the scope's count is two, or the second round was not clean |

Follow-ups never hold the verdict, so a report that holds only follow-ups
is an `accept` that lists them. Approving a change that improves the code
while it is not perfect, and labeling what is optional, is Google's review
standard
(https://google.github.io/eng-practices/review/reviewer/standard.html).
`accept after fixes` is never the verdict of a review's initial report. It
is written only once the fixes exist and the coordinator has rerun the
mutation of every blocking false test, since `land`, `drive`, and `next`
all read it as passing.

When the scope is this branch's work (the working tree, the branch, or its
plan's paths), record the verdict where `land` reads it (`land`, step 1). With
a plan, run:

```bash
.claude/skills/plan/scripts/plan_record.py review <plan> "<verdict>" [--rounds <n>]
```

The command stores the verdict and, when supplied, the round count. Write the
record in the same edit: the open follow-ups and blocking false-test items
under a `## Review gaps` section at the end of the plan, deleted with the
last item of either kind, and for a finding that needs a Requirement changed,
an item under the plan's Open questions that names the Requirement. A
blocking false-test item keeps its class, mutation, flaky run count when
applicable, and review-fix-test flag in the record. Commit that with a
message naming the review and a body that lists the open items, so they
outlive the plan's retirement. Then run the verifier on the plan path so the
receipt post-dates the commit. Planless work runs
`.claude/skills/verify-change/scripts/ledger.py checkpoint review "<verdict>"`,
which appends the line to `$(git rev-parse --git-dir)/flowseer-checkpoints`
and needs no commit or run, then writes the record and the round count the
same way:

```bash
.claude/skills/verify-change/scripts/ledger.py checkpoint gaps "<item> | <item>"
.claude/skills/verify-change/scripts/ledger.py checkpoint rounds "<n>"
```

Never pass `--replace`, since it rewrites the whole file and drops the
`implemented:` and `review:` lines. The last `gaps:` line and the last
`rounds:` line win, so when no follow-up or blocking false-test item remains,
write `gaps: none`. In
Orca, also append the verdict to the
worktree comment, keeping what `implement` wrote:

```bash
orca worktree set --worktree active --comment "<existing>; review: rework" --json
```

A commit or path review of other work, and a subject review, record neither
a verdict nor items, since `land` reads a `review:` entry as a verdict on
this branch and a `gaps:` line written for other work would be read as this
branch's. No gate reads the record, and only `review` does. The verdict is
the one field the gates read, and a recorded follow-up does not change it.

The review itself changes nothing beyond that record. End the report by
asking the user what happens next (`AGENTS.md`, Agent behavior):

| Verdict | Options, recommended first |
| --- | --- |
| accept, no follow-up recorded | run `compound` now; stop here |
| accept, follow-ups recorded | close the follow-ups in one pass (step 6), then `compound`; run `compound` now and leave them recorded; stop here |
| fixes needed, a security or behavior finding is open in one file group and a round remains | apply the fixes here; fix and review again (step 6); stop |
| fixes needed, security or behavior findings are open across file groups and a round remains | fix and review again (step 6); apply chosen findings only; stop |
| fixes needed, only blocking false tests remain | close them in the follow-up pass (step 6), which is not a round; stop |
| fixes needed, a blocking false test still open once the pass has run | one more pass (runs `review` again from step 1 with step 6), or stop |
| fixes needed, a Requirement question is open | take the Requirement to `plan`, or stop |
| rework | the question `references/fix-loop.md`, When to stop, gives for the case: the round limit, or a finding too large to fix in place |

A delegated reviewer does not ask. It states each verdict-holding finding and
each Requirement-change finding as its blocker, and lists the follow-ups
after them.

On "apply the fixes here" and "apply chosen findings only", make the fixes,
run the verifier on the changed paths, and report what changed. When a fix
answers a security or behavior finding, it is a round: load
`references/fix-loop.md`, dispatch the reviewers of One round, steps 3 and
4, over the diff of the fixes, and add one to the round count before any
verdict is written. A fix to shipped code gets a reviewer whoever made it.
A first round that is not clean records `fixes needed` with the new count
and ends with its row's question, or, for a finding about input a checker,
parser, or validator reads, with the question `references/fix-loop.md`,
When to stop, first item, gives. Before the coordinator
writes `accept after fixes`, the coordinator closes each blocking false
test by the pass's rule (`references/fix-loop.md`, The follow-up pass): it
reruns the recorded mutation on the merged tree and closes the item only
when the suite fails. It closes the follow-ups it fixed the same way, and
deletes a convention item when the corrected lines stand at its
`path:line`. Then it runs
`.claude/skills/plan/scripts/plan_record.py review <plan> "accept after fixes"
--rounds <n>` as above. A security, behavior, or blocking false-test finding
left unfixed leaves `fixes needed`, and so does
an open Requirement question. A round that was not clean is settled as
`references/fix-loop.md`, When to stop, describes.

## 6. Fix and re-review, when asked

When the user chooses to fix the findings and review again, load
`references/fix-loop.md`. A round fixes the security and behavior findings
and is reviewed, and a scope gets at most two rounds. Clean means the
round's reviewers returned no security or behavior finding that holds the
verdict. The follow-up pass then runs once, unreviewed, on tests, comments,
and docs. The coordinating session runs the rounds and the pass through fix
workers and never makes the fixes itself.

A correction to this procedure is logged as `compound`, Observe describes.
