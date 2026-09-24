//go:build netpen_t2

package lab

import (
	"net/netip"
	"regexp"
	"slices"
	"strings"

	"go.aledante.io/FlowSeer/src/protocol/ssh"
)

var (
	iosxePrivilegedPattern = regexp.MustCompile(`(?m)^[^()\s][^()\r\n]*#\s*$`)
	iosxeMorePattern       = regexp.MustCompile(`--More--`)
)

// OSPFNeighbor is one IOS-XE OSPF neighbor-table row.
type OSPFNeighbor struct {
	// RouterID is the neighbor's OSPF router ID.
	RouterID string
	// State is the IOS-XE adjacency state, including the role suffix.
	State string
	// Address is the neighbor's source address on the shared segment.
	Address string
	// Interface is the local IOS-XE interface carrying the adjacency.
	Interface string
}

// ParseOSPFNeighbors extracts valid IPv4 neighbor rows from IOS-XE show output.
// Command echoes, prompts, pagination markers, and unrelated lines are ignored.
func ParseOSPFNeighbors(out string) []OSPFNeighbor {
	out = strings.ReplaceAll(out, "\r", "")
	out = strings.ReplaceAll(out, "\b", "")
	out = iosxeMorePattern.ReplaceAllString(out, "")

	rows := false
	var neighbors []OSPFNeighbor
	for line := range strings.Lines(out) {
		fields := strings.Fields(line)
		if !rows {
			rows = isOSPFNeighborHeader(fields)
			continue
		}
		if len(fields) < 6 {
			continue
		}
		routerID, routerErr := netip.ParseAddr(fields[0])
		address, addressErr := netip.ParseAddr(fields[4])
		if routerErr != nil || addressErr != nil || !routerID.Is4() || !address.Is4() {
			continue
		}
		neighbors = append(neighbors, OSPFNeighbor{
			RouterID:  routerID.String(),
			State:     fields[2],
			Address:   address.String(),
			Interface: fields[5],
		})
	}
	return neighbors
}

// HasNeighbor reports whether neighbors contains routerID.
func HasNeighbor(neighbors []OSPFNeighbor, routerID string) bool {
	return slices.ContainsFunc(neighbors, func(neighbor OSPFNeighbor) bool {
		return neighbor.RouterID == routerID
	})
}

// IOSXEPrivilegedPrompt returns the privileged-EXEC prompt used by IOS-XE
// show commands. Its pattern excludes configuration-mode siblings.
func IOSXEPrivilegedPrompt() ssh.Prompt {
	return ssh.Prompt{Name: "iosxe-privileged", Pattern: iosxePrivilegedPattern}
}

// IOSXEOSPFNeighborCommand returns the IOS-XE neighbor-table query with prompt
// and pagination handling configured for [ssh.Session.Run].
func IOSXEOSPFNeighborCommand() ssh.Command {
	return ssh.Command{
		Line:          "show ip ospf neighbor",
		Prompts:       []ssh.Prompt{IOSXEPrivilegedPrompt()},
		MorePattern:   iosxeMorePattern,
		MoreKeystroke: []byte(" "),
	}
}

func isOSPFNeighborHeader(fields []string) bool {
	return len(fields) >= 6 && fields[0] == "Neighbor" && fields[1] == "ID" &&
		fields[2] == "Pri" && fields[3] == "State"
}
