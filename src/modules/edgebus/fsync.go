package edgebus

import (
	"time"

	"github.com/nats-io/nats-server/v2/server"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
)

// ErrCodeConfig identifies a hub or leaf configuration the package refuses
// before any store is opened.
var ErrCodeConfig = errs.NewCode("edgebus/config")

// applyFsync sets the server's sync options from a declared policy, through
// the same normalization the process-local bus uses, so the hub, the leaf,
// and the local bus never tell different durability stories.
func applyFsync(opts *server.Options, policy service.BusFsyncPolicy, interval *time.Duration) error {
	resolved, err := service.NormalizeFsync(policy, interval)
	if err != nil {
		return errs.From(err).Code(ErrCodeConfig).Msg("normalize fsync policy")
	}
	opts.SyncAlways = policy == service.BusFsyncPerMessage
	opts.SyncInterval = resolved
	return nil
}
