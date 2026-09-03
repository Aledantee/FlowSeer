// VTP (VLAN Trunking Protocol) layer. Cisco-proprietary, rides LLC/SNAP with
// OUI 0x00000C and PID 0x2003. Three message codes: summary advertisement
// (0x01), subset advertisement (0x02), and advertisement request (0x03).
//
// The decoder surfaces typed fields a behavior can gate on: revision, domain,
// and message code. The summary advertisement carries an MD5 digest over the
// management domain — the decoder does not validate it (the baseline's SAFE
// mode never answers requests, so a valid digest is not the attack path).

package layers

import (
	"encoding/binary"
	"fmt"

	"github.com/gopacket/gopacket"
)

// VTPCode is a VTP message type.
type VTPCode uint8

// VTP message codes.
const (
	VTPCodeSummary VTPCode = 0x01
	VTPCodeSubset  VTPCode = 0x02
	VTPCodeRequest VTPCode = 0x03
)

// VTPVersion is the VTP protocol version.
type VTPVersion uint8

// VTP versions.
const (
	VTPVersion1 VTPVersion = 1
	VTPVersion2 VTPVersion = 2
	VTPVersion3 VTPVersion = 3
)

// VTPVLANInfo is one VLAN record inside a subset advertisement.
type VTPVLANInfo struct {
	VLANID  uint16
	Name    string
	MTU     uint16
	ISLVLAN uint16
	SAID    uint32
	Status  uint8
	Type    uint8
	NameLen uint8
}

// VTP is a VLAN Trunking Protocol frame. Its zero value is ready for decoding.
// Decoding retains data in BaseLayer and is not safe concurrently with other
// uses of the same frame.
type VTP struct {
	BaseLayer
	Version   VTPVersion
	Code      VTPCode
	Seq       uint8 // subset advertisement sequence
	Followers uint8 // summary advertisement followers count
	Domain    string
	Revision  uint32
	Updater   uint32 // summary advertisement updater IP (as uint32)

	// Summary-only fields.
	Timestamp []byte // 12-byte yymmddHHMMSS
	MD5Digest []byte // 16-byte MD5

	// Subset-only fields.
	VLANs []VTPVLANInfo

	// Request-only fields.
	StartValue uint16
}

// LayerType returns LayerTypeVTP.
func (v *VTP) LayerType() gopacket.LayerType { return LayerTypeVTP }

// CanDecode returns the set of layer types this DecodingLayer can decode.
func (v *VTP) CanDecode() gopacket.LayerClass { return LayerTypeVTP }

// NextLayerType returns gopacket.LayerTypeZero; VTP has no sub-layers.
func (v *VTP) NextLayerType() gopacket.LayerType { return gopacket.LayerTypeZero }

// DecodeFromBytes decodes the VTP payload (the bytes after the SNAP header).
func (v *VTP) DecodeFromBytes(data []byte, df gopacket.DecodeFeedback) error {
	if len(data) < 4 {
		df.SetTruncated()
		return fmt.Errorf("VTP: truncated at offset 0, need >=4 bytes, got %d", len(data))
	}

	v.BaseLayer = BaseLayer{Contents: data, Payload: nil}
	v.Version = VTPVersion(data[0])
	v.Code = VTPCode(data[1])

	switch v.Code {
	case VTPCodeSummary:
		v.Followers = data[2]
	case VTPCodeSubset:
		v.Seq = data[2]
	case VTPCodeRequest:
		v.Seq = data[2]
	}

	domainLen := int(data[3])
	if domainLen > 32 {
		domainLen = 32
	}
	if 4+domainLen > len(data) {
		df.SetTruncated()
		return fmt.Errorf("VTP: truncated domain at offset 4, need %d bytes, got %d", domainLen, len(data)-4)
	}
	v.Domain = string(data[4 : 4+domainLen])

	switch v.Code {
	case VTPCodeSummary:
		return v.decodeSummary(data, df)
	case VTPCodeSubset:
		return v.decodeSubset(data, df)
	case VTPCodeRequest:
		return v.decodeRequest(data, df)
	default:
		// Unknown code: the general path holds what it decoded so far.
		return nil
	}
}

func (v *VTP) decodeSummary(data []byte, df gopacket.DecodeFeedback) error {
	if len(data) < 40 {
		df.SetTruncated()
		return fmt.Errorf("VTP: truncated summary at offset %d, need >=40 bytes, got %d", 0, len(data))
	}

	v.Revision = binary.BigEndian.Uint32(data[36:40])

	if len(data) < 44 {
		df.SetTruncated()
		return fmt.Errorf("VTP: truncated summary updater at offset %d, need 44 bytes, got %d", 40, len(data))
	}
	v.Updater = binary.BigEndian.Uint32(data[40:44])

	if len(data) >= 56 {
		v.Timestamp = append(v.Timestamp[:0], data[44:56]...)
	}
	if len(data) >= 72 {
		v.MD5Digest = append(v.MD5Digest[:0], data[56:72]...)
	}

	return nil
}

func (v *VTP) decodeSubset(data []byte, df gopacket.DecodeFeedback) error {
	offset := 4 + 32 // version(1)+code(1)+seq(1)+domainLen(1)+domain(32, zero-padded)
	if len(data) < offset+4 {
		df.SetTruncated()
		return fmt.Errorf("VTP: truncated subset revision at offset %d, need 4 bytes, got %d", offset, len(data)-offset)
	}
	v.Revision = binary.BigEndian.Uint32(data[offset : offset+4])
	offset += 4

	v.VLANs = v.VLANs[:0]
	for offset < len(data) {
		if offset+4 > len(data) {
			df.SetTruncated()
			return fmt.Errorf("VTP: truncated VLAN record header at offset %d, need 4 bytes, got %d", offset, len(data)-offset)
		}
		recLen := int(data[offset])
		if recLen < 12 {
			return fmt.Errorf("VTP: VLAN record at offset %d has length %d < 12 (fixed fields)", offset, recLen)
		}
		if offset+recLen > len(data) {
			df.SetTruncated()
			return fmt.Errorf("VTP: truncated VLAN record at offset %d, need %d bytes, got %d", offset, recLen, len(data)-offset)
		}

		rec := data[offset : offset+recLen]
		vlan := VTPVLANInfo{
			Status:  rec[1],
			Type:    rec[2],
			NameLen: rec[3],
			ISLVLAN: binary.BigEndian.Uint16(rec[4:6]),
			MTU:     binary.BigEndian.Uint16(rec[6:8]),
			SAID:    binary.BigEndian.Uint32(rec[8:12]),
		}
		nameLen := int(rec[3])
		if 12+nameLen <= len(rec) {
			vlan.Name = string(rec[12 : 12+nameLen])
		}
		vlan.VLANID = vlan.ISLVLAN
		v.VLANs = append(v.VLANs, vlan)

		offset += recLen
	}

	return nil
}

func (v *VTP) decodeRequest(data []byte, df gopacket.DecodeFeedback) error {
	offset := 4 + 32
	if len(data) < offset+2 {
		df.SetTruncated()
		return fmt.Errorf("VTP: truncated request start-value at offset %d, need 2 bytes, got %d", offset, len(data)-offset)
	}
	v.StartValue = binary.BigEndian.Uint16(data[offset : offset+2])
	return nil
}

// SerializeTo writes the VTP layer from the typed fields. For summary
// advertisements, the MD5 digest is recomputed if MD5Digest is empty, matching
// the baseline's vtp_generate_md5 (md5 of zeros(16) + body + vlan_records +
// zeros(16), valid only without a VTP password).
func (v *VTP) SerializeTo(b gopacket.SerializeBuffer, _ gopacket.SerializeOptions) error {
	dom := []byte(v.Domain)
	if len(dom) > 32 {
		dom = dom[:32]
	}
	domainField := make([]byte, 32)
	copy(domainField, dom)

	var body []byte
	switch v.Code {
	case VTPCodeSummary:
		body = v.serializeSummary(domainField, uint8(len(dom)))
	case VTPCodeSubset:
		body = v.serializeSubset(domainField, uint8(len(dom)))
	case VTPCodeRequest:
		body = v.serializeRequest(domainField, uint8(len(dom)))
	default:
		return fmt.Errorf("VTP: unknown code 0x%02x", v.Code)
	}

	buf, err := b.PrependBytes(len(body))
	if err != nil {
		return err
	}
	copy(buf, body)
	return nil
}

func (v *VTP) serializeSummary(domainField []byte, domainLen uint8) []byte {
	header := make([]byte, 4)
	header[0] = byte(v.Version)
	header[1] = byte(VTPCodeSummary)
	header[2] = v.Followers
	header[3] = domainLen

	revision := make([]byte, 4)
	binary.BigEndian.PutUint32(revision, v.Revision)

	updater := make([]byte, 4)
	binary.BigEndian.PutUint32(updater, v.Updater)

	ts := v.Timestamp
	if len(ts) == 0 {
		ts = make([]byte, 12)
	}
	if len(ts) > 12 {
		ts = ts[:12]
	}
	tsPadded := make([]byte, 12)
	copy(tsPadded, ts)

	body := make([]byte, 0, 72)
	body = append(body, header...)
	body = append(body, domainField...)
	body = append(body, revision...)
	body = append(body, updater...)
	body = append(body, tsPadded...)
	body = append(body, make([]byte, 16)...) // digest placeholder

	if len(v.MD5Digest) == 16 {
		copy(body[56:72], v.MD5Digest)
	} else {
		digest := md5sum(append(append(make([]byte, 16), body...), make([]byte, 16)...))
		copy(body[56:72], digest)
	}

	return body
}

func (v *VTP) serializeSubset(domainField []byte, domainLen uint8) []byte {
	header := make([]byte, 4)
	header[0] = byte(v.Version)
	header[1] = byte(VTPCodeSubset)
	header[2] = v.Seq
	header[3] = domainLen

	revision := make([]byte, 4)
	binary.BigEndian.PutUint32(revision, v.Revision)

	body := make([]byte, 0, 40)
	body = append(body, header...)
	body = append(body, domainField...)
	body = append(body, revision...)

	for _, vlan := range v.VLANs {
		name := []byte(vlan.Name)
		npad := (len(name) + 3) &^ 3
		recLen := 12 + npad
		rec := make([]byte, recLen)
		rec[0] = byte(recLen)
		rec[1] = vlan.Status
		rec[2] = vlan.Type
		rec[3] = byte(len(name))
		binary.BigEndian.PutUint16(rec[4:6], vlan.ISLVLAN)
		binary.BigEndian.PutUint16(rec[6:8], vlan.MTU)
		binary.BigEndian.PutUint32(rec[8:12], vlan.SAID)
		copy(rec[12:], name)
		body = append(body, rec...)
	}

	return body
}

func (v *VTP) serializeRequest(domainField []byte, domainLen uint8) []byte {
	header := make([]byte, 4)
	header[0] = byte(v.Version)
	header[1] = byte(VTPCodeRequest)
	header[2] = 0
	header[3] = domainLen

	startVal := make([]byte, 2)
	binary.BigEndian.PutUint16(startVal, v.StartValue)

	body := make([]byte, 0, 38)
	body = append(body, header...)
	body = append(body, domainField...)
	body = append(body, startVal...)
	return body
}

func decodeVTP(data []byte, p gopacket.PacketBuilder) error {
	v := &VTP{}
	if err := v.DecodeFromBytes(data, p); err != nil {
		return err
	}
	p.AddLayer(v)
	return nil
}
