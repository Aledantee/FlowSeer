package proto_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// genTemplate is the part of a buf generate template these tests judge.
// Managed is decoded as an opaque tree so a field added to either file is
// compared without this struct having to learn about it.
type genTemplate struct {
	Managed any `yaml:"managed"`
	Plugins []struct {
		Remote string `yaml:"remote"`
		Out    string `yaml:"out"`
	} `yaml:"plugins"`
}

// buf.gen.go.yaml carries a hand-copy of buf.gen.yaml's managed block, because
// buf has no template inheritance. Managed mode rewrites go_package at
// generation time, so if the copies drift a scoped run and an unscoped run
// emit different import paths for the same sources — and the drift surfaces as
// a build error in whichever tree was generated second, far from the edit that
// caused it.
func TestGenerationTemplatesAgreeOnManagedMode(t *testing.T) {
	full := decodeGenTemplate(t, "buf.gen.yaml")
	goOnly := decodeGenTemplate(t, "buf.gen.go.yaml")

	if !reflect.DeepEqual(goOnly.Managed, full.Managed) {
		t.Errorf("got managed block %#v in buf.gen.go.yaml, want buf.gen.yaml's %#v", goOnly.Managed, full.Managed)
	}
}

// The whole reason buf.gen.go.yaml exists: `--path` narrows inputs, not
// plugins, so a scoped run with the full template still fires the TypeScript
// plugin and buf creates the missing output directory. A plugin writing
// anywhere but the Go tree would put that behavior back.
func TestGoTemplateEmitsOnlyGoOutput(t *testing.T) {
	goOnly := decodeGenTemplate(t, "buf.gen.go.yaml")

	if len(goOnly.Plugins) == 0 {
		t.Fatal("got no plugins in buf.gen.go.yaml, want the Go plugin set")
	}
	for _, p := range goOnly.Plugins {
		if p.Out != "generated/go/proto" {
			t.Errorf("got out %q for plugin %q, want generated/go/proto", p.Out, p.Remote)
		}
	}
}

func decodeGenTemplate(t *testing.T, name string) genTemplate {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	var tpl genTemplate
	if err := yaml.Unmarshal(raw, &tpl); err != nil {
		t.Fatalf("decoding %s: %v", name, err)
	}
	return tpl
}
