package history

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	"go.aledante.io/FlowSeer/src/common/errs"
)

func TestConfigRejectsWhatCannotNameADeployment(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"no endpoints", func(c *Config) { c.Endpoints = nil }},
		{"endpoint without a port", func(c *Config) { c.Endpoints = []string{"clickhouse"} }},
		{"database that is not an identifier", func(c *Config) { c.Database = "flow-seer" }},
		{"database that would escape its quotes", func(c *Config) { c.Database = "a`; DROP DATABASE x; --" }},
		{"cluster that is not an identifier", func(c *Config) { c.Cluster = "my cluster" }},
		{"user that is not an identifier", func(c *Config) { c.Writer.Name = "w r" }},
		{"user without a password file", func(c *Config) { c.Analyst.AuthFile = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig(t, "")
			tt.mutate(&cfg)

			_, err := Open(cfg)

			if code, _ := errs.CodeOf(err); code != ErrCodeConfig {
				t.Errorf("Open code = %v (err %v), want %v", code, err, ErrCodeConfig)
			}
		})
	}
}

func TestOpenSucceedsWhileTheServerIsUnreachableAndACallFails(t *testing.T) {
	cfg := testConfig(t, "")
	cfg.Endpoints = []string{"127.0.0.1:1"}

	store, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open with an unreachable server: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.Writer().Ping(context.Background()); err == nil {
		t.Fatal("ping an unreachable server succeeded")
	}
}

func TestACallToAServerThatNeverAnswersEndsAtTheDialTimeout(t *testing.T) {
	// The listener completes the TCP handshake through its backlog and never
	// accepts, so the client waits for a server hello that does not come.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	cfg := testConfig(t, "")
	cfg.Endpoints = []string{ln.Addr().String()}
	store, err := Open(cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	if err := store.Writer().Ping(ctx); err == nil {
		t.Fatal("ping a silent server succeeded")
	}

	if elapsed := time.Since(start); elapsed < dialTimeout-time.Second || elapsed > dialTimeout+2*time.Second {
		t.Errorf("the call took %v, want about the %v dial timeout", elapsed, dialTimeout)
	}
}

func TestOpenRefusesACAFileThatHoldsNoCertificate(t *testing.T) {
	cfg := testConfig(t, "")
	cfg.CAFile = filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(cfg.CAFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(cfg); err == nil {
		t.Fatal("Open accepted a CA file with no certificate")
	}
}

// fakeDial records the options each dial received and answers per address.
type fakeDial struct {
	attempts  []string
	passwords []string
	failing   map[string]bool
}

func (f *fakeDial) dial(_ context.Context, addr string, opt *clickhouse.Options) (clickhouse.DialResult, error) {
	f.attempts = append(f.attempts, addr)
	f.passwords = append(f.passwords, opt.Auth.Password)
	if f.failing[addr] {
		return clickhouse.DialResult{}, errs.Msg("refused")
	}
	return clickhouse.DialResult{}, nil
}

func TestDialStrategyReadsThePasswordFileAtEveryDial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pw")
	write := func(s string) {
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	strategy := dialStrategy(User{Name: "w", AuthFile: path})
	opt := &clickhouse.Options{Addr: []string{"a:9000"}, Auth: clickhouse.Auth{Password: "stale"}}
	f := &fakeDial{}

	write("first\n")
	if _, err := strategy(context.Background(), 1, opt, f.dial); err != nil {
		t.Fatalf("first dial: %v", err)
	}
	write("rotated")
	if _, err := strategy(context.Background(), 2, opt, f.dial); err != nil {
		t.Fatalf("second dial: %v", err)
	}

	if got := f.passwords; len(got) != 2 || got[0] != "first" || got[1] != "rotated" {
		t.Errorf("passwords dialed = %q, want [first rotated]", got)
	}
	if opt.Auth.Password != "stale" {
		t.Errorf("the pool's own options were changed to %q", opt.Auth.Password)
	}
}

func TestDialStrategyTriesEndpointsInOrderAndTakesTheFirstThatAnswers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	strategy := dialStrategy(User{Name: "w", AuthFile: path})
	opt := &clickhouse.Options{Addr: []string{"a:9000", "b:9000", "c:9000"}}
	f := &fakeDial{failing: map[string]bool{"a:9000": true}}

	if _, err := strategy(context.Background(), 1, opt, f.dial); err != nil {
		t.Fatalf("dial: %v", err)
	}

	if got := f.attempts; len(got) != 2 || got[0] != "a:9000" || got[1] != "b:9000" {
		t.Errorf("attempts = %q, want [a:9000 b:9000]", got)
	}
}

func TestDialStrategyFailsWithoutDialingWhenThePasswordCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, file := range map[string]string{"missing file": filepath.Join(dir, "absent"), "empty file": empty} {
		t.Run(name, func(t *testing.T) {
			f := &fakeDial{}

			_, err := dialStrategy(User{Name: "w", AuthFile: file})(context.Background(), 1, &clickhouse.Options{Addr: []string{"a:9000"}}, f.dial)

			if code, _ := errs.CodeOf(err); code != ErrCodePassword {
				t.Errorf("code = %v (err %v), want %v", code, err, ErrCodePassword)
			}
			if len(f.attempts) != 0 {
				t.Errorf("dialed %q with no password", f.attempts)
			}
		})
	}
}
