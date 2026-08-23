// Package layers holds netpen's owned L2/L3 protocol decoders — the protocols
// gopacket's fork does not provide. Each decoder follows gopacket's layer
// contract (DecodeFromBytes/CanDecode/NextLayerType/LayerType + SerializeTo)
// and is registered into the fork's dispatch tables at init time so the
// Ethernet → LLC → SNAP → protocol and Ethernet → protocol chains reach them.
//
// Every decoder is a Guarded Fast Path: specialized paths decline with
// ok=false, never error, and the general path holds the semantics. Decoders
// are pinned by differential fixture tests (testdata/*.pcap) rather than
// parser symmetry; malformed and adversarial frames accrete in the corpus
// with provenance, never deleted.
package layers

import (
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

// BaseLayer is the fork's layers.BaseLayer, re-exported so owned decoder
// structs embed it without a package-qualified reference (our package is also
// named layers).
type BaseLayer = layers.BaseLayer

// LayerType numbers for owned protocols. The gopacket fork reserves 0-999;
// 1000-1999 are for application-specific types. We use 200-204 to avoid the
// fork's 1000-1534 range.
var (
	LayerTypeDTP  = gopacket.RegisterLayerType(200, gopacket.LayerTypeMetadata{Name: "DTP", Decoder: gopacket.DecodeFunc(decodeDTP)})
	LayerTypeVTP  = gopacket.RegisterLayerType(201, gopacket.LayerTypeMetadata{Name: "VTP", Decoder: gopacket.DecodeFunc(decodeVTP)})
	LayerTypeMVRP = gopacket.RegisterLayerType(202, gopacket.LayerTypeMetadata{Name: "MVRP", Decoder: gopacket.DecodeFunc(decodeMVRP)})
	LayerTypePAgP = gopacket.RegisterLayerType(203, gopacket.LayerTypeMetadata{Name: "PAgP", Decoder: gopacket.DecodeFunc(decodePAgP)})
	LayerTypeLACP = gopacket.RegisterLayerType(204, gopacket.LayerTypeMetadata{Name: "LACP", Decoder: gopacket.DecodeFunc(decodeLACP)})
)

// Cisco SNAP protocol IDs carried in the SNAP type field under OUI 0x00000C.
// These register into the fork's EthernetTypeMetadata so the SNAP decoder
// dispatches to our owned decoders the same way it dispatches to CDP (0x2000).
const (
	ciscoOUI           = 0x00000C
	snapPIDDTP  uint16 = 0x2004
	snapPIDVTP  uint16 = 0x2003
	snapPIDPAgP uint16 = 0x0104
)

// EtherType values for directly-Ethernet-encapsulated owned protocols.
const (
	ethertypeLACP uint16 = 0x8809
	ethertypeMVRP uint16 = 0x88F5
)

// LACP slow-protocol subtype.
const lacpSubtype uint8 = 0x01

func init() {
	// Register SNAP PIDs as EthernetType entries so the fork's SNAP decoder
	// dispatches to our decoders. The fork treats the SNAP type field as an
	// EthernetType and dispatches through EthernetTypeMetadata; this is the
	// same mechanism CDP (0x2000) uses.
	layers.EthernetTypeMetadata[layers.EthernetType(snapPIDDTP)] = layers.EnumMetadata{
		DecodeWith: gopacket.DecodeFunc(decodeDTP),
		Name:       "DTP",
		LayerType:  LayerTypeDTP,
	}
	layers.EthernetTypeMetadata[layers.EthernetType(snapPIDVTP)] = layers.EnumMetadata{
		DecodeWith: gopacket.DecodeFunc(decodeVTP),
		Name:       "VTP",
		LayerType:  LayerTypeVTP,
	}
	layers.EthernetTypeMetadata[layers.EthernetType(snapPIDPAgP)] = layers.EnumMetadata{
		DecodeWith: gopacket.DecodeFunc(decodePAgP),
		Name:       "PAgP",
		LayerType:  LayerTypePAgP,
	}

	// Register direct-Ethernet EtherTypes.
	layers.EthernetTypeMetadata[layers.EthernetType(ethertypeLACP)] = layers.EnumMetadata{
		DecodeWith: gopacket.DecodeFunc(decodeLACP),
		Name:       "LACP",
		LayerType:  LayerTypeLACP,
	}
	layers.EthernetTypeMetadata[layers.EthernetType(ethertypeMVRP)] = layers.EnumMetadata{
		DecodeWith: gopacket.DecodeFunc(decodeMVRP),
		Name:       "MVRP",
		LayerType:  LayerTypeMVRP,
	}
}
