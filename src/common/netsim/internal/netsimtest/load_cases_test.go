package netsimtest

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

func TestLoadCases(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func() Case
		frame fabric.FrameID
	}{
		{"stated buffer", CasePlanningOversubscribedTrunkStatedBuffer, 3},
		{"unstated buffer", CasePlanningOversubscribedTrunkUnstatedBuffer, 3},
		{"policed", CasePlanningPolicedStream, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.build()
			if err := ValidateCase(c); err != nil {
				t.Fatalf("ValidateCase: %v", err)
			}
			if _, ok := DefaultRegistry().Get(c.ID); !ok {
				t.Fatalf("default registry lacks %q", c.ID)
			}
			result := AssertCase(t, c)
			if result.Journey == nil || result.Journey.FrameID != tc.frame {
				t.Fatalf("selected journey = %+v, want frame %d", result.Journey, tc.frame)
			}
		})
	}
}

func TestUnstatedLoadCaseCrossingPrecedesTransmission(t *testing.T) {
	result := AssertCase(t, CasePlanningOversubscribedTrunkUnstatedBuffer())
	journey := result.Journey
	crossingIndex, arrivalIndex := -1, -1
	for i, entry := range journey.Entries {
		if entry.Kind == fabric.EntryQueueThreshold {
			if crossingIndex >= 0 {
				t.Fatal("selected journey has more than one queue threshold")
			}
			crossingIndex = i
			if entry.Step == nil || entry.Step.Op != trace.OpQueue || entry.Step.RuleID != traffic.RuleQueueBufferUnstated {
				t.Fatalf("queue entry step = %+v", entry.Step)
			}
		}
		if entry.Kind == fabric.EntryArrival && entry.Device == "h2" {
			arrivalIndex = i
		}
		if entry.Kind == fabric.EntryDrop {
			t.Fatalf("unstated-buffer frame dropped at entry %d", i)
		}
	}
	if crossingIndex < 0 || arrivalIndex <= crossingIndex {
		t.Fatalf("queue entry %d must precede host arrival %d", crossingIndex, arrivalIndex)
	}
	if result.FabricMetadata == nil {
		t.Fatal("fabric metadata absent")
	}
	issue := result.Metadata.IssuesFor(analysis.PortScope("sw1", "1/1/2"))
	if len(issue) != 1 || len(issue[0].Evidence) != 1 {
		t.Fatalf("crossing issue = %+v, want one evidence ref", issue)
	}
	ref := issue[0].Evidence[0]
	for name, catalog := range map[string]analysis.EvidenceCatalog{
		"journey": result.Metadata.Evidence(), "fabric": result.FabricMetadata.Evidence(),
	} {
		if _, ok := catalog.Lookup(ref); !ok {
			t.Errorf("%s catalog cannot resolve %q", name, ref)
		}
	}
}
