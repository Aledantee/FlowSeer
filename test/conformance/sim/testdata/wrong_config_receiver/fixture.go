package fixture

import (
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

const LayerName trace.Layer = "fixture"

type Config struct{}

type Other struct{}

func (o Other) Normalize(_ layer.Env) Config {
	return Config{}
}

func (o Other) Validate(_ layer.Env) error {
	return nil
}

func (o Other) Clone() Config {
	return Config{}
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
