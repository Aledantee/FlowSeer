# Retire a landed plan

Load this in step 4 for every plan the branch lands, and from `steer`'s
sweep or `next`'s `retire` group for a plan on `main` that was never
retired. A plan under `docs/plans/` describes open work. Once the work lands
it goes stale, and an agent that finds it later reads it as a statement
about the tree, so the plan and its state file are retired together. Git
history keeps the text. Before the retirement, every decision in it that
outlives the work gets a home in
`docs/architecture/`.

Which plans retire: a plan for which
`.claude/skills/plan/scripts/plan_record.py show <plan>` reports
`implemented`, `superseded`, or `abandoned`. A phase plan retires with its
phase. Its parent stays while the parent's state lists remaining phases, and
retires once `.claude/skills/plan/scripts/plan_record.py show <parent>` reports it finished. A
`partially-implemented` plan never retires.

## 1. Sort the decisions

Read the plan's Goal, Decisions, Out of scope, and outcome note, then the
landed code the Decisions describe. Record what landed, not what the plan
intended: a Decision the implementation departed from is recorded as
built. Put each Decision through the promotion test in `plan`, step 1
(reverting it touches more than one package or a wire contract, it
constrains work outside the plan's units, it changes an accepted record, or
an earlier plan or record decided the same question), then act on the first
row that fits:

| The decision | Action |
| --- | --- |
| stated by a direction record, and the record matches what landed | none |
| changes, narrows, or extends what a record states, accepted or proposed | amend that record (section 2) |
| passes the promotion test and no record covers it | draft a record as `plan/references/direction-record.md` describes, `status: proposed-direction` |
| a lesson rather than a decision: a trap, a pattern, a fix that generalizes | none; that is `compound`'s, whose outcome the plan already carries |
| local to the plan's units: a library, a field name, a test layout | none; the code and git history hold it |

A parent plan carries the Decisions for all its phases; sort them when the
parent retires, not with each phase.

## 2. Amend a record

A record cites landed work by date and scope, never by plan path, as
`docs/architecture/README.md` states. An amendment that changes what the
record decides also edits the text it changes. Edit a proposed or an
accepted record in place and name it in the retire commit; the report names
an amended accepted record so a person re-reads it, since acceptance stays a
person's action.

## 3. Rewrite the links to the plan

```bash
git grep -n "<plan file name>" -- ':!docs/plans/<plan file name>' ':!docs/plans/*-plan.state.json'
```

Every hit changes in the same commit. State files are left out of the search
because `retire` in section 4 owns them: it moves a phase from its parent's
`phases` to `retired`, and a dependent's `after` keeps naming the retired
phase, whose landed range it still needs. A parent's unit `Files:` line
naming a retired phase stays as it is. A
link from a record, a solution, `GOALS.md`, or a README points at the
record that now holds the decision, or gives the date and scope of the
work as `docs/architecture/README.md` shows. An open plan's `amends:`
naming the retired plan names the record instead, or is dropped when no
record holds the decision.

## 4. Delete and commit

Run the state command, then commit its staged removal with the records and
link rewrites:

```bash
.claude/skills/plan/scripts/plan_record.py retire <plan>
```

```text
docs(plans): retire <plan slug>

> Implemented. 6 units, 2026-09-11T10:02Z to 2026-09-11T16:40Z.
review: accept
compound: docs/solutions/<path>.md
records: <paths written or amended, or none>
```

The command prints the outcome, review, and compound lines for the commit
body. Copy those lines verbatim. `steer` reads phase-size data from the
outcome line in these bodies. A sweep of several plans makes one commit per
plan.

Under `land`, step 5 verifies the result. A sweep outside `land` runs the
verifier on the changed paths itself, sandbox disabled:

```bash
.claude/skills/verify-change/scripts/verify-change.sh --base main -- <changed paths>
```

## 5. Report

Name the records drafted (each waiting for a person to set
`accepted-direction`), the records amended (an accepted one to be re-read),
and the files whose links were rewritten.
