package main

import (
	"fmt"
	"slices"
	"strings"

	goyang "github.com/openconfig/goyang/pkg/yang"
)

// vendorDataView is the schema view shared by package emission. Each module
// keeps its own roots, while a node records groups for children owned by an
// augmenting module.
type vendorDataView struct {
	moduleViews map[string]*dataModuleView
}

type dataModuleView struct {
	module *LoadedModule
	roots  []*dataNodeView
}

type dataNodeView struct {
	entry    *goyang.Entry
	module   *LoadedModule
	path     []pathSeg
	dataPath string
	children []*dataNodeView
	groups   []*dataGroupView
}

type dataGroupView struct {
	module   *LoadedModule
	children []*dataNodeView
}

type dataViewBuilder struct {
	modules   map[string]*LoadedModule
	recovered map[*goyang.Entry][]*goyang.Entry
	edges     map[string]map[string]struct{}
	vendor    string
}

// buildVendorDataView builds the module-qualified data tree used by the
// emitter. The path is derived from the view position because recovered
// entries retain the augment entry as their goyang parent.
func buildVendorDataView(modules []*LoadedModule, recovered map[*goyang.Entry][]*goyang.Entry) (*vendorDataView, error) {
	b := &dataViewBuilder{
		modules:   make(map[string]*LoadedModule, len(modules)),
		recovered: recovered,
		edges:     make(map[string]map[string]struct{}),
	}

	ordered := slices.Clone(modules)
	for _, module := range ordered {
		if module == nil {
			return nil, &LoadError{Vendor: b.vendor, Issue: "data view contains a nil module"}
		}
	}
	slices.SortFunc(ordered, func(a, b *LoadedModule) int {
		return strings.Compare(a.Name, b.Name)
	})
	for _, module := range ordered {
		if b.vendor == "" {
			b.vendor = module.Vendor
		}
		if _, exists := b.modules[module.Name]; exists {
			return nil, &LoadError{Vendor: b.vendor, Module: module.Name, Issue: "data view contains a duplicate module"}
		}
		b.modules[module.Name] = module
		b.edges[module.Package] = make(map[string]struct{})
	}

	view := &vendorDataView{moduleViews: make(map[string]*dataModuleView, len(ordered))}
	for _, module := range ordered {
		if module.Entry == nil {
			return nil, &LoadError{Vendor: b.vendor, Module: module.Name, Issue: "data view module has no entry tree"}
		}
		moduleView := &dataModuleView{module: module}
		for _, entry := range b.children(module.Entry) {
			node, err := b.node(entry, nil)
			if err != nil {
				return nil, err
			}
			moduleView.roots = append(moduleView.roots, node)
		}
		slices.SortFunc(moduleView.roots, compareDataNodes)
		view.moduleViews[module.Name] = moduleView
	}

	if cycle := findPackageCycle(b.edges); len(cycle) > 0 {
		return nil, &LoadError{
			Vendor: b.vendor,
			Issue:  fmt.Sprintf("binding package import cycle: %s", strings.Join(cycle, " -> ")),
		}
	}
	return view, nil
}

func (b *dataViewBuilder) node(entry *goyang.Entry, parentPath []pathSeg) (*dataNodeView, error) {
	module, err := b.moduleFor(entry)
	if err != nil {
		return nil, err
	}

	path := append(slices.Clone(parentPath), pathSeg{
		module:    module.Name,
		namespace: namespaceOf(entry),
		name:      entry.Name,
	})
	node := &dataNodeView{
		entry:    entry,
		module:   module,
		path:     path,
		dataPath: renderDataPath(path),
	}

	groupByModule := make(map[string]*dataGroupView)
	for _, childEntry := range b.children(entry) {
		child, err := b.node(childEntry, path)
		if err != nil {
			return nil, err
		}

		if child.module.Package == module.Package {
			node.children = append(node.children, child)
			continue
		}

		group := groupByModule[child.module.Name]
		if group == nil {
			group = &dataGroupView{module: child.module}
			groupByModule[child.module.Name] = group
		}
		group.children = append(group.children, child)
		b.edges[module.Package][child.module.Package] = struct{}{}
	}

	slices.SortFunc(node.children, compareDataNodes)
	for _, group := range groupByModule {
		slices.SortFunc(group.children, compareDataNodes)
		node.groups = append(node.groups, group)
	}
	slices.SortFunc(node.groups, func(a, b *dataGroupView) int {
		return strings.Compare(a.module.Name, b.module.Name)
	})
	return node, nil
}

func (b *dataViewBuilder) moduleFor(entry *goyang.Entry) (*LoadedModule, error) {
	name, err := entry.InstantiatingModule()
	if err != nil {
		return nil, &LoadError{
			Vendor:  b.vendor,
			Issue:   fmt.Sprintf("resolve data node %q module: %v", entry.Name, err),
			wrapped: err,
		}
	}
	module := b.modules[name]
	if module == nil {
		return nil, &LoadError{
			Vendor: b.vendor,
			Module: name,
			Issue:  fmt.Sprintf("data node %q belongs to an unloaded module", entry.Name),
		}
	}
	return module, nil
}

func (b *dataViewBuilder) children(parent *goyang.Entry) []*goyang.Entry {
	var out []*goyang.Entry
	var collect func(*goyang.Entry)
	collect = func(entry *goyang.Entry) {
		children := make([]*goyang.Entry, 0, len(entry.Dir)+len(b.recovered[entry]))
		for _, name := range sortedKeys(entry.Dir) {
			children = append(children, entry.Dir[name])
		}
		children = append(children, b.recovered[entry]...)
		slices.SortFunc(children, compareEntries)
		for _, child := range children {
			if child.IsChoice() || child.IsCase() {
				collect(child)
				continue
			}
			if isViewData(child) {
				out = append(out, child)
			}
		}
	}
	collect(parent)
	slices.SortFunc(out, compareEntries)
	return out
}

func isViewData(entry *goyang.Entry) bool {
	return entry != nil && entry.RPC == nil && dataEntry(entry) && !isNonData(entry)
}

func compareEntries(a, b *goyang.Entry) int {
	if n := strings.Compare(a.Name, b.Name); n != 0 {
		return n
	}
	am, _ := a.InstantiatingModule()
	bm, _ := b.InstantiatingModule()
	return strings.Compare(am, bm)
}

func compareDataNodes(a, b *dataNodeView) int {
	return strings.Compare(a.dataPath, b.dataPath)
}

func renderDataPath(path []pathSeg) string {
	var b strings.Builder
	previousModule := ""
	for _, segment := range path {
		b.WriteByte('/')
		if segment.module != "" && segment.module != previousModule {
			b.WriteString(segment.module)
			b.WriteByte(':')
			previousModule = segment.module
		}
		b.WriteString(segment.name)
	}
	return b.String()
}

func findPackageCycle(edges map[string]map[string]struct{}) []string {
	packages := make([]string, 0, len(edges))
	for packageName := range edges {
		packages = append(packages, packageName)
	}
	slices.Sort(packages)

	state := make(map[string]uint8, len(packages))
	stack := make([]string, 0, len(packages))
	position := make(map[string]int, len(packages))
	var visit func(string) []string
	visit = func(packageName string) []string {
		state[packageName] = 1
		position[packageName] = len(stack)
		stack = append(stack, packageName)

		nextPackages := make([]string, 0, len(edges[packageName]))
		for next := range edges[packageName] {
			nextPackages = append(nextPackages, next)
		}
		slices.Sort(nextPackages)
		for _, next := range nextPackages {
			switch state[next] {
			case 0:
				if cycle := visit(next); len(cycle) > 0 {
					return cycle
				}
			case 1:
				cycle := append([]string(nil), stack[position[next]:]...)
				return append(cycle, next)
			}
		}

		stack = stack[:len(stack)-1]
		delete(position, packageName)
		state[packageName] = 2
		return nil
	}

	for _, packageName := range packages {
		if state[packageName] == 0 {
			if cycle := visit(packageName); len(cycle) > 0 {
				return cycle
			}
		}
	}
	return nil
}
