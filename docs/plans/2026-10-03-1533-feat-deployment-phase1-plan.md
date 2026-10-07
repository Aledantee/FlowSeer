---
title: Deployment, Phase 1, Central Image and Manifests - Plan
type: feat
date: 2026-10-03
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Deployment, Phase 1, Central Image and Manifests - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Central runs on a Kubernetes cluster from manifests in the repository. The
means are a container image for the device service, a Kustomize base under
`deploy/k8s/` with a `dev` overlay, and a gate that renders every overlay.

Stop condition: the plan is wrong if central still needs a volume of its own
after the central high availability plan lands.

## Decisions

The parent's decisions apply
([parent plan](2026-10-03-1533-feat-deployment-plan.md)). This phase adds:

- The image holds one statically linked binary. Why:
  `CGO_ENABLED=0 GOOS=linux go build ./src/services/device/cmd/device`
  succeeds on the current tree.
- The base image is pinned by digest and gets a dependency statement. Why:
  the [dependency admission record](../architecture/2026-10-01-dependency-admission-direction.md)
  requires it for a container image.
- Central is a Deployment with two replicas in the base and one in `dev`,
  with no volume claim. Its TLS pair and NATS keys mount from Secrets. Why:
  the
  [central high availability record](../architecture/2026-10-03-central-high-availability-direction.md),
  "Central runs as several replicas".
- The base spreads the replicas across nodes and carries a
  PodDisruptionBudget that keeps one available. Why: the record's definition
  of ready for high availability.
- The readiness probe calls the endpoint the central high availability plan
  adds.
- The prototext files become ConfigMaps through `configMapGenerator`. Why: a
  changed file then rolls the pod, and central has no reload path.
- The gate is a Go test under `test/conformance/` that renders each overlay
  and asserts on the result. Why: the Stop hooks already run every
  conformance gate, and a tier that skips when a binary is missing passes
  without checking anything
  (`docs/solutions/conventions/a-tag-gated-container-tier-lacking-the-binary-is-silently-inert.md`).

## Requirements

1. `kustomize build` succeeds for the base and the `dev` overlay. Example:
   `kustomize build deploy/k8s/overlays/dev` exits 0.
2. The operator API is not exposed outside the cluster. Example: in the
   rendered base, the Service that selects the API port has type `ClusterIP`.
3. The edge listener is exposed with TLS passed through. Example: the
   rendered base holds a `LoadBalancer` Service for the edge port and no
   Ingress that terminates TLS for it.
4. Central is replicated in the base and single in `dev`. Example: the
   rendered Deployment has `replicas: 2` in the base and `replicas: 1` in
   `dev`, and the base holds a PodDisruptionBudget for it.
5. No secret value is committed. Example: the credential volume mounts a
   Secret the repository does not define.
6. `GOALS.md` names the deployment record and no longer lists packaging and
   deployment as not decided.

## Out of scope

- NATS and the stores, which the second phase installs.
- A registry to push the image to, and a release pipeline.

## Open questions

- How the gate obtains Kustomize: a Go tool module under `tools/`, as
  `tools/buf` does for buf, or a pinned binary. A Go tool module is a new
  dependency under the admission record.
- Ports and hostnames for central's edge listener and the NATS WebSocket
  listener.
- Whether the verifier needs a path rule for `deploy/k8s/`. The verifier
  script may be a policy surface, which needs a guardrail review.
