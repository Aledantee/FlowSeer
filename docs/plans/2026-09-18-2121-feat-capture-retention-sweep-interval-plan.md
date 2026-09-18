---
title: Configurable Capture Retention Sweep Interval - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
---

# Configurable Capture Retention Sweep Interval - Plan

## Goal

Allow operators and test fixtures to configure the capture artifact retention
sweeper cadence through `ServiceIntervals` in the device service configuration,
replacing the hardcoded one-minute interval. The means is adding `capture_sweep`
to `ServiceIntervals` in `service_config.proto`, parsing it in `host/config.go`,
and wiring it to `setupCaptureSweeper` in `host/host.go` with a fallback to the
one-minute default. Stop condition: This plan is wrong if the capture retention
sweep must share an existing interval rather than carrying its own cadence in
`ServiceIntervals`.

## Decisions

- The capture retention sweep has its own interval field (`capture_sweep`) in
  `ServiceIntervals` rather than sharing `read_sweep`. Why: The review report
  hypothesized that capture sweep borrowed `ReadSweep`. Inspection of
  `src/services/device/internal/host/host.go` reveals that `ReadSweep` (line
  279) is wired exclusively to `dispatchapi` for expiring device mutation read
  requests. Meanwhile, `setupCaptureSweeper` (lines 495-532) hardcodes
  `const defaultCaptureSweepInterval = time.Minute` and reads no configuration
  at all. `read_sweep` is specifically documented in `service_config.proto` as
  the cadence for sweeping expired dispatch reads across journal keys, whereas
  capture sweep walks JetStream session metadata in the `captures` bucket to
  unlink expired pcapng payload files from disk under `<StateDir>/captures/`.
  The two operations have different workloads, different storage backends, and
  different performance characteristics.
- The schema comment for `capture_sweep` explicitly states the default,
  behavior, and purpose. Why: In accordance with `docs/doc-style.md` and
  AIP-192, the comment specifies what unset means (one minute), the minimum
  duration constraint (1 second via protovalidate), and explains that the
  sweeper walks session records to purge on-disk pcapng files past their
  `expires_at` deadline while preserving metadata for audit.
- Unset or zero duration preserves the existing one-minute default. Why:
  Existing configurations and conformance tests continue running with
  `defaultCaptureSweepInterval` without requiring config updates.

## Requirements

1. `spec/proto/flowseer/store/device/v1/service_config.proto` defines
   `google.protobuf.Duration capture_sweep = 8` in `ServiceIntervals`.
   Acceptance: Protobuf validation passes with `capture_sweep` set to 5 seconds;
   setting `capture_sweep` to 0 seconds or negative duration fails protovalidate
   rule `gte = {seconds: 1}`.
2. `src/services/device/internal/host/config.go` exposes `Intervals.CaptureSweep`.
   Acceptance: A `ServiceConfig` protobuf message with
   `intervals.capture_sweep = 15s` yields
   `cfg.Intervals().CaptureSweep == 15 * time.Second`.
3. `setupCaptureSweeper` in `src/services/device/internal/host/host.go` uses the
   configured interval. Acceptance: When `CaptureSweep` is configured to 200ms
   in a test, the sweeper ticks and executes `SweepExpired` on that interval;
   when unset, it ticks every one minute.

## Out of scope

- Changing the artifact expiry deletion mechanism in `captureapi.Store.SweepExpired`.
- Modifying `dispatchapi`'s `read_sweep` interval.
- Filesystem-level crawling or storage quota management.

## Units

### U1. Schema definition for capture_sweep in ServiceIntervals

Files: `spec/proto/flowseer/store/device/v1/service_config.proto`
After: none
Change: Adds `google.protobuf.Duration capture_sweep = 8 [(buf.validate.field).duration.gte = {seconds: 1}];`
to `ServiceIntervals` with documentation of default and retention sweep behavior.
Tests: `test/conformance/proto/layering_test.go`, `buf lint`, and
`buf format -d`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/store/device/v1/service_config.proto`

### U2. Host configuration parsing and sweeper wiring

Files: `src/services/device/internal/host/config.go`,
`src/services/device/internal/host/config_test.go`,
`src/services/device/internal/host/host.go`,
`src/services/device/test/integration/capture_test.go`
After: U1
Change: `Intervals` struct gains `CaptureSweep time.Duration`, `c.Intervals()`
parses it from `msg.GetIntervals().GetCaptureSweep()`, and `setupCaptureSweeper`
initializes its ticker using `h.cfg.Intervals().CaptureSweep` (or
`defaultCaptureSweepInterval` if non-positive).
Tests: `config_test.go` tests parsing with set, unset, and zero values;
`capture_test.go` integration test tests that a configured sweep interval
executes retention purge on schedule.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/host src/services/device/test/integration/capture_test.go`

Waves: U1 | U2

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/store/device/v1/service_config.proto src/services/device/internal/host src/services/device/test/integration/capture_test.go
go test -race ./src/services/device/...
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `spec/proto/flowseer/store/device/v1/service_config.proto` updated with `capture_sweep`.
- [ ] `src/services/device/internal/host/config.go` and `host.go` wired to `capture_sweep`.
- [ ] Unit and integration tests green.
- [ ] No plan labels in code.

## Open questions

None.
