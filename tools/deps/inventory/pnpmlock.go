package inventory

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"go.aledante.io/FlowSeer/src/common/errs"
)

type pnpmLock struct {
	Entries []Entry
	Direct  []DirectDependency
	Deploy  map[string]bool
	Via     map[string][]string
}

type pnpmDocument struct {
	Importers map[string]pnpmImporter `yaml:"importers"`
	Packages  map[string]pnpmPackage  `yaml:"packages"`
	Snapshots map[string]pnpmSnapshot `yaml:"snapshots"`
}

type pnpmImporter struct {
	Dependencies    map[string]pnpmDependency `yaml:"dependencies"`
	DevDependencies map[string]pnpmDependency `yaml:"devDependencies"`
}

type pnpmDependency struct {
	Version string `yaml:"version"`
}

type pnpmPackage struct {
	Resolution pnpmResolution `yaml:"resolution"`
}

type pnpmResolution struct {
	Integrity string `yaml:"integrity"`
}

type pnpmSnapshot struct {
	Dependencies         map[string]string `yaml:"dependencies"`
	OptionalDependencies map[string]string `yaml:"optionalDependencies"`
}

func parsePnpmLock(path, manifest string) ([]Entry, []DirectDependency, error) {
	lock, err := readPnpmLock(path, manifest)
	if err != nil {
		return nil, nil, err
	}
	return lock.Entries, lock.Direct, nil
}

func readPnpmLock(path, manifest string) (pnpmLock, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pnpmLock{}, errs.Wrap(err, "read pnpm lockfile")
	}
	var document pnpmDocument
	if err := yaml.Unmarshal(data, &document); err != nil {
		return pnpmLock{}, errs.Wrap(err, "parse pnpm lockfile")
	}

	direct := make([]DirectDependency, 0)
	deployRoots := make(map[string]bool)
	for importer, value := range document.Importers {
		packageManifest := filepath.ToSlash(filepath.Join(filepath.Dir(manifest), importer, "package.json"))
		if importer == "." {
			packageManifest = filepath.ToSlash(filepath.Join(filepath.Dir(manifest), "package.json"))
		}
		for name, dependency := range value.Dependencies {
			version := snapshotVersion(dependency.Version)
			key := packageKey(name, version)
			direct = append(direct, DirectDependency{Ecosystem: "npm", Name: name, Version: version, Manifests: []string{packageManifest}, Deploy: true})
			deployRoots[key] = true
		}
		for name, dependency := range value.DevDependencies {
			version := snapshotVersion(dependency.Version)
			direct = append(direct, DirectDependency{Ecosystem: "npm", Name: name, Version: version, Manifests: []string{packageManifest}})
		}
	}
	sort.Slice(direct, func(i, j int) bool {
		if direct[i].Name != direct[j].Name {
			return direct[i].Name < direct[j].Name
		}
		return direct[i].Version < direct[j].Version
	})

	entries := make([]Entry, 0, len(document.Packages))
	for key, value := range document.Packages {
		name, version := splitPackageKey(key)
		entries = append(entries, Entry{
			Ecosystem: "npm",
			Name:      name,
			Version:   version,
			Hash:      value.Resolution.Integrity,
			Manifests: []string{manifest},
			Direct:    containsDirect(direct, name, version),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Name != entries[j].Name {
			return entries[i].Name < entries[j].Name
		}
		return entries[i].Version < entries[j].Version
	})

	graph := make(map[string][]string, len(document.Snapshots))
	for key, snapshot := range document.Snapshots {
		name, version := splitPackageKey(key)
		from := packageKey(name, version)
		for dependency, dependencyVersion := range snapshot.Dependencies {
			graph[from] = append(graph[from], packageKey(dependency, snapshotVersion(dependencyVersion)))
		}
		for dependency, dependencyVersion := range snapshot.OptionalDependencies {
			graph[from] = append(graph[from], packageKey(dependency, snapshotVersion(dependencyVersion)))
		}
		sort.Strings(graph[from])
	}
	via := make(map[string][]string)
	deploy := closure(graph, deployRoots)
	for _, dependency := range direct {
		root := packageKey(dependency.Name, dependency.Version)
		reachable := closure(graph, map[string]bool{root: true})
		for key := range reachable {
			via[key] = appendUnique(via[key], dependency.Name)
		}
	}
	return pnpmLock{Entries: entries, Direct: direct, Deploy: deploy, Via: via}, nil
}

func classifyPnpmEntries(entries []Entry, lock pnpmLock) []Entry {
	for i := range entries {
		if entries[i].Ecosystem != "npm" {
			continue
		}
		key := packageKey(entries[i].Name, entries[i].Version)
		if lock.Deploy[key] {
			entries[i].Criteria = "deploy"
		} else {
			entries[i].Criteria = "run"
		}
		entries[i].Via = append(entries[i].Via, lock.Via[key]...)
	}
	return entries
}

func containsDirect(dependencies []DirectDependency, name, version string) bool {
	for _, dependency := range dependencies {
		if dependency.Name == name && dependency.Version == version {
			return true
		}
	}
	return false
}

func closure(graph map[string][]string, roots map[string]bool) map[string]bool {
	seen := make(map[string]bool, len(roots))
	queue := make([]string, 0, len(roots))
	for root := range roots {
		seen[root] = true
		queue = append(queue, root)
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range graph[current] {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return seen
}

func packageKey(name, version string) string {
	return name + "@" + version
}

func snapshotVersion(value string) string {
	if index := strings.IndexByte(value, '('); index >= 0 {
		return value[:index]
	}
	return value
}

func splitPackageKey(key string) (string, string) {
	separator := strings.IndexByte(key, '@')
	if strings.HasPrefix(key, "@") {
		separator = strings.IndexByte(key[1:], '@')
		if separator >= 0 {
			separator++
		}
	}
	if separator < 0 {
		return key, ""
	}
	return key[:separator], snapshotVersion(key[separator+1:])
}
