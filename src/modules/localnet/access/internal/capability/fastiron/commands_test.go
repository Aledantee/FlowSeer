package fastiron_test

import (
	"slices"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/fastiron"
	"go.aledante.io/FlowSeer/src/protocol/ssh"
)

func TestShowInterfaceCommand(t *testing.T) {
	cmd := fastiron.ShowInterfaceCommand("ethernet 1/1/1")

	if want := "show interfaces ethernet 1/1/1"; cmd.Line != want {
		t.Errorf("Line = %q, want %q", cmd.Line, want)
	}

	if cmd.MorePattern == nil {
		t.Error("MorePattern is nil, want the pagination marker")
	}

	if len(cmd.Prompts) != 1 || cmd.Prompts[0].Name != fastiron.PromptPrivileged {
		t.Errorf("Prompts = %v, want exactly [privileged]", cmd.Prompts)
	}
}

func TestSelectInterfaceCommand(t *testing.T) {
	cmd := fastiron.SelectInterfaceCommand("ethernet 1/1/1")

	if want := "interface ethernet 1/1/1"; cmd.Line != want {
		t.Errorf("Line = %q, want %q", cmd.Line, want)
	}

	wantNames := []string{fastiron.PromptConfigIf, fastiron.PromptConfig}
	if got := promptNames(cmd.Prompts); !slices.Equal(got, wantNames) {
		t.Errorf("Prompts = %v, want %v", got, wantNames)
	}
}

func TestPortNameCommand(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		{text: "uplink to core", want: "port-name uplink to core"},
		{text: "", want: "no port-name"},
	}

	for _, tt := range tests {
		cmd := fastiron.PortNameCommand(tt.text)
		if cmd.Line != tt.want {
			t.Errorf("PortNameCommand(%q).Line = %q, want %q", tt.text, cmd.Line, tt.want)
		}
	}
}

func TestEndCommand_NeverWritesStartupConfig(t *testing.T) {
	cmd := fastiron.EndCommand()
	if want := "end"; cmd.Line != want {
		t.Errorf("Line = %q, want %q", cmd.Line, want)
	}
}

// TestRunningConfigOnly scans every command builder's output for the two
// ways FastIron persists configuration to startup config, proving no
// command in this package can ever send one.
func TestRunningConfigOnly(t *testing.T) {
	lines := []string{
		fastiron.ShowInterfaceCommand("ethernet 1/1/1").Line,
		fastiron.EnableCommand().Line,
		fastiron.EnablePasswordCommand("secret").Line,
		fastiron.ConfigureTerminalCommand().Line,
		fastiron.SelectInterfaceCommand("ethernet 1/1/1").Line,
		fastiron.PortNameCommand("uplink to core").Line,
		fastiron.PortNameCommand("").Line,
		fastiron.EndCommand().Line,
	}

	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "write mem") || strings.Contains(lower, "copy running-config") {
			t.Errorf("command %q would persist to startup configuration", line)
		}
	}
}

func promptNames(prompts []ssh.Prompt) []string {
	names := make([]string, len(prompts))
	for i, p := range prompts {
		names[i] = p.Name
	}

	return names
}
