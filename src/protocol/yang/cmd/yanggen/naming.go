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

// naming.go: deterministic YANG-to-Go identifier mangling with fair-growth
// shortest unique suffix resolution and a collision registry.

// camel converts a YANG identifier to an exported Go identifier
// using the shared goname rules.
func camel(name string) string {
	return goname.Exported(name)
}

// symbolKind defines the priority of a symbol when breaking ties
// for hash fallback. Lower values take precedence.
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

// candidateSuffixes returns the candidate names for segments from shortest
// (length 1) to longest (all segments).
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
	id         string
	candidates []string
	curIndex   int
	getSymbols func(base string) []claimSpec
}

type claimItem struct {
	entity        *claimantEntity
	wantedName    string
	kind          symbolKind
	depth         int
	tieBreak      string
	discriminator string
}

// resolveFairGrowth resolves clashing symbol names across entities
// by growing candidate suffixes fairly and falling back to X<hash> deterministically.
func resolveFairGrowth(entities []*claimantEntity) *nameScope {
	for {
		byName := make(map[string][]*claimItem)
		for _, ent := range entities {
			base := ent.candidates[ent.curIndex]
			for _, spec := range ent.getSymbols(base) {
				item := &claimItem{
					entity:        ent,
					wantedName:    spec.wanted,
					kind:          spec.kind,
					depth:         spec.depth,
					tieBreak:      spec.tieBreak,
					discriminator: spec.discriminator,
				}
				byName[spec.wanted] = append(byName[spec.wanted], item)
			}
		}

		toAdvance := make(map[*claimantEntity]bool)
		hasClashes := false
		canAnyGrow := false

		for _, items := range byName {
			if len(items) <= 1 {
				continue
			}
			hasClashes = true
			for _, it := range items {
				if it.entity.curIndex+1 < len(it.entity.candidates) {
					toAdvance[it.entity] = true
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
		base := ent.candidates[ent.curIndex]
		allFinal = append(allFinal, ent.getSymbols(base)...)
	}

	// Sort deterministically:
	// 1. Shallower nodes first
	// 2. Struct types before companions
	// 3. Wanted symbol name
	// 4. Tie-break discriminator/path
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

// nameScope allocates unique identifiers within one scope (a package
// or one struct's fields). Identical (identifier, path) pairs return
// the same name; a different path colliding on the identifier gets a
// deterministic "X<hash>" suffix derived from its path.
type nameScope struct {
	byName map[string]string // identifier -> owning path
	byPath map[string]string // path -> identifier
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
		// A hash collision on top of a name collision is vanishingly
		// unlikely but must still terminate deterministically.
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

// leafTypeSignature returns a deterministic string describing a YANG leaf type,
// including enum, bits, union, decimal64, and identity details.
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

// computeShapeKey computes the bottom-up shape key for a data node entry.
func computeShapeKey(e *goyang.Entry, moduleOf func(*goyang.Entry) string, memo map[*goyang.Entry]string) string {
	if k, ok := memo[e]; ok {
		return k
	}

	var kind string
	switch {
	case e.IsList():
		kind = "list"
	case isPresence(e):
		kind = "presence_container"
	default:
		kind = "container"
	}

	mod := moduleOf(e)
	var keys []string
	if e.IsList() && e.Key != "" {
		keys = strings.Fields(e.Key)
	}

	children := dataChildren(e)
	fieldScope := newNameScope()

	var childSigs []string
	for _, c := range children {
		goName := fieldScope.claim(camel(c.Name), c.Path())
		cMod := moduleOf(c)
		switch {
		case isDataDir(c):
			childKey := computeShapeKey(c, moduleOf, memo)
			childSigs = append(childSigs, fmt.Sprintf("dir:%s:%s:%s:%s", c.Name, goName, cMod, childKey))
		case c.IsLeafList():
			tSig := leafTypeSignature(resolveLeafref(c, c.Type, 0))
			childSigs = append(childSigs, fmt.Sprintf("leaflist:%s:%s:%s:%s", c.Name, goName, cMod, tSig))
		case c.IsLeaf():
			tSig := leafTypeSignature(resolveLeafref(c, c.Type, 0))
			childSigs = append(childSigs, fmt.Sprintf("leaf:%s:%s:%s:%s", c.Name, goName, cMod, tSig))
		}
	}

	raw := fmt.Sprintf("kind=%s;name=%s;module=%s;keys=%s;children=[%s]",
		kind, e.Name, mod, strings.Join(keys, ","), strings.Join(childSigs, "|"))
	sum := sha256.Sum256([]byte(raw))
	key := hex.EncodeToString(sum[:])
	memo[e] = key
	return key
}

// nodeShape represents one unique structural shape across the package.
type nodeShape struct {
	key            string
	kind           string
	name           string
	module         string
	namespace      string
	representative *goyang.Entry
	instances      []shapeInstance
	candidates     []string
}

// shapeInstance records one concrete schema path where a shape appears.
type shapeInstance struct {
	entry     *goyang.Entry
	path      []pathSeg
	ancestors []ancestorList
	segments  []string
}

// listInstance records one list data node that emits Key, Descriptor,
// and FlatRow.
type listInstance struct {
	entry      *goyang.Entry
	path       []pathSeg
	ancestors  []ancestorList
	segments   []string
	shapeKey   string
	candidates []string
}

// topContainerInstance records a top-level container that emits a Descriptor.
type topContainerInstance struct {
	entry      *goyang.Entry
	path       []pathSeg
	segments   []string
	shapeKey   string
	candidates []string
}

// collectShapesAndInstances walks the data tree of m.Entry and groups nodes by shape.
func collectShapesAndInstances(
	m *LoadedModule, moduleOf func(*goyang.Entry) string,
) (map[string]*nodeShape, []*nodeShape, []*listInstance, []*topContainerInstance, map[*goyang.Entry]string) {
	memo := make(map[*goyang.Entry]string)
	shapeMap := make(map[string]*nodeShape)
	var listInstances []*listInstance
	var topContainers []*topContainerInstance

	segFor := func(e *goyang.Entry) pathSeg {
		return pathSeg{name: e.Name, module: moduleOf(e), namespace: namespaceOf(e)}
	}

	var walk func(e *goyang.Entry, path []pathSeg, ancestors []ancestorList, segments []string)
	walk = func(e *goyang.Entry, path []pathSeg, ancestors []ancestorList, segments []string) {
		sk := computeShapeKey(e, moduleOf, memo)
		nodeSegs := append(append([]string{}, segments...), camel(e.Name))
		nodePath := append(append([]pathSeg{}, path...), segFor(e))

		shape, ok := shapeMap[sk]
		if !ok {
			var kind string
			switch {
			case e.IsList():
				kind = "list"
			case isPresence(e):
				kind = "presence_container"
			default:
				kind = "container"
			}
			shape = &nodeShape{
				key:            sk,
				kind:           kind,
				name:           e.Name,
				module:         moduleOf(e),
				namespace:      namespaceOf(e),
				representative: e,
			}
			shapeMap[sk] = shape
		}
		inst := shapeInstance{
			entry:     e,
			path:      nodePath,
			ancestors: ancestors,
			segments:  nodeSegs,
		}
		shape.instances = append(shape.instances, inst)

		if e.IsList() && len(strings.Fields(e.Key)) > 0 {
			listInstances = append(listInstances, &listInstance{
				entry:     e,
				path:      nodePath,
				ancestors: ancestors,
				segments:  nodeSegs,
				shapeKey:  sk,
			})
		} else if !e.IsList() && len(nodePath) == 1 {
			topContainers = append(topContainers, &topContainerInstance{
				entry:    e,
				path:     nodePath,
				segments: nodeSegs,
				shapeKey: sk,
			})
		}

		nextAncestors := ancestors
		if e.IsList() {
			nextAncestors = append(append([]ancestorList{}, ancestors...), ancestorList{
				entryName: e.Name,
				shapeKey:  sk,
				keys:      strings.Fields(e.Key),
			})
		}

		for _, c := range dataChildren(e) {
			if isDataDir(c) {
				walk(c, nodePath, nextAncestors, nodeSegs)
			}
		}
	}

	for _, c := range dataChildren(m.Entry) {
		if isDataDir(c) {
			walk(c, nil, nil, nil)
		}
	}

	var shapeList []*nodeShape
	for _, s := range shapeMap {
		slices.SortFunc(s.instances, func(a, b shapeInstance) int {
			return strings.Compare(a.entry.Path(), b.entry.Path())
		})
		var allSegs [][]string
		for _, inst := range s.instances {
			allSegs = append(allSegs, inst.segments)
		}
		common := longestCommonSuffix(allSegs)
		s.candidates = candidateSuffixes(common)
		shapeList = append(shapeList, s)
	}

	for _, li := range listInstances {
		li.candidates = candidateSuffixes(li.segments)
	}

	for _, tc := range topContainers {
		tc.candidates = candidateSuffixes(tc.segments)
	}

	return shapeMap, shapeList, listInstances, topContainers, memo
}

// resolvePackageNaming runs the pre-pass over all symbols in m to resolve names fairly.
func resolvePackageNaming(
	m *LoadedModule, moduleOf func(*goyang.Entry) string,
) (map[string]*nodeShape, *nameScope, []*nodeShape, []*listInstance, []*topContainerInstance, map[*goyang.Entry]string) {
	shapeMap, shapes, listInstances, topContainers, memo := collectShapesAndInstances(m, moduleOf)

	var entities []*claimantEntity

	for _, s := range shapes {
		sCopy := s
		ent := &claimantEntity{
			id:         "shape:" + sCopy.key,
			candidates: sCopy.candidates,
			getSymbols: func(base string) []claimSpec {
				return []claimSpec{
					{
						wanted:        base,
						kind:          kindStruct,
						depth:         len(sCopy.instances[0].segments),
						tieBreak:      sCopy.instances[0].entry.Path(),
						discriminator: sCopy.key,
					},
					{
						wanted:        base + "Schema",
						kind:          kindSchema,
						depth:         len(sCopy.instances[0].segments),
						tieBreak:      sCopy.instances[0].entry.Path(),
						discriminator: "schema:" + sCopy.key,
					},
				}
			},
		}
		entities = append(entities, ent)
	}

	for _, li := range listInstances {
		liCopy := li
		ent := &claimantEntity{
			id:         "list:" + liCopy.entry.Path(),
			candidates: liCopy.candidates,
			getSymbols: func(base string) []claimSpec {
				specs := []claimSpec{
					{
						wanted:        base + "Key",
						kind:          kindListKey,
						depth:         len(liCopy.segments),
						tieBreak:      liCopy.entry.Path(),
						discriminator: "key:" + liCopy.entry.Path(),
					},
					{
						wanted:        base + "Descriptor",
						kind:          kindListDescriptor,
						depth:         len(liCopy.segments),
						tieBreak:      liCopy.entry.Path(),
						discriminator: "desc:" + liCopy.entry.Path(),
					},
				}
				if len(liCopy.ancestors) > 0 {
					specs = append(specs, claimSpec{
						wanted:        base + "FlatRow",
						kind:          kindListFlatRow,
						depth:         len(liCopy.segments),
						tieBreak:      liCopy.entry.Path(),
						discriminator: "flat:" + liCopy.entry.Path(),
					})
				}
				return specs
			},
		}
		entities = append(entities, ent)
	}

	for _, tc := range topContainers {
		tcCopy := tc
		ent := &claimantEntity{
			id:         "container:" + tcCopy.entry.Path(),
			candidates: tcCopy.candidates,
			getSymbols: func(base string) []claimSpec {
				return []claimSpec{
					{
						wanted:        base + "Descriptor",
						kind:          kindContainerDescriptor,
						depth:         1,
						tieBreak:      tcCopy.entry.Path(),
						discriminator: "desc:" + tcCopy.entry.Path(),
					},
				}
			},
		}
		entities = append(entities, ent)
	}

	if m.Module != nil {
		for _, id := range m.Module.Identity {
			idName := id.Name
			ent := &claimantEntity{
				id:         "identity:" + idName,
				candidates: []string{"Identity" + camel(idName)},
				getSymbols: func(base string) []claimSpec {
					return []claimSpec{
						{
							wanted:        base,
							kind:          kindIdentity,
							depth:         0,
							tieBreak:      idName,
							discriminator: "identity:" + idName,
						},
					}
				},
			}
			entities = append(entities, ent)
		}
	}

	scope := resolveFairGrowth(entities)
	return shapeMap, scope, shapes, listInstances, topContainers, memo
}
