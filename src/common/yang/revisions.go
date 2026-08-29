package yang

import (
	"encoding/json"
	"sort"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// revisions.go is the runtime half of revision-drift detection: the
// yanggen lockfile records the
// module revisions the committed bindings were generated from; a
// session's advertised revisions are compared against them and any
// mismatch surfaces as a warning-grade drift list (warn-and-proceed
// by default — the caller decides whether drift is fatal).

// RevisionDrift is one module whose device-advertised revision
// differs from the vendored revision the bindings were built from.
type RevisionDrift struct {
	Module     string
	Vendored   string
	Advertised string
}

// lockfileDoc mirrors yanggen.lock.json's shape.
type lockfileDoc struct {
	Modules map[string]struct {
		Revision string `json:"revision"`
	} `json:"modules"`
}

// ParseLockfileRevisions extracts one vendor's module→revision map
// from yanggen.lock.json bytes (lockfile keys are "<vendor>/<module>").
// Callers typically embed the lockfile into their binary.
func ParseLockfileRevisions(data []byte, vendor string) (map[string]string, error) {
	var doc lockfileDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, errs.Wrap(err, "parse yanggen lockfile")
	}
	out := make(map[string]string)
	prefix := vendor + "/"
	for key, m := range doc.Modules {
		if name, ok := strings.CutPrefix(key, prefix); ok {
			out[name] = m.Revision
		}
	}
	if len(out) == 0 {
		return nil, errs.Msgf("lockfile carries no modules for vendor %q", vendor)
	}
	return out, nil
}

// DiffRevisions compares vendored revisions against a session's
// advertised ones and returns the drifting modules, sorted. Modules
// absent on either side are not drift: a device may implement a
// subset, and it may serve modules the bindings do not cover.
func DiffRevisions(vendored, advertised map[string]string) []RevisionDrift {
	var out []RevisionDrift
	for module, vendoredRev := range vendored {
		advertisedRev, ok := advertised[module]
		if !ok || advertisedRev == "" || advertisedRev == vendoredRev {
			continue
		}
		out = append(out, RevisionDrift{Module: module, Vendored: vendoredRev, Advertised: advertisedRev})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Module < out[j].Module })
	return out
}
