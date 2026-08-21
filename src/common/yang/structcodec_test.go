package yang_test

import (
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/yang"
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

func serverSchema() *yang.Schema {
	return &yang.Schema{
		Module:    "test-main",
		Namespace: "urn:test:main",
		Name:      "server",
		Keys:      []string{"name"},
		Fields: []yang.Field{
			{GoName: "Name", Name: "name", Type: &yang.Type{Kind: yang.TypeString}},
			{GoName: "Port", Name: "port", Type: &yang.Type{Kind: yang.TypeUint16}},
			{
				GoName: "Owner", Name: "owner",
				Module: "test-aug", Namespace: "urn:test:aug",
				Type: &yang.Type{Kind: yang.TypeString},
			},
			{GoName: "Tags", Name: "tags", LeafList: true, Type: &yang.Type{Kind: yang.TypeString}},
			{
				GoName: "Extra", Name: "extra",
				Child: &yang.Schema{
					Module: "test-main", Namespace: "urn:test:main", Name: "extra", Presence: true,
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
		Module: "test-main", Namespace: "urn:test:main", Name: "servers",
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
