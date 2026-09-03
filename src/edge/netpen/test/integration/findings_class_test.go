package integration

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
)

// parseFindingsClass requires a complete JSONL run so an empty or truncated
// stream cannot count as a reproduced attack. Timestamps and counts vary across
// runs; the comparison uses the set of record kinds and finding modules.
func parseFindingsClass(out string) (string, error) {
	seen := map[string]bool{}
	var records []findings.Record
	for i, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec findings.Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return "", fmt.Errorf("JSONL line %d: %w", i+1, err)
		}
		if rec.SchemaVersion != findings.SchemaVersion || rec.Kind == "" {
			return "", fmt.Errorf("JSONL line %d: missing kind or invalid schema version", i+1)
		}
		module := ""
		if rec.Kind == findings.KindFinding {
			if rec.Finding == nil || rec.Finding.Module == "" {
				return "", fmt.Errorf("JSONL line %d: finding has no module", i+1)
			}
			module = rec.Finding.Module
		}
		records = append(records, rec)
		seen[string(rec.Kind)+":"+module] = true
	}
	if len(records) < 3 || records[0].Kind != findings.KindMeta || records[0].Meta == nil ||
		records[len(records)-1].Kind != findings.KindSummary || records[len(records)-1].Summary == nil {
		return "", fmt.Errorf("want meta header, attack records, and closing summary")
	}
	for _, rec := range records[1 : len(records)-1] {
		if rec.Kind == findings.KindMeta || rec.Kind == findings.KindSummary {
			return "", fmt.Errorf("unexpected %s inside the run", rec.Kind)
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return strings.Join(keys, ","), nil
}

func TestParseFindingsClass(t *testing.T) {
	const header = `{"schema_version":1,"kind":"meta","meta":{}}` + "\n"
	const summary = `{"schema_version":1,"kind":"summary","summary":{}}`
	const finding = `{"schema_version":1,"kind":"finding","finding":{"module":"ospf","detail":{}}}` + "\n"
	cases := []struct {
		name string
		out  string
		want string
	}{
		{"valid", header + finding + summary, "finding:ospf,meta:,summary:"},
		{"duplicates", header + finding + finding + summary, "finding:ospf,meta:,summary:"},
		{"JSON whitespace", header + `{ "schema_version": 1, "kind": "finding", "finding": {"module": "ospf"}}` + "\n" + summary, "finding:ospf,meta:,summary:"},
		{"nested detail", header + `{"schema_version":1,"kind":"finding","finding":{"detail":{"module":"decoy"},"module":"ospf"}}` + "\n" + summary, "finding:ospf,meta:,summary:"},
		{"no findings", header + `{"schema_version":1,"kind":"resisted","rollup":{"verdict":"resisted"}}` + "\n" + summary, "meta:,resisted:,summary:"},
		{"empty", "", ""},
		{"no attack records", header + summary, ""},
		{"missing summary", header + finding, ""},
		{"missing header", finding + summary, ""},
		{"malformed JSON", header + `{"kind":"finding","finding":{"module":"ospf"}` + "\n" + summary, ""},
		{"diagnostic on stdout", header + finding + "warning\n" + summary, ""},
		{"missing module", header + `{"schema_version":1,"kind":"finding"}` + "\n" + summary, ""},
		{"wrong schema", header + `{"schema_version":2,"kind":"progress"}` + "\n" + summary, ""},
		{"duplicate header", header + header + finding + summary, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseFindingsClass(tc.out)
			if (err != nil) != (tc.want == "") || got != tc.want {
				t.Errorf("got (%q, %v), want class %q (error if empty)", got, err, tc.want)
			}
		})
	}
}
