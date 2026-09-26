---
title: An Error Wrapper Must Not Reuse the Rich Error Type It Wraps
date: 2026-09-26
category: architecture-patterns
module: src/common/errs
problem_type: architecture_pattern
component: service_layer
severity: high
applies_when:
  - "implementing or refactoring error wrapping functions such as Wrap or Wrapf"
  - "migrating call sites from fmt.Errorf(\"%w\", err) to an owned error package"
  - "extracting typed errors across wrapped chains using errors.As"
related_components:
  - api_layer
  - observability
tags:
  - error-handling
  - errs
  - golang
  - errors-as
  - wrap
---

# An error wrapper must not reuse the rich error type it wraps

## The situation

During the migration of 575 call sites from `fmt.Errorf("%w", err)` to
`errs.Wrap(err, "...")`, `Wrap` was initially implemented as
`From(err).build(msg)`, returning `*errs.Error`. Standard `fmt.Errorf("%w")`
returns a minimal unexported struct (`fmt.wrapError`) that defines only `Error()`
and `Unwrap() error`. Reusing `*errs.Error` for simple context wrapping broke
`errors.As` extraction across call chains.

In Go, `errors.As(err, &target)` traverses an error chain from outside to inside,
stopping at the first value assignable to the target. When `Wrap` produces
`*errs.Error`, `errors.As(err, &target)` targeting `*errs.Error` binds the
outermost context wrapper rather than the causal error that originated the
failure.

At `src/services/device/internal/host/interceptor.go:103-118`, `internalCause`
relies on this contract:

```go
// src/services/device/internal/host/interceptor.go:108-116
// Reaching past it with [errors.As] gets the innermost failure that
// carries the chain, and its LogValue renders the whole tree.
func internalCause(err error) any {
	var internal *errs.Error
	if errors.As(err, &internal) {
		return internal
	}
	return err
}
```

When wrappers were `*errs.Error`, `internalCause` returned the wrapper rather
than the causal failure. Similarly, telemetry SDK lifecycle tests in
`src/common/service/telemetry_sdk_test.go:281-284` and `:626-629` asserting
`errors.As(err, &first) && first == cause` failed because `first` bound the
wrapper returned by `errs.Wrap(err, "create telemetry resource")`.

## The rule

A context wrapper like `errs.Wrap` or `errs.Wrapf` must return a distinct,
unexported wrapper type that defines only `Error()` and `Unwrap()`. It must not
define `As` or `Is`, and must not be an instance of the package's rich error
type:

```go
// src/common/errs/wrap.go:8-16
type wrapError struct {
	msg   string
	err   error
	stack stack
}

func (w *wrapError) Error() string { ... }
func (w *wrapError) Unwrap() error { return w.err }
```

Because `wrapError` defines no `As` method, `errors.As(wrapped, &target)` falls
through transparently until it encounters the innermost `*errs.Error`.

This matches prior art in Go error libraries: `fmt.wrapError`,
`github.com/pkg/errors` (`withMessage`, `withStack`), `cockroachdb/errors`
(`withPrefix`), and `go-faster/errors` (`wrapError`) all separate the wrapper from
the rich error type. In contrast, `samber/oops` (`OopsError`) and `juju/errors`
(`*Err`) reuse their rich error structs for wrapping and carry this bug.

## Stacks and chain walkers

A naive wrapper defining only `Error()` and `Unwrap()` drops origin stacks. In
`errs`, `Wrap` must record program counters when a foreign, stackless error enters
the chain (`src/common/errs/wrap.go:66-70`).

Because `wrapError` is not an `*errs.Error`, traversal functions cannot rely on
type assertions to `*Error` alone. All chain walkers—`walk`
(`src/common/errs/attr.go:103-107`), `anyStack` (`src/common/errs/stack.go:73-80`),
`stacks` (`src/common/errs/stack.go:94-98`), and `(*wrapError).LogValue`
(`src/common/errs/wrap.go:39-61`)—must recognize `*wrapError` so its stack remains
reachable.

## Evidence

- Invariant test: `src/common/errs/wrap_test.go:55-71`
  (`TestWrapAsFallsThroughToInnermostError`) proves `errors.As` through multiple
  wraps binds the causal `*Error`.
- Stack preservation test: `src/common/errs/wrap_test.go:73-92`
  (`TestWrapForeignStacklessErrorCapturesOriginStack`) verifies origin stack
  capture for foreign errors.
- Telemetry SDK assertions: `src/common/service/telemetry_sdk_test.go:281-284` and
  `:626-629` pin innermost error extraction.
- Documentation contract: `src/common/errs/doc.go:36-40`.

## What it does not cover

Sentinels (`errs.Msg`) and structured errors built via `errs.New` or `errs.From`.
Those explicitly construct `*errs.Error` because they attach codes, attributes,
user messages, or retry dispositions.
