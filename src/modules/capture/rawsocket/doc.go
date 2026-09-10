// Package rawsocket is the Linux-only byte layer beneath src/modules/capture:
// an AF_PACKET local-interface tap and a mirror-receiver pair of raw IP and
// UDP sockets. Every other platform's build returns ErrUnsupportedPlatform,
// following src/edge/netpen/link's build-tag shape, so the engine (which
// consumes the Frame shape structurally, not through an interface declared
// here) compiles and its fake-socket tests run everywhere.
package rawsocket
