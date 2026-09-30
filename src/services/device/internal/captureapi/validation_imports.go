package captureapi

import (
	// Uploaded chunks carry net/switching VLAN rules in their mirror fields.
	// The schema imports those rules as an option, so protovalidate needs the
	// generated package linked to register the rule extension before validation.
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
)
