// Package mirror decapsulates the mirror wrapper protocols
// flowseer.net.capture.v1.MirrorEncapsulation names: ERSPAN Type I, II, and
// III, plain GRE, VXLAN, and TZSP. Decode and DecodeUDP are pure functions
// over already-received bytes; they know nothing about sockets, so the same
// code runs over a hand-built fixture and over whatever a live receiver
// hands it.
package mirror
