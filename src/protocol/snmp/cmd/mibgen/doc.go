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
//   - Each row type carries its decoded INDEX as a comparable key struct
//     with one field per part, decoded through [snmp.DecodeIndexInto]; a
//     suffix that does not match the declared shape leaves the key zero
//     and KeyValid false without ending the walk. A textual convention
//     that solely indexes a table of its own module becomes a named key
//     type in that module's package, with a HomeTable method naming the
//     table, and every column of that type is a reference to a row of it
//     (see emit_key.go for the keyed-convention rule and the home-table
//     tiebreak). A reference to a key type whose module is not
//     configured is emitted in its base type and listed on stdout.
//
// One package is not per module: -out/sysobjectid/ holds every naming
// node the configured modules declare under the enterprises subtree,
// sorted by OID with one entry per OID, and a Lookup that resolves a
// sysObjectID to the deepest of them. It imports only the SNMP library,
// so a consumer that wants device identity alone links no per-module
// package (see emit_identity.go for the collection and trimming rules).
//
// Invoke from the repository root:
//
//	go run ./src/protocol/snmp/cmd/mibgen [-config <yaml>] [-out <dir>] [-pkg-prefix <importpath>] [-baseline <yaml>]
//	go run ./src/protocol/snmp/cmd/mibgen -verify   # load-only; no codegen
//	go run ./src/protocol/snmp/cmd/mibgen -check    # exit non-zero if regenerated output drifts
//	go run ./src/protocol/snmp/cmd/mibgen -update   # regenerate committed bindings
//	go run ./src/protocol/snmp/cmd/mibgen -refresh-baseline  # rewrite the diagnostic baseline
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
// emit_table.go, emit_key.go, emit_enum.go, emit_bits.go, emit_tc.go,
// emit_dispatch.go); the cross-module identity pass is emit_identity.go;
// the CLI entrypoint is in main.go.
//
// # Refreshing golden test fixtures
//
// After an intentional emitter change, refresh the committed golden
// fixtures under testdata/golden/ with:
//
//	go test ./src/protocol/snmp/cmd/mibgen -run 'TestEmit_FakeMIB_Golden|TestEmit_Identity_Golden' -update-golden
//
// The goldentest package compiles those fixtures and walks them against
// a scripted session, so run `go test ./src/protocol/snmp/cmd/mibgen/...`
// after refreshing.
package main
