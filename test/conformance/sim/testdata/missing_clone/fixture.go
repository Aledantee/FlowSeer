package fixture

import (
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

const LayerName trace.Layer = "fixture"

type Config struct{}

func (c Config) Normalize(_ layer.Env) Config {
	return c
}

func (c Config) Validate(_ layer.Env) error {
	return nil
}

func Diff(prev, next Config) []trace.Change {
	return nil
}
