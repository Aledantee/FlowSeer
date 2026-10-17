package edgebus

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/fsconv"
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
	// MaxStoreBytes is the server-wide JetStream store ceiling. Zero uses
	// 75% of free disk at startup. The CENTRAL budget must fit under it.
	MaxStoreBytes int64
	// CentralBudgetBytes is CENTRAL's disk ceiling. Zero means 1 GiB.
	CentralBudgetBytes int64
	// AuditMaxBytes bounds the audit stream within the central budget. Zero
	// means 256 MiB.
	AuditMaxBytes int64
	// OperatorActionMaxBytes bounds the operator action stream within the central budget. Zero
	// means 64 MiB.
	OperatorActionMaxBytes int64
	// OperatorActionMaxPerSubject bounds the operator action stream to at most this many
	// records per subject. Zero means 10,000.
	OperatorActionMaxPerSubject int64
	// OperatorReadMaxBytes bounds the operator read stream within the central budget. Zero
	// means 16 MiB.
	OperatorReadMaxBytes int64
	// OperatorReadMaxPerSubject bounds the operator read stream to at most this many
	// records per subject. Zero means 1,000.
	OperatorReadMaxPerSubject int64
	// AuditDuplicateWindow is the requested duplicate window for the audit,
	// operator action, operator read, typed ingest, and evidence streams.
	// Evidence caps it at its configured maximum age. A re-delivered record
	// inside the window is stored once. Zero means ten minutes.
	AuditDuplicateWindow time.Duration
	// IngestMaxBytes bounds each typed ingest stream. Zero means 256 MiB. A
	// full stream refuses new records and removes none: only
	// [CleanIngest] removes a record, once every member resolved it.
	IngestMaxBytes int64
	// EvidenceMaxBytes and EvidenceMaxAge bound the raw evidence stream. Zero
	// means 64 MiB and 24 hours.
	EvidenceMaxBytes int64
	EvidenceMaxAge   time.Duration
	// StartupTimeout bounds server readiness. Zero means ten seconds.
	StartupTimeout time.Duration
	// Logger receives the embedded server's own warnings and errors, so a
	// line like "JetStream out of space" reaches an operator. Nil discards
	// them.
	Logger *slog.Logger
}

// Hub owns CENTRAL's connection and stores in an embedded server with no
// edge-facing listener. A Hub is safe for concurrent use.
type Hub struct {
	cfg    HubConfig
	keys   *hubKeys
	opts   *server.Options
	server *server.Server

	central   *nats.Conn
	centralJS jetstream.JetStream

	mu     sync.Mutex // guards closed and serializes shutdown
	closed bool
}

const (
	defaultAuditStreamBytes          = 256 << 20
	defaultAuditDedupeWindow         = 10 * time.Minute
	defaultOperatorActionStreamBytes = 64 << 20
	defaultOperatorActionMaxPerSubj  = 10000
	defaultOperatorReadStreamBytes   = 16 << 20
	defaultOperatorReadMaxPerSubj    = 1000
	defaultIngestStreamBytes         = 256 << 20
	defaultEvidenceBytes             = 64 << 20
	defaultEvidenceMaxAge            = 24 * time.Hour
	defaultCentralBudget             = 1 << 30
)

// CentralStreamBytes is the sum of the byte bounds the hub reserves in the
// CENTRAL account for its streams, defaults applied: the audit stream, both
// operator streams, one typed ingest stream per record type, and the evidence
// stream. The key-value buckets set no byte limit and are not in it. A central
// budget below this sum makes the server refuse the stream that no longer fits.
func (c HubConfig) CentralStreamBytes() int64 {
	return orDefault(c.AuditMaxBytes, defaultAuditStreamBytes) +
		orDefault(c.OperatorActionMaxBytes, defaultOperatorActionStreamBytes) +
		orDefault(c.OperatorReadMaxBytes, defaultOperatorReadStreamBytes) +
		orDefault(c.IngestMaxBytes, defaultIngestStreamBytes)*int64(len(IngestRecordTypes())) +
		orDefault(c.EvidenceMaxBytes, defaultEvidenceBytes)
}

// CentralBudget is the CENTRAL account's disk reservation, default applied.
func (c HubConfig) CentralBudget() int64 {
	return orDefault(c.CentralBudgetBytes, defaultCentralBudget)
}

func orDefault(value, fallback int64) int64 {
	if value <= 0 {
		return fallback
	}
	return value
}

// StartHub starts the hub and creates the stores central owns. The returned
// Hub must be closed.
func StartHub(ctx context.Context, cfg HubConfig) (_ *Hub, err error) {
	if cfg.StateDir == "" || !filepath.IsAbs(cfg.StateDir) {
		return nil, errs.New().Code(ErrCodeConfig).Msg("hub state directory must be absolute")
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

	resolver := &server.MemAccResolver{}
	systemJWT, err := keys.accountJWT(keys.system, "SYS", 0)
	if err != nil {
		return nil, err
	}
	centralJWT, err := keys.accountJWT(keys.central, "CENTRAL", cfg.CentralBudget())
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

	srv, err := server.NewServer(opts)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeHub).Msg("construct hub server")
	}
	logger := newQuietLogger(cfg.Logger)
	srv.SetLoggerV2(logger, false, false, false)
	srv.Start()
	hub := &Hub{cfg: cfg, keys: keys, opts: opts, server: srv}
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

	hub.central, hub.centralJS, err = hub.connectAccount(srv, keys.central, "central-journal")
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
func (h *Hub) connectAccount(srv *server.Server, account nkeys.KeyPair, name string) (*nats.Conn, jetstream.JetStream, error) {
	user, err := h.keys.mintUser(account, name)
	if err != nil {
		return nil, nil, err
	}
	conn, err := nats.Connect("nats://hub",
		nats.InProcessServer(srv),
		// NATS accepts the JWT and the seed only as raw strings, so both are
		// revealed at this boundary and nowhere earlier.
		nats.UserJWTAndSeed(user.UserJWT.RevealString(), user.Seed.RevealString()),
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
	for _, bucket := range []string{LaneBucket, EdgeBucket, CapturesBucket, RawWindowsBucket, TenantBucket, AccessBucket} {
		if _, err := h.centralJS.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
			Bucket:  bucket,
			Storage: jetstream.FileStorage,
			History: 1,
		}); err != nil {
			return errs.From(err).Code(ErrCodeHub).Attr("bucket", bucket).Msg("create key-value bucket")
		}
	}
	// KeyValueConfig has no AllowAtomicPublish field, and each
	// CreateOrUpdateKeyValue clears the stream flag, so restore it on every start.
	tenantStream, err := h.centralJS.Stream(ctx, "KV_"+TenantBucket)
	if err != nil {
		return errs.From(err).Code(ErrCodeHub).Attr("bucket", TenantBucket).Msg("load key-value stream")
	}
	tenantConfig := tenantStream.CachedInfo().Config
	tenantConfig.AllowAtomicPublish = true
	if _, err := h.centralJS.UpdateStream(ctx, tenantConfig); err != nil {
		return errs.From(err).Code(ErrCodeHub).Attr("bucket", TenantBucket).Msg("update key-value stream")
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
		Subjects:   []string{"flowseer.*.audit.device.>"},
		Storage:    jetstream.FileStorage,
		Retention:  jetstream.LimitsPolicy,
		MaxBytes:   auditBytes,
		Duplicates: window,
	}); err != nil {
		return errs.From(err).Code(ErrCodeHub).Msg("create audit stream")
	}

	opActionBytes := h.cfg.OperatorActionMaxBytes
	if opActionBytes <= 0 {
		opActionBytes = defaultOperatorActionStreamBytes
	}
	opActionMaxPerSubj := h.cfg.OperatorActionMaxPerSubject
	if opActionMaxPerSubj <= 0 {
		opActionMaxPerSubj = defaultOperatorActionMaxPerSubj
	}
	if _, err := h.centralJS.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:              OperatorActionStream,
		Subjects:          []string{"flowseer.*.operator.action.*"},
		Storage:           jetstream.FileStorage,
		Retention:         jetstream.LimitsPolicy,
		Discard:           jetstream.DiscardOld,
		MaxBytes:          opActionBytes,
		MaxMsgsPerSubject: opActionMaxPerSubj,
		Duplicates:        window,
	}); err != nil {
		return errs.From(err).Code(ErrCodeHub).Msg("create operator action stream")
	}

	opReadBytes := h.cfg.OperatorReadMaxBytes
	if opReadBytes <= 0 {
		opReadBytes = defaultOperatorReadStreamBytes
	}
	opReadMaxPerSubj := h.cfg.OperatorReadMaxPerSubject
	if opReadMaxPerSubj <= 0 {
		opReadMaxPerSubj = defaultOperatorReadMaxPerSubj
	}
	if _, err := h.centralJS.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:              OperatorReadStream,
		Subjects:          []string{"flowseer.*.operator.read.*"},
		Storage:           jetstream.FileStorage,
		Retention:         jetstream.LimitsPolicy,
		Discard:           jetstream.DiscardOld,
		MaxBytes:          opReadBytes,
		MaxMsgsPerSubject: opReadMaxPerSubj,
		Duplicates:        window,
	}); err != nil {
		return errs.From(err).Code(ErrCodeHub).Msg("create operator read stream")
	}

	ingestBytes := h.cfg.IngestMaxBytes
	if ingestBytes <= 0 {
		ingestBytes = defaultIngestStreamBytes
	}
	for _, recordType := range IngestRecordTypes() {
		if _, err := h.centralJS.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
			Name:       IngestStream(recordType),
			Subjects:   []string{"flowseer.*.ingest." + recordType + ".*"},
			Storage:    jetstream.FileStorage,
			Retention:  jetstream.LimitsPolicy,
			Discard:    jetstream.DiscardNew,
			MaxBytes:   ingestBytes,
			Duplicates: window,
		}); err != nil {
			return errs.From(err).Code(ErrCodeHub).Attr("record_type", recordType).Msg("create ingest stream")
		}
		for _, name := range IngestConsumers(recordType) {
			if _, err := EnsureIngestConsumer(ctx, h.centralJS, recordType, name); err != nil {
				return err
			}
		}
	}

	evidenceBytes := h.cfg.EvidenceMaxBytes
	if evidenceBytes <= 0 {
		evidenceBytes = defaultEvidenceBytes
	}
	evidenceAge := h.cfg.EvidenceMaxAge
	if evidenceAge <= 0 {
		evidenceAge = defaultEvidenceMaxAge
	}
	evidenceDuplicates := min(window, evidenceAge)
	if _, err := h.centralJS.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       EvidenceStream,
		Subjects:   []string{"flowseer.*.evidence.>"},
		Storage:    jetstream.FileStorage,
		Retention:  jetstream.LimitsPolicy,
		Discard:    jetstream.DiscardOld,
		MaxBytes:   evidenceBytes,
		MaxAge:     evidenceAge,
		Duplicates: evidenceDuplicates,
	}); err != nil {
		return errs.From(err).Code(ErrCodeHub).Msg("create evidence stream")
	}
	return nil
}

// JetStream is CENTRAL's context for the journal buckets and central streams.
func (h *Hub) JetStream() jetstream.JetStream { return h.centralJS }

// Connection is central's own connection into the central account.
func (h *Hub) Connection() *nats.Conn { return h.central }

// Close closes CENTRAL's connection and stops the server. Safe to call more
// than once or concurrently.
func (h *Hub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	central, srv := h.central, h.server
	if central != nil {
		central.Close()
	}
	if srv != nil {
		srv.Shutdown()
	}
	h.mu.Unlock()
	if srv != nil {
		srv.WaitForShutdown()
	}
}

// nkeysPublic is the reading of a key pair StartHub needs; nkeys.KeyPair
// satisfies it.
type nkeysPublic interface{ PublicKey() (string, error) }

// quietLogger drops the server's own log lines and keeps the last few, so a
// server that fails to become ready can say why through the package's own
// error instead of a log the host never sees.
type quietLogger struct {
	host  *slog.Logger
	mu    sync.Mutex // guards last and lines
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
		fsconv.EventName("flowseer.edgebus.server"),
		slog.String("flowseer.edgebus.detail", line),
	)
}

func (l *quietLogger) lastError() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.last
}

func (*quietLogger) Noticef(string, ...any)             {}
func (l *quietLogger) Warnf(format string, args ...any) { l.record(slog.LevelWarn, format, args...) }

func (l *quietLogger) Fatalf(format string, args ...any) { l.record(slog.LevelError, format, args...) }

func (l *quietLogger) Errorf(format string, args ...any) { l.record(slog.LevelError, format, args...) }
func (*quietLogger) Debugf(string, ...any)               {}
func (*quietLogger) Tracef(string, ...any)               {}
