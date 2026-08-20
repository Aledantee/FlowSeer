package yang_test

import (
	"errors"
	"reflect"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/yang"
)

// compoundPath is the shared fixture: a nested list with a compound
// key whose values exercise percent-encoding (`/`, `:`, space) and
// gNMI escaping (`]`).
func compoundPath() yang.Path {
	return yang.Path{Segments: []yang.Segment{
		{Module: "openconfig-network-instance", Namespace: "http://openconfig.net/yang/network-instance", Name: "network-instances"},
		{Name: "network-instance", Keys: []yang.KeyValue{{Name: "name", Value: "VRF red/blue"}}},
		{Name: "protocols"},
		{Name: "protocol", Keys: []yang.KeyValue{
			{Name: "identifier", Value: "oc-pol-types:STATIC"},
			{Name: "name", Value: "static [v4]"},
		}},
	}}
}

func TestPathGNMIStringRoundTrip(t *testing.T) {
	p := compoundPath()
	s := p.String()
	want := `/openconfig-network-instance:network-instances/network-instance[name=VRF red/blue]/protocols/protocol[identifier=oc-pol-types:STATIC][name=static [v4\]]`
	if s != want {
		t.Errorf("String() = %q, want %q", s, want)
	}

	got, err := yang.ParsePath(s)
	if err != nil {
		t.Fatalf("ParsePath(%q): %v", s, err)
	}
	// Namespace is schema knowledge; parsing never recovers it.
	exp := compoundPath()
	exp.Segments[0].Namespace = ""
	if !reflect.DeepEqual(got, exp) {
		t.Errorf("ParsePath() = %+v, want %+v", got, exp)
	}
}

func TestPathRESTCONFURIRoundTrip(t *testing.T) {
	p := compoundPath()
	uri := p.RESTCONFURI()
	want := "/openconfig-network-instance:network-instances" +
		"/network-instance=VRF%20red%2Fblue" +
		"/protocols" +
		"/protocol=oc-pol-types%3ASTATIC,static%20%5Bv4%5D"
	if uri != want {
		t.Errorf("RESTCONFURI() = %q, want %q", uri, want)
	}

	got, err := yang.ParseRESTCONFURI(uri, []string{"name"}, []string{"identifier", "name"})
	if err != nil {
		t.Fatalf("ParseRESTCONFURI(%q): %v", uri, err)
	}
	// The URI form qualifies segments only on module boundaries, and
	// namespaces never travel; normalize the fixture the same way.
	exp := compoundPath()
	exp.Segments[0].Namespace = ""
	for i := 1; i < len(exp.Segments); i++ {
		exp.Segments[i].Module = exp.Segments[0].Module
	}
	if !reflect.DeepEqual(got, exp) {
		t.Errorf("ParseRESTCONFURI() = %+v, want %+v", got, exp)
	}
}

func TestPathSubtreeFilterXML(t *testing.T) {
	p := yang.Path{Segments: []yang.Segment{
		{Module: "ietf-interfaces", Namespace: "urn:ietf:params:xml:ns:yang:ietf-interfaces", Name: "interfaces"},
		{Name: "interface", Keys: []yang.KeyValue{{Name: "name", Value: `GigabitEthernet0/0 <&>`}}},
	}}
	got, err := p.SubtreeFilterXML()
	if err != nil {
		t.Fatalf("SubtreeFilterXML(): %v", err)
	}
	want := `<interfaces xmlns="urn:ietf:params:xml:ns:yang:ietf-interfaces">` +
		`<interface><name>GigabitEthernet0/0 &lt;&amp;&gt;</name></interface></interfaces>`
	if string(got) != want {
		t.Errorf("SubtreeFilterXML() = %s, want %s", got, want)
	}
}

func TestPathRootForms(t *testing.T) {
	var root yang.Path
	if got := root.String(); got != "/" {
		t.Errorf("root String() = %q, want /", got)
	}
	if got := root.RESTCONFURI(); got != "/" {
		t.Errorf("root RESTCONFURI() = %q, want /", got)
	}
	for _, in := range []string{"", "/"} {
		p, err := yang.ParsePath(in)
		if err != nil || len(p.Segments) != 0 {
			t.Errorf("ParsePath(%q) = %+v, %v; want the root path", in, p, err)
		}
	}
}

func TestParsePathRejectsMalformed(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{name: "no leading slash", in: "interfaces/interface"},
		{name: "empty segment name", in: "/interfaces//name"},
		{name: "module only", in: "/ietf-interfaces:"},
		{name: "predicate without equals", in: "/a[key]"},
		{name: "unterminated predicate", in: "/a[key=v"},
		{name: "dangling escape", in: `/a[key=v\`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := yang.ParsePath(tc.in)
			if err == nil {
				t.Fatalf("ParsePath(%q) succeeded, want an error", tc.in)
			}
			if code, ok := errs.CodeOf(err); !ok || code != yang.ErrCodePathParse {
				t.Errorf("error code = %v, want %v", code, yang.ErrCodePathParse)
			}
		})
	}
}

func TestParseRESTCONFURIRejectsKeyMismatch(t *testing.T) {
	_, err := yang.ParseRESTCONFURI("/m:list=a,b", []string{"only-one"})
	if err == nil {
		t.Fatal("key-count mismatch parsed without error")
	}
	var e *errs.Error
	if !errors.As(err, &e) {
		t.Errorf("error is %T, want *errs.Error", err)
	}
}
