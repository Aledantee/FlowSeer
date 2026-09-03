package service

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/emptypb"

	servicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/service/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

func TestValidateDeclarationAcceptsModuleForms(t *testing.T) {
	setup := testSetup()
	tests := []struct {
		name      string
		config    Config
		wantPaths []string
	}{
		{
			name:      "implicit singleton",
			config:    Config{Identity: testIdentity(), Setup: setup},
			wantPaths: []string{"edge"},
		},
		{
			name: "explicit singleton",
			config: Config{Identity: testIdentity(), Modules: []Module{{
				Name: "worker",
				Leaf: &Leaf{Setup: setup},
			}}},
			wantPaths: []string{"edge/worker"},
		},
		{
			name: "nested duplicate leaf names",
			config: Config{Identity: testIdentity(), Modules: []Module{
				{Name: "first", Branch: &Branch{Children: []Module{{Name: "worker", Leaf: &Leaf{Setup: setup}}}}},
				{Name: "second", Branch: &Branch{Children: []Module{{Name: "worker", Leaf: &Leaf{Setup: setup}}}}},
			}},
			wantPaths: []string{"edge/first", "edge/first/worker", "edge/second", "edge/second/worker"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			declaration, err := validateDeclaration(tt.config)
			if err != nil {
				t.Fatalf("validateDeclaration() error: %v", err)
			}
			if got := declaration.modulePaths(); !equalStrings(got, tt.wantPaths) {
				t.Errorf("module paths = %v, want %v", got, tt.wantPaths)
			}
		})
	}
}

func TestValidateDeclarationRejectsInvalidTreesBeforeGateOrSetup(t *testing.T) {
	setupCalls := 0
	probeCalls := 0
	setup := func(context.Context) (Attempt, error) {
		setupCalls++
		return Attempt{Runner: func(context.Context) error { return nil }}, nil
	}
	probe := ProbeGate(func(context.Context) (bool, error) {
		probeCalls++
		return true, nil
	})
	longSegment := strings.Repeat("a", maxIdentityLength)

	tests := []struct {
		name string
		cfg  Config
		code string
	}{
		{name: "empty", cfg: Config{Identity: testIdentity()}, code: "service/module-shape"},
		{
			name: "implicit and explicit",
			cfg:  Config{Identity: testIdentity(), Setup: setup, Modules: []Module{{Name: "worker", Leaf: &Leaf{Setup: setup}}}},
			code: "service/module-shape",
		},
		{
			name: "duplicate sibling",
			cfg: Config{Identity: testIdentity(), Modules: []Module{
				{Name: "worker", Gate: probe, Leaf: &Leaf{Setup: setup}},
				{Name: "worker", Leaf: &Leaf{Setup: setup}},
			}},
			code: "service/module-collision",
		},
		{
			name: "leaf and branch",
			cfg: Config{Identity: testIdentity(), Modules: []Module{{
				Name: "worker", Leaf: &Leaf{Setup: setup}, Branch: &Branch{Children: []Module{{Name: "child", Leaf: &Leaf{Setup: setup}}}},
			}}},
			code: "service/module-shape",
		},
		{
			name: "neither leaf nor branch",
			cfg:  Config{Identity: testIdentity(), Modules: []Module{{Name: "worker"}}},
			code: "service/module-shape",
		},
		{
			name: "empty branch",
			cfg:  Config{Identity: testIdentity(), Modules: []Module{{Name: "worker", Branch: &Branch{}}}},
			code: "service/module-shape",
		},
		{
			name: "invalid name",
			cfg:  Config{Identity: testIdentity(), Modules: []Module{{Name: "BadName", Leaf: &Leaf{Setup: setup}}}},
			code: "service/module-name",
		},
		{
			name: "nil probe",
			cfg:  Config{Identity: testIdentity(), Modules: []Module{{Name: "worker", Gate: ProbeGate(nil), Leaf: &Leaf{Setup: setup}}}},
			code: "service/gate",
		},
		{
			name: "disabled parent still validates child",
			cfg: Config{Identity: testIdentity(), Modules: []Module{{
				Name: "branch", Gate: FixedGate(false), Branch: &Branch{Children: []Module{{Name: "BadName", Leaf: &Leaf{Setup: setup}}}},
			}}},
			code: "service/module-name",
		},
		{
			name: "path too long",
			cfg: Config{Identity: testIdentity(), Modules: []Module{{
				Name:   longSegment,
				Branch: &Branch{Children: []Module{{Name: longSegment, Leaf: &Leaf{Setup: setup}}}},
			}}},
			code: "service/module-name",
		},
		{
			name: "environment key collision",
			cfg: Config{Identity: testIdentity(), Modules: []Module{
				{Name: "a_b", Leaf: &Leaf{Setup: setup}},
				{Name: "a", Branch: &Branch{Children: []Module{{Name: "b", Leaf: &Leaf{Setup: setup}}}}},
			}},
			code: "service/module-collision",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupCalls = 0
			probeCalls = 0
			_, err := validateDeclaration(tt.cfg)
			if err == nil {
				t.Fatal("validateDeclaration() succeeded, want error")
			}
			code, ok := errs.CodeOf(err)
			if !ok || code.String() != tt.code {
				t.Errorf("error code = %q, %t, want %q", code, ok, tt.code)
			}
			if setupCalls != 0 || probeCalls != 0 {
				t.Errorf("side effects: setup=%d probe=%d, want zero", setupCalls, probeCalls)
			}
			if _, ok := errs.Attributes(err)["module_path"]; !ok {
				t.Errorf("error attributes = %v, want module_path", errs.Attributes(err))
			}
		})
	}
}

func TestDerivedModuleIdentitiesAreCollisionSafe(t *testing.T) {
	if got, other := encodeSubjectToken("edge/a_b"), encodeSubjectToken("edge/a/b"); got == other {
		t.Fatalf("subject tokens collide: %q", got)
	}
	if got := len(durableConsumerName("edge/" + strings.Repeat("a", 200))); got > maxDurableNameLength {
		t.Errorf("durable name length = %d, want <= %d", got, maxDurableNameLength)
	}

	identities := newDerivedIdentitySet()
	if err := identities.add("durable_name", "same", "edge/first"); err != nil {
		t.Fatalf("first identity: %v", err)
	}
	if err := identities.add("durable_name", "same", "edge/second"); err == nil {
		t.Fatal("colliding derived identity succeeded")
	}
}

func TestValidateAttemptHandlersExactStaticDeclaration(t *testing.T) {
	subscriptions := []plannedSubscription{
		{kind: servicev1.MessageKind_MESSAGE_KIND_COMMAND, fullName: "google.protobuf.Empty"},
		{kind: servicev1.MessageKind_MESSAGE_KIND_EVENT, fullName: "google.protobuf.Empty"},
	}
	handle := func(context.Context, proto.Message) error { return nil }
	tests := []struct {
		name     string
		handlers []Handler
		wantErr  bool
	}{
		{
			name: "exact",
			handlers: []Handler{
				{Kind: servicev1.MessageKind_MESSAGE_KIND_COMMAND, Message: &emptypb.Empty{}, Handle: handle},
				{Kind: servicev1.MessageKind_MESSAGE_KIND_EVENT, Message: &emptypb.Empty{}, Handle: handle},
			},
		},
		{name: "missing", handlers: []Handler{{Kind: servicev1.MessageKind_MESSAGE_KIND_COMMAND, Message: &emptypb.Empty{}, Handle: handle}}, wantErr: true},
		{
			name: "extra",
			handlers: []Handler{
				{Kind: servicev1.MessageKind_MESSAGE_KIND_COMMAND, Message: &emptypb.Empty{}, Handle: handle},
				{Kind: servicev1.MessageKind_MESSAGE_KIND_EVENT, Message: &emptypb.Empty{}, Handle: handle},
				{Kind: servicev1.MessageKind_MESSAGE_KIND_REPLY, Message: &emptypb.Empty{}, Handle: handle},
			},
			wantErr: true,
		},
		{
			name: "duplicate",
			handlers: []Handler{
				{Kind: servicev1.MessageKind_MESSAGE_KIND_COMMAND, Message: &emptypb.Empty{}, Handle: handle},
				{Kind: servicev1.MessageKind_MESSAGE_KIND_COMMAND, Message: &emptypb.Empty{}, Handle: handle},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAttemptHandlers("edge/worker", subscriptions, tt.handlers)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateAttemptHandlers() error = %v, want error %t", err, tt.wantErr)
			}
			if err != nil {
				if code, ok := errs.CodeOf(err); !ok || code.String() != "service/handler-mismatch" {
					t.Errorf("error code = %q, %t", code, ok)
				}
			}
		})
	}
}

func TestValidateAttemptHandlersRejectsAliasAsHandlerName(t *testing.T) {
	setup := testSetup()
	declaration, err := validateDeclaration(Config{
		Identity: testIdentity(),
		Modules: []Module{{
			Name: "worker",
			Leaf: &Leaf{Setup: setup, Subscriptions: []Subscription{{
				Kind:    servicev1.MessageKind_MESSAGE_KIND_COMMAND,
				Message: &durationpb.Duration{},
				Aliases: []protoreflect.FullName{"google.protobuf.Empty"},
			}}},
		}},
	})
	if err != nil {
		t.Fatalf("validateDeclaration() error: %v", err)
	}

	handler := Handler{
		Kind:    servicev1.MessageKind_MESSAGE_KIND_COMMAND,
		Message: &emptypb.Empty{},
		Handle:  func(context.Context, proto.Message) error { return nil },
	}
	if err := validateAttemptHandlers("edge/worker", declaration.modules[0].leaf.subscriptions, []Handler{handler}); err == nil {
		t.Fatal("alias-named handler succeeded")
	}
}

func TestRunRejectsHandlerMismatchBeforeRunnerStarts(t *testing.T) {
	runnerCalls := 0
	err := run(context.Background(), Config{
		Identity: testIdentity(),
		Modules: []Module{{
			Name: "worker",
			Leaf: &Leaf{
				Subscriptions: []Subscription{{Kind: servicev1.MessageKind_MESSAGE_KIND_COMMAND, Message: &emptypb.Empty{}}},
				Setup: func(context.Context) (Attempt, error) {
					return Attempt{Runner: func(context.Context) error {
						runnerCalls++
						return nil
					}}, nil
				},
			},
		}},
	})
	if err == nil {
		t.Fatal("run() succeeded, want handler mismatch")
	}
	if code, ok := errs.CodeOf(err); !ok || code.String() != "service/handler-mismatch" {
		t.Errorf("error code = %q, %t", code, ok)
	}
	if runnerCalls != 0 {
		t.Errorf("runner calls = %d, want zero", runnerCalls)
	}
}

func testSetup() SetupFunc {
	return func(context.Context) (Attempt, error) {
		return Attempt{Runner: func(context.Context) error { return nil }}, nil
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
