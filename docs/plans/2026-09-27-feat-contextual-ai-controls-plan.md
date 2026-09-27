---
title: Contextual AI Controls for the Web Console - Plan
type: feat
date: 2026-09-27
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: code
---

# Contextual AI Controls for the Web Console - Plan

> Implemented. 4 units, 2026-09-27T16:32Z to 2026-09-27T16:58Z.

## Goal

Make meaningful instances in the web console and Storybook addressable by stable ID, with a compact Ask action and on-demand summaries that honestly report the absence of an AI handler. Keep the existing web design system. Stop if an existing backend or authentication contract must be changed to make the local interaction work.

## Decisions

- Register targets through a Vue directive bound to a root DOM element, with `mounted`, `updated`, and `unmounted` owning registration, replacement, and removal. Why: views already key repeated rows by entity ID; Vue's directive hooks match their DOM lifetime ([Vue custom directives](https://vuejs.org/guide/reusability/custom-directives)).
- Qualify view target IDs with the physical pane slot (`a` or `b`), view, kind, and entity ID. `FleetView.vue` passes its slot to `PageHost.vue`, which provides it to view descendants; each component reads it during setup and supplies the full ID in its directive value. Standalone mounts use `standalone`. Swapping panes keeps each mounted page's slot and target IDs. Why: `PageContext.primary` changes when panes swap, while `navigation/panes.ts` keeps slot identity stable; a directive hook cannot call `inject()` for its own parent context.
- Use one document-scoped registry and one visual action layer per app or Storybook canvas. `window.flowseerAi` exposes `listTargets()`, `highlight(id)`, `clearHighlight()`, and `onRequest(handler)`; a returned unsubscribe removes the handler. Why: agent selection needs an explicit, inspectable contract while the application has no AI service. Duplicate IDs in one document are invalid; pane and view identity distinguish simultaneous renderings.
- Give CSS-only mobile and desktop copies of a device row separate `mobile` and `desktop` ID segments. `listTargets()` exposes only currently visible elements, and `highlight()` returns false for a hidden one. Re-evaluate visibility on each call and on viewport resize. Why: `WorkspacePage.vue` mounts both layouts at once and hides one with CSS, so mount cleanup alone cannot choose the visible instance.
- Put the selection outline on the registered element and the compact Ask button in one pointer-transparent overlay aligned to it, without nesting controls in a table row, chart, or button. Recompute geometry on ancestor scroll, resize, and pane movement; clip or hide the affordance outside its scroll viewport. Focus within a target reveals Ask, and Alt+A opens it from the focused target; the overlaid button is pointer operable but is not an out-of-order tab stop. Why: `WorkspacePage.vue` has nested scroll areas, interactive table rows, and moving split panes; altering their DOM semantics or tab sequence risks navigation and accessibility.
- Render the prompt and answer in `UiPopover` (or `UiDialog` if its focus behavior fits better), with `UiButton` for the trigger and `UiTextarea` for the prompt. Why: the accepted web direction puts interactive behavior on the existing Reka-backed `Ui` components, including dismissal and focus management.
- An `AiRequest` is a snapshot of `{ requestId, kind, targetId, label, context, prompt? }`. The registered handler returns a `Promise` of answer text; an absent handler rejects with an unavailable state. Ignore a completion if its target registration is gone or replaced. Why: a result must refer to the chosen instance and must not appear on a later instance reusing its ID.
- Use the current semantic coral (`--primary`) and cyan (`--accent`) tokens for the outline and shimmer; keep focus styling from the web direction record. Why: `docs/architecture/2026-09-26-web-design-system-direction.md` requires semantic tokens and Storybook theme parity. The existing web direction record covers this work, so no new direction record is needed.
- Keep this as one plan. Why: registry, action layer, summary, views, and stories all live in the single `frontend/web` package and import one another.
- Ruled: Both CSS-only device-row copies carry an ID segment (`desktop` and `mobile`), not only the mobile copy. Why: the registry decides visibility from the segment, and an unsegmented desktop ID would stay listable at narrow widths. Cost if wrong: the example ID `a:devices:device:d1` reads `a:devices:device:desktop:d1` in tests and the README.
- Ruled: A topology link registers on its HTML link-label button, not the SVG edge group. Why: the registry addresses HTML elements and the label is the element that already carries the link's pointer interaction, so registering the `<g>` would need an Element-typed registry and risk Vue Flow's edge handling. Cost if wrong: SVG targets would need `Element` support throughout the registry and geometry.

## Requirements

1. `listTargets()` returns visible mounted targets with stable IDs, labels, and explicit context. Example: after filtering out device `d1`, its row target is absent; returning to that filter restores the same ID. At desktop width, the CSS-hidden mobile copy is absent from the list.
2. `highlight(id)` selects and scrolls the exact mounted instance and returns `true`; an unknown ID returns `false` and clears an old selection. Example: `highlight('a:devices:device:d1')` follows `d1` through a sort or pane swap and shows Ask at that row.
3. Ask submits only a nonempty prompt with the selected target snapshot. Example: submitting whitespace sends no request; submitting “Why offline?” sends one `kind: 'ask'` request with `d1` context and shows its returned answer.
4. Missing and rejected handlers produce clear unavailable or error states. Example: Ask with no handler displays “AI is unavailable”; a rejection displays an error without a fabricated answer; unmounting the target before resolution discards the answer.
5. `UiAiSummary` makes no request until “Generate summary” is activated; retry makes a new request ID. A pending request shows muted bars with a narrow coral and cyan shimmer; success and error stop it. Reduced motion gives static bars and a text loading status.
6. View roots and meaningful repeated device, client, site, chart, and topology items have explicit context and pane-qualified IDs. Example: a device row in a second pane has a different ID from its primary-pane counterpart.
7. Every Storybook component family has an inspectable Ask example through a shared decorator, and composite stories register distinct items when they represent distinct targets. Selection, Ask, summary states, and both themes can be exercised in Storybook.
8. Existing row navigation, sorting, filtering, controls, and overlays remain usable with the action layer present. The accessibility audit and focused interaction tests pass.

## Out of scope

- AI backend, model provider, authentication contract, and generated answers in the application.
- Targeting decorative descendants of a registered component.
- Persisting responses across navigation or sessions.

## Units

### U1. Target registry and request contract

Files: `frontend/web/src/ai/`, `frontend/web/src/main.ts`, `frontend/web/src/ai/*.test.ts`, `frontend/web/README.md`
After: none
Change: A typed target registry manages directive mount, update, and unmount by element and ID; the public window API lists targets, selects or clears by ID, and installs an asynchronous handler. Request IDs are unique per invocation and request completion is valid only while the same registration remains mounted.
Tests: Registry tests cover sorted target discovery, duplicate IDs, updates, unknown IDs, stale selection removal, handler replacement/unsubscribe, request snapshots, and unmount during pending work.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ai frontend/web/src/main.ts frontend/web/README.md`

### U2. Shared action layer and summary component

Files: `frontend/web/src/ai/`, `frontend/web/src/ui/ai/`, `frontend/web/src/theme/`, `frontend/web/src/ui/ai/*.test.ts`, `frontend/web/src/ui/ai/*.stories.ts`
After: U1
Change: One overlay tracks the selected or hovered/focused target without blocking its controls. Ask uses an existing `Ui` overlay primitive for the prompt panel and renders pending, answer, and error states. `UiAiSummary` owns idle, loading, result, error, and retry presentation, using semantic tokens and reduced-motion styles. Its colocated stories land in this unit so the existing story coverage gate passes.
Tests: Component tests prove Alt+A and pointer access, nonempty Ask submission, unavailable/rejected/stale results, scroll and pane movement alignment, viewport clipping, summary request timing and retry, accessible status announcements, and reduced-motion fallback.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ai frontend/web/src/ui/ai frontend/web/src/theme`

### U3. Web view target coverage

Files: `frontend/web/src/FleetView.vue`, `frontend/web/src/navigation/PageHost.vue`, `frontend/web/src/navigation/page.ts`, `frontend/web/src/WorkspacePage.vue`, `frontend/web/src/DashboardView.vue`, `frontend/web/src/DeviceView.vue`, `frontend/web/src/ClientsView.vue`, `frontend/web/src/components/topology/`, `frontend/web/src/{FleetView,DeviceView}.test.ts`
After: U2
Change: Provide the physical slot through `PageHost`, then apply slot-qualified target IDs and explicit fixture context to view roots and meaningful rows, cards, charts, and topology nodes. The responsive device list uses distinct mobile and desktop IDs for its simultaneously mounted copies. Add on-demand summary placements where a view has useful context. Existing navigation and control elements retain behavior.
Tests: View mount tests register the directive in their test app, select rows after sort, filter, and pane changes; check duplicate-entity pane IDs, visible layout selection at desktop and narrow widths, and removal cleanup; verify row links and controls remain operable.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/FleetView.vue frontend/web/src/navigation frontend/web/src/WorkspacePage.vue frontend/web/src/DashboardView.vue frontend/web/src/DeviceView.vue frontend/web/src/ClientsView.vue frontend/web/src/components/topology frontend/web/src/FleetView.test.ts frontend/web/src/DeviceView.test.ts`

### U4. Storybook examples and audit

Files: `frontend/web/.storybook/`, `frontend/web/src/ui/a11y.test.ts`, `frontend/web/src/ui/table/UiTable.stories.ts`, `frontend/web/src/ui/card/UiCard.stories.ts`, `frontend/web/src/components/{TrafficChart,TrafficSparkline}.stories.ts`, `frontend/web/src/ui/ai/*.stories.ts`
After: U2
Change: A shared Storybook decorator initializes the target API independently of `main.ts`, registers each story instance as an inspectable target, and supplies a local demo handler, cleaning all listeners and DOM up on unmount. The wrapper makes Ask available for every existing component family without per-story edits. Composite table, card, and chart examples identify distinct items. AI stories exercise selection, Ask, summary loading, result, error, light, and dark states.
Tests: The existing `src/ui/a11y.test.ts` audit covers new stories and checks that each component-family story renders a registered target and Ask action. Story interaction checks exercise selected target and Ask.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/.storybook frontend/web/src/ui frontend/web/src/components`

Waves: U1 | U2 | U3 U4

## Verification

- From `frontend/web`: `pnpm test`, `pnpm typecheck`, `pnpm lint`, `pnpm build`, `pnpm build-storybook` and the existing Storybook accessibility audit.
- Run `.claude/skills/verify-change/scripts/verify-change.sh -- <all changed paths>` after each merged stage and before handoff.
- Inspect desktop and narrow layouts in light and dark themes, including a table row, a composite Storybook story, and the summary loading state. Confirm the outline and Ask button do not intercept row or overlay controls.

## Definition of done

- [ ] Each unit passes focused checks and the diff-aware verifier is green for every changed path.
- [ ] The package README documents the public API and unavailable state; any invalidated convention docs are updated in this change.
- [ ] This plan records implementation outcome under its title and sets `status: implemented`; `review` and `compound` checkpoints are recorded.
- [ ] No plan labels appear in code or comments.

## Open questions

None.
