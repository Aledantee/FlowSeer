package assembly

import (
	"strconv"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
)

type linkEndRef struct{ end *fabric.LinkEnd }

func resolveLink(c fabric.Cable, cfg fabric.Config) fabric.Link {
	r := c.Resolve(cfg.Nodes[c.A.Node].Physical[c.A.Port], cfg.Nodes[c.B.Node].Physical[c.B.Port], cfg.PhyAssumption, cableEndpointsFor(c).Canonical(), c.Evidence)
	c.Cable = r.Cable
	return fabric.Link{Cable: c, A: fabric.LinkEnd{Endpoint: c.A, EndState: r.A}, B: fabric.LinkEnd{Endpoint: c.B, EndState: r.B}}
}

func endpointLabel(ep fabric.Endpoint) string {
	if ep.Port == "" {
		return strconv.Quote(ep.Node)
	}
	return strconv.Quote(ep.Node) + ":" + strconv.Quote(ep.Port)
}

func endpointScope(ep fabric.Endpoint) analysis.Scope {
	if ep.Port == "" {
		return analysis.NodeScope(ep.Node)
	}
	return analysis.PortScope(ep.Node, ep.Port)
}
