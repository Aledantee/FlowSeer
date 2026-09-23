//go:build netpen_t2

package lab

import (
	"os"
	"testing"
)

func TestParseOSPFNeighbors(t *testing.T) {
	tests := []struct {
		name       string
		fixture    string
		wantRouter string
		wantCount  int
	}{
		{name: "neighbor present", fixture: "testdata/iosxe_show_ip_ospf_neighbor.txt", wantRouter: "10.0.0.99", wantCount: 2},
		{name: "empty", fixture: "testdata/iosxe_show_ip_ospf_neighbor_empty.txt", wantCount: 0},
		{name: "paginated", fixture: "testdata/iosxe_show_ip_ospf_neighbor_paged.txt", wantRouter: "10.0.0.99", wantCount: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := os.ReadFile(tc.fixture)
			if err != nil {
				t.Fatal(err)
			}
			neighbors := ParseOSPFNeighbors(string(out))
			if len(neighbors) != tc.wantCount {
				t.Fatalf("ParseOSPFNeighbors() returned %d neighbors, want %d: %#v", len(neighbors), tc.wantCount, neighbors)
			}
			if tc.wantRouter != "" && !HasNeighbor(neighbors, tc.wantRouter) {
				t.Errorf("HasNeighbor(%q) = false, want true: %#v", tc.wantRouter, neighbors)
			}
		})
	}
}

func TestIOSXEPrivilegedPrompt(t *testing.T) {
	prompt := IOSXEPrivilegedPrompt()
	if !prompt.Pattern.MatchString("show ip ospf neighbor\r\nLABRT42#") {
		t.Error("privileged prompt did not match a multiline operational transcript")
	}
	if prompt.Pattern.MatchString("configure terminal\r\nLABRT42(config)#") {
		t.Error("privileged prompt matched a configuration-mode prompt")
	}

	command := IOSXEOSPFNeighborCommand()
	if command.Line != "show ip ospf neighbor" || command.MorePattern == nil || len(command.MoreKeystroke) == 0 {
		t.Errorf("IOSXEOSPFNeighborCommand() = %#v, want command with pagination handling", command)
	}
}
