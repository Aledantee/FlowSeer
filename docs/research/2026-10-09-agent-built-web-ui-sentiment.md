---
title: Builder sentiment on distinct and smooth agent-built web UI
date: 2026-10-09
scope: What developers, designers, and tool authors say makes a web interface built with AI coding agents distinct and smooth, which remedies they endorse or reject, and how weak the evidence behind each is
status: research; sentiment gathered on 2026-10-09 from Hacker News, GitHub, vendor posts, and surveys, with no controlled comparison behind any technique and no end-user data
---

# Builder sentiment on distinct and smooth agent-built web UI

Research date: 2026-10-09

Across developers, designers, and tool authors, the consistent view is that a
distinct and smooth agent-built interface comes from inputs that carry a
specific human decision (a committed typeface and palette, a reference, an owned
component system, a named defect) and from a person using the running app, while
every shared, reusable aid converges on a new recognisable look once enough
people adopt it. Builders complain about sameness more than ugliness, and the
complaint has moved from "purple gradient" in 2025 to "any recognisable default"
in 2026, including the cream-and-serif and near-black-and-neon looks that
anti-slop skills produce. For smoothness, practitioners name animation and
interaction feel as the weakest area: a browser and a visual target let the
agent fix layout and overflow, but stills carry no timing, so motion quality
depends on strict numeric rules and a human judging feel. Model choice matters
less than the brief, since the top models sit inside overlapping confidence
intervals and the lead changed hands at least twice in twelve months. The
evidence base is weak: nearly everything is anecdote, vendor testimony, or
GitHub stars, no technique has a controlled comparison behind it, and no study
tests whether end users notice or penalise the look at all. Reddit beyond a
quote bank, X, Bluesky, and YouTube were largely not reached, so the sample
leans toward Hacker News and vendor blogs. The findings rank what experienced
builders say. Nothing here was measured against an alternative.

## Builders complain about sameness, and nobody has asked end users

The loudest complaint is undifferentiated sameness, ahead of any single visual
feature. A keyword study of 3,033 Reddit comments from 125 threads found
**"screams AI / slop" in 6.4% and "all looks the same" in 6.1%**, against 2.5%
for the default shadcn/Tailwind kit, 2.3% for purple, and 2.0% for gradients,
while the stereotyped memes (bento grids, glassmorphism) sat near the bottom
([vibecoded-design-tells
data](https://github.com/JCarterJohnson/vibecoded-design-tells/blob/HEAD/unslop-ai-ui/DATA_AND_GRAPHS.md)).
The study's author ships an anti-slop skill and says ordering is more reliable
than the percentages. An independent count points the same way: Adrian Krebs
scored 1,590 Show HN landing pages against 16 deterministic DOM and CSS patterns
and found **22% matched four or more and 54% matched two or more**, with an
estimated 5 to 10% false positives
([Krebs](https://www.adriankrebs.ch/blog/design-slop/)). Designers supply the
finest-grained tells. One told Krebs that "colored left borders are almost as
reliable a sign of AI-generated design as em-dashes for text."

The accepted cause is statistical. Anthropic calls it "distributional
convergence": safe choices dominate web training data, and "without direction,
Claude samples from this high-probability center"
([Anthropic](https://claude.com/resources/articles/improving-frontend-design-through-skills)).
Hacker News commenters say the same in fewer words ("the AI has learned what the
average webpage looks like") and blame Tailwind and shadcn defaults for the
content of that average ([HN](https://news.ycombinator.com/item?id=47872415),
[HN](https://news.ycombinator.com/item?id=47887604)). A second, well-upvoted
camp in tool subreddits blames the prompter: "It's only generic when your prompt
is generic"
([Reddit](https://reddit.com/r/vibecoding/comments/1oxutwy/lets_be_honest_ai_ui_all_looks_the_same/np02vjf/)).
The two explanations are compatible, and Nielsen Norman Group states the
combined version: a vaguely prompted build is "functional but generic,
reflecting the AI's default design choices when given minimal specification"
([NN/g](https://www.nngroup.com/articles/genui-vs-vibe/)).

The complaint did not fade as models improved. Its target moved. Anthropic
reported that banning the usual fonts made Claude converge on Space Grotesk
instead
([Anthropic](https://claude.com/resources/articles/improving-frontend-design-through-skills)),
and by April 2026 Krebs's detector listed Space Grotesk, Instrument Serif,
Geist, and serif italic accent words as tells, which are the choices people
moved to after dropping Inter
([Krebs](https://www.adriankrebs.ch/blog/design-slop/)). One HN commenter put
the dynamic plainly: "Once a design gets old enough that LLMs can reproduce
them, they are now 'slop'"
([HN](https://news.ycombinator.com/item?id=48504912)).

Sentiment splits by role. Designers frame the problem as absent design decisions
("Being 'average' just means relegating these decisions to something someone
else has done before") ([HN](https://news.ycombinator.com/item?id=45638110)).
Developers divide between agreement, indifference ("unless the product is a
design system or website builder, it's not my main concern"), and resentment
that hand-built shadcn work now reads as AI
([HN](https://news.ycombinator.com/item?id=47871208),
[HN](https://news.ycombinator.com/item?id=47871458)). **End-user sentiment is
unknown.** Every voice found is a builder judging another builder's work, the
Reddit study's top subreddits are all builder communities, and no user study,
A/B test, or conversion data was found. The cost that is supported is
reputational inside a builder audience: the look signals low effort to the
people most trained to spot it.

## Every shared remedy converges, and specific human input does not

The most widely endorsed technique, by vendors and independent users alike, is
specific direction before any code: committed fonts, a small palette, and
explicit decisions on type scale, radius, spacing, and elevation. "The more
specificity you add to your prompt and less freedom you give Claude Code to kind
of just 'do its own thing', the better your results will be"
([HN](https://news.ycombinator.com/item?id=47865413)). "Defining fonts is a good
way to at least not get the same typography as everyone else"
([HN](https://news.ycombinator.com/item?id=48432559)). PostHog's engineering
guide adds the hard constraint "Never invent new hues outside the supplied
palette unless asked"
([PostHog](https://posthog.com/newsletter/vibe-designing)). Bare adjectives
fail: Impeccable's author says "an adjective with nothing behind it is just a
nice apostrophe," and an unguided model asked for "bolder" adds gradients, neon,
or glass ([Latent.Space](https://latent.space/p/skill-engineering-design)).

References are the most repeated independent tip, with a known ceiling. A
designer with 32 years of practice advises: "Keep a list of sites you like and
just jam screenshots of the best bits" into the tool
([HN](https://news.ycombinator.com/item?id=48455991)). Another user reports the
limit: the model looks "for through-lines and high-level takeaways" and cannot
"lift specific details" from a screenshot, so people add written notes on what
they like ([HN](https://news.ycombinator.com/item?id=48445011)). An existing
component system does similar work, because "the model has a very specific
grammar to pull from instead of averaging over everything web-related"
([HN](https://news.ycombinator.com/item?id=48504912)).

A design system in the repository buys consistency more than identity. Adoption
signals are large (`awesome-design-md` at about 120,000 stars, Google's
`design.md` at about 28,000 on 2026-10-09)
([awesome-design-md](https://github.com/VoltAgent/awesome-design-md),
[design.md](https://github.com/google-labs-code/design.md)), but a token file
transmits whatever identity it holds, and stock tokens yield a consistent stock
look. Owned systems also collide with distinctiveness skills. An open pull
request against Anthropic's `frontend-design` skill says the skill "assumes a
blank-canvas brief" and proposes that existing tokens and components become
"fixed material," with distinctiveness coming from "information architecture,
content/voice, and component choices" ([anthropics/skills PR
#1561](https://github.com/anthropics/skills/pull/1561)). Impeccable recorded the
same conflict as a bug when its AI-palette rule "fired 80 times" in one review
of a site's own documented tokens, and 218 times across that site's 54 pages ([impeccable PR
#809](https://github.com/pbakaus/impeccable/pull/809)). A project with an owned
system has to tell any design skill that its tokens are fixed.

Design skills are the most adopted remedy and the least evidenced. Impeccable
holds about 79,000 stars and taste-skill about 94,000, yet no source reports a
measured effect ([impeccable](https://github.com/pbakaus/impeccable),
[taste-skill](https://github.com/Leonxlnx/taste-skill)). Impeccable's own HN
launch drew "That thing made all the designs worse" and the sharper charge that
it "seems to replace one knock-off visual identity for another," and the author
conceded the form example "got functionally worse"
([HN](https://news.ycombinator.com/item?id=46632320),
[HN](https://news.ycombinator.com/item?id=46635642),
[HN](https://news.ycombinator.com/item?id=46649766)). The author later stated the
structural risk: "if everybody uses the same skill to do frontend design
work... everything ends up looking the same," and "It's never going to be a tool
for one-shot design"
([Latent.Space](https://latent.space/p/skill-engineering-design)). The skills
now chase their own output. A September 2026 pull request notes that Anthropic's
skill lists five whole-page looks AI design falls into, and Impeccable added a
neobrutalist cluster in October after a run shipped one undetected
([anthropics/skills PR #1806](https://github.com/anthropics/skills/pull/1806),
[impeccable PR #934](https://github.com/pbakaus/impeccable/pull/934)). The same
PR quotes Anthropic's guidance that "a general instruction such as 'avoid a
generic AI look' mostly swaps one default for another," while naming specific
patterns works. An academic audit of 120 interfaces from five generative UI
tools found they converged in appearance and layout and that **over 25% of
stated design rationales were never implemented** ([arXiv
2607.22928](https://arxiv.org/abs/2607.22928)).

The table ranks techniques by breadth and credibility of endorsement in the
material reviewed. The ordering is a synthesis of dated statements, not a
measured result.

| Technique | Endorsement | Main reported failure |
|---|---|---|
| Human steering over many cheap iterations in real code | Very broad: designers, engineers, tool authors | Polish still takes real effort |
| Specific direction with committed fonts and palette | Broad, independent and vendor | Bare adjectives read as gradients and neon |
| Screenshots and reference sites, with notes | Frequent among independent voices | Model lifts themes, not details |
| Design system in the repository | Broad in guides, few direct reports | Consistency, not identity |
| Named-pattern ban lists | Vendor-endorsed, widely copied | Swaps defaults, ages within months |
| Design skills | Highest adoption, no measurement | Second-generation sameness, harm to density and contrast |
| Mockup-first tools | Praised for speed | Outputs converge and carry their own tells |
| Recolouring stock components | Tutorial praise only | Structure still reads as a template |
| One-shot generation | Rejected by everyone who addresses it | Generic output |

## Agents can check layout but cannot feel motion

Practitioners rate animation and interaction feel as the weakest part of agent
output, and the failure is specific: motion that runs and passes every check but
is wrong in origin, curve, or duration. Kieran Klaassen describes an agent-built
card that "slid in from the top of the screen instead of opening from where I'd
clicked," on a pull request where "the review agents had nothing left to flag"
([Every](https://every.to/source-code/how-i-polish-software-that-agents-built)).
An HN user calls frontend polish "the #1 most glaring" gap "that simply
re-prompting often can't solve" and adds that screenshots have not "solved
things like glitchy animations"
([HN](https://news.ycombinator.com/item?id=47545419)). Community tell lists
point the other way from "too little motion": the motion complaint that appears
is too many fade-ins, and the stance design engineers encode is restraint.

Giving the agent a browser is the single most broadly endorsed practice,
recommended by Anthropic ("Give Claude a check it can run: tests, a build, a
screenshot to compare") and reported as helpful by independent users ([Claude
Code best practices](https://code.claude.com/docs/en/best-practices)).
Playwright MCP with reference screenshots lets the agent verify output is
"nearly pixel perfect," and self-checking ends loops where the model keeps
claiming a bug is fixed ([HN](https://news.ycombinator.com/item?id=44777210),
[HN](https://news.ycombinator.com/item?id=45856645)). Three limits recur. Full
DOM or accessibility snapshots produce "huge dumps" that eat context, so users
scope captures to one element or viewport. Agents break on dynamic UIs and
sometimes hallucinate a success report
([HN](https://news.ycombinator.com/item?id=46921368)). And the benefit is
correctness (renders, overflows, matches the mock), not feel. Every strong
positive report pairs the loop with a target image. No one in the sample credits
an open-ended "does this look good" loop with anything.

The practice growing fastest in 2026 is packaging a design engineer's rules as a
skill and running a separate strict review pass. Emil Kowalski's position is
that "almost every 'taste' decision has a logical reason if you look close
enough," and the rules are numeric and binary: start from `scale(0.95)` not
`scale(0)`, ease-out on enter and exit, **UI animation under 300ms**,
micro-interactions at 100 to 150ms, exits faster than entrances ([Agents with
Taste](https://emilkowal.ski/ui/agents-with-taste)). The skills repository
reached about 44,700 stars in seven months and splits building from reviewing,
with separate commands for worst-case data and mobile quirks
([emilkowalski/skills](https://github.com/emilkowalski/skills)). Vercel ships
its Web Interface Guidelines as a review command covering accessibility,
keyboard support, forms, animation, and performance
([Vercel](https://vercel.com/changelog/web-interface-guidelines-now-available-as-an-agent-command)).
Evidence that these work is author testimony and stars. Anthropic adds a caution
about review loops in general: "A reviewer prompted to find gaps will usually
report some, even when the work is sound," and chasing every finding leads to
over-engineering ([Claude Code best
practices](https://code.claude.com/docs/en/best-practices)).

The final step in every candid account is a person in the running app.
Klaassen's instruction is "Click everything. Don't review the code or take
notes," and say what feels wrong aloud to the agent with hot reload running
([Every](https://every.to/source-code/how-i-polish-software-that-agents-built)).
The "last 10 to 20 percent" figure is folklore with varying numbers. One builder
reports vibe-coding about 70% and finishing "UI polish that was faster to do
directly" ([HN](https://news.ycombinator.com/item?id=46058180)). The stable part
is qualitative: what remains is whatever has no runnable check, and Anthropic's
own wording explains why, since "Claude stops when the work looks done" and
without a check "you become the verification loop" ([Claude Code best
practices](https://code.claude.com/docs/en/best-practices)). Experts report a
smaller residue than generalists, because someone who can state the rule gets
the fix and someone who can only say "it feels off" gets re-prompt loops.

Perceived performance is the thinnest area. Almost no practitioner sentiment was
found on optimistic updates, skeletons, loading and error states, or layout
shift in agent-built apps, and that silence is not evidence that agents handle
them well. On stack, the common view is that React gets the best output because
it dominates training data, and Svelte 5 is the documented counter-case of
models falling back to older syntax
([GitNation](https://gitnation.com/contents/how-i-taught-llms-how-to-svelte)).
Evidence for Vue is thin. The community `vuejs-ai/skills` project keeps a rule
only if an eval shows the model fails without it and has no animation or
transition skill, and the published motion skills target React and CSS
([vuejs-ai/skills](https://github.com/vuejs-ai/skills)). The CSS-level motion
rules transfer to any stack. The React library recipes do not.

## The brief outweighs the model, and the leaderboard churns

No model leads every board in October 2026. On LMArena WebDev (852,934 votes,
142 models, updated 2026-10-08) Claude Opus 5.5 leads at 1813, followed by GPT-6
Astra at 1786, Claude Sonnet 5.5 at 1774, and GPT-6.1 Sol at 1755, with
**margins of about ±15 that overlap across the top ranks** and the first Google
model at rank 9 ([arena.ai](https://arena.ai/leaderboard/webdev)). Design
Arena's website board, read through an aggregator, orders them differently: Muse
Spark 1.3 at 1362, Kimi K3 at 1341, Claude Opus 5.5 at 1338, with the top 15
inside 62 Elo points ([modelgrep](https://modelgrep.com/best/design)).
Open-weight models now sit within about 50 Elo of the leaders. Practitioner
comments lean toward Anthropic's models for visual taste ("You really have to a)
use Opus and b) use the frontend-design skill for decent results"), though the
same thread questions whether the skill does anything consistent
([HN](https://news.ycombinator.com/item?id=48504912)). Both arenas measure
first-impression preference on single-shot output, which says nothing about
consistency across a product, interaction feel, or behaviour under iteration.

Workflow sentiment is more settled than model sentiment. Every endorsed pipeline
moves the taste decision out of the coding agent and into an artifact it must
obey: a mockup, a design file, reference HTML and screenshots, or an existing
component library. People disagree on the artifact and agree that it must exist.
Mockup-first tools draw praise for speed and the same convergence complaint as
everything else: "They all look more or less identical out of the box," and
Impeccable users were filing Claude Design's own tells within days of its
release ([HN](https://news.ycombinator.com/item?id=48432559), [impeccable issue
#112](https://github.com/pbakaus/impeccable/issues/112)). In one informal
same-prompt test Claude Design gave "a really professional non-AI-slop result"
that a co-author still called "not ready for production use yet"
([createwith](https://createwith.com/blog/we-tested-claude-design-vs-figma-make-google-stitch-and-claude-code)).
A second recurring pattern is build with one agent and review with another, on
the reasoning that a model is poor at finding its own omissions ([Pablo
Stanley](https://pablostanley.substack.com/p/a-heated-rivalry-claude-code-openai)).

The surveys agree that judgment stays human. In the AI in Design Report 2026,
**50% of designers had pushed AI-generated code to production while about 80%
said AI assists but does not replace their quality judgment, final visual
polish, or creative direction** ([State of AI
Design](https://stateofaidesign.com/chapters/craft)). Figma's 2026 report shows
designers taking part in development doubling from 21% to 41% and developers
doing design rising from 44% to 60%
([Figma](https://www.figma.com/blog/2026-ai-report/)). Both publishers have a
stake in "design matters more." The working accounts match them anyway. A Jane
Street designer credits Claude with "free, unlimited iteration" while steering
the details in person ([Jane
Street](https://blog.janestreet.com/i-design-with-claude-code-more-than-figma-now-index/)),
and a designer on HN concludes that "someone needs to evaluate the output and
hold responsibility for the UI/UX"
([HN](https://news.ycombinator.com/item?id=49924708)).

## Dissenters call the average a feature for product UI

The dissent is substantial and well upvoted, and it mostly accepts the facts
while rejecting the verdict. Sameness predates AI ("let's not act as if now is
different than 2013"), and Krebs concedes that before AI "everything looked like
Bootstrap" ([HN](https://news.ycombinator.com/item?id=45625915),
[Krebs](https://www.adriankrebs.ch/blog/design-slop/)). Conventional UI is
defended as good UX: "I want an average website not a weird one"
([HN](https://news.ycombinator.com/item?id=45634688)). One essayist argues that
for internal tools, prototypes, and settings pages "the average is not a defect,
it is a gift" ([Sascha
Becker](https://saschb2b.com/blog/same-same-but-different)). shadcn is
recommended as the fix about as often as it is named as the tell, and of 76
Reddit comments naming the kit only 16 were negative ([comment
counts](https://github.com/JCarterJohnson/vibecoded-design-tells/blob/HEAD/unslop-ai-ui/comment_tell_counts.csv)).

The split tracks context. Marketing and launch pages, where memorability is the
point, draw the complaint. Dashboards and internal tools draw the defence, and
the criticism of anti-slop restyling is sharpest there: reviewers of
Impeccable's examples found that "the information density on the 'after' example
is much worse for most dashboard use cases"
([HN](https://news.ycombinator.com/item?id=46632632)). Even the strongest
critics rarely claim the generic look harms usability. Harm is claimed only for
particular patterns such as low-contrast dark text, gradient text failing
contrast, and decoration that imitates data ([Fountain
Institute](https://www.thefountaininstitute.com/blog/signs-vibe-coded-ui)).

## What follows for a product with its own design system

The two goals differ in how far they can be mechanised. Smoothness is partly
mechanisable: once a person can state a rule as a number or a yes-or-no check,
an agent can apply and review it, and the browser loop already handles whatever
is visible in a still frame. Distinctiveness resists that, because any aid that
many projects share becomes the next default within months. The durable source
of a distinct interface is therefore whatever a project owns and others lack:
its tokens, its components, its information architecture, and its voice. For a
product UI with an owned design system, the practical reading is to treat that
system as fixed material, write the project's own motion and interaction rules
in place of a general taste pack, keep automated review narrow and per concern,
and budget human time for using the running app.

Two open questions decide how much this matters. Nobody has measured whether end
users notice or penalise the agent-built look, so the case for distinctiveness
outside builder-facing launch pages rests on belief. And nobody has measured any
technique against another, so the adoption counts on design skills record
interest, not results. A team that runs its own small before-and-after
comparison on its own screens would hold better evidence than anything
published.