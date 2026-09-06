package edgebus

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
)

// ErrCodeHub identifies a failure starting or operating the hub.
var ErrCodeHub = errs.NewCode("edgebus/hub")

// HubConfig declares the hub central embeds. Construct with keyed fields.
type HubConfig struct {
	// StateDir holds the keys and the JetStream store. Must be absolute.
	StateDir string
	// FsyncPolicy must be declared; the hub is the journal's authority, so
	// central declares BusFsyncPerMessage.
	FsyncPolicy   service.BusFsyncPolicy
	FsyncInterval *time.Duration
	// ListenHost and ListenPort are the WebSocket listener the edges' leaf
	// nodes dial. Port 0 disables the listener; -1 picks a free port. A
	// non-loopback host requires TLS.
	ListenHost string
	ListenPort int
	// TLS serves the listener with Connect's certificate. Nil serves plain
	// WebSocket, allowed only on a loopback host for tests and a lab.
	TLS *tls.Config
	// MaxStoreBytes bounds the whole JetStream store. Zero means 1 GiB. The
	// edge and central account budgets below default to half each so the
	// two never exceed it.
	MaxStoreBytes int64
	// EdgeBudgetBytes and CentralBudgetBytes are the per-account disk
	// ceilings. Zero means half of MaxStoreBytes each: telemetry volume in
	// the edge account cannot starve the journal in the central account.
	EdgeBudgetBytes    int64
	CentralBudgetBytes int64
	// EdgeStreamMaxBytes and EdgeStreamMaxAge bound each per-edge source
	// stream. Zero means 64 MiB and 24 hours; the age must comfortably
	// exceed any forwarder outage, since a stream emptied by age re-sources
	// its edge's whole buffer on a hub restart.
	EdgeStreamMaxBytes int64
	EdgeStreamMaxAge   time.Duration
	// AuditMaxBytes bounds the audit stream within the central budget. Zero
	// means 256 MiB.
	AuditMaxBytes int64
	// AuditDuplicateWindow is how long the audit stream remembers an event
	// id; a re-delivered record inside it is stored once. Zero means ten
	// minutes, longer than any edge re-send.
	AuditDuplicateWindow time.Duration
	// StartupTimeout bounds server readiness. Zero means ten seconds.
	StartupTimeout time.Duration
	// Logger receives the embedded server's own warnings and errors, so a
	// line like "JetStream out of space" reaches an operator. Nil discards
	// them.
	Logger *slog.Logger
}

// Hub is the running hub. It holds one connection into each data account:
// central's own for the journal buckets and the audit stream, and an
// edge-account connection for the per-edge source streams the forwarder
// reads. The two accounts are the security boundary of finding 1: an edge
// credential lives in the edge account and cannot address a central stream
// even through a server-reflected publish.
type Hub struct {
	log    *quietLogger
	cfg    HubConfig
	keys   *hubKeys
	opts   *server.Options
	server *server.Server

	central   *nats.Conn
	centralJS jetstream.JetStream
	edge      *nats.Conn
	edgeJS    jetstream.JetStream

	edgeAccountJWT string

	mu       sync.Mutex
	attachMu sync.Mutex
	closed   bool
}

const (
	defaultHubStoreBytes     = 1 << 30
	defaultEdgeStreamBytes   = 64 << 20
	defaultEdgeStreamMaxAge  = 24 * time.Hour
	defaultAuditStreamBytes  = 256 << 20
	defaultAuditDedupeWindow = 10 * time.Minute
)

// StartHub starts the hub and creates the stores central owns. The returned
// Hub must be closed.
func StartHub(ctx context.Context, cfg HubConfig) (_ *Hub, err error) {
	if cfg.StateDir == "" || !filepath.IsAbs(cfg.StateDir) {
		return nil, errs.New().Code(ErrCodeConfig).Msg("hub state directory must be absolute")
	}
	if cfg.ListenPort != 0 && cfg.TLS == nil && !isLoopback(cfg.ListenHost) {
		return nil, errs.New().Code(ErrCodeConfig).Attr("host", cfg.ListenHost).Msg("a non-loopback listener must serve TLS")
	}
	// Validate the fsync policy before any key is written, so an undeclared
	// policy leaves the state directory untouched, the local bus's rule.
	if _, err := service.NormalizeFsync(cfg.FsyncPolicy, cfg.FsyncInterval); err != nil {
		return nil, errs.From(err).Code(ErrCodeConfig).Msg("normalize hub fsync policy")
	}
	keys, err := loadOrCreateKeys(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	operator, err := keys.operatorJWT()
	if err != nil {
		return nil, err
	}

	maxStore := cfg.MaxStoreBytes
	if maxStore <= 0 {
		maxStore = defaultHubStoreBytes
	}
	edgeBudget := cfg.EdgeBudgetBytes
	if edgeBudget <= 0 {
		edgeBudget = maxStore / 2
	}
	centralBudget := cfg.CentralBudgetBytes
	if centralBudget <= 0 {
		centralBudget = maxStore / 2
	}

	resolver := &server.MemAccResolver{}
	systemJWT, err := keys.accountJWT(keys.system, "SYS", 0)
	if err != nil {
		return nil, err
	}
	edgeJWT, err := keys.accountJWT(keys.edge, "EDGE", edgeBudget)
	if err != nil {
		return nil, err
	}
	centralJWT, err := keys.accountJWT(keys.central, "CENTRAL", centralBudget)
	if err != nil {
		return nil, err
	}
	for pair, encoded := range map[nkeysPublic]string{keys.system: systemJWT, keys.edge: edgeJWT, keys.central: centralJWT} {
		pub, err := pair.PublicKey()
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeKeys).Msg("read account public key")
		}
		if err := resolver.Store(pub, encoded); err != nil {
			return nil, errs.From(err).Code(ErrCodeHub).Msg("store account claims")
		}
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
	logger := newQuietLogger(cfg.Logger)
	srv.SetLoggerV2(logger, false, false, false)
	srv.Start()
	hub := &Hub{cfg: cfg, keys: keys, opts: opts, server: srv, log: logger, edgeAccountJWT: edgeJWT}
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

	hub.central, hub.centralJS, err = hub.connectAccount(srv, keys.central, centralJWT, "central-journal")
	if err != nil {
		return nil, err
	}
	hub.edge, hub.edgeJS, err = hub.connectAccount(srv, keys.edge, edgeJWT, "central-forwarder")
	if err != nil {
		return nil, err
	}
	if err := hub.createStores(ctx); err != nil {
		return nil, err
	}
	return hub, nil
}

// connectAccount opens one of central's own connections, a full-permission
// user in the named account.
func (h *Hub) connectAccount(srv *server.Server, account nkeys.KeyPair, accountJWT, name string) (*nats.Conn, jetstream.JetStream, error) {
	user, err := h.keys.mintUser(account, accountJWT, name, jwt.Permissions{})
	if err != nil {
		return nil, nil, err
	}
	conn, err := nats.Connect("nats://hub",
		nats.InProcessServer(srv),
		nats.UserJWTAndSeed(user.UserJWT, user.Seed),
		nats.Name("flowseer-"+name),
	)
	if err != nil {
		return nil, nil, errs.From(err).Code(ErrCodeHub).Attr("connection", name).Msg("connect to the hub")
	}
	js, err := jetstream.NewWithDomain(conn, HubDomain)
	if err != nil {
		conn.Close()
		return nil, nil, errs.From(err).Code(ErrCodeHub).Attr("connection", name).Msg("create JetStream context")
	}
	return conn, js, nil
}

func (h *Hub) createStores(ctx context.Context) error {
	for _, bucket := range []string{LaneBucket, EdgeBucket} {
		if _, err := h.centralJS.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
			Bucket:  bucket,
			Storage: jetstream.FileStorage,
			History: 1,
		}); err != nil {
			return errs.From(err).Code(ErrCodeHub).Attr("bucket", bucket).Msg("create key-value bucket")
		}
	}
	window := h.cfg.AuditDuplicateWindow
	if window == 0 {
		window = defaultAuditDedupeWindow
	}
	auditBytes := h.cfg.AuditMaxBytes
	if auditBytes <= 0 {
		auditBytes = defaultAuditStreamBytes
	}
	if _, err := h.centralJS.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       AuditStream,
		Subjects:   []string{fmt.Sprintf("flowseer.%s.audit.device.>", DefaultTenant)},
		Storage:    jetstream.FileStorage,
		Retention:  jetstream.LimitsPolicy,
		MaxBytes:   auditBytes,
		Duplicates: window,
	}); err != nil {
		return errs.From(err).Code(ErrCodeHub).Msg("create audit stream")
	}
	return nil
}

// JetStream is central's own JetStream context on the central account, for
// the journal buckets and the audit stream. It is not an account an edge
// credential can reach.
func (h *Hub) JetStream() jetstream.JetStream { return h.centralJS }

// EdgeJetStream is the edge-account context the forwarder consumes the
// per-edge source streams through.
func (h *Hub) EdgeJetStream() jetstream.JetStream { return h.edgeJS }

// Connection is central's own connection into the central account.
func (h *Hub) Connection() *nats.Conn { return h.central }

// EdgeConnection is central's own connection into the edge account, where
// the per-edge source streams and the edges' own traffic live.
func (h *Hub) EdgeConnection() *nats.Conn { return h.edge }

// Tenant is the subject-layout tenant token. Accounts are the isolation
// boundary; the token distinguishes tenants within a subject once more than
// one exists.
func (h *Hub) Tenant() string { return DefaultTenant }

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
// listener is disabled.
func (h *Hub) ListenPort() int { return h.opts.Websocket.Port }

// MintEdgeUser mints the credential AttachBus returns for one edge: a user
// in the edge account confined to that edge's subtree and the JetStream
// subjects sourcing needs.
func (h *Hub) MintEdgeUser(edgeID string) (EdgeCredentials, error) {
	return h.keys.mintUser(h.keys.edge, h.edgeAccountJWT, "edge-"+edgeID, edgePermissions(edgeID))
}

// AttachEdge creates the edge-account stream that sources this edge's
// buffer across the leaf link, one stream per edge so that which edge a
// record came from is a fact of the stream it sits in, not of the subject
// it carries. A SubjectTransform re-roots every sourced record under the
// edge's own subtree, so a forged reply subject cannot store a record as
// another edge's. Idempotent; serialized so two concurrent attaches cannot
// lose one another's stream.
func (h *Hub) AttachEdge(ctx context.Context, edgeID string) error {
	h.attachMu.Lock()
	defer h.attachMu.Unlock()
	branch := EdgeSubtree(DefaultTenant, edgeID)
	maxBytes := h.cfg.EdgeStreamMaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultEdgeStreamBytes
	}
	maxAge := h.cfg.EdgeStreamMaxAge
	if maxAge <= 0 {
		maxAge = defaultEdgeStreamMaxAge
	}
	// The source consumer delivers on the edge's source branch, the one
	// place the edge's leaf may publish; the default $JS.S prefix would be
	// refused by that permission, and a branch inside the stream's own
	// subjects would be refused by JetStream, which never lets a consumer
	// deliver into the stream it reads. The transform maps whatever subject
	// the delivery carried onto this edge's otel/ingest branches, so the
	// stored subject is the edge's regardless of a forged reply.
	if _, err := h.edgeJS.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      HubEdgeStream(edgeID),
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		Discard:   jetstream.DiscardOld,
		MaxBytes:  maxBytes,
		MaxAge:    maxAge,
		Sources: []*jetstream.StreamSource{{
			Name: EdgeBufferStream,
			External: &jetstream.ExternalStream{
				APIPrefix:     "$JS." + EdgeDomain(edgeID) + ".API",
				DeliverPrefix: branch + ".source",
			},
			SubjectTransforms: []jetstream.SubjectTransformConfig{
				{Source: branch + ".otel.>", Destination: branch + ".otel.>"},
				{Source: branch + ".ingest.>", Destination: branch + ".ingest.>"},
			},
		}},
	}); err != nil {
		return errs.From(err).Code(ErrCodeHub).Attr("edge", edgeID).Msg("create the edge's hub stream")
	}
	return nil
}

// EdgeStream returns the edge-account stream that sources one edge's buffer.
func (h *Hub) EdgeStream(ctx context.Context, edgeID string) (jetstream.Stream, error) {
	stream, err := h.edgeJS.Stream(ctx, HubEdgeStream(edgeID))
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeHub).Attr("edge", edgeID).Msg("look up the edge's hub stream")
	}
	return stream, nil
}

// LeafCount reports how many leaf nodes are connected. Safe against a
// concurrent Close.
func (h *Hub) LeafCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || h.server == nil {
		return 0
	}
	return h.server.NumLeafNodes()
}

// Close closes central's connections and stops the server, waiting for its
// shutdown. Safe to call more than once.
func (h *Hub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	central, edge, srv := h.central, h.edge, h.server
	h.mu.Unlock()

	if central != nil {
		central.Close()
	}
	if edge != nil {
		edge.Close()
	}
	if srv != nil {
		srv.Shutdown()
		srv.WaitForShutdown()
	}
}

// nkeysPublic is the reading of a key pair StartHub needs; nkeys.KeyPair
// satisfies it.
type nkeysPublic interface{ PublicKey() (string, error) }

func isLoopback(host string) bool {
	return host == "" || host == "127.0.0.1" || host == "::1" || host == "localhost"
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

// quietLogger drops the server's own log lines and keeps the last few, so a
// server that fails to become ready can say why through the package's own
// error instead of a log the host never sees.
type quietLogger struct {
	host  *slog.Logger
	mu    sync.Mutex
	last  string
	lines []string
}

func newQuietLogger(host *slog.Logger) *quietLogger {
	if host == nil {
		host = slog.New(slog.DiscardHandler)
	}
	return &quietLogger{host: host}
}

func (l *quietLogger) record(level slog.Level, format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	l.mu.Lock()
	l.last = line
	if len(l.lines) < 64 {
		l.lines = append(l.lines, line)
	}
	l.mu.Unlock()
	// The embedded server's own diagnostics would otherwise vanish under
	// NoLog; forward them so an operator sees the store filling.
	l.host.LogAttrs(context.Background(), level, "embedded nats server",
		slog.String("otel.event.name", "flowseer.edgebus.server"),
		slog.String("flowseer.edgebus.detail", line),
	)
}

func (l *quietLogger) lastError() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.last
}

func (*quietLogger) Noticef(string, ...any)              {}
func (l *quietLogger) Warnf(format string, args ...any)  { l.record(slog.LevelWarn, format, args...) }
func (l *quietLogger) Fatalf(format string, args ...any) { l.record(slog.LevelError, format, args...) }
func (l *quietLogger) Errorf(format string, args ...any) { l.record(slog.LevelError, format, args...) }
func (*quietLogger) Debugf(string, ...any)               {}
func (*quietLogger) Tracef(string, ...any)               {}
