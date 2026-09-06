package secret_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/secret"
)

const material = "hunter2"

// opts stands in for the options structs the protocol libraries expose:
// a secret next to public fields, printed as a whole.
type opts struct {
	User string
	Pass secret.Value
}

func TestRenderingRedacts(t *testing.T) {
	t.Parallel()

	v := secret.NewString(material)
	o := opts{User: "admin", Pass: v}

	for _, tc := range []struct {
		name string
		got  string
	}{
		{"value %v", fmt.Sprintf("%v", v)},
		{"value %+v", fmt.Sprintf("%+v", v)},
		{"value %#v", fmt.Sprintf("%#v", v)},
		{"value %s", fmt.Sprintf("%s", v)},
		{"value %q", fmt.Sprintf("%q", v)},
		{"value %d", fmt.Sprintf("%d", v)},
		{"value %x", fmt.Sprintf("%x", v)},
		{"struct %v", fmt.Sprintf("%v", o)},
		{"struct %+v", fmt.Sprintf("%+v", o)},
		{"struct %#v", fmt.Sprintf("%#v", o)},
		{"struct %s", fmt.Sprintf("%s", o)},
		{"pointer to struct", fmt.Sprintf("%+v", &o)},
		{"slice of structs", fmt.Sprintf("%+v", []opts{o})},
		{"map value", fmt.Sprintf("%v", map[string]opts{"dev": o})},
		{"any", fmt.Sprintf("%v", any(v))},
		{"wrapped error", fmt.Errorf("dial: %w", fmt.Errorf("opts %+v", o)).Error()},
		{"print", fmt.Sprint(o)},
		{"println", fmt.Sprintln(o)},
	} {
		if strings.Contains(tc.got, material) {
			t.Errorf("%s leaked the material: %s", tc.name, tc.got)
		}
		if !strings.Contains(tc.got, secret.Redacted) {
			t.Errorf("%s is not redacted: %s", tc.name, tc.got)
		}
	}
}

func TestRenderingRedactsInEncoders(t *testing.T) {
	t.Parallel()

	o := opts{User: "admin", Pass: secret.NewString(material)}

	encoded, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	assertRedacted(t, "json", string(encoded))

	text, err := o.Pass.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	assertRedacted(t, "text", string(text))

	for _, tc := range []struct {
		name    string
		handler func(*bytes.Buffer) slog.Handler
	}{
		{"text handler", func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) }},
		{"json handler", func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) }},
	} {
		var buf bytes.Buffer
		slog.New(tc.handler(&buf)).Info("dialing", "opts", o, "pass", o.Pass)
		assertRedacted(t, tc.name, buf.String())
	}
}

func TestRenderingRedactsInErrorAttributes(t *testing.T) {
	t.Parallel()

	v := secret.NewString(material)
	err := errs.New().Attr("pass", v).Attr("pass_len", v.Len()).Msg("authentication failed")

	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Error("dial", "error", err)
	assertRedacted(t, "error attribute", buf.String())

	if !strings.Contains(buf.String(), "pass_len=7") {
		t.Errorf("length attribute is missing: %s", buf.String())
	}
}

func assertRedacted(t *testing.T, name, got string) {
	t.Helper()
	if strings.Contains(got, material) {
		t.Errorf("%s leaked the material: %s", name, got)
	}
	if !strings.Contains(got, secret.Redacted) {
		t.Errorf("%s is not redacted: %s", name, got)
	}
}

func TestUnsetValueRendersEmpty(t *testing.T) {
	t.Parallel()

	var v secret.Value
	if got := fmt.Sprint(v); got != "" {
		t.Errorf("fmt.Sprint = %q, want empty", got)
	}
	if got := fmt.Sprintf("%q", v); got != `""` {
		t.Errorf("%%q = %s, want \"\"", got)
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if string(encoded) != `""` {
		t.Errorf("json.Marshal = %s, want \"\"", encoded)
	}
	if !v.Empty() || v.Len() != 0 {
		t.Errorf("zero Value reports Empty=%t Len=%d", v.Empty(), v.Len())
	}
}

func TestReveal(t *testing.T) {
	t.Parallel()

	v := secret.NewString(material)
	if got := string(v.Reveal()); got != material {
		t.Errorf("Reveal = %q, want %q", got, material)
	}
	if got := v.RevealString(); got != material {
		t.Errorf("RevealString = %q, want %q", got, material)
	}
	if v.Len() != len(material) || v.Empty() {
		t.Errorf("Len = %d, Empty = %t", v.Len(), v.Empty())
	}

	b := []byte(material)
	if got := secret.New(b).RevealString(); got != material {
		t.Errorf("New(...).RevealString = %q, want %q", got, material)
	}
}

func TestNewStringCopies(t *testing.T) {
	t.Parallel()

	b := []byte(material)
	v := secret.NewString(string(b))
	b[0] = 'x'
	if got := v.RevealString(); got != material {
		t.Errorf("NewString kept a reference: %q", got)
	}
}

func TestZeroWipesEveryCopy(t *testing.T) {
	t.Parallel()

	v := secret.NewString(material)
	w := v
	v.Zero()

	for _, b := range w.Reveal() {
		if b != 0 {
			t.Fatalf("copy still holds material: %q", w.Reveal())
		}
	}
	if w.Len() != len(material) {
		t.Errorf("Len = %d, want %d", w.Len(), len(material))
	}
}

func TestComparison(t *testing.T) {
	t.Parallel()

	// A Value holds a slice, so == does not compile on it; Equal and
	// EqualString are the only comparisons.
	var unset secret.Value
	for _, tc := range []struct {
		name string
		got  bool
		want bool
	}{
		{"equal values", secret.NewString("a").Equal(secret.NewString("a")), true},
		{"different values", secret.NewString("a").Equal(secret.NewString("b")), false},
		{"different lengths", secret.NewString("a").Equal(secret.NewString("ab")), false},
		{"unset values", unset.Equal(secret.Value{}), true},
		{"unset against set", unset.Equal(secret.NewString("a")), false},
		{"equal string", secret.NewString("a").EqualString("a"), true},
		{"different string", secret.NewString("a").EqualString("b"), false},
		{"unset against empty string", unset.EqualString(""), true},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: got %t, want %t", tc.name, tc.got, tc.want)
		}
	}
}

func TestUnmarshal(t *testing.T) {
	t.Parallel()

	var v secret.Value
	if err := json.Unmarshal([]byte(`"`+material+`"`), &v); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got := v.RevealString(); got != material {
		t.Errorf("decoded %q, want %q", got, material)
	}

	var redacted secret.Value
	err := json.Unmarshal([]byte(`"`+secret.Redacted+`"`), &redacted)
	if !errors.Is(err, secret.ErrRedactedInput) {
		t.Errorf("decoding the placeholder returned %v, want ErrRedactedInput", err)
	}
	if !redacted.Empty() {
		t.Errorf("rejected input still set the value: %q", redacted.RevealString())
	}

	var text secret.Value
	if err := text.UnmarshalText([]byte(secret.Redacted)); !errors.Is(err, secret.ErrRedactedInput) {
		t.Errorf("UnmarshalText(placeholder) returned %v, want ErrRedactedInput", err)
	}

	var invalid secret.Value
	if err := json.Unmarshal([]byte(`42`), &invalid); err == nil {
		t.Error("decoding a JSON number succeeded, want an error")
	}
}

func TestUnmarshalTextCopies(t *testing.T) {
	t.Parallel()

	text := []byte(material)
	var v secret.Value
	if err := v.UnmarshalText(text); err != nil {
		t.Fatalf("UnmarshalText: %v", err)
	}
	text[0] = 'x'
	if got := v.RevealString(); got != material {
		t.Errorf("UnmarshalText kept a reference: %q", got)
	}
}

// unexportedHolder mirrors the shape packages use when they keep material
// away from their public surface, such as the SNMP session's community.
type unexportedHolder struct{ pass secret.Value }

// TestRedactionLimits pins the two fmt paths the package documentation
// records as bypassing redaction. They are properties of fmt, not of the
// type, and the test exists so a change in either — Go closing the hole,
// or a caller assuming it is already closed — is noticed here.
func TestRedactionLimits(t *testing.T) {
	t.Parallel()

	// fmt consults a value's own methods only when the field can be
	// interfaced, so an unexported field reaches the reflect walker.
	if got := fmt.Sprintf("%+v", unexportedHolder{secret.NewString(material)}); !strings.Contains(got, "104 117") {
		t.Errorf("unexported field no longer renders raw bytes: %s", got)
	}

	// %w on a non-error takes fmt's badVerb path, which reprints the
	// operand through the same walker. go vet rejects the literal form,
	// so reaching it takes an indirect format string.
	verb := "%" + "w"
	if got := fmt.Sprintf(verb, secret.NewString(material)); !strings.Contains(got, "104 117") {
		t.Errorf("%%w on a non-error no longer renders raw bytes: %s", got)
	}
}
