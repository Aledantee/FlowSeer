---
title: Holding a Secondary File Store to a Swept Record Requires In-Memory Ownership
date: 2026-09-18
last_verified: 2026-09-18
category: architecture-patterns
module: src/services/device/internal/captureapi
problem_type: architecture_pattern
component: packet_capture
severity: high
applies_when:
  - "Pairing a metadata record store with a secondary filesystem payload store whose files are pruned by walking metadata records rather than the filesystem"
  - "Writing an invariant test across dual stores, where an in-flight operation writes payload bytes before metadata finalization"
  - "Multiple review rounds each patch an individual discrepancy between record state and disk state, and you want to hold the invariant across operation sequences"
related_components: [device, messaging]
tags: [dual-store, invariant, sequence-enumeration, retention-sweep, orphan-files, in-flight-ownership, packet-capture]
---

# Holding a secondary file store to a swept record requires in-memory ownership

## The situation

The central capture leg pairs JetStream KeyValue session metadata with binary
pcapng files on local disk under `<StateDir>/captures/`. Retention sweeping
walks session records to prune files when records expire; it does not crawl
the filesystem. Any payload file on disk that lacks a corresponding metadata
record holding an artifact descriptor sits outside retention and never expires.

Initial review passes attempted to resolve discrepancies between record state
and disk state one case at a time. Each round fixed the failure in front of
it: refusing to open an existing artifact file (`O_EXCL`), unlinking partial
files when a writer is abandoned, and adding tombstones so a deleted session
cannot be recreated by in-flight chunks. Each fix opened or left another
sequence, because every operation that touches either half can disagree on
ordering.

## Why exempting in-flight state blinds the invariant

To break the case-by-case cycle, an invariant test stated the property: a
pcapng file exists exactly while a session record claims it. Walking every
permutation of operations over that property immediately surfaced another
defect: an append arriving after retention purged a session recreated the
file on disk despite the record marking the payload purged.

The invariant itself, however, contained a blind spot. Because an active
upload creates a file before finalization records its descriptor, the property
checker exempted any file whose record had no artifact descriptor. That
exemption excused the exact state in which orphan files live.

When an operator cancels an in-flight session, the record transitions to
canceled while the file remains on disk. When `FinalizeArtifact` encounters a
disk error, or when the subsequent JetStream write fails, the payload file
exists without an artifact descriptor. Because retention sweeps walk records,
those bytes are never purged. A black-box invariant that compares only the
persistent record store and the disk directory cannot distinguish an active
upload from an abandoned orphan. It walks hundreds of sequences, passes green,
and hides orphan leaks.

## The pattern: in-memory ownership and asymmetric sequences

Holding a secondary file store to a swept metadata record requires three
disciplines:

1. **Tie in-flight files to volatile writer ownership.** A file on disk
   without an artifact descriptor in its record is valid only while an active
   in-memory writer owns it. The invariant check must be package-internal to
   inspect the store's writer registry directly.
2. **Include asymmetric partial failures in the sequence alphabet.**
   Operations where one store changes without the other must be generated
   alongside clean lifecycle transitions. The alphabet must include operator
   cancellation (record moves without file) and lost record updates (file
   finalizes on disk but record write fails).
3. **Discard files on every non-terminal error.** `AbandonWriter` unlinks
   partial files when a client stream terminates early. `FinalizeArtifact`
   removes the writer before finishing its work, becoming the sole owner, and
   unlinks the file on any subsequent failure. Callers must invoke
   `DiscardArtifact` whenever a post-finalize record mutation fails. Handlers
   must drop the writer before releasing session claim locks to prevent
   concurrent retries from appending to a file about to be unlinked.

## Evidence

- Invariant check requiring active writer ownership:
  `src/services/device/internal/captureapi/artifact_invariant_internal_test.go:217-224`.
- Sequence walk exercising asymmetric steps (`cancel`, `finalize_record_lost`):
  `src/services/device/internal/captureapi/artifact_invariant_internal_test.go:103-120`.
- Record permission check preventing recreation after purge:
  `src/services/device/internal/captureapi/store.go:316-335`.
- Unlink on finalize failure:
  `src/services/device/internal/captureapi/store.go:439-444`.
- Handler discard on post-finalize record write failure:
  `src/services/device/internal/captureapi/edge_service.go:542-547`.
- Dropping writer before releasing session upload claim:
  `src/services/device/internal/captureapi/edge_service.go:387-394`.
- The invariant holds across 729 generated sequences:
  `src/services/device/internal/captureapi/artifact_invariant_internal_test.go:48-188`,
  verified on 2026-09-18.

## What this does not cover

This pattern does not guard against process crashes before `fsync`.
Ungraceful host termination relies on JetStream and filesystem fsync policies
followed by startup reconciliation. It also does not address filesystem
monitoring or disk quotas. It is distinct from
[a contract that must never reach a refusal](../conventions/a-degrade-contract-is-tested-against-the-refusing-side.md),
which governs input translation boundaries rather than synchronized multi-store
state.
