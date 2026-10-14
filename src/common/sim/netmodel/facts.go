package netmodel

import (
	"strings"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
)

type factKey struct {
	id      string
	display string
	scope   analysis.Scope
}

type factConflict struct {
	key    factKey
	values []string
}

func resolveFacts[T any](rows []T, identify func(T) (factKey, string, bool)) (map[string]T, []factConflict) {
	keys := make(map[string]factKey)
	groups := make(map[string]map[string]T)
	for _, row := range rows {
		key, value, ok := identify(row)
		if !ok {
			continue
		}
		keys[key.id] = key
		if groups[key.id] == nil {
			groups[key.id] = make(map[string]T)
		}
		groups[key.id][value] = row
	}

	resolved := make(map[string]T)
	var conflicts []factConflict
	for _, id := range sortedKeys(groups) {
		variants := groups[id]
		values := sortedKeys(variants)
		if len(values) == 1 {
			resolved[id] = variants[values[0]]
			continue
		}
		conflicts = append(conflicts, factConflict{key: keys[id], values: values})
	}
	return resolved, conflicts
}

func (c factConflict) detail() string {
	return "reported values disagree: " + strings.Join(c.values, "; ")
}
