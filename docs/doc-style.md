---
name: Documentation & Prose Style
last_updated: 2026-08-29
---

# FlowSeer — Documentation & Prose Style

How to write prose that developers trust: README files, everything under
`docs/`, schema comments, commit messages, PR descriptions. It binds human
contributors and coding agents equally, the same way
[`code-style.md`](code-style.md) does for Go. Where that document governs *when*
a comment exists, this one governs how all our prose reads.

Two research passes ground it (2026-08-29): one on what developers praise and
abandon in real documentation, one on how readers detect machine-written text
and what that detection costs. The findings are condensed below with sources;
the rules follow from them.

## What the evidence says

**Working examples are the currency of trust.** Across "best docs ever" threads,
the loudest praise goes to docs that show the thing working: Stripe's
copy-paste snippets with your own credentials inlined, Lodash's examples you can
run in the console, Redis stating time complexity and version on every command
page. The inverse is the fastest way to lose readers: one stale example and
"they start doubting everything else."

**Reference dumps are not documentation.** Steve Losh's line has held up for a
decade: an auto-generated API reference "lacks the coherent voice and structure
necessary for teaching." Docs that only say *what* draw the same complaint —
Google's own docs get dinged as "very focused on what and not why/how." Diátaxis
makes the structural version of the point: tutorials, how-tos, reference, and
explanation serve different moments, and mixing them undermines each.

**Honest, dense prose beats marketing tone.** PostgreSQL's docs get praised for
their "human feel"; SQLite's for documenting the hard parts (locking,
multi-process access) most projects omit. Developers explicitly resent sales
language in docs — one HN commenter argues good docs should "turn away
developers from your framework as quickly as possible" when it's not the right
fit, because honest disqualification saves everyone time.

**A field comment earns its place by stating what the type cannot.** Google's
AIP-192 is the canonical checklist: default when omitted, units, valid ranges
with inclusivity, what unset means, side effects, truncate-vs-error behavior.
A comment that restates the field name is noise.

**Machine-looking text carries a trust tax.** Maintainers describe AI-slop
contributions as "tantamount to a DDoS" (curl's Daniel Stenberg; the project
ended its bug bounty over it). The NixOS ban discussion puts the reader's side
plainly: "LLMs allow people to submit PRs that they don't understand, which
places the burden of understanding exclusively on the side of the reviewer."
Studies find readers rate AI-attributed text as less trustworthy even at equal
quality. The polish itself is the problem: slop "has none of the tells of
rushed code," so every reviewer has to simulate harder.

**The #1 code-level tell is indiscriminate commenting.** "A codebase with a
comment above every line is almost always AI slop, as the author was not
thinking about which lines deserved explanation." The pre-AI consensus already
condemned exactly those comments; the rule didn't change, the failure got
cheaper to mass-produce.

## Rules for docs and README files

1. **Show it working early.** A concrete, realistic example beats a paragraph of
   description. If the doc explains a mechanism, include the case a reader can
   check themselves (the tag README's `EMEA/Berlin/DC-1` rollup is the model).
2. **Say why.** Every design-shaped statement carries its reason: not "the
   parent is a field," but what that buys (stable keys across reparenting).
   If you can't state the why, you've found a decision worth questioning.
3. **Document the hard parts.** Concurrency, failure modes, invariants the
   schema can't express, what happens on deletion — the SQLite standard. Docs
   that only cover the happy path read as advertising.
4. **No marketing register.** No "powerful," "seamless," "robust," or
   "comprehensive." State capability limits plainly; a reader who leaves early
   because the tool doesn't fit was served well.
5. **Write for the rushed reader.** Front-load the point, keep paragraphs to a
   few lines, link related concepts instead of re-explaining them. Density is a
   courtesy, padding is a cost.
6. **Don't mix modes.** A package README explains shape and rationale; field
   contracts live in the schema comments; task walkthroughs live elsewhere.
   When a doc starts doing two jobs, split it.
7. **Stale is worse than missing.** Update the doc in the same change that
   invalidates it, or delete the claim. An outdated example poisons trust in
   every correct one around it.

## Rules for schema and code comments

[`code-style.md`](code-style.md) and [`code-style-proto.md`](code-style-proto.md)
already govern when a comment exists and what a contract states. The additions
from this research:

- A field comment must say something the type system cannot: units, ranges and
  their inclusivity, what unset means, side effects, localization or
  truncation behavior. If nothing qualifies, the comment is the field name in
  more words — delete it.
- Keep the repo's uniform contract phrasing ("Must be present.",
  "Unset means …"). Mechanical uniformity in *contract* sentences is a feature
  readers scan by; the human-voice rules below apply to explanatory prose, not
  to these formulas.
- Never comment every line, and never narrate a diff. Comment density is a
  signal of judgment; indiscriminate commenting is the single most-cited slop
  tell.

## Write like a person

Readers pattern-match on machine tells and discount what they read when the
tells pile up. Avoid the catalogued ones:

- **Em-dash chains.** One per paragraph is punctuation; three is a fingerprint.
  Use plain sentences, commas, parentheses.
- **Rule-of-three lists** ("filters, reports, and grants") and symmetric
  bullet sets where every item has the same length and shape. Real lists are
  ragged because reality is.
- **Negative parallelism**: "not X, but Y", "it's a query, not a field".
  Occasionally sharp; as a habit, a tell. Say the positive form.
- **Trailing participles**: "…, ensuring consistency", "…, falling through to
  English", "…, highlighting its importance". End the sentence, start another.
- **Inflated significance**: nothing here "plays a vital role", "serves as a
  testament", or is "crucial". If it mattered, show the consequence instead.
- **Puffery vocabulary**: delve, robust, seamless, comprehensive, leverage,
  vibrant, pivotal, landscape, "serves as", "stands as". Use the plain verb.
- **Bold-lead-in bullets** where the sentence restates the bolded term, and
  boldface or emoji as decoration.
- **Wrap-ups**: "In conclusion", "In summary", restating what the reader just
  read. Stop when the content stops.
- **Vague attribution**: "experts note", "it is widely known". Cite the source
  or drop the claim.
- **Uniform sentence rhythm.** Vary length. A short sentence lands.

The positive version is simpler than the checklist: know what you want to say,
say it in the order the reader needs it, and cut everything you wrote while
deciding. A doc reads human when its author made choices — which example to
show, which caveat matters, what to leave out — and machine when it hedges
toward completeness.

## Sources

Developer sentiment and docs quality:

- Ask HN: best API documentation — https://news.ycombinator.com/item?id=17905919
  and https://news.ycombinator.com/item?id=34820382
- Ask HN: key to good technical documentation —
  https://news.ycombinator.com/item?id=20909783
- Steve Losh, "Teach, Don't Tell" —
  https://stevelosh.com/blog/2013/09/teach-dont-tell/
- Diátaxis — https://diataxis.fr/
- Google AIP-192 (API documentation) — https://google.aip.dev/192
- Google developer documentation style guide —
  https://developers.google.com/style/highlights
- PostHog on iterating docs — https://posthog.com/newsletter/what-nobody-tells-devs-about-docs
- Postman State of the API (docs as adoption gate) —
  https://voyager.postman.com/doc/postman-state-of-the-api-report-2025.pdf

Comments and machine-writing tells:

- Stack Overflow blog, best practices for comments —
  https://stackoverflow.blog/2021/12/23/best-practices-for-writing-code-comments/
- antirez, "Writing system software: code comments" —
  http://antirez.com/news/124
- Ousterhout vs. Martin on comments —
  https://github.com/johnousterhout/aposd-vs-clean-code
- Wikipedia, "Signs of AI writing" —
  https://en.wikipedia.org/wiki/Wikipedia:Signs_of_AI_writing
- curl and AI slop reports —
  https://www.theregister.com/2025/05/07/curl_ai_bug_reports/
- NixOS discussion on LLM-generated PRs —
  https://discourse.nixos.org/t/can-we-explicitly-ban-llm-generated-pr-descriptions-commits/79736
