package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/snmp"
)

// Config is the parsed mibgen.yaml file.
//
// Field tags mirror the on-disk YAML schema. Strict decoding is enforced
// at load time via [LoadConfig]/[LoadConfigBytes]; unknown keys are an
// error, not a warning, so typos in checked-in config surface immediately.
type Config struct {
	// SearchPaths is the ordered list of directories handed to the gosmi
	// loader (via AppendPath, in order). Relative paths are resolved
	// against the directory that contained the YAML file.
	SearchPaths []string `yaml:"search_paths"`

	// Modules is the ordered list of MIB modules to load and emit Go
	// bindings for. Order is significant only as a stable
	// tiebreaker when topologically sorting [Module.DependsOn].
	Modules []Module `yaml:"modules"`
}

// Module is a single MIB module entry in the config.
type Module struct {
	// Name is the canonical MIB module name (e.g. "IF-MIB"). It must
	// match the MODULE-IDENTITY name declared inside the MIB file and
	// (for the bundled IETF MIBs) the filename on disk.
	Name string `yaml:"name"`

	// Package is the Go package name for the emitted bindings. Must be
	// a valid lowercase Go identifier and unique across the config.
	Package string `yaml:"package"`

	// DependsOn lists other module names this module depends on across
	// search-path / authority boundaries. Same-search-path transitive
	// IMPORTS are resolved by gosmi and need not appear here.
	DependsOn []string `yaml:"depends_on,omitempty"`

	// Overrides is the per-OID Go-type override table for this module.
	// Applied at emission time; validated here.
	Overrides []Override `yaml:"overrides,omitempty"`

	// Indicators is the per-module list of ChangeIndicator
	// declarations that augment mibgen's structural discovery.
	// Required for the cases the structural rules
	// cannot reach: cross-subtree scalars, multi-table-sibling
	// scalars, and cross-table-coverage scalars.
	Indicators []IndicatorDecl `yaml:"indicators,omitempty"`
}

// IndicatorDecl declares a single change indicator for a table the
// structural discovery rules cannot bind. Two
// shapes are supported:
//
//   - Scalar indicator covering one or more tables: set ScalarOID +
//     CoversTables. The indicator scalar's OID drives the per-tick
//     probe; each table in CoversTables produces its own generated
//     <Table>Indicator var referencing this scalar (single-table
//     Coverage per emitted var).
//
//   - Per-row column override: set ColumnOID + TableOID. Rare —
//     structural per-row discovery (column whose name matches the
//     indicator-suffix heuristic) covers the common case. Override
//     when the column does NOT match the heuristic but the MIB
//     author has documented it as the change indicator.
//
// The two shapes are mutually exclusive within a single declaration.
type IndicatorDecl struct {
	// ScalarOID is the dotted-decimal OID of the scalar that the
	// Watcher probes each tick. Mutually exclusive with ColumnOID.
	ScalarOID string `yaml:"scalar_oid,omitempty"`

	// CoversTables is the dotted-decimal OID list of the tables this
	// scalar covers. Required together with ScalarOID.
	CoversTables []string `yaml:"covers_tables,omitempty"`

	// ColumnOID is the dotted-decimal OID of a per-row indicator
	// column that overrides the structural heuristic. Mutually
	// exclusive with ScalarOID.
	ColumnOID string `yaml:"column_oid,omitempty"`

	// TableOID is the dotted-decimal OID of the table ColumnOID
	// indicates. Required together with ColumnOID.
	TableOID string `yaml:"table_oid,omitempty"`
}

// Override redirects a single object's emitted Go type to a developer-
// supplied named type, optionally imported from another package.
type Override struct {
	// OID is the dotted-decimal object identifier of the column or
	// scalar to override. Must parse via [snmp.ParseOID].
	OID string `yaml:"oid"`

	// GoType is the Go type name to use in place of the default
	// emission. May be a bare identifier or a qualified selector
	// (e.g. "ifmib.IfAdminStatus"); the emitter interprets it together
	// with [Override.Import].
	GoType string `yaml:"go_type"`

	// Import is the optional import path that supplies [Override.GoType].
	// Empty means the type is defined in the same generated package.
	Import string `yaml:"import,omitempty"`
}

// ConfigError reports a single, structured config-validation failure.
// Path is the on-disk config file (or "<bytes>" for in-memory loads);
// Module is the offending module name when applicable; Issue is a
// human-readable description. ConfigError implements [error] and wraps
// an optional underlying cause.
type ConfigError struct {
	Path    string
	Module  string
	Issue   string
	wrapped error
}

// Error returns a one-line diagnostic. Module is interpolated when set.
func (e *ConfigError) Error() string {
	switch {
	case e.Module != "":
		return fmt.Sprintf("config %s: module %q: %s", e.Path, e.Module, e.Issue)
	default:
		return fmt.Sprintf("config %s: %s", e.Path, e.Issue)
	}
}

// Unwrap returns the wrapped cause so callers can use errors.Is/As.
func (e *ConfigError) Unwrap() error { return e.wrapped }

// LoadConfig reads and validates a mibgen YAML config from disk.
//
// Relative search_paths in the file are resolved against the directory
// of path (so a config committed alongside MIB directories stays
// portable). The result's SearchPaths are always absolute.
func LoadConfig(path string) (*Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, errs.Wrapf(err, "config %s: resolve absolute path", path)
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		// Preserve os.ErrNotExist so callers can errors.Is it.
		return nil, errs.Wrapf(err, "config %s", abs)
	}
	return LoadConfigBytes(b, abs)
}

// LoadConfigBytes parses raw YAML bytes as a mibgen config. basePath is
// the on-disk path the bytes nominally came from; it is used as the
// directory anchor for resolving relative search_paths and as the
// diagnostic prefix in errors. Tests can pass an arbitrary basePath.
func LoadConfigBytes(b []byte, basePath string) (*Config, error) {
	displayPath := basePath
	if displayPath == "" {
		displayPath = "<bytes>"
	}

	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			// Empty input — treat as a syntactically empty Config and
			// fall through to validation, which rejects an empty
			// search_paths list with a clear message.
			cfg = Config{}
		} else {
			return nil, &ConfigError{Path: displayPath, Issue: "parse YAML: " + err.Error(), wrapped: err}
		}
	}

	// Resolve search_paths relative to the config file's directory.
	baseDir := filepath.Dir(basePath)
	if basePath == "" {
		baseDir = "."
	}
	for i, p := range cfg.SearchPaths {
		if p == "" {
			return nil, &ConfigError{Path: displayPath, Issue: fmt.Sprintf("search_paths[%d] is empty", i)}
		}
		if !filepath.IsAbs(p) {
			cfg.SearchPaths[i] = filepath.Clean(filepath.Join(baseDir, p))
		} else {
			cfg.SearchPaths[i] = filepath.Clean(p)
		}
	}

	if err := validateConfig(&cfg, displayPath); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// validateConfig enforces the semantic rules documented on [Config].
// Errors are returned as *ConfigError values.
func validateConfig(cfg *Config, displayPath string) error {
	if len(cfg.SearchPaths) == 0 {
		return &ConfigError{Path: displayPath, Issue: "search_paths is empty; at least one MIB search path is required"}
	}

	seenName := make(map[string]struct{}, len(cfg.Modules))
	seenPkg := make(map[string]struct{}, len(cfg.Modules))
	for i, m := range cfg.Modules {
		if m.Name == "" {
			return &ConfigError{Path: displayPath, Issue: fmt.Sprintf("modules[%d].name is empty", i)}
		}
		if m.Package == "" {
			return &ConfigError{Path: displayPath, Module: m.Name, Issue: "package is empty"}
		}
		if _, dup := seenName[m.Name]; dup {
			return &ConfigError{Path: displayPath, Module: m.Name, Issue: "duplicate module name"}
		}
		seenName[m.Name] = struct{}{}
		if _, dup := seenPkg[m.Package]; dup {
			return &ConfigError{Path: displayPath, Module: m.Name, Issue: fmt.Sprintf("duplicate package %q", m.Package)}
		}
		seenPkg[m.Package] = struct{}{}

		for j, ov := range m.Overrides {
			if ov.OID == "" {
				return &ConfigError{Path: displayPath, Module: m.Name, Issue: fmt.Sprintf("overrides[%d]: oid is empty", j)}
			}
			if ov.GoType == "" {
				return &ConfigError{Path: displayPath, Module: m.Name, Issue: fmt.Sprintf("overrides[%d] (oid %s): go_type is empty", j, ov.OID)}
			}
			if _, err := snmp.ParseOID(ov.OID); err != nil {
				return &ConfigError{
					Path:    displayPath,
					Module:  m.Name,
					Issue:   fmt.Sprintf("overrides[%d]: malformed oid %q: %s", j, ov.OID, err.Error()),
					wrapped: err,
				}
			}
		}

		for j, ind := range m.Indicators {
			scalarSet := ind.ScalarOID != ""
			columnSet := ind.ColumnOID != ""
			switch {
			case scalarSet && columnSet:
				return &ConfigError{
					Path: displayPath, Module: m.Name,
					Issue: fmt.Sprintf("indicators[%d]: scalar_oid and column_oid are mutually exclusive", j),
				}
			case !scalarSet && !columnSet:
				return &ConfigError{
					Path: displayPath, Module: m.Name,
					Issue: fmt.Sprintf("indicators[%d]: exactly one of scalar_oid or column_oid must be set", j),
				}
			case scalarSet:
				if _, err := snmp.ParseOID(ind.ScalarOID); err != nil {
					return &ConfigError{
						Path: displayPath, Module: m.Name,
						Issue:   fmt.Sprintf("indicators[%d]: malformed scalar_oid %q: %s", j, ind.ScalarOID, err.Error()),
						wrapped: err,
					}
				}
				if len(ind.CoversTables) == 0 {
					return &ConfigError{
						Path: displayPath, Module: m.Name,
						Issue: fmt.Sprintf("indicators[%d]: at least one covered table required for scalar indicator %s",
							j, ind.ScalarOID),
					}
				}
				for k, tbl := range ind.CoversTables {
					if tbl == "" {
						return &ConfigError{
							Path: displayPath, Module: m.Name,
							Issue: fmt.Sprintf("indicators[%d]: covers_tables[%d] is empty", j, k),
						}
					}
					if _, err := snmp.ParseOID(tbl); err != nil {
						return &ConfigError{
							Path: displayPath, Module: m.Name,
							Issue:   fmt.Sprintf("indicators[%d]: malformed covers_tables[%d] %q: %s", j, k, tbl, err.Error()),
							wrapped: err,
						}
					}
				}
			case columnSet:
				if _, err := snmp.ParseOID(ind.ColumnOID); err != nil {
					return &ConfigError{
						Path: displayPath, Module: m.Name,
						Issue:   fmt.Sprintf("indicators[%d]: malformed column_oid %q: %s", j, ind.ColumnOID, err.Error()),
						wrapped: err,
					}
				}
				if ind.TableOID == "" {
					return &ConfigError{
						Path: displayPath, Module: m.Name,
						Issue: fmt.Sprintf("indicators[%d]: table_oid is required with column_oid", j),
					}
				}
				if _, err := snmp.ParseOID(ind.TableOID); err != nil {
					return &ConfigError{
						Path: displayPath, Module: m.Name,
						Issue:   fmt.Sprintf("indicators[%d]: malformed table_oid %q: %s", j, ind.TableOID, err.Error()),
						wrapped: err,
					}
				}
			}
		}
	}

	// depends_on references must resolve. Validate after the name set is
	// known so the order modules are listed in does not matter.
	for _, m := range cfg.Modules {
		for _, dep := range m.DependsOn {
			if dep == "" {
				return &ConfigError{Path: displayPath, Module: m.Name, Issue: "depends_on contains an empty entry"}
			}
			if _, ok := seenName[dep]; !ok {
				return &ConfigError{
					Path:   displayPath,
					Module: m.Name,
					Issue:  fmt.Sprintf("depends_on references unknown module %q", dep),
				}
			}
		}
	}

	// Verify each resolved search path exists on disk. A typo or stale
	// vendored-spec layout would otherwise surface later as a confusing
	// gosmi "module not found"; fail fast with the exact offending path.
	// This check runs after the structural validation above so structural
	// errors keep their existing diagnostics.
	for i, p := range cfg.SearchPaths {
		info, statErr := os.Stat(p)
		if statErr != nil {
			return &ConfigError{
				Path:    displayPath,
				Issue:   fmt.Sprintf("search_paths[%d]: %q does not exist on disk", i, p),
				wrapped: statErr,
			}
		}
		if !info.IsDir() {
			return &ConfigError{
				Path:  displayPath,
				Issue: fmt.Sprintf("search_paths[%d]: %q is not a directory", i, p),
			}
		}
	}

	return nil
}
