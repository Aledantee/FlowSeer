package conformance

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	alarmv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/alarm/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
)

func alarmRef(res *alarmv1.AlarmResource, typeID string, qualifier ...string) *alarmv1.AlarmGlobalRef {
	loc := alarmv1.AlarmLocalRef_builder{
		Resource: res,
		TypeId:   proto.String(typeID),
	}
	if len(qualifier) > 0 && qualifier[0] != "" {
		loc.TypeQualifier = proto.String(qualifier[0])
	}
	return alarmv1.AlarmGlobalRef_builder{
		Device: deviceRef(deviceID),
		Alarm:  loc.Build(),
	}.Build()
}

func deviceAlarmResource() *alarmv1.AlarmResource {
	return alarmv1.AlarmResource_builder{
		Device: alarmv1.WholeDevice_builder{}.Build(),
	}.Build()
}

func componentAlarmResource(name string) *alarmv1.AlarmResource {
	return alarmv1.AlarmResource_builder{
		Component: inventoryv1.ComponentLocalRef_builder{Name: proto.String(name)}.Build(),
	}.Build()
}

func interfaceAlarmResource(name string) *alarmv1.AlarmResource {
	return alarmv1.AlarmResource_builder{
		InterfaceName: proto.String(name),
	}.Build()
}

func otherAlarmResource(other string) *alarmv1.AlarmResource {
	return alarmv1.AlarmResource_builder{
		Other: proto.String(other),
	}.Build()
}

func TestAlarmStateRules(t *testing.T) {
	validRef := alarmRef(deviceAlarmResource(), "power-loss")

	stateWith := func(modify func(b *alarmv1.AlarmState_builder)) *alarmv1.AlarmState {
		b := alarmv1.AlarmState_builder{
			Ref:      validRef,
			Severity: alarmv1.AlarmSeverity_ALARM_SEVERITY_MAJOR.Enum(),
			Cleared:  proto.Bool(false),
			Text:     proto.String("power supply unit lost AC feed"),
		}
		modify(&b)
		return b.Build()
	}

	runFieldCases(t, []fieldCase{
		{
			name:    "whole device resource passes",
			message: stateWith(func(_ *alarmv1.AlarmState_builder) {}),
		},
		{
			name: "component resource passes",
			message: stateWith(func(b *alarmv1.AlarmState_builder) {
				b.Ref = alarmRef(componentAlarmResource("PSU 1"), "voltage-low")
			}),
		},
		{
			name: "interface resource passes",
			message: stateWith(func(b *alarmv1.AlarmState_builder) {
				b.Ref = alarmRef(interfaceAlarmResource("GigabitEthernet1/0/1"), "link-down")
			}),
		},
		{
			name: "other resource passes",
			message: stateWith(func(b *alarmv1.AlarmState_builder) {
				b.Ref = alarmRef(otherAlarmResource("bgp peer 192.0.2.1"), "session-down")
			}),
		},
		{
			name: "missing ref fails",
			message: stateWith(func(b *alarmv1.AlarmState_builder) {
				b.Ref = nil
			}),
			wantField: "ref",
			wantText:  "value is required",
		},
		{
			name: "missing ref.device fails",
			message: stateWith(func(b *alarmv1.AlarmState_builder) {
				b.Ref = alarmv1.AlarmGlobalRef_builder{
					Alarm: validRef.GetAlarm(),
				}.Build()
			}),
			wantField: "ref.device",
			wantText:  "value is required",
		},
		{
			name: "empty resource target fails oneof requirement",
			message: stateWith(func(b *alarmv1.AlarmState_builder) {
				b.Ref = alarmRef(alarmv1.AlarmResource_builder{}.Build(), "power-loss")
			}),
			wantField: "ref.alarm.resource.target",
			wantText:  "exactly one field is required",
		},
		{
			name: "empty interface_name fails key rule",
			message: stateWith(func(b *alarmv1.AlarmState_builder) {
				b.Ref = alarmRef(interfaceAlarmResource(""), "link-down")
			}),
			wantField: "ref.alarm.resource.interface_name",
			wantText:  "value must be an interface name of 1 to 255 characters",
		},
		{
			name: "missing type_id fails",
			message: stateWith(func(b *alarmv1.AlarmState_builder) {
				b.Ref = alarmv1.AlarmGlobalRef_builder{
					Device: deviceRef(deviceID),
					Alarm: alarmv1.AlarmLocalRef_builder{
						Resource: deviceAlarmResource(),
					}.Build(),
				}.Build()
			}),
			wantField: "ref.alarm.type_id",
			wantText:  "value is required",
		},
		{
			name: "missing cleared fails",
			message: stateWith(func(b *alarmv1.AlarmState_builder) {
				b.Cleared = nil
			}),
			wantField: "cleared",
			wantText:  "value is required",
		},
		{
			name: "severity 0 fails not_in rule",
			message: stateWith(func(b *alarmv1.AlarmState_builder) {
				b.Severity = alarmv1.AlarmSeverity_ALARM_SEVERITY_UNSPECIFIED.Enum()
			}),
			wantField: "severity",
			wantText:  "must not be in list",
		},
		{
			name: "optional severity unset passes",
			message: stateWith(func(b *alarmv1.AlarmState_builder) {
				b.Severity = nil
			}),
		},
	})
}

func TestAlarmEventRules(t *testing.T) {
	ref1 := alarmRef(interfaceAlarmResource("GigabitEthernet1/0/1"), "link-down", "port-1")
	refDifferentQualifier := alarmRef(interfaceAlarmResource("GigabitEthernet1/0/1"), "link-down", "port-2")

	beforeUncleared := alarmv1.AlarmState_builder{
		Ref:       ref1,
		Severity:  alarmv1.AlarmSeverity_ALARM_SEVERITY_MAJOR.Enum(),
		Cleared:   proto.Bool(false),
		Text:      proto.String("link is down"),
		CreatedAt: timestamppb.New(time.Now().Add(-10 * time.Minute)),
	}.Build()

	afterCleared := alarmv1.AlarmState_builder{
		Ref:          ref1,
		Severity:     alarmv1.AlarmSeverity_ALARM_SEVERITY_MAJOR.Enum(),
		Cleared:      proto.Bool(true),
		Text:         proto.String("link is down"),
		LastRaisedAt: timestamppb.Now(),
	}.Build()

	runFieldCases(t, []fieldCase{
		{
			name: "raise event with after only passes",
			message: alarmv1.AlarmEvent_builder{
				Ref:   ref1,
				After: beforeUncleared,
			}.Build(),
		},
		{
			name: "clear event with before uncleared and after cleared passes",
			message: alarmv1.AlarmEvent_builder{
				Ref:    ref1,
				Before: beforeUncleared,
				After:  afterCleared,
			}.Build(),
		},
		{
			name: "neither side set fails one_side rule",
			message: alarmv1.AlarmEvent_builder{
				Ref: ref1,
			}.Build(),
			wantRule: "alarm_event.one_side",
		},
		{
			name: "before ref with mismatched qualifier fails before_matches_ref rule",
			message: alarmv1.AlarmEvent_builder{
				Ref: ref1,
				Before: alarmv1.AlarmState_builder{
					Ref:      refDifferentQualifier,
					Severity: alarmv1.AlarmSeverity_ALARM_SEVERITY_MAJOR.Enum(),
					Cleared:  proto.Bool(false),
				}.Build(),
			}.Build(),
			wantRule: "alarm_event.before_matches_ref",
		},
		{
			name: "after ref with mismatched qualifier fails after_matches_ref rule",
			message: alarmv1.AlarmEvent_builder{
				Ref: ref1,
				After: alarmv1.AlarmState_builder{
					Ref:      refDifferentQualifier,
					Severity: alarmv1.AlarmSeverity_ALARM_SEVERITY_MAJOR.Enum(),
					Cleared:  proto.Bool(true),
				}.Build(),
			}.Build(),
			wantRule: "alarm_event.after_matches_ref",
		},
	})
}
