# Research: Preventing coding agents from violating repository structure

**Date:** 2026-08-26

**Repository:** FlowSeer

**Scope:** Research and recommendations only; no guardrails are implemented by this report.

## Executive summary

Do not try to solve this with a longer prompt. Use four layers:

1. Put a short, unambiguous invariant and its safe alternative in the root
   `AGENTS.md`.
2. Express the same invariant as a deterministic repository check.
3. run that check in provider-specific hooks for fast feedback and in the merge gate
   as the authoritative enforcement point.
4. Treat changes to guardrails, excludes, and CI as policy changes that cannot be
   smuggled into an ordinary feature task.

For the reported example, the correct invariant is:

> `spec/proto/` is a production Buf input tree. It may contain production `.proto`
> files and package-boundary `README.md` files only. It may not contain Go, tests,
> fixtures, testdata, source inventories, or tool code. Put Go schema-conformance
> tests under one designated Go package (proposed: `src/common/protoconformance/`)
> and fixtures under that package's `testdata/` directory. Never add a `buf.yaml`
> exclude to make test artifacts legal inside `spec/proto/`.

The main diagnosis is that this was not an implementation agent casually ignoring a
known rule. The plan itself specified the bad paths and the hook exception, and the
implementation followed the plan. Prevention must therefore operate before plan
approval as well as during editing and at merge time.

**Overall confidence: high.** The repository history directly establishes the local
failure chain. Current OpenAI and Anthropic documentation agrees that instruction
files are contextual guidance rather than hard enforcement, while pre-tool hooks can
block common operations. OpenAI explicitly warns that hooks are not a complete
enforcement boundary, which is why a deterministic gate remains necessary.

## What actually happened in FlowSeer

### The wrong placement was designed into the plan

The current plan at
`docs/plans/2026-08-21-1257-feat-proto-base-types-plan.md` explicitly required:

- R11: a Go test under `spec/proto/`;
- R12: `_test_fixtures` directories under the production schema tree;
- KTD2: `spec/proto/layering_test.go`;
- KTD3: changing `proto-check.sh` so it skipped lint for `_test_fixtures`;
- KTD4: `spec/proto/addr_rules_test.go`;
- U3/U4: exact file manifests and verification commands that cemented those paths.

KTD3 and KTD4 are marked as session-settled and user-approved. That does not make
the architecture sound; it shows why a reviewable plan can still need mechanical
policy validation before approval.

Commit `7f71eac` then added `spec/proto/addr_rules_test.go`. Its package comment
argued that the test belonged there because its subject was the directory. Subsequent
commits added `spec/proto/layering_test.go`, `spec/proto/generation_test.go`,
`spec/proto/proto_check_hook_test.go`, and `_test_fixtures`. Commit `03dd0c0` removed
them and added the rule that `spec/proto/` contains protobuf definitions only.

The specific commit attribution says `Co-Authored-By: Claude Opus 5`; regardless of
provider, the system failure is the same: an agent-generated plan made a locally
plausible architectural decision, the plan was accepted, and later agents executed
and reinforced it.

### Why the existing conventions did not save the repository

At the time of the first bad commit, `docs/code-style.md` said that tests live in the
package they test, while the proto style guide did not yet say that `spec/proto/` was
source-only. The agent resolved that ambiguity in favor of co-location. All ordinary
Go gates passed because Go is perfectly happy to treat `spec/proto/` as a Go package.
The tests therefore proved behavioral correctness while violating repository
architecture.

The current prose rule is much better, but it remains advisory. A full-tree scan of
the current branch confirms that `spec/proto/` is clean today: tracked entries are
`.proto` or `README.md` files only. No automated repository-layout check establishes
that invariant.

### The present hooks are not cross-agent enforcement

FlowSeer has Claude hooks in `.claude/settings.json`, but no project-local Codex hook
configuration under `.codex/`. OpenAI documents Codex hook discovery in user,
project, managed, and plugin Codex config layers, including
`<repo>/.codex/hooks.json`; it does not document loading `.claude/settings.json` as a
Codex hook source. Therefore the Claude configuration should not be assumed to run
for Codex.

Even if the same script were registered with Codex, the present hook scripts read
`.tool_input.file_path`. OpenAI documents Codex `apply_patch` input as
`.tool_input.command`. On that path, the scripts would find no file and silently
allow the operation. The hooks also match editor tools only; a shell command or
generator can create the same file.

This is not a criticism of hooks. It is exactly their documented boundary: OpenAI
says most local tools are observable but specialized paths may opt out, and to treat
hooks as a useful guardrail rather than a complete enforcement boundary.

## Evidence from current guidance and research

### Instructions should be close, concise, specific, and tested

OpenAI documents that Codex reads `AGENTS.md` before work, layers instructions from
root to the current working directory, and stops discovery at the current directory.
For a Codex session launched at the FlowSeer root, a nested `spec/proto/AGENTS.md`
cannot be relied upon as the only statement of the invariant. The critical line
belongs in the root file. OpenAI's code-review guidance also recommends concise rules
that name both the behavior to flag and the safe path, leaving formatting and lint
checks to CI.

Anthropic is even more explicit: `CLAUDE.md` content is context, not enforced
configuration, and shorter, more specific instructions are followed more
consistently. It recommends adding a durable instruction after repeated mistakes or
when review catches something the agent should have known.

OpenAI's current model guidance reports that leaner system prompts performed better
in an internal coding-agent evaluation and recommends stating each instruction once,
keeping product-requirement examples, and validating prompt changes on representative
tasks. More reasoning is not a substitute for clear constraints: the same guide notes
that conflicting instructions, weak stopping criteria, or open-ended tools can make
higher effort produce unnecessary work or regressions.

### Constrain the interface, not just the model's prose

Both Codex and Claude Code support `PreToolUse` hooks that can deny a file edit before
it executes. Both support stop/post-tool checks for feedback, but a post-tool hook
cannot undo a completed side effect. OpenAI documents project Codex hooks under
`.codex/hooks.json`; Anthropic documents project Claude hooks under
`.claude/settings.json`.

The SWE-agent paper provides broader empirical support for this approach: changing
the agent-computer interface materially changes agent behavior and software-task
performance. The relevant lesson is not its benchmark score; it is that the action
surface and feedback loop are part of the agent's competence. A repository policy
checker exposed at edit and completion time is therefore more reliable than another
paragraph of preferences.

### Evaluate the agent configuration like production code

OpenAI recommends representative evals and rerunning them after each prompt or model
change. For repository agents, the output under evaluation is the entire resulting
tree and diff, not merely whether unit tests pass. A placement eval can be completely
deterministic: given a request to test proto validation, assert that no Go or fixtures
appear below `spec/proto/`, the designated conformance package contains the tests,
policy files remain unchanged, and the normal test suite passes.

## Recommended control stack for FlowSeer

### P0: establish one invariant and one safe path

Add a short **Hard repository boundaries** block to the root `AGENTS.md`. Do not copy
the entire proto style guide into it. State:

- what `spec/proto/` is;
- its allowed artifact types;
- explicitly forbidden test/fixture/tool artifacts;
- the one designated location for schema tests and fixtures;
- that new `buf.yaml` excludes for test artifacts are forbidden;
- that a proposed exception is a policy decision and requires explicit human review
  before implementation.

Also remove the ambiguity in the generic Go testing rule. “Tests live in the package
they test” should apply to Go production packages; repository, schema, generation,
and architecture conformance tests live in the designated conformance package.

Why root placement matters: Codex loads root-to-current-directory instructions at
session start. A linked rule buried only in `docs/code-style-proto.md` costs an extra
retrieval decision and is easier to miss during planning.

### P0: make the invariant executable

Add one small, deterministic full-tree checker, for example
`scripts/check-repo-layout.sh`. For `spec/proto/`, it should fail if any tracked or
untracked non-ignored path is:

- not a `.proto` file or package-boundary `README.md`;
- below a directory named `_test_fixtures`, `testdata`, `fixtures`, or another
  reserved test segment;
- a Go test, tool, inventory, or generated artifact.

Use a full-tree check, not only a diff check. A full-tree invariant catches an old
violation carried into a new branch and is simple enough to audit. Start with this
high-value subtree rather than inventing a universal policy language for the whole
repository.

The checker should print the forbidden path and the correct destination. That turns a
block into actionable feedback for both agents and humans.

Wire the same command into the normal merge gate. Passing `go test`, `buf lint`, or
`buf generate` is insufficient because those tools do not encode FlowSeer's source
tree architecture.

### P1: use hooks for fast feedback on every agent, not as the authority

Register the same policy checker in both providers:

- Claude: `.claude/settings.json`;
- Codex: `.codex/hooks.json` or an active Codex config layer.

Use two moments:

1. `PreToolUse` on normal edit/write paths to reject an obviously forbidden target
   before the file is created.
2. `Stop` (and optionally post-tool) to run the full-tree check, catching shell
   writes, generators, unusual tools, or a path parser the pre-hook did not cover.

The shared script must understand each provider's actual event schema. For Codex,
`apply_patch` carries a patch command rather than a single `file_path`; all paths in
the patch must be extracted and checked. Unknown or unparsable mutating input should
produce visible failure, not silently exit successfully.

Hooks improve the interaction loop, but CI or the merge gate remains authoritative
because OpenAI explicitly documents incomplete tool coverage and because users can
disable non-managed project hooks.

### P1: prevent a feature task from weakening its own gate

The old plan did not merely add misplaced files; it changed `proto-check.sh` and
`buf.yaml` so those files would be accepted. Add a policy-change boundary:

- Treat `AGENTS.md`, `.claude/hooks/**`, `.codex/**`, the layout checker, CI/merge
  configuration, and `buf.yaml` excludes as guardrail surfaces.
- Any task that changes a guardrail and also adds files that depend on that relaxation
  must stop for explicit review. Prefer separate changes.
- Flag new `exclude`, `ignore`, `skip`, lint-disable, generated-file bypass, or test
  suppression entries in plan review and code review.
- For stronger local enforcement, place the critical path deny in a trusted user or
  managed hook outside the repository. OpenAI documents managed hooks as enforceable
  and non-disableable by the ordinary hook browser. A repository hook can otherwise
  be edited by the same agent it constrains.

FlowSeer currently has no remote, so a hosted protected-branch rule is not available.
The practical local equivalent is: isolated worktree, a trusted external hook for
the narrow hard boundary, and explicit human review of any guardrail diff before it
is merged to `master`.

### P1: validate plans before approving them

For plans that create files or change architecture, require a compact artifact
manifest:

| Proposed path | Artifact role | Governing rule | Existing analogue |
| --- | --- | --- | --- |
| `src/common/protoconformance/validation_test.go` | schema behavior test | Go test + source-tree boundary | nearest conformance guard test |
| `src/common/protoconformance/testdata/...` | negative fixture | Go `testdata` convention | nearest fixture corpus |

Run the layout checker against the proposed paths before plan approval. The checker
can accept paths on stdin or expose a dry-run mode; it need not wait for files to
exist.

Add three plan-review questions:

1. Does any proposed file introduce a new artifact type into a source-of-truth or
   generated tree?
2. Does the plan add an exclude, ignore, skip, or hook exception to make its own
   design pass?
3. Is there an existing analogue elsewhere in the repository, and if the plan
   diverges, is the reason a real architectural requirement rather than proximity?

The second question would have caught the FlowSeer failure directly.

### P2: add a small agent regression suite

Keep 5–10 disposable-worktree scenarios derived from actual failures. For this one:

> Add executable tests for the protovalidate rules in
> `spec/proto/flowseer/net/addr/v1` and a negative import-layering fixture.

Deterministic graders should assert:

- no new path under `spec/proto/` except intended production schemas/README files;
- tests are under the designated Go conformance package;
- fixtures are under `testdata/` outside `spec/`;
- no guardrail or `buf.yaml` exclude was relaxed;
- relevant behavior tests and repository-layout checks pass;
- the agent's final response names any policy conflict instead of routing around it.

Run these scenarios when changing model family, reasoning effort, root instructions,
skills, hooks, or planning templates. Do not judge success only by compilation and
unit tests.

## What not to rely on

- **A bigger `AGENTS.md`:** long, repeated prompts can dilute the important rule.
- **“Use a smarter model” or maximum reasoning:** model guidance says more effort is
  not automatically better under conflicting or open-ended constraints.
- **Unit tests alone:** the bad Go package compiled and its tests passed.
- **Pre-tool hooks alone:** shell/generator paths and specialized tools can evade
  them; OpenAI explicitly calls hooks incomplete as an enforcement boundary.
- **Post-tool hooks alone:** they can report a violation but cannot undo the write.
- **Human plan approval alone:** the bad paths and hook exception were explicitly
  present in the approved plan.
- **A repo-local checker the same change may freely weaken:** policy and policy
  exceptions need a separate review boundary.

## Suggested rollout order

1. Decide the exact canonical test package (the report proposes
   `src/common/protoconformance/`) and fixture location.
2. Add the five-line hard boundary to root `AGENTS.md` and disambiguate the Go test
   co-location rule.
3. Add and test the full-tree layout checker.
4. Put it in the merge gate.
5. Register provider-specific pre-edit and stop hooks that call the shared checker.
6. Add policy-surface review rules.
7. Capture the proto-testing scenario as the first agent regression eval.

Steps 2–5 give the highest return. The later steps stop the same class of mistake from
reappearing through a more sophisticated plan.

## Sources

### Primary product documentation

- OpenAI, [Custom instructions with AGENTS.md](https://developers.openai.com/codex/guides/agents-md) — instruction discovery, nested precedence, concise code-review rules, safe-path examples, and verification.
- OpenAI, [Hooks](https://learn.chatgpt.com/codex/hooks) — Codex hook locations, event schemas, `PreToolUse` denial, `apply_patch` input, managed hooks, stop hooks, and the explicit incomplete-enforcement warning.
- OpenAI, [Model guidance](https://developers.openai.com/api/docs/guides/latest-model) — lean prompts, representative evals, constrained tool orchestration, and cautions about reasoning effort under conflicting/open-ended instructions.
- Anthropic, [How Claude remembers your project](https://code.claude.com/docs/en/memory) — `CLAUDE.md` is context rather than enforced configuration; specificity and concision improve adherence.
- Anthropic, [Automate workflows with hooks](https://code.claude.com/docs/en/hooks-guide) — project hook location, pre-tool blocking, stop/compaction feedback, provider event input, and post-tool limitations.
- Anthropic, [Hooks reference](https://code.claude.com/docs/en/hooks) — current `PreToolUse` decision behavior and hook event coverage.

### Primary research

- Yang et al., [SWE-agent: Agent-Computer Interfaces Enable Automated Software Engineering](https://arxiv.org/abs/2405.15793) — empirical evidence that the agent-computer interface materially changes repository-editing behavior and task performance.

## Confidence by conclusion

| Conclusion | Confidence | Basis |
| --- | --- | --- |
| The FlowSeer mistake originated in the plan, not only implementation | Very high | Exact requirements, paths, exception, and commit history are in the repository |
| Root concise instructions reduce ambiguity but cannot enforce placement | High | OpenAI and Anthropic documentation; local passing-test counterexample |
| A deterministic full-tree gate is the authoritative fix | High | Directly checks the violated invariant; hooks are documented as incomplete |
| Both Claude and Codex need explicit provider hook registration | High | Each provider documents a different project config location |
| Existing FlowSeer hook scripts should not be assumed Codex-compatible | High | No `.codex` hook config; current scripts expect `file_path`, while Codex documents `apply_patch.command` |
| The exact destination should be `src/common/protoconformance/` | Medium | Fits current Go layout and names the role, but should be confirmed as a project architecture choice before implementation |
