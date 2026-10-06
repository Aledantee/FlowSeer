package integration

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// captureCommand is the command under test.
var captureCommand = []string{"uv", "run", "scripts/capture-snmprec.py"}

// missingTargetExit is the status the command gives when --target is
// absent.
var missingTargetExit = 2

// snmpwalkFixture is what the stand-in snmpwalk prints: one line per
// type the converter maps, an untyped value, a value whose character
// after the colon is not a space, and a continuation line that has no
// " = " at all.
const snmpwalkFixture = `.1.3.6.1.2.1.1.5.0 = STRING: "sw1"
.1.3.6.1.2.1.2.1.0 = INTEGER: 3
.1.3.6.1.2.1.1.3.0 = Timeticks: (12345) 0:02:03.45
.1.3.6.1.2.1.31.1.1.1.6.1 = Counter64: 9
.1.3.6.1.2.1.1.2.0 = OID: .1.3.6.1.4.1.9
.1.3.6.1.2.1.1.4.0 = admin
.1.3.6.1.2.1.1.9.0 = X:yz
0A 0B
`

// snmprecFixture is the converter's output for snmpwalkFixture.
const snmprecFixture = `1.3.6.1.2.1.1.5.0|4|sw1
1.3.6.1.2.1.2.1.0|2|3
1.3.6.1.2.1.1.3.0|67|12345
1.3.6.1.2.1.31.1.1.1.6.1|70|9
1.3.6.1.2.1.1.2.0|6|.1.3.6.1.4.1.9
1.3.6.1.2.1.1.4.0|4|admin
1.3.6.1.2.1.1.9.0|4|z
0A 0B|4|
`

// captureRun is one invocation of the capture script.
type captureRun struct {
	stdout string
	stderr string
	exit   int
	args   string // the arguments the stand-in snmpwalk received
}

// runCapture runs the capture script with a stand-in snmpwalk first on
// PATH that prints snmpwalkFixture and records its arguments.
func runCapture(t *testing.T, args ...string) captureRun {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the stand-in snmpwalk is a POSIX sh file")
	}

	bin := t.TempDir()
	fixture := filepath.Join(bin, "fixture.txt")
	argLog := filepath.Join(bin, "args.txt")
	if err := os.WriteFile(fixture, []byte(snmpwalkFixture), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	standIn := "#!/bin/sh\necho \"$*\" >'" + argLog + "'\ncat '" + fixture + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "snmpwalk"), []byte(standIn), 0o700); err != nil {
		t.Fatalf("writing the stand-in snmpwalk: %v", err)
	}

	cmd := exec.Command(captureCommand[0], append(captureCommand[1:], args...)...)
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	run := captureRun{stdout: stdout.String(), stderr: stderr.String()}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		run.exit = exit.ExitCode()
	default:
		t.Fatalf("running the capture script: %v\n%s", err, run.stderr)
	}

	if logged, err := os.ReadFile(argLog); err == nil {
		run.args = strings.TrimSpace(string(logged))
	}

	return run
}

// TestCaptureConvertsSnmpwalkLines pins the conversion of each shape of
// snmpwalk line, including the two that look wrong: the character after
// the colon is skipped whatever it is, and a line with no " = " becomes
// a type 4 row with an empty value.
func TestCaptureConvertsSnmpwalkLines(t *testing.T) {
	run := runCapture(t, "--target", "192.0.2.1")
	if run.exit != 0 {
		t.Fatalf("capture exited %d:\n%s", run.exit, run.stderr)
	}
	if run.stdout != snmprecFixture {
		t.Errorf("capture wrote:\n%s\nwant:\n%s", run.stdout, snmprecFixture)
	}
	const wantArgs = "-v2c -c public -OnQU -Cc 192.0.2.1 .1"
	if run.args != wantArgs {
		t.Errorf("snmpwalk got %q, want %q", run.args, wantArgs)
	}
}

// TestCaptureWritesTheOutputFile covers --output, and that --community
// and --root reach snmpwalk.
func TestCaptureWritesTheOutputFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "device.snmprec")

	run := runCapture(t, "--target", "192.0.2.1", "--community", "lab", "--root", ".1.3.6", "--output", out)
	if run.exit != 0 {
		t.Fatalf("capture exited %d:\n%s", run.exit, run.stderr)
	}
	if run.stdout != "" {
		t.Errorf("capture wrote to standard output with --output set:\n%s", run.stdout)
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading the output file: %v", err)
	}
	if string(got) != snmprecFixture {
		t.Errorf("output file holds:\n%s\nwant:\n%s", got, snmprecFixture)
	}
	const wantArgs = "-v2c -c lab -OnQU -Cc 192.0.2.1 .1.3.6"
	if run.args != wantArgs {
		t.Errorf("snmpwalk got %q, want %q", run.args, wantArgs)
	}
}

// TestCaptureRequiresATarget covers a run that names no device.
func TestCaptureRequiresATarget(t *testing.T) {
	run := runCapture(t)
	if run.exit != missingTargetExit {
		t.Errorf("capture exited %d without --target, want %d:\n%s", run.exit, missingTargetExit, run.stderr)
	}
	if !strings.Contains(run.stderr, "--target") {
		t.Errorf("capture did not name --target on standard error:\n%s", run.stderr)
	}
	if run.args != "" {
		t.Errorf("snmpwalk ran without a target: %q", run.args)
	}
}
