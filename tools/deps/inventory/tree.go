package inventory

import (
	"sort"
	"strings"
)

// ModuleGraph is a directed graph whose direct nodes are module paths with
// the versions they select. Edges point to selected transitive versions.
type ModuleGraph struct {
	Direct []string
	Edges  map[string][]string
}

// TreeEntry reports the total and exclusive versions reachable from a direct
// dependency.
type TreeEntry struct {
	Name     string `json:"name"`
	Versions int    `json:"versions"`
	Only     int    `json:"only"`
}

// Tree reads each Go module graph and returns its direct dependency tree.
func Tree(root string) ([]TreeEntry, error) {
	modules, err := DiscoverModules(root)
	if err != nil {
		return nil, err
	}
	all := make(map[string]TreeEntry)
	for _, module := range modules {
		graph, err := readModuleGraph(module)
		if err != nil {
			return nil, err
		}
		for _, stat := range treeStats(graph) {
			key := module.Manifest + "\x00" + stat.Name
			all[key] = stat
		}
	}
	result := make([]TreeEntry, 0, len(all))
	for _, stat := range all {
		result = append(result, stat)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func treeStats(graph ModuleGraph) []TreeEntry {
	result := make([]TreeEntry, 0, len(graph.Direct))
	reachable := make(map[string]map[string]bool, len(graph.Direct))
	for _, direct := range graph.Direct {
		set := closure(graph.Edges, map[string]bool{direct: true})
		reachable[direct] = set
	}
	for _, direct := range graph.Direct {
		set := reachable[direct]
		only := 0
		for node := range set {
			if node == direct {
				continue
			}
			owners := 0
			for _, other := range graph.Direct {
				if reachable[other][node] {
					owners++
				}
			}
			if owners == 1 {
				only++
			}
		}
		result = append(result, TreeEntry{Name: nodeName(direct), Versions: len(set), Only: only})
	}
	return result
}

func nodeName(node string) string {
	if index := strings.LastIndexByte(node, '@'); index >= 0 {
		return node[:index]
	}
	return node
}
