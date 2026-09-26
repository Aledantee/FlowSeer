package conformance

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	systemv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/system/v1"
)

func TestProcessorUtilizationRules(t *testing.T) {
	runFieldCases(t, []fieldCase{
		{
			name: "valid utilization with 60s window",
			message: systemv1.ProcessorUtilization_builder{
				UtilizationAvgBasisPoints: proto.Uint32(4250),
				Window:                    durationpb.New(60 * time.Second),
			}.Build(),
		},
		{
			name: "valid utilization without window",
			message: systemv1.ProcessorUtilization_builder{
				UtilizationAvgBasisPoints: proto.Uint32(0),
			}.Build(),
		},
		{
			name: "valid 100 percent utilization",
			message: systemv1.ProcessorUtilization_builder{
				UtilizationAvgBasisPoints: proto.Uint32(10000),
				Window:                    durationpb.New(time.Second),
			}.Build(),
		},
		{
			name:      "utilization absent",
			message:   systemv1.ProcessorUtilization_builder{}.Build(),
			wantField: "utilization_avg_basis_points",
			wantText:  "required",
		},
		{
			name: "utilization exceeds 10000 basis points",
			message: systemv1.ProcessorUtilization_builder{
				UtilizationAvgBasisPoints: proto.Uint32(10001),
			}.Build(),
			wantField: "utilization_avg_basis_points",
		},
		{
			name: "window is zero seconds",
			message: systemv1.ProcessorUtilization_builder{
				UtilizationAvgBasisPoints: proto.Uint32(5000),
				Window:                    durationpb.New(0),
			}.Build(),
			wantField: "window",
		},
	})
}

func TestStorageUtilizationRules(t *testing.T) {
	runFieldCases(t, []fieldCase{
		{
			name: "valid RAM utilization",
			message: systemv1.StorageUtilization_builder{
				Kind:            systemv1.StorageKind_STORAGE_KIND_RAM.Enum(),
				TotalBytes:      proto.Uint64(100),
				UsedBytes:       proto.Uint64(50),
				UsedBasisPoints: proto.Uint32(5000),
			}.Build(),
		},
		{
			name: "valid named flash utilization",
			message: systemv1.StorageUtilization_builder{
				Name:            proto.String("flash0"),
				Kind:            systemv1.StorageKind_STORAGE_KIND_FLASH_MEMORY.Enum(),
				TotalBytes:      proto.Uint64(100),
				UsedBytes:       proto.Uint64(100),
				UsedBasisPoints: proto.Uint32(10000),
			}.Build(),
		},
		{
			name: "used bytes exceeds total bytes",
			message: systemv1.StorageUtilization_builder{
				TotalBytes: proto.Uint64(100),
				UsedBytes:  proto.Uint64(101),
			}.Build(),
			wantRule: "storage_utilization.used_within_total",
		},
		{
			name: "kind unspecified",
			message: systemv1.StorageUtilization_builder{
				Kind: systemv1.StorageKind_STORAGE_KIND_UNSPECIFIED.Enum(),
			}.Build(),
			wantField: "kind",
		},
		{
			name: "kind unknown value",
			message: systemv1.StorageUtilization_builder{
				Kind: systemv1.StorageKind(7).Enum(),
			}.Build(),
			wantField: "kind",
		},
		{
			name: "name empty",
			message: systemv1.StorageUtilization_builder{
				Name: proto.String(""),
			}.Build(),
			wantField: "name",
		},
		{
			name: "name exceeds 255 chars",
			message: systemv1.StorageUtilization_builder{
				Name: proto.String(strings.Repeat("a", 256)),
			}.Build(),
			wantField: "name",
		},
		{
			name: "used basis points exceeds 10000",
			message: systemv1.StorageUtilization_builder{
				UsedBasisPoints: proto.Uint32(10001),
			}.Build(),
			wantField: "used_basis_points",
		},
	})
}

func TestSoftwareImageRules(t *testing.T) {
	validImage := func() *systemv1.SoftwareImage_builder {
		return &systemv1.SoftwareImage_builder{
			NetworkInstance: proto.String("default"),
			Slot:            proto.String("primary"),
			Version:         proto.String("17.03.01"),
			Running:         proto.Bool(true),
			NextBoot:        proto.Bool(true),
			SizeBytes:       proto.Uint64(500000000),
		}
	}

	withoutSlot := validImage()
	withoutSlot.Slot = nil

	withoutInstance := validImage()
	withoutInstance.NetworkInstance = nil

	emptySlot := validImage()
	emptySlot.Slot = proto.String("")

	longSlot := validImage()
	longSlot.Slot = proto.String(strings.Repeat("s", 256))

	longVersion := validImage()
	longVersion.Version = proto.String(strings.Repeat("v", 129))

	emptyInstance := validImage()
	emptyInstance.NetworkInstance = proto.String("")

	runFieldCases(t, []fieldCase{
		{
			name:    "valid software image",
			message: validImage().Build(),
		},
		{
			name:      "slot absent",
			message:   withoutSlot.Build(),
			wantField: "slot",
			wantText:  "required",
		},
		{
			name:      "slot empty",
			message:   emptySlot.Build(),
			wantField: "slot",
		},
		{
			name:      "slot exceeds 255 chars",
			message:   longSlot.Build(),
			wantField: "slot",
		},
		{
			name:      "network_instance absent",
			message:   withoutInstance.Build(),
			wantField: "network_instance",
			wantText:  "required",
		},
		{
			name:      "network_instance empty",
			message:   emptyInstance.Build(),
			wantField: "network_instance",
		},
		{
			name:      "version exceeds 128 chars",
			message:   longVersion.Build(),
			wantField: "version",
		},
	})
}

func TestLicenseRules(t *testing.T) {
	validLicense := func() *systemv1.License_builder {
		return &systemv1.License_builder{
			NetworkInstance:  proto.String("default"),
			Name:             proto.String("ipbase"),
			Description:      proto.String("IP Base feature set"),
			Status:           systemv1.LicenseStatus_LICENSE_STATUS_IN_USE.Enum(),
			IssuedAt:         timestamppb.New(time.Now()),
			ExpiresAt:        timestamppb.New(time.Now().Add(365 * 24 * time.Hour)),
			EntitlementCount: proto.Uint32(1),
		}
	}

	withoutInstance := validLicense()
	withoutInstance.NetworkInstance = nil

	withoutName := validLicense()
	withoutName.Name = nil

	statusZero := validLicense()
	statusZero.Status = systemv1.LicenseStatus_LICENSE_STATUS_UNSPECIFIED.Enum()

	statusUnknown := validLicense()
	statusUnknown.Status = systemv1.LicenseStatus(6).Enum()

	emptyName := validLicense()
	emptyName.Name = proto.String("")

	longName := validLicense()
	longName.Name = proto.String(strings.Repeat("n", 1025))

	longDescription := validLicense()
	longDescription.Description = proto.String(strings.Repeat("d", 1025))

	runFieldCases(t, []fieldCase{
		{
			name:    "valid license in use",
			message: validLicense().Build(),
		},
		{
			name:      "network_instance absent",
			message:   withoutInstance.Build(),
			wantField: "network_instance",
			wantText:  "required",
		},
		{
			name:      "name absent",
			message:   withoutName.Build(),
			wantField: "name",
			wantText:  "required",
		},
		{
			name:      "name empty",
			message:   emptyName.Build(),
			wantField: "name",
		},
		{
			name:      "name exceeds 1024 chars",
			message:   longName.Build(),
			wantField: "name",
		},
		{
			name:      "description exceeds 1024 chars",
			message:   longDescription.Build(),
			wantField: "description",
		},
		{
			name:      "status is unspecified 0",
			message:   statusZero.Build(),
			wantField: "status",
		},
		{
			name:      "status is undefined enum 6",
			message:   statusUnknown.Build(),
			wantField: "status",
		},
	})
}
