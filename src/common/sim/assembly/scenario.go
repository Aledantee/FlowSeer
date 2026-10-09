package assembly

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// ConfigureAction owns an independent copy of cfg for scenario execution and diffing.
func ConfigureAction(node string, cfg vswitch.Config) fabric.ConfigureAction {
	return fabric.ConfigureAction{Node: node, Operation: configureOperation{config: cfg.Clone()}}
}

type configureOperation struct{ config vswitch.Config }

func (o configureOperation) Apply(f *fabric.Fabric, node string) error {
	return Configure(f, node, o.config)
}

func (o configureOperation) Diff(other fabric.Operation) []trace.Change {
	next, ok := other.(configureOperation)
	if !ok {
		return vswitch.Diff(o.config, vswitch.Config{})
	}
	return vswitch.Diff(o.config, next.config)
}

// McheckAction builds protocol migration checking for one switch port.
func McheckAction(node, port string) fabric.McheckAction {
	if port == "" {
		return fabric.McheckAction{Node: node}
	}
	return fabric.McheckAction{Node: node, Operation: mcheckOperation{port: port}}
}

type mcheckOperation struct{ port string }

func (o mcheckOperation) Apply(f *fabric.Fabric, node string) error {
	if curSwitch(f, node) == nil {
		return errs.Msgf("node %q is not a switch", node)
	}
	return fabric.Operate(f, node, func(d device.Device, now time.Time) (device.Output, error) {
		return d.(*vswitch.Switch).Mcheck(now, o.port), nil
	})
}

func (o mcheckOperation) Diff(other fabric.Operation) []trace.Change {
	next, ok := other.(mcheckOperation)
	if ok && o.port == next.port {
		return nil
	}
	return []trace.Change{{Layer: fabric.Layer, Subject: trace.Subject{Kind: "mcheck"}, Field: "port", From: operationPortFact(o.port), To: operationPortFact(next.port)}}
}

type operationPortFact string

func (f operationPortFact) TypeID() string    { return "fabric.action.payload" }
func (f operationPortFact) Canonical() string { return string(f) }
