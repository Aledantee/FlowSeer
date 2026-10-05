---
title: Deployment, Phase 2, Stores - Plan
type: feat
date: 2026-10-03
artifact_contract: flowseer-plan/v2
execution: docs
---

# Deployment, Phase 2, Stores - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

A cluster running the base holds a highly available NATS, Postgres,
ClickHouse, and OpenFGA, and a cluster running the `dev` overlay holds one
instance of each. The means are Helm values for NATS, the CloudNativePG
operator, the ClickHouse operator, cert-manager, and OpenFGA, and Kustomize
manifests for the custom resources that declare the clusters.

Stop condition: the plan is wrong if the ClickHouse operator cannot run a
replicated cluster that keeps accepting inserts with one replica down.

## Decisions

The parent's decisions apply
([parent plan](2026-10-03-1533-feat-deployment-plan.md)). This phase adds:

- NATS runs three clustered servers with JetStream on a volume each, a
  WebSocket listener for leaf nodes, operator mode with a directory
  resolver, and `sync_interval: always` through `config.merge`. Why: the
  [central high availability record](../architecture/2026-10-03-central-high-availability-direction.md),
  "NATS leaves the device service", and the NATS chart's README for each
  setting.
- Helm installs NATS, the operators, and OpenFGA, each at a pinned chart
  version with a values file under `deploy/k8s/helm/`. Why: the deployment record,
  "Kustomize renders FlowSeer's manifests, Helm installs third-party
  software".
- The `Cluster`, `ClickHouseCluster`, and `KeeperCluster` resources are
  FlowSeer's own manifests in the Kustomize base. Why: they state FlowSeer's
  sizing, and a patch in the `dev` overlay is how the record scales them
  down.
- The shapes are the record's table: NATS 3 servers, Postgres 3 instances,
  ClickHouse 2 replicas in 1 shard, Keeper 3, OpenFGA 3. The `dev` shape is
  1 of each.
- OpenFGA uses the Postgres cluster through `datastore.existingSecret`, and
  the chart's bundled Postgres stays off. Why: the chart marks the bundled
  one deprecated (`charts/openfga/values.yaml`).
- OpenFGA and its Postgres share a cluster and a zone. Why: the operator
  authorization record says to place the engine close to its datastore.

## Requirements

1. The base declares the highly available shapes. Example: the rendered base
   holds a `Cluster` with `instances: 3` and a `KeeperCluster` with 3
   replicas.
2. The `dev` overlay declares one instance of each. Example: the rendered
   `dev` overlay holds `instances: 1`.
3. Every chart is pinned to a version. Example: the install command in
   `deploy/k8s/README.md` passes `--version` for each chart.
4. Postgres instances land on separate nodes in the base. Example: the
   rendered `Cluster` carries a required pod anti-affinity.
5. The NATS WebSocket listener is exposed with TLS passed through and
   serves the certificate central's edge listener serves. Example: both
   reference one Secret by name, and the rendered base holds no Ingress that
   terminates TLS for either.
6. Every NATS server fsyncs before it acknowledges. Example: the rendered
   server configuration holds `sync_interval: always` inside its `jetstream`
   block, where the server parses it (`nats-server` v2.15.0,
   `server/opts.go`, `parseJetStream`).
7. No store password is committed. Example: OpenFGA's values name a Secret
   the repository does not define.

## Out of scope

- Table schemas, migrations, and tenant isolation inside the stores. The
  ingestion record lists them as open.
- ClickHouse query limits, which the ingestion record requires before the
  first query.
- Backups.

## Open questions

- Whether each chart lets every image be pinned by digest. Unverified.
- Whether the chart's `config.merge` can add a key inside the `jetstream`
  block it renders. Unverified.
- Whether the operators and cert-manager run replicated from their charts.
  Unverified.
- The ClickHouse operator's maturity and which release to pin. Its README
  states neither. Unverified.
- Whether CloudNativePG replicates synchronously by default. Unverified.
- Nothing renders Helm values in a test without fetching the chart, so the
  values files are checked only by installing them.
