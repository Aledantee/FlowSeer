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
	log        *quietLogger
	cfg        HubConfig
	keys       *hubKeys
	opts       *server.Options
	server     *server.Server
	resolver   *server.MemAccResolver
	edgeBudget int64

	central   *nats.Conn
	centralJS jetstream.JetStream

	mu       sync.Mutex
	attachMu sync.Mutex
	edges    map[string]*edgeAccount
	closed   bool
}

// edgeAccount is central's own handle on one edge's account: the connection
// the forwarder reads its source stream through, and the account JWT
// MintEdgeUser returns to the edge.
type edgeAccount struct {
	conn       *nats.Conn
	js         jetstream.JetStream
	accountJWT string
	key        nkeys.KeyPair
}

const (
	defaultEdgeStreamBytes   = 64 << 20
	defaultEdgeStreamMaxAge  = 24 * time.Hour
	defaultAuditStreamBytes  = 256 << 20
	defaultAuditDedupeWindow = 10 * time.Minute
	// defaultCentralBudget reserves the central account's disk for the
	// journal and the audit stream; defaultEdgeBudget bounds one edge's
	// source stream plus margin. They are independent, so telemetry cannot
	// starve the journal.
	defaultCentralBudget = 512 << 20
	defaultEdgeBudget    = 128 << 20
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

	// JetStreamMaxStore is a server-wide backstop; zero lets the server size
	// it against the available disk. The per-account disk budgets below are
	// the real guard: each account is limited to its own budget, so no
	// number of edges can consume the store the journal writes into. A
	// reserved MaxStoreBytes would have to exceed central plus every edge's
	// budget at once, which an unbounded edge count cannot promise, so it
	// stays a backstop rather than a reservation.
	centralBudget := cfg.CentralBudgetBytes
	if centralBudget <= 0 {
		centralBudget = defaultCentralBudget
	}
	edgeBudget := cfg.EdgeBudgetBytes
	if edgeBudget <= 0 {
		edgeBudget = defaultEdgeBudget
	}

	resolver := &server.MemAccResolver{}
	systemJWT, err := keys.accountJWT(keys.system, "SYS", 0)
	if err != nil {
		return nil, err
	}
	centralJWT, err := keys.accountJWT(keys.central, "CENTRAL", centralBudget)
	if err != nil {
		return nil, err
	}
	for pair, encoded := range map[nkeysPublic]string{keys.system: systemJWT, keys.central: centralJWT} {
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
		JetStreamMaxStore:      cfg.MaxStoreBytes,
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
	hub := &Hub{cfg: cfg, keys: keys, opts: opts, server: srv, log: logger, resolver: resolver, edgeBudget: edgeBudget, edges: map[string]*edgeAccount{}}
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
	if err := hub.createStores(ctx); err != nil {
		return nil, err
	}
	// Re-attach every edge whose account key persisted, so a restart
	// restores the accounts and source streams the leaves reconnect into.
	edgeIDs, err := keys.persistedEdgeIDs()
	if err != nil {
		return nil, err
	}
	for _, edgeID := range edgeIDs {
		if _, err := hub.ensureEdgeAccount(ctx, edgeID); err != nil {
			return nil, err
		}
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

// waitForJetStream blocks until the account's JetStream answers an account
// info request or the context ends, so a stream create does not race the
// server provisioning a just-fetched account.
func waitForJetStream(ctx context.Context, js jetstream.JetStream) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		infoCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, err := js.AccountInfo(infoCtx)
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
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

// Connection is central's own connection into the central account.
func (h *Hub) Connection() *nats.Conn { return h.central }

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

// ensureEdgeAccount creates the edge's own account, central's connection
// into it, and its source stream, once. Every per-edge structure hangs off
// it: the account is the isolation boundary of finding 3, so one edge's
// reflection can address nothing but its own subjects. Serialized against
// concurrent attaches and against Close.
func (h *Hub) ensureEdgeAccount(ctx context.Context, edgeID string) (*edgeAccount, error) {
	h.attachMu.Lock()
	defer h.attachMu.Unlock()

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, errs.New().Code(ErrCodeHub).Msg("hub is closed")
	}
	if ea, ok := h.edges[edgeID]; ok {
		h.mu.Unlock()
		return ea, nil
	}
	srv := h.server
	h.mu.Unlock()

	key, err := h.keys.edgeAccountKey(edgeID)
	if err != nil {
		return nil, err
	}
	// The edge account holds one bounded source stream, so its disk budget
	// is that stream's ceiling plus margin; it is independent of the
	// central budget, so no number of edges can consume the journal's
	// store.
	budget := h.edgeBudget
	accountJWT, err := h.keys.accountJWT(key, "EDGE_"+edgeID, budget)
	if err != nil {
		return nil, err
	}
	pub, err := publicKey(key)
	if err != nil {
		return nil, err
	}
	if err := h.resolver.Store(pub, accountJWT); err != nil {
		return nil, errs.From(err).Code(ErrCodeHub).Attr("edge", edgeID).Msg("store edge account claims")
	}
	conn, js, err := h.connectAccount(srv, key, accountJWT, "edge-"+edgeID)
	if err != nil {
		return nil, err
	}
	ea := &edgeAccount{conn: conn, js: js, accountJWT: accountJWT, key: key}

	// A newly fetched account's JetStream is provisioned a beat after the
	// first connection; wait for it to answer before creating the stream.
	if err := waitForJetStream(ctx, js); err != nil {
		conn.Close()
		return nil, errs.From(err).Code(ErrCodeHub).Attr("edge", edgeID).Msg("wait for the edge account JetStream")
	}
	if err := h.createEdgeStream(ctx, js, edgeID); err != nil {
		conn.Close()
		return nil, err
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		conn.Close()
		return nil, errs.New().Code(ErrCodeHub).Msg("hub is closed")
	}
	h.edges[edgeID] = ea
	h.mu.Unlock()
	return ea, nil
}

// createEdgeStream creates the edge's source stream in its own account.
func (h *Hub) createEdgeStream(ctx context.Context, js jetstream.JetStream, edgeID string) error {
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
	// deliver into the stream it reads. There is deliberately no
	// SubjectTransform: a transform that re-roots a subject
	// ({Source: ">", Destination: branch + ".>"}) prepends the branch to
	// every subject, which corrupts a legitimate record's subject rather
	// than just re-rooting a forged one, and no single transform re-roots a
	// foreign subject while leaving an in-branch one alone. Containment
	// rests instead on the edge's own account (a forged reply cannot leave
	// it) and on the forwarder, which refuses a record whose subject is
	// outside this edge's subtree before shipping it.
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
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
		}},
	}); err != nil {
		return errs.From(err).Code(ErrCodeHub).Attr("edge", edgeID).Msg("create the edge's hub stream")
	}
	return nil
}

// MintEdgeUser mints the credential AttachBus returns for one edge: a user
// in the edge's own account confined to that edge's subtree and the
// JetStream subjects sourcing needs. It ensures the account and its source
// stream exist first.
func (h *Hub) MintEdgeUser(ctx context.Context, edgeID string) (EdgeCredentials, error) {
	ea, err := h.ensureEdgeAccount(ctx, edgeID)
	if err != nil {
		return EdgeCredentials{}, err
	}
	return h.keys.mintUser(ea.key, ea.accountJWT, "edge-"+edgeID, edgePermissions(edgeID))
}

// AttachEdge ensures the edge's account, connection, and source stream
// exist. Idempotent; safe against concurrent attaches.
func (h *Hub) AttachEdge(ctx context.Context, edgeID string) error {
	_, err := h.ensureEdgeAccount(ctx, edgeID)
	return err
}

// AttachedEdges lists the edges the hub currently sources.
func (h *Hub) AttachedEdges() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	ids := make([]string, 0, len(h.edges))
	for id := range h.edges {
		ids = append(ids, id)
	}
	return ids
}

// EdgeStream returns the edge-account stream that sources one edge's buffer.
func (h *Hub) EdgeStream(ctx context.Context, edgeID string) (jetstream.Stream, error) {
	h.mu.Lock()
	ea, ok := h.edges[edgeID]
	h.mu.Unlock()
	if !ok {
		return nil, errs.New().Code(ErrCodeHub).Attr("edge", edgeID).Msg("edge is not attached")
	}
	stream, err := ea.js.Stream(ctx, HubEdgeStream(edgeID))
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
	central, srv := h.central, h.server
	edges := h.edges
	h.edges = map[string]*edgeAccount{}
	h.mu.Unlock()

	for _, ea := range edges {
		ea.conn.Close()
	}
	if central != nil {
		central.Close()
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
