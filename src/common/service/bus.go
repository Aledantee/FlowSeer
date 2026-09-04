package service

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"go.aledante.io/FlowSeer/src/common/errs"
)

const (
	defaultBusMaxStoreBytes = int64(1 << 30)
	defaultMailboxMaxBytes  = int64(768 << 20)
	defaultMetadataMaxBytes = int64(64 << 20)
	defaultBusReserveBytes  = int64(192 << 20)
	defaultBusFsyncInterval = 5 * time.Second
	minimumBusFsyncInterval = time.Millisecond

	mailboxStreamName  = "FLOWSEER_MAILBOX"
	metadataStreamName = "FLOWSEER_METADATA"
	mailboxSubjectRoot = "_flowseer.mailbox"
	metadataSubject    = "_flowseer.metadata"
)

var (
	errCodeBusConfig      = errs.NewCode("service/bus-config")
	errCodeBusStoreLocked = errs.NewCode("service/bus-store-locked")
	errCodeBusCapacity    = errs.NewCode("service/bus-capacity")
	errCodeBusUnhealthy   = errs.NewCode("service/bus-unhealthy")
)

type normalizedBusConfig struct {
	serviceNamespace string
	serviceName      string
	storeDir         string
	domain           string
	maxStoreBytes    int64
	mailboxMaxBytes  int64
	metadataMaxBytes int64
	reserveBytes     int64
	startupTimeout   time.Duration
	healthInterval   time.Duration
	fsyncPolicy      BusFsyncPolicy
	fsyncInterval    time.Duration
}

// busResources are available to reconciliation after both durable streams
// exist and before startup proves them writable.
type busResources struct {
	connection *nats.Conn
	jetStream  jetstream.JetStream
	mailbox    jetstream.Stream
	metadata   jetstream.Stream
}

// busReconciler validates persistent metadata after both owned streams open and
// before startup health probes begin.
type busReconciler func(context.Context, busResources) error

// localBus owns the embedded server, client connection, health monitor, server
// waiter, and store lock. close releases the lock after its shutdown waits
// finish or their cleanup deadline expires.
type localBus struct {
	healthInterval time.Duration
	server         *server.Server
	connection     *nats.Conn
	resources      busResources
	lock           *fileStoreLock
	logger         *busServerLogger
	failure        chan error
	monitorDone    chan struct{}
	serverDone     chan struct{}
	monitorCancel  context.CancelFunc
	closeOnce      sync.Once
	closeErr       error
}

func normalizeBusConfig(identity Identity, config BusConfig) (normalizedBusConfig, error) {
	if err := validateIdentity(identity); err != nil {
		return normalizedBusConfig{}, errs.From(err).Code(errCodeBusConfig).Msg("normalize local bus identity")
	}

	storeDir := config.StoreDir
	if storeDir == "" {
		root, err := privateStateDir()
		if err != nil {
			return normalizedBusConfig{}, errs.From(err).Code(errCodeBusConfig).Msg("resolve local bus state directory")
		}
		storeDir = filepath.Join(root, "flowseer", identity.Namespace, identity.Name, "service-bus")
	} else if !filepath.IsAbs(storeDir) {
		return normalizedBusConfig{}, errs.New().Code(errCodeBusConfig).Msg("local bus store directory must be absolute")
	}
	storeDir = filepath.Clean(storeDir)

	maxStore := defaultedPositive(config.MaxStoreBytes, defaultBusMaxStoreBytes)
	mailboxMax := defaultedPositive(config.MailboxMaxBytes, defaultMailboxMaxBytes)
	metadataMax := defaultedPositive(config.MetadataMaxBytes, defaultMetadataMaxBytes)
	reserve := defaultedPositive(config.ReserveBytes, defaultBusReserveBytes)
	if maxStore <= 0 || mailboxMax <= 0 || metadataMax <= 0 || reserve <= 0 {
		return normalizedBusConfig{}, errs.New().Code(errCodeBusConfig).Msg("local bus capacity values must be positive")
	}
	if mailboxMax > maxStore-metadataMax || reserve > maxStore-mailboxMax-metadataMax {
		return normalizedBusConfig{}, errs.New().Code(errCodeBusConfig).Msg("local bus stream limits and reserve exceed the server limit")
	}

	startupTimeout := config.StartupTimeout
	if startupTimeout == 0 {
		startupTimeout = 10 * time.Second
	}
	healthInterval := config.HealthInterval
	if healthInterval == 0 {
		healthInterval = 30 * time.Second
	}
	if startupTimeout < 0 || healthInterval < 0 {
		return normalizedBusConfig{}, errs.New().Code(errCodeBusConfig).Msg("local bus timeouts must be positive")
	}

	fsyncPolicy := config.FsyncPolicy
	if fsyncPolicy > BusFsyncPerMessage {
		return normalizedBusConfig{}, errs.New().Code(errCodeBusConfig).Msg("local bus fsync policy is invalid")
	}
	fsyncInterval := time.Duration(0)
	switch fsyncPolicy {
	case BusFsyncPeriodic:
		fsyncInterval = defaultBusFsyncInterval
		if config.FsyncInterval != nil {
			fsyncInterval = *config.FsyncInterval
		}
		if fsyncInterval < minimumBusFsyncInterval {
			return normalizedBusConfig{}, errs.New().Code(errCodeBusConfig).Msg("local bus fsync interval must be at least one millisecond")
		}
	case BusFsyncPerMessage:
		if config.FsyncInterval != nil {
			return normalizedBusConfig{}, errs.New().Code(errCodeBusConfig).Msg("local bus fsync interval requires periodic sync")
		}
	}

	return normalizedBusConfig{
		serviceNamespace: identity.Namespace,
		serviceName:      identity.Name,
		storeDir:         storeDir,
		domain:           busDomain(identity),
		maxStoreBytes:    maxStore,
		mailboxMaxBytes:  mailboxMax,
		metadataMaxBytes: metadataMax,
		reserveBytes:     reserve,
		startupTimeout:   startupTimeout,
		healthInterval:   healthInterval,
		fsyncPolicy:      fsyncPolicy,
		fsyncInterval:    fsyncInterval,
	}, nil
}

func defaultedPositive(value, fallback int64) int64 {
	if value == 0 {
		return fallback
	}
	return value
}

func privateStateDir() (string, error) {
	switch runtime.GOOS {
	case "linux":
		if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
			if !filepath.IsAbs(dir) {
				return "", fmt.Errorf("XDG_STATE_HOME is not absolute")
			}
			return dir, nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "state"), nil
	case "windows":
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return dir, nil
		}
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("user config directory is not absolute")
	}
	return dir, nil
}

// busDomain derives the stable JetStream domain owned by one service identity.
func busDomain(identity Identity) string {
	sum := sha256.Sum256([]byte(identity.Namespace + "\x00" + identity.Name))
	return "v1_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:]))
}

// startLocalBus acquires the store lock before opening durable state and returns
// only after reconciliation and health checks succeed. Failure unwinds partial
// state; success transfers cleanup ownership to localBus.close.
func startLocalBus(ctx context.Context, config normalizedBusConfig, reconcile busReconciler) (_ *localBus, err error) {
	if err := os.MkdirAll(config.storeDir, 0o700); err != nil {
		return nil, busUnhealthy(err, "create local bus store directory")
	}
	if err := restrictStoreDir(config.storeDir); err != nil {
		return nil, busUnhealthy(err, "secure local bus store directory")
	}

	lock, err := acquireStoreLock(filepath.Join(config.storeDir, ".lock"))
	if err != nil {
		return nil, err
	}
	bus := &localBus{
		healthInterval: config.healthInterval,
		lock:           lock,
		logger:         newBusServerLogger(),
		failure:        make(chan error, 1),
		monitorDone:    make(chan struct{}),
		serverDone:     make(chan struct{}),
	}
	defer func() {
		if err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.startupTimeout)
			defer cancel()
			err = errors.Join(err, bus.close(cleanupCtx, false))
		}
	}()
	if err := reconcileStoreProvenance(config); err != nil {
		return nil, err
	}

	options := localBusServerOptions(config)
	bus.server, err = server.NewServer(options)
	if err != nil {
		return nil, busUnhealthy(err, "construct local bus server")
	}
	bus.server.SetLoggerV2(bus.logger, false, false, false)
	bus.server.Start()
	go func() {
		bus.server.WaitForShutdown()
		close(bus.serverDone)
	}()

	if !bus.server.ReadyForConnections(config.startupTimeout) {
		if fatalErr := bus.logger.err(); fatalErr != nil {
			return nil, busUnhealthy(fatalErr, "start local bus server")
		}
		return nil, busUnhealthy(context.DeadlineExceeded, "wait for local bus server readiness")
	}
	if !bus.server.JetStreamEnabled() {
		return nil, busUnhealthy(nil, "local bus server started without JetStream")
	}

	bus.connection, err = nats.Connect(
		"nats://127.0.0.1:4222",
		nats.InProcessServer(bus.server),
		nats.NoReconnect(),
		nats.Name("flowseer-service-runtime"),
	)
	if err != nil {
		return nil, busUnhealthy(err, "connect to local bus server")
	}
	js, err := jetstream.NewWithDomain(bus.connection, config.domain)
	if err != nil {
		return nil, busUnhealthy(err, "create local JetStream context")
	}
	if _, err := js.AccountInfo(ctx); err != nil {
		return nil, busUnhealthy(err, "read local JetStream account information")
	}

	mailbox, err := openOwnedStream(ctx, js, mailboxStreamConfig(config.mailboxMaxBytes))
	if err != nil {
		if code, ok := errs.CodeOf(err); ok && code == errCodeBusMigration {
			return nil, err
		}
		return nil, busUnhealthy(err, "reconcile local bus mailbox stream")
	}
	metadata, err := openOwnedStream(ctx, js, metadataStreamConfig(config.metadataMaxBytes))
	if err != nil {
		if code, ok := errs.CodeOf(err); ok && code == errCodeBusMigration {
			return nil, err
		}
		return nil, busUnhealthy(err, "reconcile local bus metadata stream")
	}
	bus.resources = busResources{connection: bus.connection, jetStream: js, mailbox: mailbox, metadata: metadata}
	if reconcile != nil {
		if err := reconcile(ctx, bus.resources); err != nil {
			return nil, err
		}
	}
	if err := bus.checkWritable(ctx); err != nil {
		return nil, err
	}
	if err := bus.checkHealth(); err != nil {
		return nil, err
	}

	monitorCtx, cancel := context.WithCancel(context.Background())
	bus.monitorCancel = cancel
	go bus.monitor(monitorCtx)
	return bus, nil
}

func localBusServerOptions(config normalizedBusConfig) *server.Options {
	return &server.Options{
		ServerName:             config.domain,
		DontListen:             true,
		NoSigs:                 true,
		NoLog:                  true,
		JetStream:              true,
		JetStreamMaxStore:      config.maxStoreBytes,
		JetStreamDomain:        config.domain,
		StoreDir:               config.storeDir,
		SyncAlways:             config.fsyncPolicy == BusFsyncPerMessage,
		SyncInterval:           config.fsyncInterval,
		DisableJetStreamBanner: true,
		JetStreamLimits: server.JSLimitOpts{
			Duplicates:                24 * time.Hour,
			MaxBatchInflightPerStream: 1,
			MaxBatchInflightTotal:     1,
		},
	}
}

// openOwnedStream creates a missing owned stream but refuses to modify a
// persisted stream whose contract differs from desired.
func openOwnedStream(ctx context.Context, js jetstream.JetStream, desired jetstream.StreamConfig) (jetstream.Stream, error) {
	stream, err := js.Stream(ctx, desired.Name)
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		return js.CreateStream(ctx, desired)
	}
	if err != nil {
		return nil, err
	}
	info, err := stream.Info(ctx)
	if err != nil {
		return nil, err
	}
	if !ownedStreamConfigEqual(info.Config, desired) {
		return nil, migrationRequired().
			Attr("stream_name", desired.Name).
			Msg("local bus stream configuration does not match the persisted contract")
	}
	return stream, nil
}

func ownedStreamConfigEqual(actual, desired jetstream.StreamConfig) bool {
	return actual.Name == desired.Name &&
		slices.Equal(actual.Subjects, desired.Subjects) &&
		actual.Retention == desired.Retention &&
		actual.MaxConsumers == desired.MaxConsumers &&
		actual.MaxMsgs == desired.MaxMsgs &&
		actual.MaxBytes == desired.MaxBytes &&
		actual.Discard == desired.Discard &&
		actual.MaxMsgsPerSubject == desired.MaxMsgsPerSubject &&
		actual.Storage == desired.Storage &&
		actual.Replicas == desired.Replicas &&
		actual.Duplicates == desired.Duplicates &&
		actual.AllowAtomicPublish == desired.AllowAtomicPublish
}

func mailboxStreamConfig(maxBytes int64) jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:               mailboxStreamName,
		Subjects:           []string{mailboxSubjectRoot + ".>"},
		Retention:          jetstream.WorkQueuePolicy,
		MaxConsumers:       -1,
		MaxMsgs:            -1,
		MaxBytes:           maxBytes,
		Discard:            jetstream.DiscardNew,
		MaxMsgsPerSubject:  -1,
		Storage:            jetstream.FileStorage,
		Replicas:           1,
		Duplicates:         24 * time.Hour,
		AllowAtomicPublish: true,
	}
}

func metadataStreamConfig(maxBytes int64) jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:              metadataStreamName,
		Subjects:          []string{metadataSubject + ".>"},
		Retention:         jetstream.LimitsPolicy,
		MaxConsumers:      -1,
		MaxMsgs:           -1,
		MaxBytes:          maxBytes,
		Discard:           jetstream.DiscardNew,
		MaxMsgsPerSubject: -1,
		Storage:           jetstream.FileStorage,
		Replicas:          1,
		Duplicates:        24 * time.Hour,
	}
}

// checkWritable probes both streams. Mailbox capacity exhaustion is healthy
// publisher backpressure, but metadata must remain writable so settlement and
// reconciliation can progress.
func (b *localBus) checkWritable(ctx context.Context) error {
	stamp := fmt.Sprintf("%x", time.Now().UnixNano())
	if err := writeCanary(ctx, b.resources.jetStream, b.resources.mailbox, mailboxSubjectRoot+"._health."+stamp); err != nil && !isCapacityError(err) {
		return busUnhealthy(err, "verify local bus mailbox stream")
	}
	if err := writeCanary(ctx, b.resources.jetStream, b.resources.metadata, metadataSubject+"._health."+stamp); err != nil {
		return busUnhealthy(err, "verify local bus metadata stream")
	}
	return nil
}

func writeCanary(ctx context.Context, js jetstream.JetStream, stream jetstream.Stream, subject string) error {
	ack, err := js.Publish(ctx, subject, []byte("ok"))
	if err != nil {
		return err
	}
	return stream.DeleteMsg(ctx, ack.Sequence)
}

func (b *localBus) checkHealth() error {
	status := b.server.Healthz(&server.HealthzOptions{Details: true})
	if status == nil || status.StatusCode != http.StatusOK {
		if status == nil {
			return busUnhealthy(nil, "local bus health check returned no status")
		}
		return busUnhealthy(errors.New(status.Error), "local bus health check failed")
	}
	return nil
}

// monitor reports the first unexpected server, connection, fatal-log, or health
// failure and exits. close cancels and joins it.
func (b *localBus) monitor(ctx context.Context) {
	defer close(b.monitorDone)
	ticker := time.NewTicker(b.healthInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.serverDone:
			if ctx.Err() != nil {
				return
			}
			b.reportFailure(busUnhealthy(b.logger.err(), "local bus server stopped unexpectedly"))
			return
		case fatalErr := <-b.logger.fatal:
			if ctx.Err() != nil {
				return
			}
			b.reportFailure(busUnhealthy(fatalErr, "local bus server failed"))
			return
		case <-ticker.C:
			if err := b.checkHealth(); err != nil {
				if ctx.Err() != nil {
					return
				}
				b.reportFailure(err)
				return
			}
			checkCtx, cancel := context.WithTimeout(ctx, b.healthInterval)
			err := b.checkWritable(checkCtx)
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				b.reportFailure(err)
				return
			}
			if err := b.connection.LastError(); err != nil {
				b.reportFailure(busUnhealthy(err, "local bus client failed"))
				return
			}
		}
	}
}

// reportFailure publishes at most one pending infrastructure failure and never
// blocks.
func (b *localBus) reportFailure(err error) {
	select {
	case b.failure <- err:
	default:
	}
}

func (b *localBus) failures() <-chan error { return b.failure }

// close idempotently stops bus-owned work. A healthy bus drains its client; an
// unhealthy bus closes immediately. It releases the store lock after the
// server and monitor shutdown waits, even when ctx ends first.
func (b *localBus) close(ctx context.Context, healthy bool) error {
	b.closeOnce.Do(func() {
		if b.monitorCancel != nil {
			b.monitorCancel()
		}
		if b.connection != nil {
			if healthy && !b.connection.IsClosed() {
				closed := b.connection.StatusChanged(nats.CLOSED)
				if err := b.connection.Drain(); err != nil {
					b.closeErr = errors.Join(b.closeErr, err)
					b.connection.Close()
				}
				if !b.connection.IsClosed() {
					select {
					case <-closed:
					case <-ctx.Done():
						b.connection.Close()
						b.closeErr = errors.Join(b.closeErr, ctx.Err())
					}
				}
			} else {
				b.connection.Close()
			}
		}
		if b.server != nil {
			b.server.Shutdown()
			select {
			case <-b.serverDone:
			case <-ctx.Done():
				b.closeErr = errors.Join(b.closeErr, ctx.Err())
			}
		}
		if b.monitorCancel != nil {
			select {
			case <-b.monitorDone:
			case <-ctx.Done():
				b.closeErr = errors.Join(b.closeErr, ctx.Err())
			}
		}
		if b.lock != nil {
			b.closeErr = errors.Join(b.closeErr, b.lock.Close())
		}
	})
	return b.closeErr
}

func isCapacityError(err error) bool {
	var apiErr *jetstream.APIError
	return errors.As(err, &apiErr) && apiErr.ErrorCode == 10077 && strings.Contains(apiErr.Description, "maximum bytes exceeded")
}

func busCapacity(err error) error {
	return errs.From(err).Code(errCodeBusCapacity).Retryable().Msg("local bus capacity exhausted")
}

func busUnhealthy(err error, message string) error {
	return errs.From(err).Code(errCodeBusUnhealthy).Msg(message)
}

// busServerLogger suppresses routine embedded-server output while retaining and
// signaling fatal records for startup and health supervision.
type busServerLogger struct {
	fatal chan error
	// mu guards last.
	mu   sync.Mutex
	last error
}

func newBusServerLogger() *busServerLogger {
	return &busServerLogger{fatal: make(chan error, 1)}
}

func (l *busServerLogger) Noticef(string, ...any) {}
func (l *busServerLogger) Warnf(string, ...any)   {}
func (l *busServerLogger) Errorf(string, ...any)  {}
func (l *busServerLogger) Debugf(string, ...any)  {}
func (l *busServerLogger) Tracef(string, ...any)  {}

func (l *busServerLogger) Fatalf(format string, values ...any) {
	err := fmt.Errorf(format, values...)
	l.mu.Lock()
	l.last = err
	l.mu.Unlock()
	select {
	case l.fatal <- err:
	default:
	}
}

func (l *busServerLogger) err() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.last
}
