package bridge_test

import (
	"reflect"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestDeferredLearningMatchesIngress(t *testing.T) {
	cases := []struct {
		name                           string
		learns, forwards, full, tunnel bool
	}{
		{"learning", true, true, false, false},
		{"no learning", false, true, false, false},
		{"learning only", true, false, false, false},
		{"full database", true, true, true, false},
		{"tunnel", true, true, false, true},
	}
	concrete := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := gateVLANConfig()
			if tc.full {
				cfg.MaxEntries = 1
			}
			if tc.tunnel {
				cfg.VLAN.Switchports["1/1/1"] = bridge.Switchport{Tunnel: &bridge.Tunnel{VID: 10}}
			}
			newRelay := func() *bridge.Layer {
				b := mustNewBridge(t, cfg, buildTestPorts(t, 2))
				b.SetFDBScope(analysis.ProtocolScope("sw", "relay", ""))
				b.SetGate(testGate{
					learns:   map[string]bool{"1/1/1": tc.learns},
					forwards: map[string]bool{"1/1/1": tc.forwards},
				}, analysis.ProtocolScope("sw", "gate", ""))
				if tc.full {
					mustLearn(t, b, []bridge.Seed{{FID: 10, MAC: macC, Port: "1/1/2", LearnedAt: testTime0}})
				}
				return b
			}
			deferred, direct := newRelay(), newRelay()
			frame := ethernet.Frame{Src: macA, Dst: macB}
			in, refused, admitted := deferred.Ingress(testTime0, "1/1/1", frame, false, true)
			want, wantRefused, wantAdmitted := direct.Ingress(testTime0, "1/1/1", frame, true, true)
			if admitted != tc.forwards || wantAdmitted != tc.forwards {
				t.Fatalf("admission = %v/%v, want %v", admitted, wantAdmitted, tc.forwards)
			}
			if !tc.forwards {
				if refused.Reason != bridge.ReasonPortBlocked || wantRefused.Reason != bridge.ReasonPortBlocked || len(direct.Entries()) != 1 || direct.Entries()[0].MAC != macA || direct.Counters().Learned != 1 {
					t.Fatalf("learning-only ingress = %+v/%+v, want learned source then refusal", wantRefused, direct.Entries())
				}
				concrete++
				return
			}
			in = deferred.Learn(in, frame)
			if in.Port != want.Port || in.FID != want.FID || !reflect.DeepEqual(in.Steps, want.Steps) ||
				!reflect.DeepEqual(in.ConsultedPorts(), want.ConsultedPorts()) || !reflect.DeepEqual(in.ConsultedScopes(), want.ConsultedScopes()) ||
				!reflect.DeepEqual(deferred.Entries(), direct.Entries()) || deferred.Counters() != direct.Counters() {
				t.Fatalf("deferred/direct = %+v/%+v, entries %+v/%+v, counters %+v/%+v", in, want, deferred.Entries(), direct.Entries(), deferred.Counters(), direct.Counters())
			}
			wantEntries := 1
			if !tc.learns {
				wantEntries = 0
			}
			if len(deferred.Entries()) != wantEntries || tc.learns && deferred.Entries()[0].MAC != macA {
				t.Fatalf("entries = %+v, want %d entries with the learned source", deferred.Entries(), wantEntries)
			}
			if tc.learns && !sawLearn(in.Steps) {
				t.Fatal("missing learning step")
			}
			concrete++
		})
	}
	if concrete != 5 {
		t.Fatalf("concrete cases = %d, want 5", concrete)
	}
}

func sawLearn(steps []trace.Step) bool {
	for _, step := range steps {
		if step.Op == trace.OpLearn {
			return true
		}
	}
	return false
}
