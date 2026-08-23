// Standalone netpen module: the L2/L3 security audit and attack binary.
//
// It lives in its own go.module so the gopacket and bubbletea dependency
// family never enters the main module's dependency graph (the main
// module's no_heavy_deps_test.go guard stays green). Run it from this
// directory; root `go build ./...` / `go mod tidy` do not descend here.
module go.aledante.io/FlowSeer/src/netpen

go 1.26.5

replace go.aledante.io/FlowSeer => ../../

require (
	github.com/gopacket/gopacket v1.7.1
	go.aledante.io/FlowSeer v0.0.0-00010101000000-000000000000
	golang.org/x/net v0.58.0
	golang.org/x/sys v0.47.0
)
