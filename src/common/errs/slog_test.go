package errs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"
)

// logAttributes logs err through a JSON handler and returns the attributes
// group the record carries, which is what an operator actually reads.
func logAttributes(t *testing.T, err error) map[string]any {
	t.Helper()

	record := logRecord(t, err)

	group, ok := record["attributes"]
	if !ok {
		return map[string]any{}
	}

	attrs, ok := group.(map[string]any)
	if !ok {
		t.Fatalf("attributes group = %T, want an object", group)
	}

	return attrs
}

func logRecord(t *testing.T, err error) map[string]any {
	t.Helper()

	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}

			return a
		},
	})

	slog.New(handler).Error("operation failed", slog.Any("err", err))

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("decoding log line %q: %v", buf.String(), err)
	}

	record, ok := line["err"].(map[string]any)
	if !ok {
		t.Fatalf("err field = %v, want an object", line["err"])
	}

	return record
}

// Covers AE2: one grouped record, the outermost value, no duplicate key.
func TestLogValueReportsOutermostAttribute(t *testing.T) {
	inner := New().Attr("have", 4).Msg("short read")
	outer := From(inner).Attr("have", 8).Msg("decode failed")

	attrs := logAttributes(t, outer)

	if got := attrs["have"]; got != float64(8) {
		t.Errorf("logged have = %v, want 8", got)
	}
	if len(attrs) != 1 {
		t.Errorf("logged attributes = %v, want exactly one key", attrs)
	}
	if got, want := logRecord(t, outer)["msg"], "decode failed: short read"; got != want {
		t.Errorf("logged msg = %v, want %q", got, want)
	}
}

// Covers AE5: the log record and the extractor agree on the winning value.
func TestLogValueAndAttributesAgree(t *testing.T) {
	left := New().Attr("proto", "usm-aes").Attr("engine_id", "80001f88").Msg("left")
	right := New().Attr("proto", "usm-des").Msg("right")
	err := From(errors.Join(left, right)).Attr("target", "10.0.0.1:161").Msg("decrypt failed")

	logged := logAttributes(t, err)
	extracted := Attributes(err)

	if len(logged) != len(extracted) {
		t.Fatalf("logged %v and extracted %v disagree on key count", logged, extracted)
	}
	for key, want := range extracted {
		if got := logged[key]; got != want {
			t.Errorf("logged %s = %v, extracted %v", key, got, want)
		}
	}
	if got := extracted["proto"]; got != "usm-aes" {
		t.Errorf("proto = %v, want usm-aes", got)
	}
}

func TestLogValueCarriesCode(t *testing.T) {
	err := Wrap(New().Code(testCodePrivDecrypt).Msg("privacy decryption failed"), "read varbinds")

	if got, want := logRecord(t, err)["code"], testCodePrivDecrypt.String(); got != want {
		t.Errorf("logged code = %v, want %q", got, want)
	}
	if _, ok := logRecord(t, Msg("uncoded"))["code"]; ok {
		t.Error("uncoded error logged a code field")
	}
}

func TestLogValueToleratesOddValues(t *testing.T) {
	var nilErr *Error

	err := New().
		Attr("nil_value", nil).
		Attr("typed_nil", nilErr).
		Attr("map_value", map[string]int{"a": 1}).
		Cause(nil).
		Msg("odd values")

	attrs := logAttributes(t, err)

	if len(attrs) != 3 {
		t.Errorf("logged attributes = %v, want three keys", attrs)
	}
}

func TestLogValueOfSentinelHasNoAttributesGroup(t *testing.T) {
	record := logRecord(t, Msg("plain sentinel"))

	if _, ok := record["attributes"]; ok {
		t.Error("sentinel logged an empty attributes group")
	}
	if got, want := record["msg"], "plain sentinel"; got != want {
		t.Errorf("logged msg = %v, want %q", got, want)
	}
}

// slog recovers panics from LogValue; these call the methods directly so a
// nil receiver is proven safe rather than merely hidden.
func TestNilReceiverIsSafe(t *testing.T) {
	var nilErr *Error

	if got, want := nilErr.Error(), "<nil>"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if got := nilErr.Unwrap(); got != nil {
		t.Errorf("Unwrap() = %v, want nil", got)
	}
	if nilErr.Is(Msg("other")) {
		t.Error("Is() on a nil receiver = true, want false")
	}
	if got := nilErr.LogValue(); got.Any() != nil {
		t.Errorf("LogValue() = %v, want the zero value", got)
	}
	if got := Attributes(nilErr); len(got) != 0 {
		t.Errorf("Attributes() = %v, want empty", got)
	}
	if _, ok := CodeOf(nilErr); ok {
		t.Error("CodeOf() on a nil receiver reported a code")
	}
}

// doc.go claims the log record and the extractor cannot disagree. Shadowed
// keys are where that claim is easiest to break, so assert parity over a
// chain that shadows the same key at three levels and across a join.
func TestLogValueMatchesAttributesOnShadowedKeys(t *testing.T) {
	inner := New().Attr("proto", "inner").Attr("len", 4).Msg("inner")
	joined := errors.Join(inner, New().Attr("proto", "sibling").Attr("index", 2).Msg("sibling"))
	err := From(joined).Attr("proto", "outer").PubAttr("target", "10.0.0.1:161").Msg("outer")

	logged := logAttributes(t, err)
	extracted := Attributes(err)

	if len(logged) != len(extracted) {
		t.Fatalf("logged %v and extracted %v disagree on key count", logged, extracted)
	}
	for key, want := range extracted {
		got, ok := logged[key]
		if !ok {
			t.Errorf("key %s extracted as %v but never logged", key, want)
			continue
		}
		// JSON widens every number to float64; compare through the same lens.
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("logged %s = %v, extracted %v", key, got, want)
		}
	}
	if got := extracted["proto"]; got != "outer" {
		t.Errorf("proto = %v, want outer (the outermost value)", got)
	}
}
