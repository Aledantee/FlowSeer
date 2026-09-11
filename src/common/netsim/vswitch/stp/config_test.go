package stp_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

func TestDefaultPathCost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		speedBPS uint64
		want     uint32
	}{
		{name: "100 Gbps", speedBPS: 100_000_000_000, want: 200},
		{name: "400 Gbps", speedBPS: 400_000_000_000, want: 200},
		{name: "40 Gbps", speedBPS: 40_000_000_000, want: 2_000},
		{name: "10 Gbps", speedBPS: 10_000_000_000, want: 2_000},
		{name: "2.5 Gbps", speedBPS: 2_500_000_000, want: 20_000},
		{name: "1 Gbps", speedBPS: 1_000_000_000, want: 20_000},
		{name: "100 Mbps", speedBPS: 100_000_000, want: 200_000},
		{name: "10 Mbps", speedBPS: 10_000_000, want: 2_000_000},
		{name: "unknown speed", speedBPS: 0, want: 20_000},
		{name: "sub-10 Mbps", speedBPS: 1_000_000, want: 20_000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := stp.DefaultPathCost(tc.speedBPS)
			if got != tc.want {
				t.Errorf("DefaultPathCost(%d) = %d, want %d", tc.speedBPS, got, tc.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	validMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

	tbl, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}).
		Add(port.Port{Name: "lag1", Kind: port.Lag}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, LagParent: "lag1"}).
		Build()
	if err != nil {
		t.Fatalf("port.Builder.Build: %v", err)
	}

	tests := []struct {
		name    string
		cfg     stp.Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: stp.Config{
				Priority: 32768,
				Address:  validMAC,
				Ports: map[string]stp.Port{
					"1/1/1": {Priority: 128},
					"lag1":  {Priority: 128},
				},
			},
			wantErr: false,
		},
		{
			name: "zero address accepted",
			cfg: stp.Config{
				Priority: 32768,
				Address:  netaddr.MAC{},
				Ports: map[string]stp.Port{
					"1/1/1": {Priority: 128},
				},
			},
			wantErr: false,
		},
		{
			name: "non-multiple of 4096 priority rejected",
			cfg: stp.Config{
				Priority: 32767,
				Address:  validMAC,
				Ports: map[string]stp.Port{
					"1/1/1": {Priority: 128},
				},
			},
			wantErr: true,
		},
		{
			name: "hello time above ten seconds rejected",
			cfg: stp.Config{
				Priority:  32768,
				Address:   validMAC,
				HelloTime: 11 * time.Second,
				Ports:     map[string]stp.Port{"1/1/1": {}},
			},
			wantErr: true,
		},
		{
			name: "max age below six seconds rejected",
			cfg: stp.Config{
				Priority: 32768,
				Address:  validMAC,
				MaxAge:   5 * time.Second,
				Ports:    map[string]stp.Port{"1/1/1": {}},
			},
			wantErr: true,
		},
		{
			name: "forward delay above thirty seconds rejected",
			cfg: stp.Config{
				Priority:     32768,
				Address:      validMAC,
				ForwardDelay: 31 * time.Second,
				Ports:        map[string]stp.Port{"1/1/1": {}},
			},
			wantErr: true,
		},
		{
			name: "port absent from table rejected",
			cfg: stp.Config{
				Priority: 32768,
				Address:  validMAC,
				Ports: map[string]stp.Port{
					"1/1/99": {Priority: 128},
				},
			},
			wantErr: true,
		},
		{
			name: "lag member port rejected",
			cfg: stp.Config{
				Priority: 32768,
				Address:  validMAC,
				Ports: map[string]stp.Port{
					"1/1/3": {Priority: 128},
				},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.cfg.Validate(tbl)
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestBridgeID(t *testing.T) {
	t.Parallel()

	mac1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	mac2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

	b1 := stp.BridgeID{Priority: 4096, Address: mac1}
	b2 := stp.BridgeID{Priority: 8192, Address: mac1}
	b3 := stp.BridgeID{Priority: 4096, Address: mac2}

	if !b1.Less(b2) {
		t.Error("b1 should be Less than b2 by priority")
	}
	if b2.Less(b1) {
		t.Error("b2 should not be Less than b1")
	}
	if !b1.Less(b3) {
		t.Error("b1 should be Less than b3 by MAC address")
	}
	if b3.Less(b1) {
		t.Error("b3 should not be Less than b1")
	}

	if got := b1.String(); got != "4096/00:11:22:33:44:01" {
		t.Errorf("b1.String() = %q, want %q", got, "4096/00:11:22:33:44:01")
	}
}

func TestDiff(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

	a := stp.Config{
		Priority:     32768,
		Address:      mac,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
		Ports: map[string]stp.Port{
			"1/1/1": {Priority: 128, PathCost: 20000, AdminEdge: false, PointToPoint: stp.PointToPointAuto},
			"1/1/2": {Priority: 128, PathCost: 20000},
		},
	}

	b := stp.Config{
		Priority:     4096,
		Address:      mac,
		HelloTime:    1 * time.Second,
		MaxAge:       10 * time.Second,
		ForwardDelay: 7 * time.Second,
		Ports: map[string]stp.Port{
			"1/1/1": {Priority: 64, PathCost: 2000, AdminEdge: true, PointToPoint: stp.PointToPointForceTrue},
			"1/1/3": {Priority: 128, PathCost: 20000},
		},
	}

	changes := stp.Diff(a, b)

	findChange := func(kind, key, field string) (any, any, bool) {
		for _, c := range changes {
			if c.Subject.Kind == kind && c.Subject.Key == key && c.Field == field {
				return c.From, c.To, true
			}
		}
		return nil, nil, false
	}

	if from, to, ok := findChange("bridge", "", "priority"); !ok || from != uint16(32768) || to != uint16(4096) {
		t.Errorf("priority change: got (%v, %v, %v), want (32768, 4096, true)", from, to, ok)
	}
	if from, to, ok := findChange("bridge", "", "hello_time"); !ok || from != 2*time.Second || to != 1*time.Second {
		t.Errorf("hello_time change: got (%v, %v, %v)", from, to, ok)
	}
	if from, to, ok := findChange("bridge", "", "max_age"); !ok || from != 20*time.Second || to != 10*time.Second {
		t.Errorf("max_age change: got (%v, %v, %v)", from, to, ok)
	}
	if from, to, ok := findChange("bridge", "", "forward_delay"); !ok || from != 15*time.Second || to != 7*time.Second {
		t.Errorf("forward_delay change: got (%v, %v, %v)", from, to, ok)
	}

	otherMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x66}
	moved := b
	moved.Address = otherMAC
	if from, to, ok := findAddress(stp.Diff(b, moved)); !ok || from != mac || to != otherMAC {
		t.Errorf("address change: got (%v, %v, %v), want (%v, %v, true)", from, to, ok, mac, otherMAC)
	}

	if from, to, ok := findChange("port", "1/1/1", "priority"); !ok || from != uint8(128) || to != uint8(64) {
		t.Errorf("port priority change: got (%v, %v, %v)", from, to, ok)
	}
	if from, to, ok := findChange("port", "1/1/1", "admin_path_cost"); !ok || from != uint32(20000) || to != uint32(2000) {
		t.Errorf("port admin_path_cost change: got (%v, %v, %v)", from, to, ok)
	}
	if from, to, ok := findChange("port", "1/1/1", "admin_edge"); !ok || from != false || to != true {
		t.Errorf("port admin_edge change: got (%v, %v, %v)", from, to, ok)
	}
	if from, to, ok := findChange("port", "1/1/1", "admin_point_to_point"); !ok || from != stp.PointToPointAuto || to != stp.PointToPointForceTrue {
		t.Errorf("port admin_point_to_point change: got (%v, %v, %v)", from, to, ok)
	}

	// 1/1/2 removed
	if from, to, ok := findChange("port", "1/1/2", ""); !ok || from == nil || to != nil {
		t.Errorf("port 1/1/2 removed: got (%v, %v, %v)", from, to, ok)
	}
	// 1/1/3 added
	if from, to, ok := findChange("port", "1/1/3", ""); !ok || from != nil || to == nil {
		t.Errorf("port 1/1/3 added: got (%v, %v, %v)", from, to, ok)
	}
}

func findAddress(changes []trace.Change) (any, any, bool) {
	for _, c := range changes {
		if c.Subject.Kind == "bridge" && c.Field == "address" {
			return c.From, c.To, true
		}
	}
	return nil, nil, false
}
