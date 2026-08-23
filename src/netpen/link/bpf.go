// bpf.go builds BPF filter programs as raw instructions. The string BPF DSL
// (tcpdump syntax) is a libpcap feature and does not exist in the cgo-free
// path, so filters are assembled programmatically with golang.org/x/net/bpf
// and converted to the raw form afpacket.SetBPF accepts (KTD4).

package link

import (
	"golang.org/x/net/bpf"
)

// Instruction is a BPF instruction from the x/net/bpf package. Filter builders
// compose these and [Assemble] converts them to the raw form.
type Instruction = bpf.Instruction

// Assemble converts a slice of BPF instructions into the raw form
// afpacket.SetBPF installs. It panics on a malformed instruction, matching
// x/net/bpf.Assemble; callers build filters from known-good constants.
func Assemble(insts []Instruction) ([]RawInstruction, error) {
	raw, err := bpf.Assemble(insts)
	if err != nil {
		return nil, err
	}

	out := make([]RawInstruction, len(raw))
	for i, r := range raw {
		out[i] = RawInstruction{
			Op: r.Op,
			Jt: r.Jt,
			Jf: r.Jf,
			K:  r.K,
		}
	}

	return out, nil
}

// FilterEtherType returns a BPF program that accepts frames whose Ethernet
// EtherType field (offset 12, 2 bytes, big-endian) equals etype. It is the
// building block for per-protocol legs: CDP (0x88be), DTP (0x2004), LLDP
// (0x88cc), and so on. The program loads the EtherType, skips the accept
// instruction on mismatch, and returns a verdict.
func FilterEtherType(etype uint16) ([]RawInstruction, error) {
	return Assemble([]bpf.Instruction{
		bpf.LoadAbsolute{Off: 12, Size: 2},
		bpf.JumpIf{Cond: bpf.JumpNotEqual, Val: uint32(etype), SkipTrue: 1},
		bpf.RetConstant{Val: 4096},
		bpf.RetConstant{Val: 0},
	})
}
