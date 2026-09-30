package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	goyang "github.com/openconfig/goyang/pkg/yang"

	"go.aledante.io/FlowSeer/src/protocol/internal/goname"
)

// naming.go resolves identifiers for a complete vendor view. A package owns
// the nodes its module defines, while list companions belong to the package
// of the tree that contains the list.

type emissionPlan struct {
	view       *vendorDataView
	packages   map[string]*packagePlan
	groups     map[*dataGroupView]*groupShape
	nodeShapes map[*dataNodeView]*nodeShape
	importBase string
}

type packagePlan struct {
	module         *LoadedModule
	nodes          []*dataNodeView
	groups         []*groupShape
	shapes         []*nodeShape
	listInstances  []*listInstance
	topContainers  []*topContainerInstance
	scope          *nameScope
	shapeKeyByNode map[*dataNodeView]string
}

type groupShape struct {
	key        string
	module     *LoadedModule
	target     *dataNodeView
	group      *dataGroupView
	targets    []*dataNodeView
	candidates []string
	name       string
	schemaName string
	owner      *packagePlan
}

// camel converts a YANG identifier to an exported Go identifier using the
// shared naming rules.
func camel(name string) string {
	return goname.Exported(name)
}

// symbolKind defines the priority of a symbol when breaking ties for hash
// fallback. Lower values take precedence.
type symbolKind int

const (
	kindStruct symbolKind = iota
	kindListKey
	kindListFlatRow
	kindListDescriptor
	kindContainerDescriptor
	kindSchema
	kindIdentity
)

// candidateSuffixes returns candidate names for segments from shortest to
// longest.
func candidateSuffixes(segments []string) []string {
	if len(segments) == 0 {
		return nil
	}
	cands := make([]string, len(segments))
	for i := 1; i <= len(segments); i++ {
		cands[i-1] = strings.Join(segments[len(segments)-i:], "")
	}
	return cands
}

// longestCommonSuffix returns the longest common trailing segment sequence
// across all sequences in seqs.
func longestCommonSuffix(seqs [][]string) []string {
	if len(seqs) == 0 {
		return nil
	}
	if len(seqs) == 1 {
		return slices.Clone(seqs[0])
	}
	minLen := len(seqs[0])
	for _, s := range seqs[1:] {
		if len(s) < minLen {
			minLen = len(s)
		}
	}
	commonLen := 0
	for i := 1; i <= minLen; i++ {
		val := seqs[0][len(seqs[0])-i]
		match := true
		for _, s := range seqs[1:] {
			if s[len(s)-i] != val {
				match = false
				break
			}
		}
		if !match {
			break
		}
		commonLen = i
	}
	if commonLen == 0 {
		return nil
	}
	return slices.Clone(seqs[0][len(seqs[0])-commonLen:])
}

// claimSpec defines one symbol wanted by an entity at a given candidate.
type claimSpec struct {
	wanted        string
	kind          symbolKind
	depth         int
	tieBreak      string
	discriminator string
}

// claimantEntity is an entity participating in fair growth.
type claimantEntity struct {
	candidates []string
	curIndex   int
	getSymbols func(base string) []claimSpec
}

// resolveFairGrowth resolves clashing symbol names across entities by growing
// candidate suffixes fairly and falling back to X<hash> deterministically.
func resolveFairGrowth(entities []*claimantEntity) *nameScope {
	for {
		byName := make(map[string][]*claimantEntity)
		for _, ent := range entities {
			if len(ent.candidates) == 0 {
				continue
			}
			base := ent.candidates[ent.curIndex]
			for _, spec := range ent.getSymbols(base) {
				byName[spec.wanted] = append(byName[spec.wanted], ent)
			}
		}

		toAdvance := make(map[*claimantEntity]bool)
		hasClashes := false
		canAnyGrow := false

		for _, ents := range byName {
			if len(ents) <= 1 {
				continue
			}
			hasClashes = true
			for _, ent := range ents {
				if ent.curIndex+1 < len(ent.candidates) {
					toAdvance[ent] = true
					canAnyGrow = true
				}
			}
		}

		if !hasClashes || !canAnyGrow {
			break
		}

		for ent := range toAdvance {
			ent.curIndex++
		}
	}

	scope := newNameScope()

	var allFinal []claimSpec
	for _, ent := range entities {
		if len(ent.candidates) == 0 {
			continue
		}
		base := ent.candidates[ent.curIndex]
		allFinal = append(allFinal, ent.getSymbols(base)...)
	}

	slices.SortFunc(allFinal, func(a, b claimSpec) int {
		if a.depth != b.depth {
			return a.depth - b.depth
		}
		if a.kind != b.kind {
			return int(a.kind) - int(b.kind)
		}
		if c := strings.Compare(a.wanted, b.wanted); c != 0 {
			return c
		}
		return strings.Compare(a.tieBreak, b.tieBreak)
	})

	for _, it := range allFinal {
		scope.claim(it.wanted, it.discriminator)
	}

	return scope
}

// nameScope allocates unique identifiers within one package or struct's
// fields. Identical (identifier, path) pairs return the same name.
type nameScope struct {
	byName map[string]string
	byPath map[string]string
}

func newNameScope() *nameScope {
	return &nameScope{byName: make(map[string]string), byPath: make(map[string]string)}
}

// claim returns the unique identifier for (want, path).
func (s *nameScope) claim(want, path string) string {
	if name, ok := s.byPath[path]; ok {
		return name
	}
	name := want
	if owner, taken := s.byName[name]; taken && owner != path {
		sum := sha256.Sum256([]byte(path))
		name = fmt.Sprintf("%sX%s", want, hex.EncodeToString(sum[:3]))
		for i := 4; ; i++ {
			if owner, taken := s.byName[name]; !taken || owner == path {
				break
			}
			sum = sha256.Sum256([]byte(path + fmt.Sprint(i)))
			name = fmt.Sprintf("%sX%s", want, hex.EncodeToString(sum[:3]))
		}
	}
	s.byName[name] = path
	s.byPath[path] = name
	return name
}

// leafTypeSignature returns a deterministic string describing a YANG leaf
// type, including enum, bits, union, decimal64, and identity details.
func leafTypeSignature(t *goyang.YangType) string {
	if t == nil {
		return "string"
	}
	switch t.Kind {
	case goyang.Yenum:
		var names []string
		if t.Enum != nil {
			names = slices.Clone(t.Enum.Names())
			slices.Sort(names)
		}
		return fmt.Sprintf("enum(%s)", strings.Join(names, ","))
	case goyang.Ybits:
		var names []string
		if t.Bit != nil {
			names = slices.Clone(t.Bit.Names())
			slices.Sort(names)
		}
		return fmt.Sprintf("bits(%s)", strings.Join(names, ","))
	case goyang.Yidentityref:
		base := ""
		if t.IdentityBase != nil {
			base = t.IdentityBase.Name
		}
		return fmt.Sprintf("identityref(%s)", base)
	case goyang.Ydecimal64:
		return fmt.Sprintf("decimal64(%d)", t.FractionDigits)
	case goyang.Yunion:
		var members []string
		for _, m := range t.Type {
			members = append(members, leafTypeSignature(resolveLeafref(nil, m, 0)))
		}
		return fmt.Sprintf("union(%s)", strings.Join(members, ","))
	default:
		return t.Kind.String()
	}
}

func nodeFieldNames(n *dataNodeView) (map[*dataNodeView]string, map[*dataGroupView]string) {
	scope := newNameScope()
	children := make(map[*dataNodeView]string, len(n.children))
	groups := make(map[*dataGroupView]string, len(n.groups))
	for _, child := range n.children {
		children[child] = scope.claim(camel(child.entry.Name), child.dataPath)
	}
	for _, group := range n.groups {
		path := "group:" + n.dataPath + ":" + group.module.Name
		groups[group] = scope.claim(camel(group.module.Name), path)
	}
	return children, groups
}

func groupFieldNames(g *dataGroupView) map[*dataNodeView]string {
	scope := newNameScope()
	names := make(map[*dataNodeView]string, len(g.children))
	for _, child := range g.children {
		names[child] = scope.claim(camel(child.entry.Name), child.dataPath)
	}
	return names
}

func viewPathSegments(path []pathSeg) []string {
	segments := make([]string, 0, len(path))
	for _, segment := range path {
		segments = append(segments, camel(segment.name))
	}
	return segments
}

func computeGroupShapeKey(g *dataGroupView, target *dataNodeView, nodeMemo map[*dataNodeView]string, groupMemo map[*dataGroupView]string) string {
	if key, ok := groupMemo[g]; ok {
		return key
	}
	fieldNames := groupFieldNames(g)
	var children []string
	for _, child := range g.children {
		goName := fieldNames[child]
		if isDataDir(child.entry) {
			children = append(children, fmt.Sprintf("dir:%s:%s:%s", child.entry.Name, goName, computeViewShapeKey(child, nodeMemo, groupMemo)))
			continue
		}
		if child.entry.IsLeafList() {
			children = append(children, fmt.Sprintf("leaflist:%s:%s:%s", child.entry.Name, goName, leafTypeSignature(resolveLeafref(child.entry, child.entry.Type, 0))))
			continue
		}
		if child.entry.IsLeaf() {
			children = append(children, fmt.Sprintf("leaf:%s:%s:%s", child.entry.Name, goName, leafTypeSignature(resolveLeafref(child.entry, child.entry.Type, 0))))
		}
	}
	raw := fmt.Sprintf("group=%s:%s;target=%s:%s;children=[%s]", g.module.Name, g.module.Package, target.module.Name, target.entry.Name, strings.Join(children, "|"))
	sum := sha256.Sum256([]byte(raw))
	key := hex.EncodeToString(sum[:])
	groupMemo[g] = key
	return key
}

func computeViewShapeKey(n *dataNodeView, nodeMemo map[*dataNodeView]string, groupMemo map[*dataGroupView]string) string {
	if key, ok := nodeMemo[n]; ok {
		return key
	}
	fieldNames, groupNames := nodeFieldNames(n)
	var children []string
	for _, child := range n.children {
		goName := fieldNames[child]
		if isDataDir(child.entry) {
			children = append(children, fmt.Sprintf("dir:%s:%s:%s:%s", child.entry.Name, goName, child.module.Name, computeViewShapeKey(child, nodeMemo, groupMemo)))
			continue
		}
		if child.entry.IsLeafList() {
			children = append(children, fmt.Sprintf("leaflist:%s:%s:%s:%s", child.entry.Name, goName, child.module.Name, leafTypeSignature(resolveLeafref(child.entry, child.entry.Type, 0))))
			continue
		}
		if child.entry.IsLeaf() {
			children = append(children, fmt.Sprintf("leaf:%s:%s:%s:%s", child.entry.Name, goName, child.module.Name, leafTypeSignature(resolveLeafref(child.entry, child.entry.Type, 0))))
		}
	}
	for _, group := range n.groups {
		children = append(children, fmt.Sprintf("group:%s:%s:%s", group.module.Name, groupNames[group], computeGroupShapeKey(group, n, nodeMemo, groupMemo)))
	}
	kind := "container"
	if n.entry.IsList() {
		kind = "list"
	} else if isPresence(n.entry) {
		kind = "presence_container"
	}
	var keys []string
	if n.entry.IsList() {
		keys = strings.Fields(n.entry.Key)
	}
	raw := fmt.Sprintf("kind=%s;name=%s;module=%s;keys=%s;children=[%s]", kind, n.entry.Name, n.module.Name, strings.Join(keys, ","), strings.Join(children, "|"))
	sum := sha256.Sum256([]byte(raw))
	key := hex.EncodeToString(sum[:])
	nodeMemo[n] = key
	return key
}

func nodeShapeKind(e *goyang.Entry) string {
	switch {
	case e.IsList():
		return "list"
	case isPresence(e):
		return "presence_container"
	default:
		return "container"
	}
}

// nodeShape represents one unique structural shape across a package.
type nodeShape struct {
	key            string
	kind           string
	name           string
	module         string
	namespace      string
	representative *dataNodeView
	instances      []shapeInstance
	candidates     []string
}

// shapeInstance records one concrete schema path where a shape appears.
type shapeInstance struct {
	node     *dataNodeView
	path     []pathSeg
	segments []string
}

// listInstance records one list data node that emits Key, Descriptor, and
// FlatRow. The row schema belongs to rowOwner, while the companions belong
// to topOwner.
type listInstance struct {
	node          *dataNodeView
	path          []pathSeg
	ancestorNodes []*dataNodeView
	ancestors     []ancestorList
	segments      []string
	shapeKey      string
	rowOwner      *packagePlan
	topOwner      *packagePlan
	candidates    []string
}

// topContainerInstance records a top-level container that emits a Descriptor.
type topContainerInstance struct {
	node       *dataNodeView
	path       []pathSeg
	segments   []string
	shapeKey   string
	topOwner   *packagePlan
	candidates []string
}

// ancestorList records one enclosing list level for nested-list flattening.
type ancestorList struct {
	node      *dataNodeView
	entryName string
	shapeKey  string
	keys      []string
}

func resolvePackageNaming(p *packagePlan, groupMemo map[*dataGroupView]string) map[*dataGroupView]*groupShape {
	orderedNodes := slices.Clone(p.nodes)
	slices.SortFunc(orderedNodes, func(a, b *dataNodeView) int { return strings.Compare(a.dataPath, b.dataPath) })
	nodeMemo := make(map[*dataNodeView]string)
	p.shapeKeyByNode = make(map[*dataNodeView]string, len(orderedNodes))
	shapeMap := make(map[string]*nodeShape)
	for _, node := range orderedNodes {
		key := computeViewShapeKey(node, nodeMemo, groupMemo)
		p.shapeKeyByNode[node] = key
		shape, ok := shapeMap[key]
		if !ok {
			shape = &nodeShape{
				key:            key,
				kind:           nodeShapeKind(node.entry),
				name:           node.entry.Name,
				module:         node.module.Name,
				namespace:      namespaceOf(node.entry),
				representative: node,
			}
			shapeMap[key] = shape
		}
		shape.instances = append(shape.instances, shapeInstance{
			node:     node,
			path:     node.path,
			segments: viewPathSegments(node.path),
		})
	}

	for _, shape := range shapeMap {
		slices.SortFunc(shape.instances, func(a, b shapeInstance) int {
			return strings.Compare(a.node.dataPath, b.node.dataPath)
		})
		var paths [][]string
		for _, instance := range shape.instances {
			paths = append(paths, instance.segments)
		}
		shape.candidates = candidateSuffixes(longestCommonSuffix(paths))
		p.shapes = append(p.shapes, shape)
	}
	slices.SortFunc(p.shapes, func(a, b *nodeShape) int { return strings.Compare(a.key, b.key) })

	groupShapes := make(map[string]*groupShape, len(p.groups))
	groupByView := make(map[*dataGroupView]*groupShape, len(p.groups))
	for _, group := range p.groups {
		key := computeGroupShapeKey(group.group, group.target, nodeMemo, groupMemo)
		shape := groupShapes[key]
		if shape == nil {
			shape = &groupShape{
				key:    key,
				module: group.module,
				target: group.target,
				group:  group.group,
				owner:  group.owner,
			}
			groupShapes[key] = shape
		}
		shape.targets = append(shape.targets, group.target)
		groupByView[group.group] = shape
	}
	p.groups = p.groups[:0]
	for _, shape := range groupShapes {
		slices.SortFunc(shape.targets, func(a, b *dataNodeView) int { return strings.Compare(a.dataPath, b.dataPath) })
		var paths [][]string
		for _, target := range shape.targets {
			paths = append(paths, viewPathSegments(target.path))
		}
		shape.candidates = candidateSuffixes(longestCommonSuffix(paths))
		for i := range shape.candidates {
			shape.candidates[i] += "Augment"
		}
		p.groups = append(p.groups, shape)
	}
	slices.SortFunc(p.groups, func(a, b *groupShape) int {
		if c := strings.Compare(a.target.dataPath, b.target.dataPath); c != 0 {
			return c
		}
		return strings.Compare(a.module.Name, b.module.Name)
	})

	var entities []*claimantEntity
	for _, shape := range p.shapes {
		shapeCopy := shape
		entities = append(entities, &claimantEntity{
			candidates: shapeCopy.candidates,
			getSymbols: func(base string) []claimSpec {
				return []claimSpec{
					{wanted: base, kind: kindStruct, depth: len(shapeCopy.instances[0].segments), tieBreak: shapeCopy.instances[0].node.dataPath, discriminator: shapeCopy.key},
					{wanted: base + "Schema", kind: kindSchema, depth: len(shapeCopy.instances[0].segments), tieBreak: shapeCopy.instances[0].node.dataPath, discriminator: "schema:" + shapeCopy.key},
				}
			},
		})
	}
	for _, group := range p.groups {
		groupCopy := group
		entities = append(entities, &claimantEntity{
			candidates: groupCopy.candidates,
			getSymbols: func(base string) []claimSpec {
				return []claimSpec{
					{wanted: base, kind: kindStruct, depth: len(groupCopy.target.path), tieBreak: groupCopy.target.dataPath, discriminator: "group:" + groupCopy.key},
					{wanted: base + "Schema", kind: kindSchema, depth: len(groupCopy.target.path), tieBreak: groupCopy.target.dataPath, discriminator: "group-schema:" + groupCopy.key},
				}
			},
		})
	}
	for _, li := range p.listInstances {
		liCopy := li
		entities = append(entities, &claimantEntity{
			candidates: liCopy.candidates,
			getSymbols: func(base string) []claimSpec {
				specs := []claimSpec{
					{wanted: base + "Key", kind: kindListKey, depth: len(liCopy.segments), tieBreak: liCopy.node.dataPath, discriminator: "key:" + liCopy.node.dataPath},
					{wanted: base + "Descriptor", kind: kindListDescriptor, depth: len(liCopy.segments), tieBreak: liCopy.node.dataPath, discriminator: "desc:" + liCopy.node.dataPath},
				}
				if len(liCopy.ancestorNodes) > 0 {
					specs = append(specs, claimSpec{wanted: base + "FlatRow", kind: kindListFlatRow, depth: len(liCopy.segments), tieBreak: liCopy.node.dataPath, discriminator: "flat:" + liCopy.node.dataPath})
				}
				return specs
			},
		})
	}
	for _, tc := range p.topContainers {
		tcCopy := tc
		entities = append(entities, &claimantEntity{
			candidates: tcCopy.candidates,
			getSymbols: func(base string) []claimSpec {
				return []claimSpec{{wanted: base + "Descriptor", kind: kindContainerDescriptor, depth: 1, tieBreak: tcCopy.node.dataPath, discriminator: "desc:" + tcCopy.node.dataPath}}
			},
		})
	}
	if p.module.Module != nil {
		for _, id := range p.module.Module.Identity {
			idName := id.Name
			entities = append(entities, &claimantEntity{
				candidates: []string{"Identity" + camel(idName)},
				getSymbols: func(base string) []claimSpec {
					return []claimSpec{{wanted: base, kind: kindIdentity, depth: 0, tieBreak: idName, discriminator: "identity:" + idName}}
				},
			})
		}
	}

	p.scope = resolveFairGrowth(entities)
	for _, group := range p.groups {
		group.name = p.scope.byPath["group:"+group.key]
		group.schemaName = p.scope.byPath["group-schema:"+group.key]
	}
	for _, group := range groupByView {
		group.name = p.scope.byPath["group:"+group.key]
		group.schemaName = p.scope.byPath["group-schema:"+group.key]
	}
	return groupByView
}

func (p *packagePlan) structName(shapeKey string) string {
	return p.scope.byPath[shapeKey]
}

func (p *packagePlan) schemaName(shapeKey string) string {
	return p.scope.byPath["schema:"+shapeKey]
}
