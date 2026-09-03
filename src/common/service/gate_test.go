package service

import (
	"context"
	"errors"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
)

func TestSnapshotGatesSourcesAndOverrides(t *testing.T) {
	errProbe := errors.New("probe failed")
	tests := []struct {
		name       string
		gate       Gate
		env        map[string]string
		wantEnable bool
		wantErr    bool
		wantProbes int
	}{
		{name: "omitted", wantEnable: true},
		{name: "fixed enabled", gate: FixedGate(true), wantEnable: true},
		{name: "fixed disabled", gate: FixedGate(false)},
		{name: "probe enabled", gate: ProbeGate(func(context.Context) (bool, error) { return true, nil }), wantEnable: true, wantProbes: 1},
		{name: "probe disabled", gate: ProbeGate(func(context.Context) (bool, error) { return false, nil }), wantProbes: 1},
		{name: "probe error", gate: ProbeGate(func(context.Context) (bool, error) { return false, errProbe }), wantErr: true, wantProbes: 1},
		{name: "true overrides fixed false", gate: FixedGate(false), env: map[string]string{"FLOWSEER_EDGE_WORKER_ENABLED": "true"}, wantEnable: true},
		{name: "false overrides probe", gate: ProbeGate(func(context.Context) (bool, error) { return true, nil }), env: map[string]string{"FLOWSEER_EDGE_WORKER_ENABLED": "false"}},
		{name: "invalid override", gate: FixedGate(true), env: map[string]string{"FLOWSEER_EDGE_WORKER_ENABLED": "1"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probes := 0
			gate := tt.gate
			if gate.kind == gateProbe {
				original := gate.probe
				gate.probe = func(ctx context.Context) (bool, error) {
					probes++
					return original(ctx)
				}
			}
			modules := []plannedModule{{path: "edge/worker", envKey: "FLOWSEER_EDGE_WORKER_ENABLED", gate: gate, leaf: &plannedLeaf{}}}
			got, leaves, err := snapshotGates(context.Background(), modules, mapLookup(tt.env), true)
			if (err != nil) != tt.wantErr {
				t.Fatalf("snapshotGates() error = %v, want error %t", err, tt.wantErr)
			}
			if probes != tt.wantProbes {
				t.Errorf("probe calls = %d, want %d", probes, tt.wantProbes)
			}
			if tt.wantErr {
				if code, ok := errs.CodeOf(err); !ok || code.String() != "service/gate" {
					t.Errorf("error code = %q, %t", code, ok)
				}
				if tt.name == "probe error" && !errors.Is(err, errProbe) {
					t.Errorf("error = %v, want probe cause", err)
				}
				return
			}
			if got[0].enabled != tt.wantEnable {
				t.Errorf("enabled = %t, want %t", got[0].enabled, tt.wantEnable)
			}
			if leaves != btoi(tt.wantEnable) {
				t.Errorf("enabled leaves = %d, want %d", leaves, btoi(tt.wantEnable))
			}
		})
	}
}

func TestPreflightUsesFullNestedPathUnderEnvironmentPrefix(t *testing.T) {
	setup := testSetup()
	cfg := Config{Identity: testIdentity(), Modules: []Module{{
		Name: "ingest",
		Branch: &Branch{Children: []Module{{
			Name: "syslog",
			Gate: FixedGate(false),
			Leaf: &Leaf{Setup: setup},
		}}},
	}}}

	got, err := preflight(context.Background(), cfg, mapLookup(map[string]string{
		"FLOWSEER_EDGE_INGEST_SYSLOG_ENABLED": "true",
	}))
	if err != nil {
		t.Fatalf("preflight() error: %v", err)
	}
	leaf := got.modules[0].children[0]
	if leaf.envKey != "FLOWSEER_EDGE_INGEST_SYSLOG_ENABLED" || !leaf.enabled {
		t.Errorf("leaf key/enabled = %q/%t", leaf.envKey, leaf.enabled)
	}
}

func TestSnapshotGatesDisabledParentSkipsDescendants(t *testing.T) {
	childCalls := 0
	modules := []plannedModule{{
		path:   "edge/branch",
		envKey: "FLOWSEER_EDGE_BRANCH_ENABLED",
		gate:   FixedGate(false),
		children: []plannedModule{{
			path:   "edge/branch/leaf",
			envKey: "FLOWSEER_EDGE_BRANCH_LEAF_ENABLED",
			gate: ProbeGate(func(context.Context) (bool, error) {
				childCalls++
				return false, errors.New("must not run")
			}),
			leaf: &plannedLeaf{},
		}},
	}}

	got, leaves, err := snapshotGates(context.Background(), modules, mapLookup(map[string]string{
		"FLOWSEER_EDGE_BRANCH_LEAF_ENABLED": "invalid-but-skipped",
	}), true)
	if err != nil {
		t.Fatalf("snapshotGates() error: %v", err)
	}
	if leaves != 0 || got[0].enabled || got[0].children[0].enabled {
		t.Errorf("snapshot = %+v, leaves = %d, want disabled subtree", got, leaves)
	}
	if childCalls != 0 {
		t.Errorf("child probe calls = %d, want zero", childCalls)
	}
}

func TestPreflightRejectsAllDisabledBeforeSetup(t *testing.T) {
	setupCalls := 0
	cfg := Config{
		Identity: testIdentity(),
		Modules: []Module{{Name: "worker", Gate: FixedGate(false), Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
			setupCalls++
			return Attempt{}, nil
		}}}},
	}

	_, err := preflight(context.Background(), cfg, mapLookup(nil))
	if err == nil {
		t.Fatal("preflight() succeeded, want error")
	}
	if code, ok := errs.CodeOf(err); !ok || code.String() != "service/empty-effective-tree" {
		t.Errorf("error code = %q, %t", code, ok)
	}
	if setupCalls != 0 {
		t.Errorf("setup calls = %d, want zero", setupCalls)
	}
}

func mapLookup(values map[string]string) envLookup {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func btoi(value bool) int {
	if value {
		return 1
	}
	return 0
}
