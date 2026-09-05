package ssh_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"regexp"
	"strings"
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
	fs := newFakeServer(t, func(t *testing.T, ch xssh.Channel) {
		readCommandLine(ch)
		_, _ = ch.Write([]byte("page one\r\n--More--"))
		key := make([]byte, 1)
		if _, err := io.ReadFull(ch, key); err != nil {
			t.Errorf("read continuation keystroke: %v", err)
			return
		}
		if key[0] != ' ' {
			t.Errorf("continuation keystroke = %q, want %q", key, " ")
		}
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
