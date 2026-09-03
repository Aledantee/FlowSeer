package syslog

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

func TestFraming(t *testing.T) {
	for _, tc := range []struct {
		wire string
		mode Framing
		want []string
	}{
		{"3 abc3 def", OctetCounting, []string{"abc", "def"}},
		{"<1>a\n4 <2>b", Auto, []string{"<1>a", "<2>b"}},
		{"123\r\nxyz\r\n", CRLF, []string{"123", "xyz"}},
		{"a\r\nb\n", LF, []string{"a\r", "b"}},
		{"a\x00b\x00", NUL, []string{"a", "b"}},
	} {
		reader := streamReader{reader: bytes.NewBufferString(tc.wire), buffer: make([]byte, 4096)}
		var first time.Time
		for _, want := range tc.want {
			got, at, err := readFrame(&reader, tc.mode, make([]byte, 64))
			if err != nil || string(got) != want {
				t.Fatalf("%q: %q %v", tc.wire, got, err)
			}
			if first.IsZero() {
				first = at
			} else if !at.Equal(first) {
				t.Fatal("coalesced read time changed")
			}
		}
	}
	for _, wire := range []string{"0 ", "99999999999 ", "123x\n", "unknown\n", "65 ", "<1>unterminated"} {
		reader := streamReader{reader: bytes.NewBufferString(wire), buffer: make([]byte, 4096)}
		if _, _, err := readFrame(&reader, Auto, make([]byte, 64)); err == nil {
			t.Fatalf("accepted %q", wire)
		}
	}
	reader := streamReader{reader: bytes.NewReader(nil), buffer: make([]byte, 8)}
	if _, _, err := readFrame(&reader, LF, make([]byte, 64)); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

func FuzzFrame(f *testing.F) {
	f.Add([]byte("4 <1>x3 abc"), uint8(0))
	f.Add([]byte("a\r\nb\r\n"), uint8(3))
	f.Fuzz(func(t *testing.T, input []byte, mode uint8) {
		reader := streamReader{reader: bytes.NewReader(input), buffer: make([]byte, 4096)}
		modes := []Framing{Auto, OctetCounting, LF, CRLF, NUL}
		for range len(input) + 1 {
			b, _, err := readFrame(&reader, modes[int(mode)%len(modes)], make([]byte, 256))
			if err != nil {
				return
			}
			if len(b) > 256 {
				t.Fatal("frame limit")
			}
		}
		t.Fatal("framer did not progress")
	})
}

func TestIncompleteFrameIsNotCleanEOF(t *testing.T) {
	reader := streamReader{reader: bytes.NewBufferString("5 abc"), buffer: make([]byte, 8)}
	if _, _, err := readFrame(&reader, OctetCounting, make([]byte, 8)); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
}
