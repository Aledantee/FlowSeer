# netpen t2: operator-supplied Cisco target

T2 currently checks environment settings and logs the eight intended attack/target
pairs. It does not execute netpen, connect to the target, assert vendor behavior,
or write results to the validation matrix. A passing test supplies no behavioral
evidence. Command execution and expected findings remain unimplemented.

Run the offline harness checks from the repository root:

```sh
go -C src/edge/netpen test -race -short -tags=netpen_t2 ./test/integration
```

The operator entry point is opt-in and never a gate. From `src/edge/netpen`, the
current placeholder can be invoked with an existing image and target:

```sh
NETPEN_T2_IMAGE=your-cisco-image:tag \
NETPEN_T2_TARGET=172.20.0.2 \
task tier-t2
```

The operator owns the image lifecycle and interface wiring. The harness skips
when Docker is unavailable, the daemon is stopped, or either environment setting
is absent. It reads the image name without inspecting or starting that image.

Vendor validation will require concrete commands, expected findings, and recorded
results before any row in [the matrix](../VALIDATION_MATRIX.md) can claim source
(b). The supplied target address alone does not establish that evidence.
