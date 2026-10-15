---
title: A synctest test body joins the goroutine it started on a timer before it returns, or a regression aborts the package with a bubble panic
date: 2026-10-15
last_verified: 2026-10-15
category: conventions
module: src/edge/netpen/attacks/ip6
problem_type: convention
component: testing_framework
severity: medium
applies_when:
  - "Writing a test inside `synctest.Test` that starts a goroutine which sleeps on the fake clock before it pushes a frame, closes a channel, or fires an event the code under test waits for"
  - "A test in a synctest bubble fails with `deadlock: main bubble goroutine has exited but blocked goroutines remain` and a goroutine dump instead of its own assertion lines"
  - "Running a mutation against a synctest test and the whole package reports a panic rather than one failed test"
related_components: [netpen, concurrency]
tags: [testing, synctest, concurrency, netpen, mutation-testing]
---

# A synctest test body joins the goroutine it started on a timer before it returns

## The situation

`TestDADDOS_LateDefenseIsRead` (`src/edge/netpen/attacks/ip6/ip6_test.go:483-500`)
runs `RunDADDOS` inside a `synctest.Test` bubble. Before the call it starts a
goroutine that sleeps one second of fake time and then pushes the defending
advertisement onto the leg, and it asserts that the behavior returned at that
second. On the committed tree the goroutine has fired by the time the body
reaches its assertions.

Make the behavior return before its send and the two assertions at
`ip6_test.go:498` and `:500` print as expected, then the body returns while the
goroutine is still asleep. `go test -run TestDADDOS_LateDefenseIsRead ./attacks/ip6/`
(go1.27.2, darwin/arm64, 2026-10-15) ends with

```text
panic: deadlock: main bubble goroutine has exited but blocked goroutines remain
goroutine 7 [sleep (durable), synctest bubble 1]:
time.Sleep(0x3b9aca00)
    .../ip6_test.go:487
```

A panic ends the test binary, so every test after it in the package does not
run, and the failure a reader sees is a runtime dump rather than the assertion
that names the regression.

## What is true and why

The fake clock stops when the bubble's main goroutine exits, and a goroutine
still blocked at that point is a deadlock by the runtime's rule:

```go
// runtime/synctest.go (go1.27.2)
226    if bubble.done {
227        // Time stops once the bubble's main goroutine has exited.
228        break
229    }
...
241    if total != 1 {
242        var reason string
243        if bubble.done {
244            reason = "deadlock: main bubble goroutine has exited but blocked goroutines remain"
```

A sleeping goroutine is durably blocked, so `synctest.Wait()` before returning
does not help: it returns at once without advancing the clock.

The body must therefore block on the goroutine's completion before it returns.
Blocking the main goroutine durably lets the clock run the sleep out, the
goroutine pushes (`PushRXFrame` never blocks, `attacks/testtest/leg.go:83-86`)
and exits, and the body's own failure lines are the whole output.

## How to apply it

Have the goroutine signal when it is done and join it in a `defer`, which also
runs after `t.Fatalf`:

```go
synctest.Test(t, func(t *testing.T) {
	leg := fixtureLeg(t, nil)
	fired := make(chan struct{})
	go func() {
		defer close(fired)
		time.Sleep(time.Second)
		leg.PushRX(defense)
	}()
	defer func() { <-fired }()

	start := time.Now()
	if _, err := ip6.RunDADDOS(context.Background(), deps); err != nil {
		t.Fatalf("RunDADDOS() error = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed != time.Second {
		t.Errorf("returned after %v, want 1s", elapsed)
	}
})
```

Check it the way the regression would be found: make the behavior return
early and read a plain `--- FAIL` with the assertion lines, no panic.

## Evidence

- `src/edge/netpen/attacks/ip6/ip6_test.go:486-489`: the goroutine sleeps,
  then pushes, with nothing joining it.
- `runtime/synctest.go:226-229,241-246` (go1.27.2): the clock stops on the
  main goroutine's exit and the remaining goroutine becomes the panic.
- `docs/code-style.md:478-484`: a test waits for the condition it needs, and
  a sleep inside a bubble is deterministic. This solution adds the join the
  rule leaves implicit.

## What it does not cover

A goroutine outside the bubble, such as the record collector in `runOne`
(`ip6_test.go:78-84`), which the runner's own shutdown joins. A goroutine
blocked on a channel nothing will ever serve, which panics with the other
reason text (`all goroutines in bubble are blocked`) while the body is still
running and points at a missing sender, not a missing join.
