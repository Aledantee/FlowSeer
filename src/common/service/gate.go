package service

import (
	"context"

	"go.aledante.io/FlowSeer/src/common/errs"
)

var errCodeGate = errs.NewCode("service/gate")

type gateKind uint8

const (
	gateOmitted gateKind = iota
	gateFixed
	gateProbe
)

// Gate is an immutable module admission decision. Its zero value enables the
// module. Construct non-default gates with [FixedGate] or [ProbeGate]. Gate
// values are safe to copy and reuse concurrently.
type Gate struct {
	kind    gateKind
	enabled bool
	probe   GateProbe
}

// GateProbe derives one gate decision for a supervisor generation. It must
// not start module work or retain ctx after returning. A probe reused by
// sibling modules must be safe for concurrent calls.
type GateProbe func(ctx context.Context) (bool, error)

// FixedGate returns a gate with the supplied fixed decision. A generated
// environment override for the module takes precedence over enabled.
func FixedGate(enabled bool) Gate {
	return Gate{kind: gateFixed, enabled: enabled}
}

// ProbeGate returns a gate evaluated when its owning supervisor snapshots a
// generation. A generated environment override takes precedence and prevents
// the probe call. A nil probe makes the declaration invalid.
func ProbeGate(probe GateProbe) Gate {
	return Gate{kind: gateProbe, probe: probe}
}

type envLookup func(string) (string, bool)

func validateGate(path string, gate Gate) error {
	if gate.kind > gateProbe || (gate.kind == gateProbe && gate.probe == nil) {
		return errs.New().
			Code(errCodeGate).
			Attr("module_path", path).
			Msgf("module %s has an invalid gate", path)
	}
	return nil
}

func snapshotGates(
	ctx context.Context,
	modules []plannedModule,
	lookup envLookup,
	parentEnabled bool,
) ([]plannedModule, int, error) {
	snapshot := make([]plannedModule, len(modules))
	enabledLeaves := 0
	for i, module := range modules {
		snapshot[i] = module
		enabled := false
		if parentEnabled {
			var err error
			enabled, err = evaluateGate(ctx, module, lookup)
			if err != nil {
				return nil, 0, err
			}
		}
		snapshot[i].enabled = enabled
		children, leaves, err := snapshotGates(ctx, module.children, lookup, enabled)
		if err != nil {
			return nil, 0, err
		}
		snapshot[i].children = children
		enabledLeaves += leaves
		if enabled && module.leaf != nil {
			enabledLeaves++
		}
	}

	return snapshot, enabledLeaves, nil
}

func evaluateGate(ctx context.Context, module plannedModule, lookup envLookup) (bool, error) {
	if lookup != nil {
		if value, ok := lookup(module.envKey); ok {
			switch value {
			case "true":
				return true, nil
			case "false":
				return false, nil
			default:
				return false, errs.New().
					Code(errCodeGate).
					Attr("module_path", module.path).
					Attr("environment_key", module.envKey).
					Attr("gate_source", "environment").
					Msgf("module %s gate override must be true or false", module.path)
			}
		}
	}

	switch module.gate.kind {
	case gateOmitted:
		return true, nil
	case gateFixed:
		return module.gate.enabled, nil
	case gateProbe:
		enabled, err := callGateProbe(ctx, module.path, module.gate.probe)
		if err != nil {
			return false, errs.From(err).
				Code(errCodeGate).
				Attr("module_path", module.path).
				Attr("environment_key", module.envKey).
				Attr("gate_source", "probe").
				Msgf("module %s gate probe failed", module.path)
		}
		return enabled, nil
	default:
		return false, errs.New().
			Code(errCodeGate).
			Attr("module_path", module.path).
			Msgf("module %s has an invalid gate", module.path)
	}
}

func callGateProbe(ctx context.Context, path string, probe GateProbe) (enabled bool, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = newPanicDiagnostic(path, "gate", recovered)
		}
	}()
	return probe(ctx)
}
