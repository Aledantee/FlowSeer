package yang

import (
	"reflect"
	"strings"
	"testing"
)

func TestWalkFieldsYieldsPlainAndGroupedFieldsInSchemaOrder(t *testing.T) {
	modA := &Module{Name: "a", Namespace: "urn:a"}
	modB := &Module{Name: "b", Namespace: "urn:b"}
	modC := &Module{Name: "c", Namespace: "urn:c"}
	groupB := &Schema{
		Module: modB,
		Fields: []Field{
			{GoName: "BLeaf", Name: "b-leaf", Type: TString},
			{GoName: "BSecond", Name: "b-second", Type: TString},
		},
	}
	groupC := &Schema{
		Module: modC,
		Fields: []Field{{GoName: "CLeaf", Name: "c-leaf", Type: TString}},
	}
	schema := &Schema{
		Module: modA,
		Fields: []Field{
			{GoName: "Plain", Name: "plain", Type: TString},
			{GoName: "B", Group: true, Child: groupB},
			{GoName: "C", Group: true, Child: groupC},
		},
	}

	var got []string
	err := walkFields(schema, func(field *Field, owner *Schema, group *Field) error {
		behind := "-"
		if group != nil {
			behind = group.GoName
		}
		got = append(got, field.Name+"@"+owner.Module.Name+"/"+behind)
		return nil
	})
	if err != nil {
		t.Fatalf("walkFields: %v", err)
	}

	want := []string{"plain@a/-", "b-leaf@b/B", "b-second@b/B", "c-leaf@c/C"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fields = %v, want %v", got, want)
	}
}

func TestGroupValueAllocation(t *testing.T) {
	type group struct {
		Leaf *string
	}
	type parent struct {
		Group *group
	}
	field := &Field{GoName: "Group", Group: true, Child: &Schema{}}
	value := parent{}
	rv := reflect.ValueOf(&value).Elem()

	got, err := groupValue(rv, field, false)
	if err != nil {
		t.Fatalf("groupValue without allocation: %v", err)
	}
	if got.IsValid() {
		t.Errorf("groupValue without allocation = %v, want invalid", got)
	}
	if value.Group != nil {
		t.Fatal("groupValue without allocation allocated the group")
	}

	got, err = groupValue(rv, field, true)
	if err != nil {
		t.Fatalf("groupValue with allocation: %v", err)
	}
	if !got.IsValid() || got.Type() != reflect.TypeOf(group{}) {
		t.Errorf("groupValue with allocation = %v, want group struct", got)
	}
	if value.Group == nil {
		t.Fatal("groupValue with allocation did not allocate the group")
	}

	allocated := value.Group
	if _, err := groupValue(rv, field, true); err != nil {
		t.Fatalf("groupValue for existing group: %v", err)
	}
	if value.Group != allocated {
		t.Fatal("groupValue replaced an existing group")
	}
}

func TestWalkFieldsRejectsNestedGroups(t *testing.T) {
	modA := &Module{Name: "a", Namespace: "urn:a"}
	modB := &Module{Name: "b", Namespace: "urn:b"}
	modC := &Module{Name: "c", Namespace: "urn:c"}
	schema := &Schema{
		Module: modA,
		Fields: []Field{{
			GoName: "B",
			Group:  true,
			Child: &Schema{
				Module: modB,
				Fields: []Field{{
					GoName: "C",
					Group:  true,
					Child:  &Schema{Module: modC},
				}},
			},
		}},
	}

	err := walkFields(schema, func(*Field, *Schema, *Field) error { return nil })
	if err == nil {
		t.Fatal("walkFields accepted a nested group")
	}
	if !strings.Contains(err.Error(), "B") || !strings.Contains(err.Error(), "C") {
		t.Errorf("nested group error = %q, want both field names", err)
	}
}
