package device_test

import (
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
)

var _ device.Device = (*vswitch.Switch)(nil)
