package vswitch

import (
	"strings"
)

// LayerRetention records whether one capability layer's runtime state was
// retained across Derive, and if not, the dependency that differed.
type LayerRetention struct {
	Kept       bool
	Difference string
}

// Retention summarizes the retention outcome across all capability layers.
type Retention struct {
	STP         LayerRetention
	LoopProtect LayerRetention
	LAG         LayerRetention
	Mcast       LayerRetention
	Routing     LayerRetention
	Traffic     LayerRetention
}

// AllKeptRetention returns a Retention value with every layer marked kept.
func AllKeptRetention() Retention {
	return Retention{
		STP:         LayerRetention{Kept: true},
		LoopProtect: LayerRetention{Kept: true},
		LAG:         LayerRetention{Kept: true},
		Mcast:       LayerRetention{Kept: true},
		Routing:     LayerRetention{Kept: true},
		Traffic:     LayerRetention{Kept: true},
	}
}

// diffDependency parses curKey and nextKey and returns the canonical name of the
// dependency that differed ("config", "port-state", "resolved-speed", "member-state",
// "mac", or "system-id").
func diffDependency(curKey, nextKey string) string {
	if curKey == nextKey {
		return ""
	}
	if curKey == "" || nextKey == "" {
		return "config"
	}

	curSec := parseKeySections(curKey)
	nextSec := parseKeySections(nextKey)

	for _, dep := range []string{"port-state", "resolved-speed", "member-state", "mac", "system-id", "config"} {
		if curSec[dep] != nextSec[dep] {
			return dep
		}
	}
	return "config"
}

func parseKeySections(key string) map[string]string {
	sections := make(map[string]string)
	if key == "" {
		return sections
	}

	for _, line := range strings.Split(key, "\n") {
		for _, tag := range []string{"config", "port-state", "resolved-speed", "member-state", "mac", "system-id"} {
			prefix := tag + "="
			if strings.HasPrefix(line, prefix) {
				sections[tag] = strings.TrimPrefix(line, prefix)
				break
			}
		}
	}
	return sections
}
