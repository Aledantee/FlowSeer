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

Storybook 10 Docs mounts several canvases into one document. When a decorator installs state in setup and cleans up in `onBeforeUnmount`, the first unmounting canvas strips the shared `window.flowseerAi` contract while others remain visible. Every canvas teleports an action layer into `document.body`, producing duplicate Ask buttons. Because `aiRegistry.onRequest` replaces the single handler, the last mounted canvas intercepts every request.

## What is true and why

The window contract, request dispatcher, and action layer belong to the document. Ownership must be tracked at module scope across canvases:

- Install document state on the first mount and release it on the last unmount so departing canvases do not strip shared contracts.
- Elect one canvas as layer host to render `UiAiActionLayer`. Re-elect when the host unmounts, and close only when no canvases remain.
- Route requests to the canvas containing the target element, preserving handlers for nested targets.
- Retain the registry's first registration on duplicate IDs so re-used IDs do not hijack existing handlers.
- Canvas labels from `parameters.ai.labels` are document-scoped state for the single layer. The layer displays labels for the canvas holding the active target. When no target is displayed, labels are cleared.

## Working example

`createStoryScope` in `frontend/web/.storybook/aiDecorator.ts` builds the scope once at module level, outside the decorator factory, and `storyScope` holds that single instance:

```ts
function createStoryScope() {
  const canvases = new Set<MountedCanvas>()
  const handlers = new Map<string, AiHandler>()
  const activeLabels = shallowRef<UiAiActionLayerLabels | undefined>()
  let host: MountedCanvas | undefined
  let removeWindow: (() => void) | undefined
  let removeDispatcher: (() => void) | undefined
  // ...
}
const storyScope = createStoryScope()
```

The dispatcher resolves the owner per request instead of relying on the single handler (`onRequest` in `openDocument`):

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

The host layer reports the target it displays through `targetChange`. The decorator listens and resolves active labels from that target's canvas:

```ts
function onTargetChange(id: string | undefined) {
  if (!id) {
    activeLabels.value = undefined
    return
  }
  const targetElement = aiRegistry.view(id)?.element
  const canvas = [...canvases].find(
    (c) => targetElement && c.element.contains(targetElement),
  )
  activeLabels.value = canvas?.labels
}
```

The decorator's mount returns a release that removes the canvas, re-elects a host when the leaving canvas owned one, and closes the document only when the set is empty (`mount` in `aiDecorator.ts`). The decorator renders the layer only for the host:

```html
<UiAiActionLayer
  v-if="showLayer"
  :labels="activeLabels"
  @target-change="onTargetChange"
/>
```

## Evidence

- `createStoryScope` in `frontend/web/.storybook/aiDecorator.ts` holds the module-level scope, host election, per-target dispatch, and target-driven label resolution.
- `keeps both canvases inspectable and releases them after the last unmount` in `frontend/web/.storybook/aiDecorator.test.ts` mounts two canvases, asserts one `.ai-ask`, keeps targets listable after the first unmounts, and deletes `window.flowseerAi` only after the second unmounts.
- `applies label overrides immediately to a nested element highlighted in onMounted` in `frontend/web/.storybook/aiDecorator.test.ts` verifies immediate label emission for nested elements.
- `holds the canvas-label invariant across all host, selection, focus, and panel states` in `frontend/web/.storybook/aiDecorator.test.ts` mounts two canvases (default and override with a nested target), drives host election, focus, selection, and closed and open panel states, asserts trigger and heading labels match the active target canvas (or no trigger when inactive), and leaves out three or more canvases and prompt submission.
- `frontend/web/src/ai/registry.ts:226-235` shows the registry keeps a single handler, which forces target-based dispatch.
- `frontend/web/src/ai/window.ts:31-33` deletes the window API only if the installed instance matches.
- Review fix commits `8d9167b6` (document scope), `c390cf26` (nested target routing), and `f86f447a` (shared registry).

## What this does not cover

- The application console at `frontend/web/src/main.ts:28`, where only one root mounts.
- Placement geometry and viewport clipping in `frontend/web/src/ui/ai/geometry.ts:135`.
