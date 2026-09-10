package filter

import (
	"fmt"

	"golang.org/x/net/bpf"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
)

// RawInstruction is one assembled BPF instruction, the form SO_ATTACH_FILTER
// accepts.
type RawInstruction = bpf.RawInstruction

// Assemble converts insts to their raw form.
func Assemble(insts []bpf.Instruction) ([]RawInstruction, error) {
	return bpf.Assemble(insts)
}

// acceptLen is the verdict a matching program returns to the kernel: the
// number of packet bytes to keep. It exceeds any frame this engine will ever
// see, so a match always keeps the whole packet.
const acceptLen = 0x40000

// maxInstructions is the classic BPF program length the kernel enforces
// (BPF_MAXINSNS).
const maxInstructions = 4096

const (
	dot1qEtherType = 0x8100
	ipv4EtherType  = 0x0800
	ipv6EtherType  = 0x86dd

	dstMACOff = 0
	srcMACOff = 6
)

// frameLayout names where the real EtherType and the IP header start, under
// one assumption about whether a single 802.1Q tag is present. A tag shifts
// every later field's offset by 4 bytes; a MAC address, at a fixed offset
// either way, does not go through this.
type frameLayout struct {
	etherTypeOff uint32
	ipOff        uint32
}

var (
	untagged = frameLayout{etherTypeOff: 12, ipOff: 14}
	tagged   = frameLayout{etherTypeOff: 16, ipOff: 18}
)

type ipField int

const (
	fieldSrc ipField = iota
	fieldDst
)

// block emits its instructions into prog and returns the indices of every
// trailing Jump instruction still needing a target: the caller decides what
// "this did not match" means and backpatches accordingly. A block that
// matches falls through to whatever the caller appends next.
type block func(prog *[]bpf.Instruction) []int

// Compile turns f into a linear cBPF program. An absent or empty filter
// accepts every packet, matching the schema's stated semantics. Pass the
// result through Assemble for the raw form SO_ATTACH_FILTER accepts.
func Compile(f *capturev1.CaptureFilter) ([]bpf.Instruction, error) {
	clauses := f.GetAnyOf()
	if len(clauses) == 0 {
		return []bpf.Instruction{bpf.RetConstant{Val: acceptLen}}, nil
	}

	blocks := make([]block, len(clauses))
	for i, c := range clauses {
		b, err := compileClause(c)
		if err != nil {
			return nil, fmt.Errorf("compile clause %d: %w", i, err)
		}
		blocks[i] = b
	}

	var prog []bpf.Instruction
	fails := or(blocks...)(&prog)

	// A matched clause falls through to here: keep the packet.
	prog = append(prog, bpf.RetConstant{Val: acceptLen})
	rejectPos := len(prog)
	prog = append(prog, bpf.RetConstant{Val: 0})
	backpatch(&prog, fails, rejectPos)

	if len(prog) > maxInstructions {
		return nil, fmt.Errorf("filter compiles to %d instructions, over the %d classic BPF ceiling", len(prog), maxInstructions)
	}

	return prog, nil
}

// compileClause combines a clause's populated fields with AND. The schema's
// own validation rule guarantees at least one field is populated; the error
// path below is defense in depth, not a case a valid CaptureFilterClause can
// reach.
func compileClause(c *capturev1.CaptureFilterClause) (block, error) {
	var blocks []block

	if c.HasEtherType() {
		blocks = append(blocks, etherTypeBlock(c.GetEtherType()))
	}
	if c.HasVlan() {
		blocks = append(blocks, vlanBlock(c.GetVlan()))
	}
	if c.HasSrcMac() {
		b, err := macBlock(srcMACOff, c.GetSrcMac())
		if err != nil {
			return nil, fmt.Errorf("src_mac: %w", err)
		}
		blocks = append(blocks, b)
	}
	if c.HasDstMac() {
		b, err := macBlock(dstMACOff, c.GetDstMac())
		if err != nil {
			return nil, fmt.Errorf("dst_mac: %w", err)
		}
		blocks = append(blocks, b)
	}
	if c.HasSrcPrefix() {
		b, err := prefixFieldBlock(fieldSrc, c.GetSrcPrefix())
		if err != nil {
			return nil, fmt.Errorf("src_prefix: %w", err)
		}
		blocks = append(blocks, b)
	}
	if c.HasDstPrefix() {
		b, err := prefixFieldBlock(fieldDst, c.GetDstPrefix())
		if err != nil {
			return nil, fmt.Errorf("dst_prefix: %w", err)
		}
		blocks = append(blocks, b)
	}
	if c.HasIpProtocol() {
		b, err := ipProtocolBlock(c.GetIpProtocol())
		if err != nil {
			return nil, fmt.Errorf("ip_protocol: %w", err)
		}
		blocks = append(blocks, b)
	}
	if c.HasSrcPort() {
		b, err := portBlock(0, c.GetSrcPort())
		if err != nil {
			return nil, fmt.Errorf("src_port: %w", err)
		}
		blocks = append(blocks, b)
	}
	if c.HasDstPort() {
		b, err := portBlock(2, c.GetDstPort())
		if err != nil {
			return nil, fmt.Errorf("dst_port: %w", err)
		}
		blocks = append(blocks, b)
	}
	if c.HasTcpFlags() {
		b, err := tcpFlagsBlock(c.GetTcpFlags())
		if err != nil {
			return nil, fmt.Errorf("tcp_flags: %w", err)
		}
		blocks = append(blocks, b)
	}
	if c.HasIcmp() {
		b, err := icmpBlock(c.GetIcmp())
		if err != nil {
			return nil, fmt.Errorf("icmp: %w", err)
		}
		blocks = append(blocks, b)
	}
	if c.HasDscp() {
		b, err := dscpBlock(c.GetDscp())
		if err != nil {
			return nil, fmt.Errorf("dscp: %w", err)
		}
		blocks = append(blocks, b)
	}

	if len(blocks) == 0 {
		return nil, fmt.Errorf("clause constrains no field")
	}
	return and(blocks...), nil
}

// backpatch points every Jump instruction named by idxs at target.
func backpatch(prog *[]bpf.Instruction, idxs []int, target int) {
	for _, idx := range idxs {
		(*prog)[idx] = bpf.Jump{Skip: uint32(target - idx - 1)}
	}
}

// atom is a leaf block: run setup, which must leave the compared value in
// register A, then compare it. On a mismatch it jumps to a placeholder the
// caller backpatches; on a match it falls through.
func atom(setup []bpf.Instruction, cond bpf.JumpTest, val uint32) block {
	return func(prog *[]bpf.Instruction) []int {
		*prog = append(*prog, setup...)
		*prog = append(*prog, bpf.JumpIf{Cond: cond, Val: val, SkipTrue: 1, SkipFalse: 0})
		idx := len(*prog)
		*prog = append(*prog, bpf.Jump{Skip: 0})
		return []int{idx}
	}
}

func loadAbs(off uint32, size int) []bpf.Instruction {
	return []bpf.Instruction{bpf.LoadAbsolute{Off: off, Size: size}}
}

// atomAbs is atom for the common case: an absolute load compared for
// equality. Every offset-and-value field check other than a port range uses
// it.
func atomAbs(off uint32, size int, val uint32) block {
	return atom(loadAbs(off, size), bpf.JumpEqual, val)
}

// and requires every block to match; the first failure exits through the
// shared trampoline the caller backpatches. A clause's populated fields
// compile to and() of one block per field.
func and(blocks ...block) block {
	if len(blocks) == 1 {
		return blocks[0]
	}
	return func(prog *[]bpf.Instruction) []int {
		var fails []int
		for _, b := range blocks {
			fails = append(fails, b(prog)...)
		}
		return fails
	}
}

// or requires at least one block to match: a match jumps past the remaining
// alternatives, and only once every alternative has failed does it hand the
// caller its own trampoline. A CaptureFilter's clauses compile to or() of
// one block per clause; a field whose value alone does not fix an address
// family compiles to or() of its IPv4 and IPv6 forms.
func or(blocks ...block) block {
	if len(blocks) == 1 {
		return blocks[0]
	}
	return func(prog *[]bpf.Instruction) []int {
		var passJumps []int
		for _, b := range blocks {
			fails := b(prog)
			passJumps = append(passJumps, len(*prog))
			*prog = append(*prog, bpf.Jump{Skip: 0})
			backpatch(prog, fails, len(*prog))
		}
		failIdx := len(*prog)
		*prog = append(*prog, bpf.Jump{Skip: 0})
		backpatch(prog, passJumps, len(*prog))
		return []int{failIdx}
	}
}

// withLayouts builds a block trying both the untagged and single-802.1Q-
// tagged interpretations of the frame, since a field whose position depends
// on the IP header's start cannot know in advance whether a tag shifted it.
func withLayouts(build func(frameLayout) (block, error)) (block, error) {
	u, err := build(untagged)
	if err != nil {
		return nil, err
	}
	t, err := build(tagged)
	if err != nil {
		return nil, err
	}
	return or(u, and(atomAbs(untagged.etherTypeOff, 2, dot1qEtherType), t)), nil
}

func etherTypeBlock(want packetv1.EtherType) block {
	w := uint32(want)
	return or(
		atomAbs(untagged.etherTypeOff, 2, w),
		and(atomAbs(untagged.etherTypeOff, 2, dot1qEtherType), atomAbs(tagged.etherTypeOff, 2, w)),
	)
}

func vlanBlock(v *capturev1.VlanMatch) block {
	blocks := []block{atomAbs(untagged.etherTypeOff, 2, dot1qEtherType)}
	if v.HasVlanId() {
		blocks = append(blocks, atom([]bpf.Instruction{
			bpf.LoadAbsolute{Off: 14, Size: 2},
			bpf.ALUOpConstant{Op: bpf.ALUOpAnd, Val: 0x0FFF},
		}, bpf.JumpEqual, v.GetVlanId()))
	}
	if v.HasPcp() {
		blocks = append(blocks, atom([]bpf.Instruction{
			bpf.LoadAbsolute{Off: 14, Size: 2},
			bpf.ALUOpConstant{Op: bpf.ALUOpAnd, Val: 0xE000},
			bpf.ALUOpConstant{Op: bpf.ALUOpShiftRight, Val: 13},
		}, bpf.JumpEqual, v.GetPcp()))
	}
	return and(blocks...)
}

func be32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func be16(b []byte) uint32 {
	return uint32(b[0])<<8 | uint32(b[1])
}

func macBlock(off uint32, addr *addrv1.EuiAddress) (block, error) {
	if !addr.HasEui48() {
		return nil, fmt.Errorf("a MAC match requires a 48-bit address")
	}
	octets := addr.GetEui48().GetOctets()
	if len(octets) != 6 {
		return nil, fmt.Errorf("a 48-bit address must carry 6 octets, got %d", len(octets))
	}
	return and(
		atomAbs(off, 4, be32(octets[0:4])),
		atomAbs(off+4, 2, be16(octets[4:6])),
	), nil
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func ipv4Mask(length uint32) uint32 {
	if length == 0 {
		return 0
	}
	return ^uint32(0) << (32 - length)
}

func maskedAtom(off uint32, size int, mask, want uint32) block {
	setup := append(loadAbs(off, size), bpf.ALUOpConstant{Op: bpf.ALUOpAnd, Val: mask})
	return atom(setup, bpf.JumpEqual, want)
}

func prefixBlock(l frameLayout, which ipField, p *addrv1.IpPrefix) (block, error) {
	switch {
	case p.HasV4():
		v4 := p.GetV4()
		addr := v4.GetAddress().GetOctets()
		if len(addr) != 4 {
			return nil, fmt.Errorf("an IPv4 prefix address must carry 4 octets, got %d", len(addr))
		}
		off := l.ipOff + 12
		if which == fieldDst {
			off = l.ipOff + 16
		}
		mask := ipv4Mask(v4.GetLength())
		return and(
			atomAbs(l.etherTypeOff, 2, ipv4EtherType),
			maskedAtom(off, 4, mask, be32(addr)&mask),
		), nil
	case p.HasV6():
		v6 := p.GetV6()
		addr := v6.GetAddress().GetOctets()
		if len(addr) != 16 {
			return nil, fmt.Errorf("an IPv6 prefix address must carry 16 octets, got %d", len(addr))
		}
		base := l.ipOff + 8
		if which == fieldDst {
			base = l.ipOff + 24
		}
		length := int(v6.GetLength())
		blocks := []block{atomAbs(l.etherTypeOff, 2, ipv6EtherType)}
		for i := range 4 {
			wordBits := clamp(length-32*i, 0, 32)
			if wordBits == 0 {
				continue
			}
			mask := ^uint32(0) << uint(32-wordBits)
			want := be32(addr[i*4:i*4+4]) & mask
			blocks = append(blocks, maskedAtom(base+uint32(i*4), 4, mask, want))
		}
		return and(blocks...), nil
	default:
		return nil, fmt.Errorf("an IP prefix names neither v4 nor v6")
	}
}

func prefixFieldBlock(which ipField, p *addrv1.IpPrefix) (block, error) {
	return withLayouts(func(l frameLayout) (block, error) { return prefixBlock(l, which, p) })
}

func ipProtocolFamilyBlock(l frameLayout, want uint32) block {
	return or(
		and(atomAbs(l.etherTypeOff, 2, ipv4EtherType), atomAbs(l.ipOff+9, 1, want)),
		and(atomAbs(l.etherTypeOff, 2, ipv6EtherType), atomAbs(l.ipOff+6, 1, want)),
	)
}

func ipProtocolBlock(want packetv1.IpProtocol) (block, error) {
	w := uint32(want)
	return withLayouts(func(l frameLayout) (block, error) { return ipProtocolFamilyBlock(l, w), nil })
}

// loadMemShiftAbs computes X = the IPv4 header length in bytes, read from
// the IHL nibble at off (the first byte of the IP header).
func loadMemShiftAbs(off uint32) []bpf.Instruction {
	return []bpf.Instruction{bpf.LoadMemShift{Off: off}}
}

// ipv4NoFragmentOffset requires the IPv4 fragment offset field (the low 13
// bits of the flags-and-fragment-offset word at bytes 6-7) to be zero. A
// non-first fragment carries no transport header at the offset a field
// below assumes: those bytes are payload continuation, and reading a port,
// a TCP flags byte, or an ICMP type and code from them would match on
// arbitrary payload data rather than reject the packet.
func ipv4NoFragmentOffset(l frameLayout) block {
	return atom(loadAbs(l.ipOff+6, 2), bpf.JumpBitsNotSet, 0x1FFF)
}

// portBearingProtocol matches the IP protocols whose header places a
// 16-bit port at the first two bytes past the IP header: TCP, UDP, and
// SCTP. A field that reads the transport header without this gate would
// match a differently-shaped header's bytes (an ICMP type and code, say) as
// if they were a port.
func portBearingProtocol(protocolOff uint32) block {
	return or(
		atomAbs(protocolOff, 1, uint32(packetv1.IpProtocol_IP_PROTOCOL_TCP)),
		atomAbs(protocolOff, 1, uint32(packetv1.IpProtocol_IP_PROTOCOL_UDP)),
		atomAbs(protocolOff, 1, uint32(packetv1.IpProtocol_IP_PROTOCOL_SCTP)),
	)
}

func portSetupV4(l frameLayout, portOff uint32) []bpf.Instruction {
	return append(loadMemShiftAbs(l.ipOff), bpf.LoadIndirect{Off: l.ipOff + portOff, Size: 2})
}

func portSetupV6(l frameLayout, portOff uint32) []bpf.Instruction {
	// IPv6 extension headers are not walked: the transport header is assumed
	// to follow the fixed 40-byte header immediately.
	return loadAbs(l.ipOff+40+portOff, 2)
}

func portCompare(setup []bpf.Instruction, m *packetv1.TransportPortMatch) (block, error) {
	switch {
	case m.HasExact():
		return atom(setup, bpf.JumpEqual, m.GetExact()), nil
	case m.HasRange():
		r := m.GetRange()
		return and(
			atom(setup, bpf.JumpGreaterOrEqual, r.GetStart()),
			atom(setup, bpf.JumpLessOrEqual, r.GetEnd()),
		), nil
	default:
		return nil, fmt.Errorf("a port match names neither exact nor range")
	}
}

func portFamilyBlock(l frameLayout, portOff uint32, m *packetv1.TransportPortMatch) (block, error) {
	v4Port, err := portCompare(portSetupV4(l, portOff), m)
	if err != nil {
		return nil, err
	}
	v6Port, err := portCompare(portSetupV6(l, portOff), m)
	if err != nil {
		return nil, err
	}
	return or(
		and(
			atomAbs(l.etherTypeOff, 2, ipv4EtherType),
			ipv4NoFragmentOffset(l),
			portBearingProtocol(l.ipOff+9),
			v4Port,
		),
		and(
			atomAbs(l.etherTypeOff, 2, ipv6EtherType),
			portBearingProtocol(l.ipOff+6),
			v6Port,
		),
	), nil
}

func portBlock(portOff uint32, m *packetv1.TransportPortMatch) (block, error) {
	return withLayouts(func(l frameLayout) (block, error) { return portFamilyBlock(l, portOff, m) })
}

func flagMask(flags []packetv1.TcpFlag) uint32 {
	var m uint32
	for _, f := range flags {
		m |= uint32(f)
	}
	return m
}

func tcpFlagsCompare(setup []bpf.Instruction, setMask, clearMask uint32) block {
	var blocks []block
	if setMask != 0 {
		masked := append(append([]bpf.Instruction{}, setup...), bpf.ALUOpConstant{Op: bpf.ALUOpAnd, Val: setMask})
		blocks = append(blocks, atom(masked, bpf.JumpEqual, setMask))
	}
	if clearMask != 0 {
		blocks = append(blocks, atom(setup, bpf.JumpBitsNotSet, clearMask))
	}
	return and(blocks...)
}

func tcpFlagsBlock(m *packetv1.TcpFlagsMatch) (block, error) {
	setMask := flagMask(m.GetRequiredSet())
	clearMask := flagMask(m.GetRequiredClear())
	if setMask == 0 && clearMask == 0 {
		return nil, fmt.Errorf("a TCP flags match requires at least one set or clear flag")
	}
	return withLayouts(func(l frameLayout) (block, error) {
		v4Setup := append(loadMemShiftAbs(l.ipOff), bpf.LoadIndirect{Off: l.ipOff + 13, Size: 1})
		v6Setup := loadAbs(l.ipOff+40+13, 1)
		v4 := and(
			atomAbs(l.etherTypeOff, 2, ipv4EtherType),
			ipv4NoFragmentOffset(l),
			atomAbs(l.ipOff+9, 1, uint32(packetv1.IpProtocol_IP_PROTOCOL_TCP)),
			tcpFlagsCompare(v4Setup, setMask, clearMask),
		)
		v6 := and(
			atomAbs(l.etherTypeOff, 2, ipv6EtherType),
			atomAbs(l.ipOff+6, 1, uint32(packetv1.IpProtocol_IP_PROTOCOL_TCP)),
			tcpFlagsCompare(v6Setup, setMask, clearMask),
		)
		return or(v4, v6), nil
	})
}

func icmpValuesOR(setup []bpf.Instruction, values []uint32) block {
	alts := make([]block, len(values))
	for i, v := range values {
		alts[i] = atom(setup, bpf.JumpEqual, v)
	}
	return or(alts...)
}

func icmpv4FamilyBlock(l frameLayout, m *packetv1.Icmpv4Match) block {
	blocks := []block{
		atomAbs(l.etherTypeOff, 2, ipv4EtherType),
		ipv4NoFragmentOffset(l),
		atomAbs(l.ipOff+9, 1, uint32(packetv1.IpProtocol_IP_PROTOCOL_ICMP)),
	}
	typeSetup := append(loadMemShiftAbs(l.ipOff), bpf.LoadIndirect{Off: l.ipOff, Size: 1})
	codeSetup := append(loadMemShiftAbs(l.ipOff), bpf.LoadIndirect{Off: l.ipOff + 1, Size: 1})
	if types := m.GetTypes(); len(types) > 0 {
		blocks = append(blocks, icmpValuesOR(typeSetup, types))
	}
	if codes := m.GetCodes(); len(codes) > 0 {
		blocks = append(blocks, icmpValuesOR(codeSetup, codes))
	}
	return and(blocks...)
}

func icmpv6FamilyBlock(l frameLayout, m *packetv1.Icmpv6Match) block {
	blocks := []block{
		atomAbs(l.etherTypeOff, 2, ipv6EtherType),
		atomAbs(l.ipOff+6, 1, uint32(packetv1.IpProtocol_IP_PROTOCOL_ICMPV6)),
	}
	typeSetup := loadAbs(l.ipOff+40, 1)
	codeSetup := loadAbs(l.ipOff+41, 1)
	if types := m.GetTypes(); len(types) > 0 {
		blocks = append(blocks, icmpValuesOR(typeSetup, types))
	}
	if codes := m.GetCodes(); len(codes) > 0 {
		blocks = append(blocks, icmpValuesOR(codeSetup, codes))
	}
	return and(blocks...)
}

func icmpBlock(m *packetv1.IcmpMatch) (block, error) {
	switch {
	case m.HasV4():
		v4 := m.GetV4()
		if len(v4.GetTypes()) == 0 && len(v4.GetCodes()) == 0 {
			return nil, fmt.Errorf("an ICMPv4 match requires at least one type or code")
		}
		return withLayouts(func(l frameLayout) (block, error) { return icmpv4FamilyBlock(l, v4), nil })
	case m.HasV6():
		v6 := m.GetV6()
		if len(v6.GetTypes()) == 0 && len(v6.GetCodes()) == 0 {
			return nil, fmt.Errorf("an ICMPv6 match requires at least one type or code")
		}
		return withLayouts(func(l frameLayout) (block, error) { return icmpv6FamilyBlock(l, v6), nil })
	default:
		return nil, fmt.Errorf("an ICMP match names neither v4 nor v6")
	}
}

func dscpFamilyBlock(l frameLayout, want uint32) block {
	v4 := atom([]bpf.Instruction{
		bpf.LoadAbsolute{Off: l.ipOff + 1, Size: 1},
		bpf.ALUOpConstant{Op: bpf.ALUOpShiftRight, Val: 2},
	}, bpf.JumpEqual, want)
	v6 := atom([]bpf.Instruction{
		bpf.LoadAbsolute{Off: l.ipOff, Size: 2},
		bpf.ALUOpConstant{Op: bpf.ALUOpShiftRight, Val: 6},
		bpf.ALUOpConstant{Op: bpf.ALUOpAnd, Val: 0x3F},
	}, bpf.JumpEqual, want)
	return or(
		and(atomAbs(l.etherTypeOff, 2, ipv4EtherType), v4),
		and(atomAbs(l.etherTypeOff, 2, ipv6EtherType), v6),
	)
}

func dscpBlock(want packetv1.IpDscp) (block, error) {
	w := uint32(want)
	return withLayouts(func(l frameLayout) (block, error) { return dscpFamilyBlock(l, w), nil })
}
