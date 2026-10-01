package lookup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"go.aledante.io/FlowSeer/tools/deps/inventory"
)

func TestAdvisoriesReadsBatchAndDetailFixtures(t *testing.T) {
	batch := fixture(t, "osv-batch.json")
	grpcDetail := fixture(t, "osv-detail.json")
	cryptoDetail := fixture(t, "osv-detail-crypto.json")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/querybatch":
			var body struct {
				Queries []struct {
					Package struct {
						Ecosystem string `json:"ecosystem"`
						Name      string `json:"name"`
					} `json:"package"`
					Version string `json:"version"`
				} `json:"queries"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode request: %v", err)
			}
			if len(body.Queries) != 3 || body.Queries[0].Version != "1.84.0" || body.Queries[1].Version != "0.57.0" || body.Queries[2].Version != "2.10.5" {
				t.Errorf("queries = %#v", body.Queries)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write(batch)
		case "/vulns/GO-2026-6443":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write(grpcDetail)
		case "/vulns/GO-2026-5932":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write(cryptoDetail)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	entries := []inventory.Entry{
		{Ecosystem: "go", Name: "google.golang.org/grpc", Version: "v1.84.0"},
		{Ecosystem: "go", Name: "golang.org/x/crypto", Version: "v0.57.0"},
		{Ecosystem: "npm", Name: "reka-ui", Version: "2.10.5"},
	}
	advisories, err := Advisories(context.Background(), entries, server.URL+"/querybatch", server.URL+"/vulns/")
	if err != nil {
		t.Fatal(err)
	}
	if len(advisories) != 2 {
		t.Fatalf("got %d advisories, want 2", len(advisories))
	}
	if advisories[0].ID != "GO-2026-6443" || advisories[0].Summary == "" || len(advisories[0].Imports) != 2 {
		t.Fatalf("first advisory = %#v", advisories[0])
	}
	if advisories[1].ID != "GO-2026-5932" || len(advisories[1].Imports) != 1 {
		t.Fatalf("second advisory = %#v", advisories[1])
	}
}

func TestAdvisoriesFollowsPerQueryPageToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"id":"detail","summary":"page detail"}`))
			return
		}
		var body struct {
			Queries []struct {
				PageToken string `json:"page_token"`
			} `json:"queries"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		if body.Queries[0].PageToken == "" {
			_, _ = writer.Write([]byte(`{"results":[{"vulns":[{"id":"GO-PAGE-ONE"}],"next_page_token":"next"}]}`))
			return
		}
		if body.Queries[0].PageToken != "next" {
			t.Errorf("page token = %q, want next", body.Queries[0].PageToken)
		}
		_, _ = writer.Write([]byte(`{"results":[{"vulns":[{"id":"GO-PAGE-TWO"}]}]}`))
	}))
	defer server.Close()

	advisories, err := Advisories(context.Background(), []inventory.Entry{{Ecosystem: "go", Name: "example.com/module", Version: "v1.0.0"}}, server.URL, server.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if len(advisories) != 2 || advisories[0].ID != "GO-PAGE-ONE" || advisories[1].ID != "GO-PAGE-TWO" {
		t.Fatalf("advisories = %#v", advisories)
	}
}

func TestAdvisoriesReturnsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "no", http.StatusBadGateway)
	}))
	defer server.Close()

	_, err := Advisories(context.Background(), []inventory.Entry{{Ecosystem: "go", Name: "example.com/module", Version: "v1.0.0"}}, server.URL, server.URL+"/")
	if err == nil {
		t.Fatal("Advisories() error = nil, want HTTP error")
	}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
