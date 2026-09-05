package main

import (
	"os"
	"path/filepath"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/smi"
)

// loadRepositoryIdentity collects the identity table from the repository's
// own mibgen.yaml, so the test sees exactly the entries the committed
// sysobjectid package is rendered from.
func loadRepositoryIdentity(t *testing.T) []identityEntry {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", "..", "..", "..", ".."))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	cfgPath := filepath.Join(root, "mibgen.yaml")
	if _, err := os.Stat(cfgPath); err != nil {
		t.Skipf("repository config not available: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	set, err := LoadModules(cfg)
	if err != nil {
		t.Fatalf("load modules: %v", err)
	}

	return collectIdentity(cfg, set)
}

// resolveIdentity is the longest-prefix rule the generated Lookup applies,
// spelled out over the collected entries so the test does not depend on
// the rendered package.
func resolveIdentity(entries []identityEntry, oid smi.OID) (identityEntry, bool) {
	var (
		best  identityEntry
		found bool
	)
	for _, e := range entries {
		if oid.HasPrefix(e.OID) && (!found || e.OID.Len() > best.OID.Len()) {
			best, found = e, true
		}
	}

	return best, found
}

// TestIdentity_RepositoryConfigResolvesProducts pins what listing the
// vendor product MIBs buys: a Ruckus ICX 6610 stack resolves to its own
// node, an unlisted model under a known family resolves to the family, a
// Comware product the list does not name resolves to hh3cProductId, and
// an enterprise no configured module declares is not found rather than
// attributed to the wrong vendor.
func TestIdentity_RepositoryConfigResolvesProducts(t *testing.T) {
	entries := loadRepositoryIdentity(t)

	// foundry(1991).products(1).registration(3).snFastIronStackFamily(48)
	foundryStack := smi.NewOID(1, 3, 6, 1, 4, 1, 1991, 1, 3, 48)
	// hh3c(25506).hh3cProductId(1)
	comwareProducts := smi.NewOID(1, 3, 6, 1, 4, 1, 25506, 1)

	tests := []struct {
		name   string
		oid    smi.OID
		want   string
		module string
	}{
		{
			name:   "ICX 6610 stack",
			oid:    foundryStack.Child(3),
			want:   "snFastIronStackICX6610",
			module: "FOUNDRY-SN-ROOT-MIB",
		},
		{
			name:   "unknown model under the stack family",
			oid:    foundryStack.Child(4711),
			want:   "snFastIronStackFamily",
			module: "FOUNDRY-SN-ROOT-MIB",
		},
		{
			name:   "unlisted Comware product",
			oid:    comwareProducts.Child(999999),
			want:   "hh3cProductId",
			module: "HH3C-OID-MIB",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resolveIdentity(entries, tc.oid)
			if !ok {
				t.Fatalf("%s resolved to nothing, want %s", tc.oid, tc.want)
			}
			if got.Name != tc.want || got.Module != tc.module {
				t.Errorf("%s resolved to %s from %s, want %s from %s",
					tc.oid, got.Name, got.Module, tc.want, tc.module)
			}
		})
	}

	// Enterprise 99999999 is assigned to nobody in the configured set.
	unknown := smi.NewOID(1, 3, 6, 1, 4, 1, 99999999, 1, 1)
	if got, ok := resolveIdentity(entries, unknown); ok {
		t.Errorf("%s resolved to %s from %s, want not found", unknown, got.Name, got.Module)
	}
}
