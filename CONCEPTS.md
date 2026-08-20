# Concepts

Shared domain vocabulary for this project — entities, named processes, and status
concepts with project-specific meaning. Seeded with core domain vocabulary, then
accretes as ce-compound and ce-compound-refresh process learnings; direct edits
are fine. Glossary only, not a spec or catch-all.

Standard SNMP and networking vocabulary (OID, varbind, PDU, MIB, community,
walk, trap) is assumed and not redefined here. What follows is what FlowSeer
means by its own terms.

## Relationships

A Table is observed by exactly one of the Collection Primitives at a time. A
Column belongs to one table and carries exactly one Tier; a Change Indicator is
a Column (or set of Columns) chosen for that role. Adaptive Watch is the process
a Watcher runs; Tier is the input that decides what each tick fetches. A Quirk is
recorded as one row of the Conformance Corpus and carries exactly one status.

## Collection primitives

The three first-class streaming shapes. All share one lifecycle contract: the
constructor starts the producer, iteration is the idiomatic consumer surface,
Close is idempotent and terminates the producer within one request round-trip,
and a terminal error latches so it can be read once after the loop ends. New
primitives are expected to extend this shape rather than add one-shot request
helpers.

### Walker
A bounded traversal that streams the rows or values of one subtree or table until
the subtree is exhausted. Finite by nature — a Walker completes.

### Watcher
A long-lived observer of one (target, table) pair that emits a typed event
whenever a row is added, modified, or removed. Unbounded by nature — a Watcher
runs until closed. Distinct from a Walker in that it holds a snapshot of the
observed rows between ticks and reports differences, rather than reporting
everything it sees.

### Trap Stream
A stream of traps and notifications received from targets rather than solicited
from them. Backpressure is drop-oldest with a monotonic dropped count: under
overload the stream loses the oldest undelivered traps rather than blocking the
receiver or growing without bound. Source filtering and rate limiting on it are
advisory, not guarantees.

## Adaptive Watch

### Adaptive Watch
The process a Watcher runs to keep a table view current without re-reading the
whole table every tick: probe a cheap Change Indicator, and fetch the columns
that an advance implies, at a cadence that widens while nothing changes and
narrows when something does.

The first cycle is a Cold Start (below). Thereafter each tick reads the Change
Indicator; if it has not advanced, no state column is re-fetched. Counter and
Static columns keep their own cadences independent of indicator state. Row
removal is not observable from an indicator advance, so a full traversal is also
forced on a long interval.

### Change Indicator
The column (or per-row set of columns) a Watcher probes as its cheap
"did anything change?" signal, in place of reading every column. Two shapes: a
per-row indicator, where each row carries its own signal and fetches are targeted
at the rows that moved; and a table-scalar indicator, where one value covers the
whole table and an advance triggers a wholesale re-read.

An indicator advance means something *may* have changed, not that it did — a
Watcher that finds the row unchanged after an advance emits nothing.

### Tier
The per-column classification that decides *when* Adaptive Watch fetches that
column. Four tiers, by what makes the value move:

- **Counter** — monotonically accumulating; fetched on its own cadence
  regardless of indicator state, often enough that the underlying counter cannot
  wrap unobserved between samples.
- **Indicator** — advances when something else in the row or table changes;
  used as the Change Indicator probe.
- **State** — changes when the indicator advances; fetched on advance, skipped
  on quiet ticks.
- **Static** — effectively never changes (descriptions, serial numbers);
  fetched on a much longer cadence.

*Avoid:* column tier, tier map.

Tiers are assigned at code-generation time from the column's declared type and
name, with one exception: Static is never assigned automatically. It is always a
deliberate caller override, because misclassifying a changing column as Static
loses changes silently.

### Cold Start
A Watcher's first cycle, which walks the whole table and reports every row it
finds as added. It establishes the baseline snapshot; there is nothing to diff
against yet, so nothing is reported as modified or removed.

### Fallback
A Watcher's degraded-but-operational state, entered when the Change Indicator
becomes untrustworthy — for example when the agent returns an exception in place
of a value, or an indicator sits at zero while a counter in the same row is
visibly moving. In Fallback the Watcher stops trusting the cheap probe and reads
more broadly.

Fallback is a degradation, not a failure: the stream keeps running and the
terminal error stays unset. It is the operator-visible contract for "the library
is still working, but paying more to do it," and so is reported separately from
both events and transient per-tick errors.

## Typed surface

### Column
A typed, OID-anchored accessor for one column of one table, carrying the wire
type it expects, how to decode it, its Tier, and its Wire Key. Columns are the
unit that generated MIB bindings expose and that Adaptive Watch reasons about —
a table is described as its set of Columns.

### Wire Key
An OID's on-the-wire encoded octets treated as an opaque immutable key, used
instead of the human-readable dotted form for lookups on the hot path. Because
the encoding is canonical, byte order of Wire Keys matches numeric OID order and
a byte prefix matches an OID prefix, so ordering and containment questions can be
answered without decoding. A Column computes its Wire Key once, when it is
constructed.

### Guarded Fast Path
An optimization that first checks the exact shape it requires and, when the
input does not match, *declines* — reporting not-applicable so the general path
handles the case. A decline is never an error: the caller must not be able to
tell a declined fast path from one that was never there, apart from cost.

Every fast path in the library owes a machine-checked proof that it agrees with
the path it bypasses — byte-for-byte equality of output where the output is
bytes, or a differential test driving both paths through the same inputs. A fast
path that accepts a shape it should have declined is as much a defect as one that
produces wrong output.

### Fused Decoder
A generated per-column decode step that reads a value straight from the
undecoded response octets, skipping the intermediate typed representation. A
Fused Decoder is a Guarded Fast Path: it declines on any tag other than the one
the MIB declares, on an out-of-range value, and on input that was already
decoded — and the generic decoder then produces exactly the values and errors it
would have produced alone. This is what lets the library be fast on
well-behaved agents and tolerant of the rest, with no separate strictness mode.

## Conformance and gates

### Quirk
A specific way real SNMP agents deviate from the specification that the library
must tolerate rather than reject — a column served with a type other than the one
its MIB declares, an OID encoded validly but not minimally, a v3 exchange that
skips a step. Quirks are treated as facts about the installed base, not as bugs
in the devices.

### Conformance Corpus
The manifest of known Quirks, one row each, and the single source of truth for
what the library claims to tolerate. A row records the specification clause at
issue, where the quirk was observed in the wild, what the library's behavior is,
the adversarial input that exercises it, and a status.

Rows are append-only: a row is never deleted, because deleting one erases the
evidence that the quirk exists. Provenance is a real report against a real
stack, not a hypothesis — the corpus is a transcription of other implementations'
hard-won lessons, so a row without a citation carries no weight. The rendered
coverage map is generated from the corpus and never hand-edited.

### Accepted Risk
The Conformance Corpus status for a Quirk the library deliberately does *not*
handle, because handling it would require guessing between two indistinguishable
interpretations. It is the one status that requires a written rationale and a
second, separate acknowledgement — precisely so it cannot become the quiet
default for anything inconvenient. Distinct from a covered quirk (handled and
pinned by a test) and from a pending one (known, not yet resolved either way).

### Integration Tier
One of the numbered levels of the integration test suite. Each tier is a distinct
*kind* of environment rather than a rung on a fidelity ladder: a containerized
reference agent, which earns its place by producing edge cases real hardware will
not produce on demand; a virtualized vendor network OS, for realistic end-to-end
collection and trap reception; replay of committed captures, which is where
per-vendor regression coverage accretes; and an operator-supplied live device,
which is opt-in and skips when no target is configured.

Every tier talks over the wire — no tier constructs a fake session, which is what
separates an Integration Tier from an ordinary test. Each owns its own environment
lifecycle and is selected explicitly; none run as part of the default test
command, and selecting two at once is a deliberate compile error rather than a
silent merge of two environments.

### Perf Gate
The check that compares a benchmark run against a committed baseline and fails
on regression. Deliberately narrow: only metrics that are deterministic on shared
hardware can fail it, while timing and throughput are advisory, because a gate
that false-trips gets disabled and then protects nothing. It never rewrites its
own baseline — accepting new numbers is a reviewed, deliberate change.

## Errors

### Error Code
An error's stable identity across process boundaries: a namespaced
`<package>/<name>` string that two errors can share so they match as the same
failure even when they hold no pointer identity in common — which is how a
decoded error from a peer matches the local sentinel it stands for.

Codes are a wire contract, so they are append-only: never renamed, never reused
for a different meaning. They are globally unique across the project, and a
duplicate declaration fails loudly at startup rather than surfacing later as a
false match. Because uniqueness must hold across packages that are never linked
into the same binary, it is enforced by a repo-wide source scan and not only by
the runtime registry.

### Public Attribute
An attribute explicitly marked as safe to show an untrusted caller, as opposed to
an ordinary attribute, which is internal. Safety is declared where the value is
attached — by the code that knows what the value is — never inferred at the
boundary that emits it.

The public set is a strict subset of the full set: when the same key is carried
both internally and publicly, the internal value claims the key and the key is
then simply absent from the public view, rather than falling through to the
public value. Logs are a trusted surface and receive internal attributes; the
distinction exists for the client boundary. Raw secret material is never an
attribute at all — the length and the protocol name are, the key or salt is not.

### Retry Disposition
An error's answer to "is this worth trying again?", read by the mechanisms that
must decide — a poll loop's backoff, a broker's redelivery, an RPC boundary
choosing a status code — so that none of them has to pattern-match on codes.

Three states, not two: retryable, fatal, and *no view expressed*. The third is
what lets a wrapper that has exhausted its budget declare a transient cause fatal
while an undecided wrapper defers to its cause. The outermost error that
expressed a view decides. Silence means not retryable — retrying is the claim
that needs making, since a caller that retries a permanent failure loops forever.

### User Message
The client-facing message an error carries, sent in place of the internal one at
a boundary facing untrusted callers. The internal message is written for the log
and may name hosts, engine IDs, and call paths; the two are deliberately separate
so neither is compromised trying to serve both audiences. An accompanying Hint
carries the remedy under the same client-safe rule.

Both resolve to the outermost value in the chain, because the level closest to
the caller knows what that caller was trying to do.

## Flagged ambiguities

- "Backend" and "driver" had been used for the SNMP wire implementation — there
  is no backend or driver abstraction. The wire implementation is internal to the
  collection library and reached only through its session and trap-listener entry
  points. Prose still describing a pluggable backend is stale.
- "Tier" refers only to a Column's Adaptive Watch classification. The numbered
  levels of the integration suite are Integration Tiers, and the two are
  unrelated.
