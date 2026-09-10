package ssh_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	xssh "golang.org/x/crypto/ssh"

	"go.aledante.io/FlowSeer/src/protocol/ssh"
)

// readCommandLine reads bytes from r up to and including the next
// "\n", simulating a shell that must consume the caller's command
// before it can respond.
func readCommandLine(r io.Reader) string {
	var buf []byte
	one := make([]byte, 1)
	for {
		n, err := r.Read(one)
		if n > 0 {
			buf = append(buf, one[0])
			if one[0] == '\n' {
				break
			}
		}
		if err != nil {
			break
		}
	}
	return string(buf)
}

// dialSession opens a Session against fs, applying any option
// overrides, and registers its Close for test cleanup.
func dialSession(t *testing.T, fs *fakeServer, override func(*ssh.Options)) *ssh.Session {
	t.Helper()
	opts := optsFor(fs)
	if override != nil {
		override(&opts)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := ssh.Dial(ctx, fs.addr, opts)
	if err != nil {
		t.Fatalf("Dial() = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

var privPrompt = ssh.Prompt{Name: "privileged", Pattern: regexp.MustCompile(`(?m)switch#\s*$`)}

func TestRunPromptSplitAcrossReads(t *testing.T) {
	t.Parallel()
	fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		readCommandLine(ch)
		_, _ = ch.Write([]byte("resu"))
		time.Sleep(20 * time.Millisecond)
		_, _ = ch.Write([]byte("lt\r\n"))
		time.Sleep(20 * time.Millisecond)
		_, _ = ch.Write([]byte("switch#"))
	})
	s := dialSession(t, fs, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := s.Run(ctx, ssh.Command{Line: "show version", Prompts: []ssh.Prompt{privPrompt}})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if res.MatchedPrompt != "privileged" {
		t.Errorf("MatchedPrompt = %q, want %q", res.MatchedPrompt, "privileged")
	}
	if !bytes.Contains(res.Output, []byte("result")) {
		t.Errorf("Output = %q, want it to contain %q", res.Output, "result")
	}
}

func TestRunStripsEchoedInput(t *testing.T) {
	t.Parallel()
	const line = "show version"
	fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		got := readCommandLine(ch)
		_, _ = ch.Write([]byte(got)) // echo exactly what was sent, including "\n"
		_, _ = ch.Write([]byte("output text\r\nswitch#"))
	})
	s := dialSession(t, fs, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := s.Run(ctx, ssh.Command{Line: line, Prompts: []ssh.Prompt{privPrompt}})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if bytes.HasPrefix(res.Output, []byte(line)) {
		t.Errorf("Output = %q, still starts with the echoed command", res.Output)
	}
	if !bytes.Contains(res.Output, []byte("output text")) {
		t.Errorf("Output = %q, want it to contain %q", res.Output, "output text")
	}
}

func TestRunPagination(t *testing.T) {
	t.Parallel()
	// The handler runs in its own goroutine and must never call a
	// *testing.T method after Run returns and the test may have
	// already finished: report its one assertion through a buffered
	// channel the main goroutine reads instead.
	keyResult := make(chan error, 1)
	fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		readCommandLine(ch)
		_, _ = ch.Write([]byte("page one\r\n--More--"))
		key := make([]byte, 1)
		if _, err := io.ReadFull(ch, key); err != nil {
			keyResult <- fmt.Errorf("read continuation keystroke: %w", err)
			return
		}
		if key[0] != ' ' {
			keyResult <- fmt.Errorf("continuation keystroke = %q, want %q", key, " ")
			return
		}
		keyResult <- nil
		_, _ = ch.Write([]byte("\r\npage two\r\nswitch#"))
	})
	s := dialSession(t, fs, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := s.Run(ctx, ssh.Command{
		Line:          "show run",
		Prompts:       []ssh.Prompt{privPrompt},
		MorePattern:   regexp.MustCompile(`--More--`),
		MoreKeystroke: []byte(" "),
	})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	// Run only returns after the handler wrote page two, which it
	// only does after sending keyResult, so this receive cannot block.
	if kerr := <-keyResult; kerr != nil {
		t.Fatal(kerr)
	}
	if bytes.Contains(res.Output, []byte("--More--")) {
		t.Errorf("Output = %q, still contains the pagination marker", res.Output)
	}
	if !bytes.Contains(res.Output, []byte("page one")) || !bytes.Contains(res.Output, []byte("page two")) {
		t.Errorf("Output = %q, want both pages", res.Output)
	}
}

func TestRunCommandDeadlineExceeded(t *testing.T) {
	t.Parallel()
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		readCommandLine(ch)
		<-stop // never respond
	})
	s := dialSession(t, fs, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err := s.Run(ctx, ssh.Command{
		Line:     "show version",
		Prompts:  []ssh.Prompt{privPrompt},
		Deadline: 200 * time.Millisecond,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want context.DeadlineExceeded", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("Run() took too long: %s", time.Since(start))
	}

	_, err = s.Run(ctx, ssh.Command{Line: "show version", Prompts: []ssh.Prompt{privPrompt}})
	if !errors.Is(err, ssh.ErrSessionClosed) {
		t.Fatalf("second Run() error = %v, want ErrSessionClosed (deadline closes the session)", err)
	}
}

func TestRunCancellationMidWait(t *testing.T) {
	t.Parallel()
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		readCommandLine(ch)
		<-stop // never respond
	})
	s := dialSession(t, fs, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	runCtx, runCancel := context.WithCancel(ctx)
	go func() {
		time.Sleep(100 * time.Millisecond)
		runCancel()
	}()
	_, err := s.Run(runCtx, ssh.Command{Line: "show version", Prompts: []ssh.Prompt{privPrompt}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}

	_, err = s.Run(ctx, ssh.Command{Line: "show version", Prompts: []ssh.Prompt{privPrompt}})
	if !errors.Is(err, ssh.ErrSessionClosed) {
		t.Fatalf("second Run() error = %v, want ErrSessionClosed (cancellation closes the session)", err)
	}
}

func TestRunOutputCapTruncates(t *testing.T) {
	t.Parallel()
	const flood = 4096
	fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		readCommandLine(ch)
		chunk := bytes.Repeat([]byte("x"), 512)
		for range flood / len(chunk) {
			_, _ = ch.Write(chunk)
		}
		_, _ = ch.Write([]byte("\r\nswitch#"))
	})
	s := dialSession(t, fs, func(o *ssh.Options) { o.StdoutBufferBytes = 1024 })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := s.Run(ctx, ssh.Command{
		Line:      "show tech",
		Prompts:   []ssh.Prompt{privPrompt},
		MaxOutput: 256,
	})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if !res.Truncated {
		t.Error("Truncated = false, want true for output far exceeding MaxOutput")
	}
	if len(res.Output) > 256 {
		t.Errorf("len(Output) = %d, want <= 256", len(res.Output))
	}
	if res.Evidence.BytesReceived < flood {
		t.Errorf("BytesReceived = %d, want >= %d (the true flood size)", res.Evidence.BytesReceived, flood)
	}
}

func TestRunStdoutBufferSaturationReportsTruncation(t *testing.T) {
	t.Parallel()
	const bufCap = 512
	const flood = bufCap * 10
	fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		readCommandLine(ch)
		chunk := bytes.Repeat([]byte("y"), 256)
		for range flood / len(chunk) {
			_, _ = ch.Write(chunk)
		}
		_, _ = ch.Write([]byte("\r\nswitch#"))
	})
	s := dialSession(t, fs, func(o *ssh.Options) { o.StdoutBufferBytes = bufCap })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// No MaxOutput set: any truncation here comes only from the
	// session's bounded stdout ring dropping the oldest bytes, not
	// from the MaxOutput cap.
	res, err := s.Run(ctx, ssh.Command{Line: "show tech", Prompts: []ssh.Prompt{privPrompt}})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if !res.Truncated {
		t.Error("Truncated = false, want true when the stdout ring dropped bytes")
	}
	if len(res.Output) > bufCap {
		t.Errorf("len(Output) = %d, want <= the ring's %d-byte capacity", len(res.Output), bufCap)
	}
	if res.Evidence.BytesReceived < flood {
		t.Errorf("BytesReceived = %d, want >= %d (the true flood size, independent of the ring cap)", res.Evidence.BytesReceived, flood)
	}
}

func TestRunStderrSaturationDoesNotBlockStdout(t *testing.T) {
	t.Parallel()
	fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		readCommandLine(ch)
		stderrStop := make(chan struct{})
		go func() {
			defer close(stderrStop)
			deadline := time.Now().Add(500 * time.Millisecond)
			chunk := bytes.Repeat([]byte("e"), 256)
			for time.Now().Before(deadline) {
				if _, err := ch.Stderr().Write(chunk); err != nil {
					return
				}
			}
		}()
		time.Sleep(100 * time.Millisecond)
		_, _ = ch.Write([]byte("ok\r\nswitch#"))
		<-stderrStop
	})
	s := dialSession(t, fs, func(o *ssh.Options) { o.StderrBufferBytes = 512 })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := s.Run(ctx, ssh.Command{Line: "show version", Prompts: []ssh.Prompt{privPrompt}})
	if err != nil {
		t.Fatalf("Run() = %v, want the stderr flood to never block stdout completion", err)
	}
	if !bytes.Contains(res.Output, []byte("ok")) {
		t.Errorf("Output = %q, want it to contain %q", res.Output, "ok")
	}
}

func TestRunConnectionLostAfterCommandSent(t *testing.T) {
	t.Parallel()
	fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		readCommandLine(ch)
		_ = ch.Close()
	})
	s := dialSession(t, fs, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := s.Run(ctx, ssh.Command{Line: "show version", Prompts: []ssh.Prompt{privPrompt}})
	if err == nil {
		t.Fatal("Run() = nil error, want a connection-lost failure")
	}
	if string(res.Evidence.Sent) != "show version" {
		t.Errorf("Evidence.Sent = %q, want %q even though the peer hung up", res.Evidence.Sent, "show version")
	}
}

func TestRunRedactsSecretFromEvidence(t *testing.T) {
	t.Parallel()
	fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		readCommandLine(ch)
		_, _ = ch.Write([]byte("\r\nswitch#"))
	})
	s := dialSession(t, fs, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const secretLine = "enable secret hunter2"
	const redacted = "enable secret [REDACTED]"
	res, err := s.Run(ctx, ssh.Command{Line: secretLine, Redacted: redacted, Prompts: []ssh.Prompt{privPrompt}})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if string(res.Evidence.Sent) != redacted {
		t.Errorf("Evidence.Sent = %q, want %q", res.Evidence.Sent, redacted)
	}
	if strings.Contains(string(res.Evidence.Sent), "hunter2") {
		t.Errorf("Evidence.Sent = %q, contains the credential", res.Evidence.Sent)
	}
}

func TestRunDiscardsPreCommandBytes(t *testing.T) {
	t.Parallel()
	fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		// A login banner that happens to end in something matching
		// the caller's prompt pattern, written before the client
		// ever sends a command.
		_, _ = ch.Write([]byte("Welcome to switch\r\nswitch#"))
		readCommandLine(ch)
		_, _ = ch.Write([]byte("\r\nswitch#"))
	})
	s := dialSession(t, fs, nil)
	// Give the banner time to reach the client's stdout ring before
	// Run is ever called, the one condition a real shell always
	// satisfies.
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := s.Run(ctx, ssh.Command{Line: "show version", Prompts: []ssh.Prompt{privPrompt}})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if bytes.Contains(res.Output, []byte("Welcome")) {
		t.Errorf("Output = %q, leaked the pre-command banner", res.Output)
	}
	const wantBytes = int64(len("\r\nswitch#"))
	if res.Evidence.BytesReceived != wantBytes {
		t.Errorf("BytesReceived = %d, want %d (the banner must not be counted as this command's output)", res.Evidence.BytesReceived, wantBytes)
	}
}

func TestRunEchoContainingPromptCharacterDoesNotSelfTerminate(t *testing.T) {
	t.Parallel()
	const line = "show run | include #"
	fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		got := readCommandLine(ch)
		_, _ = ch.Write([]byte(got)) // echo, which itself ends in "#"
		_, _ = ch.Write([]byte("real output\r\nswitch#"))
	})
	s := dialSession(t, fs, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := s.Run(ctx, ssh.Command{Line: line, Prompts: []ssh.Prompt{privPrompt}})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if !bytes.Contains(res.Output, []byte("real output")) {
		t.Errorf("Output = %q, want it to contain the real response instead of stopping at the echoed '#'", res.Output)
	}
	if bytes.Contains(res.Output, []byte(line)) {
		t.Errorf("Output = %q, still contains the echoed command", res.Output)
	}
}

func TestRunEvidenceBytesReceivedSetOnDeadlineError(t *testing.T) {
	t.Parallel()
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		readCommandLine(ch)
		_, _ = ch.Write(bytes.Repeat([]byte("z"), 2048)) // no prompt ever arrives
		<-stop
	})
	s := dialSession(t, fs, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := s.Run(ctx, ssh.Command{
		Line:     "show tech",
		Prompts:  []ssh.Prompt{privPrompt},
		Deadline: 300 * time.Millisecond,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want context.DeadlineExceeded", err)
	}
	if res.Evidence.BytesReceived < 2048 {
		t.Errorf("Evidence.BytesReceived = %d, want >= 2048 even though Run failed", res.Evidence.BytesReceived)
	}
}

func TestSessionCloseDuringRunUnblocksTheWait(t *testing.T) {
	t.Parallel()
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		readCommandLine(ch)
		<-stop // never respond
	})
	opts := optsFor(fs)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := ssh.Dial(ctx, fs.addr, opts)
	if err != nil {
		t.Fatalf("Dial() = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, runErr := s.Run(ctx, ssh.Command{Line: "show version", Prompts: []ssh.Prompt{privPrompt}})
		done <- runErr
	}()
	time.Sleep(100 * time.Millisecond)
	if err := s.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	select {
	case runErr := <-done:
		if runErr == nil {
			t.Fatal("Run() = nil error, want a failure once the session was closed underneath it")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run() did not return after a concurrent Close")
	}
}

// TestRunRefusesALineCarryingATerminator covers the backstop against a caller
// that interpolated a value it did not choose into a command.
//
// CR as well as LF: the shell runs under a PTY, where carriage return is the
// enter key, so a caller that filtered only "\n" would still send everything
// after a "\r" as a second command. Nothing reaches the device.
func TestRunRefusesALineCarryingATerminator(t *testing.T) {
	for _, line := range []string{
		"interface ethernet 1/1/1\nport-name pwned",
		"interface ethernet 1/1/1\rwrite memory",
		"show interfaces ethernet 1/1/1\n",
	} {
		t.Run(strconv.Quote(line), func(t *testing.T) {
			var seen atomic.Int64
			fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
				buf := make([]byte, 256)
				for {
					n, err := ch.Read(buf)
					if n > 0 {
						seen.Add(int64(n))
					}
					if err != nil {
						return
					}
				}
			})
			session := dialSession(t, fs, nil)

			_, err := session.Run(context.Background(), ssh.Command{
				Line:    line,
				Prompts: []ssh.Prompt{privPrompt},
			})
			if err == nil {
				t.Fatal("Run accepted a Line carrying a line terminator")
			}
			if got := seen.Load(); got != 0 {
				t.Errorf("the device received %d bytes; nothing may be written", got)
			}
		})
	}
}

// TestRunRefusesAPatternThatMatchesNothing covers both empty-match refusals.
// A pattern matching the empty string consumes no output, so the read loop
// matches it again immediately: a MorePattern would write its keystroke at
// CPU speed for the whole deadline, and a prompt would end the command
// before the device answered. Nothing reaches the device either way.
func TestRunRefusesAPatternThatMatchesNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  ssh.Command
	}{
		{
			name: "pagination marker",
			cmd: ssh.Command{
				Prompts:       []ssh.Prompt{privPrompt},
				MorePattern:   regexp.MustCompile(`(--More--)?`),
				MoreKeystroke: []byte(" "),
			},
		},
		{
			name: "prompt",
			cmd: ssh.Command{
				Prompts: []ssh.Prompt{{Name: "anything", Pattern: regexp.MustCompile(`x*`)}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var seen atomic.Int64
			fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
				buf := make([]byte, 256)
				for {
					n, err := ch.Read(buf)
					if n > 0 {
						seen.Add(int64(n))
					}
					if err != nil {
						return
					}
				}
			})
			session := dialSession(t, fs, nil)

			cmd := tc.cmd
			cmd.Line = "show running-config"
			if _, err := session.Run(context.Background(), cmd); err == nil {
				t.Fatal("Run accepted a pattern matching the empty string")
			}
			if got := seen.Load(); got != 0 {
				t.Errorf("the device received %d bytes; nothing may be written", got)
			}
		})
	}
}

// TestRunUnderACanceledContextSendsNothingAndSaysSo covers the pre-write
// context check and the evidence it returns. A caller reading Evidence.Sent
// as "what the device received" must not be told a configuration line was
// sent when the write never happened.
func TestRunUnderACanceledContextSendsNothingAndSaysSo(t *testing.T) {
	var seen atomic.Int64
	fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		buf := make([]byte, 256)
		for {
			n, err := ch.Read(buf)
			if n > 0 {
				seen.Add(int64(n))
			}
			if err != nil {
				return
			}
		}
	})
	session := dialSession(t, fs, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := session.Run(ctx, ssh.Command{
		Line:    "interface ethernet 1/1/1",
		Prompts: []ssh.Prompt{privPrompt},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() = %v, want the unwrapped context.Canceled", err)
	}
	if got := seen.Load(); got != 0 {
		t.Errorf("the device received %d bytes under a canceled context", got)
	}
	if len(res.Evidence.Sent) != 0 {
		t.Errorf("Evidence.Sent = %q, want empty: nothing was written", res.Evidence.Sent)
	}
}
