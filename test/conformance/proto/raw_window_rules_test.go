package conformance

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/ingest/v1"
	edgeingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1"
	rawwindowv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/rawwindow/v1"
	agentv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/agent/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
)

const (
	rawWindowID    = "0192e6a0-0000-7000-8000-0000000000f1"
	rawWindowEpoch = "0192e6a0-0000-7000-8000-0000000000f2"
)

var rawWindowExpiry = time.Date(2026, time.October, 8, 13, 0, 0, 0, time.UTC)

func rawWindowLocalRef(id string) *rawwindowv1.RawWindowLocalRef {
	return rawwindowv1.RawWindowLocalRef_builder{Id: proto.String(id)}.Build()
}

func rawWindowGlobalRef() *rawwindowv1.RawWindowGlobalRef {
	return rawwindowv1.RawWindowGlobalRef_builder{
		Edge:      edgeRef(),
		RawWindow: rawWindowLocalRef(rawWindowID),
	}.Build()
}

func rawWindowDeviceSelector() *rawwindowv1.RawWindowSelector {
	return rawwindowv1.RawWindowSelector_builder{Device: deviceRef(deviceID)}.Build()
}

func rawWindowAuthorization() *rawwindowv1.RawWindowAuthorization {
	return rawwindowv1.RawWindowAuthorization_builder{
		RequestedBy: validOperatorRef(),
		Reason:      proto.String("intermittent parse failure on the uplink"),
	}.Build()
}

func validRawWindowConfig() rawwindowv1.RawWindowConfig_builder {
	return rawwindowv1.RawWindowConfig_builder{
		Ref:           rawWindowGlobalRef(),
		Selector:      rawWindowDeviceSelector(),
		ExpiresAt:     timestamppb.New(rawWindowExpiry),
		MaxBytes:      proto.Uint64(4096),
		Authorization: rawWindowAuthorization(),
	}
}

func validRawWindowState(lifecycle rawwindowv1.RawWindowLifecycle) rawwindowv1.RawWindowState_builder {
	return rawwindowv1.RawWindowState_builder{
		Ref:       rawWindowGlobalRef(),
		Lifecycle: lifecycle.Enum(),
	}
}

func terminalRawWindowState() rawwindowv1.RawWindowState_builder {
	b := validRawWindowState(rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL)
	b.StopReason = rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_EXPIRED.Enum()
	b.EndedAt = timestamppb.New(rawWindowExpiry)
	return b
}

func TestRawWindowSelectorRules(t *testing.T) {
	runFieldCases(t, []fieldCase{
		{name: "device scope", message: rawWindowDeviceSelector()},
		{name: "integration scope", message: rawwindowv1.RawWindowSelector_builder{Integration: integrationRef(integrationID)}.Build()},
		{
			name: "source scope",
			message: rawwindowv1.RawWindowSelector_builder{
				Source: rawwindowv1.RawWindowSource_builder{Key: proto.String("syslog")}.Build(),
			}.Build(),
		},
		{
			name:      "absent scope",
			message:   rawwindowv1.RawWindowSelector_builder{}.Build(),
			wantField: "scope",
			wantText:  "exactly one field is required",
		},
		{
			name: "source key with a dot",
			message: rawwindowv1.RawWindowSelector_builder{
				Source: rawwindowv1.RawWindowSource_builder{Key: proto.String("ingest.syslog")}.Build(),
			}.Build(),
			wantField: "source.key",
			wantText:  "does not match regex pattern",
		},
		{
			name: "source key over 32 characters",
			message: rawwindowv1.RawWindowSelector_builder{
				Source: rawwindowv1.RawWindowSource_builder{Key: proto.String(strings.Repeat("a", 33))}.Build(),
			}.Build(),
			wantField: "source.key",
			wantText:  "at most 32",
		},
		{
			name:      "source without a key",
			message:   rawwindowv1.RawWindowSelector_builder{Source: rawwindowv1.RawWindowSource_builder{}.Build()}.Build(),
			wantField: "source.key",
			wantText:  "value is required",
		},
		{
			name:      "device ref without an id",
			message:   rawwindowv1.RawWindowSelector_builder{Device: deviceRef("")}.Build(),
			wantField: "device.device.id",
		},
	})
}

func TestRawWindowConfigRules(t *testing.T) {
	without := func(edit func(*rawwindowv1.RawWindowConfig_builder)) *rawwindowv1.RawWindowConfig {
		b := validRawWindowConfig()
		edit(&b)
		return b.Build()
	}

	runFieldCases(t, []fieldCase{
		{name: "complete config", message: validRawWindowConfig().Build()},
		{name: "missing ref", message: without(func(b *rawwindowv1.RawWindowConfig_builder) { b.Ref = nil }), wantField: "ref", wantText: "value is required"},
		{name: "missing selector", message: without(func(b *rawwindowv1.RawWindowConfig_builder) { b.Selector = nil }), wantField: "selector", wantText: "value is required"},
		{name: "missing expiry", message: without(func(b *rawwindowv1.RawWindowConfig_builder) { b.ExpiresAt = nil }), wantField: "expires_at", wantText: "value is required"},
		{
			name:      "expiry at the epoch is no expiry",
			message:   without(func(b *rawwindowv1.RawWindowConfig_builder) { b.ExpiresAt = timestamppb.New(time.Unix(0, 0)) }),
			wantField: "expires_at",
			wantText:  "greater than",
		},
		{
			name:      "expiry before the epoch",
			message:   without(func(b *rawwindowv1.RawWindowConfig_builder) { b.ExpiresAt = &timestamppb.Timestamp{Seconds: -1} }),
			wantField: "expires_at",
			wantText:  "greater than",
		},
		{name: "missing byte budget", message: without(func(b *rawwindowv1.RawWindowConfig_builder) { b.MaxBytes = nil }), wantField: "max_bytes", wantText: "value is required"},
		{name: "zero byte budget", message: without(func(b *rawwindowv1.RawWindowConfig_builder) { b.MaxBytes = proto.Uint64(0) }), wantField: "max_bytes", wantText: "greater than 0"},
		{name: "missing authorization", message: without(func(b *rawwindowv1.RawWindowConfig_builder) { b.Authorization = nil }), wantField: "authorization", wantText: "value is required"},
		{
			name: "stored config without an operator",
			message: without(func(b *rawwindowv1.RawWindowConfig_builder) {
				b.Authorization = rawwindowv1.RawWindowAuthorization_builder{Reason: proto.String("why")}.Build()
			}),
			wantRule: "raw_window_config.named_operator",
		},
		{
			name: "reason over 2048 characters",
			message: without(func(b *rawwindowv1.RawWindowConfig_builder) {
				b.Authorization = rawwindowv1.RawWindowAuthorization_builder{
					RequestedBy: validOperatorRef(),
					Reason:      proto.String(strings.Repeat("a", 2049)),
				}.Build()
			}),
			wantField: "authorization.reason",
			wantText:  "at most 2048",
		},
		{
			name: "empty reason",
			message: without(func(b *rawwindowv1.RawWindowConfig_builder) {
				b.Authorization = rawwindowv1.RawWindowAuthorization_builder{RequestedBy: validOperatorRef(), Reason: proto.String("")}.Build()
			}),
			wantField: "authorization.reason",
			wantText:  "at least 1",
		},
	})
}

func TestRawWindowRefRules(t *testing.T) {
	runFieldCases(t, []fieldCase{
		{name: "complete ref", message: rawWindowGlobalRef()},
		{name: "local ref that is not a uuid", message: rawWindowLocalRef("not-a-uuid"), wantField: "id", wantText: "must be a valid UUID"},
		{name: "local ref without an id", message: rawwindowv1.RawWindowLocalRef_builder{}.Build(), wantField: "id", wantText: "value is required"},
		{
			name:      "global ref without an edge",
			message:   rawwindowv1.RawWindowGlobalRef_builder{RawWindow: rawWindowLocalRef(rawWindowID)}.Build(),
			wantField: "edge",
			wantText:  "value is required",
		},
		{
			name:      "global ref without a window",
			message:   rawwindowv1.RawWindowGlobalRef_builder{Edge: edgeRef()}.Build(),
			wantField: "raw_window",
			wantText:  "value is required",
		},
	})
}

func TestRawWindowStateRules(t *testing.T) {
	pending := validRawWindowState(rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_PENDING)
	pendingActivated := validRawWindowState(rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_PENDING)
	pendingActivated.ActivatedAt = timestamppb.Now()
	active := validRawWindowState(rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_ACTIVE)
	active.ActivatedAt = timestamppb.Now()
	active.Usage = rawwindowv1.RawWindowUsage_builder{SelectedBytes: proto.Uint64(6), SelectedRecords: proto.Uint64(1)}.Build()
	activeWithReason := active
	activeWithReason.StopReason = rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_OPERATOR.Enum()
	activeEnded := active
	activeEnded.EndedAt = timestamppb.Now()
	terminalNoReason := terminalRawWindowState()
	terminalNoReason.StopReason = nil
	terminalUnspecified := terminalRawWindowState()
	terminalUnspecified.StopReason = rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_UNSPECIFIED.Enum()
	terminalNoEnd := terminalRawWindowState()
	terminalNoEnd.EndedAt = nil
	unspecified := validRawWindowState(rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_UNSPECIFIED)
	undefined := validRawWindowState(rawwindowv1.RawWindowLifecycle(99))
	noRef := terminalRawWindowState()
	noRef.Ref = nil

	runFieldCases(t, []fieldCase{
		{name: "pending", message: pending.Build()},
		{name: "active with usage", message: active.Build()},
		{name: "terminal with reason and end", message: terminalRawWindowState().Build()},
		{name: "pending already activated", message: pendingActivated.Build(), wantRule: "raw_window_state.pending_not_activated"},
		{name: "active with a stop reason", message: activeWithReason.Build(), wantRule: "raw_window_state.terminal_has_reason"},
		{name: "active with an end time", message: activeEnded.Build(), wantRule: "raw_window_state.terminal_has_end"},
		{name: "terminal without a stop reason", message: terminalNoReason.Build(), wantRule: "raw_window_state.terminal_has_reason"},
		{name: "terminal with the unspecified stop reason", message: terminalUnspecified.Build(), wantRule: "raw_window_state.terminal_has_reason"},
		{name: "terminal without an end time", message: terminalNoEnd.Build(), wantRule: "raw_window_state.terminal_has_end"},
		{name: "unspecified lifecycle", message: unspecified.Build(), wantField: "lifecycle", wantText: "must not be in list"},
		{name: "undefined lifecycle", message: undefined.Build(), wantField: "lifecycle", wantText: "must be one of the defined enum values"},
		{name: "missing ref", message: noRef.Build(), wantField: "ref", wantText: "value is required"},
	})
}

func TestRawWindowEventRules(t *testing.T) {
	event := func(from, to rawwindowv1.RawWindowLifecycle) *rawwindowv1.RawWindowEvent {
		return rawwindowv1.RawWindowEvent_builder{Ref: rawWindowGlobalRef(), From: from.Enum(), To: to.Enum()}.Build()
	}
	creation := rawwindowv1.RawWindowEvent_builder{
		Ref: rawWindowGlobalRef(),
		To:  rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_PENDING.Enum(),
	}.Build()

	runFieldCases(t, []fieldCase{
		{name: "creation has no from", message: creation},
		{name: "activation", message: event(rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_PENDING, rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_ACTIVE)},
		{
			name:     "same lifecycle on both sides",
			message:  event(rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_ACTIVE, rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_ACTIVE),
			wantRule: "raw_window_event.lifecycle_changes",
		},
		{
			name:      "unspecified destination",
			message:   event(rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_PENDING, rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_UNSPECIFIED),
			wantField: "to",
			wantText:  "must not be in list",
		},
	})
}

func TestOpenRawWindowRequestRules(t *testing.T) {
	valid := func() apiingestv1.OpenRawWindowRequest_builder {
		return apiingestv1.OpenRawWindowRequest_builder{
			Edge:          edgeRef(),
			Selector:      rawWindowDeviceSelector(),
			ExpiresAt:     timestamppb.New(time.Now().Add(30 * time.Minute)),
			MaxBytes:      proto.Uint64(1024),
			Authorization: rawwindowv1.RawWindowAuthorization_builder{Reason: proto.String("why")}.Build(),
		}
	}
	with := func(edit func(*apiingestv1.OpenRawWindowRequest_builder)) *apiingestv1.OpenRawWindowRequest {
		b := valid()
		edit(&b)
		return b.Build()
	}

	runFieldCases(t, []fieldCase{
		{name: "request without a requester", message: valid().Build()},
		{name: "request with a spoofable requester", message: with(func(b *apiingestv1.OpenRawWindowRequest_builder) {
			b.Authorization = rawwindowv1.RawWindowAuthorization_builder{RequestedBy: validOperatorRef(), Reason: proto.String("why")}.Build()
		})},
		{name: "missing edge", message: with(func(b *apiingestv1.OpenRawWindowRequest_builder) { b.Edge = nil }), wantField: "edge", wantText: "value is required"},
		{name: "missing selector", message: with(func(b *apiingestv1.OpenRawWindowRequest_builder) { b.Selector = nil }), wantField: "selector", wantText: "value is required"},
		{name: "missing expiry", message: with(func(b *apiingestv1.OpenRawWindowRequest_builder) { b.ExpiresAt = nil }), wantField: "expires_at", wantText: "value is required"},
		{name: "expiry in the past", message: with(func(b *apiingestv1.OpenRawWindowRequest_builder) {
			b.ExpiresAt = timestamppb.New(time.Now().Add(-time.Minute))
		}), wantField: "expires_at", wantText: "greater than now"},
		{name: "missing byte budget", message: with(func(b *apiingestv1.OpenRawWindowRequest_builder) { b.MaxBytes = nil }), wantField: "max_bytes", wantText: "value is required"},
		{name: "zero byte budget", message: with(func(b *apiingestv1.OpenRawWindowRequest_builder) { b.MaxBytes = proto.Uint64(0) }), wantField: "max_bytes", wantText: "greater than 0"},
		{name: "missing authorization", message: with(func(b *apiingestv1.OpenRawWindowRequest_builder) { b.Authorization = nil }), wantField: "authorization", wantText: "value is required"},
		{
			name: "missing reason",
			message: with(func(b *apiingestv1.OpenRawWindowRequest_builder) {
				b.Authorization = rawwindowv1.RawWindowAuthorization_builder{}.Build()
			}),
			wantField: "authorization.reason",
			wantText:  "value is required",
		},
	})
}

func TestRawWindowGetAndCloseRequestRules(t *testing.T) {
	runFieldCases(t, []fieldCase{
		{name: "get by window id", message: apiingestv1.GetRawWindowRequest_builder{RawWindow: rawWindowLocalRef(rawWindowID)}.Build()},
		{name: "get without a window", message: apiingestv1.GetRawWindowRequest_builder{}.Build(), wantField: "raw_window", wantText: "value is required"},
		{name: "close by window id", message: apiingestv1.CloseRawWindowRequest_builder{RawWindow: rawWindowLocalRef(rawWindowID)}.Build()},
		{name: "close without a window", message: apiingestv1.CloseRawWindowRequest_builder{}.Build(), wantField: "raw_window", wantText: "value is required"},
		{name: "close with a malformed id", message: apiingestv1.CloseRawWindowRequest_builder{RawWindow: rawWindowLocalRef("x")}.Build(), wantField: "raw_window.id", wantText: "must be a valid UUID"},
	})
}

func TestRawWindowSubscribeRules(t *testing.T) {
	valid := func() edgeingestv1.SubscribeRequest_builder {
		return edgeingestv1.SubscribeRequest_builder{
			BootGeneration: proto.Uint64(1),
			ProcessEpoch:   proto.String(rawWindowEpoch),
			EnabledSources: []*rawwindowv1.RawWindowSource{rawwindowv1.RawWindowSource_builder{Key: proto.String("syslog")}.Build()},
		}
	}
	noGeneration := valid()
	noGeneration.BootGeneration = nil
	zeroGeneration := valid()
	zeroGeneration.BootGeneration = proto.Uint64(0)
	noEpoch := valid()
	noEpoch.ProcessEpoch = nil
	badEpoch := valid()
	badEpoch.ProcessEpoch = proto.String("epoch-1")
	noSources := valid()
	noSources.EnabledSources = nil
	manySources := valid()
	for i := range 16 {
		manySources.EnabledSources = append(manySources.EnabledSources, rawwindowv1.RawWindowSource_builder{Key: proto.String("s" + strings.Repeat("a", i))}.Build())
	}

	runFieldCases(t, []fieldCase{
		{name: "generation epoch and sources", message: valid().Build()},
		{name: "no sources enabled", message: noSources.Build()},
		{name: "missing generation", message: noGeneration.Build(), wantField: "boot_generation", wantText: "value is required"},
		{name: "zero generation", message: zeroGeneration.Build(), wantField: "boot_generation", wantText: "greater than 0"},
		{name: "missing epoch", message: noEpoch.Build(), wantField: "process_epoch", wantText: "value is required"},
		{name: "epoch that is not a uuid", message: badEpoch.Build(), wantField: "process_epoch", wantText: "must be a valid UUID"},
		{name: "more than sixteen sources", message: manySources.Build(), wantField: "enabled_sources", wantText: "no more than 16"},
	})
}

func validRawWindowControl() edgeingestv1.RawWindowControl_builder {
	return edgeingestv1.RawWindowControl_builder{
		BootGeneration: proto.Uint64(2),
		ProcessEpoch:   proto.String(rawWindowEpoch),
		Ref:            rawWindowGlobalRef(),
		Revision:       proto.Uint64(7),
		Open:           validRawWindowConfig().Build(),
	}
}

func TestRawWindowControlRules(t *testing.T) {
	closeControl := validRawWindowControl()
	closeControl.Open = nil
	closeControl.Close = edgeingestv1.RawWindowClose_builder{ExpiresAt: timestamppb.New(rawWindowExpiry)}.Build()
	closeNoExpiry := closeControl
	closeNoExpiry.Close = edgeingestv1.RawWindowClose_builder{}.Build()
	noIntent := validRawWindowControl()
	noIntent.Open = nil
	bothIntents := validRawWindowControl()
	bothIntents.Close = edgeingestv1.RawWindowClose_builder{ExpiresAt: timestamppb.New(rawWindowExpiry)}.Build()
	otherWindow := validRawWindowControl()
	otherWindow.Ref = rawwindowv1.RawWindowGlobalRef_builder{Edge: edgeRef(), RawWindow: rawWindowLocalRef("0192e6a0-0000-7000-8000-0000000000f9")}.Build()
	zeroRevision := validRawWindowControl()
	zeroRevision.Revision = proto.Uint64(0)
	noRevision := validRawWindowControl()
	noRevision.Revision = nil
	noGeneration := validRawWindowControl()
	noGeneration.BootGeneration = nil
	noEpoch := validRawWindowControl()
	noEpoch.ProcessEpoch = nil
	noRef := validRawWindowControl()
	noRef.Ref = nil

	runFieldCases(t, []fieldCase{
		{name: "open control", message: validRawWindowControl().Build()},
		{name: "close control", message: closeControl.Build()},
		{name: "close without the original expiry", message: closeNoExpiry.Build(), wantField: "close.expires_at", wantText: "value is required"},
		{name: "control with no instruction", message: noIntent.Build(), wantField: "intent", wantText: "exactly one field is required"},
		{name: "open configuration of another window", message: otherWindow.Build(), wantRule: "raw_window_control.open_matches_ref"},
		{name: "zero revision", message: zeroRevision.Build(), wantField: "revision", wantText: "greater than 0"},
		{name: "missing revision", message: noRevision.Build(), wantField: "revision", wantText: "value is required"},
		{name: "missing generation", message: noGeneration.Build(), wantField: "boot_generation", wantText: "value is required"},
		{name: "missing epoch", message: noEpoch.Build(), wantField: "process_epoch", wantText: "value is required"},
		{name: "missing ref", message: noRef.Build(), wantField: "ref", wantText: "value is required"},
	})

	// A oneof holds one arm, so setting both keeps the last one and the
	// message still validates. The case documents why the schema cannot
	// reject it and the relay never sees it.
	if got := bothIntents.Build().WhichIntent(); got != 11 {
		t.Errorf("both intents kept arm %d, want the last set (close, 11)", got)
	}
}

func TestRawWindowSubscribeResponseRules(t *testing.T) {
	control := validRawWindowControl().Build()
	runFieldCases(t, []fieldCase{
		{name: "ready frame", message: edgeingestv1.SubscribeResponse_builder{Ready: edgeingestv1.RawWindowReady_builder{}.Build()}.Build()},
		{name: "control frame", message: edgeingestv1.SubscribeResponse_builder{Control: control}.Build()},
		{name: "empty frame", message: edgeingestv1.SubscribeResponse_builder{}.Build(), wantField: "body", wantText: "exactly one field is required"},
	})
}

func TestRawWindowReportRules(t *testing.T) {
	report := func() *edgeingestv1.RawWindowReport {
		return edgeingestv1.RawWindowReport_builder{Revision: proto.Uint64(3), State: terminalRawWindowState().Build()}.Build()
	}
	request := func(reports ...*edgeingestv1.RawWindowReport) *edgeingestv1.ReportRequest {
		return edgeingestv1.ReportRequest_builder{
			BootGeneration: proto.Uint64(2),
			ProcessEpoch:   proto.String(rawWindowEpoch),
			Reports:        reports,
		}.Build()
	}
	seventeen := make([]*edgeingestv1.RawWindowReport, 17)
	for i := range seventeen {
		seventeen[i] = report()
	}

	runFieldCases(t, []fieldCase{
		{name: "one report", message: request(report())},
		{name: "no reports", message: request(), wantField: "reports", wantText: "at least 1"},
		{name: "seventeen reports", message: request(seventeen...), wantField: "reports", wantText: "no more than 16"},
		{
			name:      "report without a revision",
			message:   request(edgeingestv1.RawWindowReport_builder{State: terminalRawWindowState().Build()}.Build()),
			wantField: "reports[0].revision",
			wantText:  "value is required",
		},
		{
			name:      "report without a state",
			message:   request(edgeingestv1.RawWindowReport_builder{Revision: proto.Uint64(1)}.Build()),
			wantField: "reports[0].state",
			wantText:  "value is required",
		},
		{
			name:     "report carrying an inconsistent state",
			message:  request(edgeingestv1.RawWindowReport_builder{Revision: proto.Uint64(1), State: validRawWindowState(rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL).Build()}.Build()),
			wantRule: "raw_window_state.terminal_has_reason",
		},
	})
}

func TestRawWindowEdgeRecordRules(t *testing.T) {
	entry := func() *storev1.RawWindowEntry {
		return storev1.RawWindowEntry_builder{
			Config:   validRawWindowConfig().Build(),
			State:    validRawWindowState(rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_PENDING).Build(),
			Revision: proto.Uint64(1),
		}.Build()
	}
	record := func() storev1.RawWindowEdgeRecord_builder {
		return storev1.RawWindowEdgeRecord_builder{
			BootGeneration:  proto.Uint64(1),
			ProcessEpoch:    proto.String(rawWindowEpoch),
			ControlRevision: proto.Uint64(1),
			Windows:         []*storev1.RawWindowEntry{entry()},
		}
	}
	noGeneration := record()
	noGeneration.BootGeneration = nil
	noEpoch := record()
	noEpoch.ProcessEpoch = nil
	tooMany := record()
	tooMany.Windows = make([]*storev1.RawWindowEntry, 81)
	for i := range tooMany.Windows {
		tooMany.Windows[i] = entry()
	}
	noRevision := storev1.RawWindowEntry_builder{
		Config: validRawWindowConfig().Build(),
		State:  validRawWindowState(rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_PENDING).Build(),
	}.Build()

	runFieldCases(t, []fieldCase{
		{name: "record with one window", message: record().Build()},
		{name: "record before any window", message: storev1.RawWindowEdgeRecord_builder{BootGeneration: proto.Uint64(1), ProcessEpoch: proto.String(rawWindowEpoch), ControlRevision: proto.Uint64(0)}.Build()},
		{name: "missing generation", message: noGeneration.Build(), wantField: "boot_generation", wantText: "value is required"},
		{name: "missing epoch", message: noEpoch.Build(), wantField: "process_epoch", wantText: "value is required"},
		{name: "more than eighty rows", message: tooMany.Build(), wantField: "windows", wantText: "no more than 80"},
		{name: "entry without a revision", message: noRevision, wantField: "revision", wantText: "value is required"},
	})
}

func TestRawWindowEpochRules(t *testing.T) {
	runFieldCases(t, []fieldCase{
		{name: "first generation", message: agentv1.RawWindowEpoch_builder{BootGeneration: proto.Uint64(1)}.Build()},
		{name: "generation with epoch", message: agentv1.RawWindowEpoch_builder{BootGeneration: proto.Uint64(9), ProcessEpoch: proto.String(rawWindowEpoch)}.Build()},
		{name: "missing generation", message: agentv1.RawWindowEpoch_builder{}.Build(), wantField: "boot_generation", wantText: "value is required"},
		{name: "zero generation", message: agentv1.RawWindowEpoch_builder{BootGeneration: proto.Uint64(0)}.Build(), wantField: "boot_generation", wantText: "greater than 0"},
		{name: "epoch that is not a uuid", message: agentv1.RawWindowEpoch_builder{BootGeneration: proto.Uint64(1), ProcessEpoch: proto.String("x")}.Build(), wantField: "process_epoch", wantText: "must be a valid UUID"},
	})
}
