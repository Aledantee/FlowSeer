package bridge_test

import (
	"reflect"
	"strconv"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
)

func TestAgingSeedPreservesStaticEntry(t *testing.T) {
	b := mustNewBridge(t, gateVLANConfig(), buildTestPorts(t, 2))
	mustLearn(t, b, []bridge.Seed{{FID: 10, MAC: macA, Port: "1/1/1", Lifetime: bridge.Static, LearnedAt: testTime0}})
	want := b.Entries()
	mustLearn(t, b, []bridge.Seed{{FID: 10, MAC: macA, Port: "1/1/2", LearnedAt: testTime0}})
	b.Advance(testTime0.Add(301 * time.Second))
	if got := b.Entries(); !reflect.DeepEqual(got, want) {
		t.Errorf("Entries = %+v, want static %+v", got, want)
	}
	if got := b.Counters(); got != (bridge.Counters{}) {
		t.Errorf("Counters = %+v, want zero", got)
	}
}

func TestSeedNamesAndRefusals(t *testing.T) {
	for _, tc := range []struct {
		name     string
		origin   bridge.Origin
		lifetime bridge.Lifetime
		field    string
	}{
		{name: "defaults"},
		{name: "origin", origin: "learned", field: "seeds.0.origin"},
		{name: "lifetime", lifetime: "permanent", field: "seeds.0.lifetime"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := mustNewBridge(t, bridge.Config{}, buildTestPorts(t, 2))
			err := b.Learn([]bridge.Seed{{MAC: macA, Port: "1/1/1", Origin: tc.origin, Lifetime: tc.lifetime, LearnedAt: testTime0}})
			if tc.field != "" {
				if got := errs.Attributes(err)["field"]; err == nil || got != tc.field {
					t.Fatalf("Learn error = %v, field = %v, want %s", err, got, tc.field)
				}
				if len(b.Entries()) != 0 || b.Counters() != (bridge.Counters{}) {
					t.Fatal("refused seed changed table or counters")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			b.Ingress(testTime0, "1/1/2", ethernet.Frame{Src: macB, Dst: macC}, true, true)
			want := []bridge.Entry{
				{MAC: macA, Port: "1/1/1", Origin: "configured", Lifetime: "aging", LearnedAt: testTime0},
				{MAC: macB, Port: "1/1/2", Origin: "observed", Lifetime: "aging", LearnedAt: testTime0},
			}
			if got := b.Entries(); !reflect.DeepEqual(got, want) {
				t.Errorf("Entries = %+v, want %+v", got, want)
			}
		})
	}
}

func TestDestinationLookupAtFrameTime(t *testing.T) {
	for _, priorAdvance := range []bool{false, true} {
		for _, age := range []time.Duration{299 * time.Second, 301 * time.Second} {
			t.Run(age.String()+"/advance="+strconv.FormatBool(priorAdvance), func(t *testing.T) {
				b := mustNewBridge(t, bridge.Config{}, buildTestPorts(t, 3))
				mustLearn(t, b, []bridge.Seed{{MAC: macA, Port: "1/1/1", LearnedAt: testTime0}})
				now := testTime0.Add(age)
				if priorAdvance {
					b.Advance(now)
				}
				entries, counters := b.Entries(), b.Counters()
				f := ethernet.Frame{Src: macB, Dst: macA}
				in, res, ok := b.Ingress(now, "1/1/2", f, false, false)
				if !ok {
					t.Fatalf("Ingress refused: %+v", res)
				}
				res = b.Egress(in, f)
				wantRule, wantPorts := bridge.RuleUnicastHit, []string{"1/1/1"}
				if age > 300*time.Second {
					wantRule, wantPorts = bridge.RuleUnicastMiss, []string{"1/1/1", "1/1/3"}
				}
				found := false
				for _, step := range res.Steps {
					if step.RuleID == wantRule {
						found = true
					}
				}
				var gotPorts []string
				for _, out := range res.Egress {
					if out.Dropped != "" {
						t.Errorf("Egress dropped: %+v", out)
					}
					gotPorts = append(gotPorts, out.Port)
				}
				if !found || !reflect.DeepEqual(gotPorts, wantPorts) {
					t.Errorf("Egress = %+v, want rule %s and ports %v", res, wantRule, wantPorts)
				}
				if !reflect.DeepEqual(b.Entries(), entries) || b.Counters() != counters {
					t.Fatal("read-only forwarding changed table or counters")
				}
				forward := b.Forward(now, "1/1/2", f)
				var forwardPorts []string
				for _, out := range forward.Egress {
					forwardPorts = append(forwardPorts, out.Port)
				}
				if !reflect.DeepEqual(forwardPorts, wantPorts) {
					t.Errorf("Forward ports = %v, want %v", forwardPorts, wantPorts)
				}
			})
		}
	}
}

func TestSourceLearningAtFrameTime(t *testing.T) {
	for _, priorAdvance := range []bool{false, true} {
		for _, learn := range []bool{false, true} {
			t.Run(map[bool]string{false: "unaged", true: "aged"}[priorAdvance]+"/"+map[bool]string{false: "read", true: "learn"}[learn], func(t *testing.T) {
				b := mustNewBridge(t, bridge.Config{MaxEntries: 1}, buildTestPorts(t, 2))
				mustLearn(t, b, []bridge.Seed{{MAC: macA, Port: "1/1/1", LearnedAt: testTime0}})
				now := testTime0.Add(301 * time.Second)
				if priorAdvance {
					b.Advance(now)
				}
				entries, counters := b.Entries(), b.Counters()
				_, res, ok := b.Ingress(now, "1/1/2", ethernet.Frame{Src: macA, Dst: macB}, learn, true)
				if !ok {
					t.Fatalf("Ingress refused: %+v", res)
				}
				if !learn {
					if !reflect.DeepEqual(b.Entries(), entries) || b.Counters() != counters {
						t.Fatal("non-learning call changed table or counters")
					}
					return
				}
				want := []bridge.Entry{{MAC: macA, Port: "1/1/2", Origin: bridge.Observed, Lifetime: bridge.Aging, LearnedAt: now}}
				if got := b.Entries(); !reflect.DeepEqual(got, want) {
					t.Errorf("Entries = %+v, want %+v", got, want)
				}
				if got := b.Counters(); got != (bridge.Counters{Learned: 2, Expired: 1}) {
					t.Errorf("Counters = %+v, want Learned:2 Expired:1", got)
				}
			})
		}
	}
}

func TestLearningRequiresVLANMemberSet(t *testing.T) {
	for _, member := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "other-port"}[member], func(t *testing.T) {
			other := bridge.Switchport{}
			if member {
				other.Tagged = []vlan.ID{10}
			}
			b := mustNewBridge(t, bridge.Config{VLAN: &bridge.VLAN{
				Table:       map[vlan.ID]string{10: "ten"},
				Switchports: map[string]bridge.Switchport{"1/1/1": {PVID: mustVLAN(10)}, "1/1/2": other},
			}}, buildTestPorts(t, 2))
			_, res, ok := b.Ingress(testTime0, "1/1/1", ethernet.Frame{Src: macA, Dst: macB}, true, true)
			if !ok {
				t.Fatalf("Ingress refused: %+v", res)
			}
			want := []bridge.Entry{}
			var counters bridge.Counters
			if member {
				want = append(want, bridge.Entry{FID: 10, MAC: macA, Port: "1/1/1", Origin: bridge.Observed, Lifetime: bridge.Aging, LearnedAt: testTime0})
				counters.Learned = 1
			}
			if got := b.Entries(); !reflect.DeepEqual(got, want) {
				t.Errorf("Entries = %+v, want %+v", got, want)
			}
			if got := b.Counters(); got != counters {
				t.Errorf("Counters = %+v, want %+v", got, counters)
			}
		})
	}
}
