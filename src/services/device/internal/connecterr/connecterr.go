// Package connecterr renders an [errs] failure as the Connect error a caller
// is allowed to see.
//
// Two things happen at that boundary. The failure gets the Connect code its
// caller branches on, from the table the serving package declares; a code the
// table does not name answers Internal, so a failure nobody has classified is
// never reported as something the caller can fix. And the text is sanitized.
// [connect.NewError] takes the error's Error(), which for an errs error is its
// own message followed by its whole cause chain — the trusted-internal content
// the error-wire record keeps off a client boundary, naming hosts, transports,
// and, on the credential path, a line of a credential file. What crosses
// instead is what [errs.EncodeForClient] allows: the code, the client-safe
// attributes, the retry disposition, and a user message. One of the services
// this package serves answers unauthenticated callers, so the two have to be
// told apart at every handler and not only on the ones that look sensitive.
package connecterr

import (
	connect "connectrpc.com/connect"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Mapping is what one [errs.Code] tells a caller: the Connect code the call
// answers with, and the client-safe message that stands in for the internal
// text. An empty UserMsg leaves the generic string [errs.EncodeForClient]
// falls back to, which is the honest answer for a failure the caller can do
// nothing about. A message set on the error itself with [errs.Builder.UserMsg]
// wins over the table's, because the site closest to the caller knows best
// what the caller was trying to do.
type Mapping struct {
	Code    connect.Code
	UserMsg string
}

// Table maps the errs codes one serving package can return onto their
// client-facing mappings, including codes it passes through from a dependency.
// A Table is read-only once declared and safe for concurrent use.
type Table map[errs.Code]Mapping

// Wrap renders err as the Connect error its caller sees, per t; a code t does
// not name is Internal with the generic message. Wrap returns nil for a nil
// err. The returned error unwraps to err, so [errors.Is] still matches it and
// a server-side log still has the whole chain; what is sanitized is the text
// that crosses the wire.
func (t Table) Wrap(err error) error {
	if err == nil {
		return nil
	}

	code, _ := errs.CodeOf(err)
	mapping, ok := t[code]
	if !ok {
		mapping = Mapping{Code: connect.CodeInternal}
	}

	return clientError(mapping.Code, mapping.UserMsg, err)
}

// WrapAs renders err as a Connect error with the code and message the call
// site chose, and nil for a nil err. It is for a boundary whose answer does
// not depend on what failed behind it: an assertion that did not verify is Unauthenticated whether the
// signature was wrong, the edge unknown, or the key lookup unable to answer,
// and telling the caller which would be the leak the check exists to prevent.
func WrapAs(code connect.Code, userMsg string, err error) error {
	if err == nil {
		return nil
	}

	return clientError(code, userMsg, err)
}

func clientError(code connect.Code, userMsg string, err error) error {
	payload := errs.EncodeForClient(err)
	if userMsg != "" && errs.UserMessage(err) == "" {
		payload.SetUserMessage(userMsg)
	}

	wrapped := connect.NewError(code, clientFacing{msg: payload.GetUserMessage(), internal: err})

	// The payload is a message this call just built from an in-memory error,
	// so marshaling it cannot fail; if it somehow did, the code and the user
	// message have already crossed, and those are what a caller branches on.
	if detail, detailErr := connect.NewErrorDetail(payload); detailErr == nil {
		wrapped.AddDetail(detail)
	}

	return wrapped
}

// clientFacing renders the message a caller may see while still unwrapping to
// the failure behind it. Connect puts the wrapped error's Error() on the wire,
// and an errs error renders its whole cause chain there, so the two have to be
// separated: the text is the sanitized one, the chain stays reachable for a
// server-side log and for [errors.Is].
type clientFacing struct {
	msg      string
	internal error
}

func (e clientFacing) Error() string { return e.msg }

func (e clientFacing) Unwrap() error { return e.internal }
