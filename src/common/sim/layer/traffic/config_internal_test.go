package traffic

import (
	"slices"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
)

func TestConfigOutputPorts(t *testing.T) {
	t.Parallel()

	v99 := vlan.ID(99)
	cfg := Config{
		Mirrors: []Mirror{
			{Name: "m1", OutputPort: "1/1/24"},
			{Name: "m2", OutputPort: "1/1/4"},
			{Name: "m3", OutputPort: "1/1/24"},
			{Name: "m4", OutputVLAN: &v99},
		},
	}
	if got, want := cfg.outputPorts(), []string{"1/1/24", "1/1/4"}; !slices.Equal(got, want) {
		t.Errorf("outputPorts = %v, want %v", got, want)
	}
}
