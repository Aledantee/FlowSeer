package integration

import (
	"os"
	"path/filepath"
	"strings"

	"go.aledante.io/ae"
	"gopkg.in/yaml.v3"
)

// Manifest is the YAML index of T3 .snmprec captures. One entry per
// device-baseline; the t3 replay test iterates entries and asserts
// the dense-row contract against each replay.
//
// The format is intentionally tiny so an operator (A2) can add a new
// vendor capture by dropping a .snmprec file under
// testdata/snmprec/<vendor>/ and appending one entry — no Go code
// change.
type Manifest struct {
	Entries []ManifestEntry `yaml:"entries"`
}

// ManifestEntry describes one .snmprec baseline.
type ManifestEntry struct {
	// Vendor is the device manufacturer (free-form text used for
	// reporting and grouping; e.g., "nokia", "cisco", "ruckus").
	Vendor string `yaml:"vendor"`

	// Device is the model or platform (e.g., "srlinux-24", "ios-xe-17").
	Device string `yaml:"device"`

	// CapturedOn is the date the capture was taken, in YYYY-MM-DD
	// form. Surfaces in test output so stale captures are visible
	// without a separate audit.
	CapturedOn string `yaml:"captured_on"`

	// SnmpsimContext is the v2c community string the replay test
	// uses to address this entry's data. snmpsim's community →
	// context mapping routes incoming requests to the matching
	// .snmprec file by this name; entries must be unique within a
	// manifest.
	SnmpsimContext string `yaml:"snmpsim_context"`

	// Snmprec is the path to the .snmprec file, relative to the
	// manifest's directory.
	Snmprec string `yaml:"snmprec"`

	// Scenarios is the optional list of scenario tags the replay
	// covers (e.g., "dense-row-collector", "trap-receiver"). Used
	// only for human-readable filtering; the replay test does not
	// branch on this field.
	Scenarios []string `yaml:"scenarios,omitempty"`
}

// LoadManifest reads, decodes, and validates the YAML manifest at
// path. Validation:
//
//   - Each entry's required fields (Vendor, Device, SnmpsimContext,
//     Snmprec) are non-empty.
//   - SnmpsimContext is unique within the manifest.
//   - SnmpsimContext equals Snmprec with the .snmprec extension
//     stripped — snmpsim's v2c community→context routing is
//     filename-based, so a mismatched entry would silently dial the
//     wrong community and surface as "agent has no ifTable entries"
//     with no breadcrumb back to the manifest.
//   - Snmprec resolves (after filepath.Join with the manifest's
//     directory) to a descendant of the manifest directory — no
//     ../ traversal is permitted. The data dir gets bind-mounted
//     into snmpsim, so an out-of-tree snmprec would silently leak
//     into the replay corpus.
//   - The resolved Snmprec path exists as a regular file.
//
// On any failure the returned error names the manifest path and the
// offending entry's index or context so iteration is fast.
func LoadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, ae.Wrapf("manifest %s: read", err, path)
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return Manifest{}, ae.Wrapf("manifest %s: decode", err, path)
	}
	manifestDir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return Manifest{}, ae.Wrapf("manifest %s: resolve dir", err, path)
	}
	seen := make(map[string]int, len(m.Entries))
	for i, e := range m.Entries {
		if e.Vendor == "" {
			return Manifest{}, ae.New().Attr("entry", i).Attr("manifest", path).
				Msg("manifest entry has empty vendor")
		}
		if e.Device == "" {
			return Manifest{}, ae.New().Attr("entry", i).Attr("vendor", e.Vendor).Attr("manifest", path).
				Msg("manifest entry has empty device")
		}
		if e.SnmpsimContext == "" {
			return Manifest{}, ae.New().Attr("entry", i).Attr("vendor", e.Vendor).Attr("device", e.Device).Attr("manifest", path).
				Msg("manifest entry has empty snmpsim_context")
		}
		if e.Snmprec == "" {
			return Manifest{}, ae.New().Attr("entry", i).Attr("vendor", e.Vendor).Attr("device", e.Device).Attr("manifest", path).
				Msg("manifest entry has empty snmprec")
		}
		if dup, ok := seen[e.SnmpsimContext]; ok {
			return Manifest{}, ae.New().Attr("snmpsim_context", e.SnmpsimContext).Attr("manifest", path).
				Attr("first_entry", dup).Attr("second_entry", i).
				Msg("duplicate snmpsim_context in manifest")
		}
		seen[e.SnmpsimContext] = i

		// snmpsim's community-to-context derivation rule.
		wantContext := strings.TrimSuffix(e.Snmprec, ".snmprec")
		if e.SnmpsimContext != wantContext {
			return Manifest{}, ae.New().Attr("got", e.SnmpsimContext).Attr("want", wantContext).
				Attr("snmprec", e.Snmprec).Attr("entry", i).Attr("vendor", e.Vendor).
				Attr("device", e.Device).Attr("manifest", path).
				Msg("snmpsim_context does not match snmprec path (snmpsim routes by file path without the .snmprec extension)")
		}

		// Reject ../ traversal. Resolve the absolute path of the
		// joined target and require it to stay under manifestDir.
		full, err := filepath.Abs(filepath.Join(manifestDir, e.Snmprec))
		if err != nil {
			return Manifest{}, ae.Wrapf("manifest %s: entry %d (%s/%s): resolve snmprec path %s", err, path, i, e.Vendor, e.Device, e.Snmprec)
		}
		rel, err := filepath.Rel(manifestDir, full)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return Manifest{}, ae.New().Attr("snmprec", e.Snmprec).Attr("entry", i).
				Attr("vendor", e.Vendor).Attr("device", e.Device).Attr("manifest", path).
				Msg("snmprec escapes the manifest directory")
		}
		if _, err := os.Stat(full); err != nil {
			return Manifest{}, ae.Wrapf("manifest %s: entry %d (%s/%s): snmprec file %s", err, path, i, e.Vendor, e.Device, e.Snmprec)
		}
	}
	return m, nil
}
