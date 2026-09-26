package conformance

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	measurev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/measure/v1"
	systemv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/system/v1"
)

func TestComponentSensorRules(t *testing.T) {
	voltageReading := measurev1.SensorReading_builder{
		Voltage: measurev1.Voltage_builder{
			ValueMicrovolts: proto.Int32(12000000),
		}.Build(),
	}.Build()

	currentReading := measurev1.SensorReading_builder{
		Current: measurev1.Current_builder{
			ValueMicroamperes: proto.Int32(8500000),
		}.Build(),
	}.Build()

	powerReading := measurev1.SensorReading_builder{
		Power: measurev1.Power_builder{
			ValueNanowatts: proto.Uint64(102000000000),
		}.Build(),
	}.Build()

	psu := func(sensors ...*measurev1.SensorReading) *inventoryv1.ComponentState {
		return inventoryv1.ComponentState_builder{
			Ref:     componentRef(deviceID, "psu-1"),
			Kind:    inventoryv1.ComponentKind_COMPONENT_KIND_POWER_SUPPLY.Enum(),
			Sensors: sensors,
		}.Build()
	}

	runFieldCases(t, []fieldCase{
		{
			name:    "multiple distinct sensor readings on power supply pass",
			message: psu(voltageReading, currentReading, powerReading),
		},
		{
			name:      "empty sensor reading fails oneof requirement",
			message:   psu(measurev1.SensorReading_builder{}.Build()),
			wantField: "sensors[0].quantity",
			wantText:  "exactly one field is required",
		},
		{
			name:     "two readings of same quantity fail uniqueness rule",
			message:  psu(voltageReading, voltageReading),
			wantRule: "component_state.one_reading_per_quantity",
		},
	})
}

func TestComponentOperStatusAndUtilizationRules(t *testing.T) {
	cpuWith := func(modify func(b *inventoryv1.ComponentState_builder)) *inventoryv1.ComponentState {
		b := inventoryv1.ComponentState_builder{
			Ref:  componentRef(deviceID, "cpu-0"),
			Kind: inventoryv1.ComponentKind_COMPONENT_KIND_CPU.Enum(),
		}
		modify(&b)
		return b.Build()
	}

	fanWith := func(modify func(b *inventoryv1.ComponentState_builder)) *inventoryv1.ComponentState {
		b := inventoryv1.ComponentState_builder{
			Ref:  componentRef(deviceID, "fan-0"),
			Kind: inventoryv1.ComponentKind_COMPONENT_KIND_FAN.Enum(),
		}
		modify(&b)
		return b.Build()
	}

	storageWith := func(modify func(b *inventoryv1.ComponentState_builder)) *inventoryv1.ComponentState {
		b := inventoryv1.ComponentState_builder{
			Ref:  componentRef(deviceID, "disk-0"),
			Kind: inventoryv1.ComponentKind_COMPONENT_KIND_STORAGE.Enum(),
		}
		modify(&b)
		return b.Build()
	}

	validProcUtil := systemv1.ProcessorUtilization_builder{
		UtilizationAvgBasisPoints: proto.Uint32(4250),
		Window:                    durationpb.New(60 * time.Second),
	}.Build()

	validStorageUtil := systemv1.StorageUtilization_builder{
		TotalBytes: proto.Uint64(1000),
		UsedBytes:  proto.Uint64(500),
	}.Build()

	runFieldCases(t, []fieldCase{
		{
			name: "valid oper_status UP on CPU passes",
			message: cpuWith(func(b *inventoryv1.ComponentState_builder) {
				b.OperStatus = inventoryv1.ComponentOperStatus_COMPONENT_OPER_STATUS_UP.Enum()
			}),
		},
		{
			name: "oper_status UNSPECIFIED fails",
			message: cpuWith(func(b *inventoryv1.ComponentState_builder) {
				b.OperStatus = inventoryv1.ComponentOperStatus_COMPONENT_OPER_STATUS_UNSPECIFIED.Enum()
			}),
			wantField: "oper_status",
			wantText:  "must not be in list",
		},
		{
			name: "valid processor utilization on CPU passes",
			message: cpuWith(func(b *inventoryv1.ComponentState_builder) {
				b.ProcessorUtilization = validProcUtil
			}),
		},
		{
			name: "processor utilization on fan fails rule",
			message: fanWith(func(b *inventoryv1.ComponentState_builder) {
				b.ProcessorUtilization = validProcUtil
			}),
			wantRule: "component_state.processor_utilization_only_cpu",
		},
		{
			name: "processor utilization basis points exceeds 10000 fails",
			message: cpuWith(func(b *inventoryv1.ComponentState_builder) {
				b.ProcessorUtilization = systemv1.ProcessorUtilization_builder{
					UtilizationAvgBasisPoints: proto.Uint32(10001),
				}.Build()
			}),
			wantField: "processor_utilization.utilization_avg_basis_points",
			wantText:  "value must be a basis-point ratio in 0..10000",
		},
		{
			name: "processor utilization window of 0s fails",
			message: cpuWith(func(b *inventoryv1.ComponentState_builder) {
				b.ProcessorUtilization = systemv1.ProcessorUtilization_builder{
					UtilizationAvgBasisPoints: proto.Uint32(5000),
					Window:                    durationpb.New(0),
				}.Build()
			}),
			wantField: "processor_utilization.window",
			wantText:  "must be greater than 0s",
		},
		{
			name: "storage utilization on CPU fails rule",
			message: cpuWith(func(b *inventoryv1.ComponentState_builder) {
				b.StorageUtilization = validStorageUtil
			}),
			wantRule: "component_state.storage_utilization_only_storage",
		},
		{
			name: "storage utilization on storage component passes",
			message: storageWith(func(b *inventoryv1.ComponentState_builder) {
				b.StorageUtilization = validStorageUtil
			}),
		},
		{
			name: "storage utilization with used > total fails used_within_total rule",
			message: storageWith(func(b *inventoryv1.ComponentState_builder) {
				b.StorageUtilization = systemv1.StorageUtilization_builder{
					TotalBytes: proto.Uint64(100),
					UsedBytes:  proto.Uint64(101),
				}.Build()
			}),
			wantRule: "storage_utilization.used_within_total",
		},
		{
			name: "named storage utilization on component fails storage_unnamed rule",
			message: storageWith(func(b *inventoryv1.ComponentState_builder) {
				b.StorageUtilization = systemv1.StorageUtilization_builder{
					Name:       proto.String("flash:"),
					TotalBytes: proto.Uint64(1000),
					UsedBytes:  proto.Uint64(500),
				}.Build()
			}),
			wantRule: "component_state.storage_unnamed",
		},
	})
}

func TestDeviceSystemIdentityRules(t *testing.T) {
	deviceWith := func(modify func(b *inventoryv1.DeviceState_builder)) *inventoryv1.DeviceState {
		b := inventoryv1.DeviceState_builder{
			Ref:       deviceRef(deviceID),
			Lifecycle: inventoryv1.DeviceLifecycle_DEVICE_LIFECYCLE_ACTIVE.Enum(),
		}
		modify(&b)
		return b.Build()
	}

	maxContact := strings.Repeat("c", 255)
	overContact := strings.Repeat("c", 256)

	runFieldCases(t, []fieldCase{
		{
			name: "contact of 255 chars passes",
			message: deviceWith(func(b *inventoryv1.DeviceState_builder) {
				b.SystemContact = proto.String(maxContact)
			}),
		},
		{
			name: "contact of 256 chars fails",
			message: deviceWith(func(b *inventoryv1.DeviceState_builder) {
				b.SystemContact = proto.String(overContact)
			}),
			wantField: "system_contact",
			wantText:  "must be at most 255 characters",
		},
		{
			name: "empty location fails min_len",
			message: deviceWith(func(b *inventoryv1.DeviceState_builder) {
				b.SystemLocation = proto.String("")
			}),
			wantField: "system_location",
			wantText:  "must be at least 1 characters",
		},
		{
			name: "max sysUpTime uptime passes",
			message: deviceWith(func(b *inventoryv1.DeviceState_builder) {
				b.Uptime = durationpb.New(time.Duration(42949672950) * time.Millisecond)
			}),
		},
		{
			name: "negative uptime fails duration gte",
			message: deviceWith(func(b *inventoryv1.DeviceState_builder) {
				b.Uptime = durationpb.New(-1 * time.Second)
			}),
			wantField: "uptime",
			wantText:  "must be greater than or equal to 0s",
		},
	})
}

func TestDeviceUtilizationRules(t *testing.T) {
	deviceWith := func(modify func(b *inventoryv1.DeviceState_builder)) *inventoryv1.DeviceState {
		b := inventoryv1.DeviceState_builder{
			Ref:       deviceRef(deviceID),
			Lifecycle: inventoryv1.DeviceLifecycle_DEVICE_LIFECYCLE_ACTIVE.Enum(),
		}
		modify(&b)
		return b.Build()
	}

	validProcUtil := systemv1.ProcessorUtilization_builder{
		UtilizationAvgBasisPoints: proto.Uint32(2500),
	}.Build()

	row1 := systemv1.StorageUtilization_builder{
		Name:       proto.String("flash:"),
		TotalBytes: proto.Uint64(1000),
		UsedBytes:  proto.Uint64(200),
	}.Build()

	row2 := systemv1.StorageUtilization_builder{
		Name:       proto.String("nvram:"),
		TotalBytes: proto.Uint64(500),
		UsedBytes:  proto.Uint64(50),
	}.Build()

	unnamedRow := systemv1.StorageUtilization_builder{
		TotalBytes: proto.Uint64(1000),
		UsedBytes:  proto.Uint64(200),
	}.Build()

	runFieldCases(t, []fieldCase{
		{
			name: "valid processor and storage utilization pass on device",
			message: deviceWith(func(b *inventoryv1.DeviceState_builder) {
				b.ProcessorUtilization = validProcUtil
				b.StorageUtilization = []*systemv1.StorageUtilization{row1, row2}
			}),
		},
		{
			name: "unnamed storage row on device fails storage_named rule",
			message: deviceWith(func(b *inventoryv1.DeviceState_builder) {
				b.StorageUtilization = []*systemv1.StorageUtilization{unnamedRow}
			}),
			wantRule: "device_state.storage_named",
		},
		{
			name: "duplicate storage row names on device fail storage_names_unique rule",
			message: deviceWith(func(b *inventoryv1.DeviceState_builder) {
				b.StorageUtilization = []*systemv1.StorageUtilization{row1, row1}
			}),
			wantRule: "device_state.storage_names_unique",
		},
	})
}
