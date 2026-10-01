package dependencies_test

import (
	"testing"

	"go.aledante.io/FlowSeer/tools/deps/inventory"
)

func TestDirectDependenciesHaveStatements(t *testing.T) {
	root := repositoryRoot(t)
	direct, err := inventory.DirectDependencies(root)
	if err != nil {
		t.Fatalf("discovering direct dependencies: %v", err)
	}
	if len(direct) == 0 {
		t.Fatal("statement gate checked zero direct dependencies")
	}

	findings, err := inventory.CheckStatements(root)
	if err != nil {
		t.Fatalf("checking dependency statements: %v", err)
	}
	for _, finding := range findings {
		t.Errorf("%s: %s", finding.Path, finding.Message)
	}
}
