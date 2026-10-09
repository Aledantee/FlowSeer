package fabric

import (
	"errors"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func TestRunHalfImportsNoDeviceKind(t *testing.T) {
	construction := map[string]bool{"config.go": true, "diff.go": true, "configure.go": true, "derive.go": true, "assemble.go": true, "scenario.go": true}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || construction[name] {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if path == "go.aledante.io/FlowSeer/src/common/sim/layer" || strings.Contains(path, "/sim/layer/") || strings.Contains(path, "/sim/device/") {
				t.Errorf("%s imports %s", name, path)
			}
		}
	}
}

type linkErrorDevice struct {
	device.Device
	ports port.Table
	err   error
}

func (d *linkErrorDevice) NextWake() (time.Time, bool) { return time.Time{}, false }

func (d *linkErrorDevice) Ports() port.Table { return d.ports }
func (d *linkErrorDevice) LinkChange(time.Time, string, device.Link) (device.Output, error) {
	return device.Output{}, d.err
}

func TestLinkChangeErrorReachesNew(t *testing.T) {
	ports, err := port.NewBuilder().Add(port.Port{Name: "p", Kind: port.Physical, AdminStatus: port.Up}).Build()
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("built device rejected link")
	node := builtNode{device: &linkErrorDevice{ports: ports, err: want}}
	for _, path := range []string{"New", "Derive"} {
		t.Run(path, func(t *testing.T) {
			f, err := newTopology(topology{nodes: map[string]builtNode{"n": node}})
			if !errors.Is(err, want) || f != nil {
				t.Fatalf("built topology = %v, %v, want nil, %v", f, err, want)
			}
		})
	}
	t.Run("Configure", func(t *testing.T) {
		f, err := newTopology(topology{nodes: map[string]builtNode{}})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.replace("n", node, nil, nil, nil); !errors.Is(err, want) {
			t.Fatalf("replace error = %v, want %v", err, want)
		}
	})
}
