package fabric

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

type originClassificationRule struct {
	requireOf    bool
	forbidMirror bool
}

var originClassificationTable = map[JourneyOriginKind]originClassificationRule{
	OriginInjection: {requireOf: false, forbidMirror: true},
	OriginMirror:    {requireOf: true, forbidMirror: false},
	OriginRelease:   {requireOf: true, forbidMirror: true},
}

var allDeclaredOriginKinds = []JourneyOriginKind{
	OriginInjection,
	OriginMirror,
	OriginRelease,
}

func TestJourneyOriginKindClassificationGate(t *testing.T) {
	t.Parallel()

	// 1. Check that every declared kind appears in the classification table and no extra entries exist.
	if len(originClassificationTable) != len(allDeclaredOriginKinds) {
		t.Fatalf("originClassificationTable has %d entries, want %d", len(originClassificationTable), len(allDeclaredOriginKinds))
	}
	for _, kind := range allDeclaredOriginKinds {
		if _, ok := originClassificationTable[kind]; !ok {
			t.Fatalf("originClassificationTable is missing entry for declared kind %q", kind)
		}
	}

	// 2. Validate that Of and Mirror requirements are enforced according to the classification table.
	for kind, rule := range originClassificationTable {
		kind, rule := kind, rule
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			// Check valid baseline according to rule
			validOrigin := JourneyOrigin{Kind: kind}
			if rule.requireOf {
				validOrigin.Of = 42
			}
			if err := validOrigin.Validate(); err != nil {
				t.Errorf("valid baseline for %s failed Validate: %v", kind, err)
			}

			// Check Of violations
			if rule.requireOf {
				invalid := validOrigin
				invalid.Of = 0
				if err := invalid.Validate(); err == nil {
					t.Errorf("%s with Of=0 passed Validate, want error (requireOf=true)", kind)
				}
			} else {
				invalid := validOrigin
				invalid.Of = 42
				if err := invalid.Validate(); err == nil {
					t.Errorf("%s with Of=42 passed Validate, want error (requireOf=false)", kind)
				}
			}

			// Check Mirror violations
			if rule.forbidMirror {
				invalid := validOrigin
				invalid.Mirror = "unexpected-mirror"
				if err := invalid.Validate(); err == nil {
					t.Errorf("%s with Mirror set passed Validate, want error (forbidMirror=true)", kind)
				}
			} else {
				withMirror := validOrigin
				withMirror.Mirror = "span-session"
				if err := withMirror.Validate(); err != nil {
					t.Errorf("%s with Mirror set failed Validate: %v", kind, err)
				}
				withoutMirror := validOrigin
				withoutMirror.Mirror = ""
				if err := withoutMirror.Validate(); err != nil {
					t.Errorf("%s with Mirror unset failed Validate: %v", kind, err)
				}
			}
		})
	}

	// 3. Check that an unknown kind returns an error.
	unknown := JourneyOrigin{Kind: JourneyOriginKind("UnknownKind")}
	if err := unknown.Validate(); err == nil {
		t.Errorf("Validate on unknown kind returned nil, want error")
	}
}

func TestJourneyStatePrecedenceTable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		pending   bool
		released  bool
		entries   []Entry
		issues    []analysis.Issue
		wantState JourneyState
	}{
		// 8 individual state cases
		{
			name:      "individual_pending",
			pending:   true,
			wantState: JourneyPending,
		},
		{
			name: "individual_unresolved",
			entries: []Entry{
				{Kind: EntryUnresolved, Reason: "test-unresolved"},
			},
			wantState: JourneyUnresolved,
		},
		{
			name: "individual_looped",
			entries: []Entry{
				{Kind: EntryLoop},
			},
			wantState: JourneyLooped,
		},
		{
			name: "individual_truncated_issue",
			issues: []analysis.Issue{
				{Code: IssueTruncatedRecord},
			},
			wantState: JourneyTruncated,
		},
		{
			name: "individual_truncated_reason",
			entries: []Entry{
				{Reason: ReasonTruncatedRecord},
			},
			wantState: JourneyTruncated,
		},
		{
			name:      "individual_released",
			released:  true,
			wantState: JourneyReleased,
		},
		{
			name: "individual_delivered_entry",
			entries: []Entry{
				{Kind: EntryDelivery},
			},
			wantState: JourneyDelivered,
		},
		{
			name: "individual_delivered_reflection",
			entries: []Entry{
				{Kind: EntryReflection},
			},
			wantState: JourneyDelivered,
		},
		{
			name: "individual_rejected",
			entries: []Entry{
				{Kind: EntryRejection, Reason: "mac-mismatch"},
			},
			wantState: JourneyRejected,
		},
		{
			name: "individual_dropped_entry",
			entries: []Entry{
				{Kind: EntryDrop, Reason: ReasonBadFrame},
			},
			wantState: JourneyDropped,
		},
		{
			name: "individual_dropped_loss",
			entries: []Entry{
				{Kind: EntryLoss, Reason: ReasonCableLoss},
			},
			wantState: JourneyDropped,
		},

		// 7 adjacent pair cases testing strict declared precedence:
		// 1. Pending outranks Unresolved
		{
			name:    "pair_pending_over_unresolved",
			pending: true,
			entries: []Entry{
				{Kind: EntryUnresolved, Reason: "test-unresolved"},
			},
			wantState: JourneyPending,
		},
		// 2. Unresolved outranks Looped
		{
			name: "pair_unresolved_over_looped",
			entries: []Entry{
				{Kind: EntryUnresolved, Reason: "test-unresolved"},
				{Kind: EntryLoop},
			},
			wantState: JourneyUnresolved,
		},
		// 3. Looped outranks Truncated
		{
			name: "pair_looped_over_truncated",
			entries: []Entry{
				{Kind: EntryLoop},
				{Reason: ReasonTruncatedRecord},
			},
			issues: []analysis.Issue{
				{Code: IssueTruncatedRecord},
			},
			wantState: JourneyLooped,
		},
		// 4. Truncated outranks Released
		{
			name:     "pair_truncated_over_released",
			released: true,
			issues: []analysis.Issue{
				{Code: IssueTruncatedRecord},
			},
			wantState: JourneyTruncated,
		},
		// 5. Released outranks Delivered
		{
			name:     "pair_released_over_delivered",
			released: true,
			entries: []Entry{
				{Kind: EntryDelivery},
			},
			wantState: JourneyReleased,
		},
		// 6. Delivered outranks Rejected
		{
			name: "pair_delivered_over_rejected",
			entries: []Entry{
				{Kind: EntryDelivery},
				{Kind: EntryRejection, Reason: "mac-mismatch"},
			},
			wantState: JourneyDelivered,
		},
		// 7. Rejected outranks Dropped
		{
			name: "pair_rejected_over_dropped",
			entries: []Entry{
				{Kind: EntryRejection, Reason: "mac-mismatch"},
				{Kind: EntryDrop, Reason: ReasonBadFrame},
			},
			wantState: JourneyRejected,
		},
		// Delivered outranks Dropped (flooding case where one host delivers, one drops)
		{
			name: "delivered_over_dropped",
			entries: []Entry{
				{Kind: EntryDelivery},
				{Kind: EntryDrop, Reason: ReasonBadFrame},
			},
			wantState: JourneyDelivered,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			j := &Journey{
				FrameID:  1,
				Entries:  tc.entries,
				Metadata: analysis.NewMetadata(analysis.WholeScope(), tc.issues, analysis.EvidenceCatalog{}, nil),
			}
			got := assignJourneyState(j, tc.pending, tc.released)
			if got != tc.wantState {
				t.Fatalf("assignJourneyState() = %q, want %q", got, tc.wantState)
			}
		})
	}
}

func TestJourneyReportPopulatesOriginAndState(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	macH1 := netaddr.MAC{0x00, 0x01, 0x02, 0x03, 0x04, 0x01}
	macH2 := netaddr.MAC{0x00, 0x01, 0x02, 0x03, 0x04, 0x02}
	macSpan := netaddr.MAC{0x00, 0x01, 0x02, 0x03, 0x04, 0x99}

	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable, err := b.Build()
	if err != nil {
		t.Fatalf("port.NewBuilder: %v", err)
	}

	gig := phy.Ethernet{
		SupportedSpeedsBPS:       []uint64{1_000_000_000},
		AutoNegotiationSupported: phy.CapabilitySupported,
		Setting:                  &phy.Setting{AutoNegotiation: true},
	}

	cfg := Config{
		Start: t0,
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: pTable,
				Bridge: &bridge.Config{
					AgingTime: 300 * time.Second,
				},
				STP: &stp.Config{
					Priority: 32768,
					Ports: map[string]stp.Port{
						"1/1/1": {},
						"1/1/2": {},
						"1/1/3": {},
					},
				},
				Traffic: &traffic.Config{
					Mirrors: []traffic.Mirror{
						{
							Name:       "span1",
							SelectAll:  true,
							OutputPort: "1/1/3",
						},
					},
				},
			},
		},
		Hosts: map[string]Host{
			"h1":   {Address: macH1, Ethernet: gig},
			"h2":   {Address: macH2, Ethernet: gig},
			"span": {Address: macSpan, Ethernet: gig},
		},
		Cables: []Cable{
			{A: Endpoint{Node: "h1"}, B: Endpoint{Node: "sw1", Port: "1/1/1"}, Medium: TwistedPair},
			{A: Endpoint{Node: "h2"}, B: Endpoint{Node: "sw1", Port: "1/1/2"}, Medium: TwistedPair},
			{A: Endpoint{Node: "span"}, B: Endpoint{Node: "sw1", Port: "1/1/3"}, Medium: TwistedPair},
		},
		PhyAssumption: defaultPhyAssumption(),
	}

	fab, err := New(cfg)
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	// 1. Caller injection from h1 to h2
	frame := ethernet.Frame{
		Dst:       macH2,
		Src:       macH1,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("test-payload-bytes"),
	}
	if _, err := fab.Inject(Injection{
		At:     t0.Add(time.Millisecond),
		Origin: Endpoint{Node: "h1"},
		Frame:  frame,
	}); err != nil {
		t.Fatalf("fab.Inject: %v", err)
	}

	// Run enough steps for STP wake/emissions, caller frame delivery, and mirror copy delivery
	fab.Run(50)

	journeys := fab.Report()
	if len(journeys) == 0 {
		t.Fatal("Report() returned no journeys")
	}

	var hasInjection, hasMirror, hasProtocol bool
	for _, j := range journeys {
		if j.Origin.Kind == "" {
			t.Errorf("journey %d has empty Origin.Kind", j.FrameID)
		}
		if j.State == "" {
			t.Errorf("journey %d has empty State", j.FrameID)
		}
		if err := j.Origin.Validate(); err != nil {
			t.Errorf("journey %d origin validation failed: %v", j.FrameID, err)
		}

		if j.Origin.Kind == OriginInjection && !j.Protocol {
			hasInjection = true
		}
		if j.Origin.Kind == OriginMirror {
			hasMirror = true
			if j.Origin.Mirror != "span1" {
				t.Errorf("mirror journey %d Mirror = %q, want %q", j.FrameID, j.Origin.Mirror, "span1")
			}
		}
		if j.Protocol {
			hasProtocol = true
		}
	}

	if !hasInjection {
		t.Errorf("Report() missing caller injection journey")
	}
	if !hasMirror {
		t.Errorf("Report() missing mirror copy journey")
	}
	if !hasProtocol {
		t.Errorf("Report() missing protocol emission journey")
	}
}
