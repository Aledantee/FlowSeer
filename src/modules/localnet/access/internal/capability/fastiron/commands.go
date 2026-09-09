package fastiron

import (
	"regexp"

	"go.aledante.io/FlowSeer/src/common/secret"
	"go.aledante.io/FlowSeer/src/protocol/ssh"
)

// Prompt names identify which prompt matched a Run, so Adapter can tell a
// privilege or mode transition happened without src/protocol/ssh knowing
// what one is.
const (
	PromptUnprivileged   = "unprivileged"
	PromptPrivileged     = "privileged"
	PromptEnablePassword = "enable-password"
	PromptConfig         = "config"
	PromptConfigIf       = "config-if"
)

// Prompt patterns match a FastIron CLI prompt's tail: "SSH@<hostname>>"
// unprivileged, "SSH@<hostname>#" privileged, "SSH@<hostname>(config)#"
// configuration, and "SSH@<hostname>(config-if-e1000-1/1/1)#" per-interface
// configuration — the shapes this capability is built against, matching
// the config-if example on the port-name reference page
// (https://docs.ruckuswireless.com/fastiron/08.0.60/fastiron-08060-commandref/GUID-E28909AD-2C2C-43FD-8AD8-C4A9FD289BA0.html).
//
// Each pattern requires (?m): a command's own output precedes the prompt
// in the buffer scanPrompt scans, so an unanchored-to-buffer-start `^`
// would never see the prompt's own line. Each also requires the line to
// start with a non-space character and carry no parenthesis before its
// terminal punctuation, so an indented output line — every line FastIron's
// "show interfaces" prints beside its unindented header is indented two
// spaces or more — can never be mistaken for a prompt, and a config-mode
// prompt (which also ends in "#") can never be mistaken for a privileged
// one.
var (
	unprivilegedPattern   = regexp.MustCompile(`(?m)^\S[^()\r\n]*>\s*$`)
	privilegedPattern     = regexp.MustCompile(`(?m)^\S[^()\r\n]*#\s*$`)
	configPattern         = regexp.MustCompile(`(?m)^\S[^()\r\n]*\(config\)#\s*$`)
	configIfPattern       = regexp.MustCompile(`(?m)^\S[^()\r\n]*\(config-if-[^)]*\)#\s*$`)
	enablePasswordPattern = regexp.MustCompile(`(?im)^password:\s*$`)

	// invalidInputPattern matches FastIron's syntax-rejection wording, used
	// by parser.go to tell a rejected command from interface data.
	invalidInputPattern = regexp.MustCompile(`(?m)^(Invalid input|Incomplete command\.)`)
)

// The five prompts a FastIron session moves between.
var (
	UnprivilegedPrompt   = ssh.Prompt{Name: PromptUnprivileged, Pattern: unprivilegedPattern}
	PrivilegedPrompt     = ssh.Prompt{Name: PromptPrivileged, Pattern: privilegedPattern}
	EnablePasswordPrompt = ssh.Prompt{Name: PromptEnablePassword, Pattern: enablePasswordPattern}
	ConfigPrompt         = ssh.Prompt{Name: PromptConfig, Pattern: configPattern}
	ConfigIfPrompt       = ssh.Prompt{Name: PromptConfigIf, Pattern: configIfPattern}
)

// morePattern matches FastIron's pagination marker in full, so scanPrompt
// strips the whole line and none of it reaches Result.Output. Every
// fixture in this package is authored, not captured, so the pattern
// matches the marker's documented text exactly rather than tolerating a
// partial match
// (https://docs.ruckuswireless.com/fastiron/08.0.60/fastiron-08060-commandref/GUID-69E560B4-597A-49FB-AA0B-F19E859F5D31.html).
var morePattern = regexp.MustCompile(regexp.QuoteMeta("--More--, next page: Space, next line: Return key, quit: Control-c"))

// moreKeystroke is the key FastIron's pagination prompt asks for to
// display the next page.
var moreKeystroke = []byte(" ")

// ShowInterfaceCommand builds "show interfaces <name>". name already
// spells the CLI's own media keyword ("ethernet 1/1/1"), matching the
// vendor syntax "show interfaces ethernet stackid/slot/port"
// (https://docs.ruckuswireless.com/fastiron/08.0.60/fastiron-08060-commandref/GUID-54E45EEA-5E28-49B9-B4C7-DCA7811947F6.html),
// so the command does not repeat the keyword.
func ShowInterfaceCommand(name string) ssh.Command {
	return ssh.Command{
		Line:          "show interfaces " + name,
		Prompts:       []ssh.Prompt{PrivilegedPrompt},
		MorePattern:   morePattern,
		MoreKeystroke: moreKeystroke,
	}
}

// EnableCommand requests privileged mode. The device answers either with
// EnablePasswordPrompt, when an enable password is configured, or directly
// with PrivilegedPrompt.
func EnableCommand() ssh.Command {
	return ssh.Command{
		Line:    "enable",
		Prompts: []ssh.Prompt{EnablePasswordPrompt, PrivilegedPrompt},
	}
}

// EnablePasswordCommand answers the enable-password prompt. The password
// is revealed here because this is where it enters the transport, and it
// is redacted in the returned Evidence.
func EnablePasswordCommand(password secret.Value) ssh.Command {
	return ssh.Command{
		Line:     password.RevealString(),
		Redacted: "[REDACTED]",
		Prompts:  []ssh.Prompt{EnablePasswordPrompt, PrivilegedPrompt},
	}
}

// ConfigureTerminalCommand enters configuration mode.
func ConfigureTerminalCommand() ssh.Command {
	return ssh.Command{
		Line:    "configure terminal",
		Prompts: []ssh.Prompt{ConfigPrompt},
	}
}

// SelectInterfaceCommand builds "interface <name>". Prompts list both the
// config-if prompt a valid name returns and the config prompt an invalid
// or ambiguous name rejects to, so Adapter can tell a rejection from a Run
// timeout by inspecting Result.MatchedPrompt.
func SelectInterfaceCommand(name string) ssh.Command {
	return ssh.Command{
		Line:    "interface " + name,
		Prompts: []ssh.Prompt{ConfigIfPrompt, ConfigPrompt},
	}
}

// PortNameCommand sets or clears an interface's description on running
// configuration only: "port-name <text>", or "no port-name" when text is
// empty, since the syntax has no way to set an empty name
// (https://docs.ruckuswireless.com/fastiron/08.0.60/fastiron-08060-commandref/GUID-E28909AD-2C2C-43FD-8AD8-C4A9FD289BA0.html).
// Prompts list both the config-if prompt the command normally returns to
// and the config prompt a rejected command returns to instead.
func PortNameCommand(text string) ssh.Command {
	line := "no port-name"
	if text != "" {
		line = "port-name " + text
	}

	return ssh.Command{
		Line:    line,
		Prompts: []ssh.Prompt{ConfigIfPrompt, ConfigPrompt},
	}
}

// EndCommand leaves configuration mode for privileged mode. This package
// defines no command that writes startup configuration — "write memory" or
// "copy running-config startup-config" — so a mutation this adapter makes
// affects only running configuration until a separate, explicit step
// outside this capability persists it.
func EndCommand() ssh.Command {
	return ssh.Command{
		Line:    "end",
		Prompts: []ssh.Prompt{PrivilegedPrompt},
	}
}
