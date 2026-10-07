---
title: Central High Availability, Phase 2, External NATS - Plan
type: refactor
date: 2026-10-03
artifact_contract: flowseer-plan/v2
execution: mixed
amends: docs/architecture/2026-08-20-device-service-and-inventory-direction.md
---

# Central High Availability, Phase 2, External NATS - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

The device service connects to a NATS cluster it does not run. The means are
a hub in `src/modules/edgebus` that configures a remote server over client
connections, and a `nats-server` process beside central in the lab run and in
the integration fixtures.

Stop condition: the plan is wrong if a per-edge account cannot be created on
a running server from a client connection.

## Decisions

The parent's decisions apply
([parent plan](2026-10-03-1533-feat-central-high-availability-plan.md)). This phase adds:

- The server runs in operator mode with a directory resolver, and central
  pushes each account's claims on `$SYS.REQ.CLAIMS.UPDATE`. Why: the pinned
  server subscribes to that subject for a directory resolver
  (`nats-server` v2.15.0, `server/accounts.go`, `DirAccResolver.Start`),
  and the embedded hub already signs account claims with an operator key
  (`src/modules/edgebus/hub.go`, `StartHub`).
- Central signs account claims with an operator signing key, never with the
  operator key itself. Why: a server whose operator sets strict signing key
  usage refuses a pushed claim the operator key issued
  (`server/accounts.go`, the `claim.Issuer == op && strict` branch), and the
  operator key can then stay off every central replica.
- One account per edge stays the security boundary. Why:
  `src/modules/edgebus/README.md`, "Accounts are the security boundary".
- Central reads the operator, system, and CENTRAL keys from files its
  configuration names and never generates them into `state_dir`. Why: every
  replica must sign with the same keys.
- Per-edge account keys move from files under `state_dir/keys` to a
  JetStream key-value bucket in the CENTRAL account. Why: a replica that did
  not create an account must still mint its user.
- The stream replica count is a field of `DeviceServiceConfig`. Why: central
  creates the streams, and the `dev` overlay runs one server.
- The NATS WebSocket listener serves the certificate central's edge listener
  serves. Why: `EdgeProvisioning.trust_anchors` then needs no second pin.
- The integration fixtures start an embedded server configured as the
  upstream one is. Why: the tests need no external binary, and a fixture
  with a different resolver would not prove the push.

## Requirements

1. Central starts no NATS server. Example: with no server reachable, central
   exits with a connection error and writes nothing under `state_dir`.
2. An edge attaches through an account central pushed at run time. Example:
   the existing end-to-end test passes against a server that started with
   only the system account preloaded.
3. One edge cannot address another edge's JetStream API. Example:
   `TestOneEdgeCannotAddressAnotherEdgesJetStreamAPI` passes against the
   external server.
4. A second central process mints a valid user for an edge the first one
   attached. Example: a leaf node authenticates with a credential minted by a
   process that did not create the account.
5. Streams and buckets carry the configured replica count. Example: with the
   count set to 3 on a three-server cluster, `device-lanes` reports three
   replicas.

## Out of scope

- Running central as several replicas, which the third phase does.
- Installing NATS on Kubernetes, which the deployment plan does.

## Open questions

- Whether every server of a cluster holds a pushed claim before an edge
  dials it. The server source has a claims pack exchange between servers
  (`server/accounts.go`, `accPackReqSubj`) and a lookup by account. It was
  read and not run, and requirement 4 on three servers needs a test.
- Where the per-account disk budgets and the store ceiling go. They are
  account claims and server configuration now, and
  `docs/solutions/architecture-patterns/per-account-jetstream-disk-budgets-reserve-against-the-server-store-ceiling.md`
  needs a refresh.
- What replaces the hub's re-attach of persisted edge accounts at start,
  which
  `docs/solutions/architecture-patterns/a-restarted-hub-must-re-attach-every-persisted-edge-account.md`
  describes.
- Whether per-edge account keys in a bucket need the redacting carrier the
  secret material record proposes.
