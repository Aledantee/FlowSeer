// Package pcapng renders flowseer.net.capture.v1 values as a pcapng file
// (draft-ietf-opsawg-pcapng-05): a Section Header Block, one Interface
// Description Block, one Enhanced Packet Block per PacketRecord, and one
// Interface Statistics Block on close. Writer streams to an io.Writer so a
// caller can pipe records it drains from a live capture straight to disk
// without buffering the whole capture in memory first.
package pcapng
