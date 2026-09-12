# Analysis trust metadata

The `analysis` package separates invalid inputs from valid analyses whose
answers are partial. A capability rejects invalid configuration before running.
When valid input cannot be evaluated completely, it returns its domain result
with metadata that explains which scope is affected.

```go
func analyze(validity analysis.InputValidity) (analysis.Metadata, error) {
	if validity == analysis.InputInvalid {
		return analysis.Metadata{}, errors.New("invalid input")
	}

	port := analysis.PortScope("switch-a", "1/1")
	catalog, evidenceRef := (analysis.EvidenceCatalog{}).Add(analysis.Evidence{
		Kind:    "observation",
		Origin:  "snapshot/current",
		Context: "operational state was absent",
	})
	return analysis.NewMetadata(analysis.NodeScope("switch-a"), []analysis.Issue{{
		Code:     "port/operational-state-missing",
		Status:   analysis.Incomplete,
		Scope:    port,
		Evidence: []trace.EvidenceRef{evidenceRef},
	}}, catalog, nil), nil
}
```

The node result is incomplete because one child port is incomplete. A query for
a sibling port remains complete:

```go
metadata.Status()                                      // incomplete
metadata.StatusFor(analysis.PortScope("switch-a", "1/1")) // incomplete
metadata.StatusFor(analysis.PortScope("switch-a", "1/2")) // complete
```

Issues use capability-owned codes; the shared package does not register them.
All causes are retained in canonical order. Status uses the fixed conservative
precedence `unsupported > unstable > exhausted > incomplete > complete`.

Scopes cover the whole analysis, a node, one of its ports, a link, a protocol
instance, a journey, or a field path beneath any of those subjects. Parent and
child scopes overlap, while siblings do not. Field paths retain element
boundaries: the field root contains every field path, `FieldScope(parent, "")`
is distinct from that root, and a path contains paths with the same element
prefix. `Scope.String` quotes identifiers, so rendering stays deterministic
even when an identifier contains `/`.

`Metadata`, `Scope`, and `EvidenceCatalog` are immutable values safe for
concurrent readers. `EvidenceCatalog.Add` returns a new catalog. Collection
accessors return copies, so changing a returned slice cannot alter stored
metadata.
