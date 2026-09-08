// Package busattach brings up the edge's side of the bus: the credential
// AttachBus hands out, the embedded leaf node it authenticates with, and the
// loopback OTLP receiver the agent's own telemetry goes to.
//
// The order is dictated by what each step needs. AttachBus is a signed call,
// so it needs the identity; the leaf needs the credential and the cluster
// URLs that call returns; the receiver needs the leaf to publish into; and
// the agent's telemetry endpoint is the receiver's address, which does not
// exist until it has bound. Nothing here can be started early and wired up
// afterwards.
package busattach

import (
	"context"
	"log/slog"
	"time"

	connect "connectrpc.com/connect"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

// ErrCodeAttach identifies a failure to bring up the edge's bus side.
var ErrCodeAttach = errs.NewCode("agent/bus-attach")

// Attacher is the one EdgeService call this package makes. Satisfied by the
// generated client; an interface so the assembly can be tested without a hub.
type Attacher interface {
	AttachBus(context.Context, *connect.Request[edgev1.AttachBusRequest]) (*connect.Response[edgev1.AttachBusResponse], error)
}

// Config is what Attach needs beyond the call itself.
type Config struct {
	// StateDir holds the leaf's credential file and its JetStream buffer.
	// Must be absolute.
	StateDir string
	// EdgeID names the JetStream domain and the subject subtree.
	EdgeID string
	// TrustAnchors are the SPKI digests this edge pins central by, from its
	// enrollment. Required: an empty set would mean this edge trusts no
	// certificate, which is the safe direction but not a working one.
	TrustAnchors [][]byte
	// BufferMaxBytes and BufferMaxAge bound the local buffer. Zero takes
	// edgebus's defaults.
	BufferMaxBytes int64
	BufferMaxAge   time.Duration
	Logger         *slog.Logger
}

// Attachment is the running bus side: the leaf and the receiver, and the OTLP
// endpoint the agent's own telemetry is pointed at.
type Attachment struct {
	Leaf     *edgebus.Leaf
	Receiver *edgebus.Receiver
	// Endpoint is the loopback OTLP address the agent exports to. It is the
	// receiver's own bound address, so it exists only once the receiver has
	// bound and is different on every start.
	Endpoint string
}

// Close shuts the receiver down and then the leaf, in that order: the
// receiver publishes into the leaf, so closing the leaf first would leave a
// request in flight writing to a closed buffer.
func (a *Attachment) Close(ctx context.Context) {
	if a.Receiver != nil {
		_ = a.Receiver.Close(ctx)
	}
	if a.Leaf != nil {
		a.Leaf.Close()
	}
}

// Attach calls AttachBus, starts the leaf against what it returned, and binds
// the loopback receiver.
//
// The TLS the leaf dials with is the same pinned configuration the Connect
// client uses, built from the same anchors, so the two dialers cannot drift
// into trusting different things. An empty anchor set is refused here rather
// than passed down: edgebus.PinnedTLSConfig fails closed on one, which is the
// right direction, but the failure arrives as every hub connection being
// refused with no explanation rather than as a configuration error at start.
func Attach(ctx context.Context, client Attacher, cfg Config) (_ *Attachment, err error) {
	if len(cfg.TrustAnchors) == 0 {
		return nil, errs.New().Code(ErrCodeAttach).
			Msg("this edge has no trust anchors; it would refuse every certificate central presents")
	}

	response, err := client.AttachBus(ctx, connect.NewRequest(&edgev1.AttachBusRequest{}))
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeAttach).Msg("attach to the bus")
	}

	leaf, err := edgebus.StartLeaf(ctx, edgebus.LeafConfig{
		StateDir:        cfg.StateDir,
		EdgeID:          cfg.EdgeID,
		HubURLs:         response.Msg.GetClusterUrls(),
		CredentialsFile: response.Msg.GetUserCredential(),
		TLS:             edgebus.PinnedTLSConfig(cfg.TrustAnchors),
		// Periodic rather than per-message: this buffer is the edge's own
		// observability, and a record lost to a power cut is a gap in
		// history rather than a lost instruction.
		FsyncPolicy:    service.BusFsyncPeriodic,
		BufferMaxBytes: cfg.BufferMaxBytes,
		BufferMaxAge:   cfg.BufferMaxAge,
		Logger:         cfg.Logger,
	})
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeAttach).Msg("start the edge leaf node")
	}
	defer func() {
		if err != nil {
			leaf.Close()
		}
	}()

	receiver, err := edgebus.StartReceiver(leaf)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeAttach).Msg("start the loopback otlp receiver")
	}
	return &Attachment{Leaf: leaf, Receiver: receiver, Endpoint: receiver.Endpoint()}, nil
}
