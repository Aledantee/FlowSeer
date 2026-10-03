---
title: A Storybook Decorator That Installs Document-Scoped State Must Own It at Module Scope and Release It on the Last Unmount
date: 2026-09-27
last_verified: 2026-09-27
category: architecture-patterns
module: frontend/web/.storybook
problem_type: architecture_pattern
component: web-console
severity: high
applies_when:
  - "Writing a Storybook decorator that installs a window global, a singleton registry handler, or a teleported overlay."
  - "A decorator or provider installs document-wide state per canvas while Storybook Docs renders several canvases into one document."
  - "Investigating an inspectable window API that vanishes, a duplicated overlay, or a story that answers with another story's handler once two canvases are open."
related_components: [web_ui, vue_components]
tags: [storybook, vue, decorator, lifecycle, singleton, document-scope, testing]
---

# A Storybook decorator that installs document-scoped state must own it at module scope and release it on the last unmount

## The situation

Storybook 10's Docs page mounts several story canvases into one document, and
each canvas runs the decorator again. A decorator written the ordinary way
installs its state in setup and removes it in `onBeforeUnmount`. That is correct
while one canvas owns the document, and wrong as soon as two are open:

- The first canvas to unmount deletes the shared `window.flowseerAi` contract,
  leaving the canvas still on screen with no API.
- Every canvas teleports its own action layer into `document.body`, so selection
  draws one Ask button per canvas.
- The registry holds a single handler (`registry.onRequest` replaces it), so the
  canvas that mounted last answers every story's request and each story's
  configured `parameters.ai.handler` is ignored.

## What is true and why

The window contract, the request dispatcher, and the action layer belong to the
document, not to one canvas. Because the decorator runs once per canvas,
ownership of the document-wide pieces has to be lifted out of any single canvas
and counted across them.

- Keep the document-wide pieces in a module-level scope created once, outside the
  decorator. Install on the first mount and remove on the last unmount, so a
  canvas leaving cannot strip state from the canvases that remain.
- Elect one mounted canvas as the layer host, and let only that canvas render
  `UiAiActionLayer`. When the host unmounts, elect another; only an empty set
  closes the scope.
- Route each request to the canvas that registered its target. With one registry
  handler for several canvases, the handler has to look up the request's target
  element, find the owning canvas, and call that canvas's handler. A story's own
  handler then serves its own requests, including a nested element registered
  inside the canvas rather than the story root.
- Keep the registry's first registration of a duplicate id. A second canvas that
  reuses a story id must not take over the target's handler or release it on
  unmount, or the original target loses the handler it was configured with.

## Working example

`frontend/web/.storybook/aiDecorator.ts:43` builds the scope once at module
level, outside the decorator factory, and `:123` holds that single instance:

```ts
function createStoryScope() {
  const canvases = new Set<MountedCanvas>()
  const handlers = new Map<string, AiHandler>()
  let host: MountedCanvas | undefined
  let removeWindow: (() => void) | undefined
  let removeDispatcher: (() => void) | undefined
  // ...
}
const storyScope = createStoryScope()
```

The dispatcher resolves the owner per request instead of relying on the single
handler (`aiDecorator.ts:57`):

```ts
removeDispatcher = aiRegistry.onRequest((request) => {
  const targetElement = aiRegistry.view(request.targetId)?.element
  const handler =
    handlers.get(request.targetId) ??
    [...canvases].find(
      (canvas) => targetElement && canvas.element.contains(targetElement),
    )?.handler
  // ...
})
```

The decorator's mount returns a release that removes the canvas, re-elects a host
when the leaving canvas owned one, and closes the document only when the set is
empty (`aiDecorator.ts:108-117`). The decorator renders the layer only for the
host: `<UiAiActionLayer v-if="showLayer" />`.

## Evidence

- `frontend/web/.storybook/aiDecorator.ts:43-123` holds the module-level scope,
  host election, and per-target dispatch.
- `frontend/web/.storybook/aiDecorator.test.ts:140-179` mounts two canvases,
  asserts one `.ai-ask`, keeps both targets listable after the first unmounts,
  and expects `window.flowseerAi` to be deleted only after the second unmounts.
- `frontend/web/.storybook/aiDecorator.test.ts:181-191` mounts the same story
  twice and asserts one target and one action layer.
- `frontend/web/src/ai/registry.ts:226-235` shows the registry keeps a single
  handler, which forces the decorator to dispatch by target.
- `frontend/web/src/ai/window.ts:31-33` deletes the window API only if the value
  there is still the one it installed.
- Review fix commits `8d9167b6` (document scope), `c390cf26` (nested target
  routing), and `f86f447a` (stories and the a11y audit on the shared registry).

## What this does not cover

- The application path. `frontend/web/src/main.ts:28` installs the contract once
  for the console, where no second canvas competes for the document.
- The action layer's placement geometry. Whether its Ask button is drawn or
  withheld is `placeAsk`'s concern (`frontend/web/src/ui/ai/geometry.ts:135`),
  not the decorator's.
