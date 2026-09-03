package syslog_test

import (
	"encoding/json"
	"os"
	"testing"
)

type fixture struct {
	ID            string         `json:"id"`
	Family        string         `json:"family"`
	Provenance    string         `json:"provenance"`
	Source        string         `json:"source"`
	Configuration string         `json:"configuration"`
	Payload       string         `json:"payload"`
	Format        string         `json:"format"`
	Module        string         `json:"module"`
	EventID       string         `json:"event_id"`
	Mnemonic      string         `json:"mnemonic"`
	Expected      *fixtureRecord `json:"expected"`
}

type fixtureRecord struct {
	Hostname    string   `json:"hostname"`
	Timestamp   string   `json:"timestamp"`
	Content     string   `json:"content"`
	Year        int      `json:"year"`
	Zone        string   `json:"zone"`
	Status      string   `json:"status"`
	Diagnostics []string `json:"diagnostics"`
}

func corpus(t testing.TB) []fixture {
	t.Helper()
	b, err := os.ReadFile("testdata/corpus/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []fixture
	if err := json.Unmarshal(b, &fixtures); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, f := range fixtures {
		if f.ID == "" || ids[f.ID] || f.Family == "" || f.Provenance == "" || f.Source == "" || f.Configuration == "" || f.Expected == nil {
			t.Fatalf("invalid fixture provenance: %q", f.ID)
		}
		ids[f.ID] = true
	}
	return fixtures
}

func TestCorpusProvenance(t *testing.T) {
	if len(corpus(t)) < 16 {
		t.Fatal("missing compatibility families")
	}
}
