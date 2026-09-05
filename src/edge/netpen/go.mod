// Standalone netpen module: the L2/L3 security audit and attack binary.
//
// It lives in its own go.module so the gopacket and bubbletea dependency
// family never enters the main module's dependency graph (the main
// module's no_heavy_deps_test.go guard stays green). Run it from this
// directory; root `go build ./...` / `go mod tidy` do not descend here.
module go.aledante.io/FlowSeer/src/edge/netpen

go 1.27

replace go.aledante.io/FlowSeer => ../../../

require (
	charm.land/bubbles/v2 v2.2.1
	charm.land/bubbletea/v2 v2.0.9
	charm.land/lipgloss/v2 v2.0.6
	github.com/gopacket/gopacket v1.7.1
	go.aledante.io/FlowSeer v0.0.0-00010101000000-000000000000
	golang.org/x/net v0.58.0
	golang.org/x/term v0.45.0
)

require (
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20260903151058-ae99b731b8c5 // indirect
	github.com/charmbracelet/x/ansi v0.11.8 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mattn/go-runewidth v0.0.29 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/xo/terminfo v1.0.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)
