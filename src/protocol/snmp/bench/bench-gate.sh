#!/bin/sh
# Local SNMP perf-gate (plan 003, U5). Runs the FlowSeer-arm micro benchmarks
# and benchstat-compares them against the committed baseline
# (testdata/baseline-micro.txt), HARD-FAILING only on a statistically
# significant regression in the deterministic metrics allocs/op and B/op.
#
# Why only those two: on the committed baseline allocs/op and B/op have ±0%
# variance, while ns/op (ColdStart especially, ±680% from the re-dial guard)
# and the throughput/GC/RSS metrics are noisy. Gating noisy metrics on shared
# hardware produces false regressions that erode trust in the gate, so:
#
#   - allocs/op, B/op   → HARD gate (significant increase ⇒ exit 1)
#   - sec/op (ns/op)    → advisory by default; GATE_NS=1 makes it hard too
#                         (local only — never on shared/CI hardware)
#   - throughput/GC/RSS → not measured here; they live in the build-tagged
#                         fan-out (bench:fanout) and GC (bench:gc) harnesses
#
# The gate keys off benchstat's own significance verdict (the "vs base"
# column), so high-variance benchmarks like ColdStart read as "~" and do not
# false-trip, while a deterministic +1 allocation reads as a significant "+%".
#
# Refreshing the baseline is a deliberate, reviewed action — re-run
#   task bench:micro COUNT=10
# keep the FlowSeer arm, and commit the new testdata/baseline-micro.txt. This
# gate never rewrites the baseline, so a regression cannot silently rebaseline.
#
# Run via `task bench:gate` (which sets the working dir to common/snmp/bench).
# Env: COUNT (default 10), BENCH (default '.'), GATE_NS (default 0).

set -eu

COUNT="${COUNT:-10}"
BENCH="${BENCH:-.}"
GATE_NS="${GATE_NS:-0}"
BASELINE="testdata/baseline-micro.txt"

if [ ! -f "$BASELINE" ]; then
	echo "perf-gate: missing baseline $BASELINE (capture it per U1)" >&2
	exit 2
fi
if ! command -v benchstat >/dev/null 2>&1; then
	echo "perf-gate: benchstat not on PATH — go install golang.org/x/perf/cmd/benchstat@latest" >&2
	exit 2
fi

RAW="$(mktemp -t snmp-bench-raw.XXXXXX)"
NEW="$(mktemp -t snmp-bench-new.XXXXXX)"
trap 'rm -f "$RAW" "$NEW"' EXIT

echo "perf-gate: running micro benchmarks (BENCH=$BENCH COUNT=$COUNT)…"
go test -bench "$BENCH" -benchmem -run '^$' -count="$COUNT" >"$RAW"
# Keep only the FlowSeer arm + the benchstat preamble, matching the baseline.
grep -E 'impl=flowseer|^(goos|goarch|pkg|cpu)' "$RAW" >"$NEW"

echo
echo "perf-gate: benchstat baseline vs new —"
benchstat "$BASELINE" "$NEW" || true

echo
echo "perf-gate: verdict —"
benchstat -format csv "$BASELINE" "$NEW" 2>/dev/null | awk -v gate_ns="$GATE_NS" '
	BEGIN { FS = ","; fail = 0; metric = "" }
	# Metric block header, e.g.  ,allocs/op,CI,allocs/op,CI,vs base,P
	$1 == "" && $3 == "CI" { metric = $2; next }
	# Data row: name,baseVal,baseCI,newVal,newCI,vsBase,P
	$1 != "" && $1 != "geomean" && NF >= 6 {
		delta = $6
		if (delta ~ /^\+/) { # significant regression only
			if (metric == "allocs/op" || metric == "B/op" || (metric == "sec/op" && gate_ns == "1")) {
				printf "  REGRESSION %-10s %-28s %s -> %s (%s)\n", metric, $1, $2, $4, delta
				fail = 1
			} else {
				printf "  advisory   %-10s %-28s %s (not gated)\n", metric, $1, delta
			}
		}
	}
	END {
		if (fail) { print "  perf-gate: FAIL — deterministic (allocs/op or B/op) regression above"; exit 1 }
		print "  perf-gate: PASS — no significant allocs/op or B/op regression vs baseline"
	}
'
