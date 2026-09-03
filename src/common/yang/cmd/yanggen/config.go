package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Config is the parsed yanggen.yaml file.
//
// Unlike mibgen's per-module manifest, yanggen lists vendors: each
// vendor entry includes every .yang file under its paths, subtracts an
// explicit skip-list (each skip carrying a reason), and may override the Go package name
// where the deterministic mangling collides. Strict decoding is
// enforced at load time; unknown keys are an error.
type Config struct {
	// Vendors is the ordered list of vendored YANG trees to generate
	// bindings for. Order is the emission order; it does not affect
	// module resolution, which is per-vendor.
	Vendors []Vendor `yaml:"vendors"`
}

// Vendor is one vendored YANG tree: a name (the directory the
// vendor's generated packages live under) and the source directories
// holding its modules.
type Vendor struct {
	// Name is the output directory segment for the vendor's generated
	// packages (generated/go/yang/<name>/...). Lowercase letters,
	// digits, and '-' only.
	Name string `yaml:"name"`

	// Paths is the ordered list of directories scanned recursively
	// for .yang files. Relative paths are resolved against the
	// directory containing the YAML file.
	Paths []string `yaml:"paths"`

	// Skip lists modules excluded from generation. Every entry names
	// the module and the reason it is skipped, so the manifest records
	// why a vendored module is not part of the generated surface.
	Skip []Skip `yaml:"skip,omitempty"`

	// PackageOverrides renames the derived Go package for specific
	// modules, the escape hatch for package-name collisions the
	// deterministic mangling cannot avoid.
	PackageOverrides []PackageOverride `yaml:"package_overrides,omitempty"`
}

// Skip is one skipped module (or glob of modules) with its mandatory
// reason. Exactly one of Module or Pattern is set: Module names one
// module; Pattern is a [path.Match] glob over module names for
// families sharing one exclusion reason (e.g. a vendor's per-platform
// "*-deviation" bundles). A pattern matching nothing on disk is an
// error, so stale skips surface.
type Skip struct {
	Module  string `yaml:"module,omitempty"`
	Pattern string `yaml:"pattern,omitempty"`
	Reason  string `yaml:"reason"`
}

// PackageOverride assigns an explicit Go package name to one module.
type PackageOverride struct {
	Module  string `yaml:"module"`
	Package string `yaml:"package"`
}

// ConfigError reports a single, structured config-validation failure,
// mirroring mibgen's shape. Path is the on-disk config file (or
// "<bytes>" for in-memory loads); Vendor is the offending vendor name
// when applicable.
type ConfigError struct {
	Path    string
	Vendor  string
	Issue   string
	wrapped error
}

// Error returns a one-line diagnostic. Vendor is interpolated when set.
func (e *ConfigError) Error() string {
	if e.Vendor != "" {
		return fmt.Sprintf("config %s: vendor %q: %s", e.Path, e.Vendor, e.Issue)
	}
	return fmt.Sprintf("config %s: %s", e.Path, e.Issue)
}

// Unwrap returns the wrapped cause so callers can use errors.Is/As.
func (e *ConfigError) Unwrap() error { return e.wrapped }

// vendorNameRE and packageNameRE pin the shapes validation enforces:
// vendor names become directory segments, package names become Go
// package identifiers.
var (
	vendorNameRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	packageNameRE = regexp.MustCompile(`^[a-z][a-z0-9]*$`)
)

// LoadConfig reads and validates a yanggen YAML config from disk.
// Relative vendor paths are resolved against the directory of path,
// so a config committed alongside spec/yang stays portable; the
// result's paths are always absolute.
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

// LoadConfigBytes parses raw YAML bytes as a yanggen config. basePath
// anchors relative path resolution and prefixes diagnostics; tests
// pass an arbitrary basePath.
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
			cfg = Config{}
		} else {
			return nil, &ConfigError{Path: displayPath, Issue: "parse YAML: " + err.Error(), wrapped: err}
		}
	}

	baseDir := filepath.Dir(basePath)
	if basePath == "" {
		baseDir = "."
	}
	for vi := range cfg.Vendors {
		for pi, p := range cfg.Vendors[vi].Paths {
			if p == "" {
				return nil, &ConfigError{
					Path: displayPath, Vendor: cfg.Vendors[vi].Name,
					Issue: fmt.Sprintf("paths[%d] is empty", pi),
				}
			}
			if !filepath.IsAbs(p) {
				cfg.Vendors[vi].Paths[pi] = filepath.Clean(filepath.Join(baseDir, p))
			} else {
				cfg.Vendors[vi].Paths[pi] = filepath.Clean(p)
			}
		}
	}

	if err := validateConfig(&cfg, displayPath); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// validateConfig enforces the semantic rules documented on [Config],
// returning *ConfigError values.
func validateConfig(cfg *Config, displayPath string) error {
	if len(cfg.Vendors) == 0 {
		return &ConfigError{Path: displayPath, Issue: "vendors is empty; at least one vendor is required"}
	}

	seenVendor := make(map[string]struct{}, len(cfg.Vendors))
	for i, v := range cfg.Vendors {
		if v.Name == "" {
			return &ConfigError{Path: displayPath, Issue: fmt.Sprintf("vendors[%d].name is empty", i)}
		}
		if !vendorNameRE.MatchString(v.Name) {
			return &ConfigError{
				Path: displayPath, Vendor: v.Name,
				Issue: "name must be lowercase letters, digits, and '-'",
			}
		}
		if _, dup := seenVendor[v.Name]; dup {
			return &ConfigError{Path: displayPath, Vendor: v.Name, Issue: "duplicate vendor name"}
		}
		seenVendor[v.Name] = struct{}{}

		if len(v.Paths) == 0 {
			return &ConfigError{Path: displayPath, Vendor: v.Name, Issue: "paths is empty"}
		}

		seenSkip := make(map[string]struct{}, len(v.Skip))
		for j, s := range v.Skip {
			switch {
			case s.Module == "" && s.Pattern == "":
				return &ConfigError{Path: displayPath, Vendor: v.Name, Issue: fmt.Sprintf("skip[%d]: one of module or pattern is required", j)}
			case s.Module != "" && s.Pattern != "":
				return &ConfigError{Path: displayPath, Vendor: v.Name, Issue: fmt.Sprintf("skip[%d]: module and pattern are mutually exclusive", j)}
			case s.Pattern != "":
				if _, err := path.Match(s.Pattern, "probe"); err != nil {
					return &ConfigError{Path: displayPath, Vendor: v.Name, Issue: fmt.Sprintf("skip[%d]: malformed pattern %q", j, s.Pattern), wrapped: err}
				}
			}
			key := s.Module + "|" + s.Pattern
			if s.Reason == "" {
				return &ConfigError{
					Path: displayPath, Vendor: v.Name,
					Issue: fmt.Sprintf("skip[%d] (%s%s): reason is required — record why the module is excluded", j, s.Module, s.Pattern),
				}
			}
			if _, dup := seenSkip[key]; dup {
				return &ConfigError{Path: displayPath, Vendor: v.Name, Issue: fmt.Sprintf("duplicate skip entry %q", s.Module+s.Pattern)}
			}
			seenSkip[key] = struct{}{}
		}

		seenOverride := make(map[string]struct{}, len(v.PackageOverrides))
		seenPkg := make(map[string]struct{}, len(v.PackageOverrides))
		for j, o := range v.PackageOverrides {
			if o.Module == "" {
				return &ConfigError{Path: displayPath, Vendor: v.Name, Issue: fmt.Sprintf("package_overrides[%d].module is empty", j)}
			}
			if !packageNameRE.MatchString(o.Package) {
				return &ConfigError{
					Path: displayPath, Vendor: v.Name,
					Issue: fmt.Sprintf("package_overrides[%d] (%s): package %q is not a lowercase Go identifier", j, o.Module, o.Package),
				}
			}
			if _, dup := seenOverride[o.Module]; dup {
				return &ConfigError{Path: displayPath, Vendor: v.Name, Issue: fmt.Sprintf("duplicate package override for %q", o.Module)}
			}
			seenOverride[o.Module] = struct{}{}
			if _, dup := seenPkg[o.Package]; dup {
				return &ConfigError{Path: displayPath, Vendor: v.Name, Issue: fmt.Sprintf("package override name %q used twice", o.Package)}
			}
			seenPkg[o.Package] = struct{}{}
		}
	}

	// Verify each resolved path exists on disk after structural
	// validation, so structural errors keep their diagnostics.
	for _, v := range cfg.Vendors {
		for i, p := range v.Paths {
			info, statErr := os.Stat(p)
			if statErr != nil {
				return &ConfigError{
					Path: displayPath, Vendor: v.Name,
					Issue:   fmt.Sprintf("paths[%d]: %q does not exist on disk", i, p),
					wrapped: statErr,
				}
			}
			if !info.IsDir() {
				return &ConfigError{
					Path: displayPath, Vendor: v.Name,
					Issue: fmt.Sprintf("paths[%d]: %q is not a directory", i, p),
				}
			}
		}
	}

	return nil
}
