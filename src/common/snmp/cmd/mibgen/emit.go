package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"go.aledante.io/ae"
	"golang.org/x/tools/imports"
	"mvdan.cc/gofumpt/format"

	"github.com/dave/jennifer/jen"
	"github.com/sleepinggenius2/gosmi"
	gosmitypes "github.com/sleepinggenius2/gosmi/types"
)

// snmpImport is the import path for the FlowSeer SNMP runtime that
// every generated package consumes. Kept as a const so that a future
// rename of the host package surfaces as a single-site change.
const snmpImport = "go.aledante.io/FlowSeer/src/common/snmp"

// aeImport is the import path for go.aledante.io/ae, the error
// construction primitive every generated package uses for non-wrap
// errors (generated code never calls raw fmt.Errorf).
const aeImport = "go.aledante.io/ae"

const generatedGoVersion = "go1.26"

// Emit generates Go bindings for every module in cfg and writes one
// `mib.go` per module under outDir/<package>/. pkgPrefix is the Go
// import-path prefix used for cross-package qualified references (e.g.
// when one MIB's emitted enum is referenced from another generated
// package). cfg must have been validated; modules must already be
// loaded into the gosmi global state via [LoadModules].
//
// Emit is reentrant in the sense that subsequent calls overwrite the
// per-module mib.go in-place. Existing files outside of mib.go are
// preserved (so hand-maintained helpers can live next to generated
// code, though FlowSeer does not currently use that affordance).
//
// On any module-level failure Emit returns a wrapped error and stops
// — output files written before the failure remain on disk; the
// caller is responsible for deciding whether to roll those back.
func Emit(cfg *Config, outDir, pkgPrefix string) error {
	if cfg == nil {
		return ae.Msg("Emit called with nil config")
	}
	if outDir == "" {
		return ae.Msg("Emit called with empty outDir")
	}

	// Build a name → Module map so EmitModule can look up overrides
	// and the configured package name without a linear scan.
	cfgByName := make(map[string]Module, len(cfg.Modules))
	for _, m := range cfg.Modules {
		cfgByName[m.Name] = m
	}

	for _, cm := range cfg.Modules {
		mod, err := gosmi.GetModule(cm.Name)
		if err != nil {
			return ae.Wrapf("emit: module %q", err, cm.Name)
		}
		if err := EmitModule(&mod, cm, cfgByName, outDir, pkgPrefix); err != nil {
			return ae.Wrapf("emit: module %q", err, cm.Name)
		}
	}
	return nil
}

// EmitModule generates the mib.go for a single SmiModule and writes it
// under outDir/<package>/mib.go. It is exposed so tests can drive a
// single fixture module without round-tripping through the full config.
//
// cfgByName supplies the configured Module entries for every module
// loaded — EmitModule consults it to resolve cross-MIB references to
// the correct Go package name. The argument may be nil for the single-
// module case; cross-MIB references fall back to the source MIB's
// lowercased name.
func EmitModule(mod *gosmi.SmiModule, cm Module, cfgByName map[string]Module, outDir, pkgPrefix string) error {
	pkgDir := filepath.Join(outDir, cm.Package)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return ae.Wrapf("create package dir %s", err, pkgDir)
	}

	out, err := renderModule(mod, cm, cfgByName, pkgPrefix)
	if err != nil {
		return err
	}

	target := filepath.Join(pkgDir, "mib.go")
	if err := os.WriteFile(target, out, 0o644); err != nil {
		return ae.Wrapf("write %s", err, target)
	}
	return nil
}

// renderModule produces repository-format-clean source for a single module.
// It does not touch the filesystem; callers compose write or diff behavior
// on top.
func renderModule(mod *gosmi.SmiModule, cm Module, cfgByName map[string]Module, pkgPrefix string) ([]byte, error) {
	ec := newEmitCtx(mod, cm, cfgByName, pkgPrefix)

	// Pre-pass: discover the module's change indicators (structural
	// rules plus config-declared overrides). The result feeds the
	// emit-time gating for ColumnTiers and the indicator-var emission.
	ec.tableIndicators = discoverIndicators(ec, mod)
	ec.hasIndicator = len(ec.tableIndicators) > 0
	ec.tableIndicatorsByOID = make(map[string]struct{}, len(ec.tableIndicators))
	for _, ti := range ec.tableIndicators {
		ec.tableIndicatorsByOID[oidString(ti.Table.Oid)] = struct{}{}
	}

	f := jen.NewFilePathName(pkgPrefix+"/"+cm.Package, cm.Package)
	writeHeader(f, mod, cm)

	// Sort nodes by OID for deterministic output regardless of gosmi's
	// internal iteration order. We then partition by NodeKind so the
	// emitted file groups enums, scalars, columns, and tables together.
	nodes := mod.GetNodes()
	sort.SliceStable(nodes, func(i, j int) bool { return oidLess(nodes[i].Oid, nodes[j].Oid) })

	// Pass 1: collect typed enums (named INTEGER {…} types) from the
	// module's reusable type list and from individual nodes that
	// declare an inline enum. The emitter de-duplicates by Go type
	// name so multiple scalars referencing the same enum produce one
	// declaration.
	emitEnums(f, ec, mod, nodes)

	// Pass 2: scalars (read-accessible OBJECT-TYPEs with NodeScalar).
	for _, n := range nodes {
		if n.Kind != gosmitypes.NodeScalar {
			continue
		}
		emitScalar(f, ec, n)
	}

	// Pass 3: tables. emitTable also walks the table's row node and
	// emits each column's Column[T] value and registers the column in
	// the dispatch map. emitTable also records per-column tier
	// classifications for the ColumnTiers map.
	for _, n := range nodes {
		if n.Kind != gosmitypes.NodeTable {
			continue
		}
		emitTable(f, ec, n)
	}

	// Tail: per-package OID dispatch map, ColumnTiers map (gated
	// on hasIndicator), and ChangeIndicator vars.
	emitDispatch(f, ec)
	emitTierMap(f, ec)
	emitIndicators(f, ec)

	// Render to a buffer, then apply the same formatters enforced by the
	// repository. Jennifer owns import discovery but only guarantees gofmt-
	// equivalent output; goimports groups imports and gofumpt applies the
	// repository's stricter source normalization.
	var buf bytes.Buffer
	if err := f.Render(&buf); err != nil {
		return nil, ae.Wrap("render", err)
	}
	withImports, err := imports.Process(cm.Package+"/mib.go", buf.Bytes(), &imports.Options{
		Comments:   true,
		TabIndent:  true,
		TabWidth:   8,
		FormatOnly: true,
	})
	if err != nil {
		return nil, ae.Wrap("format imports", err)
	}
	formatted, err := format.Source(withImports, format.Options{
		LangVersion: generatedGoVersion,
		ModulePath:  "go.aledante.io/FlowSeer",
	})
	if err != nil {
		return nil, ae.Wrap("format source", err)
	}
	return formatted, nil
}

// writeHeader writes the file's leading `Code generated by` banner. The banner names the source MIB, its on-disk path (relative
// to the repo root when possible), and the SHA-256 of the source so
// regeneration drift is reviewable.
//
// The header uses the single-line `//`-comment form matching the proto
// pipeline precedent (`generated/go/proto/.../*.pb.go`) — `jen.HeaderComment`
// renders each newline-separated line as its own `//` comment.
func writeHeader(f *jen.File, mod *gosmi.SmiModule, cm Module) {
	sourcePath := mod.Path
	relPath := sourcePath
	if cwd, err := os.Getwd(); err == nil {
		if r, err := filepath.Rel(cwd, sourcePath); err == nil && !strings.HasPrefix(r, "..") {
			relPath = r
		}
	}

	hash := "unavailable"
	if b, err := os.ReadFile(sourcePath); err == nil {
		sum := sha256.Sum256(b)
		hash = hex.EncodeToString(sum[:])
	}

	// Pre-format the header as a block of `//` lines so jennifer's
	// Comment renderer emits it verbatim — matching the proto pipeline
	// precedent (generated/go/proto/.../*.pb.go) instead of jennifer's
	// default `/* … */` multiline form.
	header := fmt.Sprintf(
		"// Code generated by mibgen; DO NOT EDIT.\n"+
			"//\n"+
			"// Source MIB:    %s\n"+
			"// Source path:   %s\n"+
			"// Source SHA-256: %s\n"+
			"//\n"+
			"// Regenerate with `go generate ./...` or `go tool mibgen`.",
		cm.Name, relPath, hash,
	)
	f.HeaderComment(header)
}

// newOIDCall produces a `snmp.MustOID(<subs>...)` call expression from
// a dotted-decimal OID string. The string is the mibgen-internal form
// produced by [oidString]; each component is rendered as an untyped
// integer constant which Go implicitly converts to uint32 at the call
// site (MustOID's parameter type pins the conversion). An empty input
// returns `snmp.MustOID()` which evaluates to the zero OID — generator-
// internal sites that emit this should never do so because an empty
// OID is treated as a generator bug at the callsite.
//
// MustOID (not NewOID) is the codegen-facing companion: a malformed
// generated OID panics at package init rather than silently producing
// a structurally-invalid value that propagates to wire IO.
func newOIDCall(oidStr string) *jen.Statement {
	if oidStr == "" {
		return jen.Qual(snmpImport, "MustOID").Call()
	}
	parts := strings.Split(oidStr, ".")
	args := make([]jen.Code, 0, len(parts))
	for _, p := range parts {
		v, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			// The OID strings reaching this helper come from
			// oidString(gosmi.SmiNode.Oid), which is already a sequence
			// of uint32 values. A parse failure here would be a
			// generator bug; emit a clearly-broken literal so the
			// generator output trips compilation instead of silently
			// rendering bad code.
			args = append(args, jen.Lit(p))
			continue
		}
		// Rendering the literal as a plain int keeps the generated
		// source readable (snmp.MustOID(1, 3, 6, 1, ...)) rather than
		// the typed-conversion form (uint32(0x1), uint32(0x3), ...).
		// Untyped int constants assign to uint32 implicitly when the
		// value fits, which is guaranteed here because v already
		// fit ParseUint(10, 32).
		args = append(args, jen.Lit(int(v)))
	}
	return jen.Qual(snmpImport, "MustOID").Call(args...)
}

// runCheck regenerates every module into a tmpdir and compares each
// `mib.go` byte-for-byte to the committed file under outDir. Returns
// a non-nil error on any drift. The caller is expected to map that
// error to a non-zero exit code.
func runCheck(cfg *Config, outDir, pkgPrefix string) error {
	tmp, err := os.MkdirTemp("", "mibgen-check-*")
	if err != nil {
		return ae.Wrap("create tmpdir", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	if err := Emit(cfg, tmp, pkgPrefix); err != nil {
		return err
	}

	var drift []string
	for _, m := range cfg.Modules {
		got, err := os.ReadFile(filepath.Join(tmp, m.Package, "mib.go"))
		if err != nil {
			return ae.Wrapf("read regenerated module %q", err, m.Name)
		}
		want, err := os.ReadFile(filepath.Join(outDir, m.Package, "mib.go"))
		if err != nil {
			drift = append(drift, fmt.Sprintf("%s: %v", m.Name, err))
			continue
		}
		if !bytes.Equal(got, want) {
			drift = append(drift, fmt.Sprintf("%s: drift", m.Name))
		}
	}
	if len(drift) > 0 {
		return ae.Msgf("check: drift in %d module(s):\n  %s", len(drift), strings.Join(drift, "\n  "))
	}
	return nil
}

// oidLess orders two gosmi Oid slices lexicographically by sub-id.
func oidLess(a, b gosmitypes.Oid) bool {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// oidString renders a gosmi Oid as the dotted-decimal string expected
// by [snmp.ParseOID]. Returns "" for an empty OID, which the generator
// treats as a generator bug at the callsite.
func oidString(o gosmitypes.Oid) string {
	if len(o) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, v := range o {
		if i > 0 {
			sb.WriteByte('.')
		}
		fmt.Fprintf(&sb, "%d", v)
	}
	return sb.String()
}
