// Package seglog encodes checksummed segment files and scans their valid
// record prefixes in append order.
package seglog

import (
	"encoding/binary"
	"hash/crc32"
	"strconv"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

var (
	// ErrCodeConfig identifies invalid log bounds or timing configuration.
	ErrCodeConfig = errs.NewCode("agent/seglog-config")
	// ErrCodeIO identifies a failure to read or write the log's files.
	ErrCodeIO = errs.NewCode("agent/seglog-io")
	// ErrCodeRecord identifies an empty record or one exceeding the size bound.
	ErrCodeRecord = errs.NewCode("agent/seglog-record")
	// ErrCodeClosed identifies an operation on a closed log.
	ErrCodeClosed = errs.NewCode("agent/seglog-closed")
)

const (
	segmentHeaderBytes = 20
	recordHeaderBytes  = 8
	openSuffix         = ".open"
	sealedSuffix       = ".seg"
)

var checksumTable = crc32.MakeTable(crc32.Castagnoli)

type scanReason string

const (
	scanShortHeader     scanReason = "short header"
	scanBadMagic        scanReason = "bad magic"
	scanUnknownVersion  scanReason = "unknown version"
	scanHeaderChecksum  scanReason = "header checksum"
	scanZeroLength      scanReason = "zero length"
	scanLengthPastEnd   scanReason = "length past the end"
	scanPayloadChecksum scanReason = "payload checksum"
)

// scanResult records the accepted prefix. Records alias the scanned bytes.
// Offset is the start of the rejected header or record, or the input length
// on success. An empty reason means the entire segment was accepted.
type scanResult struct {
	created time.Time
	records [][]byte
	offset  int
	reason  scanReason
}

func appendSegmentHeader(dst []byte, created time.Time) []byte {
	start := len(dst)
	dst = append(dst, 'F', 'S', 'S', 'L')
	dst = binary.LittleEndian.AppendUint16(dst, 1)
	dst = binary.LittleEndian.AppendUint16(dst, 0)
	dst = binary.LittleEndian.AppendUint64(dst, uint64(created.UnixNano()))
	return binary.LittleEndian.AppendUint32(dst, crc32.Checksum(dst[start:], checksumTable))
}

func appendRecord(dst, payload []byte) ([]byte, error) {
	if len(payload) == 0 || uint64(len(payload)) > uint64(1<<32-1) {
		return dst, errs.New().Code(ErrCodeRecord).Attr("bytes", len(payload)).Msg("record length is outside the format bound")
	}

	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(payload)))
	dst = binary.LittleEndian.AppendUint32(dst, crc32.Checksum(payload, checksumTable))
	return append(dst, payload...), nil
}

func scanSegment(data []byte) scanResult {
	var result scanResult
	if len(data) < segmentHeaderBytes {
		result.reason = scanShortHeader
		return result
	}
	if string(data[:4]) != "FSSL" {
		result.reason = scanBadMagic
		return result
	}
	if binary.LittleEndian.Uint16(data[4:6]) != 1 {
		result.reason = scanUnknownVersion
		return result
	}
	if binary.LittleEndian.Uint32(data[16:20]) != crc32.Checksum(data[:16], checksumTable) {
		result.reason = scanHeaderChecksum
		return result
	}

	result.created = time.Unix(0, int64(binary.LittleEndian.Uint64(data[8:16])))
	result.offset = segmentHeaderBytes
	for result.offset < len(data) {
		tail := data[result.offset:]
		if len(tail) < recordHeaderBytes {
			result.reason = scanShortHeader
			return result
		}
		length := binary.LittleEndian.Uint32(tail[:4])
		if length == 0 {
			result.reason = scanZeroLength
			return result
		}
		if uint64(length) > uint64(len(tail)-recordHeaderBytes) {
			result.reason = scanLengthPastEnd
			return result
		}
		payload := tail[recordHeaderBytes : recordHeaderBytes+int(length)]
		if binary.LittleEndian.Uint32(tail[4:8]) != crc32.Checksum(payload, checksumTable) {
			result.reason = scanPayloadChecksum
			return result
		}

		result.records = append(result.records, payload)
		result.offset += recordHeaderBytes + int(length)
	}
	return result
}

func parseSegmentName(name string) (seq uint64, sealed, ok bool) {
	if len(name) != 16+len(openSuffix) && len(name) != 16+len(sealedSuffix) {
		return 0, false, false
	}
	suffix := name[16:]
	if suffix != openSuffix && suffix != sealedSuffix {
		return 0, false, false
	}
	for _, digit := range name[:16] {
		if (digit < '0' || digit > '9') && (digit < 'a' || digit > 'f') {
			return 0, false, false
		}
	}
	seq, err := strconv.ParseUint(name[:16], 16, 64)
	if err != nil {
		return 0, false, false
	}
	return seq, suffix == sealedSuffix, true
}
