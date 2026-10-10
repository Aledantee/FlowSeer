package netmodel

import (
	"fmt"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Issue codes used by netmodel.
const (
	IssueMissingMTU         analysis.IssueCode = "netmodel.interface.missing_mtu"
	IssueInvalidOperStatus  analysis.IssueCode = "netmodel.interface.invalid_oper_status"
	IssueMissingOperStatus  analysis.IssueCode = "netmodel.interface.missing_oper_status"
	IssueInvalidAdminStatus analysis.IssueCode = "netmodel.interface.invalid_admin_status"
	IssueMissingAdminStatus analysis.IssueCode = "netmodel.interface.missing_admin_status"
)

func validateInterfaceNames(ifaces []*interfacev1.Interface) error {
	if len(ifaces) == 0 {
		return errs.New().Msg("interface list cannot be empty")
	}

	// The port builder only sees interfaces that become ports; a sub-interface
	// never reaches it, so name collisions and blanks are caught here instead,
	// across the whole input rather than one port kind.
	seenNames := make(map[string]struct{}, len(ifaces))
	for _, iface := range ifaces {
		name := iface.GetName()
		if name == "" {
			return errs.New().Msg("interface name cannot be empty")
		}
		if _, exists := seenNames[name]; exists {
			return errs.New().Attr("name", name).Msgf("duplicate interface name %q", name)
		}
		seenNames[name] = struct{}{}
	}

	return nil
}

func (c *loadContext) loadPorts(ifaces []*interfacev1.Interface) (port.Table, error) {
	portBuilder := port.NewBuilder()
	for _, iface := range ifaces {
		if iface.GetSub() != nil {
			// A sub-interface carves a routed interface out of its parent
			// port's frames; it is not itself a port, and a port-table entry
			// for it would be a bridgeless router's phantom port that
			// nothing routes.
			continue
		}
		p := port.Port{
			Name: iface.GetName(),
		}
		if iface.HasIfIndex() {
			p.IfIndex = iface.GetIfIndex()
		}
		switch {
		case iface.GetPhysical() != nil:
			p.Kind = port.Physical
			if iface.GetPhysical().HasLagParentInterfaceName() {
				p.LagParent = iface.GetPhysical().GetLagParentInterfaceName()
			}
		case iface.GetLag() != nil:
			p.Kind = port.LAG
		default:
			p.Kind = port.Other
		}

		if iface.HasMtu() {
			p.MTU = int(iface.GetMtu())
		} else {
			ref := c.addEvidence(EvidenceKindState, fmt.Sprintf("interface %s mtu unreported", iface.GetName()))
			c.issues = append(c.issues, analysis.Issue{
				Code:     IssueMissingMTU,
				Status:   analysis.Incomplete,
				Scope:    c.portScope(iface.GetName()),
				Message:  fmt.Sprintf("interface %q has no reported MTU", iface.GetName()),
				Evidence: []trace.EvidenceRef{ref},
			})
			c.addDefault(iface.GetName(), "mtu", "0")
		}

		switch {
		case !iface.HasAdminStatus() || iface.GetAdminStatus() == interfacev1.AdminStatus_ADMIN_STATUS_UNSPECIFIED:
			p.AdminStatus = port.Unknown
			ref := c.addEvidence(EvidenceKindState, fmt.Sprintf("interface %s admin_status unspecified", iface.GetName()))
			c.issues = append(c.issues, analysis.Issue{
				Code:     IssueMissingAdminStatus,
				Status:   analysis.Incomplete,
				Scope:    c.portScope(iface.GetName()),
				Message:  fmt.Sprintf("interface %q has unspecified or missing administrative status", iface.GetName()),
				Evidence: []trace.EvidenceRef{ref},
			})
		case iface.GetAdminStatus() == interfacev1.AdminStatus_ADMIN_STATUS_UP:
			p.AdminStatus = port.Up
		case iface.GetAdminStatus() == interfacev1.AdminStatus_ADMIN_STATUS_DOWN ||
			iface.GetAdminStatus() == interfacev1.AdminStatus_ADMIN_STATUS_TESTING:
			p.AdminStatus = port.Down
		default:
			p.AdminStatus = port.Unknown
			ref := c.addEvidence(EvidenceKindState, fmt.Sprintf("interface %s admin_status unrecognized value %d", iface.GetName(), iface.GetAdminStatus()))
			c.issues = append(c.issues, analysis.Issue{
				Code:     IssueInvalidAdminStatus,
				Status:   analysis.Unsupported,
				Scope:    c.portScope(iface.GetName()),
				Message:  fmt.Sprintf("interface %q has invalid or unrecognized administrative status value %d", iface.GetName(), iface.GetAdminStatus()),
				Evidence: []trace.EvidenceRef{ref},
			})
		}

		switch {
		case !iface.HasOperStatus() ||
			iface.GetOperStatus() == interfacev1.OperStatus_OPER_STATUS_UNSPECIFIED ||
			iface.GetOperStatus() == interfacev1.OperStatus_OPER_STATUS_UNKNOWN:
			p.OperStatus = port.Unknown
			ref := c.addEvidence(EvidenceKindState, fmt.Sprintf("interface %s oper_status unspecified or unknown", iface.GetName()))
			c.issues = append(c.issues, analysis.Issue{
				Code:     IssueMissingOperStatus,
				Status:   analysis.Incomplete,
				Scope:    c.portScope(iface.GetName()),
				Message:  fmt.Sprintf("interface %q has unspecified or missing operational status", iface.GetName()),
				Evidence: []trace.EvidenceRef{ref},
			})
		case iface.GetOperStatus() == interfacev1.OperStatus_OPER_STATUS_UP:
			p.OperStatus = port.Up
		case iface.GetOperStatus() == interfacev1.OperStatus_OPER_STATUS_DOWN ||
			iface.GetOperStatus() == interfacev1.OperStatus_OPER_STATUS_TESTING ||
			iface.GetOperStatus() == interfacev1.OperStatus_OPER_STATUS_DORMANT ||
			iface.GetOperStatus() == interfacev1.OperStatus_OPER_STATUS_NOT_PRESENT ||
			iface.GetOperStatus() == interfacev1.OperStatus_OPER_STATUS_LOWER_LAYER_DOWN:
			p.OperStatus = port.Down
		default:
			p.OperStatus = port.Unknown
			ref := c.addEvidence(EvidenceKindState, fmt.Sprintf("interface %s oper_status unrecognized value %d", iface.GetName(), iface.GetOperStatus()))
			c.issues = append(c.issues, analysis.Issue{
				Code:     IssueInvalidOperStatus,
				Status:   analysis.Unsupported,
				Scope:    c.portScope(iface.GetName()),
				Message:  fmt.Sprintf("interface %q has invalid or unrecognized operational status value %d", iface.GetName(), iface.GetOperStatus()),
				Evidence: []trace.EvidenceRef{ref},
			})
		}

		portBuilder.Add(p)
	}

	ports, err := portBuilder.Build()
	if err != nil {
		return port.Table{}, errs.Wrap(err, "build port table")
	}

	return ports, nil
}
