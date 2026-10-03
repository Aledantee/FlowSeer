package openfga_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1"
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1"
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1"
	authzv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/authz/v1"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz/openfga"
)

func TestModelParsesAndRefusesMisspelledMember(t *testing.T) {
	m, err := openfga.Model()
	if err != nil {
		t.Fatalf("Model() failed: %v", err)
	}
	if m == nil {
		t.Fatal("Model() returned nil")
	}

	data, err := os.ReadFile("model.json")
	if err != nil {
		t.Fatalf("read model.json: %v", err)
	}

	misspelled := strings.Replace(string(data), `"schema_version"`, `"schema_versiooon"`, 1)
	if misspelled == string(data) {
		t.Fatal("failed to mutate schema_version in model.json")
	}

	var target openfgav1.AuthorizationModel
	opts := protojson.UnmarshalOptions{DiscardUnknown: false}
	if err := opts.Unmarshal([]byte(misspelled), &target); err == nil {
		t.Fatal("Unmarshal on misspelled JSON succeeded, want error")
	}
}

func TestModelTypesAndRelationsTable(t *testing.T) {
	model, err := openfga.Model()
	if err != nil {
		t.Fatalf("Model(): %v", err)
	}

	// expected maps type name to map of relation name -> sorted directly related user types.
	expected := map[string]map[string][]string{
		"user": {},
		"platform": {
			"admin":    {},
			"claimed":  {"user"},
			"enrolled": {"user"},
		},
		"role": {
			"assignee": {"user"},
		},
		"tenant": {
			"platform":     {"platform"},
			"partner":      {"tenant"},
			"claimed":      {"user"},
			"enrolled":     {"user"},
			"admin":        {"role#assignee", "user"},
			"active_admin": {},
			"member":       {},
			"operator":     {"role#assignee", "tenant#active_admin", "user"},
			"capturer":     {"role#assignee", "tenant#active_admin", "user"},
			"viewer":       {"role#assignee", "tenant#active_admin", "user"},
			"full_payload": {"role#assignee", "user"},
		},
		"site": {},
		"tag":  {},
		"edge": {
			"tenant":     {"tenant"},
			"administer": {"role#assignee", "user"},
			"operate":    {"role#assignee", "user"},
			"capture":    {"role#assignee", "user"},
			"view":       {"role#assignee", "user"},
		},
		"device": {
			"tenant":  {"tenant"},
			"operate": {"role#assignee", "user"},
			"view":    {"role#assignee", "user"},
		},
		"capture_session": {
			"tenant":    {"tenant"},
			"edge":      {"edge"},
			"requester": {"user"},
			"manage":    {},
			"download":  {},
		},
	}

	actualTypes := make(map[string]*openfgav1.TypeDefinition)
	for _, td := range model.GetTypeDefinitions() {
		actualTypes[td.GetType()] = td
	}

	// Verify all expected types exist
	if len(actualTypes) != len(expected) {
		t.Fatalf("model has %d types, want %d", len(actualTypes), len(expected))
	}

	for typeName, expectedRels := range expected {
		td, ok := actualTypes[typeName]
		if !ok {
			t.Errorf("type %q missing from model", typeName)
			continue
		}

		rels := td.GetRelations()
		if len(rels) != len(expectedRels) {
			t.Errorf("type %q has %d relations, want %d", typeName, len(rels), len(expectedRels))
		}

		metaRels := td.GetMetadata().GetRelations()

		for relName, expectedUsers := range expectedRels {
			if _, ok := rels[relName]; !ok {
				t.Errorf("type %q missing relation %q", typeName, relName)
				continue
			}

			var actualUsers []string
			if metaRels != nil {
				if rMeta, ok := metaRels[relName]; ok {
					for _, u := range rMeta.GetDirectlyRelatedUserTypes() {
						s := u.GetType()
						if rel := u.GetRelation(); rel != "" {
							s += "#" + rel
						}
						actualUsers = append(actualUsers, s)
					}
				}
			}
			slices.Sort(actualUsers)
			sortedExpected := slices.Clone(expectedUsers)
			slices.Sort(sortedExpected)

			if !slices.Equal(actualUsers, sortedExpected) {
				t.Errorf("type %q relation %q directly related user types = %v, want %v",
					typeName, relName, actualUsers, sortedExpected)
			}
		}
	}
}

func TestModelCoversAPIRules(t *testing.T) {
	model, err := openfga.Model()
	if err != nil {
		t.Fatalf("Model() failed: %v", err)
	}

	types := make(map[string]map[string]bool)
	for _, td := range model.GetTypeDefinitions() {
		rels := make(map[string]bool)
		for r := range td.GetRelations() {
			rels[r] = true
		}
		types[td.GetType()] = rels
	}

	methodCount := 0
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if !strings.HasPrefix(string(fd.Package()), "flowseer.api.") {
			return true
		}
		for i := range fd.Services().Len() {
			svc := fd.Services().Get(i)
			for j := range svc.Methods().Len() {
				md := svc.Methods().Get(j)
				methodCount++

				opts, ok := md.Options().(*descriptorpb.MethodOptions)
				if !ok || opts == nil || !proto.HasExtension(opts, authzv1.E_Rule) {
					t.Errorf("%s has no authorization rule option", md.FullName())
					continue
				}

				rule, ok := proto.GetExtension(opts, authzv1.E_Rule).(*authzv1.Rule)
				if !ok || rule == nil {
					t.Errorf("%s has invalid rule extension", md.FullName())
					continue
				}

				objType := rule.GetObjectType()
				rel := rule.GetRelation()

				rels, hasType := types[objType]
				if !hasType {
					t.Errorf("%s rule names unknown object type %q in model", md.FullName(), objType)
					continue
				}

				if !rels[rel] {
					t.Errorf("%s rule names unknown relation %q for object type %q in model", md.FullName(), rel, objType)
				}

				if rule.GetMode() == authzv1.RuleMode_RULE_MODE_REQUEST {
					if !rels["tenant"] {
						t.Errorf("%s is request-mode on object type %q, but model has no 'tenant' relation on that type", md.FullName(), objType)
					}
				}
			}
		}
		return true
	})

	if methodCount == 0 {
		t.Fatal("walked zero API methods in flowseer.api.")
	}
}
