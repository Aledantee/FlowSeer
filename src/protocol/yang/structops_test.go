package yang_test

import (
	"reflect"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/yang"
)

type nestedRowKey struct {
	OuterID string
	Name    string
}

type nestedEntry struct {
	Name  *string
	Value *string
}

type nestedRow struct {
	OuterID string
	Entry   nestedEntry
}

func TestNestedRowCodec(t *testing.T) {
	mod := &yang.Module{Name: "m", Namespace: "urn:m"}
	child := &yang.Schema{
		Module: mod,
		Name:   "inner",
		Keys:   []string{"name"},
		Fields: []yang.Field{
			{GoName: "Name", Name: "name", Type: yang.TString},
			{GoName: "Value", Name: "value", Type: yang.TString},
		},
	}
	outer := &yang.Schema{
		Module: mod,
		Name:   "outer",
		Keys:   []string{"id"},
		Fields: []yang.Field{
			{GoName: "ID", Name: "id", Type: yang.TString},
			{GoName: "Inner", Name: "inner", Child: child, List: true},
		},
	}
	chain := []*yang.Schema{outer, child}

	strPtr := func(s string) *string { return &s }

	codec := yang.NestedRowCodec[nestedEntry, nestedRow, nestedRowKey](
		chain,
		func(ancestors [][]yang.KeyValue, entry nestedEntry) nestedRow {
			return nestedRow{
				OuterID: yang.AncestorKey(ancestors, 0, "id"),
				Entry:   entry,
			}
		},
		func(r *nestedRow) *nestedEntry {
			return &r.Entry
		},
		func(r *nestedRow) nestedRowKey {
			var k nestedRowKey
			k.OuterID = r.OuterID
			if r.Entry.Name != nil {
				k.Name = *r.Entry.Name
			}
			return k
		},
	)

	xmlData := []byte(`<outer xmlns="urn:m"><id>out-1</id><inner><name>in-1</name><value>v1</value></inner></outer>`)
	jsonData := []byte(`{"m:outer":[{"id":"out-1","inner":[{"name":"in-1","value":"v1"}]}]}`)

	// Compare with hand-built rows from DecodeXMLNested / DecodeJSONNested.
	handXML, err := yang.DecodeXMLNested[nestedEntry](chain, xmlData)
	if err != nil {
		t.Fatalf("DecodeXMLNested: %v", err)
	}
	handXMLRows := []nestedRow{{
		OuterID: yang.AncestorKey(handXML[0].AncestorKeys, 0, "id"),
		Entry:   handXML[0].Entry,
	}}

	codecXMLRows, err := codec.DecodeXML(xmlData)
	if err != nil {
		t.Fatalf("codec.DecodeXML: %v", err)
	}
	if !reflect.DeepEqual(codecXMLRows, handXMLRows) {
		t.Errorf("codec XML rows = %+v, want %+v", codecXMLRows, handXMLRows)
	}

	handJSON, err := yang.DecodeJSONNested[nestedEntry](chain, jsonData)
	if err != nil {
		t.Fatalf("DecodeJSONNested: %v", err)
	}
	handJSONRows := []nestedRow{{
		OuterID: yang.AncestorKey(handJSON[0].AncestorKeys, 0, "id"),
		Entry:   handJSON[0].Entry,
	}}

	codecJSONRows, err := codec.DecodeJSON(jsonData)
	if err != nil {
		t.Fatalf("codec.DecodeJSON: %v", err)
	}
	if !reflect.DeepEqual(codecJSONRows, handJSONRows) {
		t.Errorf("codec JSON rows = %+v, want %+v", codecJSONRows, handJSONRows)
	}

	// A merge of an update that sets one leaf keeps the ancestor keys
	// and every other leaf of the base.
	base := nestedRow{
		OuterID: "out-1",
		Entry: nestedEntry{
			Name:  strPtr("in-1"),
			Value: strPtr("original-value"),
		},
	}
	update := nestedRow{
		OuterID: "",
		Entry: nestedEntry{
			Name:  strPtr("in-1"),
			Value: strPtr("updated-value"),
		},
	}
	merged := codec.Merge(base, update)
	if merged.OuterID != "out-1" {
		t.Errorf("merged OuterID = %q, want 'out-1'", merged.OuterID)
	}
	if merged.Entry.Name == nil || *merged.Entry.Name != "in-1" {
		t.Errorf("merged Name = %v, want 'in-1'", merged.Entry.Name)
	}
	if merged.Entry.Value == nil || *merged.Entry.Value != "updated-value" {
		t.Errorf("merged Value = %v, want 'updated-value'", merged.Entry.Value)
	}

	updateOnlyVal := nestedRow{
		Entry: nestedEntry{
			Value: strPtr("new-value"),
		},
	}
	merged2 := codec.Merge(base, updateOnlyVal)
	if merged2.OuterID != "out-1" {
		t.Errorf("merged2 OuterID = %q, want 'out-1'", merged2.OuterID)
	}
	if merged2.Entry.Name == nil || *merged2.Entry.Name != "in-1" {
		t.Errorf("merged2 Name = %v, want 'in-1'", merged2.Entry.Name)
	}
	if merged2.Entry.Value == nil || *merged2.Entry.Value != "new-value" {
		t.Errorf("merged2 Value = %v, want 'new-value'", merged2.Entry.Value)
	}
}

func TestVisitStructLeavesFlattensGroups(t *testing.T) {
	s := parentSchema()
	v := Parent{X: ptr(int32(1)), B: &ParentB{Y: ptr("v")}}

	var got []string
	err := yang.VisitStructLeaves(s, v, func(path yang.Path, value yang.Value) bool {
		canonical, err := value.Canonical()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, path.String()+"="+canonical)
		return true
	})
	if err != nil {
		t.Fatalf("VisitStructLeaves: %v", err)
	}
	want := []string{"/a:x=1", "/b:y=v"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("leaves = %v, want %v", got, want)
	}
}

func TestMergeAndEqualStructsDescendIntoGroups(t *testing.T) {
	s := parentSchema()
	base := Parent{X: ptr(int32(1))}
	update := Parent{B: &ParentB{Y: ptr("w")}}

	merged := yang.MergeStructs(s, base, update)
	if merged.B == nil || merged.B.Y == nil || *merged.B.Y != "w" {
		t.Fatalf("merged group = %+v, want B.Y='w'", merged.B)
	}
	if !yang.EqualStructs(merged, Parent{X: ptr(int32(1)), B: &ParentB{Y: ptr("w")}}) {
		t.Errorf("merged value = %+v", merged)
	}

	different := merged
	different.B = &ParentB{Y: ptr("different")}
	if yang.EqualStructs(merged, different) {
		t.Error("values differing in a grouped leaf compare equal")
	}
}
