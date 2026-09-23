# Split a large plan into phases

A plan over six units or 300 lines is checked for dependency clusters. A
plan that outlives one session is otherwise implemented by a later session
that re-derives what landed and why; a phase plan is small enough to land
in one, and the ledger `implement` keeps carries the rest across.

## Find the clusters

Group the units by the files they touch and the `After` edges between
them: two units that touch the same package or that one `After` line
connects belong to one cluster. Confirm the grouping against the real
dependency graph where the packages already exist, `go list -deps` for Go
and the module graph in `buf.yaml` for schema. A unit that creates a new
package joins the cluster of the units that import it.

One cluster means the plan stays whole: say so under Decisions, with the
cluster as the reason, and keep the plan under 300 lines by cutting what
the implementer can decide alone.

## Write the parent and the phases

More than one cluster produces a parent plan and one phase plan per
cluster, in dependency order:

- The parent keeps the Goal, Decisions, and Requirements for the whole
  change. Its Units are the phases, each with `Files:` naming the phase
  plan path, `After:` naming the earlier phases, and a `Landed:` line that
  stays empty until the phase's plan reads `implemented` and then carries
  the commit range, as `` `601e6e03..7cdc35dd` ``: the ledger check the
  verifier runs reads the last commit of that line to prove a later
  phase's worktree holds it, and a `Landed:` written as prose fails that
  check. The parent's `status` is `planned` until the last phase lands,
  then `implemented`; it never reads `partially-implemented`, since that
  means units landed.
- `After:` between phases names real dependencies only, like `After:`
  between units. Phases whose packages are disjoint run at once in
  separate worktrees, which `drive` does when the quota allows, and the parent's `Landed:` lines
  are the only thing they share.
- Each phase plan is a full plan at
  `docs/plans/<date>-<type>-<slug>-phase<N>-plan.md` with a `parent:`
  frontmatter field naming the parent path. Its Decisions cite the
  parent's rather than repeating them.
- Only the first phase is written implementation-ready. A later phase
  carries Goal, Decisions, and Requirements, `artifact_readiness:
  needs-decisions`, and this line under its title: `> Re-planned by plan
  when its turn comes; the tree will have moved.` `implement` refuses such
  a plan and names `plan`. When a phase lands, `implement` fills its
  `Landed:` line in the parent.

An example: a nine-unit plan with four units in `src/protocol/smi` and
five in `src/protocol/snmp` whose `After` lines depend on the first four
becomes a parent with two phase units and two phase plans. Nine units that
all touch `src/protocol/snmp` stay one plan.

## A parent written on purpose

A split is not the only way a parent comes to exist. When the request
names a sequence of changes that are decided as a sequence, each one
bounded enough to be its own plan and each depending on the one before,
write the parent first and the phases under it in the same shape: the
parent holds the Goal, the Decisions the phases share, and the
Requirements each phase will claim; every phase after the first carries
`needs-decisions` and its re-planning line. The parent is the one place
the sequence and its `Landed:` lines live, so a later session sees what
landed without reading the git log.

The test is that the sequence is decided, not hoped for. A direction
record's Consequences that name what could come later (a next layer, a
later protocol) stay prose in that record; a parent plan for them would
sit at `planned` with no code behind it and read as unfinished work. An
example: a virtual switch whose L2 forwarding, spanning tree, and L3
layers are each requested and ordered gets a parent with three phases and
one implementation-ready phase plan; the same switch with only L2
requested and the rest listed as possible growth in its direction record
gets one plan.

## Review and hand off

Step 4 dispatches the reviewer against the parent and the first phase
together. The handoff names the parent, the phase that is ready, and the
phases that wait for re-planning.
