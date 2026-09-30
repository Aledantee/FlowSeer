package captureapi

import (
	// This package validates messages whose fields carry net/key and net/switching predefined rules, and the global registry resolves them only when the generated packages are linked.
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/key/v1"
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
)
