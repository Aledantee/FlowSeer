package netaddr_test

import (
	"bytes"
	"net"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
)

func TestParseMAC(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    netaddr.MAC
		wantErr bool
	}{
		{
			name:  "colon-separated lower",
			input: "01:00:5e:00:00:fb",
			want:  netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0xfb},
		},
		{
			name:  "colon-separated upper",
			input: "01:00:5E:00:00:FB",
			want:  netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0xfb},
		},
		{
			name:  "hyphen-separated",
			input: "00-11-22-33-44-55",
			want:  netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		},
		{
			name:  "dot-separated",
			input: "0011.2233.4455",
			want:  netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "invalid characters",
			input:   "00:11:22:33:44:zz",
			wantErr: true,
		},
		{
			name:    "truncated address",
			input:   "00:11:22:33:44",
			wantErr: true,
		},
		{
			name:    "eight-byte hardware address",
			input:   "00:11:22:33:44:55:66:77",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := netaddr.Parse(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Parse(%q) succeeded, want error", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) failed: %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("Parse(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestMACString(t *testing.T) {
	mac := netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0xfb}
	want := "01:00:5e:00:00:fb"
	if got := mac.String(); got != want {
		t.Errorf("mac.String() = %q, want %q", got, want)
	}
}

func TestMACIsGroup(t *testing.T) {
	cases := []struct {
		name string
		mac  netaddr.MAC
		want bool
	}{
		{
			name: "unicast individual",
			mac:  netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			want: false,
		},
		{
			name: "multicast group",
			mac:  netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0xfb},
			want: true,
		},
		{
			name: "broadcast group",
			mac:  netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			want: true,
		},
		{
			name: "locally administered unicast",
			mac:  netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.mac.IsGroup(); got != tc.want {
				t.Errorf("mac.IsGroup() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMACConversions(t *testing.T) {
	t.Run("round trip conversion", func(t *testing.T) {
		orig := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
		hw := orig.HardwareAddr()

		if !bytes.Equal(hw, orig[:]) {
			t.Fatalf("HardwareAddr() = %v, want %v", hw, orig[:])
		}

		// Verify returned slice is an isolated copy.
		hw[0] = 0xff
		if orig[0] == 0xff {
			t.Fatal("mutating HardwareAddr() result modified the source MAC")
		}

		fromHW, err := netaddr.FromHardwareAddr(net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55})
		if err != nil {
			t.Fatalf("FromHardwareAddr failed: %v", err)
		}
		if fromHW != orig {
			t.Errorf("FromHardwareAddr = %v, want %v", fromHW, orig)
		}
	})

	t.Run("reject non six byte hardware addresses", func(t *testing.T) {
		invalidAddrs := []net.HardwareAddr{
			nil,
			{},
			{0x01, 0x02, 0x03, 0x04, 0x05},
			{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07},
			{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
		}

		for _, hw := range invalidAddrs {
			_, err := netaddr.FromHardwareAddr(hw)
			if err == nil {
				t.Errorf("FromHardwareAddr(%v) succeeded, want error", hw)
			}
		}
	})
}

func TestEUI64(t *testing.T) {
	t.Run("parse valid", func(t *testing.T) {
		got, err := netaddr.ParseEUI64("00:11:22:33:44:55:66:77")
		if err != nil {
			t.Fatalf("ParseEUI64 failed: %v", err)
		}
		want := netaddr.EUI64{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77}
		if got != want {
			t.Errorf("ParseEUI64 = %v, want %v", got, want)
		}
		if got.String() != "00:11:22:33:44:55:66:77" {
			t.Errorf("String() = %q, want %q", got.String(), "00:11:22:33:44:55:66:77")
		}
		if got.IsGroup() {
			t.Error("IsGroup() = true, want false")
		}
	})

	t.Run("parse invalid length", func(t *testing.T) {
		_, err := netaddr.ParseEUI64("00:11:22:33:44:55")
		if err == nil {
			t.Fatal("ParseEUI64 with 6 bytes succeeded, want error")
		}
	})

	t.Run("conversions", func(t *testing.T) {
		orig := netaddr.EUI64{0x01, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77}
		if !orig.IsGroup() {
			t.Error("IsGroup() = false, want true")
		}

		hw := orig.HardwareAddr()
		if !bytes.Equal(hw, orig[:]) {
			t.Fatalf("HardwareAddr() = %v, want %v", hw, orig[:])
		}

		got, err := netaddr.EUI64FromHardwareAddr(hw)
		if err != nil {
			t.Fatalf("EUI64FromHardwareAddr failed: %v", err)
		}
		if got != orig {
			t.Errorf("EUI64FromHardwareAddr = %v, want %v", got, orig)
		}

		_, err = netaddr.EUI64FromHardwareAddr(net.HardwareAddr{1, 2, 3, 4, 5, 6})
		if err == nil {
			t.Error("EUI64FromHardwareAddr(6 bytes) succeeded, want error")
		}
	})
}
