// Package rawsocket is the byte layer beneath src/modules/capture: a
// local-interface tap (AF_PACKET on Linux, a BPF device on FreeBSD) and, on
// Linux and FreeBSD amd64/arm64, a mirror-receiver pair of raw IP and UDP
// sockets. Builds outside Linux and FreeBSD amd64/arm64 return
// ErrUnsupportedPlatform, so the engine compiles and its fake-socket tests
// run everywhere.
package rawsocket
