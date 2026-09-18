package yang

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

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

// isRevisionDate reports whether a revision is an RFC 7950 revision
// date (§7.1.9's YYYY-MM-DD). Anything else is treated as a different
// vocabulary, in practice an OpenConfig semantic version.
func isRevisionDate(rev string) bool {
	if len(rev) != len("2006-01-02") {
		return false
	}
	_, err := time.Parse(time.DateOnly, rev)
	return err == nil
}

// DiffRevisions compares vendored revisions against a session's
// advertised ones. It returns the drifting modules and, separately,
// those whose two sides use different revision vocabularies, both
// sorted. Modules absent on either side appear in neither: a device
// may implement a subset, and it may serve modules the bindings do not
// cover.
//
// A NETCONF hello carries RFC 7950 revision dates, while gNMI
// Capabilities reports an OpenConfig module's openconfig-version
// semantic version, which never equals a vendored date. Callers may
// treat the incomparable pairs however they like, but must not read
// them as drift; see
// docs/solutions/conventions/same-typed-metadata-maps-can-carry-different-vocabularies.md.
func DiffRevisions(vendored, advertised map[string]string) (drift, incomparable []RevisionDrift) {
	for module, vendoredRev := range vendored {
		advertisedRev, ok := advertised[module]
		if !ok || advertisedRev == "" || advertisedRev == vendoredRev {
			continue
		}
		entry := RevisionDrift{Module: module, Vendored: vendoredRev, Advertised: advertisedRev}
		if isRevisionDate(vendoredRev) != isRevisionDate(advertisedRev) {
			incomparable = append(incomparable, entry)
			continue
		}
		drift = append(drift, entry)
	}
	byModule := func(a, b RevisionDrift) int { return strings.Compare(a.Module, b.Module) }
	slices.SortFunc(drift, byModule)
	slices.SortFunc(incomparable, byModule)
	return drift, incomparable
}
