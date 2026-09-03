package integration_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/syslog"
)

// TestOverload is opt-in because it records ten minutes of post-GC samples.
// Run with FLOWSEER_SYSLOG_OVERLOAD=1; ordinary package tests cover short bounds.
func TestOverload(t *testing.T) {
	if os.Getenv("FLOWSEER_SYSLOG_OVERLOAD") != "1" {
		t.Skip("set FLOWSEER_SYSLOG_OVERLOAD=1 for ten-minute memory measurement")
	}
	server, client := certificates(t, 120<<10)
	server.ClientAuth = tls.RequireAndVerifyClientCert
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var receivers []*syslog.Receiver
	var producers, consumers sync.WaitGroup
	for _, small := range []bool{false, true} {
		for _, raw := range []bool{false, true} {
			limits := syslog.Limits{PressureTimeout: 300 * time.Millisecond, HandshakeTimeout: time.Second}
			if small {
				limits.MaxPayload = 4096
				limits.MaxBytes = 2 << 20
				limits.MaxConnections = 4
				limits.MaxHandshakes = 2
				limits.MaxFrames = 8
			}
			r, err := syslog.Listen(ctx, []syslog.ListenConfig{{Transport: syslog.UDP, Address: "127.0.0.1:0"}, {Transport: syslog.TCP, Address: "127.0.0.1:0"}, {Transport: syslog.TLS, Address: "127.0.0.1:0", TLSConfig: server}}, syslog.ReceiverOptions{Limits: limits, Parse: syslog.ParseOptions{CaptureRaw: raw}})
			if err != nil {
				t.Fatal(err)
			}
			receivers = append(receivers, r)
			for _, endpoint := range r.Addresses() {
				producers.Add(1)
				go func() { defer producers.Done(); flood(ctx, endpoint, client) }()
			}
		}
	}
	defer func() {
		cancel()
		for _, r := range receivers {
			closeChecked(t, r)
		}
		producers.Wait()
		consumers.Wait()
	}()
	start := time.Now()
	samples := make([][]uint64, 10)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	slowStarted := false
	for at := range ticker.C {
		elapsed := at.Sub(start)
		minute := int(elapsed / time.Minute)
		if minute > 9 {
			minute = 9
		}
		if elapsed >= 5*time.Minute && !slowStarted {
			slowStarted = true
			for _, r := range receivers {
				consumers.Add(1)
				go func() {
					defer consumers.Done()
					tick := time.NewTicker(20 * time.Millisecond)
					defer tick.Stop()
					for {
						select {
						case <-ctx.Done():
							return
						case <-tick.C:
							next, cancelNext := context.WithTimeout(ctx, 10*time.Millisecond)
							_, _ = r.Next(next)
							cancelNext()
						}
					}
				}()
			}
		}
		runtime.GC()
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		samples[minute] = append(samples[minute], memory.HeapAlloc)
		rss := "unavailable"
		if b, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output(); err == nil {
			rss = strings.TrimSpace(string(b))
		}
		for i, r := range receivers {
			stats := r.Stats()
			limit := 32 << 20
			if i >= 2 {
				limit = 2 << 20
			}
			if stats.ReservedBytes > limit {
				t.Fatalf("profile %d budget %+v", i, stats)
			}
		}
		t.Logf("elapsed=%ds live_heap=%d heap_inuse=%d stack_inuse=%d rss_kib=%s goroutines=%d", int(elapsed.Seconds()), memory.HeapAlloc, memory.HeapInuse, memory.StackInuse, rss, runtime.NumGoroutine())
		if elapsed >= 10*time.Minute {
			break
		}
	}
	medians := make([]uint64, len(samples))
	for i, minute := range samples {
		if len(minute) == 0 {
			t.Fatalf("missing minute %d", i)
		}
		slices.Sort(minute)
		medians[i] = minute[len(minute)/2]
	}
	baseline := medians[1]
	allowance := max(baseline/10, 2<<20)
	for i := 7; i < 10; i++ {
		if medians[i] > baseline+allowance {
			t.Errorf("live heap did not plateau: baseline=%d minute=%d median=%d allowance=%d", baseline, i, medians[i], allowance)
		}
	}
	t.Logf("minute_medians=%v", medians)
	for i, r := range receivers {
		stats := r.Stats()
		t.Logf("profile=%d stats=%+v", i, stats)
		if stats.UDPDropped == 0 {
			t.Errorf("profile %d did not exercise UDP pressure", i)
		}
	}
	cancel()
	producers.Wait()
	consumers.Wait()
	for _, r := range receivers {
		closeChecked(t, r)
		if r.Stats().ReservedBytes != 0 {
			t.Fatal("close retained reservations", r.Stats())
		}
	}
}

func flood(ctx context.Context, endpoint syslog.Endpoint, config *tls.Config) {
	payload := append([]byte("<134>1 2026-09-03T10:00:00Z switch app - - [meta a=\"value\"] "), bytes.Repeat([]byte("x"), 1024)...)
	frame := append([]byte(fmt.Sprintf("%d ", len(payload))), payload...)
	network := "tcp"
	if endpoint.Transport == syslog.UDP {
		network = "udp"
		frame = payload
	}
	dialer := net.Dialer{Timeout: time.Second}
	for ctx.Err() == nil {
		raw, err := dialer.DialContext(ctx, network, endpoint.Address)
		if err != nil {
			return
		}
		conn := raw
		if endpoint.Transport == syslog.TLS {
			secure := tls.Client(raw, config)
			handshake, cancel := context.WithTimeout(ctx, time.Second)
			err = secure.HandshakeContext(handshake)
			cancel()
			if err != nil {
				_ = raw.Close()
				continue
			}
			conn = secure
		}
		tick := time.NewTicker(time.Millisecond)
		for ctx.Err() == nil {
			if err = conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
				break
			}
			if _, err = conn.Write(frame); err != nil {
				break
			}
			select {
			case <-ctx.Done():
			case <-tick.C:
			}
		}
		tick.Stop()
		_ = raw.Close()
	}
}
