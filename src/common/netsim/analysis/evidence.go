package analysis

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

// EvidenceKind identifies a caller-defined class of supporting evidence. The
// zero value is an unspecified kind. EvidenceKind is safe for concurrent use.
type EvidenceKind string

// String returns the evidence kind unchanged.
func (k EvidenceKind) String() string {
	return string(k)
}

// Evidence records domain-neutral support for an issue or assumption. Origin
// and Context are caller-supplied stable descriptions; transport provenance
// stays in the caller's package. Evidence is immutable and safe for concurrent use.
type Evidence struct {
	Kind    EvidenceKind
	Origin  string
	Context string
}

// EvidenceEntry pairs an opaque reference with its evidence value. EvidenceEntry
// is immutable and safe for concurrent use.
type EvidenceEntry struct {
	Ref      trace.EvidenceRef
	Evidence Evidence
}

// EvidenceCatalog stores deduplicated evidence under deterministic opaque
// references. Add uses copy-on-write, so copies of a catalog and the zero value
// are safe for concurrent use. Entries and Lookup never expose mutable state.
type EvidenceCatalog struct {
	entries map[trace.EvidenceRef]Evidence
}

// Add returns a catalog containing evidence and its deterministic reference.
// The receiver remains unchanged. Equal evidence always produces the same reference.
func (c EvidenceCatalog) Add(evidence Evidence) (EvidenceCatalog, trace.EvidenceRef) {
	ref := referenceForEvidence(evidence)
	if existing, ok := c.entries[ref]; ok && existing == evidence {
		return c, ref
	}

	entries := make(map[trace.EvidenceRef]Evidence, len(c.entries)+1)
	for existingRef, existing := range c.entries {
		entries[existingRef] = existing
	}
	entries[ref] = evidence
	return EvidenceCatalog{entries: entries}, ref
}

// Lookup returns the evidence for ref without exposing catalog state.
func (c EvidenceCatalog) Lookup(ref trace.EvidenceRef) (Evidence, bool) {
	evidence, ok := c.entries[ref]
	return evidence, ok
}

// Entries returns an independent snapshot ordered by reference.
func (c EvidenceCatalog) Entries() []EvidenceEntry {
	if len(c.entries) == 0 {
		return nil
	}

	entries := make([]EvidenceEntry, 0, len(c.entries))
	for ref, evidence := range c.entries {
		entries = append(entries, EvidenceEntry{Ref: ref, Evidence: evidence})
	}
	slices.SortFunc(entries, func(a, b EvidenceEntry) int {
		return strings.Compare(string(a.Ref), string(b.Ref))
	})
	return entries
}

func referenceForEvidence(evidence Evidence) trace.EvidenceRef {
	var canonical strings.Builder
	for _, field := range []string{string(evidence.Kind), evidence.Origin, evidence.Context} {
		canonical.WriteString(strconv.Itoa(len(field)))
		canonical.WriteByte(':')
		canonical.WriteString(field)
	}
	digest := sha256.Sum256([]byte(canonical.String()))
	return trace.EvidenceRef("evidence:" + hex.EncodeToString(digest[:]))
}
