package secret

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Redacted is the placeholder every rendering of a set [Value] produces.
// The literal is distinct from any plausible credential, so a reader who
// finds it in a log knows redaction happened rather than that someone
// configured a strange password.
const Redacted = "[REDACTED]"

// ErrRedactedInput is returned by [Value.UnmarshalJSON] and
// [Value.UnmarshalText] for input equal to [Redacted]: the document
// being decoded is a redacted rendering, not a credential.
var ErrRedactedInput = errs.Msgf("input is the %s placeholder, not secret material", Redacted)

// Value holds credential material. Its rendering is redacted on every
// path described in the package documentation; [Value.Reveal] and
// [Value.RevealString] are the only ways to read the material back.
//
// The zero Value is unset and usable. A Value is safe for concurrent
// reads; [Value.Zero] is a write and must not race one.
type Value struct {
	b []byte
}

// New returns a Value over b and takes ownership of it: the caller must
// not retain or write to the slice afterwards, since [Value.Zero] wipes
// it in place. Use [NewString] to copy from material the caller keeps.
func New(b []byte) Value { return Value{b: b} }

// NewString returns a Value holding a copy of s. The copy is
// unavoidable — a Go string cannot be wiped — so the original remains in
// memory until it is collected.
func NewString(s string) Value { return Value{b: []byte(s)} }

// Reveal returns the material. The returned slice is the Value's own and
// must be treated as read-only; see the package documentation.
func (v Value) Reveal() []byte { return v.b }

// RevealString returns the material as a string. The result is a copy
// that [Value.Zero] cannot reach.
func (v Value) RevealString() string { return string(v.b) }

// Len returns the length of the material in bytes. It is safe to log: a
// length is what an error should carry in place of a credential.
func (v Value) Len() int { return len(v.b) }

// Empty reports whether the Value holds no material.
func (v Value) Empty() bool { return len(v.b) == 0 }

// Equal reports whether two Values hold the same material, in time
// independent of where equal-length material first differs.
func (v Value) Equal(other Value) bool {
	return subtle.ConstantTimeCompare(v.b, other.b) == 1
}

// EqualString reports whether the Value holds exactly s, in time
// independent of where equal-length material first differs. It is the
// comparison for material that arrives as an untrusted string, such as
// a community string decoded from a datagram.
func (v Value) EqualString(s string) bool {
	return subtle.ConstantTimeCompare(v.b, []byte(s)) == 1
}

// Zero overwrites the material with zero bytes in place. Every copy of
// the Value is wiped with it; material already handed out by
// [Value.Reveal] or [Value.RevealString] is not. The Value keeps its
// length, so a zeroed Value still renders as [Redacted].
func (v Value) Zero() {
	clear(v.b)
}

// String returns [Redacted] for a set Value and the empty string for an
// unset one.
//
// String implements [fmt.Stringer].
func (v Value) String() string {
	if v.Empty() {
		return ""
	}
	return Redacted
}

// GoString returns the [Value.String] rendering in the %#v form.
//
// GoString implements [fmt.GoStringer].
func (v Value) GoString() string {
	return "secret.Value(" + strconv.Quote(v.String()) + ")"
}

// Format renders the Value for every verb, so that no verb reaches the
// reflect walker that would print the material. The package
// documentation records where fmt bypasses this.
//
// Format implements [fmt.Formatter].
func (v Value) Format(f fmt.State, verb rune) {
	switch verb {
	case 'v':
		if f.Flag('#') {
			_, _ = f.Write([]byte(v.GoString()))
			return
		}
		_, _ = f.Write([]byte(v.String()))
	case 's':
		_, _ = f.Write([]byte(v.String()))
	case 'q':
		_, _ = f.Write([]byte(strconv.Quote(v.String())))
	default:
		fmt.Fprintf(f, "%%!%c(secret.Value=%s)", verb, v.String())
	}
}

// MarshalJSON encodes the [Value.String] rendering as a JSON string.
//
// MarshalJSON implements [json.Marshaler].
func (v Value) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(v.String())), nil
}

// UnmarshalJSON decodes a JSON string into the Value, rejecting the
// [Redacted] placeholder with [ErrRedactedInput]. JSON null decodes to
// an unset Value: a key present but null says the credential is not
// configured, which is what an unset Value means.
//
// UnmarshalJSON implements [json.Unmarshaler].
func (v *Value) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return errs.Wrap(err, "decode secret value")
	}
	return v.UnmarshalText([]byte(s))
}

// MarshalText encodes the [Value.String] rendering.
//
// MarshalText implements [encoding.TextMarshaler].
func (v Value) MarshalText() ([]byte, error) {
	return []byte(v.String()), nil
}

// UnmarshalText sets the Value to a copy of text, rejecting the
// [Redacted] placeholder with [ErrRedactedInput].
//
// UnmarshalText implements [encoding.TextUnmarshaler].
func (v *Value) UnmarshalText(text []byte) error {
	if string(text) == Redacted {
		return ErrRedactedInput
	}
	v.b = append([]byte(nil), text...)
	return nil
}

// LogValue returns the [Value.String] rendering, so a Value logged
// directly as an attribute value is redacted by the handler rather than
// by the formatter.
//
// LogValue implements [slog.LogValuer].
func (v Value) LogValue() slog.Value {
	return slog.StringValue(v.String())
}
