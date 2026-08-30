package catalog_test

import (
	"slices"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/smi/internal/catalog"
)

func TestTableIsValid(t *testing.T) {
	if err := catalog.Validate(catalog.Entries()); err != nil {
		t.Fatalf("the committed table is invalid: %v", err)
	}
}

func TestEntriesAreSortedByCode(t *testing.T) {
	entries := catalog.Entries()
	codes := make([]string, len(entries))
	for i, e := range entries {
		codes[i] = e.Code
	}

	if !slices.IsSorted(codes) {
		t.Errorf("Entries() is not sorted by code: %v", codes)
	}
}

func TestEntriesIsACopy(t *testing.T) {
	first := catalog.Entries()
	if len(first) == 0 {
		t.Fatal("the table is empty")
	}

	first[0].Code = "smi/scribbled"

	if got := catalog.Entries()[0].Code; got == "smi/scribbled" {
		t.Error("mutating the result of Entries() changed the table")
	}
}

// Validate is the gate every later unit's appended row passes through, so
// each rejection it owes is proved rather than assumed.
func TestValidateRejects(t *testing.T) {
	ok := catalog.Entry{
		Severity: 2, Code: "smi/one", Tag: "One",
		Format: "a %s went wrong", Arity: 1, Description: "a thing",
	}

	tests := []struct {
		name string
		rows []catalog.Entry
		want string
	}{
		{
			name: "duplicate code",
			rows: []catalog.Entry{ok, {Severity: 2, Code: "smi/one", Tag: "Two", Format: "x", Description: "d"}},
			want: "smi/one",
		},
		{
			name: "duplicate tag",
			rows: []catalog.Entry{ok, {Severity: 2, Code: "smi/two", Tag: "One", Format: "x", Description: "d"}},
			want: "One",
		},
		{
			name: "severity above the scale",
			rows: []catalog.Entry{{Severity: 7, Code: "smi/two", Tag: "Two", Format: "x", Description: "d"}},
			want: "severity",
		},
		{
			name: "arity below the format's verb count",
			rows: []catalog.Entry{{Severity: 2, Code: "smi/two", Tag: "Two", Format: "%s and %d", Arity: 1, Description: "d"}},
			want: "arity",
		},
		{
			name: "arity above the format's verb count",
			rows: []catalog.Entry{{Severity: 2, Code: "smi/two", Tag: "Two", Format: "%s", Arity: 2, Description: "d"}},
			want: "arity",
		},
		{
			name: "arity beyond the inline argument array",
			rows: []catalog.Entry{{
				Severity: 2, Code: "smi/two", Tag: "Two",
				Format: "%s%s%s%s%s", Arity: catalog.MaxArgs + 1, Description: "d",
			}},
			want: "arity",
		},
		{
			name: "code outside the smi namespace",
			rows: []catalog.Entry{{Severity: 2, Code: "snmp/two", Tag: "Two", Format: "x", Description: "d"}},
			want: "smi/",
		},
		{
			name: "malformed code",
			rows: []catalog.Entry{{Severity: 2, Code: "smi/Two", Tag: "Two", Format: "x", Description: "d"}},
			want: "smi/Two",
		},
		{
			name: "missing tag",
			rows: []catalog.Entry{{Severity: 2, Code: "smi/two", Format: "x", Description: "d"}},
			want: "tag",
		},
		{
			name: "missing description",
			rows: []catalog.Entry{{Severity: 2, Code: "smi/two", Tag: "Two", Format: "x"}},
			want: "description",
		},
		{
			name: "empty format",
			rows: []catalog.Entry{{Severity: 2, Code: "smi/two", Tag: "Two", Description: "d"}},
			want: "format",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := catalog.Validate(tc.rows)
			if err == nil {
				t.Fatalf("Validate accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestCountVerbs(t *testing.T) {
	tests := []struct {
		format string
		want   int
	}{
		{"no verbs here", 0},
		{"%s", 1},
		{"%s and %d", 2},
		{"100%% done", 0},
		{"100%% done with %q", 1},
		{"%-8s%+d", 2},
	}

	for _, tc := range tests {
		if got := catalog.CountVerbs(tc.format); got != tc.want {
			t.Errorf("CountVerbs(%q) = %d, want %d", tc.format, got, tc.want)
		}
	}
}
