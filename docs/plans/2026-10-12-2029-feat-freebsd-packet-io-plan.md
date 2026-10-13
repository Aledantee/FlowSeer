---
title: FreeBSD Packet I/O - Plan
type: feat
date: 2026-10-12
artifact_contract: flowseer-plan/v2
execution: mixed
amends: docs/architecture/2026-10-03-edge-high-availability-direction.md
---

# FreeBSD Packet I/O - Plan

## Goal

An edge on FreeBSD has every raw packet path a Linux edge has: local
interface capture, the mirror receiver, the netpen leg, and the simload
sender. The agent builds for FreeBSD. The means are a shared `/dev/bpf`
package, FreeBSD implementations behind the existing seams, and a Vagrant
FreeBSD VM that runs the real-kernel tests.

Stop condition: the plan is wrong if the FreeBSD 14 kernel in the VM differs
from the `freebsd-src` `main` behaviour the direction record's table cites
in a way the shared package cannot absorb, for example a different
`bpf_hdr` layout on arm64.

## Decisions

The shared decisions are in the
[FreeBSD packet I/O record](../architecture/2026-10-12-freebsd-packet-io-direction.md),
accepted. The ones the user settled:

- Full parity: all four Linux-only packet paths get a FreeBSD
  implementation. (decided by the user, 2026-10-12)
- Real-kernel tests run on a FreeBSD VM booted by Vagrant. (decided by the
  user, 2026-10-12)
- This plan makes the agent build for FreeBSD by giving the store lock in
  `src/common/service` a FreeBSD build. That meets requirement 9 of the edge
  high availability plan. (decided by the user, 2026-10-12)
- The FreeBSD local capture source runs the capture filter in userspace, as
  Linux does. (decided by the user, 2026-10-12)
- Three phases. Why: the shared package, the agent build, the gate, and the
  VM harness come first because the other two import or run on them. The
  capture module and the two edge tools touch disjoint packages, so phases 2
  and 3 can run at once.
- Breaking renames of the Linux-only types (`linuxLocalSource`,
  `linuxMirrorSource`, `linuxLeg`, `linuxSender`) are allowed where a type
  becomes shared by the two platforms. Nothing outside these packages
  names them.

## Requirements

1. The agent, netpen, and simload build for FreeBSD amd64 and arm64.
   Example: `CGO_ENABLED=0 GOOS=freebsd GOARCH=arm64 go build
   ./src/edge/agent/cmd/agent` exits 0.
2. A local capture on a FreeBSD `epair(4)` end delivers a frame sent on the
   other end, with its VLAN tag. Example: a frame tagged VLAN 1000 sent on
   `epair0a` arrives from `OpenLocalInterface("epair0b", true, prog)` with
   `81 00 03 e8` at offset 12, and a VLAN 1000 filter accepts it.
3. The FreeBSD mirror receiver decodes GRE and VXLAN sent by FreeBSD's own
   `gre(4)` and `vxlan(4)`. Example: a ping through a `vxlan(4)` interface
   in a VNET jail arrives as a `Frame` whose `Envelope` is the VXLAN arm.
4. A netpen leg on FreeBSD sends a frame with a spoofed source MAC that the
   peer receives unchanged, and does not receive its own frame. Example:
   `Send` on `epair0a` with source `02:00:00:00:00:99` arrives on `epair0b`
   with that source, and `epair0a`'s `Receive` does not return it.
5. The simload sender on FreeBSD writes a complete frame. Example: a 1514
   byte frame sent on `epair0a` is captured on `epair0b` with all 1514
   bytes.
6. Platforms other than Linux and FreeBSD still return each package's
   `ErrUnsupportedPlatform`. Example: on darwin, `rawsocket.IsUnsupported`
   is true for `OpenLocalInterface`'s error.
7. A gate cross-compiles the FreeBSD packages and their tests on every run.
   Example: breaking a FreeBSD-only file fails `go test
   ./test/conformance/crossbuild` on a macOS host.

## Out of scope

- FreeBSD 32-bit architectures.
- A kernel read filter for local capture.
- The rc script that runs the agent on FreeBSD, which the deployment plan's
  third phase owns.
- CI that boots the VM. The VM suite is run by hand.
- Whether the embedded NATS leaf node behaves on FreeBSD beyond what the
  store lock test exercises.

## Units

### U1. Shared BPF device, agent build, gate, and VM harness

Files: docs/plans/2026-10-12-2029-feat-freebsd-packet-io-phase1-plan.md

### U2. Capture module on FreeBSD

Files: docs/plans/2026-10-12-2029-feat-freebsd-packet-io-phase2-plan.md

### U3. netpen leg and simload sender on FreeBSD

Files: docs/plans/2026-10-12-2029-feat-freebsd-packet-io-phase3-plan.md

Waves: U1 | U2 U3

## Verification

Each phase runs the verifier on its changed paths and its VM suite. After
the last phase, `uv run tools/scripts/run.py freebsd vmtest --suite all`
passes on a fresh `vagrant up`.

## Definition of done

- [ ] Every phase recorded implemented with its landed range.
- [ ] The edge high availability record no longer says capture or the agent
      build is Linux-only.

## Open questions

- Which Vagrant provider the development host uses. The Vagrantfile works
  with any provider the box publishes for arm64.
