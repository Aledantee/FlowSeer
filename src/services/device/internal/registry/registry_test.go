package registry_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/policy/v1"
	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/registry"
)

const (
	edgeID    = "0192e6a0-0000-7000-8000-0000000000ed"
	deviceID  = "0192e6a0-0000-7000-8000-0000000000d1"
	policyKey = "icx7150-lab"
)

func accessHandle() *policyv1.AccessPolicyHandle {
	return policyv1.AccessPolicyHandle_builder{Key: proto.String(policyKey), Version: proto.Uint64(3)}.Build()
}

// validDevice builds a listed device; mutate tweaks one field for a case.
func validDevice(mutate func(*inventoryv1.DeviceConfig, *storev1.RegistryDevice)) *storev1.RegistryDevice {
	config := inventoryv1.DeviceConfig_builder{
		Ref:            inventoryv1.DeviceGlobalRef_builder{Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(deviceID)}.Build()}.Build(),
		Name:           proto.String("icx7150"),
		ManagementMode: inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED.Enum(),
		AccessPolicy:   accessHandle(),
	}.Build()
	device := storev1.RegistryDevice_builder{
		Config:              config,
		Binding:             inventoryv1.BindingGlobalRef_builder{Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String("0192e6a0-0000-7000-8000-0000000000b1")}.Build()}.Build(),
		Ip:                  addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{172, 16, 0, 6}}.Build()}.Build(),
		DelayedApplyHorizon: durationpb.New(30 * time.Second),
		ManagedInterfaces:   []string{"ethernet 1/1/1"},
	}.Build()
	if mutate != nil {
		mutate(config, device)
	}
	return device
}

func validPolicy() *storev1.RegistryPolicy {
	return storev1.RegistryPolicy_builder{
		Handle:               accessHandle(),
		ReadCredential:       policyv1.CredentialHandle_builder{Key: proto.String("icx7150-lab-snmp"), Version: proto.Uint64(1)}.Build(),
		SubmissionCredential: policyv1.CredentialHandle_builder{Key: proto.String("icx7150-lab-ssh"), Version: proto.Uint64(1)}.Build(),
		HostTrust:            policyv1.HostTrustHandle_builder{Key: proto.String("icx7150-lab-hostkey"), Version: proto.Uint64(1)}.Build(),
		SshHostKeySha256:     proto.String("SHA256:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU"),
	}.Build()
}

func validRegistry(device *storev1.RegistryDevice) *storev1.DeviceRegistry {
	return storev1.DeviceRegistry_builder{
		Integration: storev1.RegistryIntegration_builder{
			Ref:  inventoryv1.IntegrationGlobalRef_builder{Integration: inventoryv1.IntegrationLocalRef_builder{Id: proto.String("0192e6a0-0000-7000-8000-0000000000c1")}.Build()}.Build(),
			Edge: edgev1.EdgeGlobalRef_builder{Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edgeID)}.Build()}.Build(),
		}.Build(),
		Devices:  []*storev1.RegistryDevice{device},
		Policies: []*storev1.RegistryPolicy{validPolicy()},
	}.Build()
}

func load(t *testing.T, reg *storev1.DeviceRegistry) (*registry.Registry, error) {
	t.Helper()
	data, err := prototext.Marshal(reg)
	if err != nil {
		t.Fatalf("marshal registry: %v", err)
	}
	path := filepath.Join(t.TempDir(), "registry.textproto")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	return registry.Load(path)
}

func TestLoadResolvesDevicesPoliciesAndBinding(t *testing.T) {
	r, err := load(t, validRegistry(validDevice(nil)))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ctx := context.Background()

	if _, ok := r.Device(deviceID); !ok {
		t.Fatal("listed device not found")
	}
	if _, ok := r.Policy(policyKey, 3); !ok {
		t.Fatal("listed policy not found")
	}
	devices, err := r.Devices(ctx, edgeID)
	if err != nil || len(devices) != 1 || devices[0] != deviceID {
		t.Fatalf("Devices(edge) = %v, %v", devices, err)
	}
	if devices, _ := r.Devices(ctx, "some-other-edge"); len(devices) != 0 {
		t.Fatalf("a foreign edge hosts %d devices, want 0", len(devices))
	}
	horizon, err := r.Horizon(ctx, deviceID)
	if err != nil || horizon != 30*time.Second {
		t.Fatalf("Horizon = %v, %v", horizon, err)
	}
	if hosts, _ := r.Hosts(ctx, edgeID, deviceID); !hosts {
		t.Fatal("the registry's own edge does not host its device")
	}
	if hosts, _ := r.Hosts(ctx, "some-other-edge", deviceID); hosts {
		t.Fatal("a foreign edge hosts the device")
	}
	if hosts, _ := r.Hosts(ctx, edgeID, "some-other-device"); hosts {
		t.Fatal("the edge hosts an unlisted device")
	}
}

func TestLoadRefusesADevicePolicyNotListed(t *testing.T) {
	// The device's access policy names a version no listed policy provides.
	// The schema's device_policies_listed CEL rule refuses it at load — the
	// check is not hand-rolled here.
	device := validDevice(func(config *inventoryv1.DeviceConfig, _ *storev1.RegistryDevice) {
		config.SetAccessPolicy(policyv1.AccessPolicyHandle_builder{Key: proto.String(policyKey), Version: proto.Uint64(99)}.Build())
	})
	_, err := load(t, validRegistry(device))
	if code, _ := errs.CodeOf(err); code != registry.ErrCodeInvalid {
		t.Fatalf("load error code = %v, want registry/invalid", code)
	}
}

func TestHorizonUnsetIsAnError(t *testing.T) {
	device := validDevice(func(_ *inventoryv1.DeviceConfig, d *storev1.RegistryDevice) {
		d.ClearDelayedApplyHorizon()
	})
	r, err := load(t, validRegistry(device))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	_, err = r.Horizon(context.Background(), deviceID)
	if code, _ := errs.CodeOf(err); code != registry.ErrCodeHorizonUnset {
		t.Fatalf("Horizon error code = %v, want device/horizon-unset", code)
	}
}

func TestListsReportsMembership(t *testing.T) {
	r, err := load(t, validRegistry(validDevice(nil)))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ctx := context.Background()
	if listed, err := r.Lists(ctx, deviceID); err != nil || !listed {
		t.Fatalf("Lists(listed) = %v, %v", listed, err)
	}
	if listed, err := r.Lists(ctx, "some-other-device"); err != nil || listed {
		t.Fatalf("Lists(unlisted) = %v, %v", listed, err)
	}
}
