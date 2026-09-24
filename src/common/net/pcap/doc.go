// Package pcap reads classic pcap and pcapng packet records without decoding
// their link-layer payloads. A record owns its bytes, so callers can retain it
// after advancing the reader.
package pcap
