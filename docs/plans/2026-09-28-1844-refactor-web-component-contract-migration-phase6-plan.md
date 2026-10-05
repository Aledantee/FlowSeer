---
title: Web Component Contract Migration, Phase 6 - Generative UI Catalog and Renderer - Plan
type: feat
date: 2026-09-28
artifact_contract: flowseer-plan/v2
execution: code
---

# Web Component Contract Migration, Phase 6 - Generative UI Catalog and Renderer - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`src/ai/catalog.ts` lists the components an agent may render, each with
a hand-written prop validator. `UiAiRender` validates an agent-supplied
tree, `{ component, props, children? }[]`, and renders real `Ui*`
components, or rejects the whole tree. Interactive nodes carry declared
intents, never handlers.

## Decisions

- The contract's "Agents compose UI from a catalog" and "Agents act
  through the API" sections govern.
- No schema library is added. Validators are hand-written, as the
  contract decided.

## Requirements

1. A valid tree renders its components. Example: `UiStatusBadge` with
   `status: 'Offline'` inside `UiCard`.
2. An unknown component, an unknown prop, an invalid prop value, or a
   function-valued prop rejects the whole tree with the error state.
   Example: a node naming `div`, or passing `onClick`, renders nothing
   from the tree.
3. A navigate intent routes through `PageContext.go`. An API-proposal
   intent renders a confirmation the user must accept, and until the
   service API exists it reports that it cannot be carried out.
