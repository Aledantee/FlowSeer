# Device access agent

The agent that runs where the devices are. It enrolls with central once,
holds central's dispatch stream open, drives the local-network access lane
from what arrives on it, and reports back what happened.

Assembled from `src/modules/localnet/access` (the lane) and
`src/modules/edgebus` (the leaf node and the loopback OTLP receiver). It owns
no device logic of its own: what it adds is identity, transport, and the
orderings between them.

## What it is made of

| Package | What it does |
| --- | --- |
| `internal/identity` | The Ed25519 key pair central registered, the enrollment answer, and the assertion signer every call except `Enroll` carries |
| `internal/busattach` | `AttachBus`, the embedded leaf node, and the loopback receiver the agent's own telemetry goes to |
| `internal/dispatch` | The `Subscribe` loop with backoff, and the routing of each dispatch to its lane call |
| `internal/report` | The re-send queue for dispatch reports, and the blocking deliverer for audit records |
| `internal/lanehost` | Contact with central and the freeze it drives, the device listing and what it onboards, and the per-operation device session factories |

## Which devices this edge serves

The edge is not configured with its devices; it asks. `ListDevices` names
them and, for each, where it answers, the binding that reaches it, the
access policy it pins and its measured delayed-apply horizon — all of it
read from a registry the edge cannot see. Configuring it locally instead
would make the address a second source of truth, and an address that is
wrong in one of two places fails open: the session opens, against a
different real device that answers normally.

The listing runs before every attempt to open the dispatch stream, first
attempt included, because a dispatch for a device this edge has not
onboarded has nowhere to go. Between listings the edge holds what it was
last told, which is stale by construction and the point: one operation runs
against one consistent set of facts rather than a set that can change under
it.

A device already onboarded is left alone rather than re-added — a second
`AddDevice` replaces that device's whole lane state, orphaning its queue and
any drainer working through it — so a device whose listing has changed keeps
the values it started with until the agent restarts. That is a real gap and
it is recorded rather than passed over: a re-listing that differs from what
a device was onboarded with names each field and both values, because an
operator who measures a horizon centrally and watches mutations go on being
refused has otherwise nothing anywhere connecting the two facts.

A device listed without a measured horizon is onboarded all the same and
read as usual; the lane refuses a mutation on it. The horizon bounds how
long a mutation's effect may stay unknown, and a read has no effect to
become visible.

## Nothing standing

Every credential is acquired for the operation that uses it and the session
is closed when that operation ends. There is no cached device session, no
standing SNMP user, and no lease — so a credential central revokes stops
working at the next operation rather than whenever a connection happens to
drop. The cost is one acquisition per read and per mutation, which is what
`AcquireReadCredential` is shaped for.

The same reasoning runs through the identity: the private key is the whole of
what this edge is, central holds only the public half, and an edge that loses
it cannot be recovered by anything the edge itself can do. It is written
before the first `Enroll` and never sent.

## The two things that are held open

A dispatch stream and a heartbeat, and both are mechanisms that act when
nothing is happening — which is the shape that hides a total failure as
silence.

`dispatch.Contact` counts streams central served, failures, and messages, so a
client dead since its first attempt is a number rather than a quiet fleet.
Read together: connections climbing with failures at zero is healthy;
connections at zero with failures climbing is a client that has never reached
anything; both at zero is a first stream still open. Connections climbing with
messages at zero is a central that accepts and immediately closes, which reads
healthy and is a loop backing off to its ceiling.

The heartbeat counts consecutive failed attempts, not elapsed time, and each
attempt is bounded by the interval. A laptop asleep for an hour wakes with an
hour since its last success and nothing wrong, so elapsed time would freeze
every device on resume. A call that never answers is contact lost by any
useful definition but is not a failure — it never returns to be counted — so
without the per-attempt deadline the counter stops exactly when it matters.
Two misses freeze the lane; the next success unfreezes it, and only a freeze
this loop performed is one it will lift.

## What central is owed, and what it is owed by

Reports are queued and re-sent until central confirms them. The queue
supersedes rather than accumulates — one entry per operation holding its
latest report — so its size tracks outstanding operations, and since reports
are caused by dispatches, an unreachable central cannot make it grow.

Audit records are the opposite and must not be confused with them: the
deliverer blocks, because the lane holds its own state transition until
central has the record. A record that was merely enqueued would let the lane
release the state the record describes before anything durable held it.

A duplicate dispatch is answered from what this edge already reported rather
than run again — central re-dispatches exactly what it holds no report for,
so what it wants is the report. The memory of an operation is released when
central confirms it, not when the operation finishes: a completed operation is
the most likely thing to be re-dispatched.
