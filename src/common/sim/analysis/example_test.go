package analysis_test

import (
	"errors"
	"fmt"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func ExampleMetadata_invalidAndPartial() {
	if _, err := exampleAnalysis(false); err != nil {
		fmt.Println(err)
	}

	metadata, err := exampleAnalysis(true)
	if err != nil {
		panic(err)
	}
	fmt.Println(metadata.Status())
	fmt.Println(metadata.StatusFor(analysis.PortScope("switch-a", "1/1")))
	fmt.Println(metadata.StatusFor(analysis.PortScope("switch-a", "1/2")))

	// Output:
	// invalid input
	// incomplete
	// incomplete
	// complete
}

func exampleAnalysis(valid bool) (analysis.Metadata, error) {
	if !valid {
		return analysis.Metadata{}, errors.New("invalid input")
	}

	port := analysis.PortScope("switch-a", "1/1")
	catalog, evidenceRef := (analysis.EvidenceCatalog{}).Add(analysis.Evidence{
		Kind:    "observation",
		Origin:  "snapshot/current",
		Context: "operational state was absent",
	})
	metadata := analysis.NewMetadata(analysis.NodeScope("switch-a"), []analysis.Issue{{
		Code:     "port/operational-state-missing",
		Status:   analysis.Incomplete,
		Scope:    port,
		Evidence: []trace.EvidenceRef{evidenceRef},
	}}, catalog, nil)
	return metadata, nil
}
