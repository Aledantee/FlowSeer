package projector

import (
	"context"
	"time"
)

// SetWaitHook sets an injected wait function for testing the Run lifecycle.
func (p *Projector) SetWaitHook(fn func(ctx context.Context, d time.Duration) error) {
	p.wait = fn
}

// OwnedRelations returns every object type and relation owned with an access source.
// The returned map and slices may be changed without affecting the projector.
func OwnedRelations() map[string][]string {
	result := make(map[string][]string, len(ownedRelations))
	for objectType, relations := range ownedRelations {
		for relation := range relations {
			result[objectType] = append(result[objectType], relation)
		}
	}
	return result
}
