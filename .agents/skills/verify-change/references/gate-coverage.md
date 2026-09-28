# What the gate covers beyond the changed packages

These entries complete the closed list in `SKILL.md`.

- `src/common/errs` and `test/conformance/proto` run on every targeted run
  of the root module, since they hold repository-wide invariants (error-code
  uniqueness, schema layering) no changed package's tests can see.
- Packages are vetted once per build tag their files carry, so a tagged
  integration or bench test that stopped compiling fails the gate.
- A nested module that replaces the root module (`src/protocol/*/bench`,
  `src/edge/netpen`, `generated/go/yang`) is built and vetted after a change
  to a root package it depends on (test imports and build-tagged files
  included) or to the root `go.mod` or `go.sum`; its race tests run under
  `--full`. A module under `generated/` is built only. A changed file inside
  it builds that module and lints its sample packages, since a full lint of
  the YANG bindings runs for over an hour.
- A `frontend/web/` path selects the web workspace's typecheck, Vite build,
  ESLint, Stylelint, Prettier check, and Vitest suite, using the local
  binaries installed from its lockfile.
- `buf breaking` compares only the changed `.proto` files `main` already
  holds, and prints that it skipped when every changed schema file is new on
  the branch, since `--path` naming a file the baseline lacks fails without
  saying anything about the change. The whole-module form under `--full`
  still covers a deletion.
- Full runs and telemetry-sensitive paths (service Go sources, its Collector
  integration sources and fixture, the wrapper, root `go.mod` or `go.sum`)
  run the Docker-backed OpenTelemetry tier through
  `tools/test/service-otel-integration.sh`. An unavailable daemon is a
  failed gate.
