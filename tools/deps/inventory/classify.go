package inventory

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Classify assigns deploy or run to Go entries. Every Go module contributes its
// module graph to Via. Root and netpen modules also use their shipping package
// closures for Linux, Darwin, and Windows. Other Go modules remain run-scoped.
func Classify(_ string, modules []Module, entries []Entry) ([]Entry, error) {
	rootModule := moduleForManifest(modules, "go.mod")
	closures := make(map[string]closureSet)
	graphs := make(map[string]map[string][]string, len(modules))
	for _, module := range modules {
		via, err := moduleGraph(module)
		if err != nil {
			return nil, err
		}
		graphs[module.Manifest] = via

		if module.Manifest != "go.mod" && module.Manifest != "src/edge/netpen/go.mod" {
			continue
		}
		set, err := moduleClosures(module, module.Manifest == rootModule.Manifest)
		if err != nil {
			return nil, err
		}
		closures[module.Manifest] = set
	}

	for i := range entries {
		if entries[i].Ecosystem != "go" {
			continue
		}
		criteria := entries[i].Criteria
		if criteria == "" {
			criteria = "run"
		}
		for _, manifest := range entries[i].Manifests {
			if manifest == "generated/go/yang/go.sum" {
				criteria = "deploy"
			}
			if set, ok := closures[manifestForGoSum(manifest)]; ok {
				key := moduleKey(entries[i].Name, entries[i].Version)
				if !set.Test[key] || set.Build[key] {
					criteria = "deploy"
				}
			}
			if via, ok := graphs[manifestForGoSum(manifest)]; ok {
				entries[i].Via = appendUnique(entries[i].Via, via[moduleKey(entries[i].Name, entries[i].Version)]...)
			}
		}
		entries[i].Criteria = criteria
	}
	return entries, nil
}

type closureSet struct {
	Build map[string]bool
	Test  map[string]bool
}

func moduleForManifest(modules []Module, manifest string) Module {
	for _, module := range modules {
		if module.Manifest == manifest {
			return module
		}
	}
	return Module{}
}

func manifestForGoSum(manifest string) string {
	if manifest == "go.sum" {
		return "go.mod"
	}
	return strings.TrimSuffix(manifest, "/go.sum") + "/go.mod"
}

func moduleClosures(module Module, rootModule bool) (closureSet, error) {
	set := closureSet{Build: make(map[string]bool), Test: make(map[string]bool)}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		packages, err := shippingPackages(module, rootModule, goos)
		if err != nil {
			return closureSet{}, err
		}
		build, err := listModuleClosure(module, packages, goos, false)
		if err != nil {
			return closureSet{}, err
		}
		for key := range build {
			set.Build[key] = true
		}
		tests, err := listModuleClosure(module, packages, goos, true)
		if err != nil {
			return closureSet{}, err
		}
		for key := range tests {
			set.Test[key] = true
		}
	}
	return set, nil
}

func shippingPackages(module Module, rootModule bool, goos string) ([]string, error) {
	pattern := "./..."
	if rootModule {
		pattern = "./src/..."
	}
	output, err := runGo(module.Dir, goos, "list", "-mod=readonly", "-e", "-f", "{{.ImportPath}}\t{{.Dir}}\t{{join .GoFiles \" \"}} {{join .CgoFiles \" \"}}", pattern)
	if err != nil {
		return nil, errs.Wrapf(err, "list shipping packages in %s", module.Manifest)
	}
	var packages []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 || fields[0] == "" || strings.TrimSpace(fields[2]) == "" {
			continue
		}
		rel, err := filepath.Rel(module.Dir, fields[1])
		if err != nil {
			return nil, errs.Wrap(err, "resolve package path")
		}
		rel = filepath.ToSlash(rel)
		if hasPathElement(rel, "test") || generatorPackage(fields[0]) {
			continue
		}
		packages = append(packages, fields[0])
	}
	sort.Strings(packages)
	return packages, nil
}

func generatorPackage(importPath string) bool {
	for _, suffix := range []string{"/src/protocol/snmp/cmd/mibgen", "/src/protocol/yang/cmd/yanggen", "/src/protocol/smi/internal/catalog/gen"} {
		if strings.Contains(importPath, suffix) {
			return true
		}
	}
	return false
}

func hasPathElement(path, wanted string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == wanted {
			return true
		}
	}
	return false
}

func listModuleClosure(module Module, packages []string, goos string, tests bool) (map[string]bool, error) {
	if len(packages) == 0 {
		return map[string]bool{}, nil
	}
	args := []string{"list", "-mod=readonly", "-e", "-deps"}
	if tests {
		args = append(args, "-test")
	}
	args = append(args, "-f", "{{if .Module}}{{.Module.Path}}\t{{.Module.Version}}{{end}}")
	args = append(args, packages...)
	output, err := runGo(module.Dir, goos, args...)
	if err != nil {
		kind := "build"
		if tests {
			kind = "test"
		}
		return nil, errs.Wrapf(err, "list %s closure in %s for %s", kind, module.Manifest, goos)
	}
	return parseModuleKeys(output), nil
}

func parseModuleKeys(output string) map[string]bool {
	keys := make(map[string]bool)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), "\t", 2)
		if len(fields) == 2 && fields[0] != "" && fields[1] != "" {
			keys[moduleKey(fields[0], fields[1])] = true
		}
	}
	return keys
}

func moduleGraph(module Module) (map[string][]string, error) {
	graph, err := readModuleGraph(module)
	if err != nil {
		return nil, err
	}
	via := make(map[string][]string)
	for direct := range module.Direct {
		for node := range reachableGraph(graph.Edges, module.Path, direct) {
			via[node] = appendUnique(via[node], direct)
		}
	}
	for key := range via {
		sort.Strings(via[key])
	}
	return via, nil
}

func readModuleGraph(module Module) (ModuleGraph, error) {
	output, err := runGo(module.Dir, "", "mod", "graph")
	if err != nil {
		return ModuleGraph{}, errs.Wrapf(err, "read module graph for %s", module.Manifest)
	}
	graph := ModuleGraph{Edges: make(map[string][]string)}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		graph.Edges[fields[0]] = append(graph.Edges[fields[0]], fields[1])
		if fields[0] == module.Path || strings.HasPrefix(fields[0], module.Path+"@") {
			if dependency := strings.SplitN(fields[1], "@", 2)[0]; module.Direct[dependency] && !containsString(graph.Direct, fields[1]) {
				graph.Direct = append(graph.Direct, fields[1])
			}
		}
	}
	sort.Strings(graph.Direct)
	selected, err := selectedModuleVersions(module)
	if err != nil {
		return ModuleGraph{}, err
	}
	return selectModuleGraph(graph, selected, module.Manifest)
}

type selectedModule struct {
	Path    string
	Version string
	Replace *selectedModule
	Error   *selectedModuleError
}

type selectedModuleError struct {
	Err string
}

func selectedModuleVersions(module Module) (map[string]string, error) {
	output, err := runGo(module.Dir, "", "list", "-m", "-e", "-json", "all")
	if err != nil {
		return nil, errs.Wrapf(err, "read selected module versions for %s", module.Manifest)
	}
	repositoryModule, err := enclosingRepositoryModulePath(module.Dir)
	if err != nil {
		return nil, err
	}

	selected := make(map[string]string)
	decoder := json.NewDecoder(strings.NewReader(output))
	for {
		var item selectedModule
		err := decoder.Decode(&item)
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, errs.Wrapf(err, "decode selected module versions for %s", module.Manifest)
		}
		if item.Path == "" {
			return nil, errs.Msgf("selected module versions for %s contain an empty module path", module.Manifest)
		}
		if item.Error != nil && item.Replace == nil && item.Path != module.Path && !ownedModule(item.Path, repositoryModule) {
			return nil, errs.Msgf("selected module versions for %s report %s: %s", module.Manifest, item.Path, item.Error.Err)
		}
		if item.Version == "" && item.Path != module.Path {
			return nil, errs.Msgf("selected module versions for %s omit a version for %s", module.Manifest, item.Path)
		}
		selected[item.Path] = item.Version
	}
	return selected, nil
}

func enclosingRepositoryModulePath(dir string) (string, error) {
	current := filepath.Dir(dir)
	for {
		manifest := filepath.Join(current, "go.mod")
		if _, err := os.Stat(manifest); err == nil {
			parsed, err := parseGoMod(manifest)
			if err != nil {
				return "", err
			}
			return parsed.Module.Mod.Path, nil
		} else if !os.IsNotExist(err) {
			return "", errs.Wrap(err, "stat enclosing repository Go module manifest")
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", nil
		}
		current = parent
	}
}

// selectModuleGraph applies the build list to the raw requirement graph. A
// selected target replaces an older requirement edge, but only the selected
// source version contributes outgoing edges.
func selectModuleGraph(graph ModuleGraph, selected map[string]string, manifest string) (ModuleGraph, error) {
	result := ModuleGraph{Edges: make(map[string][]string)}
	for source, targets := range graph.Edges {
		selectedSource, ok := selectedModuleSource(source, selected)
		if !ok {
			continue
		}
		for _, target := range targets {
			selectedTarget, ok := selectedModuleNode(target, selected)
			if !ok {
				continue
			}
			result.Edges[selectedSource] = appendUnique(result.Edges[selectedSource], selectedTarget)
		}
	}
	for _, direct := range graph.Direct {
		selectedDirect, ok := selectedModuleNode(direct, selected)
		if !ok {
			return ModuleGraph{}, errs.Msgf("selected module versions for %s omit direct module %s", manifest, direct)
		}
		result.Direct = appendUnique(result.Direct, selectedDirect)
	}
	sort.Strings(result.Direct)
	for source := range result.Edges {
		sort.Strings(result.Edges[source])
	}
	return result, nil
}

func selectedModuleNode(node string, selected map[string]string) (string, bool) {
	path := pathFromModuleNode(node)
	version, ok := selected[path]
	if !ok {
		return "", false
	}
	if version == "" {
		return path, true
	}
	return moduleKey(path, version), true
}

func selectedModuleSource(node string, selected map[string]string) (string, bool) {
	selectedNode, ok := selectedModuleNode(node, selected)
	if !ok {
		return "", false
	}
	if at := strings.LastIndexByte(node, '@'); at >= 0 {
		return selectedNode, selectedNode == node
	}
	return selectedNode, selected[pathFromModuleNode(node)] == ""
}

func pathFromModuleNode(node string) string {
	if at := strings.LastIndexByte(node, '@'); at >= 0 {
		return node[:at]
	}
	return node
}

func reachableGraph(edges map[string][]string, root, direct string) map[string]bool {
	directNodes := make(map[string]bool)
	for node := range edges {
		if node == root || strings.HasPrefix(node, root+"@") {
			for _, next := range edges[node] {
				if strings.HasPrefix(next, direct+"@") {
					directNodes[next] = true
				}
			}
		}
	}
	seen := make(map[string]bool, len(directNodes))
	queue := make([]string, 0, len(directNodes))
	for node := range directNodes {
		queue = append(queue, node)
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if seen[current] {
			continue
		}
		seen[current] = true
		for _, next := range edges[current] {
			if !seen[next] {
				queue = append(queue, next)
			}
		}
	}
	return seen
}

func moduleKey(path, version string) string {
	return path + "@" + version
}

func runGo(dir, goos string, args ...string) (string, error) {
	command := exec.Command("go", args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")
	if goos != "" {
		command.Env = append(command.Env, "GOOS="+goos)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return "", errs.Wrapf(err, "go %s: %s", strings.Join(args, " "), strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
