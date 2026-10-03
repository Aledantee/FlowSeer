// Package actiontrail records operator action attempts and completions on JetStream.
package actiontrail

import (
	"context"

	"go.aledante.io/FlowSeer/src/common/errs"
)

var (
	// ErrCodeUnavailable identifies an action attempt publish failure that prevents the handler from running.
	ErrCodeUnavailable = errs.NewCode("actiontrail/unavailable")
	// ErrCodeUnprepared identifies a call missing required principal or tenant in context.
	ErrCodeUnprepared = errs.NewCode("actiontrail/unprepared")
)

// Publisher places one event on a stream under a message id.
type Publisher interface {
	Publish(ctx context.Context, subject string, data []byte, msgID string) error
}
