package yang_test

import (
	"reflect"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/yang"
)

func TestNestedAncestorKeysCanonical(t *testing.T) {
	type entry struct{ Name *string }
	mod := &yang.Module{Name: "m", Namespace: "urn:m"}
	child := &yang.Schema{Module: mod, Name: "inner", Fields: []yang.Field{
		{GoName: "Name", Name: "name", Type: yang.TString},
	}}
	outer := &yang.Schema{Module: mod, Name: "outer", Keys: []string{"id"}, Fields: []yang.Field{
		{GoName: "ID", Name: "id", Type: yang.TUint16},
		{GoName: "Inner", Name: "inner", Child: child, List: true},
	}}
	chain := []*yang.Schema{outer, child}
	xmlRows, err := yang.DecodeXMLNested[entry](chain, []byte(`<outer xmlns="urn:m"><id>001</id><inner><name>x</name></inner></outer>`))
	if err != nil {
		t.Fatal(err)
	}
	jsonRows, err := yang.DecodeJSONNested[entry](chain, []byte(`{"m:outer":[{"id":"001","inner":[{"name":"x"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(xmlRows) != 1 || len(jsonRows) != 1 {
		t.Fatalf("got XML rows %d and JSON rows %d, want one each", len(xmlRows), len(jsonRows))
	}
	want := [][]yang.KeyValue{{{Name: "id", Value: "1"}}}
	if !reflect.DeepEqual(xmlRows[0].AncestorKeys, want) || !reflect.DeepEqual(jsonRows[0].AncestorKeys, want) {
		t.Errorf("got XML keys %v and JSON keys %v, want %v", xmlRows[0].AncestorKeys, jsonRows[0].AncestorKeys, want)
	}
	_, err = yang.DecodeXMLNested[entry](chain, []byte(`<outer xmlns="urn:m"><id>65536</id><inner><name>x</name></inner></outer>`))
	if code, _ := errs.CodeOf(err); code != yang.ErrCodeValueRange {
		t.Errorf("got %v, want ancestor key range error", err)
	}
}

func TestDecodeJSONNestedRejectsNull(t *testing.T) {
	type entry struct{ Name *string }
	mod := &yang.Module{Name: "m"}
	child := &yang.Schema{Module: mod, Name: "inner", Fields: []yang.Field{{GoName: "Name", Name: "name", Type: yang.TString}}}
	outer := &yang.Schema{Module: mod, Name: "outer", Fields: []yang.Field{{GoName: "Inner", Name: "inner", Child: child, List: true}}}
	chain := []*yang.Schema{outer, child}
	for _, tc := range []struct{ name, data string }{
		{"payload", `null`},
		{"outer list", `{"m:outer":null}`},
		{"ancestor entry", `{"m:outer":[null]}`},
		{"inner list", `{"m:outer":[{"inner":null}]}`},
		{"inner entry", `{"m:outer":[{"inner":[null]}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := yang.DecodeJSONNested[entry](chain, []byte(tc.data))
			if code, _ := errs.CodeOf(err); code != yang.ErrCodeValueParse {
				t.Errorf("got %v, want value parse error", err)
			}
		})
	}
	for _, data := range []string{`{}`, `{"m:outer":[]}`, `{"m:outer":[{"inner":[]}]}`} {
		rows, err := yang.DecodeJSONNested[entry](chain, []byte(data))
		if err != nil || len(rows) != 0 {
			t.Errorf("decode %s: got %d rows, %v; want no rows and nil error", data, len(rows), err)
		}
	}
}

func TestDecodeNestedListThroughGroupContainer(t *testing.T) {
	type entry struct {
		ID    *string
		Value *string
	}
	outerSchema := &yang.Schema{
		Module: modA,
		Name:   "outer",
		Keys:   []string{"id"},
		Fields: []yang.Field{
			{GoName: "ID", Name: "id", Type: yang.TString},
			{GoName: "B", Group: true, Child: &yang.Schema{
				Module: modB,
				Fields: []yang.Field{{
					GoName: "Container",
					Child: &yang.Schema{
						Module: modB,
						Name:   "c",
						Fields: []yang.Field{{
							GoName: "Row",
							Child: &yang.Schema{
								Module: modB,
								Name:   "row",
								Keys:   []string{"id"},
								Fields: []yang.Field{
									{GoName: "ID", Name: "id", Type: yang.TString},
									{GoName: "Value", Name: "value", Type: yang.TString},
								},
							},
							List: true,
						}},
					},
				}},
			}},
		},
	}
	rowSchema := outerSchema.Fields[1].Child.Fields[0].Child.Fields[0].Child
	chain := []*yang.Schema{outerSchema, rowSchema}

	xmlData := []byte(`<outer xmlns="urn:a"><id>outer-1</id><c xmlns="urn:b"><row><id>row-1</id><value>v1</value></row><row><id>row-2</id><value>v2</value></row></c></outer>`)
	jsonData := []byte(`{"a:outer":[{"id":"outer-1","b:c":{"row":[{"id":"row-1","value":"v1"},{"id":"row-2","value":"v2"}]}}]}`)

	for _, tc := range []struct {
		name   string
		data   []byte
		decode func([]byte) ([]yang.NestedEntry[entry], error)
	}{
		{name: "XML", data: xmlData, decode: func(data []byte) ([]yang.NestedEntry[entry], error) {
			return yang.DecodeXMLNested[entry](chain, data)
		}},
		{name: "JSON", data: jsonData, decode: func(data []byte) ([]yang.NestedEntry[entry], error) {
			return yang.DecodeJSONNested[entry](chain, data)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := tc.decode(tc.data)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(rows) != 2 {
				t.Fatalf("got %d rows, want 2", len(rows))
			}
			for i, row := range rows {
				if got := yang.AncestorKey(row.AncestorKeys, 0, "id"); got != "outer-1" {
					t.Errorf("row %d ancestor id = %q, want outer-1", i, got)
				}
				if row.Entry.ID == nil || *row.Entry.ID != "row-"+string(rune('1'+i)) {
					t.Errorf("row %d id = %v", i, row.Entry.ID)
				}
				if row.Entry.Value == nil || *row.Entry.Value != "v"+string(rune('1'+i)) {
					t.Errorf("row %d value = %v", i, row.Entry.Value)
				}
			}
		})
	}
}
