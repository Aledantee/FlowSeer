package netmodel

import (
	"cmp"
	"slices"
	"strconv"
	"time"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Load translates typed network model interfaces, VLANs, FDB entries, and PoE budgets
// into a virtual switch [vswitch.Config] and preloaded forwarding [bridge.Seed] entries.
//
// If want is empty, the capability set is inferred from the facets and row kinds present:
// relay is always included, vlan if any interface carries a switchport facet, ethernet if
// any carries an Ethernet facet, poe if any carries a PoE facet or budget, and lag if any
// interface is an aggregation. If want is non-empty, only the requested layers are built,
// and any present facet outside want is omitted and recorded in [Report.Skipped].
//
// Load returns an error only for conditions that prevent constructing a switch at all:
// an empty interface slice, a duplicate or empty interface name, or a LAG parent reference
// that is invalid or refers to a non-LAG interface. All other omissions and defaults are
// captured in the returned [Report].
func Load(
	now time.Time,
	ifaces []*interfacev1.Interface,
	vlans []*switchingv1.Vlan,
	fdb []*switchingv1.FdbEntry,
	budgets []*phyv1.PseBudget,
	want []port.Layer,
) (vswitch.Config, []bridge.Seed, Report, error) {
	if len(ifaces) == 0 {
		return vswitch.Config{}, nil, Report{}, errs.New().Msg("interface list cannot be empty")
	}

	report := Report{
		CapabilitySources: make(map[port.Layer]string),
	}

	portBuilder := port.NewBuilder()
	for _, iface := range ifaces {
		p := port.Port{
			Name: iface.GetName(),
		}
		if iface.HasIfIndex() {
			idx := iface.GetIfIndex()
			p.IfIndex = &idx
		}
		switch {
		case iface.GetPhysical() != nil:
			p.Kind = port.Physical
			if iface.GetPhysical().HasLagParent() {
				p.LagParent = iface.GetPhysical().GetLagParent()
			}
		case iface.GetLag() != nil:
			p.Kind = port.Lag
		default:
			p.Kind = port.Other
		}

		if iface.HasMtu() {
			if iface.GetMtu() == 0 {
				report.Defaults = append(report.Defaults, Default{
					Port:  iface.GetName(),
					Field: "mtu",
					Value: "0",
				})
				p.MTU = 0
			} else {
				p.MTU = int(iface.GetMtu())
			}
		}

		switch {
		case !iface.HasAdminStatus() || iface.GetAdminStatus() == interfacev1.AdminStatus_ADMIN_STATUS_UNSPECIFIED:
			p.AdminStatus = port.Unreported
		case iface.GetAdminStatus() == interfacev1.AdminStatus_ADMIN_STATUS_UP:
			p.AdminStatus = port.Up
		default:
			p.AdminStatus = port.Down
		}

		switch {
		case !iface.HasOperStatus() || iface.GetOperStatus() == interfacev1.OperStatus_OPER_STATUS_UNSPECIFIED:
			p.OperStatus = port.Unreported
		case iface.GetOperStatus() == interfacev1.OperStatus_OPER_STATUS_UP:
			p.OperStatus = port.Up
		default:
			p.OperStatus = port.Down
		}

		portBuilder.Add(p)
	}

	ports, err := portBuilder.Build()
	if err != nil {
		return vswitch.Config{}, nil, Report{}, err
	}

	var (
		hasSwitchportFacet bool
		hasEthernetFacet   bool
		hasPoeFacet        bool
		hasLag             bool
	)

	for _, iface := range ifaces {
		if iface.GetPhysical() != nil {
			if iface.GetPhysical().HasEthernet() && iface.GetPhysical().GetEthernet() != nil {
				hasEthernetFacet = true
				if copper := iface.GetPhysical().GetEthernet().GetCopper(); copper != nil {
					if copper.GetPoe() != nil || copper.GetPoeSettings() != nil || copper.GetPoeDetail() != nil {
						hasPoeFacet = true
					}
				}
			}
			if iface.GetPhysical().HasSwitchport() && iface.GetPhysical().GetSwitchport() != nil {
				hasSwitchportFacet = true
			}
		} else if iface.GetLag() != nil {
			hasLag = true
			if iface.GetLag().HasSwitchport() && iface.GetLag().GetSwitchport() != nil {
				hasSwitchportFacet = true
			}
		}
	}

	if len(want) == 0 {
		report.Capabilities = append(report.Capabilities, port.LayerRelay)
		report.CapabilitySources[port.LayerRelay] = "always"

		if hasSwitchportFacet {
			report.Capabilities = append(report.Capabilities, port.LayerVlan)
			report.CapabilitySources[port.LayerVlan] = "inferred:switchport"
		}
		if hasEthernetFacet {
			report.Capabilities = append(report.Capabilities, port.LayerEthernet)
			report.CapabilitySources[port.LayerEthernet] = "inferred:ethernet"
		}
		if hasPoeFacet {
			report.Capabilities = append(report.Capabilities, port.LayerPoe)
			report.CapabilitySources[port.LayerPoe] = "inferred:poe"
		} else if len(budgets) > 0 {
			report.Capabilities = append(report.Capabilities, port.LayerPoe)
			report.CapabilitySources[port.LayerPoe] = "inferred:pse_budget"
		}
		if hasLag {
			report.Capabilities = append(report.Capabilities, port.LayerLag)
			report.CapabilitySources[port.LayerLag] = "inferred:lag"
		}
	} else {
		report.Capabilities = make([]port.Layer, len(want))
		copy(report.Capabilities, want)
		for _, l := range want {
			report.CapabilitySources[l] = "wanted"
		}
	}

	isWanted := func(l port.Layer) bool {
		return slices.Contains(report.Capabilities, l)
	}

	if !isWanted(port.LayerVlan) {
		for _, iface := range ifaces {
			hasSw := false
			if iface.GetPhysical() != nil && iface.GetPhysical().HasSwitchport() && iface.GetPhysical().GetSwitchport() != nil {
				hasSw = true
			} else if iface.GetLag() != nil && iface.GetLag().HasSwitchport() && iface.GetLag().GetSwitchport() != nil {
				hasSw = true
			}
			if hasSw {
				report.Skipped = append(report.Skipped, Skipped{
					Port: iface.GetName(),
					What: "switchport",
					Why:  "layer not wanted",
				})
			}
		}
	}

	if !isWanted(port.LayerEthernet) {
		for _, iface := range ifaces {
			if iface.GetPhysical() != nil && iface.GetPhysical().HasEthernet() && iface.GetPhysical().GetEthernet() != nil {
				report.Skipped = append(report.Skipped, Skipped{
					Port: iface.GetName(),
					What: "ethernet",
					Why:  "layer not wanted",
				})
			}
		}
	}

	if !isWanted(port.LayerPoe) {
		for _, iface := range ifaces {
			if iface.GetPhysical() != nil && iface.GetPhysical().HasEthernet() && iface.GetPhysical().GetEthernet() != nil {
				if copper := iface.GetPhysical().GetEthernet().GetCopper(); copper != nil {
					if copper.GetPoe() != nil || copper.GetPoeSettings() != nil || copper.GetPoeDetail() != nil {
						report.Skipped = append(report.Skipped, Skipped{
							Port: iface.GetName(),
							What: "poe",
							Why:  "layer not wanted",
						})
					}
				}
			}
		}
		for _, b := range budgets {
			groupStr := strconv.FormatUint(uint64(b.GetPseGroup()), 10)
			report.Skipped = append(report.Skipped, Skipped{
				Port: groupStr,
				What: "pse_budget",
				Why:  "layer not wanted",
			})
		}
	}

	for _, iface := range ifaces {
		hasSw := false
		if iface.GetPhysical() != nil && iface.GetPhysical().HasSwitchport() && iface.GetPhysical().GetSwitchport() != nil {
			hasSw = true
		} else if iface.GetLag() != nil && iface.GetLag().HasSwitchport() && iface.GetLag().GetSwitchport() != nil {
			hasSw = true
		}
		if !hasSw {
			report.NoSwitchport = append(report.NoSwitchport, iface.GetName())
		}
	}

	cfg := vswitch.Config{
		Ports: ports,
	}

	if isWanted(port.LayerEthernet) || isWanted(port.LayerPoe) {
		phyCfg := &phy.Config{}
		if isWanted(port.LayerEthernet) {
			phyCfg.Ethernet = make(map[string]phy.Ethernet)
			for _, iface := range ifaces {
				if iface.GetPhysical() == nil || !iface.GetPhysical().HasEthernet() || iface.GetPhysical().GetEthernet() == nil {
					continue
				}
				ef := iface.GetPhysical().GetEthernet()
				eth := phy.Ethernet{}
				if ef.HasCapabilities() && ef.GetCapabilities() != nil {
					caps := ef.GetCapabilities()
					if len(caps.GetSupportedSpeedsBps()) > 0 {
						eth.SupportedSpeedsBPS = make([]uint64, len(caps.GetSupportedSpeedsBps()))
						copy(eth.SupportedSpeedsBPS, caps.GetSupportedSpeedsBps())
					}
					if caps.HasAutoNegotiationSupported() {
						eth.AutoNegotiationSupported = caps.GetAutoNegotiationSupported()
					}
				}
				if ef.HasAppliedAutoNegotiation() && ef.GetAppliedAutoNegotiation() != nil {
					an := ef.GetAppliedAutoNegotiation()
					if an.HasEnabled() {
						eth.Setting = &phy.Setting{
							AutoNegotiation: an.GetEnabled(),
						}
					}
				}
				var obs *phy.Observed
				speed := uint64(0)
				if ef.HasActiveSpeedBps() {
					speed = ef.GetActiveSpeedBps()
				}
				duplex := phy.Unknown
				if ef.HasActiveDuplex() {
					switch ef.GetActiveDuplex() {
					case phyv1.EthernetDuplex_ETHERNET_DUPLEX_FULL:
						duplex = phy.Full
					case phyv1.EthernetDuplex_ETHERNET_DUPLEX_HALF:
						duplex = phy.Half
					}
				}
				if speed > 0 || duplex != phy.Unknown {
					obs = &phy.Observed{
						SpeedBPS: speed,
						Duplex:   duplex,
					}
				}
				eth.Observed = obs
				phyCfg.Ethernet[iface.GetName()] = eth
			}
		}

		if isWanted(port.LayerPoe) {
			poe := &phy.PoE{
				Groups: make(map[string]phy.Group, len(budgets)),
				Ports:  make(map[string]phy.PsePort),
			}
			for _, b := range budgets {
				groupStr := strconv.FormatUint(uint64(b.GetPseGroup()), 10)
				var power uint32
				if b.HasPowerMilliwatts() {
					power = b.GetPowerMilliwatts()
				} else {
					report.Defaults = append(report.Defaults, Default{
						Port:  groupStr,
						Field: "power_milliwatts",
						Value: "0",
					})
				}
				poe.Groups[groupStr] = phy.Group{
					PowerMilliwatts: power,
				}
			}

			for _, iface := range ifaces {
				if iface.GetPhysical() == nil || !iface.GetPhysical().HasEthernet() || iface.GetPhysical().GetEthernet() == nil {
					continue
				}
				copper := iface.GetPhysical().GetEthernet().GetCopper()
				if copper == nil {
					continue
				}
				hasPoeInfo := copper.GetPoe() != nil || copper.GetPoeSettings() != nil || copper.GetPoeDetail() != nil
				if !hasPoeInfo {
					continue
				}
				detail := copper.GetPoeDetail()
				if detail == nil || !detail.HasPseGroup() {
					report.Skipped = append(report.Skipped, Skipped{
						Port: iface.GetName(),
						What: "poe",
						Why:  "missing poe_detail",
					})
					continue
				}
				groupStr := strconv.FormatUint(uint64(detail.GetPseGroup()), 10)
				report.Defaults = append(report.Defaults, Default{
					Port:  iface.GetName(),
					Field: "max_class",
					Value: "8",
				})
				psePort := phy.PsePort{
					Group:    groupStr,
					MaxClass: 8,
					Enabled:  true,
				}
				if ps := copper.GetPoeSettings(); ps != nil {
					if ps.HasEnabled() {
						psePort.Enabled = ps.GetEnabled()
					}
					if ps.HasPowerLimitMilliwatts() {
						lim := ps.GetPowerLimitMilliwatts()
						psePort.Limit = &lim
					}
					if ps.HasPriority() && ps.GetPriority() != phyv1.PoePriority_POE_PRIORITY_UNSPECIFIED {
						switch ps.GetPriority() {
						case phyv1.PoePriority_POE_PRIORITY_CRITICAL:
							psePort.Priority = phy.PriorityCritical
						case phyv1.PoePriority_POE_PRIORITY_HIGH:
							psePort.Priority = phy.PriorityHigh
						case phyv1.PoePriority_POE_PRIORITY_LOW:
							psePort.Priority = phy.PriorityLow
						}
					} else {
						report.Defaults = append(report.Defaults, Default{
							Port:  iface.GetName(),
							Field: "priority",
							Value: "",
						})
					}
				} else {
					report.Defaults = append(report.Defaults, Default{
						Port:  iface.GetName(),
						Field: "priority",
						Value: "",
					})
				}
				if copper.GetPoe() != nil && copper.GetPoe().HasPowerClass() {
					psePort.PDClass = uint8(copper.GetPoe().GetPowerClass())
				}
				poe.Ports[iface.GetName()] = psePort
			}
			phyCfg.PoE = poe
		}

		if phyCfg.Ethernet != nil || phyCfg.PoE != nil {
			cfg.Phy = phyCfg
		}
	}

	if isWanted(port.LayerRelay) {
		bridgeCfg := &bridge.Config{
			AgingTime: 300 * time.Second,
		}
		report.Defaults = append(report.Defaults, Default{
			Port:  "",
			Field: "aging_time",
			Value: "300s",
		})

		if isWanted(port.LayerVlan) {
			vlanCfg := &bridge.VLAN{
				Table:       make(map[vlan.ID]string, len(vlans)),
				Switchports: make(map[string]bridge.Switchport),
			}
			for _, v := range vlans {
				vlanCfg.Table[vlan.ID(v.GetId())] = v.GetName()
			}

			for _, iface := range ifaces {
				var (
					swFacet  *switchingv1.SwitchportFacet
					isMember bool
				)
				if iface.GetPhysical() != nil {
					if iface.GetPhysical().HasLagParent() {
						isMember = true
					}
					if iface.GetPhysical().HasSwitchport() && iface.GetPhysical().GetSwitchport() != nil {
						swFacet = iface.GetPhysical().GetSwitchport()
					}
				} else if iface.GetLag() != nil {
					if iface.GetLag().HasSwitchport() && iface.GetLag().GetSwitchport() != nil {
						swFacet = iface.GetLag().GetSwitchport()
					}
				}

				if swFacet == nil {
					continue
				}
				if isMember {
					report.Skipped = append(report.Skipped, Skipped{
						Port: iface.GetName(),
						What: "switchport",
						Why:  "lag member",
					})
					continue
				}

				sw := bridge.Switchport{}
				if swFacet.HasPvid() {
					vid := vlan.ID(swFacet.GetPvid())
					sw.PVID = &vid
				} else if len(swFacet.GetUntaggedVlanIds()) == 1 {
					vid := vlan.ID(swFacet.GetUntaggedVlanIds()[0])
					sw.PVID = &vid
					report.Defaults = append(report.Defaults, Default{
						Port:  iface.GetName(),
						Field: "pvid",
						Value: strconv.FormatUint(uint64(vid), 10),
					})
				}

				if len(swFacet.GetTaggedVlanIds()) > 0 {
					sw.Tagged = make([]vlan.ID, len(swFacet.GetTaggedVlanIds()))
					for i, id := range swFacet.GetTaggedVlanIds() {
						sw.Tagged[i] = vlan.ID(id)
					}
					slices.Sort(sw.Tagged)
				}

				if len(swFacet.GetUntaggedVlanIds()) > 0 {
					sw.Untagged = make([]vlan.ID, len(swFacet.GetUntaggedVlanIds()))
					for i, id := range swFacet.GetUntaggedVlanIds() {
						sw.Untagged[i] = vlan.ID(id)
					}
					slices.Sort(sw.Untagged)
				}

				if swFacet.HasIngressFiltering() {
					sw.IngressFiltering = swFacet.GetIngressFiltering()
				} else {
					sw.IngressFiltering = false
					report.Defaults = append(report.Defaults, Default{
						Port:  iface.GetName(),
						Field: "ingress_filtering",
						Value: "false",
					})
				}

				if swFacet.HasFrameAdmission() && swFacet.GetFrameAdmission() != switchingv1.FrameAdmission_FRAME_ADMISSION_UNSPECIFIED {
					switch swFacet.GetFrameAdmission() {
					case switchingv1.FrameAdmission_FRAME_ADMISSION_ALL:
						sw.Admission = bridge.All
					case switchingv1.FrameAdmission_FRAME_ADMISSION_TAGGED_ONLY:
						sw.Admission = bridge.TaggedOnly
					case switchingv1.FrameAdmission_FRAME_ADMISSION_UNTAGGED_AND_PRIORITY_TAGGED_ONLY:
						sw.Admission = bridge.UntaggedAndPriorityTaggedOnly
					}
				} else {
					sw.Admission = bridge.All
					report.Defaults = append(report.Defaults, Default{
						Port:  iface.GetName(),
						Field: "frame_admission",
						Value: "ALL",
					})
				}

				vlanCfg.Switchports[iface.GetName()] = sw

				for _, vid := range sw.Tagged {
					if _, exists := vlanCfg.Table[vid]; !exists {
						vlanCfg.Table[vid] = ""
					}
				}
				for _, vid := range sw.Untagged {
					if _, exists := vlanCfg.Table[vid]; !exists {
						vlanCfg.Table[vid] = ""
					}
				}
				if sw.PVID != nil {
					if _, exists := vlanCfg.Table[*sw.PVID]; !exists {
						vlanCfg.Table[*sw.PVID] = ""
					}
				}
			}

			bridgeCfg.VLAN = vlanCfg
		}

		cfg.Bridge = bridgeCfg
	}

	var seeds []bridge.Seed
	for _, entry := range fdb {
		if entry.GetStatus() == switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_INVALID {
			report.Skipped = append(report.Skipped, Skipped{
				Port: entry.GetInterfaceName(),
				What: "fdb_entry",
				Why:  "status is INVALID",
			})
			continue
		}
		var mac netaddr.MAC
		if entry.GetMac() != nil {
			copy(mac[:], entry.GetMac().GetOctets())
		}
		isStatic := entry.GetKind() == switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC
		seeds = append(seeds, bridge.Seed{
			FID:       vlan.ID(entry.GetVlanId()),
			MAC:       mac,
			Port:      entry.GetInterfaceName(),
			Static:    isStatic,
			LearnedAt: now,
		})
	}

	slices.Sort(report.Capabilities)
	slices.Sort(report.NoSwitchport)
	slices.SortFunc(report.Skipped, func(a, b Skipped) int {
		if r := cmp.Compare(a.Port, b.Port); r != 0 {
			return r
		}
		if r := cmp.Compare(a.What, b.What); r != 0 {
			return r
		}
		return cmp.Compare(a.Why, b.Why)
	})
	slices.SortFunc(report.Defaults, func(a, b Default) int {
		if r := cmp.Compare(a.Port, b.Port); r != 0 {
			return r
		}
		if r := cmp.Compare(a.Field, b.Field); r != 0 {
			return r
		}
		return cmp.Compare(a.Value, b.Value)
	})

	return cfg, seeds, report, nil
}
