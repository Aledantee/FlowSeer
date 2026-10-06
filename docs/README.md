# FlowSeer documentation

The root [`README.md`](../README.md) is the human entry point and
[`CONTRIBUTING.md`](../CONTRIBUTING.md) is the workflow for changing the
repository. This page maps the longer-lived material that contributors need
after those two.

Read [`CONCEPTS.md`](../CONCEPTS.md) first when a domain term is unfamiliar. For
work on the device service, inventory, discovery, or ingestion planes, read the
relevant accepted direction in the
[`architecture` index](architecture/README.md) before designing a change.

## Authority and freshness

Binding conventions and records marked `status: accepted-direction` govern new
design work. Scoped package READMEs explain how that direction applies to the
code beside them, while current source and tests show what has actually landed.
Plans and research preserve implementation choices and evidence; they do not
override an accepted direction or binding convention.

A package README, its `GUARANTEES.md`, and its source are one maintained contract,
not competing authorities. `GUARANTEES.md` is the normative list of guaranteed
behavior, while the README explains it. If they disagree about what exists, the
source and tests show the landed behavior and the documentation is stale; update
`GUARANTEES.md` and the README in the same change. This does not let landed code
silently overturn an accepted direction: record that mismatch and reconcile the
direction explicitly.

Each plan keeps document metadata in its Markdown frontmatter and machine
state in a `*-plan.state.json` file beside it. The frontmatter carries
`artifact_contract: flowseer-plan/v2`, while `plan_record.py` owns the state
file. The state left the frontmatter because three scripts each parsed it
their own way and disagreed about when a phase was finished. Use `.claude/skills/plan/scripts/plan_record.py show <plan>` to read it
and the commands in its module docstring to record transitions. A plan's
status is `planned`, `partially-implemented`, `implemented`, `superseded`, or
`abandoned`. If an accepted record and the tree disagree, record the mismatch
and reconcile the direction instead of guessing a new package or boundary.

When a plan has shipped, record its outcome with
`.claude/skills/plan/scripts/plan_record.py implemented <plan> --units <n>
--from <t> --to <t>`. The
`.claude/skills/plan/scripts/plan_record.py review <plan> "<verdict>"` and
`.claude/skills/plan/scripts/plan_record.py compound <plan> "<outcome>"` record
their own outcomes. `land` retires the plan and its state file once the work
lands, after moving any decision that outlives it into `docs/architecture/`.

A large plan is split into a parent plan and phase plans. A phase's state file
names its parent, prerequisites, and landed range. The parent stays computed
as `planned` until its last phase retires. While a plan is being implemented, the
worktree keeps a status ledger at
`$(git rev-parse --git-dir)/flowseer-plan-status.json`, never committed,
whose shape `.claude/skills/verify-change/SKILL.md` documents; `land`
removes it after the merge.

## Documentation map

| Location | Use it for |
| --- | --- |
| [`architecture/`](architecture/README.md) | Accepted system direction, supporting research, and the status of each record. |
| [`conventions/`](conventions/) | Cross-cutting shapes and workflows, including protobuf and test layout. |
| [`conventions/guarantees.md`](conventions/guarantees.md) | Package guarantee format, same-directory test citations, and relation to READMEs and doc comments. |
| [`conventions/observability.md`](conventions/observability.md) | Binding logging, OpenTelemetry event, trace, metric, namespacing, and semantic-convention rules. |
| [`conventions/dependencies.md`](conventions/dependencies.md) | Direct dependency statements, pin changes, the 14-day wait, and dependency inspection commands. |
| [`code-style.md`](code-style.md) | Go API, error, concurrency, comment, and test conventions. |
| [`code-style-proto.md`](code-style-proto.md) | Protobuf syntax, evolution, validation, and generation rules. |
| [`code-style-web.md`](code-style-web.md) | Frontend TypeScript conventions. |
| [`doc-style.md`](doc-style.md) | Prose rules for documentation, comments, commits, and pull requests. |
| [`agent-steering.md`](agent-steering.md) | How repository instructions, hooks, skills, and agent roles fit together. |
| [`agent-knowledge.md`](agent-knowledge.md) | Where durable facts and temporary agent memory belong. |
| [`agent-observations.md`](agent-observations.md) | Corrections to skills, agents, or hooks that await a maintainer's review. |
| [`solutions/`](solutions/README.md) | Verified lessons indexed by the conditions in which they apply. |
| [`plans/`](plans/) | Implementation decision records for open work; `land` deletes each once its work lands. |
| [`research/`](research/README.md) | Indexed evidence gathered before a design decision. |
| [`dependencies/`](dependencies/README.md) | Direct dependency statements and the statement gate. |
| [`benchmarks/`](benchmarks/) | Reproducible performance results and their test conditions. |
| [`runbooks/`](runbooks/) | Procedures for live devices and production systems. |
| [`attic/`](attic/) | Superseded material kept only for historical reference. |

Specifications document their own provenance and update procedures under
[`spec/`](../spec/README.md). Package-level behavior belongs beside the package
in its README, `GUARANTEES.md`, or Go documentation.

## Where a new document belongs

Put a statement in the narrowest durable location that will still be found by
the people who need it:

- accepted direction goes under `architecture/`;
- a reusable lesson from verified work goes under `solutions/` with the required
  frontmatter;
- a cross-cutting rule belongs in an existing convention or style guide;
- a dependency statement belongs under `dependencies/statements/`;
- evidence gathered before a decision belongs under `research/`;
- benchmark method and results belong together under `benchmarks/`;
- a task-specific implementation decision record belongs under `plans/`.

Do not create a second source of truth to make a fact easier to find. Add a link
from the appropriate map instead. Update or remove stale claims in the same
change that invalidates them; [`doc-style.md`](doc-style.md) explains the prose
standard.
