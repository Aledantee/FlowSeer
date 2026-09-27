package yang_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/yang"
)

func TestSchemaModuleResolution(t *testing.T) {
	type container struct {
		Leaf *string
	}

	modM := &yang.Module{Name: "m", Namespace: "urn:m"}
	modAug := &yang.Module{Name: "aug", Namespace: "urn:aug"}

	// A schema whose Module is &Module{Name: "m", Namespace: "urn:m"}, with a
	// field whose Module is nil, decodes <c xmlns="urn:m"><leaf>1</leaf></c>.
	schemaInherited := &yang.Schema{
		Module: modM,
		Name:   "c",
		Fields: []yang.Field{
			{GoName: "Leaf", Name: "leaf", Type: yang.TString},
		},
	}

	t.Run("inherited field module decodes XML", func(t *testing.T) {
		var got container
		xmlData := []byte(`<c xmlns="urn:m"><leaf>1</leaf></c>`)
		if err := yang.UnmarshalXMLStruct(schemaInherited, xmlData, &got); err != nil {
			t.Fatalf("UnmarshalXMLStruct: %v", err)
		}
		if got.Leaf == nil || *got.Leaf != "1" {
			t.Errorf("got %v, want Leaf='1'", got.Leaf)
		}
	})

	// A field whose Module is &Module{Name: "aug", Namespace: "urn:aug"}
	// decodes both JSON member aug:leaf and bare leaf. It does not
	// match an XML element in urn:m.
	schemaAug := &yang.Schema{
		Module: modM,
		Name:   "c",
		Fields: []yang.Field{
			{GoName: "Leaf", Name: "leaf", Module: modAug, Type: yang.TString},
		},
	}

	t.Run("augmented field module decodes qualified JSON", func(t *testing.T) {
		var got container
		jsonData := []byte(`{"aug:leaf":"1"}`)
		if err := yang.UnmarshalJSON7951Struct(schemaAug, jsonData, &got); err != nil {
			t.Fatalf("UnmarshalJSON7951Struct: %v", err)
		}
		if got.Leaf == nil || *got.Leaf != "1" {
			t.Errorf("got %v, want Leaf='1'", got.Leaf)
		}
	})

	t.Run("augmented field module decodes bare JSON", func(t *testing.T) {
		var got container
		jsonData := []byte(`{"leaf":"1"}`)
		if err := yang.UnmarshalJSON7951Struct(schemaAug, jsonData, &got); err != nil {
			t.Fatalf("UnmarshalJSON7951Struct: %v", err)
		}
		if got.Leaf == nil || *got.Leaf != "1" {
			t.Errorf("got %v, want Leaf='1'", got.Leaf)
		}
	})

	t.Run("augmented field does not match XML in parent namespace", func(t *testing.T) {
		var got container
		xmlData := []byte(`<c xmlns="urn:m"><leaf>1</leaf></c>`)
		if err := yang.UnmarshalXMLStruct(schemaAug, xmlData, &got); err != nil {
			t.Fatalf("UnmarshalXMLStruct: %v", err)
		}
		if got.Leaf != nil {
			t.Errorf("got Leaf=%q, want nil", *got.Leaf)
		}
	})

	// A Schema with a nil Module decodes like one with empty module strings.
	schemaNilMod := &yang.Schema{
		Name: "c",
		Fields: []yang.Field{
			{GoName: "Leaf", Name: "leaf", Type: yang.TString},
		},
	}

	t.Run("nil module schema decodes XML and JSON with bare names", func(t *testing.T) {
		var gotXML container
		if err := yang.UnmarshalXMLStruct(schemaNilMod, []byte(`<c><leaf>1</leaf></c>`), &gotXML); err != nil {
			t.Fatalf("UnmarshalXMLStruct: %v", err)
		}
		if gotXML.Leaf == nil || *gotXML.Leaf != "1" {
			t.Errorf("got Leaf=%v, want '1'", gotXML.Leaf)
		}

		var gotJSON container
		if err := yang.UnmarshalJSON7951Struct(schemaNilMod, []byte(`{"leaf":"1"}`), &gotJSON); err != nil {
			t.Fatalf("UnmarshalJSON7951Struct: %v", err)
		}
		if gotJSON.Leaf == nil || *gotJSON.Leaf != "1" {
			t.Errorf("got Leaf=%v, want '1'", gotJSON.Leaf)
		}
	})
}
