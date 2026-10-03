---
title: A Storybook Decorator That Installs Document-Scoped State Must Own It at Module Scope and Release It on the Last Unmount
date: 2026-09-27
last_verified: 2026-10-03
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

Storybook 10 Docs mounts several canvases into one document. When a decorator installs state in setup and cleans up in `onBeforeUnmount`, the canvas that unmounts first can strip the shared `window.flowseerAi` contract while others remain visible. Because `aiRegistry.onRequest` replaces the single handler, without dispatch routing the last mounted canvas would intercept every request.

## What is true and why

The window contract and request dispatcher belong to the document. Ownership must be tracked at module scope across canvases:

- Install document state on the first mount and release it on the last unmount so departing canvases do not strip shared contracts.
- Route requests to the canvas containing the target element. This preserves handlers for nested targets.
- Retain the registry's first registration on duplicate IDs so re-used IDs do not hijack existing handlers.
- Each canvas wraps its content in its own `UiAiContextLayer`. Because the context layer scopes its capture listeners and key listeners to its own layer root, per-canvas layers remain isolated without needing host election.

## Working example

`createStoryScope` in `frontend/web/.storybook/aiDecorator.ts` builds the scope once at module level, outside the decorator factory, and `storyScope` holds that single instance:

```ts
function createStoryScope() {
  const canvases = new Set<MountedCanvas>()
  const handlers = new Map<string, AiHandler>()
  let removeWindow: (() => void) | undefined
  let removeDispatcher: (() => void) | undefined
  // ...
}
const storyScope = createStoryScope()
```

The dispatcher resolves the owner per request instead of relying on the single handler (`onRequest` in `openDocument`):

```ts
removeDispatcher = aiRegistry.onRequest((request) => {
  const targetId = request.targets[0]?.id
  const targetElement = targetId
    ? aiRegistry.view(targetId)?.element
    : undefined
  const handler =
    (targetId ? handlers.get(targetId) : undefined) ??
    [...canvases].find(
      (canvas) => targetElement && canvas.element.contains(targetElement),
    )?.handler
  if (!handler) return Promise.reject(new AiUnavailableError())
  return handler(request)
})
```

Each canvas mounts its own layer directly:

```html
<div ref="root" data-ai-story-root>
  <UiAiContextLayer>
    <story />
  </UiAiContextLayer>
</div>
```

## Evidence

- `createStoryScope` in `frontend/web/.storybook/aiDecorator.ts` holds the module-level scope and per-target dispatch.
- `keeps both canvases inspectable and releases them after the last unmount` in `frontend/web/.storybook/aiDecorator.test.ts` mounts two canvases, keeps targets listable after the first unmounts, and expects the decorator to have deleted `window.flowseerAi` only after the second unmounts.
- `routes a nested component target to its canvas handler` in `frontend/web/.storybook/aiDecorator.test.ts` tests that action requests route to the specific canvas holding the nested target.
- `keeps a duplicate story id invalid` in `frontend/web/.storybook/aiDecorator.test.ts` mounts the same story twice and asserts one registered target and a warning log.
- `frontend/web/src/ai/registry.ts` shows the registry keeps a single handler, which forces target-based dispatch.
- `frontend/web/src/ai/window.ts` deletes the window API only if the installed instance matches.

## What this does not cover

- The application console at `frontend/web/src/main.ts`, where only one root mounts.
