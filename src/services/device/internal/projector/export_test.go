package projector

import (
	"context"
	"time"
)

// SetWaitHook sets an injected wait function for testing the Run lifecycle.
func (p *Projector) SetWaitHook(fn func(ctx context.Context, d time.Duration) error) {
	p.wait = fn
}
