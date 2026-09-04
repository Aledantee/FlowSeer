package service

import (
	"context"
	"log/slog"
	"testing"
)

func TestNormalizeConfigModuleForms(t *testing.T) {
	setup := func(context.Context) (Attempt, error) {
		return Attempt{Runner: func(context.Context) error { return nil }}, nil
	}

	tests := []struct {
		name        string
		config      Config
		wantModules int
		wantErr     bool
	}{
		{
			name: "implicit singleton",
			config: Config{
				Identity: testIdentity(),
				Setup:    setup,
			},
			wantModules: 1,
		},
		{
			name: "explicit singleton",
			config: Config{
				Identity: testIdentity(),
				Modules:  []Module{{Name: "worker", Leaf: &Leaf{Setup: setup}}},
			},
			wantModules: 1,
		},
		{
			name: "missing modules",
			config: Config{
				Identity: testIdentity(),
			},
			wantErr: true,
		},
		{
			name: "ambiguous module form",
			config: Config{
				Identity: testIdentity(),
				Setup:    setup,
				Modules:  []Module{{Name: "worker", Leaf: &Leaf{Setup: setup}}},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateDeclaration(tt.config)
			if (err != nil) != tt.wantErr {
				t.Fatalf("normalizeConfig() error = %v, want error %t", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if len(got.modules) != tt.wantModules {
				t.Errorf("module count = %d, want %d", len(got.modules), tt.wantModules)
			}
		})
	}
}

func TestNormalizeConfigHasNoSetupSideEffectsAndCopiesModules(t *testing.T) {
	called := false
	modules := []Module{{
		Name: "worker",
		Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
			called = true
			return Attempt{}, nil
		}},
	}}

	got, err := validateDeclaration(Config{Identity: testIdentity(), Modules: modules})
	if err != nil {
		t.Fatalf("normalizeConfig() error: %v", err)
	}
	if called {
		t.Fatal("normalization called module setup")
	}

	modules[0].Name = "changed"
	if got.modules[0].path != "edge/worker" {
		t.Errorf("normalized module path = %q, want %q", got.modules[0].path, "edge/worker")
	}
}

func TestNormalizeConfigRejectsInvalidIdentity(t *testing.T) {
	setup := func(context.Context) (Attempt, error) { return Attempt{}, nil }

	tests := []Identity{
		{Name: "BadName", Namespace: "flowseer", Version: "v1"},
		{Name: "edge", Version: "v1"},
		{Name: "edge", Namespace: "flowseer"},
	}
	for _, identity := range tests {
		_, err := validateDeclaration(Config{Identity: identity, Setup: setup})
		if err == nil {
			t.Errorf("normalizeConfig(%+v) succeeded, want error", identity)
		}
	}
}

func TestConfigTelemetryInjectionSurfaceIsAdditive(t *testing.T) {
	config := Config{
		Identity:   testIdentity(),
		Setup:      testSetup(),
		LogHandler: slog.DiscardHandler,
		Telemetry: TelemetryConfig{
			Endpoint: "https://collector.example",
			Signals:  TelemetryPolicy{Logs: TelemetryEnabled},
		},
	}
	if config.Logger != nil || config.TracerProvider != nil || config.MeterProvider != nil || config.Propagator != nil {
		t.Fatal("new telemetry configuration changed existing injection fields")
	}
}

func testIdentity() Identity {
	return Identity{Name: "edge", Namespace: "flowseer", Version: "v1"}
}
