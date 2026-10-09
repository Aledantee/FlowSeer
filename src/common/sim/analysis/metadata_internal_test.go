package analysis

import (
	"slices"
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestMetadataAssumptionsForSharesNoStorage(t *testing.T) {
	t.Parallel()

	scope := PortScope("sw1", "p1")
	metadata := NewMetadata(scope, nil, EvidenceCatalog{}, []Assumption{
		{Scope: scope, Statement: "original", Evidence: []trace.EvidenceRef{"b", "a", "a"}},
	})
	got := metadata.AssumptionsFor(scope)
	if len(got) != 1 || len(got[0].Evidence) != 2 {
		t.Fatalf("AssumptionsFor = %+v, want one assumption with two evidence references", got)
	}
	got[0].Statement = "changed"
	got[0].Evidence[0] = "changed"
	if kept := metadata.assumptions[0]; kept.Statement != "original" || !slices.Equal(kept.Evidence, []trace.EvidenceRef{"a", "b"}) {
		t.Errorf("stored assumption after returned mutation = %+v, want original with [a b]", kept)
	}
	if again := metadata.AssumptionsFor(scope); again[0].Statement != "original" || !slices.Equal(again[0].Evidence, []trace.EvidenceRef{"a", "b"}) {
		t.Errorf("second AssumptionsFor = %+v, want original with [a b]", again)
	}
}

// TestMetadataCloneSharesNoStorage writes through every field a clone holds and
// reads the source back through its accessors. The accessors copy on every
// call, so only a write that reaches the source's own fields can show sharing.
func TestMetadataCloneSharesNoStorage(t *testing.T) {
	t.Parallel()

	catalog, ref := EvidenceCatalog{}.Add(Evidence{Kind: "observation", Origin: "test", Context: "original"})
	scope := NodeScope("sw1")
	source := NewMetadata(scope,
		[]Issue{{
			Code:     "test/code",
			Status:   Incomplete,
			Scope:    scope,
			Message:  "original message",
			Evidence: []trace.EvidenceRef{ref},
		}},
		catalog,
		[]Assumption{{Scope: scope, Statement: "assumption 1", Evidence: []trace.EvidenceRef{ref}}},
	)
	want := source.Clone()
	if !want.Equal(source) {
		t.Fatal("Clone does not equal its source")
	}

	cloned := source.Clone()
	if len(cloned.issues) != 1 || len(cloned.assumptions) != 1 || len(cloned.issues[0].Evidence) != 1 || len(cloned.assumptions[0].Evidence) != 1 {
		t.Fatalf("clone holds %d issues and %d assumptions, want one of each with one evidence reference",
			len(cloned.issues), len(cloned.assumptions))
	}
	cloned.issues[0].Message = "mutated"
	cloned.issues[0].Evidence[0] = "mutated"
	cloned.assumptions[0].Statement = "mutated"
	cloned.assumptions[0].Evidence[0] = "mutated"
	cloned.evidence.entries[ref] = Evidence{Kind: "mutated"}

	if got := source.Issues()[0]; got.Message != "original message" || got.Evidence[0] != ref {
		t.Errorf("source issue after writing through the clone = %+v, want the original message and evidence", got)
	}
	if got := source.Assumptions()[0]; got.Statement != "assumption 1" || got.Evidence[0] != ref {
		t.Errorf("source assumption after writing through the clone = %+v, want the original statement and evidence", got)
	}
	if got := source.Evidence().Entries()[0].Evidence; got.Kind != "observation" {
		t.Errorf("source evidence after writing through the clone = %+v, want the original entry", got)
	}
	if !source.Equal(want) {
		t.Error("source changed after writing through its clone")
	}
}
