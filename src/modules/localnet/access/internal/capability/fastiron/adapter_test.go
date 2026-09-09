package fastiron_test

import (
	"strings"
	"sync"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/secret"

	xssh "golang.org/x/crypto/ssh"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/fastiron"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
	"go.aledante.io/FlowSeer/src/protocol/ssh"
)

// var _ interfaces.ShellAdapter = (*fastiron.Adapter)(nil) proves Adapter
// satisfies the capability handler's seam at compile time, so a signature
// drift between the two packages is caught here rather than where a
// future host wires them together.
var _ interfaces.ShellAdapter = (*fastiron.Adapter)(nil)

// transcriptRecorder captures every line the adapter sends over one
// session, so a test can assert on the whole running-only sequence rather
// than one command at a time.
type transcriptRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (r *transcriptRecorder) record(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.lines = append(r.lines, line)
}

func (r *transcriptRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), r.lines...)
}

func TestLogin_AlreadyPrivileged(t *testing.T) {
	fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		readLine(ch) // the empty probe line
		_, _ = ch.Write([]byte("\r\nSSH@device#"))
	})

	a := &fastiron.Adapter{Session: dialSession(t, fs)}
	if err := a.Login(t.Context()); err != nil {
		t.Fatalf("Login: %v", err)
	}
}

func TestLogin_EnableWithPassword(t *testing.T) {
	fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		readLine(ch) // the empty probe line
		_, _ = ch.Write([]byte("\r\nSSH@device>"))

		readLine(ch) // "enable"
		_, _ = ch.Write([]byte("\r\nPassword:"))

		got := readLine(ch)
		if !strings.HasPrefix(got, "enablesecret") {
			_, _ = ch.Write([]byte("\r\n% wrong password"))

			return
		}

		_, _ = ch.Write([]byte("\r\nSSH@device#"))
	})

	a := &fastiron.Adapter{Session: dialSession(t, fs), EnablePassword: secret.NewString("enablesecret")}
	if err := a.Login(t.Context()); err != nil {
		t.Fatalf("Login: %v", err)
	}
}

func TestReadInterface_HappyPathWithPagination(t *testing.T) {
	fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		readLine(ch) // "show interfaces ethernet 1/1/1"
		_, _ = ch.Write([]byte("\r\nGigabitEthernet1/1/1 is up, line protocol is up\r\n"))
		_, _ = ch.Write([]byte("--More--, next page: Space, next line: Return key, quit: Control-c"))

		key := make([]byte, 1)
		if _, err := ch.Read(key); err != nil || string(key) != " " {
			return
		}

		_, _ = ch.Write([]byte("\r\n  Port name is uplink to core\r\nSSH@device#"))
	})

	a := &fastiron.Adapter{Session: dialSession(t, fs)}

	description, admin, oper, err := a.ReadInterface(t.Context(), "ethernet 1/1/1")
	if err != nil {
		t.Fatalf("ReadInterface: %v", err)
	}

	if description != "uplink to core" {
		t.Errorf("description = %q, want %q", description, "uplink to core")
	}

	if admin != interfacev1.AdminStatus_ADMIN_STATUS_UP {
		t.Errorf("admin = %v, want ADMIN_STATUS_UP", admin)
	}

	if oper != interfacev1.OperStatus_OPER_STATUS_UP {
		t.Errorf("oper = %v, want OPER_STATUS_UP", oper)
	}
}

// TestReadInterface_DescriptionContainingPromptLookalikeSurvives proves
// Requirement 8's "a description line containing SSH@device# as literal
// text does not end the command early" at the layer where that failure can
// actually occur: Session.Run's own prompt scan
// (src/protocol/ssh/command.go), not ParseShowInterface, which only ever
// sees output the scan has already terminated. The look-alike text sits on
// an indented line, exactly as FastIron always indents "show interfaces"
// body lines, which is what privilegedPattern's non-whitespace-start
// requirement (commands.go) relies on to reject it as a match.
func TestReadInterface_DescriptionContainingPromptLookalikeSurvives(t *testing.T) {
	fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		readLine(ch) // "show interfaces ethernet 1/1/1"
		_, _ = ch.Write([]byte("\r\nGigabitEthernet1/1/1 is up, line protocol is up\r\n"))
		_, _ = ch.Write([]byte("  Port name is see SSH@device# for details\r\n"))
		_, _ = ch.Write([]byte("SSH@device#"))
	})

	a := &fastiron.Adapter{Session: dialSession(t, fs)}

	description, _, _, err := a.ReadInterface(t.Context(), "ethernet 1/1/1")
	if err != nil {
		t.Fatalf("ReadInterface: %v", err)
	}

	if want := "see SSH@device# for details"; description != want {
		t.Errorf("description = %q, want %q (the command must not have ended at the look-alike line)", description, want)
	}
}

// TestPromptPatterns_DiscriminateSiblingPrompts proves each exported
// Prompt pattern matches only its own prompt line among every session
// state and matches none of FastIron's indented output lines — the two
// hazards
// docs/solutions/architecture-patterns/ssh-prompt-patterns-need-multiline-anchors-and-must-exclude-siblings.md
// names.
func TestPromptPatterns_DiscriminateSiblingPrompts(t *testing.T) {
	lines := map[string]string{
		fastiron.PromptUnprivileged: "SSH@device>",
		fastiron.PromptPrivileged:   "SSH@device#",
		fastiron.PromptConfig:       "SSH@device(config)#",
		fastiron.PromptConfigIf:     "SSH@device(config-if-e1000-1/1/1)#",
	}

	prompts := map[string]ssh.Prompt{
		fastiron.PromptUnprivileged: fastiron.UnprivilegedPrompt,
		fastiron.PromptPrivileged:   fastiron.PrivilegedPrompt,
		fastiron.PromptConfig:       fastiron.ConfigPrompt,
		fastiron.PromptConfigIf:     fastiron.ConfigIfPrompt,
	}

	for promptName, prompt := range prompts {
		for lineName, line := range lines {
			got := prompt.Pattern.MatchString(line)
			want := promptName == lineName

			if got != want {
				t.Errorf("%s pattern matching %q line = %v, want %v", promptName, lineName, got, want)
			}
		}

		for _, indented := range []string{
			"  Port name is see " + lines[fastiron.PromptPrivileged] + " here",
			"  " + lines[fastiron.PromptConfig],
		} {
			if prompt.Pattern.MatchString(indented) {
				t.Errorf("%s pattern matched an indented output line %q", promptName, indented)
			}
		}
	}
}

func TestSetPortName_HappyPathIsRunningConfigOnly(t *testing.T) {
	rec := &transcriptRecorder{}

	fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		rec.record(readLine(ch)) // "configure terminal"
		_, _ = ch.Write([]byte("\r\nSSH@device(config)#"))

		rec.record(readLine(ch)) // "interface ethernet 1/1/1"
		_, _ = ch.Write([]byte("\r\nSSH@device(config-if-e1000-1/1/1)#"))

		rec.record(readLine(ch)) // "port-name uplink to core"
		_, _ = ch.Write([]byte("\r\nSSH@device(config-if-e1000-1/1/1)#"))

		rec.record(readLine(ch)) // "end"
		_, _ = ch.Write([]byte("\r\nSSH@device#"))
	})

	a := &fastiron.Adapter{Session: dialSession(t, fs)}
	if err := a.SetPortName(t.Context(), "ethernet 1/1/1", "uplink to core"); err != nil {
		t.Fatalf("SetPortName: %v", err)
	}

	wantLines := []string{
		"configure terminal\n",
		"interface ethernet 1/1/1\n",
		"port-name uplink to core\n",
		"end\n",
	}

	got := rec.snapshot()
	if len(got) != len(wantLines) {
		t.Fatalf("sent %d lines, want %d: %q", len(got), len(wantLines), got)
	}

	for i, want := range wantLines {
		if got[i] != want {
			t.Errorf("line %d = %q, want %q", i, got[i], want)
		}

		lower := strings.ToLower(got[i])
		if strings.Contains(lower, "write mem") || strings.Contains(lower, "copy running-config") {
			t.Errorf("line %d %q would persist to startup configuration", i, got[i])
		}
	}
}

func TestSetPortName_AmbiguousInterfaceSelection(t *testing.T) {
	// The device rejects the interface name and returns to the config
	// prompt instead of a config-if prompt: an ambiguous submission the
	// adapter must never treat as applied.
	fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		readLine(ch) // "configure terminal"
		_, _ = ch.Write([]byte("\r\nSSH@device(config)#"))

		readLine(ch) // "interface ethernet 9/9/9"
		_, _ = ch.Write([]byte("\r\nInvalid input -> ethernet 9/9/9\r\nSSH@device(config)#"))
	})

	a := &fastiron.Adapter{Session: dialSession(t, fs)}

	err := a.SetPortName(t.Context(), "ethernet 9/9/9", "uplink to core")
	if err == nil {
		t.Fatal("SetPortName did not error on a rejected interface selection")
	}
}

func TestSetPortName_ClearsDescription(t *testing.T) {
	var portNameLine string

	fs := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		readLine(ch) // "configure terminal"
		_, _ = ch.Write([]byte("\r\nSSH@device(config)#"))

		readLine(ch) // "interface ethernet 1/1/1"
		_, _ = ch.Write([]byte("\r\nSSH@device(config-if-e1000-1/1/1)#"))

		portNameLine = readLine(ch) // "no port-name"
		_, _ = ch.Write([]byte("\r\nSSH@device(config-if-e1000-1/1/1)#"))

		readLine(ch) // "end"
		_, _ = ch.Write([]byte("\r\nSSH@device#"))
	})

	a := &fastiron.Adapter{Session: dialSession(t, fs)}
	if err := a.SetPortName(t.Context(), "ethernet 1/1/1", ""); err != nil {
		t.Fatalf("SetPortName: %v", err)
	}

	if want := "no port-name\n"; portNameLine != want {
		t.Errorf("port-name line = %q, want %q", portNameLine, want)
	}
}

// The two refusals this adapter can return are different facts and must not
// carry the same code.
//
// A refused interface select is a certainty: the port-name command is not
// constructed until the select has succeeded, so nothing that changes a
// description reached the device, whatever happened to the transport. A
// refused port-name is an ambiguity: that command *was* sent and the device
// did not come back to the prompt it should have, so whether it took effect
// is unknown.
//
// Both were ErrCodeAmbiguousSubmission once, and the caller latches every
// submission as possibly-delivered unless told otherwise — so a wrong
// interface name, the likeliest failure of a first live write, was recorded
// as an effect nobody could establish on a device that was provably never
// changed. The operator was then sent to resolve an ambiguity that did not
// exist.
//
// Asserted as a pair in one test, because the property is the difference
// between them: either alone passes for an adapter that marks both the same
// way.
func TestSetPortName_DistinguishesARefusalFromAnAmbiguity(t *testing.T) {
	selectRefused := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		readLine(ch) // "configure terminal"
		_, _ = ch.Write([]byte("\r\nSSH@device(config)#"))

		readLine(ch) // "interface ethernet 9/9/9"
		_, _ = ch.Write([]byte("\r\nInvalid input -> ethernet 9/9/9\r\nSSH@device(config)#"))
	})

	err := (&fastiron.Adapter{Session: dialSession(t, selectRefused)}).
		SetPortName(t.Context(), "ethernet 9/9/9", "uplink to core")
	if code, ok := errs.CodeOf(err); !ok || code != interfaces.ErrCodeNotSubmitted {
		t.Errorf("a refused interface select returned code %v (err %v), want %v — nothing was sent and the caller has to be told",
			code, err, interfaces.ErrCodeNotSubmitted)
	}

	portNameRefused := newFakeServer(t, func(_ *testing.T, ch xssh.Channel) {
		readLine(ch) // "configure terminal"
		_, _ = ch.Write([]byte("\r\nSSH@device(config)#"))

		readLine(ch) // "interface ethernet 1/1/1"
		_, _ = ch.Write([]byte("\r\nSSH@device(config-if-e1000-1/1/1)#"))

		readLine(ch) // "port-name uplink to core"
		_, _ = ch.Write([]byte("\r\nInvalid input\r\nSSH@device(config)#"))
	})

	err = (&fastiron.Adapter{Session: dialSession(t, portNameRefused)}).
		SetPortName(t.Context(), "ethernet 1/1/1", "uplink to core")
	if code, ok := errs.CodeOf(err); !ok || code != fastiron.ErrCodeAmbiguousSubmission {
		t.Errorf("a refused port-name returned code %v (err %v), want %v — that command reached the device",
			code, err, fastiron.ErrCodeAmbiguousSubmission)
	}
}
