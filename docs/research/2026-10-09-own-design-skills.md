---
title: Whether FlowSeer should have its own design skills
date: 2026-10-09
scope: What design guidance and gates the repository already holds for web UI, how other teams give agents their own design system, what is measured about skills changing agent output, and a recommended order of work judged against docs/agent-steering.md
status: research; repository state read on 2026-10-09, recommendation not accepted, no decision recorded
---

# Whether FlowSeer should have its own design skills

Research date: 2026-10-09

FlowSeer should not build a suite of design skills, and it should not adopt a
general taste skill. It already owns one design skill, `web-component`, which
covers component correctness well. The recommendation is a sequence: repair the
design documents that disagree with the code, move every rule a tool can decide
into stylelint and tests, add a few always-loaded lines for the web subtree, and
then add exactly one new skill for composing and critiquing a view, on probation
and only if a no-skill baseline fails. The view level is where the repository's
recorded design failures sit, where no gate can see, and where the existing
skill declares itself out of scope. The published evidence supports this shape:
skills pay when they carry non-standard local practice and cost when they
restate what a model already does, they fire unprompted about half the time, and
nobody has measured a design skill improving visual quality. Judged against
`docs/agent-steering.md`, the view skill passes on procedure, scope, and listing
budget, and passes only conditionally on the first criterion, because the
failure evidence is four critique reports from one day and the observations
queue holds no entry for it. FlowSeer can settle that condition itself with the
with-and-without eval runner it has already planned.

## FlowSeer already owns a design skill, and it stops at the component

`web-component` orders the steps a Vue component takes to meet the web component
contract, and its seven references hold a large body of local interaction rules:
nine visual states, loading and empty-state timing, overlay rules, motion
durations with enter and exit values per surface, copy rules, and data-dense
table and chart rules (`.claude/skills/web-component/SKILL.md:26-34`,
`references/states-and-a11y.md:12-39`,
`references/overlays-and-motion.md:124-146`). The steering document describes
its references as holding "the overlay and motion rules agents most often break,
each traced to a failure fixed in this repository or to a primary source"
(`docs/agent-steering.md:113-125`). Its description ends "Not for view logic,
domain code, or backend work." (`.claude/skills/web-component/SKILL.md:3`), and for "a deeper visual critique"
it hands off to the third-party Impeccable plugin "when it is installed"
(`references/review.md:99-105`).

That boundary is where the recorded failures now live. The repository's failures
split into two classes. Mechanical ones (overlay stacking, exit animation, the
11px type floor) became tokens, rules, and gates. Composition ones did not. Four
Impeccable critique reports from 2026-09-27 scored the console **22/40 rising to
27/40** and described the page composition as "category-default", with "the same
four KPI cards on every view", marketing subtitles, and a dashboard that "opens
on KPI cards, not on what needs attention"
(`.impeccable/critique/2026-09-27T09-14-24Z__frontend-web-src.md:32`,
`.impeccable/critique/2026-09-27T10-20-16Z__frontend-web-src.md:48-51`). The
fourth report records a plateau: "Each round closed what it targeted and the
review found the next layer at the same depth" (`...10-20-16Z...:38`). Those
findings were fixed commit by commit, and no rule, solution, or procedure was
written from them. All 16 web solutions under `docs/solutions/` cover
implementation mechanics.

The design language itself is scattered and partly wrong. Colour and type are
deep in `frontend/web/design/`. Motion has numbers, but they live in the token
CSS, the skill reference, and an architecture record. Layout numbers exist only
in a root `DESIGN.md` that no instruction file cites and that says of itself
"When this file and the code disagree, the code is the current truth and this
file is stale" (`DESIGN.md:155-156`). A sample check confirms it is stale: it
names a `language.css` that no longer exists, a focus ring of 3px where the code
sets 2px, and a 5px radius with no token (`DESIGN.md:76`, `:152-155`, `:350`,
`frontend/web/src/theme/tailwind.css:85-88`). The reduced-motion rule is stated
three ways, two of which contradict the accepted contract (`DESIGN.md:413-414`,
`PRODUCT.md:104`,
`docs/architecture/2026-09-28-web-component-contract-direction.md:233-234`).
`PRODUCT.md` holds the only statement of who the console is for and what it
should lead with, and nothing routes an agent to it.

Enforcement is strong for colour and thin elsewhere. Stylelint gates colour,
fill, stroke, font-size, and box-shadow literals, and tests gate token contrast,
story presence, and axe in two locales (`frontend/web/.stylelintrc.json:12-39`,
`frontend/web/src/ui/a11y.test.ts:298-421`). Spacing, radius, duration, easing,
and z-index literals have no gate, template string literals "are not
machine-checked yet", and layout and exit animations "cannot be observed in
happy-dom"
(`docs/architecture/2026-09-28-web-component-contract-direction.md:245-248`,
`:294-295`). Whether an arbitrary utility such as `duration-[200ms]` is rejected
is unverified. No view has a story, so the catalogue shows parts and no
assembled screen.

## Measured evidence rewards local practice and punishes generic advice

The studies disagree on headline numbers and agree on the mechanism. SkillsBench
found curated skills raise pass rates from 33.9% to 50.5% across 87 tasks, with
focused skills of three modules or fewer beating larger bundles ([arXiv
2602.12670](https://arxiv.org/abs/2602.12670)). SWE-Skills-Bench found **39 of
49 public skills gave zero pass-rate gain**, with token overhead up to 451%
([arXiv 2603.15401](https://arxiv.org/abs/2603.15401)). The study closest to
this question, WebDev-Skills-Bench, injected 31 public web-development skills
into 50 projects and found Pass@2 fell 1.3 to 4.2 points while tokens rose 72%
or more. On two of four models an irrelevant text of equal length reproduced
most of the loss, and per-pair effects were nearly uncorrelated across models
([arXiv 2608.23067](https://arxiv.org/html/2608.23067)). The ETH Zurich study of
repository context files found they do not improve task success on average and
raise cost by over 20%, with one exception: they "are useful for specifying
non-standard coding practices" ([arXiv
2602.11988](https://arxiv.org/abs/2602.11988)). A fixed token system, a
component contract, and a product vocabulary are non-standard local practice.
"Use clear hierarchy" is standard practice a model already applies.

Two findings shape what a skill should contain. In the WebDev slice ablation,
**anti-pattern rules were the only slice with a reliable positive effect** (+3.1
points), positive rules were neutral, and examples were null on average and hurt
Claude Sonnet by 15.3 points ([arXiv
2608.23067](https://arxiv.org/html/2608.23067)). And stale guidance does
measurable harm: three SWE skills degraded results by up to 10% through
version-mismatched instructions ([arXiv
2603.15401](https://arxiv.org/abs/2603.15401)). FlowSeer's stale `DESIGN.md` is
that risk already present in the tree.

Triggering is the dominant failure in the measured cases. Vercel found its skill
"was never invoked" in 56% of eval cases and scored 53%, equal to no skill. An
explicit instruction in `AGENTS.md` raised it to 79%, and a compressed
always-loaded index reached 100%
([Vercel](https://vercel.com/blog/agents-md-outperforms-skills-in-our-agent-evals)).
A practitioner test on the real Claude Code binary measured 50 to 55% unprompted
activation over 22 prompts, and 100% with a hook that forces a yes-or-no check
per skill ([Scott
Spence](https://scottspence.com/posts/measuring-claude-code-skill-activation-with-sandboxed-evals)).
Both measurements predate current models, and neither tested a design skill.
They still say a rule that must hold on every UI edit does not belong behind a
description match.

None of this measures design. Every web result above is a functional test, and
the WebDev authors state that visual fidelity, UX, and design quality were not
captured. Impeccable reports no evals and says of its own detector that "a clean
detector run is evidence, not proof of visual or accessibility quality"
([Impeccable
README](https://raw.githubusercontent.com/pbakaus/impeccable/main/README.md)).
Zero-shot model judges of UI agree with human raters on some dimensions and
diverge on others ([arXiv 2510.08783](https://arxiv.org/abs/2510.08783)). The
case for a design skill therefore rests on mechanism and on other teams'
practice. No measured improvement in how interfaces look stands behind it.

### Teams with their own system split facts, procedure, and prohibition

The one detailed published account comes from BILL. Its component facts sit
behind a CLI and an MCP server generated from source, and its skills carry only
workflow: a data-source check before any code, an anti-pattern list for styling,
and an escalation rule when no component matches. On four tasks with one run
each, input tokens fell 31 to 63%, and the ungrounded agent shipped plain radio
buttons where radio tiles were asked for and hardcoded `#1c1b1b` and `560px`.
BILL's failure list is the useful part: a stale Data Table guide produced code
against deprecated APIs until docs regenerated with each component change, and a
vocabulary gap between design and code names went unresolved until someone wrote
the map ([BILL
Engineering](https://www.bill.com/engineering/how-we-made-our-design-system-bilingual)).
The authors flag the sample as small.

General taste skills assume a blank canvas, and FlowSeer has a fixed token
system. An open, unreviewed pull request against Anthropic's `frontend-design`
skill states that it "currently assumes a blank-canvas brief" and proposes that
in an existing system "tokens and shipped components are fixed material", with
distinctiveness coming "from information architecture, content/voice, and
component choices" ([anthropics/skills PR
1561](https://github.com/anthropics/skills/pull/1561)). Impeccable's maintainer
fixed the same conflict as a bug after its AI-palette rule fired **218 times**
on the project site's declared colour, and the fix works only when a `DESIGN.md`
with a correct colours block exists ([impeccable PR
809](https://github.com/pbakaus/impeccable/pull/809)). FlowSeer's critiques
record the local version: seven detector findings, "all false positives (brand
glow, documented popover shadow, deliberate shell clip)"
(`.impeccable/critique/2026-09-27T10-20-16Z__frontend-web-src.md:34`). [Builder
sentiment](2026-10-09-agent-built-web-ui-sentiment.md) points the same way:
every shared design aid converges on a recognisable look once widely adopted,
and what stays distinct is what a project owns, namely its tokens, components,
information architecture, and voice.

The Vue ecosystem offers no shortcut. Storybook's MCP server supports React only
([Storybook](https://storybook.js.org/blog/storybook-mcp-for-react/)), and
FlowSeer already removed its Storybook MCP server because "the rules agents
actually missed were repository conventions no tool reports"
(`docs/agent-steering.md:121-125`). The community `vuejs-ai/skills` project
contributes a selection rule worth copying: "A rule is kept only if it enables
the model to solve problems it couldn't solve without it"
([vuejs-ai/skills](https://github.com/vuejs-ai/skills)).

## Repair the documents and gate the numbers before writing a skill

The recommendation is four steps in this order. The first two are needed whether
or not a skill follows, and they remove most of what a skill would otherwise
have to say.

| Content | Where it belongs | Why |
|---|---|---|
| Token values, type scale, durations, radii | Token CSS as the single source, described in `frontend/web/design/` | A second copy drifts. `DESIGN.md` already has. |
| Users, product principles, product vocabulary | `PRODUCT.md` and `CONCEPTS.md`, cited from `frontend/web/README.md` and both UI skills | The only statement of intent, currently unreachable |
| Reduced-motion rule, Preflight status, layout numbers | One statement each, in the accepted contract or `frontend/web/design/language.md` | Three conflicting versions exist today |
| Spacing, radius, duration, easing, z-index literals, `transition: all`, arbitrary-value utilities | stylelint or a Vitest check | Decidable by a tool, and currently ungated |
| A motion specimen | A foundations story beside `Colors`, `Shape`, `Typography` | Promised in the design-system record, absent in the tree |
| "Tokens are fixed. The project skill wins. Load it before a UI edit." | Always-loaded lines for the web subtree | Must hold on every UI edit, so not behind a trigger |
| Component procedure and gates | `web-component`, as today | Already there and already scoped |
| Composing and critiquing a view | One new skill | A conditional multi-step procedure no other surface carries |
| Feel of motion, final polish | A person using the running app | No check exists for it |

**Documents first.** Retire root `DESIGN.md` after moving its still-true unique
content (the named rules such as One Coral and Glass Is Chrome, and layout
numbers checked against the code) into `frontend/web/design/language.md`.
Keeping it means keeping a second copy of token values that the code has already
outrun, and it exists mainly as input for a plugin the repository never formally
adopted. The alternative is to generate its front matter from
`palette-source.json`, which is defensible only if Impeccable stays in use.
Record that decision either way, since no file records one today. Keep
`PRODUCT.md` and cite it.

**Enforcement second.** Evil Martians' advice matches the steering document's
own table: "Every rule you can encode as a tool is a rule the LLM can't forget
and a token you don't have to spend reminding it" ([Evil
Martians](https://evilmartians.com/chronicles/stop-writing-rules-in-agents-md-use-agent-hooks-and-nano-staged-instead)).
The motion contract's property list and the token durations are numbers, and
numbers belong in a check. Once they are gated, the skill points at the command
and does not restate thresholds, which is also what the steering document asks
(`docs/agent-steering.md:82-83`). `.golangci.yml` and the verifier are policy
surfaces, and the stylelint configuration should be treated with the same care,
so these changes go through a plan and review.

**Always-loaded lines third.** Three to five lines in a
`frontend/web/AGENTS.md`, which does not exist today, would state that the token
system is fixed, that `web-component` and the view skill win over any installed
design plugin, and which files hold the design language. The steering document
permits a subtree file "when it has stable local rules that would otherwise
burden unrelated work" (`docs/agent-steering.md:35-40`), and Vercel's result
says a pointer of this kind is what lifts activation. Keep it this small,
because the ETH study prices always-loaded text at over 20% cost. Whether both
runtimes discover a nested file in this repository is unverified and is step 5
of the steering change process.

### One view skill, with a short list of what it holds

The fourth step is a single skill, working name `web-view`, for building,
changing, or critiquing a view under `frontend/web/src/`. It should hold a
procedure with an observable end: read `PRODUCT.md` and `CONCEPTS.md`, state
what the view leads with and for which user, compose from existing `Ui*`
primitives, cover view-level states, check the running dev app on fixtures at
390 × 844 and 1280 × 800 in both themes, and stop after a bounded number of
rounds as `web-component` already does. It should hold an anti-pattern list
drawn from FlowSeer's own critiques (identical KPI cards on every view,
marketing subtitles, a chart as hero, a primary action that stays "Poll now"
after a failed poll), because that slice is the one with measured support and
these are failures this console actually had. It should hold a vocabulary map
from product concepts to what appears on screen (reachability versus lifecycle,
status source and age, visible scope), which is BILL's radio-tile lesson applied
locally. It should hold view-level state patterns the component reference lacks:
partial data, stale data, "data as of", and scope errors. And it should hold
three escalation rules: a missing primitive goes to `web-component`, a token or
typeface change goes to a direction record under `docs/architecture/`, and a
judgment the agent cannot settle goes to the user.

It should not hold token values or hex codes, a component inventory (read
`frontend/web/src/ui/index.ts`), general design principles a model already
applies, any push toward bolder palettes or new typefaces, component-level rules
that `web-component` owns, linter thresholds, or worked examples in the body.
Examples go in a reference file loaded on demand, given the Sonnet result.
Critique belongs in the same skill as a reference with its own stop rule, since
the repository keeps its skill set small. Motion needs no skill of its own: the
numbers exist, the missing pieces are a gate and a specimen, and feel stays with
a person.

Nothing should be installed. The steering document's rule is "Take a procedure
from a public skill, and leave the skill" (`docs/agent-steering.md:285-294`),
and the Impeccable hand-off in `references/review.md` should be removed once the
project critique reference exists.

## The steering criteria pass the skill, but only on probation

| Criterion from `docs/agent-steering.md` | Verdict | Basis |
|---|---|---|
| Start from an observed repeated failure, a stable boundary, or a missing onboarding fact (`:1255`) | Conditional | Composition failures recurred across four critique runs, but all on 2026-09-27, before the current contract. `docs/agent-observations.md` has no web entry. |
| Narrowest surface that loads when the fact matters (`:1256`) | Pass, after steps one to three | Facts go to docs, numbers to gates, the standing rule to the subtree file. Only procedure remains for the skill. |
| Search for conflicting or duplicated guidance (`:1257`) | Fails today, fixed by step one | `DESIGN.md`, the reduced-motion wording, and the Preflight line conflict with code now. |
| A skill is for a conditional procedure with several steps (`:87-90`) | Pass | Composing and critiquing a view is one, with inputs, a browser check, and a stop rule. |
| Contains only what an agent cannot infer from the tree (`:156-165`) | Pass if the per-rule gate is applied | Product vocabulary and local anti-patterns qualify. Generic hierarchy advice does not. |
| Each skill costs listing tokens (`:139-141`, `:199-206`) | Pass | Fourteen descriptions total about 5,700 characters against an 8,000 fallback. One more fits. A suite would not fit comfortably. |
| A skill added on a gap is on probation (`:285-294`) | Applies | Unused by the next `steer` audit means removal. |
| Project skill wins over an installed plugin (`AGENTS.md`) | Supports it | Impeccable's broad trigger currently claims the same requests. |

The case against a new skill has four parts. The steering document prefers a
`references/` file over a second skill, the failure evidence is a month old and
from one day, how often agents do view work is unrecorded, and the critique
findings have not been re-checked against today's views. A view reference inside
`web-component` would cost no listing budget. It loses on scope: `web-component`
would have to drop "Not for view logic" from its description, its 200-line body
would grow a second procedure, and SkillsBench's finding favours focused skills
over bundles. The first criterion stays unmet until someone logs the candidate
in `docs/agent-observations.md` in the form "`new skill candidate: <working
name>`" and a baseline shows the failure still occurs.

## Four checks separate a working skill from a comforting one

The repository has an implementation-ready plan for exactly the needed tool: a
runner that executes a skill's scenarios headless in throwaway worktrees under
three variants (the skill as edited, the skill at a ref, no skill), grades with
mechanical checks, and reports passes, cost, and whether the skill fired
(`docs/plans/2026-10-02-1151-feat-skill-evals-plan.md`). It covers `plan` and
`review` first. The view skill should be its next subject, with scenarios
written before the skill body, as Anthropic's guidance asks: "Create evaluations
BEFORE writing extensive documentation"
([Anthropic](https://platform.claude.com/docs/en/agents-and-tools/agent-skills/best-practices)).

The first check is activation, measured apart from effect, so a skill that never
fires is not mistaken for one that does nothing. Use should-trigger and
should-not-trigger prompts phrased the way requests actually arrive, including
indirect ones, run several times each. The second is a with-and-without
comparison on three or more FlowSeer view tasks, graded by assertions a script
can decide: the verifier passes, no literal or arbitrary values appear, existing
primitives are reused, product terms appear on screen, the named anti-patterns
are absent. Add WebDev-Skills-Bench's length-matched irrelevant control to
separate content from prompt length, repeat runs because seed variance is large
relative to skill effects, and run it on each model `delegate` routes UI work
to, since rankings do not transfer between models. Apply the vuejs-ai gate per
rule: a rule the baseline already satisfies is deleted.

The third check is a blind human preference pass. A person compares unlabelled
screenshots of the with and without outputs in the running app, because scripts
cannot see composition and model judges are unvalidated for it. The fourth
guards the critique loop: run it on a view a person has already accepted and
count findings. Anthropic warns that "a reviewer prompted to find gaps will
usually report some, even when the work is sound" ([Claude Code best
practices](https://code.claude.com/docs/en/best-practices)), so a critique that
keeps finding problems in accepted work is noise and will drive churn.

One measure should be excluded. The Impeccable score is the only quantified
design record in the tree, and it is a rubric by the same author as the advice
it grades. The repository's own investigation rule names "a double that shares
the author's belief" as not a source. A rising score shows the rules were
transmitted. It does not show the console improved.

## What remains for a skill

Most of FlowSeer's design language is not skill material. Its values are code,
its prohibitions are checks waiting to be written, and its intent is a product
document nobody is routed to. What remains for a skill is small and specific:
the order of decisions when a screen is composed, and the mistakes this console
has already made. That is also the only part the evidence gives a reason to
expect will help, because it is knowledge a model cannot have and a general
design skill gets wrong by assuming a blank canvas.

Two unknowns stay open and both are FlowSeer's to close. Nobody, here or in the
literature, has shown that a design skill changes how an interface looks to a
person, so the with-and-without comparison on FlowSeer's own views would be
better evidence than anything published. And the failure record is one day old
in September, so the first act is to run one current view task without a skill
and look at the result.