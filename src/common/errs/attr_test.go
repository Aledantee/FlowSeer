package errs

import (
	"errors"
	"fmt"
	"testing"
)

// Covers AE2: the outermost value wins for a repeated key.
func TestAttributesOutermostWins(t *testing.T) {
	inner := New().Attr("have", 4).Msg("short read")
	outer := From(inner).Attr("have", 8).Msg("decode failed")

	attrs := Attributes(outer)

	if got := attrs["have"]; got != 8 {
		t.Errorf("have = %v, want 8 (the outermost value)", got)
	}
	if len(attrs) != 1 {
		t.Errorf("Attributes() = %v, want exactly one key", attrs)
	}
}

// Covers AE5: joined branches are traversed left to right and the first
// value encountered wins.
func TestAttributesTraverseJoinedBranchesLeftToRight(t *testing.T) {
	left := New().Attr("proto", "usm-aes").Msg("left")
	right := New().Attr("proto", "usm-des").Msg("right")
	err := Wrap(errors.Join(left, right), "decrypt failed")

	if got := Attributes(err)["proto"]; got != "usm-aes" {
		t.Errorf("proto = %v, want usm-aes (the leftmost branch)", got)
	}
}

func TestAttributesOfChainWithoutAttributes(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "nil", err: nil},
		{name: "sentinel", err: Msg("plain")},
		{name: "foreign", err: errors.New("foreign")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			attrs := Attributes(tc.err)
			if attrs == nil {
				t.Fatal("Attributes() = nil, want an empty map")
			}
			if len(attrs) != 0 {
				t.Errorf("Attributes() = %v, want empty", attrs)
			}
		})
	}
}

// A foreign error between two errs errors must not stop the traversal.
func TestAttributesTraverseForeignNodes(t *testing.T) {
	inner := New().Attr("index", 1).Msg("row failed")
	middle := fmt.Errorf("stdlib layer: %w", inner)
	outer := From(middle).Attr("table", "ifTable").Msg("walk failed")

	attrs := Attributes(outer)

	if got := attrs["index"]; got != 1 {
		t.Errorf("index = %v, want 1 — traversal stopped at the foreign node", got)
	}
	if got := attrs["table"]; got != "ifTable" {
		t.Errorf("table = %v, want ifTable", got)
	}
}

func TestSafeAttributesReturnOnlyMarkedValues(t *testing.T) {
	err := New().
		Attr("engine_id", "80001f88").
		PubAttr("proto", "usm-aes").
		Msg("authentication failed")

	safe := SafeAttributes(err)
	if got, want := len(safe), 1; got != want {
		t.Fatalf("SafeAttributes() = %v, want %d entry", safe, want)
	}
	if got := safe["proto"]; got != "usm-aes" {
		t.Errorf("safe proto = %v, want usm-aes", got)
	}

	all := Attributes(err)
	if _, ok := all["engine_id"]; !ok {
		t.Error("Attributes() dropped an internal attribute")
	}
	if _, ok := safe["engine_id"]; ok {
		t.Error("SafeAttributes() exposed an internal attribute")
	}
}

// The shapes the snmp suite asserts: a key set on one wrap level extracts
// with the value that level attached.
func TestAttributesOfSnmpShapes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		key  string
		want any
	}{
		{
			name: "got",
			err:  New().Attr("got", 3).Msgf("OID has %d components", 3),
			key:  "got",
			want: 3,
		},
		{
			name: "max",
			err:  New().Attr("max", 128).Msg("OID exceeds the component limit"),
			key:  "max",
			want: 128,
		},
		{
			name: "index",
			err:  New().Attr("index", 1).Msg("row index out of range"),
			key:  "index",
			want: 1,
		},
		{
			name: "type",
			err:  New().Attr("type", "Counter64").Msg("VarBind variant does not match decoder"),
			key:  "type",
			want: "Counter64",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Attributes(tc.err)[tc.key]; got != tc.want {
				t.Errorf("%s = %v, want %v", tc.key, got, tc.want)
			}
		})
	}
}

func TestAttributesTolerateOddValues(t *testing.T) {
	err := New().
		Attr("nil_value", nil).
		Attr("func_value", func() {}).
		Attr("map_value", map[string]int{"a": 1}).
		Msg("odd values")

	attrs := Attributes(err)

	if got, want := len(attrs), 3; got != want {
		t.Errorf("Attributes() has %d keys, want %d", got, want)
	}
	if attrs["nil_value"] != nil {
		t.Errorf("nil_value = %v, want nil", attrs["nil_value"])
	}
}
