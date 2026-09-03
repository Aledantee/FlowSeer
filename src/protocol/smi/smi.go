package smi

import (
	"runtime"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Options configure one load. The zero value is usable: it searches
// nothing but the paths a caller names and sizes the worker pool from
// the machine.
//
// Options is a plain struct rather than a builder or a set of functional
// options because a caller sets most of it or none of it, and because
// the call site should say what the load was configured with.
type Options struct {
	// SearchPaths are the directories a module name is looked for in, in
	// order. A module imported by something being loaded is looked for
	// under its own name, then under that name with a ".mib", ".txt",
	// ".MIB" or ".my" extension, which is how every MIB tree in the wild
	// spells the same thing.
	SearchPaths []string

	// Workers bounds how many files are read, framed and parsed at once.
	// Zero means [runtime.NumCPU]. Parallelism is across files and never
	// within one: a file's declarations are cheap next to the syscall
	// that fetched them, and the merge is by canonical order anyway, so
	// the worker count changes the wall clock and nothing else.
	Workers int
}

// workers returns the pool size a load runs with.
func (o Options) workers() int {
	if o.Workers > 0 {
		return o.Workers
	}

	return runtime.NumCPU()
}

// errNoModules is returned when a load is asked for nothing. It is an
// error rather than an empty set because a caller that named no module
// has a bug in the layer above, and an empty ModuleSet would let it run.
var errNoModules = errs.Msg("smi: no modules to load")

// Load reads the named modules from the search paths, follows their
// IMPORTS, and resolves everything it found into one module set.
//
// A module name is looked for in each of [Options.SearchPaths] in turn.
// Load returns an error when a name the caller asked for is not there,
// because that is the caller's mistake; a module that only some other
// module's IMPORTS asked for is reported as a diagnostic instead, since
// a corpus is full of imports nobody can satisfy and refusing the whole
// load over one of them would be useless.
//
// Load holds no state between calls. Two loads of the same files return
// equal, independent module sets, and neither can observe the other.
func Load(modules []string, opts Options) (*ModuleSet, error) {
	if len(modules) == 0 {
		return nil, errNoModules
	}

	paths := make([]string, 0, len(modules))
	for _, name := range modules {
		path, ok := findModule(name, opts.SearchPaths)
		if !ok {
			return nil, errs.New().Attr("module", name).Attr("search_paths", opts.SearchPaths).
				Msg("smi: module not found on any search path")
		}
		paths = append(paths, path)
	}

	return LoadFiles(paths, opts)
}

// LoadFiles reads the named files, follows the IMPORTS of every module
// they hold through [Options.SearchPaths], and resolves the result.
//
// The order of paths does not reach the result. Modules come back sorted
// by name, declarations in the order their source writes them, and
// diagnostics by file and offset, so two calls that name the same files
// in different orders return the same set.
//
// LoadFiles returns an error only when a file the caller named cannot be
// read. Everything a MIB can be wrong about is a diagnostic.
func LoadFiles(paths []string, opts Options) (*ModuleSet, error) {
	if len(paths) == 0 {
		return nil, errNoModules
	}

	sources, err := readAll(paths, opts)
	if err != nil {
		return nil, err
	}

	return resolve(sources), nil
}
