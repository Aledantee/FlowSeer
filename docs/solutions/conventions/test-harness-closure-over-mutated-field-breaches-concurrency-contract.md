---
title: A Test Harness Closure Over a Mutated Field Breaches the Concurrency Contract It Feeds
date: 2026-09-25
last_verified: 2026-09-25
category: conventions
module: src/services/device
problem_type: convention
component: testing_framework
severity: medium
applies_when:
  - "Passing a closure or callback function (such as a clock, token generator, or counter) to a constructor or service documented safe for concurrent use"
  - "Mutating harness or test-fixture state mid-test to simulate time advancement, credential expiry, or configuration updates"
  - "Reviewing a test double or mock harness whose -race check passes even though harness fields are mutated during execution"
related_components: [service_layer, testing_framework]
tags: [concurrency, test-harness, race-condition, clock-mock, atomic-sync]
---

# A Test Harness Closure Over a Mutated Field Breaches the Concurrency Contract It Feeds

## The situation

In `src/services/device/internal/captureapi`, `Store` and `OperatorService`
document that each is safe for concurrent use. During style conformance,
`NewStore` was refactored to accept a clock parameter (`clock func() time.Time`)
at initialization, eliminating an unsynchronized `SetClock` method.

In `operator_service_test.go`, the test harness stored `frozenClock time.Time`
and passed `func() time.Time { return h.frozenClock }` to `NewStore` and
`NewOperatorService`. To test session expiry, tests advanced time by assigning
`h.frozenClock = expired` mid-test.

## What is true, and why

When a type documents that it is safe for concurrent use, all callbacks and
seams provided to it must honor that contract. An uncoordinated write to a plain
struct field in the test goroutine races with concurrent reads performed by
background goroutines or HTTP handlers invoking the closure.

`go test -race` detects only races that manifest during actual execution.
When test steps execute sequentially between HTTP requests, the write and read
do not coincide in time during the test run. The test suite and `-race` stay
green, hiding the data race. The defect is caught only by comparing the
concurrency contract of the type against the implementation of the harness.

## How to apply it

Synchronize any harness state mutated across test steps. For timestamps, store
nanoseconds in `atomic.Int64` and provide explicit accessors:

```go
type operatorTestHarness struct {
	store       *captureapi.Store
	frozenNanos atomic.Int64
}

func (h *operatorTestHarness) now() time.Time {
	return time.Unix(0, h.frozenNanos.Load()).UTC()
}

func (h *operatorTestHarness) setNow(t time.Time) {
	h.frozenNanos.Store(t.UnixNano())
}

func newOperatorTestHarness(t *testing.T) *operatorTestHarness {
	h := &operatorTestHarness{}
	h.setNow(time.Date(2026, 9, 18, 14, 0, 0, 0, time.UTC))
	h.store = newTestStoreWithClock(t, h.now)
	return h
}
```

Tests advance time by calling `h.setNow(expired)` instead of assigning to a
plain struct field.

## The evidence

In `src/services/device/internal/captureapi/store.go:61`, `Store` documents its
contract:

```go
// Store retains capture sessions, artifacts, and in-flight uploads. It
// is safe for concurrent use.
type Store struct {
```

`NewStore` takes the clock closure at `store.go:76`:

```go
func NewStore(kv jetstream.KeyValue, payloadRoot string, clock func() time.Time) (*Store, error)
```

In `src/services/device/internal/captureapi/operator_service_test.go:34,41-47`,
the harness coordinates the clock atomically:

```go
	frozenNanos atomic.Int64
}

// now reads the harness clock. The store and the operator service both read it
// through this accessor, and setNow writes it, so a test moving the clock while
// a handler goroutine reads it stays race-free — the concurrency contract
// NewStore's clock parameter now states.
func (h *operatorTestHarness) now() time.Time {
	return time.Unix(0, h.frozenNanos.Load()).UTC()
}

func (h *operatorTestHarness) setNow(t time.Time) {
	h.frozenNanos.Store(t.UnixNano())
}
```

Tests advance the clock via `setNow` at `operator_service_test.go:775-776`:

```go
	expired := h.now().Add(2 * time.Hour)
	h.setNow(expired)
```

## What it does not cover

- Test doubles for types that are not safe for concurrent use.
- Clocks fixed at initialization that tests never mutate.
- Production time providers in `src/common`, which manage internal synchronization.
