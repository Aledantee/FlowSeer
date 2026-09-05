---
title: Error Wire Design - Direction
type: direction
date: 2026-09-04
topic: error-wire-design
status: accepted-direction
---

# Error Wire Design - Direction

`src/common/errs` is the error type FlowSeer's processes share, and its errors
will cross process boundaries — Connect RPC, brokers. The package's core API
already carries everything that transport needs; the transport itself is not
implemented, and lands with its first consumer. This record holds the design it
must follow, so the first implementation does not have to rediscover it.

## Payload

The proto message carries the code, the message, the client-safe attributes,
the user message, the hint, the retry disposition, and the cause chain. An
optional stack field is populated only on trusted internal transit (service to
service, broker) and is always absent on a message headed toward a client. The
exit code is not carried at all — it describes the process that failed, not the
failure.

## Decoding is total

A cause whose type the decoder does not recognize degrades to a generic opaque
leaf that preserves its message, code, and safe attributes rather than being
dropped — the chain's shape survives even when its types do not. A code the
local binary does not know surfaces as an internal error, never as a silent
unknown that some `errors.Is` might match by accident.

## What a client may see

Message text and the cause chain are trusted-internal content: they name
internal hosts, engine IDs, and call paths. A boundary facing untrusted clients
exposes only the code, the client-safe attributes, and the user message and
hint — the sanitized text an author already wrote for exactly this, falling
back to a generic string when the chain carries none.

The retry disposition crosses too, because it is what maps an error onto a
retryable RPC status; like every peer-supplied field it is a hint about the
peer, never an input to a local authorization decision.

## Trust

Decoded errors are accepted only from authenticated, integrity-protected peers.
A peer-supplied code or attribute is diagnostic input and never drives an
authorization decision.
