package lookup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/tools/deps/inventory"
)

func TestAgeReadsGoOriginAndNpmPublicationDate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.Contains(request.URL.Path, "reka-ui") {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write(fixture(t, "reka-ui.json"))
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(fixture(t, "uuid.info"))
	}))
	defer server.Close()

	entries := []inventory.Entry{
		{Ecosystem: "go", Name: "github.com/google/uuid", Version: "v1.6.0"},
		{Ecosystem: "npm", Name: "reka-ui", Version: "2.10.5"},
	}
	ages, err := Age(context.Background(), entries, server.URL, server.URL, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(ages) != 2 {
		t.Fatalf("got %d ages, want 2", len(ages))
	}
	if ages[0].Published.Format(time.RFC3339) != "2024-01-23T18:54:04Z" || ages[0].OriginCommit != "0f11ee6918f41a04c201eceeadf612a377bc7fbc" || ages[0].OriginMissing || ages[0].Under14Days || ages[0].Until.Format(time.RFC3339) != "2024-02-06T18:54:04Z" {
		t.Fatalf("Go age = %#v", ages[0])
	}
	if ages[1].Published.Format(time.RFC3339Nano) != "2026-09-21T13:18:23.018Z" || ages[1].OriginMissing || !ages[1].Under14Days || ages[1].Until.Format(time.RFC3339Nano) != "2026-10-05T13:18:23.018Z" {
		t.Fatalf("npm age = %#v", ages[1])
	}
}

func TestAgeEscapesGoPathAndReportsMissingOrigin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/github.com/!azure/go-ansiterm/@v/v0.0.0-20210617225240-d185dfc1b5a1.info" {
			t.Errorf("path = %q", request.URL.Path)
		}
		_, _ = writer.Write(fixture(t, "no-origin.info"))
	}))
	defer server.Close()

	ages, err := Age(context.Background(), []inventory.Entry{{Ecosystem: "go", Name: "github.com/Azure/go-ansiterm", Version: "v0.0.0-20210617225240-d185dfc1b5a1"}}, server.URL, server.URL, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(ages) != 1 || ages[0].OriginCommit != "" || !ages[0].OriginMissing {
		t.Fatalf("age = %#v", ages)
	}
}

func TestAgeReturnsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "no", http.StatusInternalServerError)
	}))
	defer server.Close()

	_, err := Age(context.Background(), []inventory.Entry{{Ecosystem: "go", Name: "example.com/module", Version: "v1.0.0"}}, server.URL, server.URL, time.Now())
	if err == nil {
		t.Fatal("Age() error = nil, want HTTP error")
	}
}

func TestAgeRejectsMissingGoPublicationDate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"Version":"v1.0.0"}`))
	}))
	defer server.Close()

	_, err := Age(context.Background(), []inventory.Entry{{Ecosystem: "go", Name: "example.com/module", Version: "v1.0.0"}}, server.URL, server.URL, time.Now())
	if err == nil {
		t.Fatal("Age() error = nil, want missing publication date error")
	}
	if !strings.Contains(err.Error(), "publication date") {
		t.Fatalf("Age() error = %q, want publication date", err)
	}
}
