package netsimtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/netsim/internal/netsimtest"
)

func TestAllDeclaredGroupsClaimed(t *testing.T) {
	netsimtest.AssertAllGroupsClaimed(t)
}

func TestEnumerationFailsOnUnclaimedGroup(t *testing.T) {
	declared := []netsimtest.Group{
		netsimtest.GroupResultCanonicality,
		netsimtest.GroupOrderingDeterminism,
	}
	claims := map[netsimtest.Group][]string{
		netsimtest.GroupResultCanonicality: {"vswitch"},
		// GroupOrderingDeterminism is missing
	}

	err := netsimtest.ValidateClaims(declared, claims)
	if err == nil {
		t.Fatal("ValidateClaims succeeded, want error for unclaimed group")
	}
	if !strings.Contains(err.Error(), "unclaimed conformance group") || !strings.Contains(err.Error(), string(netsimtest.GroupOrderingDeterminism)) {
		t.Errorf("ValidateClaims err = %q, want error mentioning unclaimed %q", err.Error(), netsimtest.GroupOrderingDeterminism)
	}
}

func TestEnumerationFailsOnMultiplyClaimedGroup(t *testing.T) {
	declared := []netsimtest.Group{
		netsimtest.GroupResultCanonicality,
	}
	claims := map[netsimtest.Group][]string{
		netsimtest.GroupResultCanonicality: {"vswitch", "fabric"},
	}

	err := netsimtest.ValidateClaims(declared, claims)
	if err == nil {
		t.Fatal("ValidateClaims succeeded, want error for multiply claimed group")
	}
	if !strings.Contains(err.Error(), "claimed by multiple packages") {
		t.Errorf("ValidateClaims err = %q, want error mentioning multiple packages", err.Error())
	}
}

func TestEnumerationFailsOnUnknownClaimedGroup(t *testing.T) {
	declared := []netsimtest.Group{
		netsimtest.GroupResultCanonicality,
	}
	claims := map[netsimtest.Group][]string{
		netsimtest.GroupResultCanonicality: {"vswitch"},
		"unrecognized-group":               {"fabric"},
	}

	err := netsimtest.ValidateClaims(declared, claims)
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

	claims, err := netsimtest.CollectClaims(tmp)
	if err != nil {
		t.Fatalf("CollectClaims: %v", err)
	}

	if len(claims[netsimtest.GroupResultCanonicality]) != 1 || claims[netsimtest.GroupResultCanonicality][0] != "subpkg" {
		t.Errorf("claims[result-canonicality] = %v, want [subpkg]", claims[netsimtest.GroupResultCanonicality])
	}
	if len(claims[netsimtest.GroupOrderingDeterminism]) != 1 || claims[netsimtest.GroupOrderingDeterminism][0] != "subpkg" {
		t.Errorf("claims[ordering-determinism] = %v, want [subpkg]", claims[netsimtest.GroupOrderingDeterminism])
	}
}
