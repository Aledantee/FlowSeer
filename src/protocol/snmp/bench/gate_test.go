package bench

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// The gate is a Python script that starts `go test -bench`, which these
// tests cannot afford. Each one puts a stand-in `go` first on PATH that
// prints a prepared transcript, so the filter, the benchstat comparison,
// the metric policy, and the exit code run in milliseconds against
// transcripts whose verdict is known.
//
// The `go test` argument list is the one thing the stand-in ignores, so
// nothing here covers it.

// gateCommand is the command under test.
var gateCommand = []string{"uv", "run", "bench-gate.py"}

// gateRun is one invocation of the gate.
type gateRun struct {
	output string
	exit   int
}

// runGate runs the gate with a stand-in `go` that prints transcript, and
// returns what the gate said and how it exited.
func runGate(t *testing.T, transcript string, env ...string) gateRun {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the stand-in go is a POSIX sh file")
	}

	bin := t.TempDir()
	raw := filepath.Join(bin, "transcript.txt")
	if err := os.WriteFile(raw, []byte(transcript), 0o600); err != nil {
		t.Fatalf("writing transcript: %v", err)
	}
	standIn := "#!/bin/sh\ncat '" + raw + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(standIn), 0o700); err != nil {
		t.Fatalf("writing the stand-in go: %v", err)
	}

	cmd := exec.Command(gateCommand[0], gateCommand[1:]...)
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Env = append(cmd.Env, env...)

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()

	run := gateRun{output: buf.String()}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		run.exit = exit.ExitCode()
	default:
		t.Fatalf("running the gate: %v\n%s", err, run.output)
	}

	return run
}

// baselineTranscript returns the committed baseline, which doubles as a
// benchmark transcript: comparing it against itself is the no-change
// case.
func baselineTranscript(t *testing.T) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", "baseline-micro.txt"))
	if err != nil {
		t.Fatalf("reading the committed baseline: %v", err)
	}

	return string(raw)
}

// regressBenchmark returns the transcript with one metric of one
// benchmark multiplied, which is how a regression is injected without
// touching the gate.
//
// A benchmark row is "name-P<TAB>iters<TAB>value unit<TAB>value unit…",
// so the value to scale is the field before the named unit.
func regressBenchmark(t *testing.T, transcript, benchmark, unit string, factor float64) string {
	t.Helper()

	hit := false
	lines := strings.Split(transcript, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, benchmark) {
			continue
		}

		fields := strings.Split(line, "\t")
		for j, f := range fields {
			parts := strings.Fields(f)
			if len(parts) != 2 || parts[1] != unit {
				continue
			}
			v, err := strconv.ParseFloat(parts[0], 64)
			if err != nil {
				t.Fatalf("parsing %q as a %s value: %v", parts[0], unit, err)
			}
			fields[j] = strconv.FormatFloat(v*factor, 'f', -1, 64) + " " + unit
			hit = true
		}
		lines[i] = strings.Join(fields, "\t")
	}

	if !hit {
		t.Fatalf("no %s field on any %s row; the transcript format changed", unit, benchmark)
	}

	return strings.Join(lines, "\n")
}

// gatedBenchmark is the row the regression tests perturb.
const gatedBenchmark = "BenchmarkGet/impl=flowseer"

// TestGatePassesAgainstBaseline is the case that has to hold for the
// gate to be worth running: unchanged code does not trip it.
func TestGatePassesAgainstBaseline(t *testing.T) {
	run := runGate(t, baselineTranscript(t))
	if run.exit != 0 {
		t.Errorf("gate exited %d against its own baseline:\n%s", run.exit, run.output)
	}
	if !strings.Contains(run.output, "perf-gate: PASS") {
		t.Errorf("gate did not report PASS:\n%s", run.output)
	}
}

// TestGateFailsOnBytesRegression covers the second hard metric: bytes
// allocated per operation, which can grow while the allocation count holds.
func TestGateFailsOnBytesRegression(t *testing.T) {
	bad := regressBenchmark(t, baselineTranscript(t), gatedBenchmark, "B/op", 1.5)

	run := runGate(t, bad)
	if run.exit != 1 {
		t.Errorf("gate exited %d on a +50%% B/op regression, want 1:\n%s", run.exit, run.output)
	}
	if !strings.Contains(run.output, "REGRESSION") {
		t.Errorf("gate did not report a regression:\n%s", run.output)
	}
}

// TestGateFailsOnAllocationRegression is the case the gate exists for.
func TestGateFailsOnAllocationRegression(t *testing.T) {
	bad := regressBenchmark(t, baselineTranscript(t), gatedBenchmark, "allocs/op", 1.5)

	run := runGate(t, bad)
	if run.exit != 1 {
		t.Errorf("gate exited %d on a +50%% allocation regression, want 1:\n%s", run.exit, run.output)
	}
	if !strings.Contains(run.output, "REGRESSION") {
		t.Errorf("gate did not report a regression:\n%s", run.output)
	}
	if !strings.Contains(run.output, strings.TrimPrefix(gatedBenchmark, "Benchmark")) {
		t.Errorf("gate did not name %s:\n%s", gatedBenchmark, run.output)
	}
}

// TestGateTreatsWallTimeAsAdvisory is the false-trip guard. Wall time on
// shared hardware moves for reasons that have nothing to do with the
// code, and a gate that fails on those gets turned off.
func TestGateTreatsWallTimeAsAdvisory(t *testing.T) {
	slow := regressBenchmark(t, baselineTranscript(t), gatedBenchmark, "ns/op", 2)

	run := runGate(t, slow)
	if run.exit != 0 {
		t.Errorf("gate exited %d on a wall-time-only regression, want 0:\n%s", run.exit, run.output)
	}
	if !strings.Contains(run.output, "advisory") {
		t.Errorf("gate did not report the slowdown as advisory:\n%s", run.output)
	}

	opted := runGate(t, slow, "GATE_NS=1")
	if opted.exit != 1 {
		t.Errorf("gate exited %d on the same input with GATE_NS=1, want 1:\n%s", opted.exit, opted.output)
	}
}

// TestGateFailsOnAnEmptyRun covers a `go test` run that prints nothing
// the row filter keeps. The gate exits 1 instead of passing a comparison
// of nothing.
func TestGateFailsOnAnEmptyRun(t *testing.T) {
	run := runGate(t, "")
	if run.exit != 1 {
		t.Errorf("gate exited %d on an empty run, want 1:\n%s", run.exit, run.output)
	}
}

// TestGateTakesTheDefaultForAnEmptyVariable pins that a variable set to
// the empty string takes its default. The stand-in go ignores BENCH and
// COUNT, so the line the gate prints is where they show.
func TestGateTakesTheDefaultForAnEmptyVariable(t *testing.T) {
	run := runGate(t, baselineTranscript(t), "BENCH=", "COUNT=")
	if run.exit != 0 {
		t.Errorf("gate exited %d with BENCH and COUNT empty, want 0:\n%s", run.exit, run.output)
	}
	if !strings.Contains(run.output, "running micro benchmarks (BENCH=. COUNT=10)") {
		t.Errorf("gate did not fall back to BENCH=. and COUNT=10:\n%s", run.output)
	}
}

// TestGateReadsLinesByLineFeedOnly pins that a carriage return is not a
// line break, only a line feed ends a line. A row that follows one on the
// same line is not a benchmark row to benchstat, so a regression hidden
// behind one is not compared.
func TestGateReadsLinesByLineFeedOnly(t *testing.T) {
	bad := regressBenchmark(t, baselineTranscript(t), gatedBenchmark, "allocs/op", 1.5)
	hidden := strings.ReplaceAll(bad, "\n"+gatedBenchmark, "\njunk\r"+gatedBenchmark)

	run := runGate(t, hidden)
	if run.exit != 0 {
		t.Errorf("gate exited %d on rows that follow a carriage return, want 0:\n%s", run.exit, run.output)
	}
}
