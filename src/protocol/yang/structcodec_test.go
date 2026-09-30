package yang_test

import (
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/yang"
)

// The test model mirrors the shape yanggen emits: pointer leaves,
// slice lists, a presence container, and an augmented-in leaf from a
// foreign module.

type testServer struct {
	Name  *string
	Port  *uint16
	Owner *string // augmented in by test-aug
	Tags  []string
	Extra *testExtra
}

type testExtra struct {
	Note *string
}

type groupedCollectionsParent struct {
	B *groupedCollections
}

type groupedCollections struct {
	Rows   []groupedCollectionRow
	Values []string
}

type groupedCollectionRow struct {
	ID *string
}

func groupedCollectionsSchema() *yang.Schema {
	return &yang.Schema{
		Module: modA,
		Name:   "p",
		Fields: []yang.Field{{
			GoName: "B",
			Group:  true,
			Child: &yang.Schema{
				Module: modB,
				Fields: []yang.Field{
					{
						GoName: "Rows",
						Child: &yang.Schema{
							Module: modB,
							Name:   "row",
							Fields: []yang.Field{{GoName: "ID", Name: "id", Type: yang.TString}},
						},
						List: true,
					},
					{GoName: "Values", Name: "values", LeafList: true, Type: yang.TString},
				},
			},
		}},
	}
}

type groupedContainerParent struct {
	B *groupedContainerGroup
}

type groupedContainerGroup struct {
	Box *groupedContainer
}

type groupedContainer struct {
	L *string
}

func groupedContainerSchema() *yang.Schema {
	return &yang.Schema{
		Module: modA,
		Name:   "p",
		Fields: []yang.Field{{
			GoName: "B",
			Group:  true,
			Child: &yang.Schema{
				Module: modB,
				Fields: []yang.Field{{
					GoName: "Box",
					Child: &yang.Schema{
						Module: modB,
						Name:   "box",
						Fields: []yang.Field{{GoName: "L", Name: "l", Type: yang.TString}},
					},
				}},
			},
		}},
	}
}

var (
	modTestMain = &yang.Module{Name: "test-main", Namespace: "urn:test:main"}
	modTestAug  = &yang.Module{Name: "test-aug", Namespace: "urn:test:aug"}
)

func serverSchema() *yang.Schema {
	return &yang.Schema{
		Module: modTestMain,
		Name:   "server",
		Keys:   []string{"name"},
		Fields: []yang.Field{
			{GoName: "Name", Name: "name", Type: &yang.Type{Kind: yang.TypeString}},
			{GoName: "Port", Name: "port", Type: &yang.Type{Kind: yang.TypeUint16}},
			{
				GoName: "Owner", Name: "owner",
				Module: modTestAug,
				Type:   &yang.Type{Kind: yang.TypeString},
			},
			{GoName: "Tags", Name: "tags", LeafList: true, Type: &yang.Type{Kind: yang.TypeString}},
			{
				GoName: "Extra", Name: "extra",
				Child: &yang.Schema{
					Module: modTestMain, Name: "extra", Presence: true,
					Fields: []yang.Field{
						{GoName: "Note", Name: "note", Type: &yang.Type{Kind: yang.TypeString}},
					},
				},
			},
		},
	}
}

func str(s string) *string { return &s }

func u16(v uint16) *uint16 { return &v }

func sampleServer() testServer {
	return testServer{
		Name:  str("edge-1"),
		Port:  u16(8080),
		Owner: str("netops"),
		Tags:  []string{"prod", "edge"},
		Extra: &testExtra{Note: str("rack 7")},
	}
}

func TestStructXMLRoundTrip(t *testing.T) {
	s := serverSchema()
	xmlBytes, err := yang.MarshalXMLStruct(s, sampleServer())
	if err != nil {
		t.Fatalf("MarshalXMLStruct: %v", err)
	}
	want := `<server xmlns="urn:test:main">` +
		`<name>edge-1</name><port>8080</port>` +
		`<owner xmlns="urn:test:aug">netops</owner>` +
		`<tags>prod</tags><tags>edge</tags>` +
		`<extra><note>rack 7</note></extra></server>`
	if string(xmlBytes) != want {
		t.Errorf("XML = %s\nwant %s", xmlBytes, want)
	}

	var got testServer
	if err := yang.UnmarshalXMLStruct(s, xmlBytes, &got); err != nil {
		t.Fatalf("UnmarshalXMLStruct: %v", err)
	}
	if !yang.EqualStructs(got, sampleServer()) {
		t.Errorf("XML round-trip = %+v, want %+v", got, sampleServer())
	}
}

func TestStructXMLDecodeSkipsUnknownAndWrapping(t *testing.T) {
	s := serverSchema()
	payload := `<rpc-reply><data><servers xmlns="urn:test:main">` +
		`<server><name>a</name><future-leaf>x</future-leaf><port>1</port></server>` +
		`<server><name>b</name><port>2</port></server>` +
		`</servers></data></rpc-reply>`
	rows, err := yang.DecodeXMLList[testServer](s, []byte(payload))
	if err != nil {
		t.Fatalf("DecodeXMLList: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("decoded %d rows, want 2", len(rows))
	}
	if *rows[0].Name != "a" || *rows[0].Port != 1 || *rows[1].Name != "b" {
		t.Errorf("rows = %+v", rows)
	}
}

func TestStructJSONRoundTrip(t *testing.T) {
	s := serverSchema()
	jsonBytes, err := yang.MarshalJSON7951Struct(s, sampleServer())
	if err != nil {
		t.Fatalf("MarshalJSON7951Struct: %v", err)
	}
	want := `{"name":"edge-1","port":8080,"test-aug:owner":"netops",` +
		`"tags":["prod","edge"],"extra":{"note":"rack 7"}}`
	if string(jsonBytes) != want {
		t.Errorf("JSON = %s\nwant %s", jsonBytes, want)
	}

	var got testServer
	if err := yang.UnmarshalJSON7951Struct(s, jsonBytes, &got); err != nil {
		t.Fatalf("UnmarshalJSON7951Struct: %v", err)
	}
	if !yang.EqualStructs(got, sampleServer()) {
		t.Errorf("JSON round-trip = %+v, want %+v", got, sampleServer())
	}

	// Decoders accept bare member names where the wire peer drops the
	// module qualifier.
	bare := strings.Replace(string(jsonBytes), "test-aug:owner", "owner", 1)
	var lenient testServer
	if err := yang.UnmarshalJSON7951Struct(s, []byte(bare), &lenient); err != nil {
		t.Fatalf("UnmarshalJSON7951Struct(bare): %v", err)
	}
	if lenient.Owner == nil || *lenient.Owner != "netops" {
		t.Errorf("bare-qualified owner not decoded: %+v", lenient)
	}
}

func TestGroupedStructXMLRoundTrip(t *testing.T) {
	s := parentSchema()
	wantValue := Parent{X: ptr(int32(1)), B: &ParentB{Y: ptr("v")}}
	wantXML := `<parent xmlns="urn:a"><x>1</x><y xmlns="urn:b">v</y></parent>`

	data, err := yang.MarshalXMLStruct(s, wantValue)
	if err != nil {
		t.Fatalf("MarshalXMLStruct: %v", err)
	}
	if string(data) != wantXML {
		t.Errorf("XML = %s\nwant %s", data, wantXML)
	}

	var got Parent
	if err := yang.UnmarshalXMLStruct(s, data, &got); err != nil {
		t.Fatalf("UnmarshalXMLStruct: %v", err)
	}
	if !yang.EqualStructs(got, wantValue) {
		t.Errorf("XML round-trip = %+v, want %+v", got, wantValue)
	}

	var absent Parent
	if err := yang.UnmarshalXMLStruct(s, []byte(`<parent xmlns="urn:a"><x>1</x></parent>`), &absent); err != nil {
		t.Fatalf("UnmarshalXMLStruct without grouped member: %v", err)
	}
	if absent.B != nil {
		t.Errorf("absent grouped member allocated B: %+v", absent)
	}
}

func TestGroupedStructJSONRoundTrip(t *testing.T) {
	s := parentSchema()
	wantValue := Parent{X: ptr(int32(1)), B: &ParentB{Y: ptr("v")}}
	wantJSON := `{"x":1,"b:y":"v"}`

	data, err := yang.MarshalJSON7951Struct(s, wantValue)
	if err != nil {
		t.Fatalf("MarshalJSON7951Struct: %v", err)
	}
	if string(data) != wantJSON {
		t.Errorf("JSON = %s\nwant %s", data, wantJSON)
	}

	var got Parent
	if err := yang.UnmarshalJSON7951Struct(s, data, &got); err != nil {
		t.Fatalf("UnmarshalJSON7951Struct: %v", err)
	}
	if !yang.EqualStructs(got, wantValue) {
		t.Errorf("JSON round-trip = %+v, want %+v", got, wantValue)
	}

	var absent Parent
	if err := yang.UnmarshalJSON7951Struct(s, []byte(`{"x":1}`), &absent); err != nil {
		t.Fatalf("UnmarshalJSON7951Struct without grouped member: %v", err)
	}
	if absent.B != nil {
		t.Errorf("absent grouped member allocated B: %+v", absent)
	}
}

func TestGroupedStructJSONBareMembers(t *testing.T) {
	s := parentSchema()

	var grouped Parent
	if err := yang.UnmarshalJSON7951Struct(s, []byte(`{"y":"v"}`), &grouped); err != nil {
		t.Fatalf("UnmarshalJSON7951Struct bare grouped member: %v", err)
	}
	if grouped.B == nil || grouped.B.Y == nil || *grouped.B.Y != "v" {
		t.Errorf("bare grouped member = %+v, want B.Y=v", grouped)
	}
	if grouped.C != nil {
		t.Errorf("bare grouped member allocated unrelated C: %+v", grouped.C)
	}

	var plain Parent
	if err := yang.UnmarshalJSON7951Struct(s, []byte(`{"x":0}`), &plain); err != nil {
		t.Fatalf("UnmarshalJSON7951Struct bare plain member: %v", err)
	}
	if plain.X == nil || *plain.X != 0 {
		t.Errorf("bare plain member = %+v, want X=0", plain.X)
	}
	if plain.B != nil || plain.C != nil {
		t.Errorf("bare plain member allocated grouped fields: %+v", plain)
	}
}

func TestGroupedStructJSONBareMemberAmbiguity(t *testing.T) {
	s := parentSchema()
	s.Fields[0].Name = "other"
	s.Fields[1].Child.Fields[0].Name = "z"
	s.Fields[2].Child.Fields[0].Name = "z"

	var got Parent
	err := yang.UnmarshalJSON7951Struct(s, []byte(`{"z":1}`), &got)
	if err == nil {
		t.Fatal("UnmarshalJSON7951Struct accepted ambiguous bare grouped member")
	}
	for _, module := range []string{"b", "c"} {
		if !strings.Contains(err.Error(), module) {
			t.Errorf("error %q does not name module %q", err, module)
		}
	}
}

func TestGroupedStructXMLNameCollisions(t *testing.T) {
	s := parentSchema()
	wantValue := Parent{
		X: ptr(int32(0)),
		B: &ParentB{X: ptr(int32(1))},
		C: &ParentC{X: ptr(true)},
	}
	wantXML := `<parent xmlns="urn:a"><x>0</x><x xmlns="urn:b">1</x><x xmlns="urn:c">true</x></parent>`

	data, err := yang.MarshalXMLStruct(s, wantValue)
	if err != nil {
		t.Fatalf("MarshalXMLStruct: %v", err)
	}
	if string(data) != wantXML {
		t.Errorf("XML = %s\nwant %s", data, wantXML)
	}

	var got Parent
	if err := yang.UnmarshalXMLStruct(s, []byte(wantXML), &got); err != nil {
		t.Fatalf("UnmarshalXMLStruct: %v", err)
	}
	if !yang.EqualStructs(got, wantValue) {
		t.Errorf("XML collision decode = %+v, want %+v", got, wantValue)
	}

	var absent Parent
	if err := yang.UnmarshalXMLStruct(s, []byte(`<parent xmlns="urn:a"><x>0</x></parent>`), &absent); err != nil {
		t.Fatalf("UnmarshalXMLStruct without grouped members: %v", err)
	}
	if absent.B != nil || absent.C != nil {
		t.Errorf("absent grouped members allocated groups: %+v", absent)
	}
}

func TestGroupedStructJSONNameCollisions(t *testing.T) {
	s := parentSchema()
	wantValue := Parent{
		X: ptr(int32(0)),
		B: &ParentB{X: ptr(int32(1))},
		C: &ParentC{X: ptr(true)},
	}
	wantJSON := `{"x":0,"b:x":1,"c:x":true}`

	data, err := yang.MarshalJSON7951Struct(s, wantValue)
	if err != nil {
		t.Fatalf("MarshalJSON7951Struct: %v", err)
	}
	if string(data) != wantJSON {
		t.Errorf("JSON = %s\nwant %s", data, wantJSON)
	}

	var got Parent
	if err := yang.UnmarshalJSON7951Struct(s, []byte(wantJSON), &got); err != nil {
		t.Fatalf("UnmarshalJSON7951Struct: %v", err)
	}
	if !yang.EqualStructs(got, wantValue) {
		t.Errorf("JSON collision decode = %+v, want %+v", got, wantValue)
	}

	var absent Parent
	if err := yang.UnmarshalJSON7951Struct(s, []byte(`{"x":0}`), &absent); err != nil {
		t.Fatalf("UnmarshalJSON7951Struct without grouped members: %v", err)
	}
	if absent.B != nil || absent.C != nil {
		t.Errorf("absent grouped members allocated groups: %+v", absent)
	}
}

func TestJSONDecodeDoesNotAllocateEmptyGroupedCollections(t *testing.T) {
	s := groupedCollectionsSchema()
	for _, data := range []string{`{"b:rows":[]}`, `{"b:values":[]}`} {
		var got groupedCollectionsParent
		if err := yang.UnmarshalJSON7951Struct(s, []byte(data), &got); err != nil {
			t.Fatalf("UnmarshalJSON7951Struct(%s): %v", data, err)
		}
		if got.B != nil {
			t.Errorf("decode %s allocated empty group: %+v", data, got.B)
		}
	}
}

func TestGroupedContainerStructRoundTrip(t *testing.T) {
	s := groupedContainerSchema()
	wantValue := groupedContainerParent{B: &groupedContainerGroup{Box: &groupedContainer{L: str("x")}}}
	tests := []struct {
		name    string
		marshal func() ([]byte, error)
		decode  func([]byte, any) error
		want    string
	}{
		{
			name: "JSON",
			marshal: func() ([]byte, error) {
				return yang.MarshalJSON7951Struct(s, wantValue)
			},
			decode: func(data []byte, v any) error {
				return yang.UnmarshalJSON7951Struct(s, data, v)
			},
			want: `{"b:box":{"l":"x"}}`,
		},
		{
			name: "XML",
			marshal: func() ([]byte, error) {
				return yang.MarshalXMLStruct(s, wantValue)
			},
			decode: func(data []byte, v any) error {
				return yang.UnmarshalXMLStruct(s, data, v)
			},
			want: `<p xmlns="urn:a"><box xmlns="urn:b"><l>x</l></box></p>`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, err := tc.marshal()
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(data) != tc.want {
				t.Errorf("encoded = %s\nwant %s", data, tc.want)
			}
			var got groupedContainerParent
			if err := tc.decode(data, &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !yang.EqualStructs(got, wantValue) {
				t.Errorf("round-trip = %+v, want %+v", got, wantValue)
			}
		})
	}
}

func TestPublicCodecRejectsNestedGroup(t *testing.T) {
	type outer struct{ B *struct{ C *struct{} } }
	s := &yang.Schema{
		Module: modA,
		Name:   "p",
		Fields: []yang.Field{{
			GoName: "B",
			Group:  true,
			Child: &yang.Schema{
				Module: modB,
				Fields: []yang.Field{{
					GoName: "C",
					Group:  true,
					Child:  &yang.Schema{Module: modC},
				}},
			},
		}},
	}
	_, err := yang.MarshalJSON7951Struct(s, outer{})
	if err == nil || !strings.Contains(err.Error(), "B") || !strings.Contains(err.Error(), "C") {
		t.Fatalf("MarshalJSON7951Struct error = %v, want both group fields", err)
	}
}

func TestDecodeJSONListShapes(t *testing.T) {
	s := serverSchema()
	array := `[{"name":"a"},{"name":"b"}]`
	object := `{"test-main:server":` + array + `}`
	for name, payload := range map[string]string{"bare array": array, "wrapped object": object} {
		t.Run(name, func(t *testing.T) {
			rows, err := yang.DecodeJSONList[testServer](s, []byte(payload))
			if err != nil {
				t.Fatalf("DecodeJSONList: %v", err)
			}
			if len(rows) != 2 || *rows[0].Name != "a" || *rows[1].Name != "b" {
				t.Errorf("rows = %+v", rows)
			}
		})
	}
}

func TestEqualAndMergeStructs(t *testing.T) {
	s := serverSchema()
	base := sampleServer()
	same := sampleServer()
	if !yang.EqualStructs(base, same) {
		t.Error("identical values compare unequal")
	}
	changed := sampleServer()
	changed.Port = u16(9090)
	if yang.EqualStructs(base, changed) {
		t.Error("differing values compare equal")
	}

	update := testServer{Port: u16(9090), Extra: &testExtra{Note: str("rack 9")}}
	merged := yang.MergeStructs(s, base, update)
	if *merged.Port != 9090 {
		t.Errorf("merged port = %d, want the update's 9090", *merged.Port)
	}
	if *merged.Name != "edge-1" || len(merged.Tags) != 2 {
		t.Errorf("merge dropped base fields: %+v", merged)
	}
	if *merged.Extra.Note != "rack 9" {
		t.Errorf("merged note = %q, want rack 9", *merged.Extra.Note)
	}
	// The merge never mutates base.
	if *base.Port != 8080 || *base.Extra.Note != "rack 7" {
		t.Errorf("merge mutated base: %+v", base)
	}
}

func TestVisitStructLeaves(t *testing.T) {
	// A parent container holding the server list exercises keyed
	// list segments.
	type servers struct {
		Server []testServer
	}
	parent := &yang.Schema{
		Module: modTestMain, Name: "servers",
		Fields: []yang.Field{
			{GoName: "Server", Name: "server", List: true, Child: serverSchema()},
		},
	}
	v := servers{Server: []testServer{sampleServer()}}

	var got []string
	err := yang.VisitStructLeaves(parent, v, func(p yang.Path, val yang.Value) bool {
		canon, cerr := val.Canonical()
		if cerr != nil {
			t.Fatal(cerr)
		}
		got = append(got, p.String()+"="+canon)
		return true
	})
	if err != nil {
		t.Fatalf("VisitStructLeaves: %v", err)
	}
	want := []string{
		"/test-main:server[name=edge-1]/name=edge-1",
		"/test-main:server[name=edge-1]/port=8080",
		"/test-main:server[name=edge-1]/test-aug:owner=netops",
		"/test-main:server[name=edge-1]/tags=prod",
		"/test-main:server[name=edge-1]/tags=edge",
		"/test-main:server[name=edge-1]/extra/note=rack 7",
	}
	if len(got) != len(want) {
		t.Fatalf("leaves = %v\nwant %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("leaf[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestStructRowCodec(t *testing.T) {
	type key struct{ Name string }
	codec := yang.StructRowCodec(serverSchema(), func(r *testServer) key {
		if r.Name == nil {
			return key{}
		}
		return key{Name: *r.Name}
	})

	rows, err := codec.DecodeXML([]byte(`<server xmlns="urn:test:main"><name>a</name><port>1</port></server>`))
	if err != nil || len(rows) != 1 {
		t.Fatalf("DecodeXML rows=%v err=%v", rows, err)
	}
	if codec.Key(rows[0]) != (key{Name: "a"}) {
		t.Errorf("Key = %+v", codec.Key(rows[0]))
	}
	if !codec.Equal(rows[0], rows[0]) {
		t.Error("row not equal to itself")
	}
	update := testServer{Port: u16(2)}
	merged := codec.Merge(rows[0], update)
	if *merged.Port != 2 || *merged.Name != "a" {
		t.Errorf("merged = %+v", merged)
	}
}

func TestUnmarshalStructReplacesContent(t *testing.T) {
	tests := []struct {
		name   string
		data   string
		decode func(*yang.Schema, []byte, any) error
	}{
		{name: "XML", data: `<server><name>new</name><tags>only</tags><extra/></server>`, decode: yang.UnmarshalXMLStruct},
		{name: "JSON", data: `{"name":"new","tags":["only"],"extra":{}}`, decode: yang.UnmarshalJSON7951Struct},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sampleServer()
			if err := tc.decode(serverSchema(), []byte(tc.data), &got); err != nil {
				t.Fatal(err)
			}
			want := testServer{Name: str("new"), Tags: []string{"only"}, Extra: &testExtra{}}
			if !yang.EqualStructs(got, want) {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestUnmarshalJSON7951RejectsNullStructure(t *testing.T) {
	for _, data := range []string{`null`, `{"extra":null}`, `{"tags":null}`} {
		t.Run(data, func(t *testing.T) {
			var row testServer
			if err := yang.UnmarshalJSON7951Struct(serverSchema(), []byte(data), &row); err == nil {
				t.Errorf("UnmarshalJSON7951Struct(%s) succeeded, want error", data)
			}
		})
	}
	for _, data := range []string{`null`, `[null]`, `{"server":null}`} {
		t.Run("list "+data, func(t *testing.T) {
			if _, err := yang.DecodeJSONList[testServer](serverSchema(), []byte(data)); err == nil {
				t.Errorf("DecodeJSONList(%s) succeeded, want error", data)
			}
		})
	}
}
