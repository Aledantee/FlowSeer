package main

import (
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/yang"
	fixtureaug "go.aledante.io/FlowSeer/src/protocol/yang/cmd/yanggen/testdata/golden/fixture/fixtureaug"
	fixtureaug2 "go.aledante.io/FlowSeer/src/protocol/yang/cmd/yanggen/testdata/golden/fixture/fixtureaug2"
	fixturemain "go.aledante.io/FlowSeer/src/protocol/yang/cmd/yanggen/testdata/golden/fixture/fixturemain"
)

// golden_roundtrip_test.go drives the committed generated fixture
// package through the runtime codecs: the same struct round-trips
// NETCONF XML and RFC 7951 JSON, the row machinery detects and
// merges changes, and nested flat rows carry
// ancestor keys.

func sampleGenServer() fixturemain.ServersServer {
	name := "edge-1"
	var port uint16 = 8080
	listen := yang.Value{Type: yang.Type{Kind: yang.TypeUint16}, Uint: 8080}
	proto := yang.Identity{Module: "fixture-types", Name: "tcp"}
	ratio := yang.Decimal64(1500, 3) // 1.5
	tlsMin := "tls13"
	addr := "10.0.0.1"
	var epPort uint16 = 443
	enabled := true
	owner := "augment-owner"
	var secondOwner uint8 = 7
	secondPort := "8443"
	return fixturemain.ServersServer{
		Name:        &name,
		Port:        &port,
		Listen:      &listen,
		Proto:       &proto,
		Ratio:       &ratio,
		TLS:         &fixturemain.TLS{MinVersion: &tlsMin},
		FixtureAug:  &fixtureaug.ServerAugment{Owner: &owner},
		FixtureAug2: &fixtureaug2.ServerAugment{Owner: &secondOwner, Port: &secondPort},
		Endpoint: []fixturemain.Endpoint{
			{Address: &addr, Port: &epPort, Enabled: &enabled},
		},
	}
}

func TestGeneratedXMLRoundTrip(t *testing.T) {
	in := sampleGenServer()
	data, err := yang.MarshalXMLStruct(fixturemain.ServersServerSchemaX431a4c, in)
	if err != nil {
		t.Fatalf("MarshalXMLStruct: %v", err)
	}
	for _, frag := range []string{
		`<server xmlns="urn:flowseer:fixture-main">`,
		"<listen>8080</listen>",
		"<proto>fixture-types:tcp</proto>",
		"<ratio>1.5</ratio>",
		"<min-version>tls13</min-version>",
		`<owner xmlns="urn:flowseer:fixture-aug">augment-owner</owner>`,
		`<owner xmlns="urn:flowseer:fixture-aug2">7</owner>`,
		"<endpoint><address>10.0.0.1</address>",
	} {
		if !strings.Contains(string(data), frag) {
			t.Errorf("XML missing %q in %s", frag, data)
		}
	}
	var out fixturemain.ServersServer
	if err := yang.UnmarshalXMLStruct(fixturemain.ServersServerSchemaX431a4c, data, &out); err != nil {
		t.Fatalf("UnmarshalXMLStruct: %v", err)
	}
	if !yang.EqualStructs(in, out) {
		t.Errorf("XML round-trip diverged:\nin  %+v\nout %+v", in, out)
	}
}

func TestGeneratedJSONRoundTrip(t *testing.T) {
	in := sampleGenServer()
	data, err := yang.MarshalJSON7951Struct(fixturemain.ServersServerSchemaX431a4c, in)
	if err != nil {
		t.Fatalf("MarshalJSON7951Struct: %v", err)
	}
	for _, frag := range []string{
		`"listen":8080`,
		`"proto":"fixture-types:tcp"`,
		`"ratio":"1.5"`,
		`"min-version":"tls13"`,
		`"fixture-aug:owner":"augment-owner"`,
		`"fixture-aug2:owner":7`,
	} {
		if !strings.Contains(string(data), frag) {
			t.Errorf("JSON missing %q in %s", frag, data)
		}
	}
	var out fixturemain.ServersServer
	if err := yang.UnmarshalJSON7951Struct(fixturemain.ServersServerSchemaX431a4c, data, &out); err != nil {
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
	codec := fixturemain.ServerDescriptor().Codec
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

	update := fixturemain.ServersServer{Port: &newPort}
	merged := codec.Merge(base, update)
	if merged.Port == nil || *merged.Port != 9090 {
		t.Errorf("merged port = %v, want 9090", merged.Port)
	}
	if merged.Name == nil || *merged.Name != "edge-1" || merged.TLS == nil {
		t.Errorf("merge dropped unchanged fields: %+v", merged)
	}

	if key := codec.Key(base); key != (fixturemain.ServerKey{Name: "edge-1"}) {
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

	rows, err := fixturemain.EndpointDescriptor().Codec.DecodeXML([]byte(payload))
	if err != nil {
		t.Fatalf("DecodeXML: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("decoded %d flat rows, want 3", len(rows))
	}
	keyOf := fixturemain.EndpointDescriptor().Codec.Key
	want := []fixturemain.EndpointKey{
		{ServerName: "a", Address: "10.0.0.1", Port: 443},
		{ServerName: "a", Address: "10.0.0.2", Port: 444},
		{ServerName: "b", Address: "10.0.1.1", Port: 443},
	}
	for i, w := range want {
		if got := keyOf(rows[i]); got != w {
			t.Errorf("row %d key = %+v, want %+v", i, got, w)
		}
	}

	jsonPayload := `{"fixture-main:server":[` +
		`{"name":"a","endpoint":[{"address":"10.0.0.1","port":443}]},` +
		`{"name":"b","endpoint":[{"address":"10.0.1.1","port":443}]}]}`
	jrows, err := fixturemain.EndpointDescriptor().Codec.DecodeJSON([]byte(jsonPayload))
	if err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}
	if len(jrows) != 2 || keyOf(jrows[0]).ServerName != "a" || keyOf(jrows[1]).ServerName != "b" {
		t.Errorf("JSON flat rows = %+v", jrows)
	}
}

// TestGeneratedDescriptorPaths asserts the descriptor paths render to
// the three wire forms.
func TestGeneratedDescriptorPaths(t *testing.T) {
	p := fixturemain.ServerDescriptor().Path
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
	err := yang.VisitStructLeaves(fixturemain.ServersServerSchemaX431a4c, v, func(p yang.Path, val yang.Value) bool {
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
		"/fixture-aug2:owner=7",
	} {
		if !strings.Contains(joined, frag) {
			t.Errorf("leaves missing %q:\n%s", frag, joined)
		}
	}
}

func TestGeneratedAugmentListDescriptor(t *testing.T) {
	if got := fixturemain.MirrorDescriptor().Path.String(); got != "/fixture-main:servers/fixture-aug2:mirror" {
		t.Fatalf("mirror descriptor path = %q", got)
	}
	rows, err := fixturemain.MirrorDescriptor().Codec.DecodeJSON([]byte(`{"fixture-aug2:mirror":[{"id":"m1"}]}`))
	if err != nil {
		t.Fatalf("mirror DecodeJSON: %v", err)
	}
	if len(rows) != 1 || rows[0].ID == nil || *rows[0].ID != "m1" {
		t.Fatalf("mirror rows = %+v, want one row m1", rows)
	}
}

// TestGroupingInstantiatingModuleRoundTrip asserts that nodes instantiated
// from another module's grouping decode JSON with the instantiating module's
// qualification and XML in its namespace, through each container that uses
// the grouping.
func TestGroupingInstantiatingModuleRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		container string
		decode    func(json bool, data []byte) (*fixturemain.Item, error)
	}{
		{"primary-group", func(json bool, data []byte) (*fixturemain.Item, error) {
			var got fixturemain.PrimaryGroup
			if json {
				err := yang.UnmarshalJSON7951Struct(fixturemain.PrimaryGroupSchema, data, &got)
				return got.Item, err
			}
			err := yang.UnmarshalXMLStruct(fixturemain.PrimaryGroupSchema, data, &got)
			return got.Item, err
		}},
		{"secondary-group", func(json bool, data []byte) (*fixturemain.Item, error) {
			var got fixturemain.SecondaryGroup
			if json {
				err := yang.UnmarshalJSON7951Struct(fixturemain.SecondaryGroupSchema, data, &got)
				return got.Item, err
			}
			err := yang.UnmarshalXMLStruct(fixturemain.SecondaryGroupSchema, data, &got)
			return got.Item, err
		}},
	} {
		t.Run(tc.container, func(t *testing.T) {
			jsonPayload := []byte(`{"fixture-main:item":{"fixture-main:buffer-size":4096}}`)
			item, err := tc.decode(true, jsonPayload)
			if err != nil {
				t.Fatalf("decode JSON: %v", err)
			}
			if item == nil || item.BufferSize == nil || *item.BufferSize != 4096 {
				t.Errorf("JSON item = %+v, want BufferSize 4096", item)
			}

			xmlPayload := []byte(`<` + tc.container + ` xmlns="urn:flowseer:fixture-main"><item><buffer-size>8192</buffer-size></item></` + tc.container + `>`)
			item, err = tc.decode(false, xmlPayload)
			if err != nil {
				t.Fatalf("decode XML: %v", err)
			}
			if item == nil || item.BufferSize == nil || *item.BufferSize != 8192 {
				t.Errorf("XML item = %+v, want BufferSize 8192", item)
			}
		})
	}
}

// TestGeneratedSeparatedShapesDecodeTheirOwnWireForm asserts that nodes the
// shape key keeps apart decode what their own schema path carries: a string
// where the sibling holds a uint32, a leaf qualified by its augmenting module,
// and a presence container, and that the shared list's descriptors address
// their own container.
func TestGeneratedSeparatedShapesDecodeTheirOwnWireForm(t *testing.T) {
	var byType fixturemain.ByTypeB
	if err := yang.UnmarshalJSON7951Struct(fixturemain.ByTypeBSchema, []byte(`{"setting":{"value":"eth0"}}`), &byType); err != nil {
		t.Fatalf("by-type-b: %v", err)
	}
	if byType.Setting == nil || byType.Setting.Value == nil || *byType.Setting.Value != "eth0" {
		t.Errorf("by-type-b setting = %+v, want Value eth0", byType.Setting)
	}

	payload := []byte(`{"slot":{"fixture-aug:value":7}}`)
	var byModuleB fixturemain.ByModuleB
	if err := yang.UnmarshalJSON7951Struct(fixturemain.ByModuleBSchema, payload, &byModuleB); err != nil {
		t.Fatalf("by-module-b: %v", err)
	}
	if byModuleB.Slot == nil || byModuleB.Slot.FixtureAug == nil || byModuleB.Slot.FixtureAug.Value == nil || *byModuleB.Slot.FixtureAug.Value != 7 {
		t.Errorf("by-module-b slot = %+v, want fixture-aug:value 7", byModuleB.Slot)
	}
	var byModuleA fixturemain.ByModuleA
	if err := yang.UnmarshalJSON7951Struct(fixturemain.ByModuleASchema, payload, &byModuleA); err != nil {
		t.Fatalf("by-module-a: %v", err)
	}
	if byModuleA.Slot != nil && byModuleA.Slot.Value != nil {
		t.Errorf("by-module-a decoded fixture-aug:value %d into its fixture-main leaf", *byModuleA.Slot.Value)
	}

	if !fixturemain.ByPresenceBMarkerSchema.Presence || fixturemain.ByPresenceAMarkerSchema.Presence {
		t.Errorf("marker presence: a=%v b=%v, want a=false b=true",
			fixturemain.ByPresenceAMarkerSchema.Presence, fixturemain.ByPresenceBMarkerSchema.Presence)
	}

	for _, tc := range []struct {
		path string
		got  yang.Path
	}{
		{"/fixture-main:primary-group/peer", fixturemain.PrimaryGroupPeerDescriptor().Path},
		{"/fixture-main:secondary-group/peer", fixturemain.SecondaryGroupPeerDescriptor().Path},
	} {
		if got := tc.got.String(); got != tc.path {
			t.Errorf("peer descriptor path = %q, want %q", got, tc.path)
		}
	}
	rows, err := fixturemain.SecondaryGroupPeerDescriptor().Codec.DecodeJSON([]byte(`{"fixture-main:peer":[{"name":"p1","weight":3}]}`))
	if err != nil {
		t.Fatalf("peer DecodeJSON: %v", err)
	}
	if len(rows) != 1 || rows[0].Weight == nil || *rows[0].Weight != 3 ||
		fixturemain.SecondaryGroupPeerDescriptor().Codec.Key(rows[0]) != (fixturemain.SecondaryGroupPeerKey{Name: "p1"}) {
		t.Errorf("peer rows = %+v, want one row p1 weight 3", rows)
	}
}
