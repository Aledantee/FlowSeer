package main

import (
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/yang"
	fixturemain "go.aledante.io/FlowSeer/src/common/yang/cmd/yanggen/testdata/golden/fixture/fixturemain"
)

// golden_roundtrip_test.go drives the committed generated fixture
// package through the runtime codecs: the same struct round-trips
// NETCONF XML and RFC 7951 JSON, the row machinery detects and
// merges changes, and nested flat rows carry
// ancestor keys.

func sampleGenServer() fixturemain.Servers_Server {
	name := "edge-1"
	var port uint16 = 8080
	listen := yang.Value{Type: yang.Type{Kind: yang.TypeUint16}, Uint: 8080}
	proto := yang.Identity{Module: "fixture-types", Name: "tcp"}
	ratio := yang.Decimal64(1500, 3) // 1.5
	tlsMin := "tls13"
	addr := "10.0.0.1"
	var epPort uint16 = 443
	enabled := true
	return fixturemain.Servers_Server{
		Name:   &name,
		Port:   &port,
		Listen: &listen,
		Proto:  &proto,
		Ratio:  &ratio,
		Tls:    &fixturemain.Servers_Server_Tls{MinVersion: &tlsMin},
		Endpoint: []fixturemain.Servers_Server_Endpoint{
			{Address: &addr, Port: &epPort, Enabled: &enabled},
		},
	}
}

func TestGeneratedXMLRoundTrip(t *testing.T) {
	in := sampleGenServer()
	data, err := yang.MarshalXMLStruct(fixturemain.Servers_ServerSchema, in)
	if err != nil {
		t.Fatalf("MarshalXMLStruct: %v", err)
	}
	for _, frag := range []string{
		`<server xmlns="urn:flowseer:fixture-main">`,
		"<listen>8080</listen>",
		"<proto>fixture-types:tcp</proto>",
		"<ratio>1.5</ratio>",
		"<min-version>tls13</min-version>",
		"<endpoint><address>10.0.0.1</address>",
	} {
		if !strings.Contains(string(data), frag) {
			t.Errorf("XML missing %q in %s", frag, data)
		}
	}
	var out fixturemain.Servers_Server
	if err := yang.UnmarshalXMLStruct(fixturemain.Servers_ServerSchema, data, &out); err != nil {
		t.Fatalf("UnmarshalXMLStruct: %v", err)
	}
	if !yang.EqualStructs(in, out) {
		t.Errorf("XML round-trip diverged:\nin  %+v\nout %+v", in, out)
	}
}

func TestGeneratedJSONRoundTrip(t *testing.T) {
	in := sampleGenServer()
	data, err := yang.MarshalJSON7951Struct(fixturemain.Servers_ServerSchema, in)
	if err != nil {
		t.Fatalf("MarshalJSON7951Struct: %v", err)
	}
	for _, frag := range []string{
		`"listen":8080`,
		`"proto":"fixture-types:tcp"`,
		`"ratio":"1.5"`,
		`"min-version":"tls13"`,
	} {
		if !strings.Contains(string(data), frag) {
			t.Errorf("JSON missing %q in %s", frag, data)
		}
	}
	var out fixturemain.Servers_Server
	if err := yang.UnmarshalJSON7951Struct(fixturemain.Servers_ServerSchema, data, &out); err != nil {
		t.Fatalf("UnmarshalJSON7951Struct: %v", err)
	}
	if !yang.EqualStructs(in, out) {
		t.Errorf("JSON round-trip diverged:\nin  %+v\nout %+v", in, out)
	}
}

// TestGeneratedRowMachinery covers the row machinery: equal detects
// a single leaf change in a keyed row, merge preserves unchanged
// fields, and the key extractor produces the composite identity.
func TestGeneratedRowMachinery(t *testing.T) {
	codec := fixturemain.Servers_ServerDescriptor().Codec
	base := sampleGenServer()

	if !codec.Equal(base, sampleGenServer()) {
		t.Error("identical rows compare unequal")
	}
	changed := sampleGenServer()
	newPort := uint16(9090)
	changed.Port = &newPort
	if codec.Equal(base, changed) {
		t.Error("single-leaf change not detected")
	}

	update := fixturemain.Servers_Server{Port: &newPort}
	merged := codec.Merge(base, update)
	if merged.Port == nil || *merged.Port != 9090 {
		t.Errorf("merged port = %v, want 9090", merged.Port)
	}
	if merged.Name == nil || *merged.Name != "edge-1" || merged.Tls == nil {
		t.Errorf("merge dropped unchanged fields: %+v", merged)
	}

	if key := codec.Key(base); key != (fixturemain.Servers_ServerKey{Name: "edge-1"}) {
		t.Errorf("key = %+v", key)
	}
}

// TestGeneratedNestedFlatRows covers nested-list flattening: inner-list
// rows decoded across two parents carry each parent's key, and the
// composite key struct includes it.
func TestGeneratedNestedFlatRows(t *testing.T) {
	payload := `<data><servers xmlns="urn:flowseer:fixture-main">` +
		`<server><name>a</name>` +
		`<endpoint><address>10.0.0.1</address><port>443</port></endpoint>` +
		`<endpoint><address>10.0.0.2</address><port>444</port></endpoint>` +
		`</server>` +
		`<server><name>b</name>` +
		`<endpoint><address>10.0.1.1</address><port>443</port></endpoint>` +
		`</server>` +
		`</servers></data>`

	rows, err := fixturemain.Servers_Server_EndpointDescriptor().Codec.DecodeXML([]byte(payload))
	if err != nil {
		t.Fatalf("DecodeXML: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("decoded %d flat rows, want 3", len(rows))
	}
	keyOf := fixturemain.Servers_Server_EndpointDescriptor().Codec.Key
	want := []fixturemain.Servers_Server_EndpointKey{
		{Server_Name: "a", Address: "10.0.0.1", Port: 443},
		{Server_Name: "a", Address: "10.0.0.2", Port: 444},
		{Server_Name: "b", Address: "10.0.1.1", Port: 443},
	}
	for i, w := range want {
		if got := keyOf(rows[i]); got != w {
			t.Errorf("row %d key = %+v, want %+v", i, got, w)
		}
	}

	jsonPayload := `{"fixture-main:server":[` +
		`{"name":"a","endpoint":[{"address":"10.0.0.1","port":443}]},` +
		`{"name":"b","endpoint":[{"address":"10.0.1.1","port":443}]}]}`
	jrows, err := fixturemain.Servers_Server_EndpointDescriptor().Codec.DecodeJSON([]byte(jsonPayload))
	if err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}
	if len(jrows) != 2 || keyOf(jrows[0]).Server_Name != "a" || keyOf(jrows[1]).Server_Name != "b" {
		t.Errorf("JSON flat rows = %+v", jrows)
	}
}

// TestGeneratedDescriptorPaths asserts the descriptor paths render to
// the three wire forms.
func TestGeneratedDescriptorPaths(t *testing.T) {
	p := fixturemain.Servers_ServerDescriptor().Path
	if got := p.String(); got != "/fixture-main:servers/server" {
		t.Errorf("gNMI path = %q", got)
	}
	if got := p.RESTCONFURI(); got != "/fixture-main:servers/server" {
		t.Errorf("RESTCONF path = %q", got)
	}
	xmlFilter, err := p.SubtreeFilterXML()
	if err != nil {
		t.Fatal(err)
	}
	want := `<servers xmlns="urn:flowseer:fixture-main"><server></server></servers>`
	if string(xmlFilter) != want {
		t.Errorf("subtree filter = %s, want %s", xmlFilter, want)
	}
}

// TestGeneratedVisitLeaves drives the gNMI-facing leaf enumeration
// over a generated struct.
func TestGeneratedVisitLeaves(t *testing.T) {
	v := sampleGenServer()
	var leaves []string
	err := yang.VisitStructLeaves(fixturemain.Servers_ServerSchema, v, func(p yang.Path, val yang.Value) bool {
		canon, cerr := val.Canonical()
		if cerr != nil {
			t.Fatal(cerr)
		}
		leaves = append(leaves, p.String()+"="+canon)
		return true
	})
	if err != nil {
		t.Fatalf("VisitStructLeaves: %v", err)
	}
	joined := strings.Join(leaves, "\n")
	for _, frag := range []string{
		"/fixture-main:name=edge-1",
		"/fixture-main:endpoint[address=10.0.0.1][port=443]/address=10.0.0.1",
		"/fixture-main:tls/min-version=tls13",
		"/fixture-main:proto=fixture-types:tcp",
	} {
		if !strings.Contains(joined, frag) {
			t.Errorf("leaves missing %q:\n%s", frag, joined)
		}
	}
}
