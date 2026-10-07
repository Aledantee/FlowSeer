# What the gate covers beyond the changed packages

These entries complete the closed list in `SKILL.md`.

- `src/common/errs` and every package under `test/conformance/` run once,
  from the root, on every targeted run that selects any Go module, nested
  ones included. They hold repository-wide invariants (error-code
  uniqueness, schema layering and message rules, panic placement, the
  goroutine boundary, forbidden module imports) that no changed package's
  tests can see. A protobuf change also runs `test/conformance/proto`.
- Packages are vetted once per build tag their files carry, so a tagged
  integration or bench test that stopped compiling fails the gate.
- A nested module that replaces the root module (`src/protocol/*/bench`,
  `src/edge/netpen`, `generated/go/yang`) is built and vetted after a change
  to a root package it depends on (test imports and build-tagged files
  included) or to the root `go.mod` or `go.sum`; its race tests run under
  `--full`. A module under `generated/` is built only. A changed file inside
  it builds that module and lints its sample packages, since a full lint of
  the YANG bindings runs for over an hour.
- A `tools/buf/` path selects the pinned protobuf format, lint, breaking, and
  generate gates plus `go -C tools/buf mod verify` and the protobuf
  conformance tests. The format, lint, and breaking gates omit `--path` so
  they check the whole module. It does not select a Go module gate because the
  module contains the Buf CLI tool and no Go package.
- A `frontend/web/` path selects the web workspace's typecheck, Vite build,
  ESLint, Stylelint, Prettier check, and Vitest suite, using the local
  binaries installed from its lockfile. With no Go module selected, it also
  runs `test/conformance/...`, since `test/conformance/a11y` reads the web
  tests and stories.
- A `tools/scripts/` path or `uv.toml` selects the hook tooling gates and
  needs `uv` on `PATH`. `uv run tools/scripts/run.py test` runs the suites
  under `tools/scripts/tests/`, one of which compiles every `.py` file under
  `tools/scripts/`. A run that reports zero tests fails.
- A changed `.md` file runs the relative-link check and
  `uv run tools/scripts/run.py verify check-prose`, so it needs `uv` on `PATH`. Prose that cites an agent run
  fails the gate. Style findings print as a warning count and do not.
- `buf breaking` compares only the changed `.proto` files `main` already
  holds, and prints that it skipped when every changed schema file is new on
  the branch, since `--path` naming a file the baseline lacks fails without
  saying anything about the change. The `tools/buf/` selection and the
  whole-module form under `--full` cover the whole module, including a
  deletion.
- Full runs and telemetry-sensitive paths (service Go sources, its Collector
  integration sources and fixture, the wrapper, root `go.mod` or `go.sum`)
  run the Docker-backed OpenTelemetry tier through
  `tools/test/service-otel-integration.sh`. An unavailable daemon is a
  failed gate.
