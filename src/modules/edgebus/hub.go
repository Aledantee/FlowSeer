package edgebus

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
)

// ErrCodeHub identifies a failure starting or operating the hub.
var ErrCodeHub = errs.NewCode("edgebus/hub")

// HubConfig declares the hub central embeds. Construct with keyed fields.
type HubConfig struct {
	// StateDir holds the keys and the JetStream store. Must be absolute.
	StateDir string
	// Tenant names the one account this hub runs. Empty means DefaultTenant.
	Tenant string
	// FsyncPolicy must be declared; the hub is the journal's authority, so
	// central declares BusFsyncPerMessage.
	FsyncPolicy   service.BusFsyncPolicy
	FsyncInterval *time.Duration
	// ListenHost and ListenPort are the WebSocket listener the edges' leaf
	// nodes dial. Port 0 disables the listener; -1 picks a free port.
	ListenHost string
	ListenPort int
	// TLS serves the listener with Connect's certificate. Nil serves plain
	// WebSocket, for tests and a loopback lab.
	TLS *tls.Config
	// MaxStoreBytes bounds the JetStream store. Zero means 1 GiB.
	MaxStoreBytes int64
	// AuditDuplicateWindow is how long the audit stream remembers an event
	// id; a re-delivered record inside it is stored once. Zero means ten
	// minutes, longer than any edge re-send.
	AuditDuplicateWindow time.Duration
	// StartupTimeout bounds server readiness. Zero means ten seconds.
	StartupTimeout time.Duration
}

// Hub is the running hub: the server, central's own connection into the
// tenant account, and the stores the device service writes.
type Hub struct {
	log    *quietLogger
	cfg    HubConfig
	keys   *hubKeys
	opts   *server.Options
	server *server.Server
	conn   *nats.Conn
	js     jetstream.JetStream
	tenant string
}

const defaultHubStoreBytes = 1 << 30

// StartHub starts the hub and creates the stores it owns. The returned Hub
// must be closed.
func StartHub(ctx context.Context, cfg HubConfig) (_ *Hub, err error) {
	if cfg.StateDir == "" || !filepath.IsAbs(cfg.StateDir) {
		return nil, errs.New().Code(ErrCodeConfig).Msg("hub state directory must be absolute")
	}
	tenant := cfg.Tenant
	if tenant == "" {
		tenant = DefaultTenant
	}
	keys, err := loadOrCreateKeys(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	operator, err := keys.operatorJWT()
	if err != nil {
		return nil, err
	}
	resolver := &server.MemAccResolver{}
	for _, account := range []struct {
		pair      interface{ PublicKey() (string, error) }
		name      string
		jetStream bool
	}{{keys.system, "SYS", false}, {keys.tenant, tenant, true}} {
		pub, err := account.pair.PublicKey()
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeKeys).Msg("read account public key")
		}
		var encoded string
		if account.jetStream {
			encoded, err = keys.accountJWT(keys.tenant, account.name, true)
		} else {
			encoded, err = keys.accountJWT(keys.system, account.name, false)
		}
		if err != nil {
			return nil, err
		}
		if err := resolver.Store(pub, encoded); err != nil {
			return nil, errs.From(err).Code(ErrCodeHub).Msg("store account claims")
		}
	}

	maxStore := cfg.MaxStoreBytes
	if maxStore <= 0 {
		maxStore = defaultHubStoreBytes
	}
	systemPub, err := publicKey(keys.system)
	if err != nil {
		return nil, err
	}
	opts := &server.Options{
		SystemAccount:          systemPub,
		ServerName:             "flowseer-hub",
		DontListen:             true,
		NoSigs:                 true,
		NoLog:                  true,
		JetStream:              true,
		JetStreamDomain:        HubDomain,
		JetStreamMaxStore:      maxStore,
		StoreDir:               filepath.Join(cfg.StateDir, "jetstream"),
		DisableJetStreamBanner: true,
		TrustedOperators:       []*jwt.OperatorClaims{operator},
		AccountResolver:        resolver,
	}
	if err := applyFsync(opts, cfg.FsyncPolicy, cfg.FsyncInterval); err != nil {
		return nil, err
	}
	if cfg.ListenPort != 0 {
		host := cfg.ListenHost
		if host == "" {
			host = "127.0.0.1"
		}
		// A WebSocket leaf connection needs the leaf-node subsystem enabled,
		// which the server keys on a leaf port; the plain listener stays on
		// loopback and is not what an edge dials.
		opts.LeafNode = server.LeafNodeOpts{Host: "127.0.0.1", Port: -1}
		opts.Websocket = server.WebsocketOpts{Host: host, Port: cfg.ListenPort, TLSConfig: cfg.TLS, NoTLS: cfg.TLS == nil}
	}

	srv, err := server.NewServer(opts)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeHub).Msg("construct hub server")
	}
	logger := newQuietLogger()
	srv.SetLoggerV2(logger, false, false, false)
	srv.Start()
	hub := &Hub{cfg: cfg, keys: keys, opts: opts, server: srv, tenant: tenant, log: logger}
	defer func() {
		if err != nil {
			hub.Close()
		}
	}()

	timeout := cfg.StartupTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	if !srv.ReadyForConnections(timeout) {
		return nil, errs.New().Code(ErrCodeHub).Attr("server_error", logger.lastError()).Msg("hub server did not become ready")
	}

	internal, err := keys.mintUser("central", jwt.Permissions{})
	if err != nil {
		return nil, err
	}
	hub.conn, err = nats.Connect("nats://hub",
		nats.InProcessServer(srv),
		nats.UserJWTAndSeed(internal.UserJWT, internal.Seed),
		nats.Name("flowseer-central"),
	)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeHub).Msg("connect central to the hub")
	}
	hub.js, err = jetstream.NewWithDomain(hub.conn, HubDomain)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeHub).Msg("create hub JetStream context")
	}
	if err := hub.createStores(ctx); err != nil {
		return nil, err
	}
	return hub, nil
}

func (h *Hub) createStores(ctx context.Context) error {
	for _, bucket := range []string{LaneBucket, EdgeBucket} {
		if _, err := h.js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
			Bucket:  bucket,
			Storage: jetstream.FileStorage,
			History: 1,
		}); err != nil {
			return errs.From(err).Code(ErrCodeHub).Attr("bucket", bucket).Msg("create key-value bucket")
		}
	}
	window := h.cfg.AuditDuplicateWindow
	if window == 0 {
		window = 10 * time.Minute
	}
	if _, err := h.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       AuditStream,
		Subjects:   []string{fmt.Sprintf("flowseer.%s.audit.device.>", h.tenant)},
		Storage:    jetstream.FileStorage,
		Retention:  jetstream.LimitsPolicy,
		Duplicates: window,
	}); err != nil {
		return errs.From(err).Code(ErrCodeHub).Msg("create audit stream")
	}
	return nil
}

// JetStream is central's own JetStream context on the hub domain, for the
// journal buckets and the audit stream.
func (h *Hub) JetStream() jetstream.JetStream { return h.js }

// Connection is central's own connection into the tenant account.
func (h *Hub) Connection() *nats.Conn { return h.conn }

// Tenant is the account name the hub runs.
func (h *Hub) Tenant() string { return h.tenant }

// ListenURL is what an edge's leaf remote dials, or empty when the listener
// is disabled.
func (h *Hub) ListenURL() string {
	if h.opts.Websocket.Port == 0 {
		return ""
	}
	scheme := "ws"
	if h.cfg.TLS != nil {
		scheme = "wss"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, h.opts.Websocket.Host, h.opts.Websocket.Port)
}

// ListenPort is the port the WebSocket listener bound, or zero when the
// listener is disabled; a host that asked for a free port reads it here.
func (h *Hub) ListenPort() int { return h.opts.Websocket.Port }

// MintEdgeUser mints the credential AttachBus returns for one edge: a user
// in the tenant account confined to that edge's subtree, its own JetStream
// API and inbox, and the hub's reply inboxes.
func (h *Hub) MintEdgeUser(edgeID string) (EdgeCredentials, error) {
	return h.keys.mintUser("edge-"+edgeID, edgePermissions(h.tenant, edgeID))
}

// AttachEdge creates the hub stream that sources this edge's buffer across
// the leaf link, one stream per edge so that which edge a record came from
// is a fact of the stream it sits in. Idempotent.
func (h *Hub) AttachEdge(ctx context.Context, edgeID string) error {
	// The edge's consumer delivers into the hub on the source branch of the
	// edge's own subtree, the one place the edge's leaf may publish; the
	// default $JS.S prefix would be refused by that permission, and a branch
	// inside the buffer stream's own subjects would be refused by JetStream,
	// which never lets a consumer deliver into the stream it reads.
	if _, err := h.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      HubEdgeStream(edgeID),
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		Sources: []*jetstream.StreamSource{{
			Name: EdgeBufferStream,
			External: &jetstream.ExternalStream{
				APIPrefix:     "$JS." + EdgeDomain(edgeID) + ".API",
				DeliverPrefix: EdgeSubtree(h.tenant, edgeID) + ".source",
			},
		}},
	}); err != nil {
		return errs.From(err).Code(ErrCodeHub).Attr("edge", edgeID).Msg("create the edge's hub stream")
	}
	return nil
}

// EdgeStream returns the hub stream that sources one edge's buffer.
func (h *Hub) EdgeStream(ctx context.Context, edgeID string) (jetstream.Stream, error) {
	stream, err := h.js.Stream(ctx, HubEdgeStream(edgeID))
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeHub).Attr("edge", edgeID).Msg("look up the edge's hub stream")
	}
	return stream, nil
}

// LeafCount reports how many leaf nodes are connected.
func (h *Hub) LeafCount() int { return h.server.NumLeafNodes() }

// Close closes central's connection and stops the server, waiting for its
// shutdown.
func (h *Hub) Close() {
	if h.conn != nil {
		h.conn.Close()
		h.conn = nil
	}
	if h.server != nil {
		h.server.Shutdown()
		h.server.WaitForShutdown()
		h.server = nil
	}
}

// parseURLs turns the strings a configuration carries into what a leaf
// remote takes.
func parseURLs(raw []string) ([]*url.URL, error) {
	urls := make([]*url.URL, 0, len(raw))
	for _, s := range raw {
		u, err := url.Parse(s)
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeConfig).Attr("url", s).Msg("parse hub url")
		}
		urls = append(urls, u)
	}
	return urls, nil
}

// quietLogger drops the server's own log lines and keeps only the last
// error it reported, so a server that fails to become ready can say why
// through the package's own error instead of a log the host never sees.
type quietLogger struct {
	mu    sync.Mutex
	last  string
	lines []string
}

func newQuietLogger() *quietLogger { return &quietLogger{} }

func (l *quietLogger) record(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.last = fmt.Sprintf(format, args...)
	if len(l.lines) < 64 {
		l.lines = append(l.lines, l.last)
	}
}

func (l *quietLogger) lastError() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.last
}

func (*quietLogger) Noticef(string, ...any)              {}
func (l *quietLogger) Warnf(format string, args ...any)  { l.record(format, args...) }
func (l *quietLogger) Fatalf(format string, args ...any) { l.record(format, args...) }
func (l *quietLogger) Errorf(format string, args ...any) { l.record(format, args...) }
func (*quietLogger) Debugf(string, ...any)               {}
func (*quietLogger) Tracef(string, ...any)               {}
