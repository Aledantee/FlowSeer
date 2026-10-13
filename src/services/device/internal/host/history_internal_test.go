package host

import (
	"reflect"
	"testing"

	"google.golang.org/protobuf/proto"

	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/services/device/internal/history"
)

func TestTheHistorySinkIsTheLastModule(t *testing.T) {
	h := &assembly{cfg: &Config{msg: &storev1.DeviceServiceConfig{}}}

	var names []string
	for _, m := range h.modules() {
		names = append(names, m.Name)
	}

	want := []string{"hub", "forwarder", "intake", "journal", "connect", "drift", "capture_sweeper", "projector", "history_sink"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("modules = %q, want %q", names, want)
	}
}

func TestTheHistorySinkRunsOnlyWithAHistorySection(t *testing.T) {
	tests := []struct {
		name string
		msg  *storev1.DeviceServiceConfig
		want service.Gate
	}{
		{"without a history section", &storev1.DeviceServiceConfig{}, service.FixedGate(false)},
		{"with a history section", storev1.DeviceServiceConfig_builder{History: &storev1.HistoryStore{}}.Build(), service.FixedGate(true)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &assembly{cfg: &Config{msg: tt.msg}}

			modules := h.modules()
			sink := modules[len(modules)-1]

			if !reflect.DeepEqual(sink.Gate, tt.want) {
				t.Errorf("gate of %s = %+v, want %+v", sink.Name, sink.Gate, tt.want)
			}
		})
	}
}

func TestHistoryConfigMapsEveryUserToItsOwnRole(t *testing.T) {
	user := func(role string) *storev1.HistoryUser {
		return storev1.HistoryUser_builder{Name: proto.String(role + "_user"), PasswordFile: proto.String("/secrets/" + role)}.Build()
	}
	msg := storev1.HistoryStore_builder{
		Endpoints: []string{"ch1:9440", "ch2:9440"},
		Database:  proto.String("history"),
		Cluster:   proto.String("fleet"),
		Admin:     user("admin"),
		Writer:    user("writer"),
		Reader:    user("reader"),
		Analyst:   user("analyst"),
		CaFile:    proto.String("/etc/ca.pem"),
	}.Build()

	got := historyConfig(msg)

	want := history.Config{
		Endpoints: []string{"ch1:9440", "ch2:9440"},
		Database:  "history",
		Cluster:   "fleet",
		Admin:     history.User{Name: "admin_user", PasswordFile: "/secrets/admin"},
		Writer:    history.User{Name: "writer_user", PasswordFile: "/secrets/writer"},
		Reader:    history.User{Name: "reader_user", PasswordFile: "/secrets/reader"},
		Analyst:   history.User{Name: "analyst_user", PasswordFile: "/secrets/analyst"},
		CAFile:    "/etc/ca.pem",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("history config = %+v, want %+v", got, want)
	}
}
