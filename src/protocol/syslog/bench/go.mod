module go.aledante.io/FlowSeer/src/protocol/syslog/bench

go 1.27

replace go.aledante.io/FlowSeer => ../../../..

require (
	github.com/leodido/go-syslog/v4 v4.6.1-0.20260710152630-3fd54b1cd303
	go.aledante.io/FlowSeer v0.0.0-00010101000000-000000000000
)

require google.golang.org/protobuf v1.36.12 // indirect
