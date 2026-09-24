---
title: netpen Lab Vendor Validation - Direction
type: direction
date: 2026-09-23
topic: netpen-lab-vendor-validation
status: accepted-direction
---

# netpen Lab Vendor Validation - Direction

netpen records vendor behavioral truth from a live lab. A Linux injector runs
the static netpen binary on the same data segment as the target device. The test
driver starts the injection over SSH, then reads the target's own protocol state
over an interactive SSH session. A passing test therefore says that the vendor
accepted the injected behavior, rather than only that netpen transmitted the
expected frames.

For example, the OSPF validation injects netpen's fixed fixture from the Linux
host and runs `show ip ospf neighbor` on IOS-XE. The evidence is the attacker's
router ID in the device's neighbor table. netpen's JSONL finding still matters,
but it describes transmitted frames and cannot establish the device-side state
by itself.

## The live lab is the source of vendor truth

Each behavior defines one expected finding on the target. The test drives the
pre-deployed netpen binary through a non-interactive SSH command so stdout stays
an exact JSONL stream and stderr remains separate. It reads the vendor observable
with [`src/protocol/ssh`](../../src/protocol/ssh/README.md), using a show command,
an anchored vendor prompt, and the device's pagination marker.

The management and injection paths are separate. SSH uses the target's management
address. Packets leave the injector on a data interface that shares the protocol
segment with the target. The operator supplies that wiring and a device baseline
compatible with the behavior's fixture; the test verifies the prerequisite and
does not configure the device.

This makes restoration part of the claim. A live check observes the expected
state after injection and observes its removal after teardown or protocol expiry.
If a fixed fixture cannot create the expected vendor state, the validation stops.
Changing the harness to accept a weaker proxy would turn source-(b) evidence into
a transmission test.

## The tiers answer different questions

`netpen_t1` is the containerized reproducibility tier. It checks wire shape,
fixture behavior, and stable finding classes against controlled peers.

`netpen_t2` is the live vendor tier. It runs only when the operator supplies the
lab configuration and target baseline. It records the target's own observable
over SSH and is the source for vendor behavioral truth in the validation matrix.
A skipped live run remains pending evidence; it is never recorded as a pass.

## Consequences

- A new vendor or behavior needs a parser for its observable and a precise
  expected-finding definition. A successful netpen process is insufficient.
- Credentials and host-key pins enter through the lab environment. Passwords use
  `secret.Value`, and every SSH connection verifies the pinned host key.
- The static binary is installed and granted packet privileges before the tier
  runs. The harness reports a missing prerequisite instead of changing host or
  device configuration.
- The validation matrix stays hand-maintained. A passing run emits evidence that
  an operator records with the lab prerequisites and observed result.

This direction supersedes the fixtures-plus-`netpen_t1` limitation recorded in
the [netpen port plan](../plans/2026-08-23-1042-feat-netpen-port-plan.md). Fixtures
and the container tier remain useful, but they no longer stand in for a vendor's
own response.
