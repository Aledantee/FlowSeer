package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	servicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/service/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

func TestNormalizeBusConfigUsesStablePrivateDefaults(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG state directory is a Linux convention")
	}
	stateRoot := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateRoot)
	identity := Identity{Namespace: "flowseer", Name: "edge_agent", Version: "v1"}

	got, err := normalizeBusConfig(identity, BusConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(stateRoot, "flowseer", "flowseer", "edge_agent", "service-bus"); got.storeDir != want {
		t.Fatalf("store directory = %q, want %q", got.storeDir, want)
	}
	if got.maxStoreBytes != 1<<30 || got.mailboxMaxBytes != 768<<20 || got.metadataMaxBytes != 64<<20 || got.reserveBytes != 192<<20 {
		t.Fatalf("unexpected default capacity: %+v", got)
	}
	if got.fsyncPolicy != BusFsyncPeriodic || got.fsyncInterval != 5*time.Second {
		t.Fatalf("default fsync policy = %v at %s, want periodic at 5s", got.fsyncPolicy, got.fsyncInterval)
	}

	identity.Version = "v2"
	upgraded, err := normalizeBusConfig(identity, BusConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.storeDir != got.storeDir || upgraded.domain != got.domain {
		t.Fatalf("release version changed persistent identity: before=%+v after=%+v", got, upgraded)
	}
}

func TestNormalizeBusConfigRejectsUnsafeOverrides(t *testing.T) {
	identity := testBusIdentity()
	zero := time.Duration(0)
	negative := -time.Second
	custom := time.Second
	tests := []BusConfig{
		{StoreDir: "relative"},
		{MaxStoreBytes: 100, MailboxMaxBytes: 80, MetadataMaxBytes: 20, ReserveBytes: 1},
		{HealthInterval: -time.Second},
		{FsyncInterval: &zero},
		{FsyncInterval: &negative},
		{FsyncPolicy: BusFsyncPolicy(99)},
		{FsyncPolicy: BusFsyncPerMessage, FsyncInterval: &custom},
	}
	for _, config := range tests {
		_, err := normalizeBusConfig(identity, config)
		if err == nil {
			t.Fatalf("normalizeBusConfig(%+v) succeeded", config)
		}
		if code, ok := errs.CodeOf(err); !ok || code != errCodeBusConfig {
			t.Fatalf("error code = %q, %v; want %q", code, ok, errCodeBusConfig)
		}
	}
}

func TestNormalizeBusConfigCarriesDeclaredFsyncPolicy(t *testing.T) {
	interval := 750 * time.Millisecond
	periodic, err := normalizeBusConfig(testBusIdentity(), BusConfig{
		StoreDir:      t.TempDir(),
		FsyncInterval: &interval,
	})
	if err != nil {
		t.Fatal(err)
	}
	if periodic.fsyncPolicy != BusFsyncPeriodic || periodic.fsyncInterval != interval {
		t.Fatalf("periodic policy = %v at %s, want periodic at %s", periodic.fsyncPolicy, periodic.fsyncInterval, interval)
	}

	perMessage, err := normalizeBusConfig(testBusIdentity(), BusConfig{
		StoreDir:    t.TempDir(),
		FsyncPolicy: BusFsyncPerMessage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if perMessage.fsyncPolicy != BusFsyncPerMessage || perMessage.fsyncInterval != 0 {
		t.Fatalf("per-message policy = %v at %s, want per-message with no interval", perMessage.fsyncPolicy, perMessage.fsyncInterval)
	}
}

func TestLocalBusServerOptionsCarryFsyncPolicy(t *testing.T) {
	defaultConfig := testNormalizedBusConfig(t)
	defaultOptions := localBusServerOptions(defaultConfig)
	if defaultOptions.SyncAlways || defaultOptions.SyncInterval != 5*time.Second {
		t.Fatalf("default server fsync options = always:%t interval:%s, want always:false interval:5s", defaultOptions.SyncAlways, defaultOptions.SyncInterval)
	}
	customConfig := defaultConfig
	customConfig.fsyncInterval = 750 * time.Millisecond
	customOptions := localBusServerOptions(customConfig)
	if customOptions.SyncAlways || customOptions.SyncInterval != customConfig.fsyncInterval {
		t.Fatalf("custom server fsync options = always:%t interval:%s, want always:false interval:%s", customOptions.SyncAlways, customOptions.SyncInterval, customConfig.fsyncInterval)
	}

	perMessageConfig := defaultConfig
	perMessageConfig.fsyncPolicy = BusFsyncPerMessage
	perMessageConfig.fsyncInterval = 0
	perMessageOptions := localBusServerOptions(perMessageConfig)
	if !perMessageOptions.SyncAlways || perMessageOptions.SyncInterval != 0 {
		t.Fatalf("per-message server fsync options = always:%t interval:%s, want always:true interval:0s", perMessageOptions.SyncAlways, perMessageOptions.SyncInterval)
	}
}

func TestFsyncPolicyDoesNotChangeOwnedStreamConfigs(t *testing.T) {
	periodic := testNormalizedBusConfig(t)
	perMessage := periodic
	perMessage.fsyncPolicy = BusFsyncPerMessage
	perMessage.fsyncInterval = 0

	if got, want := mailboxStreamConfig(periodic.mailboxMaxBytes), mailboxStreamConfig(perMessage.mailboxMaxBytes); !reflect.DeepEqual(got, want) {
		t.Fatalf("mailbox config changed with fsync policy:\nperiodic:   %+v\nper-message: %+v", got, want)
	}
	if got, want := metadataStreamConfig(periodic.metadataMaxBytes), metadataStreamConfig(perMessage.metadataMaxBytes); !reflect.DeepEqual(got, want) {
		t.Fatalf("metadata config changed with fsync policy:\nperiodic:   %+v\nper-message: %+v", got, want)
	}
}

func TestServiceBusOptInStartsBeforeSetupAndNilStartsNothing(t *testing.T) {
	unusedStore := filepath.Join(t.TempDir(), "unused")
	if _, err := preflight(context.Background(), Config{
		Identity: testBusIdentity(),
		Setup:    testSetup(),
	}, mapLookup(nil)); err != nil {
		t.Fatalf("preflight without bus: %v", err)
	}
	if _, err := os.Stat(unusedStore); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bus-disabled store stat error = %v, want not exist", err)
	}

	storeDir := filepath.Join(t.TempDir(), "enabled")
	setupSawStore := false
	err := runWithOptions(context.Background(), Config{
		Identity: testBusIdentity(),
		Bus:      &BusConfig{StoreDir: storeDir},
		Setup: func(context.Context) (Attempt, error) {
			_, statErr := os.Stat(storeDir)
			setupSawStore = statErr == nil
			return Attempt{Runner: func(context.Context) error { return nil }}, nil
		},
	}, immediateSupervisorOptions())
	if err != nil {
		t.Fatalf("runWithOptions() with bus: %v", err)
	}
	if !setupSawStore {
		t.Fatal("module setup ran before the local bus store existed")
	}
}

func TestInvalidBusConfigFailsBeforeSetup(t *testing.T) {
	setupCalls := 0
	_, err := preflight(context.Background(), Config{
		Identity: testBusIdentity(),
		Bus:      &BusConfig{StoreDir: "relative"},
		Setup: func(context.Context) (Attempt, error) {
			setupCalls++
			return Attempt{}, nil
		},
	}, mapLookup(nil))
	if err == nil {
		t.Fatal("preflight accepted a relative bus store")
	}
	if setupCalls != 0 {
		t.Fatalf("setup calls = %d, want zero", setupCalls)
	}
}

func TestBusDomainSeparatesAmbiguousIdentityPairs(t *testing.T) {
	left := busDomain(Identity{Namespace: "a", Name: "bc"})
	right := busDomain(Identity{Namespace: "ab", Name: "c"})
	if left == right {
		t.Fatalf("ambiguous identities share domain %q", left)
	}
}

func TestStartLocalBusIsListenerFreeDurableAndExclusive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	config := testNormalizedBusConfig(t)
	reconciled := false
	bus, err := startLocalBus(ctx, config, func(ctx context.Context, resources busResources) error {
		reconciled = true
		mailbox, err := resources.mailbox.Info(ctx)
		if err != nil {
			return err
		}
		if mailbox.Config.Retention != jetstream.WorkQueuePolicy || mailbox.Config.Storage != jetstream.FileStorage || mailbox.Config.Discard != jetstream.DiscardNew || !mailbox.Config.AllowAtomicPublish {
			return fmt.Errorf("unexpected mailbox config: %+v", mailbox.Config)
		}
		metadata, err := resources.metadata.Info(ctx)
		if err != nil {
			return err
		}
		if metadata.Config.Retention != jetstream.LimitsPolicy || metadata.Config.Storage != jetstream.FileStorage || metadata.Config.Discard != jetstream.DiscardNew {
			return fmt.Errorf("unexpected metadata config: %+v", metadata.Config)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reconciled {
		t.Fatal("reconciler was not called")
	}
	if bus.server.Addr() != nil {
		t.Fatalf("listener-free server has address %v", bus.server.Addr())
	}
	if info, err := os.Stat(config.storeDir); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("store mode = %o, want 700", info.Mode().Perm())
	}

	_, err = startLocalBus(ctx, config, nil)
	if err == nil {
		t.Fatal("second bus acquired an active store")
	}
	if code, ok := errs.CodeOf(err); !ok || code != errCodeBusStoreLocked {
		t.Fatalf("second bus error code = %q, %v; want %q: %v", code, ok, errCodeBusStoreLocked, err)
	}

	ack, err := bus.resources.jetStream.Publish(ctx, mailboxSubjectRoot+".test", []byte("survives restart"))
	if err != nil {
		t.Fatal(err)
	}
	closeBus(t, bus, true)

	reopened, err := startLocalBus(ctx, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	message, err := reopened.resources.mailbox.GetMsg(ctx, ack.Sequence)
	if err != nil {
		closeBus(t, reopened, false)
		t.Fatal(err)
	}
	if got := string(message.Data); got != "survives restart" {
		t.Fatalf("reopened payload = %q", got)
	}
	closeBus(t, reopened, true)
}

func TestStartLocalBusCleansUpAfterReconciliationFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	config := testNormalizedBusConfig(t)
	want := errors.New("incompatible manifest")
	_, err := startLocalBus(ctx, config, func(context.Context, busResources) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("start error = %v, want reconciliation error", err)
	}

	bus, err := startLocalBus(ctx, config, nil)
	if err != nil {
		t.Fatalf("partial startup retained resources: %v", err)
	}
	closeBus(t, bus, true)
}

func TestStartLocalBusRejectsExhaustedMetadataReserve(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	config, err := normalizeBusConfig(testBusIdentity(), BusConfig{
		StoreDir:         t.TempDir(),
		MaxStoreBytes:    4 << 20,
		MailboxMaxBytes:  1 << 20,
		MetadataMaxBytes: 512,
		ReserveBytes:     1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = startLocalBus(ctx, config, func(ctx context.Context, resources busResources) error {
		_, publishErr := resources.jetStream.Publish(ctx, metadataSubject+".fill", make([]byte, 400))
		return publishErr
	})
	if err == nil {
		t.Fatal("startup accepted an exhausted metadata stream")
	}
	if code, ok := errs.CodeOf(err); !ok || code != errCodeBusUnhealthy {
		t.Fatalf("startup error code = %q, %v; want %q: %v", code, ok, errCodeBusUnhealthy, err)
	}

	lock, lockErr := acquireStoreLock(filepath.Join(config.storeDir, ".lock"))
	if lockErr != nil {
		t.Fatalf("failed startup retained the store lock: %v", lockErr)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLocalBusReportsUnexpectedServerExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	bus, err := startLocalBus(ctx, testNormalizedBusConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	bus.server.Shutdown()
	select {
	case err := <-bus.failures():
		if code, ok := errs.CodeOf(err); !ok || code != errCodeBusUnhealthy {
			t.Fatalf("failure code = %q, %v; want %q: %v", code, ok, errCodeBusUnhealthy, err)
		}
	case <-ctx.Done():
		t.Fatal("unexpected server exit was not reported")
	}
	closeBus(t, bus, false)
}

func TestCapacityErrorClassification(t *testing.T) {
	apiErr := &jetstream.APIError{ErrorCode: 10077, Description: "maximum bytes exceeded"}
	if !isCapacityError(apiErr) {
		t.Fatal("pinned JetStream capacity response was not recognized")
	}
	err := busCapacity(apiErr)
	if code, ok := errs.CodeOf(err); !ok || code != errCodeBusCapacity {
		t.Fatalf("capacity code = %q, %v; want %q", code, ok, errCodeBusCapacity)
	}
	if !errs.Retryable(err) {
		t.Fatal("capacity rejection is not retryable")
	}
}

func TestMailboxRejectsNewRecordsAtCapacity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	config, err := normalizeBusConfig(testBusIdentity(), BusConfig{
		StoreDir:         t.TempDir(),
		MaxStoreBytes:    4 << 20,
		MailboxMaxBytes:  64 << 10,
		MetadataMaxBytes: 1 << 20,
		ReserveBytes:     1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	bus, err := startLocalBus(ctx, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeBus(t, bus, false)

	payload := make([]byte, 32<<10)
	var publishErr error
	for range 8 {
		_, publishErr = bus.resources.jetStream.Publish(ctx, mailboxSubjectRoot+".capacity", payload)
		if publishErr != nil {
			break
		}
	}
	if !isCapacityError(publishErr) {
		t.Fatalf("publish error = %v, want pinned capacity response", publishErr)
	}
	if code, ok := errs.CodeOf(busCapacity(publishErr)); !ok || code != errCodeBusCapacity {
		t.Fatalf("coded capacity error = %q, %v", code, ok)
	}
}

func TestBrokerHelperProcess(t *testing.T) {
	mode := os.Getenv("FLOWSEER_BROKER_HELPER_MODE")
	if mode == "" {
		t.Skip("subprocess helper")
	}
	storeDir := os.Getenv("FLOWSEER_BROKER_HELPER_STORE")
	if mode == "delivery_crash" || mode == "delivery_reopen" || mode == "delivery_retry_crash" || mode == "delivery_retry_reopen" {
		runDeliveryCrashHelper(t, mode, storeDir)
		return
	}
	identity := testBusIdentity()
	if version := os.Getenv("FLOWSEER_BROKER_HELPER_VERSION"); version != "" {
		identity.Version = version
	}
	busConfig := BusConfig{StoreDir: storeDir}
	switch policy := os.Getenv("FLOWSEER_BROKER_HELPER_FSYNC_POLICY"); policy {
	case "", "periodic":
	case "per_message":
		busConfig.FsyncPolicy = BusFsyncPerMessage
	default:
		t.Fatalf("unknown helper fsync policy %q", policy)
	}
	config, err := normalizeBusConfig(identity, busConfig)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	bus, err := startLocalBus(ctx, config, nil)
	if err != nil {
		t.Fatal(err)
	}

	switch mode {
	case "batch_publish":
		var firstSequence uint64
		for index := 1; index <= 100; index++ {
			payload := fmt.Appendf(nil, "durable-%03d", index)
			ack, err := bus.resources.jetStream.Publish(ctx, mailboxSubjectRoot+".subprocess", payload)
			if err != nil {
				t.Fatal(err)
			}
			if index == 1 {
				firstSequence = ack.Sequence
			}
			if want := firstSequence + uint64(index-1); ack.Sequence != want {
				t.Fatalf("publish %d sequence = %d, want %d", index, ack.Sequence, want)
			}
		}
		fmt.Println("READY 100")
		_ = os.Stdout.Sync()
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		closeBus(t, bus, true)
	case "batch_reopen":
		mailbox, err := bus.resources.mailbox.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if want := mailboxStreamConfig(config.mailboxMaxBytes); !ownedStreamConfigEqual(mailbox.Config, want) {
			t.Fatalf("mailbox stream drifted: got %+v, want %+v", mailbox.Config, want)
		}
		metadata, err := bus.resources.metadata.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if want := metadataStreamConfig(config.metadataMaxBytes); !ownedStreamConfigEqual(metadata.Config, want) {
			t.Fatalf("metadata stream drifted: got %+v, want %+v", metadata.Config, want)
		}
		if mailbox.State.Msgs != 100 {
			t.Fatalf("reopened mailbox messages = %d, want 100", mailbox.State.Msgs)
		}
		for index := 1; index <= 100; index++ {
			sequence := mailbox.State.FirstSeq + uint64(index-1)
			message, err := bus.resources.mailbox.GetMsg(ctx, sequence)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := string(message.Data), fmt.Sprintf("durable-%03d", index); got != want {
				t.Fatalf("message %d = %q, want %q", index, got, want)
			}
		}
		fmt.Println("FOUND 100")
		_ = os.Stdout.Sync()
		closeBus(t, bus, true)
	case "hold":
		ack, err := bus.resources.jetStream.Publish(ctx, mailboxSubjectRoot+".subprocess", []byte("durable"))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("READY %d\n", ack.Sequence)
		_ = os.Stdout.Sync()
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		closeBus(t, bus, true)
	case "reopen":
		sequence := uint64(0)
		if _, err := fmt.Sscan(os.Getenv("FLOWSEER_BROKER_HELPER_SEQUENCE"), &sequence); err != nil {
			t.Fatal(err)
		}
		message, err := bus.resources.mailbox.GetMsg(ctx, sequence)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("FOUND %s\n", message.Data)
		_ = os.Stdout.Sync()
		closeBus(t, bus, true)
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
}

func runDeliveryCrashHelper(t *testing.T, mode, storeDir string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	config := Config{
		Identity: testBusIdentity(),
		Bus:      &BusConfig{StoreDir: storeDir},
		Modules: []Module{{
			Name: "worker",
			Leaf: &Leaf{
				Subscriptions: []Subscription{{Kind: MessageKindCommand, Message: &emptypb.Empty{}, Retries: deliveryHelperRetries(mode)}},
				Setup: func(attemptCtx context.Context) (Attempt, error) {
					if mode == "delivery_crash" || mode == "delivery_retry_crash" {
						if err := Bus(attemptCtx).Command(attemptCtx, "bus_test/worker", &emptypb.Empty{}); err != nil {
							return Attempt{}, err
						}
					}
					handlerCalls := 0
					return Attempt{
						Runner: func(runCtx context.Context) error {
							<-runCtx.Done()
							return runCtx.Err()
						},
						Handlers: []Handler{{
							Kind:    servicev1.MessageKind_MESSAGE_KIND_COMMAND,
							Message: &emptypb.Empty{},
							Handle: func(context.Context, proto.Message) error {
								handlerCalls++
								if mode == "delivery_crash" {
									fmt.Println("HANDLED before-crash")
									_ = os.Stdout.Sync()
									_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
									return nil
								}
								if mode == "delivery_retry_crash" {
									if handlerCalls == 1 {
										return errs.New().Retryable().Msg("retry before crash")
									}
									fmt.Println("RETRIED after-settlement")
									_ = os.Stdout.Sync()
									_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
									return nil
								}
								if mode == "delivery_retry_reopen" {
									fmt.Println("RECOVERED retry-after-crash")
									_ = os.Stdout.Sync()
									cancel()
									return nil
								}
								fmt.Println("RECOVERED after-crash")
								_ = os.Stdout.Sync()
								cancel()
								return nil
							},
						}},
					}, nil
				},
			},
		}},
	}
	if err := Run(ctx, config); err != nil {
		t.Fatal(err)
	}
}

func deliveryHelperRetries(mode string) int {
	if mode == "delivery_retry_crash" || mode == "delivery_retry_reopen" {
		return 1
	}
	return 0
}

func testBusIdentity() Identity {
	return Identity{Namespace: "flowseer", Name: "bus_test", Version: "test"}
}

func testNormalizedBusConfig(t *testing.T) normalizedBusConfig {
	t.Helper()
	config, err := normalizeBusConfig(testBusIdentity(), BusConfig{
		StoreDir:         t.TempDir(),
		MaxStoreBytes:    8 << 20,
		MailboxMaxBytes:  4 << 20,
		MetadataMaxBytes: 2 << 20,
		ReserveBytes:     2 << 20,
		HealthInterval:   10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func closeBus(t *testing.T, bus *localBus, healthy bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := bus.close(ctx, healthy); err != nil && !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("close local bus: %v", err)
	}
}
