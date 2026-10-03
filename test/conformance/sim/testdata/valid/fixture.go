package fixture

import (
	"time"

	_ "go.aledante.io/FlowSeer/src/common/sim/devicex"
	_ "go.aledante.io/FlowSeer/src/common/sim/fabricx"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	_ "go.aledante.io/FlowSeer/src/common/sim/layer/valid/sub"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

const LayerName trace.Layer = "fixture"

type Config struct{}

func (c Config) Normalize(layer.Env) Config {
	return c
}

func (c Config) Validate(_ layer.Env) error {
	return nil
}

func (c Config) Clone() Config {
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

func (l *Layer) Advance(now time.Time) layer.Effects {
	return layer.Effects{}
}

type fixtureFact string

type host struct{}

func (host) Age() {}
