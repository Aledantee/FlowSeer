package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/imports"
	"mvdan.cc/gofumpt/format"

	"github.com/dave/jennifer/jen"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/smi"
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
// package). cfg must have been validated and set must be the resolved
// model [LoadModules] returned for it.
//
// Emit is reentrant in the sense that subsequent calls overwrite the
// per-module mib.go in-place. Existing files outside of mib.go are
// preserved (so hand-maintained helpers can live next to generated
// code, though FlowSeer does not currently use that affordance).
//
// On any module-level failure Emit returns a wrapped error and stops
// — output files written before the failure remain on disk; the
// caller is responsible for deciding whether to roll those back.
func Emit(cfg *Config, set *smi.ModuleSet, outDir, pkgPrefix string) error {
	if cfg == nil {
		return errs.Msg("Emit called with nil config")
	}
	if set == nil {
		return errs.Msg("Emit called with nil module set")
	}
	if outDir == "" {
		return errs.Msg("Emit called with empty outDir")
	}

	// Build a name → Module map so EmitModule can look up overrides
	// and the configured package name without a linear scan.
	cfgByName := make(map[string]Module, len(cfg.Modules))
	for _, m := range cfg.Modules {
		cfgByName[m.Name] = m
	}

	for _, cm := range cfg.Modules {
		mod, ok := set.Module(cm.Name)
		if !ok {
			return errs.Msgf("emit: module %q is not in the resolved set", cm.Name)
		}
		if err := EmitModule(mod, set, cm, cfgByName, outDir, pkgPrefix); err != nil {
			return errs.Wrapf(err, "emit: module %q", cm.Name)
		}
	}
	return nil
}

// EmitModule generates the mib.go for a single resolved module and writes it
// under outDir/<package>/mib.go. It is exposed so tests can drive a
// single fixture module without round-tripping through the full config.
//
// cfgByName supplies the configured Module entries for every module
// loaded — EmitModule consults it to resolve cross-MIB references to
// the correct Go package name. The argument may be nil for the single-
// module case; cross-MIB references fall back to the source MIB's
// lowercased name.
func EmitModule(
	mod *smi.Module, set *smi.ModuleSet, cm Module, cfgByName map[string]Module, outDir, pkgPrefix string,
) error {
	pkgDir := filepath.Join(outDir, cm.Package)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return errs.Wrapf(err, "create package dir %s", pkgDir)
	}

	out, err := renderModule(mod, set, cm, cfgByName, pkgPrefix)
	if err != nil {
		return err
	}

	target := filepath.Join(pkgDir, "mib.go")
	if err := os.WriteFile(target, out, 0o644); err != nil {
		return errs.Wrapf(err, "write %s", target)
	}
	return nil
}

// renderModule produces repository-format-clean source for a single module.
// It does not touch the filesystem; callers compose write or diff behavior
// on top.
func renderModule(
	mod *smi.Module, set *smi.ModuleSet, cm Module, cfgByName map[string]Module, pkgPrefix string,
) ([]byte, error) {
	if err := checkSourceNames(mod); err != nil {
		return nil, err
	}

	ec := newEmitCtx(mod, set, cm, cfgByName, pkgPrefix)

	// Pre-pass: discover the module's change indicators (structural
	// rules plus config-declared overrides). The result feeds the
	// emit-time gating for ColumnTiers and the indicator-var emission.
	ec.tableIndicators = discoverIndicators(ec, mod)
	ec.hasIndicator = len(ec.tableIndicators) > 0
	ec.tableIndicatorsByOID = make(map[string]struct{}, len(ec.tableIndicators))
	for _, ti := range ec.tableIndicators {
		ec.tableIndicatorsByOID[ti.Table.OID.String()] = struct{}{}
	}

	f := jen.NewFilePathName(pkgPrefix+"/"+cm.Package, cm.Package)
	writeHeader(f, mod, cm)

	// Sort nodes by OID so the emitted file reads in walk order rather
	// than in the order the MIB happens to declare things. We then
	// partition by NodeKind so the file groups enums, scalars, columns
	// and tables together.
	nodes := slices.Clone(mod.Nodes)
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].OID.Compare(nodes[j].OID) < 0 })

	// Pass 1: collect typed enums (named INTEGER {…} types) from the
	// module's reusable type list and from individual nodes that
	// declare an inline enum. The emitter de-duplicates by Go type
	// name so multiple scalars referencing the same enum produce one
	// declaration.
	emitEnums(f, ec, mod, nodes)

	// Pass 2: scalars (read-accessible OBJECT-TYPEs with NodeScalar).
	for _, n := range nodes {
		if n.Kind != smi.NodeScalar {
			continue
		}
		emitScalar(f, ec, n)
	}

	// Pass 3: tables. emitTable also walks the table's row node and
	// emits each column's Column[T] value and registers the column in
	// the dispatch map. emitTable also records per-column tier
	// classifications for the ColumnTiers map.
	for _, n := range nodes {
		if n.Kind != smi.NodeTable {
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
		return nil, errs.Wrap(err, "render")
	}
	withImports, err := imports.Process(cm.Package+"/mib.go", buf.Bytes(), &imports.Options{
		Comments:   true,
		TabIndent:  true,
		TabWidth:   8,
		FormatOnly: true,
	})
	if err != nil {
		return nil, errs.Wrap(err, "format imports")
	}
	formatted, err := format.Source(withImports, format.Options{
		LangVersion: generatedGoVersion,
		ModulePath:  "go.aledante.io/FlowSeer",
	})
	if err != nil {
		return nil, errs.Wrap(err, "format source")
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
func writeHeader(f *jen.File, mod *smi.Module, cm Module) {
	sourcePath := mod.File
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
			"// Regenerate with `go generate .` at the repository root.",
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
			// smi.OID.String(), which is already a sequence of uint32
			// values. A parse failure here would be a
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
func runCheck(cfg *Config, set *smi.ModuleSet, outDir, pkgPrefix string) error {
	tmp, err := os.MkdirTemp("", "mibgen-check-*")
	if err != nil {
		return errs.Wrap(err, "create tmpdir")
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	if err := Emit(cfg, set, tmp, pkgPrefix); err != nil {
		return err
	}

	var drift []string
	for _, m := range cfg.Modules {
		got, err := os.ReadFile(filepath.Join(tmp, m.Package, "mib.go"))
		if err != nil {
			return errs.Wrapf(err, "read regenerated module %q", m.Name)
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
		return errs.Msgf("check: drift in %d module(s):\n  %s", len(drift), strings.Join(drift, "\n  "))
	}
	return nil
}

// sourceNameRunes is the character set a MIB descriptor may draw on.
//
// RFC 2578 §3.1 allows letters, digits and hyphens; underscore is here
// because vendor MIBs write it and the corpus is what this generator
// reads. Everything else is refused rather than silently dropped by
// [camelCase], because a name that loses a character is a Go identifier
// that no longer says which object it came from — and two names that
// differ only in the dropped character would collide.
func sourceNameOK(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}

	return true
}

// checkSourceNames refuses a module holding a name that cannot become a
// Go identifier without losing information. It runs before any
// emission, so the refusal costs a build rather than a file that
// compiles and misnames what it binds.
func checkSourceNames(mod *smi.Module) error {
	var bad []string
	report := func(kind, name string) {
		if !sourceNameOK(name) {
			bad = append(bad, kind+" "+strconv.Quote(name))
		}
	}

	report("module", mod.Name)
	for _, n := range mod.Nodes {
		report("object", n.Name)
	}
	for _, t := range mod.Types {
		report("type", t.Name)
		for _, m := range t.Members {
			report("member", m.Name)
		}
	}
	for _, n := range mod.Nodes {
		if n.Type == nil || n.Type.Name != "" {
			continue
		}
		for _, m := range n.Type.Members {
			report("member", m.Name)
		}
	}

	if len(bad) == 0 {
		return nil
	}

	return errs.Msgf("module %q: %d source name(s) outside the descriptor character set: %s",
		mod.Name, len(bad), strings.Join(bad, ", "))
}
