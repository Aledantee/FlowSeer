// Standalone benchmark module for the FlowSeer SMI parser.
//
// It lives in its own go.module so benchmark-only code — the fixture
// pins, the synthesized malformed corpus, the gate harness — never
// enters the main module's build or test graph. Run it from this
// directory; root `go build ./...` / `go mod tidy` do not descend here.
//
// Unlike src/protocol/snmp/bench there is no third-party comparand to
// keep out. The isolation is kept anyway, because it is what makes the
// benchmark suite a separate thing to run and a separate thing to
// break, and because a reader who knows one bench module knows both.
module go.aledante.io/FlowSeer/src/protocol/smi/bench

go 1.27

replace go.aledante.io/FlowSeer => ../../../..

require (
	go.aledante.io/FlowSeer v0.0.0-00010101000000-000000000000
	gopkg.in/yaml.v3 v3.0.1
)
