package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
)

func buildEmissionPlan(vs *VendorSet, importBase string) (*emissionPlan, error) {
	view, err := buildVendorDataView(vs.Modules, vs.Recovered)
	if err != nil {
		return nil, err
	}
	plan := &emissionPlan{
		view:       view,
		packages:   make(map[string]*packagePlan, len(vs.Modules)),
		groups:     make(map[*dataGroupView]*groupShape),
		nodeShapes: make(map[*dataNodeView]*nodeShape),
		importBase: strings.TrimSuffix(importBase, "/"),
	}
	for _, module := range vs.Modules {
		plan.packages[module.Name] = &packagePlan{module: module}
	}

	var visit func(*dataNodeView, *packagePlan, []*dataNodeView)
	visit = func(node *dataNodeView, topOwner *packagePlan, ancestors []*dataNodeView) {
		if !isDataDir(node.entry) {
			return
		}
		owner := plan.packages[node.module.Name]
		owner.nodes = append(owner.nodes, node)
		if node.entry.IsList() && node.entry.Key != "" {
			li := &listInstance{
				node:          node,
				path:          node.path,
				ancestorNodes: slices.Clone(ancestors),
				segments:      viewPathSegments(node.path),
				rowOwner:      owner,
				topOwner:      topOwner,
				candidates:    candidateSuffixes(viewPathSegments(node.path)),
			}
			topOwner.listInstances = append(topOwner.listInstances, li)
		}
		if len(node.path) == 1 && !node.entry.IsList() {
			topOwner.topContainers = append(topOwner.topContainers, &topContainerInstance{
				node:       node,
				path:       node.path,
				segments:   viewPathSegments(node.path),
				topOwner:   topOwner,
				candidates: candidateSuffixes(viewPathSegments(node.path)),
			})
		}

		nextAncestors := ancestors
		if node.entry.IsList() {
			nextAncestors = append(slices.Clone(ancestors), node)
		}
		for _, child := range node.children {
			visit(child, topOwner, nextAncestors)
		}
		for _, group := range node.groups {
			groupOwner := plan.packages[group.module.Name]
			shape := &groupShape{
				module: group.module,
				target: node,
				group:  group,
				owner:  groupOwner,
			}
			plan.groups[group] = shape
			groupOwner.groups = append(groupOwner.groups, shape)
			for _, child := range group.children {
				visit(child, topOwner, nextAncestors)
			}
		}
	}

	moduleNames := make([]string, 0, len(view.moduleViews))
	for name := range view.moduleViews {
		moduleNames = append(moduleNames, name)
	}
	slices.Sort(moduleNames)
	for _, name := range moduleNames {
		moduleView := view.moduleViews[name]
		topOwner := plan.packages[moduleView.module.Name]
		for _, root := range moduleView.roots {
			visit(root, topOwner, nil)
		}
	}

	groupMemo := make(map[*dataGroupView]string, len(plan.groups))
	packageNames := make([]string, 0, len(plan.packages))
	for name := range plan.packages {
		packageNames = append(packageNames, name)
	}
	slices.Sort(packageNames)
	for _, name := range packageNames {
		packagePlan := plan.packages[name]
		resolvePackageNaming(packagePlan, groupMemo)
		for _, shape := range packagePlan.shapes {
			for _, instance := range shape.instances {
				plan.nodeShapes[instance.node] = shape
			}
		}
	}
	for _, packagePlan := range plan.packages {
		for _, li := range packagePlan.listInstances {
			li.shapeKey = li.rowOwner.shapeKeyByNode[li.node]
			for _, ancestor := range li.ancestorNodes {
				li.ancestors = append(li.ancestors, ancestorList{
					node:      ancestor,
					entryName: ancestor.entry.Name,
					shapeKey:  plan.packages[ancestor.module.Name].shapeKeyByNode[ancestor],
					keys:      strings.Fields(ancestor.entry.Key),
				})
			}
		}
		for _, tc := range packagePlan.topContainers {
			tc.shapeKey = packagePlan.shapeKeyByNode[tc.node]
		}
	}
	return plan, nil
}

// Emit renders every loaded module's binding package under outDir/<vendor>/<package>.
// Each vendor's output is built from one deterministic data view so grouped
// children and cross-package descriptors use the same schema plan.
func Emit(sets []*VendorSet, outDir string) error {
	output, err := resolveOutputModule(outDir)
	if err != nil {
		return err
	}
	for _, vs := range sets {
		vendorDir := filepath.Join(outDir, vs.Vendor)
		if err := os.RemoveAll(vendorDir); err != nil {
			return errs.Wrapf(err, "clear vendor output %s", vendorDir)
		}
		plan, err := buildEmissionPlan(vs, output.Path+"/"+vs.Vendor)
		if err != nil {
			return err
		}
		packageNames := make([]string, 0, len(plan.packages))
		for name := range plan.packages {
			packageNames = append(packageNames, name)
		}
		slices.Sort(packageNames)
		for _, name := range packageNames {
			packagePlan := plan.packages[name]
			files, err := emitModuleFiles(plan, packagePlan)
			if err != nil {
				return err
			}
			pkgDir := filepath.Join(vendorDir, packagePlan.module.Package)
			if err := os.MkdirAll(pkgDir, 0o755); err != nil {
				return errs.Wrapf(err, "create package dir %s", pkgDir)
			}
			for filename, src := range files {
				if err := os.WriteFile(filepath.Join(pkgDir, filename), src, 0o644); err != nil {
					return errs.Wrapf(err, "write %s", filepath.Join(pkgDir, filename))
				}
			}
		}
	}
	return nil
}
