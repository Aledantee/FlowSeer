package yang_test

import (
	"reflect"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/yang"
)

type propertyRow struct {
	ID *string
}

type propertyContainer struct {
	Rows []propertyRow
}

type propertyGroup struct {
	C *propertyContainer
}

type propertyOuter struct {
	ID *string
	C  *propertyContainer
	B  *propertyGroup
}

// propertyJSONSchemas builds one-node container paths for nested JSON tests.
func propertyJSONSchemas() (outer, rowA, rowB *yang.Schema) {
	rowA = &yang.Schema{
		Module: modA,
		Name:   "row",
		Fields: []yang.Field{{GoName: "ID", Name: "id", Type: yang.TString}},
	}
	rowB = &yang.Schema{
		Module: modB,
		Name:   "row",
		Fields: []yang.Field{{GoName: "ID", Name: "id", Type: yang.TString}},
	}
	plainContainer := &yang.Schema{
		Module: modA,
		Name:   "c",
		Fields: []yang.Field{{GoName: "Rows", Child: rowA, List: true}},
	}
	groupedContainer := &yang.Schema{
		Module: modB,
		Name:   "c",
		Fields: []yang.Field{{GoName: "Rows", Child: rowB, List: true}},
	}
	outer = &yang.Schema{
		Module: modA,
		Name:   "outer",
		Keys:   []string{"id"},
		Fields: []yang.Field{
			{GoName: "ID", Name: "id", Type: yang.TString},
			{GoName: "C", Child: plainContainer},
			{GoName: "B", Group: true, Child: &yang.Schema{
				Module: modB,
				Fields: []yang.Field{{GoName: "C", Child: groupedContainer}},
			}},
		},
	}
	return outer, rowA, rowB
}

type directRow struct {
	ID *string
}

type directGroup struct {
	Rows []directRow
}

type directOuter struct {
	ID   *string
	Rows []directRow
	B    *directGroup
}

// directJSONSchemas builds the module-distinct direct-list collision fixture.
func directJSONSchemas() (outer, rowA, rowB *yang.Schema) {
	rowA = &yang.Schema{
		Module: modA,
		Name:   "row",
		Fields: []yang.Field{{GoName: "ID", Name: "id", Type: yang.TString}},
	}
	rowB = &yang.Schema{
		Module: modB,
		Name:   "row",
		Fields: []yang.Field{{GoName: "ID", Name: "id", Type: yang.TString}},
	}
	outer = &yang.Schema{
		Module: modA,
		Name:   "outer",
		Keys:   []string{"id"},
		Fields: []yang.Field{
			{GoName: "ID", Name: "id", Type: yang.TString},
			{GoName: "Rows", Child: rowA, List: true},
			{GoName: "B", Group: true, Child: &yang.Schema{
				Module: modB,
				Fields: []yang.Field{{GoName: "Rows", Child: rowB, List: true}},
			}},
		},
	}
	return outer, rowA, rowB
}

type overrideRow struct {
	ID *string
}

type overrideContainer struct {
	Rows []overrideRow
}

type overrideOuter struct {
	ID *string
	C  *overrideContainer
}

// overrideJSONSchemas builds a non-grouped container with a module override.
func overrideJSONSchemas() (outer, row *yang.Schema) {
	row = &yang.Schema{
		Module: modA,
		Name:   "row",
		Fields: []yang.Field{{GoName: "ID", Name: "id", Type: yang.TString}},
	}
	outer = &yang.Schema{
		Module: modA,
		Name:   "outer",
		Keys:   []string{"id"},
		Fields: []yang.Field{
			{GoName: "ID", Name: "id", Type: yang.TString},
			{GoName: "C", Module: modB, Child: &yang.Schema{
				Module: modA,
				Name:   "c",
				Fields: []yang.Field{{GoName: "Rows", Child: row, List: true}},
			}},
		},
	}
	return outer, row
}

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

func TestDecodeJSONNestedDoesNotMatchBareGroupedContainer(t *testing.T) {
	type row struct{ ID *string }
	type group struct{ Container *struct{ Rows []row } }
	type outer struct {
		ID *string
		B  *group
	}
	rowSchema := &yang.Schema{
		Module: modB,
		Name:   "row",
		Fields: []yang.Field{{GoName: "ID", Name: "id", Type: yang.TString}},
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
					Child: &yang.Schema{Module: modB, Name: "c", Fields: []yang.Field{{
						GoName: "Rows", Child: rowSchema, List: true,
					}}},
				}},
			}},
		},
	}
	chain := []*yang.Schema{outerSchema, rowSchema}
	data := []byte(`{"a:outer":[{"id":"o","c":{"row":[{"id":"B"}]}}]}`)
	rows, err := yang.DecodeJSONNested[row](chain, data)
	if err != nil {
		t.Fatalf("DecodeJSONNested: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("DecodeJSONNested returned %d rows for bare grouped container, want 0", len(rows))
	}

	var got outer
	if err := yang.UnmarshalJSON7951Struct(outerSchema, []byte(`{"id":"o","c":{"row":[{"id":"B"}]}}`), &got); err != nil {
		t.Fatalf("UnmarshalJSON7951Struct: %v", err)
	}
	if got.B != nil {
		t.Fatalf("UnmarshalJSON7951Struct allocated bare grouped container: %+v", got.B)
	}
}

func TestDecodeJSONNestedPrefersConformingModuleMatch(t *testing.T) {
	type row struct{ ID *string }
	rowA := &yang.Schema{
		Module: modA,
		Name:   "row",
		Fields: []yang.Field{{GoName: "ID", Name: "id", Type: yang.TString}},
	}
	rowB := &yang.Schema{
		Module: modB,
		Name:   "row",
		Fields: []yang.Field{{GoName: "ID", Name: "id", Type: yang.TString}},
	}
	outerSchema := &yang.Schema{
		Module: modA,
		Name:   "outer",
		Keys:   []string{"id"},
		Fields: []yang.Field{
			{GoName: "ID", Name: "id", Type: yang.TString},
			{GoName: "C", Child: &yang.Schema{
				Module: modA,
				Name:   "c",
				Fields: []yang.Field{{GoName: "Rows", Child: rowA, List: true}},
			}},
			{GoName: "B", Group: true, Child: &yang.Schema{
				Module: modB,
				Fields: []yang.Field{{
					GoName: "Container",
					Child: &yang.Schema{
						Module: modB,
						Name:   "c",
						Fields: []yang.Field{{GoName: "Rows", Child: rowB, List: true}},
					},
				}},
			}},
		},
	}
	data := []byte(`{"a:outer":[{"id":"o","c":{"row":[{"id":"A"}]},"b:c":{"row":[{"id":"B"}]}}]}`)
	rows, err := yang.DecodeJSONNested[row]([]*yang.Schema{outerSchema, rowB}, data)
	if err != nil {
		t.Fatalf("DecodeJSONNested: %v", err)
	}
	if len(rows) != 1 || rows[0].Entry.ID == nil || *rows[0].Entry.ID != "B" {
		t.Fatalf("DecodeJSONNested rows = %+v, want one row from module b", rows)
	}
}

func TestDecodeXMLNestedIgnoresForeignAncestorKey(t *testing.T) {
	type row struct{ ID *string }
	outerSchema := &yang.Schema{
		Module: modA,
		Name:   "outer",
		Keys:   []string{"id"},
		Fields: []yang.Field{
			{GoName: "ID", Name: "id", Type: yang.TInt32},
			{GoName: "B", Group: true, Child: &yang.Schema{
				Module: modB,
				Fields: []yang.Field{
					{GoName: "ForeignID", Name: "id", Type: yang.TBool},
					{GoName: "Rows", Child: &yang.Schema{
						Module: modB,
						Name:   "row",
						Fields: []yang.Field{{GoName: "ID", Name: "id", Type: yang.TString}},
					}, List: true},
				},
			}},
		},
	}
	rowSchema := outerSchema.Fields[1].Child.Fields[1].Child
	data := []byte(`<outer xmlns="urn:a"><id>7</id><id xmlns="urn:b">true</id><row xmlns="urn:b"><id>B</id></row></outer>`)
	rows, err := yang.DecodeXMLNested[row]([]*yang.Schema{outerSchema, rowSchema}, data)
	if err != nil {
		t.Fatalf("DecodeXMLNested: %v", err)
	}
	if len(rows) != 1 || yang.AncestorKey(rows[0].AncestorKeys, 0, "id") != "7" {
		t.Fatalf("DecodeXMLNested ancestor keys = %+v, want id=7", rows)
	}
	if rows[0].Entry.ID == nil || *rows[0].Entry.ID != "B" {
		t.Fatalf("DecodeXMLNested entry = %+v, want id=B", rows[0].Entry)
	}
}

func TestDecodeJSONNestedRejectsCrossModuleBareRows(t *testing.T) {
	propertyOuterSchema, propertyRowA, propertyRowB := propertyJSONSchemas()
	directOuterSchema, _, directRowBSchema := directJSONSchemas()
	tests := []struct {
		name  string
		outer *yang.Schema
		data  string
		next  *yang.Schema
	}{
		{
			name:  "plain container cannot satisfy grouped target",
			outer: propertyOuterSchema,
			data:  `{"id":"o","c":{"row":[{"id":"A"}]}}`,
			next:  propertyRowB,
		},
		{
			name:  "grouped container cannot satisfy plain target",
			outer: propertyOuterSchema,
			data:  `{"id":"o","b:c":{"row":[{"id":"B"}]}}`,
			next:  propertyRowA,
		},
		{
			name:  "plain direct list cannot satisfy grouped target",
			outer: directOuterSchema,
			data:  `{"id":"o","row":[{"id":"A"}]}`,
			next:  directRowBSchema,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(`{"a:outer":[` + tc.data + `]}`)
			rows, err := yang.DecodeJSONNested[propertyRow]([]*yang.Schema{tc.outer, tc.next}, data)
			if err != nil {
				t.Fatalf("DecodeJSONNested: %v", err)
			}
			if len(rows) != 0 {
				t.Fatalf("DecodeJSONNested returned %+v, want no rows", rows)
			}
		})
	}
}

// TestDecodeJSONNestedMatchesStructDecoder checks that a chain whose target
// reaches one schema node returns the same entries that the struct decoder
// stores in that node's field. Two nodes with the same name and module under
// one ancestor entry are outside what the nested decoder can distinguish.
func TestDecodeJSONNestedMatchesStructDecoder(t *testing.T) {
	outer, rowA, rowB := propertyJSONSchemas()
	tests := []struct {
		name  string
		entry string
	}{
		{name: "plain container bare", entry: `{"id":"o","c":{"row":[{"id":"A"}]}}`},
		{name: "plain container qualified", entry: `{"id":"o","a:c":{"a:row":[{"id":"A"}]}}`},
		{name: "grouped container bare row", entry: `{"id":"o","b:c":{"row":[{"id":"B"}]}}`},
		{name: "grouped container qualified row", entry: `{"id":"o","b:c":{"b:row":[{"id":"B"}]}}`},
		{name: "both containers", entry: `{"id":"o","c":{"row":[{"id":"A"}]},"b:c":{"row":[{"id":"B"}]}}`},
		{name: "neither container", entry: `{"id":"o"}`},
		{name: "plain empty container", entry: `{"id":"o","c":{}}`},
		{name: "grouped empty container", entry: `{"id":"o","b:c":{}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var decoded propertyOuter
			if err := yang.UnmarshalJSON7951Struct(outer, []byte(tc.entry), &decoded); err != nil {
				t.Fatalf("UnmarshalJSON7951Struct: %v", err)
			}
			var wantA []propertyRow
			if decoded.C != nil {
				wantA = decoded.C.Rows
			}
			var wantB []propertyRow
			if decoded.B != nil && decoded.B.C != nil {
				wantB = decoded.B.C.Rows
			}
			data := []byte(`{"a:outer":[` + tc.entry + `]}`)
			for _, target := range []struct {
				name   string
				schema *yang.Schema
				want   []propertyRow
			}{
				{name: "module a", schema: rowA, want: wantA},
				{name: "module b", schema: rowB, want: wantB},
			} {
				t.Run(target.name, func(t *testing.T) {
					rows, err := yang.DecodeJSONNested[propertyRow]([]*yang.Schema{outer, target.schema}, data)
					if err != nil {
						t.Fatalf("DecodeJSONNested: %v", err)
					}
					if len(rows) != len(target.want) {
						t.Fatalf("got %d rows %+v, want %d rows %+v", len(rows), rows, len(target.want), target.want)
					}
					for i := range target.want {
						if rows[i].Entry.ID == nil || target.want[i].ID == nil || *rows[i].Entry.ID != *target.want[i].ID {
							t.Errorf("row %d = %+v, want %+v", i, rows[i].Entry, target.want[i])
						}
					}
				})
			}
		})
	}
}

func TestDecodeJSONNestedDirectListsMatchStructDecoder(t *testing.T) {
	outer, rowA, rowB := directJSONSchemas()
	for _, tc := range []struct {
		name  string
		entry string
	}{
		{name: "bare", entry: `{"id":"o","row":[{"id":"A"}],"b:row":[{"id":"B"}]}`},
		{name: "qualified", entry: `{"id":"o","a:row":[{"id":"A"}],"b:row":[{"id":"B"}]}`},
		{name: "neither", entry: `{"id":"o"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var decoded directOuter
			if err := yang.UnmarshalJSON7951Struct(outer, []byte(tc.entry), &decoded); err != nil {
				t.Fatalf("UnmarshalJSON7951Struct: %v", err)
			}
			var wantB []directRow
			if decoded.B != nil {
				wantB = decoded.B.Rows
			}
			data := []byte(`{"a:outer":[` + tc.entry + `]}`)
			for _, target := range []struct {
				name   string
				schema *yang.Schema
				want   []directRow
			}{
				{name: "module a", schema: rowA, want: decoded.Rows},
				{name: "module b", schema: rowB, want: wantB},
			} {
				t.Run(target.name, func(t *testing.T) {
					rows, err := yang.DecodeJSONNested[directRow]([]*yang.Schema{outer, target.schema}, data)
					if err != nil {
						t.Fatalf("DecodeJSONNested: %v", err)
					}
					if len(rows) != len(target.want) {
						t.Fatalf("got %d rows %+v, want %d rows %+v", len(rows), rows, len(target.want), target.want)
					}
					for i := range target.want {
						if rows[i].Entry.ID == nil || target.want[i].ID == nil || *rows[i].Entry.ID != *target.want[i].ID {
							t.Errorf("row %d = %+v, want %+v", i, rows[i].Entry, target.want[i])
						}
					}
				})
			}
		})
	}
}

func TestDecodeJSONNestedModuleOverrideContainerMatch(t *testing.T) {
	outer, row := overrideJSONSchemas()
	for _, tc := range []struct {
		name   string
		entry  string
		wantID string
	}{
		{name: "bare", entry: `{"id":"o","c":{"row":[{"id":"A"}]}}`, wantID: "A"},
		{name: "qualified", entry: `{"id":"o","b:c":{"row":[{"id":"A"}]}}`, wantID: "A"},
		{name: "bare empty", entry: `{"id":"o","c":{}}`},
		{name: "qualified empty", entry: `{"id":"o","b:c":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var decoded overrideOuter
			if err := yang.UnmarshalJSON7951Struct(outer, []byte(tc.entry), &decoded); err != nil {
				t.Fatalf("UnmarshalJSON7951Struct: %v", err)
			}
			var want []overrideRow
			if decoded.C != nil {
				want = decoded.C.Rows
			}
			data := []byte(`{"a:outer":[` + tc.entry + `]}`)
			rows, err := yang.DecodeJSONNested[overrideRow]([]*yang.Schema{outer, row}, data)
			if err != nil {
				t.Fatalf("DecodeJSONNested: %v", err)
			}
			if len(rows) != len(want) {
				t.Fatalf("got %d rows %+v, want %d rows %+v", len(rows), rows, len(want), want)
			}
			if tc.wantID != "" && (len(rows) != 1 || rows[0].Entry.ID == nil || *rows[0].Entry.ID != tc.wantID) {
				t.Fatalf("got rows %+v, want one row with id %q", rows, tc.wantID)
			}
			for i := range want {
				if rows[i].Entry.ID == nil || want[i].ID == nil || *rows[i].Entry.ID != *want[i].ID {
					t.Errorf("row %d = %+v, want %+v", i, rows[i].Entry, want[i])
				}
			}
		})
	}
}

func TestUnmarshalJSON7951StructRejectsGroupedNonArray(t *testing.T) {
	outer, _, _ := directJSONSchemas()
	for _, data := range []string{`{"b:row":"x"}`, `{"b:row":null}`} {
		t.Run(data, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("UnmarshalJSON7951Struct panicked: %v", recovered)
				}
			}()
			var got directOuter
			if err := yang.UnmarshalJSON7951Struct(outer, []byte(data), &got); err == nil {
				t.Fatalf("UnmarshalJSON7951Struct(%s) succeeded, want error", data)
			}
		})
	}
}
