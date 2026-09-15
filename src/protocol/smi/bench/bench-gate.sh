#!/bin/sh
# SMI parser perf-gate. Runs the benchmark suite and benchstat-compares
# it against the committed baseline (testdata/baseline-micro.txt),
# HARD-FAILING only on a statistically significant regression in the
# deterministic metrics allocs/op and B/op.
#
# Why only those two: allocs/op and B/op are a property of the code and
# reproduce exactly; wall time on a laptop or a shared runner does not.
# A gate that reports a regression because something else was compiling
# gets muted within a week, and a muted gate catches nothing. So:
#
#   allocs/op, B/op   -> hard gate (significant increase => exit 1)
#   sec/op            -> advisory; GATE_NS=1 makes it hard (local only)
#
# The verdict comes from benchstat's own significance test, and then from
# an effect-size floor on top of it. Significance alone is not enough
# here: allocated bytes are near-deterministic but not exactly so — slab
# and append growth depend on how many iterations the harness chose — and
# a metric that repeats to seven digits gives benchstat p<0.001 on a
# difference of eight bytes in ten megabytes. The first full run of this
# gate against its own freshly captured baseline failed on seven such
# rows, every one of them "+0.00%". So a hard failure needs both a
# significant verdict and a delta of at least MIN_DELTA percent.
#
# Throughput (B/s) is excluded from the verdict entirely. It is the
# inverse of sec/op, so a "+" there is an improvement, and reporting it
# as anything else trains readers to skim the verdict.
#
# This gate never rewrites the baseline. Refreshing it is a reviewed
# commit: re-run the suite at COUNT=10, keep the preamble and the
# benchmark rows, commit the new testdata/baseline-micro.txt.
#
# Env:
#   COUNT     benchmark repetitions (default 10)
#   BENCH     -bench pattern (default '.')
#   GATE_NS   1 to hard-fail on sec/op too (default 0)
#   MIN_DELTA smallest regression, in percent, worth failing on
#             (default 1). Below it a significant verdict is reported and
#             not acted on.
#   BASELINE  baseline file (default testdata/baseline-micro.txt)
#   RAW_IN    skip running the benchmarks and read this raw `go test`
#             output instead. The gate's own tests use it to exercise
#             the comparison without a multi-minute run.

set -eu

# The verdict below converts benchstat's delta string to a number in awk, whose
# string-to-number conversion honours LC_NUMERIC. Under a comma-decimal locale
# "+0.10%" converts to 0 while "+50.00%" converts to 50, so every sub-1% delta
# collapses to zero and MIN_DELTA below 1 can never fire. Pin the numeric locale
# so the gate compares the same way wherever it runs.
LC_ALL=C
export LC_ALL

COUNT="${COUNT:-10}"
BENCH="${BENCH:-.}"
GATE_NS="${GATE_NS:-0}"
MIN_DELTA="${MIN_DELTA:-1}"
BASELINE="${BASELINE:-testdata/baseline-micro.txt}"
RAW_IN="${RAW_IN:-}"

if [ ! -f "$BASELINE" ]; then
	echo "perf-gate: missing baseline $BASELINE" >&2
	exit 2
fi
if ! command -v benchstat >/dev/null 2>&1; then
	echo "perf-gate: benchstat not on PATH — go install golang.org/x/perf/cmd/benchstat@latest" >&2
	exit 2
fi

RAW="$(mktemp -t smi-bench-raw.XXXXXX)"
NEW="$(mktemp -t smi-bench-new.XXXXXX)"
trap 'rm -f "$RAW" "$NEW"' EXIT

if [ -n "$RAW_IN" ]; then
	echo "perf-gate: reading benchmark output from $RAW_IN"
	cat "$RAW_IN" >"$RAW"
else
	echo "perf-gate: running benchmarks (BENCH=$BENCH COUNT=$COUNT)…"
	go test -bench "$BENCH" -benchmem -run '^$' -count="$COUNT" >"$RAW"
fi

# Keep the benchstat preamble and the benchmark rows; drop the test
# harness's PASS/ok trailer, which benchstat has no use for.
grep -E '^(Benchmark|goos|goarch|pkg|cpu)' "$RAW" >"$NEW" || true

# Self-test. The SNMP gate this one is modeled on additionally filters
# its rows down to one arm of a two-arm comparison; there is only one arm
# here, and a filter that matched nothing would leave benchstat a file of
# preamble and a gate that passes while comparing nothing. So assert the
# filtered file actually holds rows before believing any verdict from it.
ROWS="$(grep -c '^Benchmark' "$NEW" || true)"
if [ "$ROWS" -lt 1 ]; then
	echo "perf-gate: FAIL — the filtered benchmark output holds no benchmark rows," >&2
	echo "  so there is nothing to compare and a PASS would mean nothing." >&2
	echo "  Check that the run produced output and that the row filter still matches it." >&2
	exit 2
fi
echo "perf-gate: $ROWS benchmark rows to compare"

echo
echo "perf-gate: benchstat baseline vs new —"
benchstat "$BASELINE" "$NEW" || true

echo
echo "perf-gate: verdict —"
benchstat -format csv "$BASELINE" "$NEW" 2>/dev/null |
	awk -v gate_ns="$GATE_NS" -v min_delta="$MIN_DELTA" '
	BEGIN { FS = ","; fail = 0; metric = "" }
	# Metric block header, e.g.  ,allocs/op,CI,allocs/op,CI,vs base,P
	$1 == "" && $3 == "CI" { metric = $2; next }
	# Throughput is the inverse of sec/op; a "+" there is an improvement.
	metric == "B/s" { next }
	# Data row: name,baseVal,baseCI,newVal,newCI,vsBase,P
	$1 != "" && $1 != "geomean" && NF >= 6 {
		delta = $6
		if (delta !~ /^\+/) { next } # significant regressions only
		pct = delta + 0

		hard = (metric == "allocs/op" || metric == "B/op" || (metric == "sec/op" && gate_ns == "1"))
		if (hard && pct >= min_delta) {
			printf "  REGRESSION %-10s %-34s %s -> %s (%s)\n", metric, $1, $2, $4, delta
			fail = 1
		} else if (hard) {
			printf "  under %s%% %-10s %-34s %s (not acted on)\n", min_delta, metric, $1, delta
		} else {
			printf "  advisory   %-10s %-34s %s (not gated)\n", metric, $1, delta
		}
	}
	END {
		if (fail) { print "  perf-gate: FAIL — deterministic (allocs/op or B/op) regression above"; exit 1 }
		printf "  perf-gate: PASS — no allocs/op or B/op regression above %s%% vs baseline\n", min_delta
	}
'
