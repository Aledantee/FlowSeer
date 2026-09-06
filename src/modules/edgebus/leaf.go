package edgebus

import (
	"context"
	"crypto/tls"
	"os"
	"path/filepath"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
)

// ErrCodeLeaf identifies a failure starting or operating the leaf node.
var ErrCodeLeaf = errs.NewCode("edgebus/leaf")

// LeafConfig declares the leaf node an edge embeds. Construct with keyed
// fields.
type LeafConfig struct {
	// StateDir holds the credential file and the JetStream store. Must be
	// absolute.
	StateDir string
	// EdgeID is the edge's own id; it names the JetStream domain and the
	// subject subtree.
	EdgeID string
	// Tenant is the account the credential was minted in. Empty means
	// DefaultTenant.
	Tenant string
	// HubURLs are the hub listeners to dial, in preference order.
	HubURLs []string
	// Credentials are what AttachBus returned.
	Credentials EdgeCredentials
	// TLS is the client configuration for a wss hub, normally
	// PinnedTLSConfig with the edge's anchors. Nil dials plain ws.
	TLS *tls.Config
	// FsyncPolicy must be declared; an edge buffer runs BusFsyncPeriodic,
	// since a record lost to a power cut here is a gap in history.
	FsyncPolicy   service.BusFsyncPolicy
	FsyncInterval *time.Duration
	// BufferMaxBytes and BufferMaxAge bound the local buffer; the oldest
	// records go first. Zero means 256 MiB and seven days.
	BufferMaxBytes int64
	BufferMaxAge   time.Duration
	// StartupTimeout bounds server readiness. Zero means ten seconds.
	StartupTimeout time.Duration
}

// Leaf is the running leaf node and the edge's own connection to it.
type Leaf struct {
	log    *quietLogger
	cfg    LeafConfig
	tenant string
	server *server.Server
	conn   *nats.Conn
	js     jetstream.JetStream
}

const (
	defaultBufferBytes = 256 << 20
	defaultBufferAge   = 7 * 24 * time.Hour
)

// StartLeaf starts the leaf node, dials the hub, and creates the local
// buffer. It returns once the local server is ready; the hub link comes up
// in the background and is retried for as long as the leaf runs, so a
// publish succeeds against the local buffer whether or not the hub is
// reachable. The returned Leaf must be closed.
func StartLeaf(ctx context.Context, cfg LeafConfig) (_ *Leaf, err error) {
	if cfg.StateDir == "" || !filepath.IsAbs(cfg.StateDir) {
		return nil, errs.New().Code(ErrCodeConfig).Msg("leaf state directory must be absolute")
	}
	if cfg.EdgeID == "" {
		return nil, errs.New().Code(ErrCodeConfig).Msg("leaf needs the edge id")
	}
	if len(cfg.HubURLs) == 0 {
		return nil, errs.New().Code(ErrCodeConfig).Msg("leaf needs at least one hub url")
	}
	tenant := cfg.Tenant
	if tenant == "" {
		tenant = DefaultTenant
	}
	var fsync server.Options
	if err := applyFsync(&fsync, cfg.FsyncPolicy, cfg.FsyncInterval); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return nil, errs.From(err).Code(ErrCodeLeaf).Msg("create leaf state directory")
	}
	creds, err := cfg.Credentials.CredsFile()
	if err != nil {
		return nil, err
	}
	credsPath := filepath.Join(cfg.StateDir, "hub.creds")
	if err := os.WriteFile(credsPath, creds, 0o600); err != nil {
		return nil, errs.From(err).Code(ErrCodeLeaf).Msg("store hub credentials")
	}
	urls, err := parseURLs(cfg.HubURLs)
	if err != nil {
		return nil, err
	}

	opts := &server.Options{
		ServerName:             "flowseer-edge-" + cfg.EdgeID,
		DontListen:             true,
		NoSigs:                 true,
		NoLog:                  true,
		JetStream:              true,
		JetStreamDomain:        EdgeDomain(cfg.EdgeID),
		StoreDir:               filepath.Join(cfg.StateDir, "jetstream"),
		DisableJetStreamBanner: true,
		LeafNode: server.LeafNodeOpts{
			Remotes: []*server.RemoteLeafOpts{{
				URLs:        urls,
				Credentials: credsPath,
				TLSConfig:   cfg.TLS,
			}},
		},
	}
	opts.SyncAlways = fsync.SyncAlways
	opts.SyncInterval = fsync.SyncInterval

	srv, err := server.NewServer(opts)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeLeaf).Msg("construct leaf server")
	}
	logger := newQuietLogger()
	srv.SetLoggerV2(logger, false, false, false)
	srv.Start()
	leaf := &Leaf{cfg: cfg, tenant: tenant, server: srv, log: logger}
	defer func() {
		if err != nil {
			leaf.Close()
		}
	}()

	timeout := cfg.StartupTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	if !srv.ReadyForConnections(timeout) {
		return nil, errs.New().Code(ErrCodeLeaf).Attr("server_error", logger.lastError()).Msg("leaf server did not become ready")
	}
	leaf.conn, err = nats.Connect("nats://leaf", nats.InProcessServer(srv), nats.Name("flowseer-edge"))
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeLeaf).Msg("connect to the leaf server")
	}
	leaf.js, err = jetstream.NewWithDomain(leaf.conn, EdgeDomain(cfg.EdgeID))
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeLeaf).Msg("create leaf JetStream context")
	}

	maxBytes := cfg.BufferMaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultBufferBytes
	}
	maxAge := cfg.BufferMaxAge
	if maxAge <= 0 {
		maxAge = defaultBufferAge
	}
	// The buffer covers the two branches an edge publishes records on and
	// not the whole subtree: the hub's source consumer delivers on the
	// source branch, and a consumer may not deliver into the subjects of
	// the stream it reads.
	subtree := EdgeSubtree(tenant, cfg.EdgeID)
	if _, err := leaf.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      EdgeBufferStream,
		Subjects:  []string{subtree + ".otel.>", subtree + ".ingest.>"},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		Discard:   jetstream.DiscardOld,
		MaxBytes:  maxBytes,
		MaxAge:    maxAge,
	}); err != nil {
		return nil, errs.From(err).Code(ErrCodeLeaf).Msg("create edge buffer stream")
	}
	return leaf, nil
}

// Publish writes one record into the local buffer and returns once the
// buffer holds it. The hub receives it whenever the link is up; a message
// id, when given, deduplicates a re-publish inside the stream's window.
func (l *Leaf) Publish(ctx context.Context, subject string, data []byte, msgID string) error {
	var opts []jetstream.PublishOpt
	if msgID != "" {
		opts = append(opts, jetstream.WithMsgID(msgID))
	}
	if _, err := l.js.Publish(ctx, subject, data, opts...); err != nil {
		return errs.From(err).Code(ErrCodeLeaf).Attr("subject", subject).Msg("publish to the edge buffer")
	}
	return nil
}

// Subject returns the subject under this edge's subtree for a logical name.
func (l *Leaf) Subject(name string) string {
	return EdgeSubtree(l.tenant, l.cfg.EdgeID) + "." + name
}

// OTelSubject returns where this edge publishes one signal.
func (l *Leaf) OTelSubject(signal OTelSignal) string {
	return OTelSubject(l.tenant, l.cfg.EdgeID, signal)
}

// Connection is the edge's own connection to its leaf server.
func (l *Leaf) Connection() *nats.Conn { return l.conn }

// JetStream is the edge's context on its own domain.
func (l *Leaf) JetStream() jetstream.JetStream { return l.js }

// HubConnected reports whether the leaf link to the hub is up right now.
func (l *Leaf) HubConnected() bool { return l.server.NumLeafNodes() > 0 }

// Close closes the connection and stops the server, waiting for its
// shutdown.
func (l *Leaf) Close() {
	if l.conn != nil {
		l.conn.Close()
		l.conn = nil
	}
	if l.server != nil {
		l.server.Shutdown()
		l.server.WaitForShutdown()
		l.server = nil
	}
}
