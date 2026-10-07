---
title: Console Frame Shared With Login, Phase 3, The Morph - Plan
type: feat
date: 2026-10-07
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Console Frame Shared With Login, Phase 3, The Morph - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Signing in turns the login page into the dashboard in one continuous
movement, and logging out turns it back. The page panel moves to the
menu's width while the form gives way to the menu and the description to
the dashboard, with nothing waited for and nothing replaced.

**Stop condition:** the dashboard takes long enough to mount that the panel
finishes moving before its content can appear. The morph then needs a
placeholder state, which is a design choice for the user.

## Decisions

The parent plan
(`docs/plans/2026-10-07-1224-feat-console-frame-login-morph-plan.md`) holds
the decisions that signing in does not wait, that the panel moves by a
transform, and that the View Transitions code is removed.

- The regions' content appears by opacity only. Why: the component contract
  allows `transform` and `opacity`, and a second movement inside a moving
  panel reads as noise.
- An amendment to
  `docs/architecture/2026-09-28-web-component-contract-direction.md` is a
  unit of this phase if the morph runs longer than 160 ms. Why: the record
  sets that budget, and a plan does not override a record.

## Requirements

1. Sign-in morphs without a wait. The example is the parent's
   requirement 5.
2. Reduced motion gets no movement. The example is the parent's
   requirement 6.
3. Logging out runs the morph in reverse. The example is the parent's
   requirement 7.
4. No View Transitions code remains. The example is the parent's
   requirement 8.

## Open questions

- How long the morph runs. The parent's first open question.
- Whether the login content animates in when a visit starts on `/login`.
  The parent's second open question.
- Whether the theme and language switches travel from the sidebar's foot to
  the top bar or fade in place.
