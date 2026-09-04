package snmp

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/service"
)

func TestServiceLoggerReachesBackgroundSNMPPaths(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})).
		With("source", "service-context")

	err := service.Run(context.Background(), service.Config{
		Identity: service.Identity{Name: "snmp_context_test", Namespace: "flowseer", Version: "test"},
		Logger:   logger,
		Setup: func(ctx context.Context) (service.Attempt, error) {
			exerciseReactorLogger(ctx, t)
			exerciseTrapLogger(ctx, t)
			exercisePrivacyLogger(ctx, t)
			return service.Attempt{Runner: func(context.Context) error { return nil }}, nil
		},
	})
	if err != nil {
		t.Fatalf("running service attempt: %v", err)
	}

	for _, message := range []string{
		"snmp: dropping reply from unexpected source",
		"snmp: dropping undecodable trap",
		"snmp: 3DES localized key has non-distinct sub-keys (accepted for interop)",
	} {
		if !strings.Contains(logs.String(), message) {
			t.Errorf("logs do not contain %q:\n%s", message, logs.String())
		}
	}
	if got := strings.Count(logs.String(), "source=service-context"); got < 3 {
		t.Errorf("service logger attribute appears %d times, want at least 3:\n%s", got, logs.String())
	}
}

func exerciseReactorLogger(ctx context.Context, t *testing.T) {
	t.Helper()

	peer := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
	r, err := newReactor(ctx, reactorConfig{
		peer:        peer,
		multiHomed:  true,
		validateSrc: true,
		version:     V2c,
		community:   "public",
	})
	if err != nil {
		t.Fatalf("starting reactor: %v", err)
	}
	t.Cleanup(func() { _ = r.close() })

	local := r.conn.LocalAddr().(*net.UDPAddr)
	destination := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: local.Port}
	sender, err := net.DialUDP("udp", nil, destination)
	if err != nil {
		t.Fatalf("dialing reactor socket: %v", err)
	}
	defer func() { _ = sender.Close() }()
	if _, err := sender.Write([]byte{0}); err != nil {
		t.Fatalf("writing reactor datagram: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for r.dropped.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if r.dropped.Load() == 0 {
		t.Fatal("reactor did not reject the unexpected source")
	}
	if err := r.close(); err != nil {
		t.Fatalf("closing reactor: %v", err)
	}
}

func exerciseTrapLogger(ctx context.Context, t *testing.T) {
	t.Helper()

	ts := NewTrapStream(ctx, 1)
	t.Cleanup(func() { _ = ts.Close() })
	l := &listener{
		ts:      ts,
		matcher: newNetMatcher(nil),
		limiter: newTokenBucket(0),
		logCtx:  context.WithoutCancel(ctx),
	}
	l.handlePacket([]byte{0x30, 0x01, 0xff}, nil)
	if ts.Dropped() != 1 {
		t.Fatalf("dropped traps = %d, want 1", ts.Dropped())
	}
}

func exercisePrivacyLogger(ctx context.Context, t *testing.T) {
	t.Helper()

	block := []byte{1, 1, 1, 1, 1, 1, 1, 1}
	key := bytes.Join([][]byte{block, block, block, {2, 3, 4, 5, 6, 7, 8, 9}}, nil)
	if _, err := newPrivContext(ctx, Priv3DES, key); err != nil {
		t.Fatalf("creating privacy context: %v", err)
	}
}
