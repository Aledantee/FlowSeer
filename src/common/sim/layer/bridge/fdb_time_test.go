package bridge_test

import (
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestFDBFrameTimeMatchesPriorAdvance(t *testing.T) {
	now := testTime0.Add(301 * time.Second)
	worlds := [][]bridge.Seed{nil}
	previous := worlds
	for _, mac := range []netaddr.MAC{macA, macC} {
		var next [][]bridge.Seed
		for _, seeds := range previous {
			for _, lifetime := range []bridge.Lifetime{bridge.Aging, bridge.Static} {
				for _, age := range []time.Duration{299 * time.Second, 300 * time.Second, 301 * time.Second} {
					row := append([]bridge.Seed(nil), seeds...)
					row = append(row, bridge.Seed{MAC: mac, Port: "1/1/1", Lifetime: lifetime, LearnedAt: now.Add(-age)})
					next = append(next, row)
				}
			}
		}
		worlds = append(worlds, next...)
		previous = next
	}
	ports := buildTestPorts(t, 3)
	count, concreteCount, boundaryCount := 0, 0, 0
	for _, maxEntries := range []int{0, 1, 2} {
		for world, seeds := range worlds {
			for layout := range 32 {
				count++
				t.Run(fmt.Sprintf("max=%d/world=%d/layout=%d", maxEntries, world, layout), func(t *testing.T) {
					f := ethernet.Frame{Src: macA, Dst: macC}
					if layout&1 != 0 {
						f.Src = macB
					}
					ingress := "1/1/1"
					if layout&2 != 0 {
						ingress = "1/1/2"
					}
					learn, split := layout&4 != 0, layout&8 != 0
					if layout&16 != 0 {
						f.Dst = macD
					}
					b := mustNewBridge(t, bridge.Config{MaxEntries: maxEntries}, ports)
					mustLearn(t, b, seeds)
					aged := b.Clone()
					aged.Advance(now)
					var results [2]bridge.Result
					for i, relay := range []*bridge.Layer{b, aged} {
						entries, counters := relay.Entries(), relay.Counters()
						if split {
							in, res, ok := relay.Ingress(now, ingress, f, learn, true)
							if !ok {
								t.Fatalf("Ingress refused: %+v", res)
							}
							results[i] = relay.Egress(in, f)
						} else {
							if !learn {
								relay.SetGate(testGate{learns: map[string]bool{}}, analysis.WholeScope())
							}
							results[i] = relay.Forward(now, ingress, f)
						}
						if !learn && (!reflect.DeepEqual(relay.Entries(), entries) || relay.Counters() != counters) {
							t.Fatalf("relay %d non-learning call changed entries or counters", i)
						}
					}
					got, want := results[0], results[1]
					if got.Outcome != want.Outcome || got.Reason != want.Reason ||
						!reflect.DeepEqual(got.Steps, want.Steps) || !reflect.DeepEqual(got.Egress, want.Egress) {
						t.Errorf("Result = %+v, want prior Advance %+v", got, want)
					}
					b.Advance(now)
					aged.Advance(now)
					if !reflect.DeepEqual(b.Entries(), aged.Entries()) || b.Counters() != aged.Counters() {
						t.Errorf("state = %+v / %+v, want prior Advance %+v / %+v", b.Entries(), b.Counters(), aged.Entries(), aged.Counters())
					}
					if maxEntries == 1 && len(seeds) == 1 && seeds[0].Lifetime == bridge.Aging &&
						seeds[0].LearnedAt.Equal(testTime0) && f.Src == macB && f.Dst == macC && ingress == "1/1/2" && learn {
						concreteCount++
						wantEntries := []bridge.Entry{{MAC: macB, Port: "1/1/2", Origin: bridge.Observed, Lifetime: bridge.Aging, LearnedAt: now}}
						wantCounters := bridge.Counters{Learned: 2, Expired: 1}
						if !reflect.DeepEqual(b.Entries(), wantEntries) || b.Counters() != wantCounters {
							t.Errorf("expired seed state = %+v / %+v, want %+v / %+v", b.Entries(), b.Counters(), wantEntries, wantCounters)
						}
						var rules []trace.RuleID
						for _, step := range got.Steps {
							rules = append(rules, step.RuleID)
						}
						wantRules := []trace.RuleID{bridge.RuleDefaultVLAN, bridge.RuleLearn, bridge.RuleUnicastMiss, bridge.RuleFlood, bridge.RuleTransmit, bridge.RuleTransmit}
						var outPorts []string
						for _, out := range got.Egress {
							if out.Dropped != "" || !reflect.DeepEqual(out.Frame, f) {
								t.Errorf("Egress = %+v, want unchanged frame without drop", out)
							}
							outPorts = append(outPorts, out.Port)
						}
						if got.Outcome != trace.Flooded || got.Reason != "" || !reflect.DeepEqual(rules, wantRules) ||
							!reflect.DeepEqual(outPorts, []string{"1/1/1", "1/1/3"}) {
							t.Errorf("expired seed Result = %+v, want Flooded, rules %v, ports [1/1/1 1/1/3]", got, wantRules)
						}
					}
					if maxEntries == 0 && len(seeds) == 2 && seeds[0].Lifetime == bridge.Static && now.Sub(seeds[0].LearnedAt) == 299*time.Second &&
						seeds[1].MAC == macC && seeds[1].Lifetime == bridge.Aging && now.Sub(seeds[1].LearnedAt) == 300*time.Second && f.Src == macB && f.Dst == macC && ingress == "1/1/2" && !learn {
						boundaryCount++
						for i, res := range results {
							if res.Outcome != trace.Forwarded || res.Reason != "" || len(res.Egress) != 1 || res.Egress[0].Port != "1/1/1" || res.Egress[0].Dropped != "" {
								t.Errorf("relay %d at aging boundary = %+v, want unicast hit on 1/1/1", i, res)
							}
						}
						for i, relay := range []*bridge.Layer{b, aged} {
							if len(relay.Entries()) != 2 || relay.Entries()[1].MAC != macC || relay.Counters() != (bridge.Counters{Learned: 1}) {
								t.Errorf("relay %d Advance at boundary = %+v / %+v, want retained seeds and Learned:1", i, relay.Entries(), relay.Counters())
							}
						}
					}
					f.Src = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x99}
					for i, relay := range []*bridge.Layer{b, aged} {
						relay.SetGate(nil, analysis.WholeScope())
						results[i] = relay.Forward(now, ingress, f)
					}
					if !reflect.DeepEqual(results[0], results[1]) || !reflect.DeepEqual(b.Entries(), aged.Entries()) || b.Counters() != aged.Counters() {
						t.Errorf("next learning frame = %+v / %+v / %+v, want prior Advance %+v / %+v / %+v", results[0], b.Entries(), b.Counters(), results[1], aged.Entries(), aged.Counters())
					}
				})
			}
		}
	}
	if count != 4128 {
		t.Errorf("ran %d cases, want 4128", count)
	}
	if concreteCount != 2 {
		t.Errorf("concrete expectations ran %d times, want 2", concreteCount)
	}
	if boundaryCount != 2 {
		t.Errorf("boundary expectations ran %d times, want 2", boundaryCount)
	}
}

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
			err := b.InstallSeeds([]bridge.Seed{{MAC: macA, Port: "1/1/1", Origin: tc.origin, Lifetime: tc.lifetime, LearnedAt: testTime0}})
			if tc.field != "" {
				if got := errs.Attributes(err)["field"]; err == nil || got != tc.field {
					t.Fatalf("InstallSeeds error = %v, field = %v, want %s", err, got, tc.field)
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
		for _, age := range []time.Duration{299 * time.Second, 300 * time.Second, 301 * time.Second} {
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
	for _, mode := range []string{"empty", "other-port", "tunnel"} {
		t.Run(mode, func(t *testing.T) {
			member := mode != "empty"
			other := bridge.Switchport{}
			if mode == "other-port" {
				other.Tagged = []vlan.ID{10}
			}
			if mode == "tunnel" {
				other.Tunnel = &bridge.Tunnel{VID: 10}
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
