package conformance

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/timestamppb"

	historyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/history/v1"
	authzv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/authz/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	logv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/log/v1"
)

func TestListSyslogRecordsRequestRules(t *testing.T) {
	const deviceID = "0192e6a0-0000-7000-8000-0000000000d1"
	const locationID = "0192e6a0-0000-7000-8000-0000000000e1"
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	device := inventoryv1.DeviceGlobalRef_builder{Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(deviceID)}.Build()}.Build()
	location := inventoryv1.LocationGlobalRef_builder{Location: inventoryv1.LocationLocalRef_builder{Id: proto.String(locationID)}.Build()}.Build()
	request := func(mutate func(*historyv1.ListSyslogRecordsRequest_builder)) *historyv1.ListSyslogRecordsRequest {
		b := historyv1.ListSyslogRecordsRequest_builder{
			Device: device,
			From:   timestamppb.New(from),
			To:     timestamppb.New(from.Add(24 * time.Hour)),
		}
		mutate(&b)
		return b.Build()
	}

	runValidationCases(t, []validationCase{
		{
			name:      "a device scope over a day",
			message:   request(func(*historyv1.ListSyslogRecordsRequest_builder) {}),
			wantValid: true,
		},
		{
			name: "a location scope with a severity and a page",
			message: request(func(b *historyv1.ListSyslogRecordsRequest_builder) {
				b.Device = nil
				b.Location = location
				b.MinSeverity = logv1.SyslogSeverity_SYSLOG_SEVERITY_ERROR.Enum()
				b.PageSize = proto.Int32(500)
				b.PageToken = proto.String("opaque")
			}),
			wantValid: true,
		},
		{
			name: "no scope",
			message: request(func(b *historyv1.ListSyslogRecordsRequest_builder) {
				b.Device = nil
			}),
			wantValid: false,
		},
		{
			name: "no start",
			message: request(func(b *historyv1.ListSyslogRecordsRequest_builder) {
				b.From = nil
			}),
			wantValid: false,
		},
		{
			name: "no end",
			message: request(func(b *historyv1.ListSyslogRecordsRequest_builder) {
				b.To = nil
			}),
			wantValid: false,
		},
		{
			name: "an end equal to the start",
			message: request(func(b *historyv1.ListSyslogRecordsRequest_builder) {
				b.To = timestamppb.New(from)
			}),
			wantValid: false,
		},
		{
			name: "an end before the start",
			message: request(func(b *historyv1.ListSyslogRecordsRequest_builder) {
				b.To = timestamppb.New(from.Add(-time.Hour))
			}),
			wantValid: false,
		},
		{
			name: "a window of exactly 366 days",
			message: request(func(b *historyv1.ListSyslogRecordsRequest_builder) {
				b.To = timestamppb.New(from.Add(366 * 24 * time.Hour))
			}),
			wantValid: true,
		},
		{
			name: "a window of more than 366 days",
			message: request(func(b *historyv1.ListSyslogRecordsRequest_builder) {
				b.To = timestamppb.New(from.Add(366*24*time.Hour + time.Second))
			}),
			wantValid: false,
		},
		{
			name: "a severity that is not defined",
			message: request(func(b *historyv1.ListSyslogRecordsRequest_builder) {
				b.MinSeverity = logv1.SyslogSeverity(99).Enum()
			}),
			wantValid: false,
		},
		{
			name: "a page size of zero",
			message: request(func(b *historyv1.ListSyslogRecordsRequest_builder) {
				b.PageSize = proto.Int32(0)
			}),
			wantValid: false,
		},
		{
			name: "a page size past the cap",
			message: request(func(b *historyv1.ListSyslogRecordsRequest_builder) {
				b.PageSize = proto.Int32(1001)
			}),
			wantValid: false,
		},
		{
			name: "a page token past its bound",
			message: request(func(b *historyv1.ListSyslogRecordsRequest_builder) {
				b.PageToken = proto.String(string(make([]byte, 257)))
			}),
			wantValid: false,
		},
	})
}

func TestTheHistoryReadIsTheOnlyMultiTenantMethod(t *testing.T) {
	var multi []string
	forEachAPIMethod(protoregistry.GlobalFiles, func(method protoreflect.MethodDescriptor) {
		if rule := methodRule(method); rule != nil && rule.GetMultiTenant() {
			multi = append(multi, string(method.FullName()))
			if rule.GetMode() != authzv1.RuleMode_RULE_MODE_TENANT || rule.GetRelation() != "viewer" {
				t.Errorf("%s is multi-tenant with mode %v and relation %q, want a tenant rule on viewer", method.FullName(), rule.GetMode(), rule.GetRelation())
			}
		}
	})

	if len(multi) != 1 || multi[0] != "flowseer.api.history.v1.HistoryService.ListSyslogRecords" {
		t.Errorf("multi-tenant methods = %v, want only the history read: a handler of a multi-tenant method must read tenant.SetFrom", multi)
	}
}
