---
name: web-component
description: Build, change, or review a FlowSeer web UI component under frontend/web/src/ui/ or frontend/web/src/components/ (a Ui* primitive, a composite, an overlay, an animated or AI surface) so it meets the web component contract and passes the story, accessibility, i18n, token, and verifier gates. Use before writing, restyling, or reviewing a Vue component in frontend/web. Not for view logic, domain code, or backend work.
---

# Build a FlowSeer web component

This skill is the order in which a component meets the rules, and the
traps each step hides. The rules themselves live in:

- `docs/architecture/2026-09-28-web-component-contract-direction.md`:
  composition, i18n, the `ai` prop and generative catalog, overlays,
  motion, and proof. It was accepted on 2026-09-28. Landed components move
  onto it through a migration plan, so flag any conflict you find in
  existing code instead of fixing it outside the plan.
- `docs/architecture/2026-09-26-web-design-system-direction.md`: tokens,
  Tailwind, Reka for behavior, and Storybook.
- `docs/code-style-web.md`: TypeScript.
- `frontend/web/README.md`: the component catalogue and the AI target
  contract.

Load a reference when its trigger applies. Each one holds rules this file
does not repeat.

| Reference | Load when |
|---|---|
| `references/vue-authoring.md` | writing any component: props, emits, `v-model`, slots, attrs, composables, reactivity |
| `references/overlays-and-motion.md` | the component floats (menu, popover, listbox, tooltip, dialog, toast) or animates anything |
| `references/states-and-a11y.md` | the component is interactive, shows loading, empty, or error states, or holds a form |
| `references/data-dense.md` | it shows tables, numbers, units, identifiers, charts, meters, or stat tiles |
| `references/i18n-and-ai.md` | it renders user-visible text, or represents an entity, a value, or an action |
| `references/review.md` | reviewing or polishing a component, or before calling one done |
| `references/model-routing.md` | delegating component work to a worker |

## 0. Check the tools

From the worktree root:

```bash
.agents/skills/web-component/scripts/check-tools.sh
```

- **Exit 1** means a required tool is missing. Stop and ask the user
  whether to install it, naming the tool, its purpose, and the install
  line the script printed.
- **An optional tool missing** means only its review step is skipped. Say
  which one in the report, and ask before installing it.
- **Nothing is ever installed without the user's yes.**

## 1. Find the primitive

Look for an existing `Ui*` component first. `frontend/web/src/ui/index.ts`
is the barrel, and the README's component list says what each one does.
Extend a component with a variant before adding a sibling.

A new interactive component sits on the matching Reka primitive. Read
the primitive's props from the installed type declarations, which are the
authoritative version and need no network:

```bash
grep -n "interface ContextMenuTriggerProps" -A10 frontend/web/node_modules/reka-ui/dist/index4.d.ts
```

The props interfaces are split across `dist/index*.d.ts`, so grep all of
them when the name is not in `index4`. For behavior the component relies
on (event order, when `preventDefault` runs, what a prop reads
synchronously), read the primitive's source under
`frontend/web/node_modules/reka-ui/dist/<Primitive>/`.

Prose docs are optional and need the network:
`npx ctx7@latest docs /llmstxt/reka-ui_llms_txt "<one topic>"`.

Views never import `reka-ui`; only files under `src/ui/` do.

## 2. Write the component

- `src/ui/<family>/Ui<Name>.vue` with `<script setup lang="ts">`, an
  exported `Ui<Name>Props` interface, and `withDefaults`. Emits and slots
  are typed as `references/vue-authoring.md` says.
- Variants go through `tv()` from `tailwind-variants`. Never concatenate
  class strings.
- **Tokens.** Colors, radii, shadows, type, z-index, and durations come
  only from semantic tokens (`bg-card`, `text-muted-foreground`,
  `rounded-control`, `--ring`, `--z-overlay`, `--duration-base`).
  - The default Tailwind palette is cleared, so `bg-red-500` silently
    renders nothing.
  - Stylelint rejects hex values, named colors, and color functions that
    don't wrap a `var()`.
- **Preflight is off.** Unlayered legacy rules in `src/style.css` can beat
  a utility. Where a utility loses to a legacy element rule, `UiButton.vue`
  shows the accepted fix: the `!` important prefix on that utility.
- **No literal text.** Visible strings come from `t()` with a caller
  override prop (`references/i18n-and-ai.md`).
- **AI prop.** A component that represents an entity, a value, or an
  action takes the `ai` prop and calls `useAiTarget`.
- Export the component and its props type from `src/ui/index.ts`.

## 3. Write the stories and the tests

- **Stories.** `Ui<Name>.stories.ts` goes in the same directory and
  imports `./Ui<Name>.vue`. `src/ui/a11y.test.ts` fails when a `Ui*.vue`
  file is not imported by a colocated story, and it runs axe over every
  story.
  - Add one story per distinct state, named by scenario (`Loading`,
    `EmptyFilter`, `LongHostname`), with no duplicates.
  - Cover both themes and both locales.
- **Overlays.** An overlay needs an `AccessibilityAudit` story whose open
  state is anchored to a rendered trigger. It also needs an entry in
  `OVERLAY_AUDITS` in `src/ui/a11y.test.ts` naming the role that must
  render in `document.body`. Axe runs on the portalled root holding that
  element, not the whole body. A trigger other than click or input needs
  a new `triggerEvent` kind there.
- **Tests.** `Ui<Name>.test.ts` tests behavior through the DOM under
  happy-dom. happy-dom has limits:
  - It runs no layout.
  - It loads no app CSS, so Reka `Presence` unmounts at once and exit
    animations never play.
  - It runs no microtask checkpoint between the listeners of a dispatched
    event.

  To work around them:
  - Give elements geometry with a stubbed `getBoundingClientRect`.
  - Dispatch `cancelable: true` events and await a tick before reading
    `defaultPrevented`.
  - Query portalled content from `document.body`.
  - In tests that exercise reduced motion, stub `matchMedia` before the first
    mount so the reduced-motion query matches. The stub selects the reduced
    path. A file installs it before its first mount, because motion-dom keeps
    the first `MediaQueryList` it reads. Put normal-motion cases in a file
    without the stub. No stub is needed to avoid a throw. Do not mock the
    animation library or add an `offsetParent` stub.
  - Leave layout, stacking, and motion to step 4.

Run the audit alone while iterating, from `frontend/web`:

```bash
./node_modules/.bin/vitest run src/ui/a11y.test.ts
```

## 4. Look at it in a real browser

Layout, collision, stacking, exit animations, and reduced motion can only
be seen in a real browser. With Storybook running (`pnpm storybook`) and
agent-browser present, follow the screenshot loop in
`references/review.md`. It checks narrow and wide widths, light and dark
themes, and the open state of every overlay.

When agent-browser is missing, say so in the report and ask the user to
look, naming the stories. Do not call the component done on happy-dom
alone.

## 5. Dependencies

Never add, upgrade, or remove a package without the user's explicit
approval for that package. Before any `pnpm add`, ask through the
question tool (`AGENTS.md`, Agent behavior) and state:

- the package and exact version, its license, and its last release date
- what it does that the existing `Ui*` components, Reka, or a few lines
  of our own code cannot
- its runtime dependencies and unpacked size
- whether a direction record already allows it

Offer building it without the package as one of the options. A delegated
worker does not ask: it stops and reports the package and the reasoning
as its blocker. Approval covers the package named and nothing else.

Once approved, run `pnpm add` from `frontend/web`. The Bash sandbox
blocks the registry, so it runs unsandboxed. pnpm then rewrites
`pnpm-lock.yaml` in its own style, and the verifier's `prettier --check .`
fails on thousands of lines. Restore the committed formatting before
anything else:

```bash
./node_modules/.bin/prettier --write pnpm-lock.yaml
```

## 6. Verify

Run the review checklist in `references/review.md`. Then run the verifier
over every changed path, from the worktree root, and update the README's
component list in the same change:

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
```

Never add a lint, stylelint, or detector suppression to make the
component pass. That includes impeccable's `ignore-value` and
`ignore-file` hooks. Report the finding and ask.

The component is done when:
- the verifier passes
- every story renders in both themes and both locales
- the browser check in step 4 ran, or the report says it did not and why
