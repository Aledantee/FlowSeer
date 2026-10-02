package simtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
)

func TestAllDeclaredGroupsClaimed(t *testing.T) {
	simtest.AssertAllGroupsClaimed(t)
}

func TestEnumerationFailsOnUnclaimedGroup(t *testing.T) {
	declared := []simtest.Group{
		simtest.GroupResultCanonicality,
		simtest.GroupOrderingDeterminism,
	}
	claims := map[simtest.Group][]string{
		simtest.GroupResultCanonicality: {"vswitch"},
		// GroupOrderingDeterminism is missing
	}

	err := simtest.ValidateClaims(declared, claims)
	if err == nil {
		t.Fatal("ValidateClaims succeeded, want error for unclaimed group")
	}
	if !strings.Contains(err.Error(), "unclaimed conformance group") || !strings.Contains(err.Error(), string(simtest.GroupOrderingDeterminism)) {
		t.Errorf("ValidateClaims err = %q, want error mentioning unclaimed %q", err.Error(), simtest.GroupOrderingDeterminism)
	}
}

func TestEnumerationFailsOnMultiplyClaimedGroup(t *testing.T) {
	declared := []simtest.Group{
		simtest.GroupResultCanonicality,
	}
	claims := map[simtest.Group][]string{
		simtest.GroupResultCanonicality: {"vswitch", "fabric"},
	}

	err := simtest.ValidateClaims(declared, claims)
	if err == nil {
		t.Fatal("ValidateClaims succeeded, want error for multiply claimed group")
	}
	if !strings.Contains(err.Error(), "claimed by multiple packages") {
		t.Errorf("ValidateClaims err = %q, want error mentioning multiple packages", err.Error())
	}
}

func TestEnumerationFailsOnUnknownClaimedGroup(t *testing.T) {
	declared := []simtest.Group{
		simtest.GroupResultCanonicality,
	}
	claims := map[simtest.Group][]string{
		simtest.GroupResultCanonicality: {"vswitch"},
		"unrecognized-group":            {"fabric"},
	}

	err := simtest.ValidateClaims(declared, claims)
	if err == nil {
		t.Fatal("ValidateClaims succeeded, want error for unknown group")
	}
	if !strings.Contains(err.Error(), "unknown conformance group") {
		t.Errorf("ValidateClaims err = %q, want error mentioning unknown conformance group", err.Error())
	}
}

func TestCollectClaimsFromDirectory(t *testing.T) {
	tmp := t.TempDir()
	pkgDir := filepath.Join(tmp, "subpkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(pkgDir, "conformance_test.go")
	content := `package subpkg
// Conformance group: result-canonicality
// Covers conformance group: ordering-determinism
`
	if err := os.WriteFile(testFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	claims, err := simtest.CollectClaims(tmp)
	if err != nil {
		t.Fatalf("CollectClaims: %v", err)
	}

	if len(claims[simtest.GroupResultCanonicality]) != 1 || claims[simtest.GroupResultCanonicality][0] != "subpkg" {
		t.Errorf("claims[result-canonicality] = %v, want [subpkg]", claims[simtest.GroupResultCanonicality])
	}
	if len(claims[simtest.GroupOrderingDeterminism]) != 1 || claims[simtest.GroupOrderingDeterminism][0] != "subpkg" {
		t.Errorf("claims[ordering-determinism] = %v, want [subpkg]", claims[simtest.GroupOrderingDeterminism])
	}
}
