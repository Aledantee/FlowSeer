# netpen t2 — operator-supplied vIOS-class Cisco target

The t2 tier is the **behavioral-truth** validation layer: a real Cisco
NOS validates that each superset attack produces the expected finding
against a real switch, not just the correct wire shape. It is opt-in,
operator-supplied, and **never a gate** — it skips cleanly when the
operator has not provided a target image.

## Prerequisites

1. A vIOS-class image (Cisco IOS/IOS-XE running under your container
   runtime, e.g. `cisco/vios-l2` or an equivalent). The operator owns
   the image lifecycle (licensing, startup config, interface wiring).

2. Docker (or a compatible container runtime) with the daemon running.

## Running

Set two environment variables and run the tier:

```sh
NETPEN_T2_IMAGE=cisco/vios-l2:latest \
NETPEN_T2_TARGET=172.20.0.2 \
task tier-t2
```

- `NETPEN_T2_IMAGE` — the image name the operator has loaded. When
  empty, the tier skips with a printed reason.
- `NETPEN_T2_TARGET` — the management IP of the running vIOS instance.
  When empty, the tier skips.

The operator starts the image before running the tier; netpen connects
to `NETPEN_T2_TARGET` and runs each superset attack.

## What t2 validates

Each of the eight superset attacks (ospf, eigrp, wpad, etherchannel, mld,
raflood, lldpspoof, glbp) runs against the vIOS target and the findings
class is recorded in `VALIDATION_MATRIX.md` under ground-truth source
**(b)** — vendor behavioral truth. This upgrades the (c) ring-only rows
("wire-shape-validated, behavior-unvalidated") to full behavioral
validation.

## Never automated

t2 is never run by automation. The operator provides the image and runs
the tier by hand. The build tag `netpen_t2` is never in the default `go
test ./...` run.
