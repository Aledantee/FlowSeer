package main

import (
	"sort"
	"strings"

	"go.aledante.io/ae"

	"github.com/sleepinggenius2/gosmi"
)

// CycleError reports a dependency cycle discovered during topological
// sorting. Modules lists the participating module names in a stable
// order (lexicographic) so the diagnostic is deterministic.
type CycleError struct {
	Modules []string
}

// Error returns a one-line diagnostic listing the cycle participants.
func (e *CycleError) Error() string {
	return "dependency cycle in modules: " + strings.Join(e.Modules, " -> ")
}

// topoSort returns the module names of mods in dependency-respecting
// order: every Module.DependsOn edge u -> v places u before v in the
// returned slice.
//
// The sort is Kahn's algorithm and is stable: ties between modules
// whose remaining in-degree reaches zero on the same pass are broken by
// the position of the module in the input slice, so output is
// independent of map iteration order.
//
// Returns a *CycleError if any cycle (including self-loops) prevents
// completion; the error names the modules still carrying unresolved
// in-edges.
func topoSort(mods []Module) ([]string, error) {
	// Snapshot input order so we can break ties stably.
	order := make(map[string]int, len(mods))
	for i, m := range mods {
		order[m.Name] = i
	}

	// indegree[name] counts how many depends_on edges still point at this
	// module. adj[u] lists modules that depend on u, so removing u
	// decrements their indegree.
	indegree := make(map[string]int, len(mods))
	adj := make(map[string][]string, len(mods))
	for _, m := range mods {
		// Ensure every node is in the map even with no edges, so the
		// ready-set loop below can find it.
		if _, ok := indegree[m.Name]; !ok {
			indegree[m.Name] = 0
		}
		for _, dep := range m.DependsOn {
			// Self-loops would otherwise be hidden as an indegree of 1
			// that can never drain; surface them as a cycle.
			indegree[m.Name]++
			adj[dep] = append(adj[dep], m.Name)
		}
	}

	// Seed the ready queue in input order so iteration is deterministic
	// regardless of map ordering.
	ready := make([]string, 0, len(mods))
	for _, m := range mods {
		if indegree[m.Name] == 0 {
			ready = append(ready, m.Name)
		}
	}

	result := make([]string, 0, len(mods))
	for len(ready) > 0 {
		// Pop the input-earliest ready node. Insertion order is already
		// input order because we built it that way and only append to
		// the tail in the same order below.
		next := ready[0]
		ready = ready[1:]
		result = append(result, next)

		// Decrement successors. We collect newly-ready ones, sort them
		// by input order, then append — this preserves determinism
		// across Go's randomized map iteration.
		var newlyReady []string
		for _, succ := range adj[next] {
			indegree[succ]--
			if indegree[succ] == 0 {
				newlyReady = append(newlyReady, succ)
			}
		}
		sort.Slice(newlyReady, func(i, j int) bool {
			return order[newlyReady[i]] < order[newlyReady[j]]
		})
		ready = append(ready, newlyReady...)
	}

	if len(result) != len(mods) {
		// Collect remaining nodes — these are the cycle participants
		// (or downstream of a cycle). Lexicographic order for stable
		// diagnostics.
		var stuck []string
		for name, deg := range indegree {
			if deg > 0 {
				stuck = append(stuck, name)
			}
		}
		sort.Strings(stuck)
		return nil, &CycleError{Modules: stuck}
	}
	return result, nil
}

// LoadModules initializes gosmi, applies the config's search paths, and
// loads every module in the config in topologically-sorted dependency
// order. The returned slice mirrors the load order.
//
// The caller owns no gosmi cleanup: this function pairs Init with Exit
// internally via a deferred call only on its error paths. On success
// the gosmi global state remains live so callers can inspect modules
// (LoadModules returns SmiModule pointers, which dereference into that
// global state). Callers that want to release it must call
// gosmi.Exit() themselves.
//
// LoadModules never returns a non-nil slice with a non-nil error.
func LoadModules(cfg *Config) ([]*gosmi.SmiModule, error) {
	if cfg == nil {
		return nil, ae.Msg("LoadModules called with nil config")
	}

	order, err := topoSort(cfg.Modules)
	if err != nil {
		return nil, err
	}

	gosmi.Init()
	success := false
	defer func() {
		if !success {
			gosmi.Exit()
		}
	}()

	for _, p := range cfg.SearchPaths {
		gosmi.AppendPath(p)
	}

	out := make([]*gosmi.SmiModule, 0, len(order))
	for _, name := range order {
		if _, err := gosmi.LoadModule(name); err != nil {
			return nil, ae.Wrapf("load module %q from search paths %v", err, name, cfg.SearchPaths)
		}
		mod, err := gosmi.GetModule(name)
		if err != nil {
			return nil, ae.Wrapf("retrieve module %q after load", err, name)
		}
		// Copy onto the heap so the slice element survives independent
		// of the loop variable.
		modCopy := mod
		out = append(out, &modCopy)
	}

	success = true
	return out, nil
}
