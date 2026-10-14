package edgebus

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
)

func cleanupHub(t *testing.T, dir string) *Hub {
	t.Helper()
	h, err := StartHub(context.Background(), HubConfig{StateDir: dir, FsyncPolicy: service.BusFsyncPerMessage, IngestMaxBytes: 240})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h
}

func cleanupStream(t *testing.T, h *Hub) jetstream.Stream {
	t.Helper()
	s, err := h.JetStream().Stream(context.Background(), IngestStream("syslog"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func cleanupPublish(t *testing.T, h *Hub, n int) {
	t.Helper()
	if _, err := h.JetStream().Publish(context.Background(), CentralIngestSubject("tenant", "syslog", fmt.Sprint(n)), []byte("record")); err != nil {
		t.Fatal(err)
	}
}

func cleanupNext(t *testing.T, c jetstream.Consumer) jetstream.Msg {
	t.Helper()
	m, err := c.Next(jetstream.FetchMaxWait(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func cleanupState(t *testing.T, s jetstream.Stream, first, count uint64) *jetstream.StreamInfo {
	t.Helper()
	info, err := s.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.State.FirstSeq != first || info.State.Msgs != count {
		t.Fatalf("state first=%d messages=%d, want %d/%d", info.State.FirstSeq, info.State.Msgs, first, count)
	}
	return info
}

func cleanupError(t *testing.T, err error) {
	t.Helper()
	if code, ok := errs.CodeOf(err); !ok || code != ErrCodeCleanup {
		t.Fatalf("error=%v code=%v, want cleanup refusal", err, code)
	}
}

func TestCleanupSharedConsumerResolvesOnlyPrefix(t *testing.T) {
	ctx := context.Background()
	h := cleanupHub(t, t.TempDir())
	s := cleanupStream(t, h)
	c, err := s.Consumer(ctx, "history_sink")
	if err != nil {
		t.Fatalf("member absent before publication: %v", err)
	}
	member, err := c.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if member.Config.AckWait != 2*time.Minute || member.Config.MaxAckPending != 10000 || member.Config.Durable != "history_sink" || member.Config.FilterSubject != "" || len(member.Config.FilterSubjects) != 0 || member.Config.DeliverPolicy != jetstream.DeliverAllPolicy || member.Config.AckPolicy != jetstream.AckExplicitPolicy || member.Config.MaxDeliver != -1 {
		t.Fatalf("initial member config=%+v", member.Config)
	}
	peerConn, peerJS, err := h.connectAccount(h.server, h.keys.central, h.centralBudgetJWT(t), "cleanup-peer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(peerConn.Close)
	peer, err := peerJS.Consumer(ctx, IngestStream("syslog"), "history_sink")
	if err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 3; n++ {
		cleanupPublish(t, h, n)
	}
	unresolved, err := CleanIngest(ctx, h.JetStream(), "syslog")
	if err != nil || unresolved.Floor != 0 || unresolved.Purged || unresolved.Pending != 3 {
		t.Fatalf("undelivered report=%+v error=%v", unresolved, err)
	}
	cleanupState(t, s, 1, 3)
	first := cleanupNext(t, c)
	second := cleanupNext(t, peer)
	if err := second.DoubleAck(ctx); err != nil {
		t.Fatal(err)
	}
	before := cleanupState(t, s, 1, 3)
	report, err := CleanIngest(ctx, h.JetStream(), "syslog")
	if err != nil {
		t.Fatal(err)
	}
	if report.Floor != 0 || report.Purged || report.Pending != 2 || report.Bytes != before.State.Bytes || report.MaxBytes != 240 || report.FirstTime != before.State.FirstTime || report.FirstSequence != 1 || report.LastSequence != 3 {
		t.Fatalf("report=%+v, state=%+v", report, before.State)
	}
	cleanupState(t, s, 1, 3)
	if _, err := h.JetStream().Publish(ctx, CentralIngestSubject("tenant", "syslog", "4"), []byte("record")); !IsStreamFull(err) {
		t.Fatalf("fourth publish=%v, want full", err)
	}
	if err := first.DoubleAck(ctx); err != nil {
		t.Fatal(err)
	}
	report, err = CleanIngest(ctx, h.JetStream(), "syslog")
	if err != nil || !report.Purged || report.Floor != 2 {
		t.Fatalf("resolved report=%+v error=%v", report, err)
	}
	cleanupState(t, s, 3, 1)
	cleanupPublish(t, h, 4)
	cleanupState(t, s, 3, 2)
}

func (h *Hub) centralBudgetJWT(t *testing.T) string {
	t.Helper()
	j, err := h.keys.accountJWT(h.keys.central, "CENTRAL", h.centralBudget)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestCleanupRefusesMissingDifferingAndStrayConsumers(t *testing.T) {
	for _, kind := range []string{"missing", "filter", "filters", "delivery_limit", "ack_all", "deliver_new", "stray"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			h := cleanupHub(t, t.TempDir())
			s := cleanupStream(t, h)
			cleanupPublish(t, h, 1)
			cfg := IngestConsumerConfig("history_sink")
			switch kind {
			case "missing", "ack_all", "deliver_new":
				if err := s.DeleteConsumer(ctx, cfg.Durable); err != nil {
					t.Fatal(err)
				}
				if kind == "ack_all" {
					cfg.AckPolicy = jetstream.AckAllPolicy
				}
				if kind == "deliver_new" {
					cfg.DeliverPolicy = jetstream.DeliverNewPolicy
				}
				if kind != "missing" {
					if _, err := s.CreateConsumer(ctx, cfg); err != nil {
						t.Fatal(err)
					}
				}
			case "filter", "filters", "delivery_limit":
				if kind == "filter" {
					cfg.FilterSubject = CentralIngestSubject("tenant", "syslog", "1")
				}
				if kind == "filters" {
					cfg.FilterSubjects = []string{CentralIngestSubject("tenant", "syslog", "1")}
				}
				if kind == "delivery_limit" {
					cfg.MaxDeliver = 3
				}
				if _, err := s.UpdateConsumer(ctx, cfg); err != nil {
					t.Fatal(err)
				}
			case "stray":
				if _, err := s.CreateConsumer(ctx, IngestConsumerConfig("stray")); err != nil {
					t.Fatal(err)
				}
			}
			_, err := CleanIngest(ctx, h.JetStream(), "syslog")
			cleanupError(t, err)
			cleanupState(t, s, 1, 1)
			if kind != "missing" && kind != "stray" {
				_, err := EnsureIngestConsumer(ctx, h.JetStream(), "syslog", cfg.Durable)
				cleanupError(t, err)
				c, err := s.Consumer(ctx, cfg.Durable)
				if err != nil {
					t.Fatal(err)
				}
				info, err := c.Info(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if info.Config.FilterSubject != cfg.FilterSubject || len(info.Config.FilterSubjects) != len(cfg.FilterSubjects) || info.Config.MaxDeliver != cfg.MaxDeliver || info.Config.DeliverPolicy != cfg.DeliverPolicy || info.Config.AckPolicy != cfg.AckPolicy {
					t.Fatalf("consumer repaired: %+v", info.Config)
				}
			}
			if kind == "stray" {
				if err := s.DeleteConsumer(ctx, "stray"); err != nil {
					t.Fatal(err)
				}
				c, err := s.Consumer(ctx, "history_sink")
				if err != nil {
					t.Fatal(err)
				}
				if err := cleanupNext(t, c).DoubleAck(ctx); err != nil {
					t.Fatal(err)
				}
				r, err := CleanIngest(ctx, h.JetStream(), "syslog")
				if err != nil || !r.Purged {
					t.Fatalf("after stray deletion: %+v %v", r, err)
				}
			}
		})
	}
}

func TestCleanupRecreatedMemberReoffersSkippedAndExhaustedRecords(t *testing.T) {
	for _, kind := range []string{"filtered", "exhausted"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			h := cleanupHub(t, t.TempDir())
			s := cleanupStream(t, h)
			cleanupPublish(t, h, 1)
			cfg := IngestConsumerConfig("history_sink")
			if kind == "filtered" {
				cleanupPublish(t, h, 2)
				cfg.FilterSubject = CentralIngestSubject("tenant", "syslog", "2")
			} else {
				cfg.MaxDeliver = 1
				cfg.AckWait = 20 * time.Millisecond
			}
			c, err := s.UpdateConsumer(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			m := cleanupNext(t, c)
			if kind == "filtered" {
				if err := m.DoubleAck(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := c.Next(jetstream.FetchMaxWait(100 * time.Millisecond)); err == nil {
					t.Fatal("exhausted record delivered twice")
				}
			}
			_, err = CleanIngest(ctx, h.JetStream(), "syslog")
			cleanupError(t, err)
			if _, err := s.GetMsg(ctx, 1); err != nil {
				t.Fatalf("unresolved record removed: %v", err)
			}
			if err := s.DeleteConsumer(ctx, cfg.Durable); err != nil {
				t.Fatal(err)
			}
			c, err = EnsureIngestConsumer(ctx, h.JetStream(), "syslog", cfg.Durable)
			if err != nil {
				t.Fatal(err)
			}
			count := 1
			if kind == "filtered" {
				count = 2
			}
			for seq := 1; seq <= count; seq++ {
				m := cleanupNext(t, c)
				md, err := m.Metadata()
				if err != nil {
					t.Fatal(err)
				}
				if md.Sequence.Stream != uint64(seq) {
					t.Fatalf("recreated delivery=%d, want %d", md.Sequence.Stream, seq)
				}
			}
			r, err := CleanIngest(ctx, h.JetStream(), "syslog")
			if err != nil || r.Purged {
				t.Fatalf("pending report=%+v error=%v", r, err)
			}
			cleanupState(t, s, 1, uint64(count))
		})
	}
}

func TestCleanupConcurrentPassesTerminationAndRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	h := cleanupHub(t, dir)
	s := cleanupStream(t, h)
	for n := 1; n <= 3; n++ {
		cleanupPublish(t, h, n)
	}
	c, err := s.Consumer(ctx, "history_sink")
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanupNext(t, c).DoubleAck(ctx); err != nil {
		t.Fatal(err)
	}
	if err := cleanupNext(t, c).Term(); err != nil {
		t.Fatal(err)
	}
	_ = cleanupNext(t, c)
	deadline := time.Now().Add(time.Second)
	for {
		info, err := c.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if info.AckFloor.Stream == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("termination floor=%d", info.AckFloor.Stream)
		}
		time.Sleep(time.Millisecond)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := CleanIngest(ctx, h.JetStream(), "syslog"); results <- err }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	cleanupState(t, s, 3, 1)
	h.Close()
	h = cleanupHub(t, dir)
	s = cleanupStream(t, h)
	cleanupState(t, s, 3, 1)
	if err := s.DeleteConsumer(ctx, "history_sink"); err != nil {
		t.Fatal(err)
	}
	c, err = EnsureIngestConsumer(ctx, h.JetStream(), "syslog", "history_sink")
	if err != nil {
		t.Fatal(err)
	}
	m := cleanupNext(t, c)
	md, err := m.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	if md.Sequence.Stream != 3 {
		t.Fatalf("recreated member got %d, want pending 3", md.Sequence.Stream)
	}
}

func TestCleanupLowestMemberFloorAndLateStray(t *testing.T) {
	ctx := context.Background()
	h := cleanupHub(t, t.TempDir())
	s := cleanupStream(t, h)
	other, err := s.CreateConsumer(ctx, IngestConsumerConfig("second"))
	if err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 3; n++ {
		cleanupPublish(t, h, n)
	}
	c, err := s.Consumer(ctx, "history_sink")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := cleanupNext(t, c).DoubleAck(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := cleanupNext(t, other).DoubleAck(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := cleanIngest(ctx, s, []string{"history_sink", "second"}, nil)
	// history_sink has one record left to receive and second has two, so the
	// sum across members is 3.
	if err != nil || r.Floor != 1 || !r.Purged || r.Pending != 3 {
		t.Fatalf("lowest report=%+v error=%v", r, err)
	}
	cleanupState(t, s, 2, 2)
	if err := s.DeleteConsumer(ctx, "second"); err != nil {
		t.Fatal(err)
	}
	var stray jetstream.Consumer
	r, err = cleanIngest(ctx, s, IngestConsumers("syslog"), func() {
		var createErr error
		stray, createErr = s.CreateConsumer(ctx, IngestConsumerConfig("late"))
		if createErr != nil {
			t.Fatal(createErr)
		}
	})
	if err != nil || !r.Purged {
		t.Fatalf("late pass=%+v %v", r, err)
	}
	cleanupState(t, s, 3, 1)
	_, err = CleanIngest(ctx, h.JetStream(), "syslog")
	cleanupError(t, err)
	m := cleanupNext(t, stray)
	md, err := m.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	if md.Sequence.Stream != 3 {
		t.Fatalf("late stray got sequence %d, want 3", md.Sequence.Stream)
	}
}

func TestEnsureIngestConsumerRefusesUnknownName(t *testing.T) {
	h := cleanupHub(t, t.TempDir())
	_, err := EnsureIngestConsumer(context.Background(), h.JetStream(), "syslog", "unknown")
	cleanupError(t, err)
}

func TestIsStreamFullMatchesOnlyByteRefusal(t *testing.T) {
	for _, tc := range []struct {
		code        jetstream.ErrorCode
		description string
		full        bool
	}{
		{10077, "maximum bytes exceeded", true}, {10077, "maximum messages exceeded", false}, {10077, "other store failure", false}, {10047, "maximum bytes exceeded", false},
	} {
		err := fmt.Errorf("publish: %w", &jetstream.APIError{Code: 503, ErrorCode: tc.code, Description: tc.description})
		if IsStreamFull(err) != tc.full {
			t.Fatalf("IsStreamFull(%v)=%v, want %v", err, IsStreamFull(err), tc.full)
		}
	}
	if IsStreamFull(nil) {
		t.Fatal("nil error is full")
	}
}

func TestCleanupAtFloorZeroSendsNoPurge(t *testing.T) {
	ctx := context.Background()
	h := cleanupHub(t, t.TempDir())
	s := cleanupStream(t, h)
	empty, err := CleanIngest(ctx, h.JetStream(), "syslog")
	if err != nil || empty.Purged || empty.Floor != 0 {
		t.Fatalf("empty stream report=%+v error=%v, want floor 0 and no purge", empty, err)
	}
	cleanupPublish(t, h, 1)
	cleanupPublish(t, h, 2)
	c, err := s.Consumer(ctx, "history_sink")
	if err != nil {
		t.Fatal(err)
	}
	cleanupNext(t, c)
	report, err := CleanIngest(ctx, h.JetStream(), "syslog")
	if err != nil || report.Purged || report.Floor != 0 {
		t.Fatalf("report=%+v error=%v, want floor 0 and no purge", report, err)
	}
	cleanupState(t, s, 1, 2)
}

func TestCleanupAcceptsAMemberAlreadyOnDiskWithTheSinkConfig(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	h := cleanupHub(t, dir)
	s := cleanupStream(t, h)
	if err := s.DeleteConsumer(ctx, "history_sink"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateConsumer(ctx, jetstream.ConsumerConfig{
		Durable: "history_sink", AckPolicy: jetstream.AckExplicitPolicy,
		AckWait: 2 * time.Minute, MaxDeliver: -1, MaxAckPending: 10000,
	}); err != nil {
		t.Fatal(err)
	}
	cleanupPublish(t, h, 1)
	h.Close()
	h = cleanupHub(t, dir)
	s = cleanupStream(t, h)
	c, err := s.Consumer(ctx, "history_sink")
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanupNext(t, c).DoubleAck(ctx); err != nil {
		t.Fatal(err)
	}
	report, err := CleanIngest(ctx, h.JetStream(), "syslog")
	if err != nil || !report.Purged || report.Floor != 1 {
		t.Fatalf("report=%+v error=%v, want purge at floor 1", report, err)
	}
	cleanupState(t, s, 2, 0)
}

func TestCleanupRefusesAStrayConsumerInPlaceOfTheMember(t *testing.T) {
	ctx := context.Background()
	h := cleanupHub(t, t.TempDir())
	s := cleanupStream(t, h)
	cleanupPublish(t, h, 1)
	if err := s.DeleteConsumer(ctx, "history_sink"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateConsumer(ctx, IngestConsumerConfig("stray")); err != nil {
		t.Fatal(err)
	}
	_, err := CleanIngest(ctx, h.JetStream(), "syslog")
	cleanupError(t, err)
	cleanupState(t, s, 1, 1)
}
