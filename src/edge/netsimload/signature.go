package netsimload

import (
	"encoding/binary"
	"fmt"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
)

const (
	// SignatureSize is the payload reservation required by the wire signature.
	SignatureSize = 32

	signatureVersion uint32 = 1
)

var signatureMagic = [4]byte{'F', 'S', 'L', 'D'}

// Signature identifies one transmitted frame and the userspace submission
// instant encoded in its reserved payload prefix.
type Signature struct {
	FlowID      fabric.FlowID
	Sequence    uint64
	SubmittedAt time.Time
}

// Sign copies frame and writes the version-one signature into the first
// SignatureSize payload octets. The source frame is never mutated.
func Sign(frame ethernet.Frame, flowID fabric.FlowID, sequence uint64, submittedAt time.Time) (ethernet.Frame, error) {
	if flowID == 0 {
		return ethernet.Frame{}, fmt.Errorf("flow ID must be nonzero")
	}
	if len(frame.Payload) < SignatureSize {
		return ethernet.Frame{}, fmt.Errorf("frame payload is %d octets, need at least %d", len(frame.Payload), SignatureSize)
	}

	frame.Payload = append([]byte(nil), frame.Payload...)
	copy(frame.Payload[0:4], signatureMagic[:])
	binary.BigEndian.PutUint32(frame.Payload[4:8], signatureVersion)
	binary.BigEndian.PutUint32(frame.Payload[8:12], uint32(flowID))
	binary.BigEndian.PutUint32(frame.Payload[12:16], 0)
	binary.BigEndian.PutUint64(frame.Payload[16:24], sequence)
	binary.BigEndian.PutUint64(frame.Payload[24:32], uint64(submittedAt.UnixNano()))

	return frame, nil
}

// DecodeSignature decodes the reserved signature from an Ethernet payload.
// It rejects short data, a different magic or version, and a nonzero reserved
// word so unrelated traffic cannot enter a configured flow.
func DecodeSignature(payload []byte) (Signature, error) {
	if len(payload) < SignatureSize {
		return Signature{}, fmt.Errorf("signature payload is %d octets, need at least %d", len(payload), SignatureSize)
	}
	if string(payload[0:4]) != string(signatureMagic[:]) {
		return Signature{}, fmt.Errorf("signature magic is %q", payload[0:4])
	}
	if version := binary.BigEndian.Uint32(payload[4:8]); version != signatureVersion {
		return Signature{}, fmt.Errorf("signature version is %d", version)
	}
	if reserved := binary.BigEndian.Uint32(payload[12:16]); reserved != 0 {
		return Signature{}, fmt.Errorf("signature reserved word is %d", reserved)
	}

	return Signature{
		FlowID:      fabric.FlowID(binary.BigEndian.Uint32(payload[8:12])),
		Sequence:    binary.BigEndian.Uint64(payload[16:24]),
		SubmittedAt: time.Unix(0, int64(binary.BigEndian.Uint64(payload[24:32]))),
	}, nil
}

// DecodeWireSignature decodes a signature from complete Ethernet wire bytes.
func DecodeWireSignature(wire []byte) (Signature, error) {
	frame, err := ethernet.Decode(wire)
	if err != nil {
		return Signature{}, fmt.Errorf("decode Ethernet frame: %w", err)
	}

	return DecodeSignature(frame.Payload)
}
