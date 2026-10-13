---
title: A Counter Fence Kept Per Edge Id Must Reset When the Edge Re-Enrolls
date: 2026-10-13
category: architecture-patterns
module: src/services/device/internal/rawwindowapi
problem_type: bug
component: service_layer
severity: high
symptoms:
  - "A replacement edge that enrolled under a retired edge's id gets FailedPrecondition (stale fence) on every raw window subscribe and receives no controls"
  - "Windows opened for that edge stay pending until they expire, holding their slots and byte reservations"
root_cause: "Central fences the edge's raw window controls on a boot generation stored under the edge id, but the edge keeps its generation in its own state directory. A box with an empty state directory counts from one again, below the stored value."
resolution_type: code_fix
applies_when:
  - "Adding central state that orders or fences an edge's messages by a counter the edge persists locally"
  - "A retired edge id can be issued a new setup key and enrolled again from another machine"
  - "Choosing a value that tells a re-enrolled edge from a rekeyed one"
related_components: [edge, messaging]
tags: [fence, boot-generation, enrollment, setup-key, raw-window]
---

# A Counter Fence Kept Per Edge Id Must Reset When the Edge Re-Enrolls

## The situation

Raw windows fence an edge's controls on `(boot generation, process epoch)`.
The edge advances the generation in its state directory
(`src/edge/agent/internal/identity/store.go:150`, a missing file starts at
one). Central keeps the highest pair it saw in the edge's
`RawWindowEdgeRecord`. An edge id outlives the machine behind it: retiring an
edge and issuing a new setup key returns the id to pending, and a replacement
box enrolls under it with an empty state directory. Its first subscribe
carries generation 1 against a stored N, and the fence refuses it for as many
boots as N.

## What is true

The counter is only comparable inside one enrollment. Central must store which
enrollment the counter belongs to and treat a different one as a new process,
whatever generation it carries (`rawwindowapi/store.go:127`). Windows of the
old enrollment end with `EDGE_RESTART`, the same outcome as any new process.

The enrollment value is the id of the setup key the edge consumed
(`internal/host/host.go:342`). Enrollment marks that key consumed and keeps it.
Only `IssueSetupKey` sets a new one, and it refuses an enrolled edge. A Rekey
rotates the edge key and leaves the setup key alone, so a rekey is not a new
enrollment. The edge key and `enrolled_at` are the wrong choices for that
reason or because they move on a rekey.

## How to apply it

Name the incarnation next to every counter central stores for an edge, pass it
in from the edge service (`EdgeServiceConfig.EdgeEnrollment`), and compare it
before the counters:

```go
reenrolled := rec.GetProcessEpoch() != "" && enrollment != rec.GetEnrollment()
switch {
case reenrolled: // a new process whatever its generation says
case generation < current, generation == current && epoch != rec.GetProcessEpoch():
	return false, staleFence
```

## Evidence

- `rawwindowapi/store_test.go:358` `TestReenrolledEdgeIsAcceptedAsANewProcess`:
  generation 5, then a new enrollment at generation 1 is accepted, and the old
  window ends with `EDGE_RESTART`. Reverting the check fails it.
- `internal/host/enrollment_internal_test.go:37`
  `TestEdgeEnrollmentFollowsTheConsumedSetupKeyAndNotTheEdgeKey`. Making
  `edgeEnrollment` return `""` survived every other test, so this test is the
  only guard on the wiring.

## Not covered

An edge that loses its state directory without re-enrolling keeps its
enrollment, so it is still fenced. That is deliberate: it cannot be told apart
from a delayed message of the earlier run.
