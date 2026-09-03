package yang_test

import (
	"reflect"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/yang"
)

func TestNestedAncestorKeysCanonical(t *testing.T) {
	type entry struct{ Name *string }
	child := &yang.Schema{Module: "m", Namespace: "urn:m", Name: "inner", Fields: []yang.Field{
		{GoName: "Name", Name: "name", Type: yang.TString},
	}}
	outer := &yang.Schema{Module: "m", Namespace: "urn:m", Name: "outer", Keys: []string{"id"}, Fields: []yang.Field{
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
	child := &yang.Schema{Module: "m", Name: "inner", Fields: []yang.Field{{GoName: "Name", Name: "name", Type: yang.TString}}}
	outer := &yang.Schema{Module: "m", Name: "outer", Fields: []yang.Field{{GoName: "Inner", Name: "inner", Child: child, List: true}}}
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
