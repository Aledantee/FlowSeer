package catalog

// gen.go carries the go:generate directive and the generator entrypoint.
// Run `go generate ./catalog/...` to regenerate zz_generated_catalog.go.
// The generator is also the drift gate: `go generate` followed by a
// byte-comparison test (TestGeneratedCatalogIsCurrent) fails when the
// committed source and the in-memory entries diverge.

//go:generate go run gen_catalog.go
