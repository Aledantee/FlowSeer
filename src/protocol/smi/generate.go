package smi

// generate.go carries the go:generate directive and nothing else, so the
// command that regenerates zz_generated_codes.go is findable from the
// package it writes into. Run `go generate ./src/protocol/smi/...` after
// appending a row to the diagnostic table in internal/catalog.
//
// Regeneration is checked, not trusted: TestGeneratedCodesAreCurrent
// compares the committed file against a fresh render, so a forgotten
// `go generate` and a hand-edited code fail the same test.

//go:generate go run ./internal/catalog/gen
