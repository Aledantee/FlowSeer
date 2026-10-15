// Package rawsocket is the byte layer beneath src/modules/capture: a
// local-interface tap (AF_PACKET on Linux, a BPF device on FreeBSD) and, on
// Linux, a mirror-receiver pair of raw IP and UDP sockets. Every other
// platform's build, and the FreeBSD mirror receiver, returns
// ErrUnsupportedPlatform, following src/edge/netpen/link's build-tag shape,
// so the engine (which consumes the Frame shape structurally, not through an
// interface declared here) compiles and its fake-socket tests run everywhere.
package rawsocket
