package fastiron

import (
	"context"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
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
	// Everything before the write is one step, and the split is what carries
	// the meaning rather than a label on chosen error sites: PortNameCommand
	// is not constructed until this returns, so nothing that fails inside it
	// can have delivered a description change. A refusal added there inherits
	// that; one added below inherits the opposite. Position answers correctly
	// in both directions, where a list of marked cases answers correctly only
	// for the ones somebody remembered.
	if err := a.selectForWrite(ctx, name); err != nil {
		return err
	}

	res, err := a.Session.Run(ctx, PortNameCommand(text))
	if err != nil {
		return errs.Wrap(err, "port-name")
	}

	if res.MatchedPrompt != PromptConfigIf {
		return errs.New().Code(ErrCodeAmbiguousSubmission).
			Attr("interface_name", name).
			Msgf("device rejected the port-name command: %s", truncateForLog(res.Output))
	}

	if _, err := a.Session.Run(ctx, EndCommand()); err != nil {
		return errs.Wrap(err, "end")
	}

	return nil
}

// selectForWrite enters configuration mode and selects name, and every
// failure it returns carries [interfaces.ErrCodeNotSubmitted].
//
// That is sound whatever went wrong, including a transport error, and the
// reason is the caller's structure rather than anything about the session: a
// Run failure here may well have delivered its own line — a wait failure
// means the line was written and the prompt was not seen — but the command
// that changes a description is not built until this has succeeded, so no
// description change can have reached the device on any path out of here.
//
// A caller that returns from here leaves the session in configuration mode,
// without an "end". Nothing leaks today because a session is opened per
// operation and closed with it, deliberately — a standing session outlives
// the credential it was opened with, which is the whole reason the lane does
// not keep one. Anyone who later pools these sessions has to fix this, and
// they will be reading this function when they do.
func (a *Adapter) selectForWrite(ctx context.Context, name string) error {
	if _, err := a.Session.Run(ctx, ConfigureTerminalCommand()); err != nil {
		return errs.From(err).Code(interfaces.ErrCodeNotSubmitted).Msg("configure terminal")
	}

	res, err := a.Session.Run(ctx, SelectInterfaceCommand(name))
	if err != nil {
		return errs.From(err).Code(interfaces.ErrCodeNotSubmitted).Msg("select interface")
	}

	if res.MatchedPrompt != PromptConfigIf {
		// Not ErrCodeAmbiguousSubmission, which this used to be and which was
		// the defect: a select the device refused is a certainty, not an
		// ambiguity. The port-name refusal below keeps that name because it
		// earns it — that command was sent.
		return errs.New().Code(interfaces.ErrCodeNotSubmitted).
			Attr("interface_name", name).
			Msgf("device did not accept interface %q: %s", name, truncateForLog(res.Output))
	}

	return nil
}

// maxLoggedOutputBytes bounds how much device output an error message
// carries: Result.Output can hold up to the session's MaxOutput (1 MiB by
// default), and a rejected command's error is not the place for that much
// of it.
const maxLoggedOutputBytes = 256

// truncateForLog bounds output for inclusion in an error message.
func truncateForLog(output []byte) []byte {
	if len(output) <= maxLoggedOutputBytes {
		return output
	}

	return output[:maxLoggedOutputBytes]
}
