# /// script
# requires-python = "==3.13.*"
# dependencies = []
# ///
"""SMI parser perf-gate.

Runs the benchmark suite and benchstat-compares it against the committed
baseline (testdata/baseline-micro.txt), HARD-FAILING only on a statistically
significant regression in the deterministic metrics allocs/op and B/op.

Why only those two: allocs/op and B/op are a property of the code and
reproduce exactly; wall time on a laptop or a shared runner does not. A gate
that reports a regression because something else was compiling gets muted
within a week, and a muted gate catches nothing. So:

    allocs/op, B/op   -> hard gate (significant increase => exit 1)
    sec/op            -> advisory; GATE_NS=1 makes it hard (local only)

The verdict comes from benchstat's own significance test, and then from an
effect-size floor on top of it. Significance alone is not enough here:
allocated bytes are near-deterministic but not exactly so, since slab and
append growth depend on how many iterations the harness chose, and a metric
that repeats to seven digits gives benchstat p<0.001 on a difference of
eight bytes in ten megabytes. The first full run of this gate against its
own freshly captured baseline failed on seven such rows, every one of them
"+0.00%". So a hard failure needs both a significant verdict and a delta of
at least MIN_DELTA percent.

Throughput (B/s) is excluded from the verdict entirely. It is the inverse of
sec/op, so a "+" there is an improvement, and reporting it as anything else
trains readers to skim the verdict.

This gate never rewrites the baseline. Refreshing it is a reviewed commit:
re-run the suite at COUNT=10, keep the preamble and the benchmark rows,
commit the new testdata/baseline-micro.txt.

Env:
  COUNT      benchmark repetitions (default 10)
  BENCH      -bench pattern (default '.')
  GATE_NS    1 to hard-fail on sec/op too (default 0)
  MIN_DELTA  smallest regression, in percent, worth failing on (default 1).
             Below it a significant verdict is reported and not acted on.
  BASELINE   baseline file (default testdata/baseline-micro.txt)
  RAW_IN     skip running the benchmarks and read this raw `go test` output
             instead. The gate's own tests use it to exercise the comparison
             without a multi-minute run.
"""

import csv
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

KEPT_LINE = re.compile(r"^(Benchmark|goos|goarch|pkg|cpu)")
HARD_METRICS = ("allocs/op", "B/op")


def eprint(*lines: str) -> None:
    print(*lines, sep="\n", file=sys.stderr)


def verdict(csv_text: str, gate_ns: str, min_delta: float, min_delta_text: str) -> int:
    """Print the regression lines of benchstat's CSV and return the exit status."""
    fail = False
    metric = ""
    for row in csv.reader(csv_text.splitlines()):
        if not row:
            continue
        # Metric block header, e.g.  ,allocs/op,CI,allocs/op,CI,vs base,P
        if row[0] == "" and len(row) > 2 and row[2] == "CI":
            metric = row[1]
            continue
        # Throughput is the inverse of sec/op; a "+" there is an improvement.
        if metric == "B/s":
            continue
        # Data row: name,baseVal,baseCI,newVal,newCI,vsBase,P
        if row[0] == "" or row[0] == "geomean" or len(row) < 6:
            continue
        delta = row[5]
        if not delta.startswith("+"):  # significant regressions only
            continue
        pct = float(delta.split("%", 1)[0])

        hard = metric in HARD_METRICS or (metric == "sec/op" and gate_ns == "1")
        if hard and pct >= min_delta:
            print(f"  REGRESSION {metric:<10} {row[0]:<34} {row[1]} -> {row[3]} ({delta})")
            fail = True
        elif hard:
            print(f"  under {min_delta_text}% {metric:<10} {row[0]:<34} {delta} (not acted on)")
        else:
            print(f"  advisory   {metric:<10} {row[0]:<34} {delta} (not gated)")

    if fail:
        print("  perf-gate: FAIL — deterministic (allocs/op or B/op) regression above")
        return 1
    print(f"  perf-gate: PASS — no allocs/op or B/op regression above {min_delta_text}% vs baseline")
    return 0


def main() -> int:
    sys.stdout.reconfigure(encoding="utf-8")
    sys.stderr.reconfigure(encoding="utf-8")

    count = os.environ.get("COUNT", "10")
    bench = os.environ.get("BENCH", ".")
    gate_ns = os.environ.get("GATE_NS", "0")
    min_delta_text = os.environ.get("MIN_DELTA", "1")
    baseline = os.environ.get("BASELINE", "testdata/baseline-micro.txt")
    raw_in = os.environ.get("RAW_IN", "")

    try:
        min_delta = float(min_delta_text)
    except ValueError:
        eprint(f"perf-gate: MIN_DELTA is not a number: {min_delta_text}")
        return 2

    if not Path(baseline).is_file():
        eprint(f"perf-gate: missing baseline {baseline}")
        return 2
    if shutil.which("benchstat") is None:
        eprint("perf-gate: benchstat not on PATH — go install golang.org/x/perf/cmd/benchstat@latest")
        return 2

    with tempfile.TemporaryDirectory(prefix="smi-bench-") as tmp:
        if raw_in:
            print(f"perf-gate: reading benchmark output from {raw_in}")
            raw = Path(raw_in).read_text(encoding="utf-8")
        else:
            print(f"perf-gate: running benchmarks (BENCH={bench} COUNT={count})…", flush=True)
            run = subprocess.run(
                ["go", "test", "-bench", bench, "-benchmem", "-run", "^$", f"-count={count}"],
                stdout=subprocess.PIPE,
                text=True,
                encoding="utf-8",
            )
            if run.returncode != 0:
                return run.returncode
            raw = run.stdout

        # Keep the benchstat preamble and the benchmark rows; drop the test
        # harness's PASS/ok trailer, which benchstat has no use for.
        kept = [line for line in raw.split("\n") if KEPT_LINE.match(line)]
        new = Path(tmp) / "new.txt"
        new.write_text("".join(line + "\n" for line in kept), encoding="utf-8")

        # Self-test. The SNMP gate this one is modeled on additionally
        # filters its rows down to one arm of a two-arm comparison; there is
        # only one arm here, and a filter that matched nothing would leave
        # benchstat a file of preamble and a gate that passes while comparing
        # nothing. So assert the filtered file actually holds rows before
        # believing any verdict from it.
        rows = sum(1 for line in kept if line.startswith("Benchmark"))
        if rows < 1:
            eprint(
                "perf-gate: FAIL — the filtered benchmark output holds no benchmark rows,",
                "  so there is nothing to compare and a PASS would mean nothing.",
                "  Check that the run produced output and that the row filter still matches it.",
            )
            return 2
        print(f"perf-gate: {rows} benchmark rows to compare")

        print()
        print("perf-gate: benchstat baseline vs new —", flush=True)
        subprocess.run(["benchstat", baseline, str(new)])

        print()
        print("perf-gate: verdict —", flush=True)
        table = subprocess.run(
            ["benchstat", "-format", "csv", baseline, str(new)],
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            encoding="utf-8",
        )
        return verdict(table.stdout, gate_ns, min_delta, min_delta_text)


if __name__ == "__main__":
    sys.exit(main())
