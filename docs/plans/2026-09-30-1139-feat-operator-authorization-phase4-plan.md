---
title: Operator Authorization Phase 4, Tenancy Admin Surfaces and the Action Trail - Plan
type: feat
date: 2026-09-30
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-30-1139-feat-operator-authorization-plan.md
---

# Operator Authorization Phase 4, Tenancy Admin Surfaces and the Action Trail - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Tenant admins enroll and remove members, define roles, and grant them.
Customers connect and disconnect service-provider tenants. Platform admins
are bootstrapped from configuration. Full-payload grants are explicit and
expire. Every one of these changes lands in the operator action trail
phase 3 introduces.

## Decisions

The parent plan's Decisions apply. To settle when this phase is planned:

- The admin API's package and RPC shapes, each with its authorization rule.
- The records that own members, roles, grants, and partner links, and how
  removal cascades to the member's grants in the tenant.
- How the first platform admin is bootstrapped without a chicken-and-egg
  grant.

## Requirements

Parent requirements 6, 7, 8, and 11 hold for the surfaces this phase adds, against the running device
service.
