// Package inventory reads the dependency lockfiles and classifies their entries.
package inventory

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Entry is one pinned, code-bearing dependency version from a lockfile.
type Entry struct {
	Ecosystem string   `json:"ecosystem"`
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	Hash      string   `json:"hash"`
	Manifests []string `json:"manifests"`
	Direct    bool     `json:"direct"`
	Criteria  string   `json:"criteria"`
	Via       []string `json:"via,omitempty"`
}

// DirectDependency is a direct requirement, including requirements whose
// source hash is not a code-bearing zip hash.
type DirectDependency struct {
	Ecosystem string
	Name      string
	Version   string
	Manifests []string
	Deploy    bool
}

// Module describes a discovered Go module and its manifest-relative path.
type Module struct {
	Dir      string
	Manifest string
	Path     string
	GoSum    string
	Direct   map[string]bool
	Requires []DirectDependency
}

// Result contains the pinned entries and the direct requirements discovered
// while reading the repository.
type Result struct {
	Entries []Entry
	Direct  []DirectDependency
}

// Collect discovers the repository's Go modules and pnpm lockfile, then
// classifies every pinned entry using the discovered dependency closures.
func Collect(root string) (Result, error) {
	root, err := resolveRoot(root)
	if err != nil {
		return Result{}, err
	}

	modules, result, err := readLockfiles(root)
	if err != nil {
		return Result{}, err
	}
	result.Entries, err = Classify(root, modules, result.Entries)
	if err != nil {
		return Result{}, err
	}
	result.Entries = mergeEntries(result.Entries)
	sortEntries(result.Entries)

	return result, nil
}

// Read reads the repository lockfiles without invoking the Go toolchain.
// Callers that only need pinned versions can use it offline, while [Collect]
// adds package classification through Go commands.
func Read(root string) (Result, error) {
	root, err := resolveRoot(root)
	if err != nil {
		return Result{}, err
	}
	_, result, err := readLockfiles(root)
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func resolveRoot(root string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", errs.Wrap(err, "resolve repository root")
	}
	return root, nil
}

func readLockfiles(root string) ([]Module, Result, error) {
	modules, err := DiscoverModules(root)
	if err != nil {
		return nil, Result{}, err
	}
	entries := make([]Entry, 0)
	direct := make([]DirectDependency, 0)
	for _, module := range modules {
		if module.GoSum == "" {
			continue
		}
		goSumPath := filepath.Join(root, filepath.FromSlash(module.GoSum))
		parsed, err := parseGoSum(goSumPath, module.GoSum, module.Path, module.Direct)
		if err != nil {
			return nil, Result{}, err
		}
		entries = append(entries, parsed...)
		direct = append(direct, module.Requires...)
	}

	lockPath := filepath.Join(root, "frontend", "web", "pnpm-lock.yaml")
	if _, err := os.Stat(lockPath); err == nil {
		lock, err := readPnpmLock(lockPath, "frontend/web/pnpm-lock.yaml")
		if err != nil {
			return nil, Result{}, err
		}
		entries = append(entries, lock.Entries...)
		direct = append(direct, lock.Direct...)
		entries = classifyPnpmEntries(entries, lock)
	} else if !os.IsNotExist(err) {
		return nil, Result{}, errs.Wrap(err, "stat pnpm lockfile")
	}

	entries = mergeEntries(entries)
	direct = mergeDirect(direct)
	sortEntries(entries)
	return modules, Result{Entries: entries, Direct: direct}, nil
}

// DiscoverModules returns every tracked-shape go.mod below root while
// ignoring repository metadata, package-manager installs, and worktree caches.
func DiscoverModules(root string) ([]Module, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, errs.Wrap(err, "resolve repository root")
	}
	repoModulePath, err := repositoryModulePath(root)
	if err != nil {
		return nil, err
	}

	var modules []Module
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if rel != "." && skipDir(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != "go.mod" {
			return nil
		}

		module, err := parseModule(root, path, repoModulePath)
		if err != nil {
			return err
		}
		modules = append(modules, module)
		return nil
	})
	if err != nil {
		return nil, errs.Wrap(err, "discover Go modules")
	}

	sort.Slice(modules, func(i, j int) bool { return modules[i].Manifest < modules[j].Manifest })
	return modules, nil
}

func skipDir(rel string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, part := range parts {
		if part == ".git" || part == "node_modules" {
			return true
		}
	}
	path := filepath.ToSlash(rel)
	return path == ".claude/worktrees" || strings.HasPrefix(path, ".claude/worktrees/") ||
		path == ".codex/worktrees" || strings.HasPrefix(path, ".codex/worktrees/")
}

func mergeEntries(entries []Entry) []Entry {
	merged := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		key := entry.Ecosystem + "\x00" + entry.Name + "\x00" + entry.Version
		current, ok := merged[key]
		if !ok {
			current = entry
		}
		if current.Hash == "" {
			current.Hash = entry.Hash
		}
		current.Direct = current.Direct || entry.Direct
		current.Criteria = stricterCriteria(current.Criteria, entry.Criteria)
		current.Manifests = appendUnique(current.Manifests, entry.Manifests...)
		current.Via = appendUnique(current.Via, entry.Via...)
		merged[key] = current
	}

	result := make([]Entry, 0, len(merged))
	for _, entry := range merged {
		entry.Manifests = sortedUnique(entry.Manifests)
		entry.Via = sortedUnique(entry.Via)
		result = append(result, entry)
	}
	return result
}

func mergeDirect(dependencies []DirectDependency) []DirectDependency {
	merged := make(map[string]DirectDependency, len(dependencies))
	for _, dependency := range dependencies {
		key := dependency.Ecosystem + "\x00" + dependency.Name + "\x00" + dependency.Version
		current := merged[key]
		current.Ecosystem = dependency.Ecosystem
		current.Name = dependency.Name
		current.Version = dependency.Version
		current.Manifests = appendUnique(current.Manifests, dependency.Manifests...)
		current.Deploy = current.Deploy || dependency.Deploy
		merged[key] = current
	}
	result := make([]DirectDependency, 0, len(merged))
	for _, dependency := range merged {
		dependency.Manifests = sortedUnique(dependency.Manifests)
		result = append(result, dependency)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Ecosystem != result[j].Ecosystem {
			return result[i].Ecosystem < result[j].Ecosystem
		}
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].Version < result[j].Version
	})
	return result
}

func stricterCriteria(left, right string) string {
	if left == "deploy" || right == "deploy" {
		return "deploy"
	}
	if left != "" {
		return left
	}
	return right
}

func appendUnique(values []string, additions ...string) []string {
	seen := make(map[string]bool, len(values)+len(additions))
	for _, value := range values {
		seen[value] = true
	}
	for _, value := range additions {
		if value != "" && !seen[value] {
			values = append(values, value)
			seen[value] = true
		}
	}
	return values
}

func sortedUnique(values []string) []string {
	values = appendUnique(nil, values...)
	sort.Strings(values)
	return values
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Ecosystem != entries[j].Ecosystem {
			return entries[i].Ecosystem < entries[j].Ecosystem
		}
		if entries[i].Name != entries[j].Name {
			return entries[i].Name < entries[j].Name
		}
		return entries[i].Version < entries[j].Version
	})
}
