package netpenguard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanHeavyDeps(t *testing.T) {
	for _, tc := range []struct {
		name   string
		path   string
		source string
		leaks  int
	}{
		{
			name:   "ordinary import",
			path:   "src/backend/leak.go",
			source: "package backend\nimport _ \"charm.land/bubbletea/v2\"\n",
			leaks:  1,
		},
		{
			name:   "guard filename in another package",
			path:   "src/backend/no_heavy_deps_test.go",
			source: "package backend\nimport _ \"charm.land/bubbletea/v2\"\n",
			leaks:  1,
		},
		{
			name:   "escaped import",
			path:   "src/backend/leak.go",
			source: "package backend\nimport _ \"\\x63harm.land/bubbletea/v2\"\n",
			leaks:  1,
		},
		{
			name:   "prefixes in comments and values",
			path:   "src/backend/allowed.go",
			source: "package backend\n// charm.land/bubbletea/v2\nconst path = \"github.com/gopacket/gopacket\"\n",
		},
		{
			name:   "quarantined import",
			path:   "src/edge/netpen/link.go",
			source: "package netpen\nimport _ \"github.com/gopacket/gopacket\"\n",
		},
		{
			name:   "quarantine sibling",
			path:   "src/edge/netpenother/link.go",
			source: "package netpenother\nimport _ \"github.com/gopacket/gopacket\"\n",
			leaks:  1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, filepath.FromSlash(tc.path))
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.source), 0o600); err != nil {
				t.Fatal(err)
			}

			leaks, err := scanHeavyDeps(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(leaks) != tc.leaks {
				t.Fatalf("got %d leaks, want %d: %v", len(leaks), tc.leaks, leaks)
			}
			if len(leaks) > 0 && leaks[0].path != filepath.FromSlash(tc.path) {
				t.Errorf("got leak path %q, want %q", leaks[0].path, filepath.FromSlash(tc.path))
			}
		})
	}
}
