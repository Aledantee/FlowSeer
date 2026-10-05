---
title: Deployment, Phase 3, Edge Agent Packaging - Plan
type: feat
date: 2026-10-03
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Deployment, Phase 3, Edge Agent Packaging - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

A site host runs the edge agent as a systemd service from files in the
repository, and an edge group runs it on one to n hosts.
The means are a unit file under `deploy/systemd/` and a README that says
where the binary and the two prototext files go.

Stop condition: the plan is wrong if the agent cannot capture without
running as root.

## Decisions

The parent's decisions apply
([parent plan](2026-10-03-1533-feat-deployment-plan.md)). This phase adds:

- The agent is a statically linked host binary. Why:
  `CGO_ENABLED=0 GOOS=linux go build ./src/edge/agent/cmd/agent` succeeds on
  the current tree.
- The unit reads `/etc/flowseer/agent.textproto` and keeps state under
  `/var/lib/flowseer/agent`. Why: `deploy/lab/agent.textproto` and the lab
  runbook already use those paths.
- The unit grants raw-socket capability and no other. Why: capture opens raw
  sockets (`src/modules/capture/rawsocket`), and the rest of the agent needs
  no privilege.

## Requirements

1. systemd accepts the unit. Example: `systemd-analyze verify` on the unit
   file exits 0.
2. The agent does not run as root. Example: the unit names a dedicated user
   and an ambient capability set.
3. A crash restarts the agent, and a configuration error does not loop.
   Example: the unit restarts on failure and stops on the exit status the
   agent uses for a rejected configuration.

## Out of scope

- How the nodes of a group share its devices, which the edge high availability plan
  decides.
- A deb or rpm package, and a release pipeline that builds the binary.
- Running the agent in a container.

## Open questions

- The FreeBSD rc script, its paths, and how the agent gets its privileges
  there. The edge high availability record makes FreeBSD a target, and
  nothing in the tree runs on it yet.
- Which capabilities capture needs beyond `CAP_NET_RAW`. Read
  `src/modules/capture/rawsocket/local_linux.go` and
  `src/edge/agent/internal/capture/README.md` at re-plan.
- Which exit status the agent uses for a configuration it will never accept.
  The device service uses 2 (`src/services/device/cmd/device/main.go`).
- `systemd-analyze` does not exist on the development hosts, which run
  macOS, so the first requirement needs a Linux check.
