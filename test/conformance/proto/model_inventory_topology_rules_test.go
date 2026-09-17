package conformance

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
)

const (
	locationID   = "0b3c4d1e-9c7a-4a4e-8b1c-6f1a2b3c4d5e"
	rackID       = "1c4d5e2f-ad8b-4b5f-9c2d-7a2b3c4d5e6f"
	cableID      = "2d5e6f30-be9c-4c60-8d3e-8b3c4d5e6f70"
	panelID      = "3e6f7041-cfad-4d71-9e4f-9c4d5e6f7081"
	linkID       = "4f708152-d0be-4e82-8f50-ad5e6f708192"
	topologyDevA = "5081a263-e1cf-4f93-9061-be6f7081a2a3"
	topologyDevB = "6192b374-f2d0-4084-8172-cf7081a2b3b4"
)

func locationRef(id string) *inventoryv1.LocationGlobalRef {
	return inventoryv1.LocationGlobalRef_builder{
		Location: inventoryv1.LocationLocalRef_builder{Id: proto.String(id)}.Build(),
	}.Build()
}

func location(id string, kind inventoryv1.LocationKind) inventoryv1.Location_builder {
	return inventoryv1.Location_builder{
		Ref:  locationRef(id),
		Kind: kind.Enum(),
		Name: proto.String("Berlin"),
	}
}

func TestLocationRules(t *testing.T) {
	site := location(locationID, inventoryv1.LocationKind_LOCATION_KIND_SITE)
	site.Latitude = proto.Float64(52.52)
	site.Longitude = proto.Float64(13.405)

	rack := location(rackID, inventoryv1.LocationKind_LOCATION_KIND_RACK)
	rack.Parent = locationRef(locationID)
	rack.RackUnitHeight = proto.Uint32(42)

	tallRoom := location(rackID, inventoryv1.LocationKind_LOCATION_KIND_ROOM)
	tallRoom.RackUnitHeight = proto.Uint32(42)

	selfParent := location(locationID, inventoryv1.LocationKind_LOCATION_KIND_SITE)
	selfParent.Parent = locationRef(locationID)

	halfCoordinates := location(locationID, inventoryv1.LocationKind_LOCATION_KIND_SITE)
	halfCoordinates.Latitude = proto.Float64(52.52)

	runValidationCases(t, []validationCase{
		{name: "a site with coordinates is valid", message: site.Build(), wantValid: true},
		{name: "a rack under a site with a height is valid", message: rack.Build(), wantValid: true},
		{name: "kind is required", message: inventoryv1.Location_builder{Ref: locationRef(locationID), Name: proto.String("x")}.Build()},
		{name: "rack unit height is only for racks", message: tallRoom.Build()},
		{name: "a location cannot be its own parent", message: selfParent.Build()},
		{name: "latitude without longitude is rejected", message: halfCoordinates.Build()},
		{
			name:      "a created location event carries only after",
			message:   inventoryv1.LocationEvent_builder{Ref: locationRef(locationID), After: site.Build()}.Build(),
			wantValid: true,
		},
		{name: "an event with neither side is rejected", message: inventoryv1.LocationEvent_builder{Ref: locationRef(locationID)}.Build()},
		{
			name: "an event whose after names another location is rejected",
			message: inventoryv1.LocationEvent_builder{
				Ref:   locationRef(locationID),
				After: location(rackID, inventoryv1.LocationKind_LOCATION_KIND_SITE).Build(),
			}.Build(),
		},
		{
			name: "an event whose before names another location is rejected",
			message: inventoryv1.LocationEvent_builder{
				Ref:    locationRef(locationID),
				Before: location(rackID, inventoryv1.LocationKind_LOCATION_KIND_SITE).Build(),
				After:  site.Build(),
			}.Build(),
		},
	})
}

func TestDeviceLocationRules(t *testing.T) {
	racked := deviceConfig(inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED, accessPolicyHandle("icx7150-lab", 1))
	racked.SetLocation(locationRef(rackID))
	racked.SetRackPosition(12)
	racked.SetRackFace(inventoryv1.RackFace_RACK_FACE_FRONT)
	rackedNowhere := deviceConfig(inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED, accessPolicyHandle("icx7150-lab", 1))
	rackedNowhere.SetRackPosition(12)

	identified := inventoryv1.DeviceState_builder{
		Ref:             deviceRef(topologyDevA),
		Lifecycle:       inventoryv1.DeviceLifecycle_DEVICE_LIFECYCLE_ACTIVE.Enum(),
		Hostname:        proto.String("core-1"),
		Vendor:          proto.String("Ruckus"),
		Model:           proto.String("ICX7150-48P"),
		SoftwareVersion: proto.String("10.0.10g"),
		SysObjectId:     proto.String("1.3.6.1.4.1.1991.1.3.71.1"),
	}
	badOID := inventoryv1.DeviceState_builder{
		Ref:         deviceRef(topologyDevA),
		Lifecycle:   inventoryv1.DeviceLifecycle_DEVICE_LIFECYCLE_ACTIVE.Enum(),
		SysObjectId: proto.String("1.3.6.1.4.1.x"),
	}

	runValidationCases(t, []validationCase{
		{name: "a device in a rack is valid", message: racked, wantValid: true},
		{name: "a rack position needs a location", message: rackedNowhere},
		{name: "a device with its platform reading is valid", message: identified.Build(), wantValid: true},
		{name: "a sysObjectID is dotted decimal", message: badOID.Build()},
	})
}

func componentRef(device, name string) *inventoryv1.ComponentGlobalRef {
	return inventoryv1.ComponentGlobalRef_builder{
		Device:    deviceRef(device),
		Component: inventoryv1.ComponentLocalRef_builder{Name: proto.String(name)}.Build(),
	}.Build()
}

func TestComponentRules(t *testing.T) {
	port := inventoryv1.ComponentState_builder{
		Ref:           componentRef(topologyDevA, "1/1/1"),
		Parent:        inventoryv1.ComponentLocalRef_builder{Name: proto.String("chassis")}.Build(),
		Kind:          inventoryv1.ComponentKind_COMPONENT_KIND_PORT.Enum(),
		InterfaceName: proto.String("GigabitEthernet1/1/1"),
		Position:      proto.Uint32(1),
	}
	transceiver := inventoryv1.ComponentState_builder{
		Ref:    componentRef(topologyDevA, "1/1/1 module"),
		Parent: inventoryv1.ComponentLocalRef_builder{Name: proto.String("1/1/1")}.Build(),
		Kind:   inventoryv1.ComponentKind_COMPONENT_KIND_TRANSCEIVER.Enum(),
		Module: phyv1.PluggableModule_builder{Present: proto.Bool(false)}.Build(),
	}
	moduleOnPort := inventoryv1.ComponentState_builder{
		Ref:    componentRef(topologyDevA, "1/1/1"),
		Kind:   inventoryv1.ComponentKind_COMPONENT_KIND_PORT.Enum(),
		Module: phyv1.PluggableModule_builder{Present: proto.Bool(false)}.Build(),
	}
	selfParent := inventoryv1.ComponentState_builder{
		Ref:    componentRef(topologyDevA, "chassis"),
		Parent: inventoryv1.ComponentLocalRef_builder{Name: proto.String("chassis")}.Build(),
		Kind:   inventoryv1.ComponentKind_COMPONENT_KIND_CHASSIS.Enum(),
	}

	runValidationCases(t, []validationCase{
		{name: "a port under the chassis is valid", message: port.Build(), wantValid: true},
		{name: "a transceiver with its module is valid", message: transceiver.Build(), wantValid: true},
		{name: "a module reading belongs only to a transceiver", message: moduleOnPort.Build()},
		{name: "a component cannot be its own parent", message: selfParent.Build()},
		{name: "kind is required", message: inventoryv1.ComponentState_builder{Ref: componentRef(topologyDevA, "x")}.Build()},
		{
			name: "an event whose after names another device is rejected",
			message: inventoryv1.ComponentEvent_builder{
				Ref: componentRef(topologyDevB, "1/1/1"),
				After: inventoryv1.ComponentState_builder{
					Ref:  componentRef(topologyDevA, "1/1/1"),
					Kind: inventoryv1.ComponentKind_COMPONENT_KIND_PORT.Enum(),
				}.Build(),
			}.Build(),
		},
	})
}

func panelPortRef(port string) *inventoryv1.PatchPanelPortGlobalRef {
	return inventoryv1.PatchPanelPortGlobalRef_builder{
		Panel: inventoryv1.PatchPanelGlobalRef_builder{
			Panel: inventoryv1.PatchPanelLocalRef_builder{Id: proto.String(panelID)}.Build(),
		}.Build(),
		Port: inventoryv1.PatchPanelPortLocalRef_builder{Name: proto.String(port)}.Build(),
	}.Build()
}

func panelPort(name string, side inventoryv1.PatchPanelPortSide) *inventoryv1.PatchPanelPort {
	return inventoryv1.PatchPanelPort_builder{Name: proto.String(name), Side: side.Enum()}.Build()
}

func TestPatchPanelRules(t *testing.T) {
	front := panelPort("F1", inventoryv1.PatchPanelPortSide_PATCH_PANEL_PORT_SIDE_FRONT)
	front.SetPairedPort(inventoryv1.PatchPanelPortLocalRef_builder{Name: proto.String("R1")}.Build())
	panel := inventoryv1.PatchPanel_builder{
		Ref:          panelPortRef("F1").GetPanel(),
		Name:         proto.String("PP-01"),
		Location:     locationRef(rackID),
		RackPosition: proto.Uint32(40),
		RackFace:     inventoryv1.RackFace_RACK_FACE_FRONT.Enum(),
		Ports:        []*inventoryv1.PatchPanelPort{front, panelPort("R1", inventoryv1.PatchPanelPortSide_PATCH_PANEL_PORT_SIDE_REAR)},
	}
	duplicate := inventoryv1.PatchPanel_builder{
		Ref:   panel.Ref,
		Name:  proto.String("PP-01"),
		Ports: []*inventoryv1.PatchPanelPort{front, front},
	}
	rackedNowhere := inventoryv1.PatchPanel_builder{
		Ref:          panel.Ref,
		Name:         proto.String("PP-01"),
		RackPosition: proto.Uint32(40),
	}
	selfPort := panelPort("F2", inventoryv1.PatchPanelPortSide_PATCH_PANEL_PORT_SIDE_FRONT)
	selfPort.SetPairedPort(inventoryv1.PatchPanelPortLocalRef_builder{Name: proto.String("F2")}.Build())
	selfPaired := inventoryv1.PatchPanel_builder{
		Ref:   panel.Ref,
		Name:  proto.String("PP-01"),
		Ports: []*inventoryv1.PatchPanelPort{selfPort},
	}

	runValidationCases(t, []validationCase{
		{name: "a racked panel with a wired-through port is valid", message: panel.Build(), wantValid: true},
		{name: "port names are unique within the panel", message: duplicate.Build()},
		{name: "a rack position needs a location", message: rackedNowhere.Build()},
		{name: "a port side is required", message: inventoryv1.PatchPanelPort_builder{Name: proto.String("F1")}.Build()},
		{name: "a port is not wired through to itself", message: selfPaired.Build()},
		{
			name:      "a created panel event carries only after",
			message:   inventoryv1.PatchPanelEvent_builder{Ref: panel.Ref, After: panel.Build()}.Build(),
			wantValid: true,
		},
		{name: "a panel event with neither side is rejected", message: inventoryv1.PatchPanelEvent_builder{Ref: panel.Ref}.Build()},
		{
			name: "a panel event whose before names another panel is rejected",
			message: inventoryv1.PatchPanelEvent_builder{
				Ref:    inventoryv1.PatchPanelGlobalRef_builder{Panel: inventoryv1.PatchPanelLocalRef_builder{Id: proto.String(cableID)}.Build()}.Build(),
				Before: panel.Build(),
				After:  panel.Build(),
			}.Build(),
		},
	})
}

func termination(port *inventoryv1.ComponentGlobalRef) *inventoryv1.CableTermination {
	return inventoryv1.CableTermination_builder{
		Connector: phyv1.ModuleConnector_MODULE_CONNECTOR_RJ45.Enum(),
		Port:      port,
	}.Build()
}

func cable(a, b *inventoryv1.CableTermination) inventoryv1.Cable_builder {
	return inventoryv1.Cable_builder{
		Ref:               inventoryv1.CableGlobalRef_builder{Cable: inventoryv1.CableLocalRef_builder{Id: proto.String(cableID)}.Build()}.Build(),
		Medium:            inventoryv1.CableMedium_CABLE_MEDIUM_CAT6A.Enum(),
		Role:              inventoryv1.CableRole_CABLE_ROLE_PATCH.Enum(),
		LengthMillimeters: proto.Uint32(2000),
		Color:             proto.String("blue"),
		Label:             proto.String("A-017"),
		AEnd:              a,
		BEnd:              b,
	}
}

func TestCableRules(t *testing.T) {
	portA := termination(componentRef(topologyDevA, "1/1/1"))
	toPanel := inventoryv1.CableTermination_builder{PanelPort: panelPortRef("F1")}.Build()
	toOutlet := inventoryv1.CableTermination_builder{Location: locationRef(locationID)}.Build()

	patch := cable(portA, toPanel)
	permanent := cable(inventoryv1.CableTermination_builder{PanelPort: panelPortRef("R1")}.Build(), toOutlet)
	permanent.Role = inventoryv1.CableRole_CABLE_ROLE_PERMANENT_LINK.Enum()
	trunk := cable(toPanel, inventoryv1.CableTermination_builder{PanelPort: panelPortRef("R1")}.Build())
	trunk.Role = inventoryv1.CableRole_CABLE_ROLE_TRUNK.Enum()
	trunk.Medium = inventoryv1.CableMedium_CABLE_MEDIUM_MULTIMODE_OM4.Enum()
	trunk.StrandCount = proto.Uint32(12)
	strandedPatch := cable(portA, toPanel)
	strandedPatch.StrandCount = proto.Uint32(12)
	looped := cable(portA, termination(componentRef(topologyDevA, "1/1/1")))
	loopedOtherConnector := cable(portA, inventoryv1.CableTermination_builder{
		Connector: phyv1.ModuleConnector_MODULE_CONNECTOR_LC.Enum(),
		Port:      componentRef(topologyDevA, "1/1/1"),
	}.Build())
	unterminated := cable(portA, inventoryv1.CableTermination_builder{}.Build())

	runValidationCases(t, []validationCase{
		{name: "a patch cord from a port to a panel is valid", message: patch.Build(), wantValid: true},
		{name: "a permanent link from a rear port to an outlet is valid", message: permanent.Build(), wantValid: true},
		{name: "a fiber trunk with a strand count is valid", message: trunk.Build(), wantValid: true},
		{name: "strand count is only for trunks", message: strandedPatch.Build()},
		{name: "both ends on the same port is rejected", message: looped.Build()},
		{name: "the same port with different connectors is still one port", message: loopedOtherConnector.Build()},
		{
			name:      "a created cable event carries only after",
			message:   inventoryv1.CableEvent_builder{Ref: patch.Ref, After: patch.Build()}.Build(),
			wantValid: true,
		},
		{name: "a cable event with neither side is rejected", message: inventoryv1.CableEvent_builder{Ref: patch.Ref}.Build()},
		{
			name: "a cable event whose before names another cable is rejected",
			message: inventoryv1.CableEvent_builder{
				Ref:    inventoryv1.CableGlobalRef_builder{Cable: inventoryv1.CableLocalRef_builder{Id: proto.String(linkID)}.Build()}.Build(),
				Before: patch.Build(),
				After:  patch.Build(),
			}.Build(),
		},
		{name: "an end must terminate on something", message: unterminated.Build()},
		{name: "medium is required", message: inventoryv1.Cable_builder{Ref: patch.Ref, Role: patch.Role, AEnd: portA, BEnd: toPanel}.Build()},
	})
}

func linkEnd(device, name string) *inventoryv1.LinkEnd {
	return inventoryv1.LinkEnd_builder{InterfaceName: proto.String(name), Device: deviceRef(device)}.Build()
}

func TestLinkRules(t *testing.T) {
	now := time.Now()
	link := inventoryv1.LinkState_builder{
		Ref:       inventoryv1.LinkGlobalRef_builder{Link: inventoryv1.LinkLocalRef_builder{Id: proto.String(linkID)}.Build()}.Build(),
		A:         linkEnd(topologyDevA, "1/1/1"),
		B:         linkEnd(topologyDevB, "1/1/24"),
		Source:    inventoryv1.LinkSource_LINK_SOURCE_LLDP.Enum(),
		Status:    inventoryv1.LinkStatus_LINK_STATUS_ACTIVE.Enum(),
		FirstSeen: timestamppb.New(now.Add(-time.Hour)),
		LastSeen:  timestamppb.New(now),
	}
	foreign := link
	foreign.B = inventoryv1.LinkEnd_builder{
		InterfaceName: proto.String("eth0"),
		Foreign:       inventoryv1.ForeignSystem_builder{ChassisId: proto.String("00:11:22:33:44:55"), SystemName: proto.String("printer")}.Build(),
	}.Build()
	looped := link
	looped.B = linkEnd(topologyDevA, "1/1/1")
	backwards := link
	backwards.LastSeen = timestamppb.New(now.Add(-2 * time.Hour))
	endless := link
	endless.B = inventoryv1.LinkEnd_builder{InterfaceName: proto.String("eth0")}.Build()

	runValidationCases(t, []validationCase{
		{name: "an LLDP link between two devices is valid", message: link.Build(), wantValid: true},
		{name: "a link to a foreign system is valid", message: foreign.Build(), wantValid: true},
		{name: "both ends on the same interface is rejected", message: looped.Build()},
		{name: "last seen before first seen is rejected", message: backwards.Build()},
		{name: "an end must name a system", message: endless.Build()},
		{
			name:      "a first observation event carries only after",
			message:   inventoryv1.LinkEvent_builder{Ref: link.Ref, After: link.Build()}.Build(),
			wantValid: true,
		},
		{name: "a link event with neither side is rejected", message: inventoryv1.LinkEvent_builder{Ref: link.Ref}.Build()},
		{
			name: "a link event whose after names another link is rejected",
			message: inventoryv1.LinkEvent_builder{
				Ref:   inventoryv1.LinkGlobalRef_builder{Link: inventoryv1.LinkLocalRef_builder{Id: proto.String(cableID)}.Build()}.Build(),
				After: link.Build(),
			}.Build(),
		},
	})
}
