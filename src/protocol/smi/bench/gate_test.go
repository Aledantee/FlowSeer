package bench

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The gate is a shell script, and a shell script nobody tests is a shell
// script that silently stops gating. These tests drive bench-gate.sh
// through RAW_IN, which feeds it a prepared `go test` transcript instead
// of running the suite, so the whole comparison — the row filter, the
// self-test, the metric policy, the exit code — is exercised in
// milliseconds against transcripts whose expected verdict is known.
//
// What RAW_IN does not cover is the `go test -bench` invocation itself.
// That is the one line these tests take on trust, and it is the line
// that changes least.

const gateScript = "bench-gate.sh"

// gateRun is one invocation of the gate.
type gateRun struct {
	output string
	exit   int
}

// runGate feeds the gate a prepared transcript and returns what it said
// and how it exited.
func runGate(t *testing.T, transcript string, env ...string) gateRun {
	t.Helper()

	raw := filepath.Join(t.TempDir(), "raw.txt")
	if err := os.WriteFile(raw, []byte(transcript), 0o600); err != nil {
		t.Fatalf("writing transcript: %v", err)
	}

	cmd := exec.Command("sh", gateScript)
	cmd.Env = append(os.Environ(), "RAW_IN="+raw)
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
// touching the parser.
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

// gatedBenchmark is the row the regression tests perturb. It is a
// whole-file parse rather than a micro-benchmark because that is where a
// real allocation regression would land, and because the gate has to
// name it in its output for the failure to be actionable.
const gatedBenchmark = "BenchmarkParseFile/fixture=lancom-oids"

// TestGatePassesAgainstBaseline is the case that has to hold for the
// gate to be worth running: an unchanged parser does not trip it.
func TestGatePassesAgainstBaseline(t *testing.T) {
	run := runGate(t, baselineTranscript(t))
	if run.exit != 0 {
		t.Errorf("gate exited %d against its own baseline:\n%s", run.exit, run.output)
	}
	if !strings.Contains(run.output, "perf-gate: PASS") {
		t.Errorf("gate did not report PASS:\n%s", run.output)
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
	// Naming the benchmark is the difference between a gate that tells
	// you where to look and one that only tells you to look.
	if !strings.Contains(run.output, strings.TrimPrefix(gatedBenchmark, "Benchmark")) {
		t.Errorf("gate did not name %s:\n%s", gatedBenchmark, run.output)
	}
}

// TestGateFailsOnHeapRegression covers the other hard metric, so a
// change that allocates the same number of larger buffers is caught too.
// It perturbs a different benchmark from the allocation case, so the two
// tests together show the gate reporting whichever row moved rather than
// a fixed one.
func TestGateFailsOnHeapRegression(t *testing.T) {
	const benchmark = "BenchmarkRecovery/decls=1k"

	bad := regressBenchmark(t, baselineTranscript(t), benchmark, "B/op", 1.5)

	run := runGate(t, bad)
	if run.exit != 1 {
		t.Errorf("gate exited %d on a +50%% B/op regression, want 1:\n%s", run.exit, run.output)
	}
	if !strings.Contains(run.output, "REGRESSION B/op") {
		t.Errorf("gate did not attribute the failure to B/op:\n%s", run.output)
	}
	if !strings.Contains(run.output, strings.TrimPrefix(benchmark, "Benchmark")) {
		t.Errorf("gate did not name %s:\n%s", benchmark, run.output)
	}
}

// TestGateIgnoresTrivialDeltas is the other false-trip guard, and the
// one that cost a gate run to find.
//
// Allocated bytes are near-deterministic but not exactly so: slab and
// append growth follow the iteration count the harness chose. Over ten
// samples that produces a spread of a few bytes in ten megabytes, which
// benchstat calls significant at p<0.001 and prints as "+0.00%". The
// first full run of this gate against its own freshly captured baseline
// failed on seven such rows. Significance alone is therefore not a
// regression; the delta has to be large enough to mean something.
func TestGateIgnoresTrivialDeltas(t *testing.T) {
	// A tenth of a percent: unambiguously significant, unambiguously
	// not worth anybody's afternoon.
	tiny := regressBenchmark(t, baselineTranscript(t), gatedBenchmark, "allocs/op", 1.001)

	run := runGate(t, tiny)
	if run.exit != 0 {
		t.Errorf("gate exited %d on a +0.1%% allocation delta, want 0:\n%s", run.exit, run.output)
	}
	if !strings.Contains(run.output, "not acted on") {
		t.Errorf("gate did not report the sub-threshold delta it saw:\n%s", run.output)
	}

	// The floor is a threshold, not a mute: lowering it below the delta
	// brings the same row back as a failure.
	strict := runGate(t, tiny, "MIN_DELTA=0.01")
	if strict.exit != 1 {
		t.Errorf("gate exited %d on the same input at MIN_DELTA=0.01, want 1:\n%s", strict.exit, strict.output)
	}
}

// TestGateTreatsWallTimeAsAdvisory is the false-trip guard. Wall time on
// a laptop or a shared runner moves for reasons that have nothing to do
// with the parser, and a gate that fails on those is a gate somebody
// turns off — after which it catches nothing at all.
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

// TestGateDoesNotRewriteBaseline pins the property that keeps the
// baseline meaningful. A gate that refreshed its own reference on
// failure would report a regression once and then accept it forever.
func TestGateDoesNotRewriteBaseline(t *testing.T) {
	path := filepath.Join("testdata", "baseline-micro.txt")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the baseline: %v", err)
	}

	run := runGate(t, regressBenchmark(t, string(before), gatedBenchmark, "allocs/op", 1.5))
	if run.exit != 1 || !strings.Contains(run.output, "REGRESSION") {
		t.Fatalf("the injected regression did not fail the gate, so this proves nothing:\n%s", run.output)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-reading the baseline: %v", err)
	}
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Error("the gate rewrote testdata/baseline-micro.txt on failure")
	}
}

// TestGateSelfTestRejectsEmptyRows covers the mistake this gate was
// forked away from.
//
// The SNMP gate filters its run down to one arm of a two-arm comparison.
// Copied here, where there is one arm, that filter matches nothing and
// hands benchstat a file of preamble — and benchstat compares nothing
// and says nothing, so the gate passes. The self-test is what turns that
// silence into a failure.
func TestGateSelfTestRejectsEmptyRows(t *testing.T) {
	preambleOnly := ""
	for _, line := range strings.Split(baselineTranscript(t), "\n") {
		if !strings.HasPrefix(line, "Benchmark") {
			preambleOnly += line + "\n"
		}
	}
	if strings.Contains(preambleOnly, "Benchmark") {
		t.Fatal("the preamble-only transcript still holds benchmark rows")
	}

	run := runGate(t, preambleOnly)
	if run.exit != 2 {
		t.Errorf("gate exited %d on a transcript with no benchmark rows, want 2:\n%s", run.exit, run.output)
	}
	if !strings.Contains(run.output, "no benchmark rows") {
		t.Errorf("gate did not say why it refused:\n%s", run.output)
	}
}
