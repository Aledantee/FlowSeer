// mibgen generates Go bindings for SMIv2 MIB modules.
//
// Each loaded MIB module produces one Go package under -out/<package>/
// containing a single mib.go file. The generated code consumes only
// the public API of common/snmp: typed scalar Get-accessors,
// column values built via [snmp.NewColumn], per-table
// row structs and Walkers, SMI enum types, and a
// per-package OID → AnyColumn dispatch map. Well-known SMIv2
// textual conventions delegate to [snmp.Decode*] helpers.
//
// Two generated shapes carry more than the MIB's field list:
//
//   - Each row type has an Observed(snmp.AnyColumn) bool method. A
//     zero-valued field is ambiguous on its own — the agent may have
//     answered zero or may not have answered at all — so the row records
//     which requested columns actually landed and answers by column
//     identity.
//   - BITS-valued objects decode to [snmp.BitSet], the set of positions
//     the agent reported, and the module gets one [snmp.BitPos] constant
//     per named bit (see emit_bits.go for how positions are recovered).
//
// Invoke from the repository root:
//
//	go run ./src/common/snmp/cmd/mibgen [-config <yaml>] [-out <dir>] [-pkg-prefix <importpath>] [-baseline <yaml>]
//	go run ./src/common/snmp/cmd/mibgen -verify   # load-only; no codegen
//	go run ./src/common/snmp/cmd/mibgen -check    # exit non-zero if regenerated output drifts
//	go run ./src/common/snmp/cmd/mibgen -update   # regenerate committed bindings
//	go run ./src/common/snmp/cmd/mibgen -refresh-baseline  # rewrite the diagnostic baseline
//
// The configuration file (default mibgen.yaml, at the repository root)
// declares MIB search paths, the module list with optional cross-authority
// depends_on edges, and per-OID Go-type overrides. The repository-root
// generate.go carries the go:generate directive, so `go generate .` at
// the root regenerates the committed bindings.
//
// # The diagnostic baseline
//
// The parser grades what a MIB is wrong about and carries on, so every
// configured module renders with a tail of diagnostics behind it.
// mibgen-baseline.yaml, beside the config, records that tail: generation
// fails on a diagnostic the file does not hold, and never on one it
// does. An entry is keyed on the diagnostic code and the declaration it
// landed in, so an upstream re-sync that moves a line leaves the record
// intact. See baseline.go for the file's shape and the two-tier gate.
//
// The emitter lives in emit.go (and per-shape companions emit_scalar.go,
// emit_table.go, emit_enum.go, emit_bits.go, emit_tc.go,
// emit_dispatch.go); the CLI
// entrypoint is in main.go.
//
// # Refreshing golden test fixtures
//
// After an intentional emitter change, refresh the committed golden
// fixture under testdata/golden/ with:
//
//	go test ./src/common/snmp/cmd/mibgen -run TestEmit_FakeMIB_Golden -update-golden
package main
