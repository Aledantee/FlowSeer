package auditapi

import (
	"context"

	"github.com/nats-io/nats.go/jetstream"
)

// JetStreamPublisher is the production [Publisher] over central's JetStream
// connection. The message id becomes the stream's Nats-Msg-Id, so the audit
// stream's duplicate window stores a resubmitted event once, and Publish
// returns only after the stream acknowledges the write.
type JetStreamPublisher struct {
	JS jetstream.JetStream
}

// Publish places data on subject under msgID and waits for the stream's ack.
func (p JetStreamPublisher) Publish(ctx context.Context, subject string, data []byte, msgID string) error {
	_, err := p.JS.Publish(ctx, subject, data, jetstream.WithMsgID(msgID))
	return err
}
