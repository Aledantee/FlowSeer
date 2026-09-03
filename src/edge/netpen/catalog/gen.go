package catalog

// Run `go generate ./catalog/...` from the netpen module to regenerate
// zz_generated_catalog.go. TestGeneratedCatalogIsCurrent runs the generator
// into a temporary file and compares it with the committed catalog.

//go:generate go run gen_catalog.go
