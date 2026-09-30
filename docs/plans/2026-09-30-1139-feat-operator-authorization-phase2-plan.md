---
title: Operator Authorization Phase 2, OIDC, OpenFGA Client, Model, and Deployment - Plan
type: feat
date: 2026-09-30
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
parent: docs/plans/2026-09-30-1139-feat-operator-authorization-plan.md
---

# Operator Authorization Phase 2, OIDC, OpenFGA Client, Model, and Deployment - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

The device service can verify an operator's token against a configured OIDC
issuer and turn it into an `authn.Principal`, and can ask a standalone
OpenFGA through a `Checker` built on the OpenFGA API. The OpenFGA model
lives in the tree with tests, and the lab deployment runs Postgres, OpenFGA
with authentication and TLS, and an OIDC issuer. Enforcement stays off
until phase 3.

## Decisions

The parent plan's Decisions apply. To settle when this phase is planned:

- The OIDC library, checked against the vendor rule in `PRODUCT.md` and
  recorded with its licence. Any first-party goroutine for key refresh goes
  through `src/common/spawn`.
- How issuer and subject encode into `Principal.ID` within OpenFGA's id
  rules (characters and length), verified in the OpenFGA source at the
  pinned version.
- How organization claims map to tenant ids: a configured claim name, and
  for more than one issuer a per-issuer mapping. An issuer configured with
  no organization claim yields `claimed` for the tenant the request names.
- The configuration shape: new sections of `DeviceServiceConfig`
  (`spec/proto/flowseer/store/device/v1/service_config.proto`) for the
  issuer and for OpenFGA, with the preshared key read from a file, as
  `credential_root` and the certificate paths already are.
- The model file sits in `src/services/device/internal/authz/` beside the
  code that embeds it. `spec/proto` admits only `.proto` and README files.
- The lab OIDC issuer (Zitadel, Keycloak, or Dex).

## Requirements

1. A token from the configured issuer with a valid signature, audience,
   and expiry becomes a principal. The same token with a wrong audience is
   refused with `Unauthenticated`.
2. The model admits the parent's relation table and nothing that grants on
   `site` or `tag`. Model tests cover member via claim and enrollment,
   partner, platform admin, and full payload not inherited.
3. The `Checker` refuses to start against an OpenFGA whose store or model
   id is not the configured one.
4. A container-backed benchmark in
   `src/services/device/test/integration/` reports uncached Check and
   BatchCheck latency for the paths the
   [spike](../research/2026-09-30-openfga-authorization-spike.md) measured,
   and a test pins that `ListObjects` stops at the configured cap without
   marking the result partial.
5. The lab OpenFGA refuses a client without the preshared key.
