# FlowSeer documentation

The root [`README.md`](../README.md) is the human entry point. This page maps the
longer-lived material that contributors need after the quick start.

Read [`CONCEPTS.md`](../CONCEPTS.md) first when a domain term is unfamiliar. For
work on the device service, inventory, discovery, or ingestion planes, read the
relevant accepted direction under [`architecture/`](architecture/) before
designing a change.

## Documentation map

| Location | Use it for |
| --- | --- |
| [`architecture/`](architecture/) | Accepted system direction and the reasons behind package or service boundaries. |
| [`conventions/`](conventions/) | Cross-cutting shapes and workflows, including protobuf and test layout. |
| [`code-style.md`](code-style.md) | Go API, error, concurrency, comment, and test conventions. |
| [`code-style-proto.md`](code-style-proto.md) | Protobuf syntax, evolution, validation, and generation rules. |
| [`code-style-web.md`](code-style-web.md) | Frontend TypeScript conventions. |
| [`doc-style.md`](doc-style.md) | Prose rules for documentation, comments, commits, and pull requests. |
| [`agent-steering.md`](agent-steering.md) | How repository instructions, hooks, skills, and agent roles fit together. |
| [`agent-knowledge.md`](agent-knowledge.md) | Where durable facts and temporary agent memory belong. |
| [`solutions/`](solutions/) | Verified lessons from problems that are likely to recur. |
| [`plans/`](plans/) | Implementation decision records for bounded changes. |
| [`research/`](research/) | Evidence gathered before a design decision. |
| [`benchmarks/`](benchmarks/) | Reproducible performance results and their test conditions. |
| [`attic/`](attic/) | Superseded material kept only for historical reference. |

Specifications document their own provenance and update procedures under
[`spec/`](../spec/README.md). Package-level behavior belongs beside the package
in its README or Go documentation.

## Where a new document belongs

Put a statement in the narrowest durable location that will still be found by
the people who need it:

- accepted direction goes under `architecture/`;
- a reusable lesson from verified work goes under `solutions/` with the required
  frontmatter;
- a cross-cutting rule belongs in an existing convention or style guide;
- evidence gathered before a decision belongs under `research/`;
- benchmark method and results belong together under `benchmarks/`;
- a task-specific implementation decision record belongs under `plans/`.

Do not create a second source of truth to make a fact easier to find. Add a link
from the appropriate map instead. Update or remove stale claims in the same
change that invalidates them; [`doc-style.md`](doc-style.md) explains the prose
standard.
