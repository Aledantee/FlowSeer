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
// Invoke via the Go tool directive declared in go.mod:
//
//	go tool mibgen [-config <yaml>] [-out <dir>] [-pkg-prefix <importpath>]
//	go tool mibgen -verify        # load-only; no codegen
//	go tool mibgen -check         # exit non-zero if regenerated output drifts
//	go tool mibgen -update        # regenerate committed bindings
//
// The configuration file (default src/common/snmp/cmd/mibgen/mibgen.yaml)
// declares MIB search paths, the module list with optional cross-authority
// depends_on edges, and per-OID Go-type overrides.
//
// The emitter lives in emit.go (and per-shape companions emit_scalar.go,
// emit_table.go, emit_enum.go, emit_tc.go, emit_dispatch.go); the CLI
// entrypoint is in main.go.
//
// # Refreshing golden test fixtures
//
// After an intentional emitter change, refresh the committed golden
// fixture under testdata/golden/ with:
//
//	go test ./src/common/snmp/cmd/mibgen -run TestEmit_FakeMIB_Golden -update-golden
package main
