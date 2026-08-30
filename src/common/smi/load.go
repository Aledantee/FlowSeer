package smi

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/smi/internal/frame"
	"go.aledante.io/FlowSeer/src/common/smi/internal/lex"
	"go.aledante.io/FlowSeer/src/common/smi/internal/parse"
)

// moduleFileExtensions are the spellings a module's file is looked for
// under, after the bare module name. MIB trees in the wild use all of
// them and several use more than one in the same directory.
var moduleFileExtensions = []string{"", ".mib", ".txt", ".MIB", ".my"}

// findModule returns the path a module's source is at.
func findModule(name string, searchPaths []string) (string, bool) {
	if name == "" {
		return "", false
	}

	for _, dir := range searchPaths {
		for _, ext := range moduleFileExtensions {
			path := filepath.Join(dir, name+ext)
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path, true
			}
		}
	}

	return "", false
}

// source is one file read, framed and parsed.
//
// Both the framed file and the parsed result are kept because they
// answer different questions: the parse carries the declarations, and
// the frames carry the IMPORTS lists, which are the one thing the AST
// has no node for.
type source struct {
	path   string
	frames *frame.File
	parsed *parse.Result
}

// rawImport is one FROM clause of an IMPORTS list, before anything is
// resolved.
type rawImport struct {
	module  string
	offset  int32
	symbols []string
}

// imports reads one framed module's IMPORTS lists.
//
// The AST has no import node — the parser's job ends at the declaration
// — so this reads the frames the framer already classified. An IMPORTS
// list is "sym, sym FROM MODULE sym FROM MODULE ;", which is a name run
// terminated by FROM and then the module the run came from.
func imports(fm frame.Module, src *lex.Result) []rawImport {
	var out []rawImport
	var pending []string

	for _, fr := range fm.Frames {
		if fr.Kind != frame.KindImports {
			continue
		}

		for i := 0; i < len(fr.Tokens); i++ {
			t := fr.Tokens[i]
			switch {
			case t.Kind == lex.KindKeyword && t.Keyword == lex.KeywordFrom:
				i++
				if i >= len(fr.Tokens) {
					break
				}
				out = append(out, rawImport{
					module:  src.Text(fr.Tokens[i]),
					offset:  fr.Tokens[i].Offset,
					symbols: pending,
				})
				pending = nil
			case t.Kind == lex.KindIdentifier || t.Kind == lex.KindTypeReference || t.Kind == lex.KindKeyword:
				pending = append(pending, src.Text(t))
			}
		}

		pending = nil
	}

	return out
}

// readAll reads the named files and everything their IMPORTS reach.
//
// It works in waves: a wave is read in parallel, the modules it defined
// and the imports they named are collected, and the next wave is the
// files those imports point at that nothing has read yet. A wave is
// dispatched longest file first, so the one file that takes the longest
// is not the one left running alone at the end.
//
// Only the files the caller named can fail the load. A file an import
// pointed at that cannot be read is left out, and resolution reports the
// module as missing at the IMPORTS clause that wanted it.
func readAll(paths []string, opts Options) ([]source, error) {
	seenPath := make(map[string]bool, len(paths))
	seenModule := make(map[string]bool, len(paths))

	wave := make([]string, 0, len(paths))
	for _, p := range paths {
		abs := canonicalPath(p)
		if seenPath[abs] {
			continue
		}
		seenPath[abs] = true
		wave = append(wave, p)
	}
	slices.Sort(wave)

	var (
		out   []source
		first = true
	)

	for len(wave) > 0 {
		read, err := readWave(wave, opts.workers())
		if err != nil && first {
			return nil, err
		}
		first = false

		var wanted []string
		for _, s := range read {
			if s.frames == nil {
				continue
			}
			out = append(out, s)

			for _, fm := range s.frames.Modules {
				seenModule[fm.Name] = true
			}
			for _, fm := range s.frames.Modules {
				for _, imp := range imports(fm, s.frames.Source) {
					wanted = append(wanted, imp.module)
				}
			}
		}

		wave = nextWave(wanted, seenModule, seenPath, opts.SearchPaths)
	}

	slices.SortStableFunc(out, func(a, b source) int { return strings.Compare(a.path, b.path) })

	return out, nil
}

// nextWave returns the files the imports just seen point at that nothing
// has read yet, sorted so the wave does not depend on the order the
// imports were found in.
func nextWave(wanted []string, seenModule, seenPath map[string]bool, searchPaths []string) []string {
	slices.Sort(wanted)
	wanted = slices.Compact(wanted)

	var wave []string
	for _, name := range wanted {
		if seenModule[name] {
			continue
		}
		// Mark it either way: a module no search path holds must not be
		// looked for once per importer.
		seenModule[name] = true

		path, ok := findModule(name, searchPaths)
		if !ok {
			continue
		}
		abs := canonicalPath(path)
		if seenPath[abs] {
			continue
		}
		seenPath[abs] = true
		wave = append(wave, path)
	}

	return wave
}

// readWave reads one wave in parallel and returns the sources in the
// order the wave named them, whatever order they finished in.
//
// The error is the first read failure in wave order, and the sources
// that did read are returned with it, so a caller that treats a failure
// as fatal and one that does not both have what they need.
func readWave(wave []string, workers int) ([]source, error) {
	sizes := make(map[string]int64, len(wave))
	for _, p := range wave {
		if info, err := os.Stat(p); err == nil {
			sizes[p] = info.Size()
		}
	}

	// Longest first: the tail of a wave is one worker finishing alone,
	// and it should not be the biggest file in it.
	order := slices.Clone(wave)
	slices.SortStableFunc(order, func(a, b string) int { return cmp.Compare(sizes[b], sizes[a]) })

	results := make([]source, len(order))
	failures := make([]error, len(order))

	if workers > len(order) {
		workers = len(order)
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i], failures[i] = readOne(order[i])
			}
		}()
	}
	for i := range order {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	byPath := make(map[string]source, len(order))
	for i, s := range results {
		if failures[i] == nil {
			byPath[order[i]] = s
		}
	}

	out := make([]source, 0, len(wave))
	for _, p := range wave {
		if s, ok := byPath[p]; ok {
			out = append(out, s)
		}
	}

	for i := range order {
		if failures[i] != nil {
			return out, failures[i]
		}
	}

	return out, nil
}

// readOne reads, frames and parses one file. Nothing here is shared with
// another worker: the framer and the parser own everything they touch,
// and the bytes are read into a slice this call allocated.
func readOne(path string) (source, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return source{}, errs.Wrapf(err, "smi: reading %s", path)
	}

	f := frame.Cut(src, frame.Options{File: path})

	return source{path: path, frames: f, parsed: parse.Parse(f)}, nil
}

// canonicalPath returns the path two spellings of the same file agree
// on, so a caller that names a file twice reads it once. A path that
// cannot be made absolute is its own key rather than an error, because
// failing a load over a path the OS will happily open would be worse.
func canonicalPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}

	return abs
}
