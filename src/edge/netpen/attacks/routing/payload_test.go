package routing

import (
	"encoding/binary"
	"testing"
)

func TestExtractIPPayloadRejectsInvalidEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{name: "wrong protocol", mutate: func(f []byte) []byte { f[23] = 17; return f }},
		{name: "wrong version", mutate: func(f []byte) []byte { f[14] = 0x65; return f }},
		{name: "short header", mutate: func(f []byte) []byte { f[14] = 0x41; return f }},
		{name: "truncated packet", mutate: func(f []byte) []byte { return f[:len(f)-1] }},
		{name: "first fragment", mutate: func(f []byte) []byte { f[20] = 0x20; return f }},
		{name: "later fragment", mutate: func(f []byte) []byte { f[21] = 1; return f }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := make([]byte, 14+20+20)
			binary.BigEndian.PutUint16(frame[12:14], 0x0800)
			frame[14] = 0x45
			binary.BigEndian.PutUint16(frame[16:18], 40)
			frame[23] = 88
			if _, err := extractIPPayload(tc.mutate(frame), 88); err == nil {
				t.Fatal("invalid IPv4 envelope accepted")
			}
		})
	}
}

func TestExtractIPPayloadExcludesPadding(t *testing.T) {
	frame := make([]byte, 60)
	binary.BigEndian.PutUint16(frame[12:14], 0x0800)
	frame[14] = 0x45
	binary.BigEndian.PutUint16(frame[16:18], 40)
	frame[23] = 88
	payload, err := extractIPPayload(frame, 88)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) != 20 {
		t.Errorf("payload length: got %d, want 20", len(payload))
	}
}
