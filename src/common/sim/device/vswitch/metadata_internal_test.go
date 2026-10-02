package vswitch

import (
	"slices"
	"testing"

	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

func TestRuntimeEvidenceIdentityDoesNotUseIssueMessage(t *testing.T) {
	scope := analysis.PortScope("sw1", "1/1/1")
	metadataFor := func(message string) analysis.Metadata {
		return forwardingMetadata("sw1", analysis.Metadata{}, []analysis.Scope{scope}, []runtimeIssue{{
			issue: analysis.Issue{
				Code:    "test.runtime",
				Status:  analysis.Incomplete,
				Scope:   scope,
				Message: message,
			},
			facts: []trace.Fact{runtimeFact{typeID: "test.fact", canonical: "value=stable"}},
		}})
	}

	first := metadataFor("first wording")
	second := metadataFor("revised wording")
	if !slices.Equal(first.Issues()[0].Evidence, second.Issues()[0].Evidence) {
		t.Fatalf("wording changed runtime evidence refs: %v != %v", first.Issues()[0].Evidence, second.Issues()[0].Evidence)
	}
	if !slices.Equal(first.Evidence().Entries(), second.Evidence().Entries()) {
		t.Fatalf("wording changed runtime evidence catalog: %+v != %+v", first.Evidence().Entries(), second.Evidence().Entries())
	}
	if !metadataEqual(first, second) {
		t.Fatal("wording changed semantic forwarding metadata")
	}
	if got := first.Evidence().Entries()[0].Evidence.Context; got == "first wording" {
		t.Fatalf("runtime evidence context uses human issue wording: %q", got)
	}
}
