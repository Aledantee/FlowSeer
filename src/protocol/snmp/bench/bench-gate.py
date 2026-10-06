# /// script
# requires-python = "==3.13.*"
# dependencies = []
# ///
"""Local SNMP performance gate.

Runs the FlowSeer-arm micro benchmarks and benchstat-compares them against
the committed baseline (testdata/baseline-micro.txt), HARD-FAILING only on a
statistically significant regression in the deterministic metrics allocs/op
and B/op.

Why only those two: on the committed baseline allocs/op and B/op have +-0%
variance, while ns/op (ColdStart especially, +-680% from the re-dial guard)
and the throughput/GC/RSS metrics are noisy. Gating noisy metrics on shared
hardware produces false regressions that erode trust in the gate, so:

    allocs/op, B/op   -> HARD gate (significant increase => exit 1)
    sec/op (ns/op)    -> advisory by default; GATE_NS=1 makes it hard too
                         (local only, never on shared/CI hardware)
    throughput/GC/RSS -> not measured here; they live in the build-tagged
                         fan-out (bench:fanout) and GC (bench:gc) harnesses

The gate keys off benchstat's own significance verdict (the "vs base"
column), so high-variance benchmarks like ColdStart read as "~" and do not
false-trip, while a deterministic +1 allocation reads as a significant "+%".

Refreshing the baseline is a deliberate, reviewed action: re-run
    task bench:micro COUNT=10
keep the FlowSeer arm, and commit the new testdata/baseline-micro.txt. This
gate never rewrites the baseline, so a regression cannot silently rebaseline.

Run via `task bench:gate` (which sets the working dir to src/protocol/snmp/bench).
Env: COUNT (default 10), BENCH (default '.'), GATE_NS (default 0).
"""

import csv
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

BASELINE = "testdata/baseline-micro.txt"
KEPT_LINE = re.compile(r"impl=flowseer|^(goos|goarch|pkg|cpu)")
HARD_METRICS = ("allocs/op", "B/op")


def eprint(*lines: str) -> None:
    print(*lines, sep="\n", file=sys.stderr)


def verdict(csv_text: str, gate_ns: str) -> int:
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
        # Data row: name,baseVal,baseCI,newVal,newCI,vsBase,P
        if row[0] == "" or row[0] == "geomean" or len(row) < 6:
            continue
        delta = row[5]
        if not delta.startswith("+"):  # significant regression only
            continue
        if metric in HARD_METRICS or (metric == "sec/op" and gate_ns == "1"):
            print(f"  REGRESSION {metric:<10} {row[0]:<28} {row[1]} -> {row[3]} ({delta})")
            fail = True
        else:
            print(f"  advisory   {metric:<10} {row[0]:<28} {delta} (not gated)")

    if fail:
        print("  perf-gate: FAIL — deterministic (allocs/op or B/op) regression above")
        return 1
    print("  perf-gate: PASS — no significant allocs/op or B/op regression vs baseline")
    return 0


def main() -> int:
    sys.stdout.reconfigure(encoding="utf-8")
    sys.stderr.reconfigure(encoding="utf-8")

    count = os.environ.get("COUNT", "10")
    bench = os.environ.get("BENCH", ".")
    gate_ns = os.environ.get("GATE_NS", "0")

    if not Path(BASELINE).is_file():
        eprint(
            f"perf-gate: missing baseline {BASELINE} (run task bench:micro COUNT=10, "
            f"keep the FlowSeer rows and preamble, and commit them as {BASELINE})"
        )
        return 2
    if shutil.which("benchstat") is None:
        eprint("perf-gate: benchstat not on PATH — go install golang.org/x/perf/cmd/benchstat@latest")
        return 2

    print(f"perf-gate: running micro benchmarks (BENCH={bench} COUNT={count})…", flush=True)
    run = subprocess.run(
        ["go", "test", "-bench", bench, "-benchmem", "-run", "^$", f"-count={count}"],
        stdout=subprocess.PIPE,
        text=True,
        encoding="utf-8",
    )
    if run.returncode != 0:
        return run.returncode

    # Keep only the FlowSeer arm + the benchstat preamble, matching the baseline.
    kept = [line for line in run.stdout.split("\n") if KEPT_LINE.search(line)]
    if not kept:
        eprint("perf-gate: FAIL — the benchmark output holds no FlowSeer rows and no preamble")
        return 1

    with tempfile.TemporaryDirectory(prefix="snmp-bench-") as tmp:
        new = Path(tmp) / "new.txt"
        new.write_text("".join(line + "\n" for line in kept), encoding="utf-8")

        print()
        print("perf-gate: benchstat baseline vs new —", flush=True)
        subprocess.run(["benchstat", BASELINE, str(new)])

        print()
        print("perf-gate: verdict —", flush=True)
        table = subprocess.run(
            ["benchstat", "-format", "csv", BASELINE, str(new)],
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            encoding="utf-8",
        )
        return verdict(table.stdout, gate_ns)


if __name__ == "__main__":
    sys.exit(main())
