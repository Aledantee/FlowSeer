---
title: Deployment - Plan
type: feat
date: 2026-10-03
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Deployment - Plan

## Goal

FlowSeer can be deployed from the repository: central, NATS, and the stores
on a Kubernetes cluster, and edge agents on site hosts. The means are a
container image, Kustomize manifests under `deploy/k8s/`, Helm values for
third-party charts, and a systemd unit for the agent.

Stop condition: the plan is wrong if central still needs a volume of its own
once the central high availability plan has landed.

## Decisions

The shared decisions are in the
[deployment record](../architecture/2026-10-03-deployment-direction.md),
proposed and awaiting acceptance. The ones the user settled:

- Kustomize renders FlowSeer's own manifests and Helm installs third-party
  charts. Why: nothing external consumes FlowSeer, so a values interface has
  no audience. (decided by the user, 2026-10-03)
- The plan installs NATS, ClickHouse, Postgres, and OpenFGA, highly
  available by default, with a `dev` overlay for a single node. Why: a
  deployment that forgets an overlay gets the durable shape. (decided by the
  user, 2026-10-03)
- This plan starts after the central high availability plan lands, and its
  third phase after the edge high availability plan. Why: the manifests
  describe the listeners, the NATS client configuration, and the replica
  count those plans leave behind. No phase here is written ready until then.
- Three phases. Why: the image and central's manifests, the stores, and the
  agent unit touch disjoint files, and the stores reuse the first phase's
  directory layout and gate.

## Requirements

1. `kustomize build` succeeds for the base and for every overlay. Example:
   `kustomize build deploy/k8s/overlays/dev` exits 0.
2. No rendered manifest exposes the operator API outside the cluster.
   Example: in the rendered base, the Service that selects the API port has
   type `ClusterIP`.
3. The base declares the highly available shapes and the `dev` overlay the
   single-node ones, as the record's table lists. Example: the rendered base
   holds a CloudNativePG `Cluster` with `instances: 3`, and the rendered
   `dev` overlay holds `instances: 1`.
4. No secret value is committed. Example: every `Secret` reference in the
   rendered base names an object the repository does not define.
5. The agent starts from a systemd unit that reads
   `/etc/flowseer/agent.textproto`. Example: `systemd-analyze verify`
   accepts the unit file.
6. `GOALS.md` names the three records and no longer lists packaging and
   deployment as not decided.

## Out of scope

- Any change to the device service or the agent. The central and edge high
  availability plans own those.
- Backups, and a release pipeline that publishes images.
- Code that reads ClickHouse, Postgres, or OpenFGA. The ingestion and
  operator authorization plans own it.
- A Helm chart for FlowSeer.

## Units

### U1. Central image and manifests

Files: docs/plans/2026-10-03-1533-feat-deployment-phase1-plan.md

### U2. NATS and the stores

Files: docs/plans/2026-10-03-1533-feat-deployment-phase2-plan.md

### U3. Edge agent packaging

Files: docs/plans/2026-10-03-1533-feat-deployment-phase3-plan.md

Waves: U1 U3 | U2

## Verification

Each phase runs the verifier on its changed paths. After the last phase,
apply the base to a three-node cluster and enroll an agent through the
passthrough LoadBalancers. Kill one central replica and one NATS server in
turn, and confirm after each that a mutation completes. Confirm from outside
the cluster that the operator API port does not answer.

## Definition of done

- [ ] Verifier green for every changed path in every phase.
- [ ] The deployment record is accepted, and `GOALS.md` names it.
- [ ] `deploy/k8s/README.md` shows the commands for the base and for `dev`.
- [ ] This plan's `status` is set, with an outcome note under its title.
- [ ] No plan labels in code.

## Open questions

The record's open questions carry over. The plan waits on the central high
availability plan, and every phase is re-planned when its turn comes.
