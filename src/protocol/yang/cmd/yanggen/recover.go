package main

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/openconfig/goyang/pkg/yang"
)

var duplicateNode = regexp.MustCompile(`Duplicate node "([^"]+)"`)

// recoverAugments retains data children that goyang left on their augment
// entries after a conflicting merge. The map is keyed by the target entry.
func recoverAugments(vendor string, ms *yang.Modules) (map[*yang.Entry][]*yang.Entry, error) {
	recovered := make(map[*yang.Entry][]*yang.Entry)
	for _, modules := range []map[string]*yang.Module{ms.Modules, ms.SubModules} {
		for _, name := range sortedKeys(modules) {
			mod := modules[name]
			if name != mod.Name {
				continue
			}
			for _, aug := range mod.Augment {
				augment := yang.ToEntry(aug)
				target := augment.Find(aug.Name)
				if target == nil || !dataEntry(target) {
					continue
				}
				duplicates := duplicateCounts(target)
				for _, childName := range sortedKeys(augment.Dir) {
					child := augment.Dir[childName]
					if !dataEntry(child) {
						continue
					}
					survivor := target.Dir[childName]
					if !child.IsCase() {
						survivor = impliedCaseChild(survivor, childName)
					}
					if survivor != nil && survivor.Node == child.Node {
						survivorModule, survivorErr := survivor.InstantiatingModule()
						childModule, childErr := child.InstantiatingModule()
						if survivorErr == nil && childErr == nil && survivorModule == childModule {
							continue
						}
					}
					if duplicates[childName] == 0 {
						if survivor == nil {
							continue
						}
						return nil, recoveryError(vendor, aug.Name, child, survivor, "unexplained augment collision")
					}
					if survivor != nil {
						childModule, childErr := child.InstantiatingModule()
						survivorModule, survivorErr := survivor.InstantiatingModule()
						if childErr != nil || survivorErr != nil {
							return nil, recoveryError(vendor, aug.Name, child, survivor, "cannot resolve collision namespaces")
						}
						if childModule == survivorModule {
							return nil, recoveryError(vendor, aug.Name, child, survivor, "duplicate node in one module")
						}
					}
					if err := checkRecoveredLeafrefs(vendor, aug.Name, target, child); err != nil {
						return nil, err
					}
					recovered[target] = append(recovered[target], child)
				}
			}
		}
	}
	for target, children := range recovered {
		slices.SortFunc(children, func(a, b *yang.Entry) int {
			am, _ := a.InstantiatingModule()
			bm, _ := b.InstantiatingModule()
			if n := strings.Compare(am, bm); n != 0 {
				return n
			}
			return strings.Compare(a.Name, b.Name)
		})
		recovered[target] = children
	}
	for _, name := range sortedKeys(ms.Modules) {
		mod := ms.Modules[name]
		if name != mod.Name {
			continue
		}
		if err := checkRecoveredDuplicates(vendor, recovered, yang.ToEntry(mod)); err != nil {
			return nil, err
		}
	}
	if err := checkAugmentPaths(vendor, ms, recovered); err != nil {
		return nil, err
	}
	if err := checkLeafrefPaths(vendor, ms, recovered); err != nil {
		return nil, err
	}
	return recovered, nil
}

func dataEntry(e *yang.Entry) bool {
	if e == nil {
		return false
	}
	for ancestor := e; ancestor != nil; ancestor = ancestor.Parent {
		if ancestor.RPC != nil || ancestor.Kind == yang.InputEntry || ancestor.Kind == yang.OutputEntry || ancestor.Kind == yang.NotificationEntry {
			return false
		}
	}
	return true
}

func impliedCaseChild(e *yang.Entry, name string) *yang.Entry {
	if e != nil && e.IsCase() {
		return e.Dir[name]
	}
	return e
}

func duplicateCounts(e *yang.Entry) map[string]int {
	counts := make(map[string]int)
	for _, err := range e.Errors {
		if match := duplicateNode.FindStringSubmatch(err.Error()); match != nil {
			counts[match[1]]++
		}
	}
	return counts
}

func recoveryError(vendor, path string, child, survivor *yang.Entry, issue string) error {
	otherSource := "unknown"
	if survivor != nil {
		otherSource = yang.Source(survivor.Node)
	}
	return &LoadError{Vendor: vendor, Issue: fmt.Sprintf("%s at %s: %s (%s and %s)", issue, path, child.Name, yang.Source(child.Node), otherSource)}
}

func checkRecoveredDuplicates(vendor string, recovered map[*yang.Entry][]*yang.Entry, root *yang.Entry) error {
	var visit func(*yang.Entry) error
	visit = func(e *yang.Entry) error {
		if !dataEntry(e) {
			return nil
		}
		counts := duplicateCounts(e)
		for _, name := range sortedKeys(counts) {
			count := counts[name]
			matched := 0
			for _, child := range recovered[e] {
				if child.Name == name {
					matched++
				}
			}
			if matched != count {
				for _, cause := range e.Errors {
					if match := duplicateNode.FindStringSubmatch(cause.Error()); match != nil && match[1] == name {
						return &LoadError{Vendor: vendor, Issue: fmt.Sprintf("unmatched duplicate node %q at %s: %s", name, e.Path(), cause)}
					}
				}
			}
		}
		for _, name := range sortedKeys(e.Dir) {
			if err := visit(e.Dir[name]); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(root)
}

func checkRecoveredLeafrefs(vendor, path string, target, child *yang.Entry) error {
	parentModule, parentErr := target.InstantiatingModule()
	childModule, childErr := child.InstantiatingModule()
	if parentErr != nil || childErr != nil {
		return recoveryError(vendor, path, child, target, "cannot resolve recovered node namespace")
	}
	if parentModule == childModule {
		return nil
	}
	var visit func(*yang.Entry, int) error
	visit = func(e *yang.Entry, depth int) error {
		if escapesRecovered(e.Type, depth) {
			survivor := target.Dir[child.Name]
			if survivor == nil {
				survivor = target
			}
			return recoveryError(vendor, path, e, survivor, "leafref escapes recovered "+child.Name)
		}
		for _, name := range sortedKeys(e.Dir) {
			if err := visit(e.Dir[name], depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(child, 0)
}

func escapesRecovered(t *yang.YangType, depth int) bool {
	if t == nil {
		return false
	}
	if t.Kind == yang.Yleafref && strings.HasPrefix(t.Path, ".") {
		level := depth
		for _, segment := range strings.Split(stripPredicates(t.Path), "/") {
			switch segment {
			case "..":
				level--
			case ".":
			default:
				level++
			}
			if level < 0 {
				return true
			}
		}
	}
	for _, member := range t.Type {
		if escapesRecovered(member, depth) {
			return true
		}
	}
	return false
}

func checkAugmentPaths(vendor string, ms *yang.Modules, recovered map[*yang.Entry][]*yang.Entry) error {
	for _, modules := range []map[string]*yang.Module{ms.Modules, ms.SubModules} {
		for _, name := range sortedKeys(modules) {
			mod := modules[name]
			if name != mod.Name {
				continue
			}
			for _, aug := range mod.Augment {
				if err := checkTargetPath(vendor, aug, aug.Name, recovered); err != nil {
					return err
				}
			}
			for _, deviation := range mod.Deviation {
				if err := checkTargetPath(vendor, deviation, deviation.Name, recovered); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func checkTargetPath(vendor string, node yang.Node, path string, recovered map[*yang.Entry][]*yang.Entry) error {
	_, err := checkPath(vendor, node, nil, path, recovered)
	return err
}

func checkPath(vendor string, node yang.Node, leaf *yang.Entry, path string, recovered map[*yang.Entry][]*yang.Entry) (*yang.Entry, error) {
	normalized := stripPredicates(path)
	var parts []string
	var e *yang.Entry
	if strings.HasPrefix(normalized, "/") {
		parts = strings.Split(strings.TrimPrefix(normalized, "/"), "/")
		e = pathRoot(node, leaf, parts)
	} else {
		parts = strings.Split(normalized, "/")
		e = leaf
	}
	if e == nil {
		return nil, nil
	}
	for index, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if e != nil {
				e = e.Parent
			}
			continue
		default:
			prefix, name, qualified := strings.Cut(part, ":")
			if !qualified {
				name = prefix
				prefix = ""
			}
			if e == nil || !dataEntry(e) {
				return e, nil
			}
			next := e.Dir[name]
			for _, child := range recovered[e] {
				if child.Name != name {
					continue
				}
				want, wantOK := pathSegmentModule(node, leaf, prefix)
				parentModule, parentErr := e.InstantiatingModule()
				keptModule := ""
				if next != nil {
					keptModule, _ = next.InstantiatingModule()
				}
				_, deviation := node.(*yang.Deviation)
				deviatedOwnNode := deviation && index == len(parts)-1 && next == nil && wantOK && want == parentModule
				if !deviatedOwnNode && (!wantOK || parentErr != nil || want != parentModule || keptModule != parentModule) {
					if leaf != nil {
						return next, &LoadError{Vendor: vendor, Issue: fmt.Sprintf("leafref %s path %s at %s crosses recovered %s (%s, %s, %s)", leaf.Path(), path, yang.Source(node), name, yang.Source(child.Node), sourceOf(next), sourceOf(e))}
					}
					return next, &LoadError{Vendor: vendor, Issue: fmt.Sprintf("target path %s at %s crosses recovered %s (%s, %s, %s)", path, yang.Source(node), name, yang.Source(child.Node), sourceOf(next), sourceOf(e))}
				}
			}
			e = next
		}
	}
	return e, nil
}

func pathRoot(node yang.Node, leaf *yang.Entry, parts []string) *yang.Entry {
	if leaf != nil {
		e := leaf
		for e.Parent != nil {
			e = e.Parent
		}
		prefix := pathPrefix(parts)
		if prefix == "" {
			return e
		}
		root := pathModule(node, prefix)
		if root == nil {
			return nil
		}
		entryRoot, ok := e.Node.(*yang.Module)
		if !ok || entryRoot != root {
			return yang.ToEntry(root)
		}
		return e
	}
	root := pathModule(node, pathPrefix(parts))
	if root == nil {
		return nil
	}
	return yang.ToEntry(root)
}

func pathPrefix(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	prefix, _, ok := strings.Cut(parts[0], ":")
	if !ok {
		return ""
	}
	return prefix
}

func pathModule(node yang.Node, prefix string) *yang.Module {
	mod := yang.FindModuleByPrefix(node, prefix)
	if mod == nil {
		return nil
	}
	if mod.Kind() == "submodule" && mod.BelongsTo != nil && mod.Modules != nil {
		if parent := mod.Modules.Modules[mod.BelongsTo.Name]; parent != nil {
			return parent
		}
	}
	return mod
}

func pathSegmentModule(node yang.Node, leaf *yang.Entry, prefix string) (string, bool) {
	if prefix != "" {
		mod := pathModule(node, prefix)
		if mod == nil {
			return "", false
		}
		return mod.Name, true
	}
	if leaf != nil {
		name, err := leaf.InstantiatingModule()
		return name, err == nil
	}
	mod := pathModule(node, "")
	if mod == nil {
		return "", false
	}
	return mod.Name, true
}

func stripPredicates(path string) string {
	var b strings.Builder
	depth := 0
	for _, r := range path {
		switch r {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func checkLeafrefPaths(vendor string, ms *yang.Modules, recovered map[*yang.Entry][]*yang.Entry) error {
	visited := make(map[*yang.Entry]bool)
	var visit func(*yang.Entry) error
	visit = func(e *yang.Entry) error {
		if e == nil || visited[e] || !dataEntry(e) {
			return nil
		}
		visited[e] = true
		for _, path := range leafrefPaths(e.Type) {
			if _, err := checkPath(vendor, e.Node, e, path, recovered); err != nil {
				return err
			}
		}
		for _, name := range sortedKeys(e.Dir) {
			if err := visit(e.Dir[name]); err != nil {
				return err
			}
		}
		for _, child := range recovered[e] {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, modules := range []map[string]*yang.Module{ms.Modules, ms.SubModules} {
		for _, name := range sortedKeys(modules) {
			mod := modules[name]
			if name != mod.Name {
				continue
			}
			if err := visit(yang.ToEntry(mod)); err != nil {
				return err
			}
		}
	}
	return nil
}

func leafrefPaths(t *yang.YangType) []string {
	if t == nil {
		return nil
	}
	var paths []string
	if t.Kind == yang.Yleafref && t.Path != "" {
		paths = append(paths, t.Path)
	}
	for _, member := range t.Type {
		paths = append(paths, leafrefPaths(member)...)
	}
	return paths
}

func sourceOf(e *yang.Entry) string {
	if e == nil {
		return "unknown"
	}
	return yang.Source(e.Node)
}
