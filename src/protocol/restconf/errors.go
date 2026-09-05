package restconf

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Error codes are the restconf package's wire contract: append-only,
// never renamed, never reused.
var (
	// ErrCodeDiscovery marks a peer whose RESTCONF API root could not
	// be located.
	ErrCodeDiscovery = errs.NewCode("restconf/discovery")
	// ErrCodeDevice marks a RESTCONF error response. The error
	// carries status, error-tag, app-tag, path, and message as
	// attributes; a malformed error body rides along raw.
	ErrCodeDevice = errs.NewCode("restconf/device")
	// ErrCodeConflict marks 409/412 responses — a concurrent writer
	// won the race. Retryable after re-reading.
	ErrCodeConflict = errs.NewCode("restconf/conflict")
	// ErrCodeTransport marks HTTP transport failures.
	ErrCodeTransport = errs.NewCode("restconf/transport")
)

// errorsEnvelope is the RFC 8040 §7.1 "ietf-restconf:errors" body.
type errorsEnvelope struct {
	Errors struct {
		Error []restconfError `json:"error"`
	} `json:"ietf-restconf:errors"`
}

// restconfError is one entry of the errors list.
type restconfError struct {
	Type    string `json:"error-type"`
	Tag     string `json:"error-tag"`
	AppTag  string `json:"error-app-tag"`
	Path    string `json:"error-path"`
	Message string `json:"error-message"`
}

// deviceError shapes an error response into the errs contract. A
// conformant body yields typed fields; a malformed one is preserved
// raw (truncated) for the conformance corpus.
func deviceError(op string, status int, body []byte) error {
	code := ErrCodeDevice
	retryable := false
	if status == 409 || status == 412 {
		code = ErrCodeConflict
		retryable = true
	}

	var envelope errorsEnvelope
	if err := json.Unmarshal(body, &envelope); err == nil && len(envelope.Errors.Error) > 0 {
		first := envelope.Errors.Error[0]
		b := errs.New().
			Code(code).
			Attr("status", status).
			Attr("error_type", first.Type).
			Attr("error_tag", first.Tag).
			Attr("error_app_tag", first.AppTag).
			Attr("error_path", first.Path)
		if retryable {
			b = b.Retryable()
		}
		msg := first.Message
		if msg == "" {
			msg = first.Tag
		}
		return b.Msgf("%s rejected by device: %s", op, msg)
	}

	b := errs.New().
		Code(code).
		Attr("status", status).
		Attr("raw_body", truncate(string(body), 512))
	if retryable {
		b = b.Retryable()
	}
	return b.Msgf("%s failed with HTTP %d and a nonconformant error body", op, status)
}

// truncate bounds a device-supplied payload to n bytes and makes it
// safe for a text log sink: carriage returns, line feeds, and every
// other control character are replaced by a space, so a device cannot
// inject record delimiters into a log line. Truncation can split a
// multi-byte rune, so invalid UTF-8 is replaced by a space as well.
func truncate(s string, n int) string {
	if len(s) > n {
		s = s[:n] + "…"
	}
	return strings.Map(func(r rune) rune {
		if r == utf8.RuneError || unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

// errorType classifies an error for the bounded error.type span
// attribute: the errs code when the error carries one, otherwise the
// concrete Go type. It never exposes error text.
func errorType(err error) string {
	if code, ok := errs.CodeOf(err); ok {
		return code.String()
	}
	return fmt.Sprintf("%T", err)
}
