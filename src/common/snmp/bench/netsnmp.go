//go:build snmp_bench_macro

package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"sort"

	"go.aledante.io/ae"
)

// netsnmp.go drives the Net-SNMP C tools as the third comparand in the
// macro tier. Net-SNMP runs as a subprocess, so it cannot be timed inside
// a Go testing.B loop without the fork+exec cost dwarfing the actual SNMP
// work. Instead it is measured with hyperfine, which amortizes process
// startup across many runs and reports its own warm statistics. The
// numbers are wall-clock-per-invocation and must be read as such — never
// compared head-to-head with the in-process ns/op figures.

// hasBin reports whether an executable is on PATH.
func hasBin(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// hyperfineStats is the slice of a hyperfine JSON export this suite reports.
type hyperfineStats struct {
	Command  string
	MeanMs   float64
	MedianMs float64
	P95Ms    float64
	MinMs    float64
	Runs     int
}

// hyperfine JSON schema (subset).
type hyperfineExport struct {
	Results []struct {
		Command string    `json:"command"`
		Mean    float64   `json:"mean"`   // seconds
		Median  float64   `json:"median"` // seconds
		Min     float64   `json:"min"`    // seconds
		Times   []float64 `json:"times"`  // seconds, per run
	} `json:"results"`
}

// runHyperfine times a single shell command with hyperfine and returns the
// warm statistics. warmup runs are discarded by hyperfine before timing. The
// context bounds the whole hyperfine invocation so a stalled subprocess
// (e.g. snmpbulkwalk waiting on an unreachable agent) cannot hang the test.
func runHyperfine(ctx context.Context, command string, warmup, runs int) (hyperfineStats, error) {
	f, err := os.CreateTemp("", "hyperfine-*.json")
	if err != nil {
		return hyperfineStats{}, err
	}
	jsonPath := f.Name()
	_ = f.Close()
	defer os.Remove(jsonPath)

	args := []string{
		"--warmup", fmt.Sprint(warmup),
		"--runs", fmt.Sprint(runs),
		"--export-json", jsonPath,
		"--style", "none",
		command,
	}
	cmd := exec.CommandContext(ctx, "hyperfine", args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return hyperfineStats{}, ae.Wrap("hyperfine", err)
	}

	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		return hyperfineStats{}, err
	}
	var exp hyperfineExport
	if err := json.Unmarshal(raw, &exp); err != nil {
		return hyperfineStats{}, err
	}
	if len(exp.Results) == 0 {
		return hyperfineStats{}, ae.Msg("hyperfine produced no results")
	}
	r := exp.Results[0]
	return hyperfineStats{
		Command:  r.Command,
		MeanMs:   r.Mean * 1000,
		MedianMs: r.Median * 1000,
		P95Ms:    percentile(r.Times, 0.95) * 1000,
		MinMs:    r.Min * 1000,
		Runs:     len(r.Times),
	}, nil
}

// percentile returns the p-quantile (0..1) of xs using nearest-rank.
func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	rank := int(math.Ceil(p*float64(len(s)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(s) {
		rank = len(s) - 1
	}
	return s[rank]
}

// snmpbulkwalkCmd builds a net-snmp snmpbulkwalk v2c command string for
// hyperfine to run. agent is host:port; root is a dotted OID. The -t/-r
// flags bound net-snmp's own per-invocation wait so a wrong agent address
// fails fast instead of stalling every hyperfine run on the default 5×1s
// retry budget.
func snmpbulkwalkCmd(agent, community, root string) string {
	return fmt.Sprintf("snmpbulkwalk -v 2c -c %s -t 1 -r 1 -On -Cr50 %s %s", community, agent, root)
}

// snmpgetCmd builds a net-snmp snmpget v2c command string.
func snmpgetCmd(agent, community, oid string) string {
	return fmt.Sprintf("snmpget -v 2c -c %s -t 1 -r 1 -On %s %s", community, agent, oid)
}
