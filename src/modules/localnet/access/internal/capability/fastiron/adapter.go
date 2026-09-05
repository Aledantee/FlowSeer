package fastiron

import (
	"context"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/ssh"
)

// ErrCodeAmbiguousSubmission identifies a command the device rejected
// instead of applying: it returned to a prompt one level short of what a
// successful command reaches, so the caller must never treat it as
// applied.
var ErrCodeAmbiguousSubmission = errs.NewCode("fastiron/ambiguous-submission")

// Adapter is the typed FastIron 10.0.10g shell adapter over one
// already-dialed [ssh.Session]. It satisfies the interface capability's
// ShellAdapter seam structurally; nothing in this package imports it, per
// the direction record's decision 11.
type Adapter struct {
	// Session is the shell this adapter drives. Must be set before any
	// method is called.
	Session *ssh.Session
	// EnablePassword is written, redacted, if Login's enable command is
	// answered with the enable-password prompt instead of the privileged
	// one. Empty means no password is expected.
	EnablePassword string
}

// Login moves the session to privileged mode. It sends an empty line to
// discover the session's starting prompt; if already privileged it does
// nothing further, otherwise it runs [EnableCommand] and, if the device
// asks for a password, [EnablePasswordCommand].
func (a *Adapter) Login(ctx context.Context) error {
	res, err := a.Session.Run(ctx, ssh.Command{Prompts: []ssh.Prompt{UnprivilegedPrompt, PrivilegedPrompt}})
	if err != nil {
		return errs.Wrap(err, "detect initial prompt")
	}

	if res.MatchedPrompt == PromptPrivileged {
		return nil
	}

	res, err = a.Session.Run(ctx, EnableCommand())
	if err != nil {
		return errs.Wrap(err, "enable")
	}

	if res.MatchedPrompt == PromptPrivileged {
		return nil
	}

	if _, err := a.Session.Run(ctx, EnablePasswordCommand(a.EnablePassword)); err != nil {
		return errs.Wrap(err, "enable password")
	}

	return nil
}

// ReadInterface runs [ShowInterfaceCommand] and parses its output. A
// successful FastIron parse yields every compared field, so a caller
// treats this result as complete.
func (a *Adapter) ReadInterface(ctx context.Context, name string) (description string, admin interfacev1.AdminStatus, oper interfacev1.OperStatus, err error) {
	res, err := a.Session.Run(ctx, ShowInterfaceCommand(name))
	if err != nil {
		return "", interfacev1.AdminStatus_ADMIN_STATUS_UNSPECIFIED, interfacev1.OperStatus_OPER_STATUS_UNSPECIFIED,
			errs.Wrap(err, "show interfaces")
	}

	return ParseShowInterface(res.Output)
}

// SetPortName sets or clears an interface's description on running
// configuration only: [ConfigureTerminalCommand], [SelectInterfaceCommand],
// [PortNameCommand], [EndCommand], and no other command — running
// configuration is never saved to startup configuration by this method.
//
// SelectInterfaceCommand and PortNameCommand each list the config-if
// prompt a successful command returns to and the config prompt a rejected
// one returns to instead; a match on the config prompt is reported as
// [ErrCodeAmbiguousSubmission], never treated as applied.
func (a *Adapter) SetPortName(ctx context.Context, name, text string) error {
	if _, err := a.Session.Run(ctx, ConfigureTerminalCommand()); err != nil {
		return errs.Wrap(err, "configure terminal")
	}

	res, err := a.Session.Run(ctx, SelectInterfaceCommand(name))
	if err != nil {
		return errs.Wrap(err, "select interface")
	}

	if res.MatchedPrompt != PromptConfigIf {
		return errs.New().Code(ErrCodeAmbiguousSubmission).
			Attr("interface_name", name).
			Msgf("device did not accept interface %q: %s", name, res.Output)
	}

	res, err = a.Session.Run(ctx, PortNameCommand(text))
	if err != nil {
		return errs.Wrap(err, "port-name")
	}

	if res.MatchedPrompt != PromptConfigIf {
		return errs.New().Code(ErrCodeAmbiguousSubmission).
			Attr("interface_name", name).
			Msgf("device rejected the port-name command: %s", res.Output)
	}

	if _, err := a.Session.Run(ctx, EndCommand()); err != nil {
		return errs.Wrap(err, "end")
	}

	return nil
}
