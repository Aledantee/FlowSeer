// Standalone differential-test module for the FlowSeer SMI parser.
//
// It lives in its own go.module so the gosmi dependency — kept only as
// the comparand the parser is measured against — never re-enters the
// main module's dependency graph. Run it from this directory; root
// `go build ./...` / `go mod tidy` do not descend here.
module go.aledante.io/FlowSeer/src/protocol/smi/differential

go 1.27

replace go.aledante.io/FlowSeer => ../../../..

require (
	github.com/sleepinggenius2/gosmi v0.4.4
	go.aledante.io/FlowSeer v0.0.0-00010101000000-000000000000
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/alecthomas/participle v0.7.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	go.opentelemetry.io/otel v1.46.0 // indirect
	go.opentelemetry.io/otel/trace v1.46.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)
