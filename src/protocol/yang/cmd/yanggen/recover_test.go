package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openconfig/goyang/pkg/yang"
)

const testModule = `module t {
  yang-version 1.1;
  namespace "urn:t";
  prefix t;
  container c {
    leaf name { type string; }
    container k { leaf base { type string; } }
  }
}`

func recoveryVendor(t *testing.T, sources map[string]string) (*VendorSet, error) {
	t.Helper()
	dir := t.TempDir()
	for name, source := range sources {
		if err := os.WriteFile(filepath.Join(dir, name+".yang"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return LoadVendor(&Vendor{Name: "recovery", Paths: []string{dir}})
}

func requireRecoveryError(t *testing.T, err error, parts ...string) {
	t.Helper()
	var loadErr *LoadError
	if !errors.As(err, &loadErr) {
		t.Fatalf("error = %v, want LoadError", err)
	}
	for _, part := range parts {
		if !strings.Contains(loadErr.Error(), part) {
			t.Errorf("error = %q, want %q", loadErr, part)
		}
	}
}

func TestRecoverCollidingAugments(t *testing.T) {
	sources := map[string]string{
		"t": testModule,
		"a": `module a { namespace "urn:a"; prefix a; import t { prefix t; }
          augment "/t:c" { leaf x { type string; } } }`,
		"b": `module b { namespace "urn:b"; prefix b; import t { prefix t; }
          augment "/t:c" { leaf x { type uint32; } } }`,
	}
	for range 20 {
		vs, err := recoveryVendor(t, sources)
		if err != nil {
			t.Fatalf("LoadVendor: %v", err)
		}
		parent := moduleByName(t, vs, "t").Entry.Dir["c"]
		if got := len(vs.Recovered[parent]); got != 1 {
			t.Fatalf("recovered = %d, want 1", got)
		}
		got := map[string]bool{}
		for _, child := range append([]*yang.Entry{parent.Dir["x"]}, vs.Recovered[parent]...) {
			if child == nil {
				t.Fatal("collision survivor missing")
			}
			module, err := child.InstantiatingModule()
			if err != nil {
				t.Fatal(err)
			}
			got[module] = true
		}
		if len(got) != 2 || !got["a"] || !got["b"] {
			t.Fatalf("collision modules = %v, want a and b", got)
		}
	}
}

func TestRecoverAgainstTargetNode(t *testing.T) {
	vs, err := recoveryVendor(t, map[string]string{
		"t": testModule,
		"b": `module b { namespace "urn:b"; prefix b; import t { prefix t; }
          augment "/t:c" { leaf name { type uint32; } } }`,
	})
	if err != nil {
		t.Fatalf("LoadVendor: %v", err)
	}
	parent := moduleByName(t, vs, "t").Entry.Dir["c"]
	if got := len(vs.Recovered[parent]); got != 1 {
		t.Fatalf("recovered = %d, want 1", got)
	}
	if got, _ := vs.Recovered[parent][0].InstantiatingModule(); got != "b" {
		t.Errorf("recovered module = %q, want b", got)
	}
}

func TestRecoverDeviatedAwaySurvivor(t *testing.T) {
	vs, err := recoveryVendor(t, map[string]string{
		"t": testModule,
		"b": `module b { namespace "urn:b"; prefix b; import t { prefix t; }
          augment "/t:c" { leaf name { type uint32; } } }`,
		"d": `module d { namespace "urn:d"; prefix d; import t { prefix t; }
          deviation "/t:c/t:name" { deviate not-supported; } }`,
	})
	if err != nil {
		t.Fatalf("LoadVendor: %v", err)
	}
	parent := moduleByName(t, vs, "t").Entry.Dir["c"]
	if got := len(vs.Recovered[parent]); got != 1 {
		t.Fatalf("recovered = %d, want 1", got)
	}
}

func TestRecoverSharedGrouping(t *testing.T) {
	vs, err := recoveryVendor(t, map[string]string{
		"t": `module t { namespace "urn:t"; prefix t; import b { prefix b; }
          container c { uses b:g; } }`,
		"b": `module b { namespace "urn:b"; prefix b; import t { prefix t; }
          grouping g { leaf x { type string; } leaf y { type string; } }
          augment "/t:c" { uses g; } }`,
	})
	if err != nil {
		t.Fatalf("LoadVendor: %v", err)
	}
	parent := moduleByName(t, vs, "t").Entry.Dir["c"]
	if got := len(vs.Recovered[parent]); got != 2 {
		t.Fatalf("recovered = %d, want 2", got)
	}
}

func TestRecoverRejectsPathThroughCollision(t *testing.T) {
	_, err := recoveryVendor(t, map[string]string{
		"t": testModule,
		"b": `module b { namespace "urn:b"; prefix b; import t { prefix t; }
          augment "/t:c" { container k { leaf other { type string; } } } }`,
		"d": `module d { namespace "urn:d"; prefix d; import t { prefix t; }
          import b { prefix b; }
          augment "/t:c/b:k" { leaf y { type string; } } }`,
	})
	requireRecoveryError(t, err, "/t:c/b:k", "b.yang", "t.yang")
}

func TestRecoverRejectsDeviationThroughCollision(t *testing.T) {
	_, err := recoveryVendor(t, map[string]string{
		"t": testModule,
		"b": `module b { namespace "urn:b"; prefix b; import t { prefix t; }
          augment "/t:c" { container k { leaf base { type string; } } } }`,
		"d": `module d { namespace "urn:d"; prefix d; import t { prefix t; }
          import b { prefix b; }
          deviation "/t:c/b:k/b:base" { deviate not-supported; } }`,
	})
	requireRecoveryError(t, err, "/t:c/b:k/b:base", "b.yang", "t.yang")
}

func TestRecoverAllowsPathThroughOwnSurvivor(t *testing.T) {
	vs, err := recoveryVendor(t, map[string]string{
		"t": testModule,
		"b": `module b { namespace "urn:b"; prefix b; import t { prefix t; }
          augment "/t:c" { container k { leaf other { type string; } } } }`,
		"d": `module d { namespace "urn:d"; prefix d; import t { prefix t; }
          augment "/t:c/t:k" { leaf y { type string; } } }`,
	})
	if err != nil {
		t.Fatalf("LoadVendor: %v", err)
	}
	parent := moduleByName(t, vs, "t").Entry.Dir["c"]
	if got := len(vs.Recovered[parent]); got != 1 {
		t.Fatalf("recovered = %d, want 1", got)
	}
	if parent.Dir["k"].Dir["y"] == nil {
		t.Fatal("own survivor lost its nested augment")
	}
}

func TestRecoverRejectsSameModuleDuplicate(t *testing.T) {
	_, err := recoveryVendor(t, map[string]string{
		"t": `module t { namespace "urn:t"; prefix t;
          container c { leaf x { type string; } }
          augment "/t:c" { leaf x { type uint32; } } }`,
	})
	requireRecoveryError(t, err, "/t:c", "t.yang")
	if err != nil && strings.Count(err.Error(), "t.yang:") < 2 {
		t.Errorf("error = %q, want both source positions", err)
	}
}

func TestRecoverRejectsDroppedContainerLeafref(t *testing.T) {
	_, err := recoveryVendor(t, map[string]string{
		"t": `module t { namespace "urn:t"; prefix t;
          container c { leaf name { type string; } container box { leaf old { type string; } } } }`,
		"b": `module b { namespace "urn:b"; prefix b; import t { prefix t; }
          augment "/t:c" { container box { leaf ref { type leafref {
          path "../../name"; } } } } }`,
	})
	requireRecoveryError(t, err, "/t:c", "box", "b.yang", "t.yang")
}

func TestRecoverRejectsUnmatchedDuplicateError(t *testing.T) {
	root := &yang.Entry{Name: "t", Dir: map[string]*yang.Entry{}}
	parent := &yang.Entry{
		Name: "c", Parent: root, Dir: map[string]*yang.Entry{},
		Errors: []error{errors.New("Duplicate node \"x\" in \"c\" from:\n   b.yang:2: x\n   t.yang:3: x")},
	}
	root.Dir["c"] = parent
	err := checkRecoveredDuplicates("recovery", map[*yang.Entry][]*yang.Entry{}, root)
	requireRecoveryError(t, err, "/t/c", "b.yang:2", "t.yang:3")
}

func TestRecoverIgnoresRPCInput(t *testing.T) {
	vs, err := recoveryVendor(t, map[string]string{
		"t": `module t { namespace "urn:t"; prefix t;
          rpc op { input { leaf x { type string; } } } }`,
		"b": `module b { namespace "urn:b"; prefix b; import t { prefix t; }
          augment "/t:op/t:input" { leaf x { type uint32; } } }`,
	})
	if err != nil {
		t.Fatalf("LoadVendor: %v", err)
	}
	if vs.Recovered == nil {
		t.Fatal("recovery map is nil")
	}
	if got := len(vs.Recovered); got != 0 {
		t.Errorf("recovery targets = %d, want 0", got)
	}
}

func parseRecoveryModules(t *testing.T, sources map[string]string) *yang.Modules {
	t.Helper()
	dir := t.TempDir()
	for name, source := range sources {
		if err := os.WriteFile(filepath.Join(dir, name+".yang"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	v := &Vendor{Name: "recovery", Paths: []string{dir}}
	files, err := discoverSources(v)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := rawParse(v.Name, files, nil)
	if err != nil {
		t.Fatal(err)
	}
	ms, _, err := parseModules(v, raw)
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

func TestRecoverRejectsPathThroughExplicitCaseCollision(t *testing.T) {
	_, err := recoveryVendor(t, map[string]string{
		"t": `module t {
          yang-version 1.1;
          namespace "urn:t";
          prefix t;
          container c {
            choice ch {
              case cs {
                container k { leaf base { type string; } }
              }
            }
          }
        }`,
		"b": `module b {
          namespace "urn:b";
          prefix b;
          import t { prefix t; }
          augment "/t:c/t:ch/t:cs" {
            container k { leaf own { type string; } }
          }
        }`,
		"d": `module d {
          namespace "urn:d";
          prefix d;
          import t { prefix t; }
          import b { prefix b; }
          augment "/t:c/t:ch/t:cs/b:k" {
            leaf z { type string; }
          }
        }`,
	})
	requireRecoveryError(t, err, "/t:c/t:ch/t:cs/b:k", "b.yang", "t.yang")
}

func TestRecoverRejectsPathThroughShorthandCaseCollision(t *testing.T) {
	_, err := recoveryVendor(t, map[string]string{
		"t": `module t {
          yang-version 1.1;
          namespace "urn:t";
          prefix t;
          container c {
            choice ch {
              container k {
                container sub { leaf base { type string; } }
              }
            }
          }
        }`,
		"b": `module b {
          namespace "urn:b";
          prefix b;
          import t { prefix t; }
          augment "/t:c/t:ch/t:k/t:k" {
            container sub { leaf own { type string; } }
          }
        }`,
		"d": `module d {
          namespace "urn:d";
          prefix d;
          import t { prefix t; }
          import b { prefix b; }
          augment "/t:c/t:ch/t:k/t:k/b:sub" {
            leaf z { type string; }
          }
        }`,
	})
	requireRecoveryError(t, err, "/t:c/t:ch/t:k/t:k/b:sub", "b.yang", "t.yang")
}

func TestRecoverAllowsPathThroughCaseOwnSurvivor(t *testing.T) {
	vs, err := recoveryVendor(t, map[string]string{
		"t": `module t {
          yang-version 1.1;
          namespace "urn:t";
          prefix t;
          container c {
            choice ch {
              case cs {
                container k { leaf base { type string; } }
              }
            }
          }
        }`,
		"b": `module b {
          namespace "urn:b";
          prefix b;
          import t { prefix t; }
          augment "/t:c/t:ch/t:cs" {
            container k { leaf own { type string; } }
          }
        }`,
		"d": `module d {
          namespace "urn:d";
          prefix d;
          import t { prefix t; }
          augment "/t:c/t:ch/t:cs/t:k" {
            leaf y { type string; }
          }
        }`,
	})
	if err != nil {
		t.Fatalf("LoadVendor: %v", err)
	}
	parent := moduleByName(t, vs, "t").Entry.Dir["c"]
	cs := parent.Dir["ch"].Dir["cs"]
	if got := len(vs.Recovered[cs]); got != 1 {
		t.Fatalf("recovered = %d, want 1", got)
	}
	if cs.Dir["k"].Dir["y"] == nil {
		t.Fatal("own survivor lost its nested augment")
	}
}

func TestRecoverDeviatedAwayAugment(t *testing.T) {
	vs, err := recoveryVendor(t, map[string]string{
		"t": `module t { namespace "urn:t"; prefix t;
          container c { leaf name { type string; } } }`,
		"b": `module b { namespace "urn:b"; prefix b; import t { prefix t; }
          augment "/t:c" { leaf extra { type string; } } }`,
		"d": `module d { namespace "urn:d"; prefix d; import t { prefix t; } import b { prefix b; }
          deviation "/t:c/b:extra" { deviate not-supported; } }`,
	})
	if err != nil {
		t.Fatalf("LoadVendor: %v", err)
	}
	parent := moduleByName(t, vs, "t").Entry.Dir["c"]
	if got := len(vs.Recovered[parent]); got != 0 {
		t.Fatalf("recovered = %d, want 0", got)
	}
}

func TestRecoverRejectsUnexplainedAugmentCollision(t *testing.T) {
	ms := parseRecoveryModules(t, map[string]string{
		"t": `module t { namespace "urn:t"; prefix t;
          container c { leaf x { type string; } } }`,
		"b": `module b { namespace "urn:b"; prefix b; import t { prefix t; }
          augment "/t:c" { leaf x { type uint32; } } }`,
	})
	target := yang.ToEntry(ms.Modules["t"]).Dir["c"]
	target.Errors = nil
	_, err := recoverAugments("recovery", ms)
	requireRecoveryError(t, err, "unexplained augment collision", "/t:c", "x", "b.yang", "t.yang")
}

func TestRecoverRejectsLeafrefThroughCollision(t *testing.T) {
	_, err := recoveryVendor(t, map[string]string{
		"t": `module t {
          yang-version 1.1;
          namespace "urn:t";
          prefix t;
          container c {
            container k { leaf base { type string; } }
          }
        }`,
		"b": `module b { namespace "urn:b"; prefix b; import t { prefix t; }
          augment "/t:c" {
            container k { leaf v { type uint8; } }
          }
        }`,
		"x": `module x {
          namespace "urn:x";
          prefix x;
          import t { prefix t; }
          import b { prefix b; }
          container box {
            leaf ref {
              type leafref {
                path "/t:c/b:k/b:v";
              }
            }
          }
        }`,
	})
	requireRecoveryError(t, err, "ref", "/t:c/b:k/b:v", "x.yang", "b.yang", "t.yang")
}

func TestRecoverRejectsRelativeLeafrefThroughCollision(t *testing.T) {
	_, err := recoveryVendor(t, map[string]string{
		"t": `module t {
          yang-version 1.1;
          namespace "urn:t";
          prefix t;
          container c {
            container k { leaf base { type string; } }
          }
          container box {
            leaf ref {
              type leafref {
                path "../../c/b:k/b:v";
              }
            }
          }
        }`,
		"b": `module b {
          namespace "urn:b";
          prefix b;
          import t { prefix t; }
          augment "/t:c" {
            container k { leaf v { type uint8; } }
          }
        }`,
	})
	requireRecoveryError(t, err, "ref", "../../c/b:k/b:v", "t.yang", "b.yang")
}

func TestRecoverAllowsLeafrefThroughOwnSurvivor(t *testing.T) {
	vs, err := recoveryVendor(t, map[string]string{
		"t": `module t {
          yang-version 1.1;
          namespace "urn:t";
          prefix t;
          container c {
            container k { leaf base { type string; } }
          }
          container box {
            leaf ref {
              type leafref {
                path "/t:c/t:k/t:base";
              }
            }
          }
        }`,
		"b": `module b {
          namespace "urn:b";
          prefix b;
          import t { prefix t; }
          augment "/t:c" {
            container k { leaf v { type uint8; } }
          }
        }`,
	})
	if err != nil {
		t.Fatalf("LoadVendor: %v", err)
	}
	parent := moduleByName(t, vs, "t").Entry.Dir["c"]
	if got := len(vs.Recovered[parent]); got != 1 {
		t.Fatalf("recovered = %d, want 1", got)
	}
}

func TestRecoverAllowsGroupingLeafrefThroughOwnSurvivor(t *testing.T) {
	_, err := recoveryVendor(t, map[string]string{
		"g": `module g {
          namespace "urn:g";
          prefix g;
          grouping gr {
            leaf ref { type leafref { path "../k/base"; } }
          }
        }`,
		"t": `module t {
          namespace "urn:t";
          prefix t;
          import g { prefix g; }
          container c {
            container k { leaf base { type string; } }
            uses g:gr;
          }
        }`,
		"b": `module b {
          namespace "urn:b";
          prefix b;
          import t { prefix t; }
          augment "/t:c" { container k { leaf own { type string; } } }
        }`,
	})
	if err != nil {
		t.Fatalf("LoadVendor: %v", err)
	}
}

func TestRecoverAllowsSubmoduleLeafrefThroughOwnSurvivor(t *testing.T) {
	_, err := recoveryVendor(t, map[string]string{
		"p": `module p {
          namespace "urn:p";
          prefix p;
          import q { prefix q; }
          include s;
          container c {
            container k { leaf base { type string; } }
          }
        }`,
		"s": `submodule s {
          belongs-to p { prefix p; }
          import r { prefix q; }
          augment "/p:c" {
            leaf ref { type leafref { path "../k/base"; } }
          }
        }`,
		"q": `module q { namespace "urn:q"; prefix q; container q; }`,
		"r": `module r { namespace "urn:r"; prefix r; container r; }`,
		"b": `module b {
          namespace "urn:b";
          prefix b;
          import p { prefix p; }
          augment "/p:c" { container k { leaf own { type string; } } }
        }`,
	})
	if err != nil {
		t.Fatalf("LoadVendor: %v", err)
	}
}

func TestRecoverRejectsSubmodulePathThroughCollision(t *testing.T) {
	_, err := recoveryVendor(t, map[string]string{
		"p": `module p {
          namespace "urn:p";
          prefix p;
          import q { prefix q; }
          include s;
          container c {
            container k { leaf base { type string; } }
          }
        }`,
		"s": `submodule s {
          belongs-to p { prefix p; }
          import r { prefix q; }
          import b { prefix b; }
          augment "/p:c/b:k" { leaf z { type string; } }
        }`,
		"q": `module q { namespace "urn:q"; prefix q; container q; }`,
		"r": `module r { namespace "urn:r"; prefix r; container r; }`,
		"b": `module b {
          namespace "urn:b";
          prefix b;
          import p { prefix p; }
          augment "/p:c" { container k { leaf own { type string; } } }
        }`,
	})
	requireRecoveryError(t, err, "/p:c/b:k", "b.yang", "p.yang")
}

func TestRecoverIgnoresLeafrefPredicates(t *testing.T) {
	ms := parseRecoveryModules(t, map[string]string{
		"t": `module t {
          namespace "urn:t";
          prefix t;
          container c {
            list l { key "id"; leaf id { type string; } }
			}
		}`,
		"b": `module b {
          namespace "urn:b";
          prefix b;
          import t { prefix t; }
          augment "/t:c" { container k { leaf v { type string; } } }
        }`,
		"x": `module x {
          namespace "urn:x";
          prefix x;
          import t { prefix t; }
          import b { prefix b; }
          container box {
            leaf ref {
              type leafref {
                path "/t:c/t:l[t:id = current()/../../t:c/b:k/b:v]/t:id";
              }
            }
			}
		}`,
	})
	augment := ms.Modules["b"].Augment[0]
	target := yang.ToEntry(augment).Find(augment.Name)
	ref := yang.ToEntry(ms.Modules["x"]).Dir["box"].Dir["ref"]
	if _, _, err := checkPath("recovery", ref.Node, ref, ref.Type.Path, map[*yang.Entry][]*yang.Entry{
		target: {yang.ToEntry(augment).Dir["k"]},
	}); err != nil {
		t.Fatalf("checkPath: %v", err)
	}
}

func TestRecoverIgnoresPredicatesWhenCheckingRecoveredLeafrefs(t *testing.T) {
	_, err := recoveryVendor(t, map[string]string{
		"t": `module t {
          namespace "urn:t";
          prefix t;
          container c {
            container box { leaf own { type string; } }
          }
        }`,
		"b": `module b {
          namespace "urn:b";
          prefix b;
          import t { prefix t; }
          augment "/t:c" {
            container box {
              container target { leaf value { type string; } }
              leaf ref {
                type leafref {
                  path "../target[current()/../../../../../../b:k]/value";
                }
              }
            }
          }
        }`,
	})
	if err != nil {
		t.Fatalf("LoadVendor: %v", err)
	}
}

func TestCheckPathMatchesEntryFind(t *testing.T) {
	sets := []*VendorSet{fixtureVendor(t)}
	if !testing.Short() {
		cfg, err := LoadConfig("yanggen.yaml")
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		vendors, err := LoadVendors(cfg)
		if err != nil {
			t.Fatalf("LoadVendors: %v", err)
		}
		sets = append(sets, vendors...)
	}

	for _, vs := range sets {
		for _, module := range vs.Modules {
			assertLeafrefPathsMatch(t, vs.Vendor, module.Entry, vs.Recovered)
		}
	}
}

func assertLeafrefPathsMatch(t *testing.T, vendor string, root *yang.Entry, recovered map[*yang.Entry][]*yang.Entry) {
	t.Helper()
	visited := make(map[*yang.Entry]bool)
	var visit func(*yang.Entry)
	visit = func(e *yang.Entry) {
		if e == nil || visited[e] || !dataEntry(e) {
			return
		}
		visited[e] = true
		for _, path := range leafrefPaths(e.Type) {
			want := e.Find(stripPredicates(path))
			got, collided, err := checkPath(vendor, e.Node, e, path, recovered)
			if err != nil {
				t.Errorf("%s: checkPath(%q) from %s: %v", vendor, path, e.Path(), err)
				continue
			}
			if !collided && want != nil && got != want {
				t.Errorf("%s: checkPath(%q) from %s = %s, Entry.Find = %s", vendor, path, e.Path(), got.Path(), want.Path())
			}
		}
		for _, name := range sortedKeys(e.Dir) {
			visit(e.Dir[name])
		}
		for _, child := range recovered[e] {
			visit(child)
		}
	}
	visit(root)
}
