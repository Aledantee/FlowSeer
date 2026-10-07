package projector

import (
	"context"
	"time"
)

// SetWaitHook sets an injected wait function for testing the Run lifecycle.
func (p *Projector) SetWaitHook(fn func(ctx context.Context, d time.Duration) error) {
	p.wait = fn
}

// OwnedRelations returns each owned relation and whether it requires an access source.
// The returned maps may be changed without affecting the projector.
func OwnedRelations() map[string]map[string]bool {
	result := make(map[string]map[string]bool, len(ownedRelations))
	for objectType, relations := range ownedRelations {
		result[objectType] = make(map[string]bool, len(relations))
		for relation, requiresAccess := range relations {
			result[objectType][relation] = requiresAccess
		}
	}
	return result
}
