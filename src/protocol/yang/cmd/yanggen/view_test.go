package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openconfig/goyang/pkg/yang"
)

func TestBuildVendorDataViewFixture(t *testing.T) {
	vs := fixtureVendor(t)

	view, err := buildVendorDataView(vs.Modules, vs.Recovered)
	if err != nil {
		t.Fatalf("buildVendorDataView: %v", err)
	}

	mainView := view.moduleViews["fixture-main"]
	if mainView == nil {
		t.Fatal("fixture-main view is missing")
	}

	server := findDataNode(mainView.roots, "/fixture-main:servers/server")
	if server == nil {
		t.Fatal("servers/server view is missing")
	}
	if server.module.Name != "fixture-main" {
		t.Errorf("servers/server module = %q, want fixture-main", server.module.Name)
	}
	if findDataNode(server.children, "/fixture-main:servers/server/name") == nil {
		t.Fatal("servers/server parent-module child name is missing")
	}
	if findDataNode(server.children, "/fixture-main:servers/server/fixture-aug:owner") != nil {
		t.Fatal("servers/server owner leaked into parent-module children")
	}

	ownerGroup := findDataGroup(server.groups, "fixture-aug")
	if ownerGroup == nil {
		t.Fatal("servers/server fixture-aug group is missing")
	}
	owner := findDataNode(ownerGroup.children, "/fixture-main:servers/server/fixture-aug:owner")
	if owner == nil {
		t.Fatal("servers/server owner view is missing")
	}
	if owner.module.Name != "fixture-aug" {
		t.Errorf("owner module = %q, want fixture-aug", owner.module.Name)
	}

	byOwner := findDataNode(mainView.roots, "/fixture-main:shape-probe/by-owner-b")
	if byOwner == nil {
		t.Fatal("by-owner-b view is missing")
	}
	flagGroup := findDataGroup(byOwner.groups, "fixture-aug")
	if flagGroup == nil {
		t.Fatal("by-owner-b fixture-aug group is missing")
	}
	if flag := findDataNode(flagGroup.children, "/fixture-main:shape-probe/by-owner-b/fixture-aug:flag"); flag == nil {
		t.Fatal("by-owner-b flag view is missing")
	}

	aug := moduleByName(t, vs, "fixture-aug")
	recovered := cloneRecovered(vs.Recovered)
	recovered[server.entry] = append(recovered[server.entry], aug.Entry.Dir["sub-typed"])

	view, err = buildVendorDataView(vs.Modules, recovered)
	if err != nil {
		t.Fatalf("buildVendorDataView with recovered child: %v", err)
	}
	mainView = view.moduleViews["fixture-main"]
	server = findDataNode(mainView.roots, "/fixture-main:servers/server")
	ownerGroup = findDataGroup(server.groups, "fixture-aug")
	if subTyped := findDataNode(ownerGroup.children, "/fixture-main:servers/server/fixture-aug:sub-typed"); subTyped == nil {
		t.Fatal("recovered sub-typed view is missing")
	}
}

func TestBuildVendorDataViewFlattensRecoveredChoiceChildren(t *testing.T) {
	dir := t.TempDir()
	writeYangModule(t, dir, "choice-base.yang", `module choice-base {
  yang-version 1.1;
  namespace "urn:flowseer:choice-base";
  prefix cb;
  container root {
    choice mode {
      case selected {
        leaf present { type string; }
      }
    }
  }
}`)
	writeYangModule(t, dir, "choice-aug.yang", `module choice-aug {
  yang-version 1.1;
  namespace "urn:flowseer:choice-aug";
  prefix ca;
  import choice-base { prefix cb; }
  leaf recovered { type string; }
}`)
	vs, err := LoadVendor(&Vendor{Name: "choice", Paths: []string{dir}})
	if err != nil {
		t.Fatalf("LoadVendor: %v", err)
	}
	base := moduleByName(t, vs, "choice-base")
	aug := moduleByName(t, vs, "choice-aug")
	var caseEntry *yang.Entry
	var findCase func(*yang.Entry)
	findCase = func(entry *yang.Entry) {
		if caseEntry != nil {
			return
		}
		if entry.IsCase() {
			caseEntry = entry
			return
		}
		for _, child := range entry.Dir {
			findCase(child)
		}
	}
	findCase(base.Entry)
	if caseEntry == nil {
		t.Fatal("choice case is missing from the entry tree")
	}
	recovered := map[*yang.Entry][]*yang.Entry{caseEntry: {aug.Entry.Dir["recovered"]}}
	view, err := buildVendorDataView(vs.Modules, recovered)
	if err != nil {
		t.Fatalf("buildVendorDataView: %v", err)
	}
	root := findDataNode(view.moduleViews["choice-base"].roots, "/choice-base:root")
	if root == nil {
		t.Fatal("choice-base root is missing")
	}
	group := findDataGroup(root.groups, "choice-aug")
	if group == nil {
		t.Fatal("choice-aug group is missing")
	}
	recoveredNode := findDataNode(group.children, "/choice-base:root/choice-aug:recovered")
	if recoveredNode == nil {
		t.Fatal("recovered child under choice case is missing from the flattened path")
	}
}

func TestBuildVendorDataViewRejectsImportPackageCycle(t *testing.T) {
	dir := t.TempDir()
	writeYangModule(t, dir, "cyc-a.yang", `module cyc-a {
  yang-version 1.1;
  namespace "urn:flowseer:cyc-a";
  prefix a;
  import cyc-b { prefix b; }
  container a-root;
  augment "/b:b-root" { container from-a; }
}`)
	writeYangModule(t, dir, "cyc-b.yang", `module cyc-b {
  yang-version 1.1;
  namespace "urn:flowseer:cyc-b";
  prefix b;
  import cyc-a { prefix a; }
  container b-root;
  augment "/a:a-root" { container from-b; }
}`)

	vs, err := LoadVendor(&Vendor{Name: "cycle", Paths: []string{dir}})
	if err != nil {
		t.Fatalf("LoadVendor: %v", err)
	}

	_, err = buildVendorDataView(vs.Modules, vs.Recovered)
	if err == nil {
		t.Fatal("buildVendorDataView accepted an import package cycle")
	}
	if got, want := err.Error(), "cyca -> cycb -> cyca"; !strings.Contains(got, want) {
		t.Errorf("cycle error = %q, want it to contain %q", got, want)
	}
}

func cloneRecovered(in map[*yang.Entry][]*yang.Entry) map[*yang.Entry][]*yang.Entry {
	out := make(map[*yang.Entry][]*yang.Entry, len(in))
	for target, children := range in {
		out[target] = append([]*yang.Entry(nil), children...)
	}
	return out
}

func findDataNode(nodes []*dataNodeView, path string) *dataNodeView {
	for _, node := range nodes {
		if node.dataPath == path {
			return node
		}
		if found := findDataNode(node.children, path); found != nil {
			return found
		}
		for _, group := range node.groups {
			if found := findDataNode(group.children, path); found != nil {
				return found
			}
		}
	}
	return nil
}

func findDataGroup(groups []*dataGroupView, module string) *dataGroupView {
	for _, group := range groups {
		if group.module.Name == module {
			return group
		}
	}
	return nil
}

func writeYangModule(t *testing.T, dir, name, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}
