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

func (c Config) Clone(_ int) Config {
	return c
}

func Diff(prev, next Config) []trace.Change {
	return nil
}

type Layer struct{}

func New(cfg Config, env layer.Env) (*Layer, error) {
	return &Layer{}, nil
}

func (l *Layer) Clone() *Layer {
	return &Layer{}
}

func RetentionKey(cfg Config, env layer.Env) string {
	return "key"
}
