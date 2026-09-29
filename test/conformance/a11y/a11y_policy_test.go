// Package conformance gates the mechanically decidable part of the
// accessibility suppression rule: a web test or story must not make the axe
// audit in frontend/web/src/ui/a11y.test.ts pass by stripping what it reads,
// switching its rules off, or dropping what it reports.
//
// AGENTS.md forbids a suppression that makes one's own artifact pass. For
// the audit that suppression is ordinary test code, not a lint directive,
// so suppression-warn.sh never sees it.
//
// The gate reads test, story, and Storybook files line by line, so review
// still catches what it cannot see: an attribute named through a variable or
// set to a passing value (setAttribute('aria-hidden', 'false')), an aliased
// axe.configure, a story skipped by widening the audit's own skip list, an
// axe context that excludes nodes, a call split across lines, and any of
// these moved into a helper module or a Vite or Vitest config. Adding a rule
// to allowedDisabledRules is not a policy surface either, so review is where
// a new entry has to justify itself.
package conformance

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// auditFile is the one file allowed to configure axe rules.
const auditFile = "frontend/web/src/ui/a11y.test.ts"

// allowedDisabledRules are the axe rules the audit switches off. Each entry
// states why the audit cannot judge the rule.
var allowedDisabledRules = map[string]bool{
	// happy-dom has no layout and no app CSS
	// (docs/architecture/2026-09-28-web-component-contract-direction.md),
	// so axe cannot measure contrast there.
	"color-contrast": true,
}

// scriptExtensions are the extensions Vitest's default include accepts.
var scriptExtensions = []string{".ts", ".tsx", ".js", ".jsx", ".mts", ".cts", ".mjs", ".cjs"}

var (
	// removeAttribute('aria-hidden'), removeAttributeNS(null, "role"),
	// toggleAttribute('aria-hidden'), attributes.removeNamedItem('role').
	strippedAttribute = regexp.MustCompile("(?i)(?:removeAttribute(?:NS)?|toggleAttribute|removeNamedItem)\\(\\s*(?:null\\s*,\\s*)?['\"`](aria-[a-z-]+|role)['\"`]")
	// el.ariaHidden = null, the ARIA reflection form of the same strip.
	clearedReflection = regexp.MustCompile(`\.(aria[A-Z]\w*|role)\s*=\s*(?:null|undefined|''|"")`)
	disabledRule      = regexp.MustCompile(`enabled\s*:\s*false`)
	disabledRuleKey   = regexp.MustCompile(`^\s*['"]?([a-z0-9-]+)['"]?\s*:\s*\{\s*enabled\s*:\s*false\s*\},?\s*$`)
	narrowedRun       = regexp.MustCompile(`\brunOnly\s*:`)
	droppedViolations = regexp.MustCompile(`violations\s*\.\s*(?:filter|splice|pop|shift)\s*\(`)
	axeConfigure      = regexp.MustCompile(`axe\.configure\(`)
	mockedAxe         = regexp.MustCompile(`vi\.(?:mock|doMock)\(\s*['"]axe-core['"]|vi\.spyOn\(\s*axe\s*,`)
	// The Storybook a11y addon's test mode: off and todo let a violation
	// pass or downgrade it to a warning wherever the addon runs.
	addonTestMode = regexp.MustCompile(`\btest\s*:\s*['"](off|todo)['"]`)
)

// TestA11ySuppressionPolicy is the gate. It reads every test and story file
// under frontend/web and every script under frontend/web/.storybook, which
// configures the stories the audit renders.
func TestA11ySuppressionPolicy(t *testing.T) {
	root := repoRoot(t)
	webRoot := filepath.Join(root, "frontend", "web")

	scanned := 0
	err := filepath.WalkDir(webRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "dist", "generated", "storybook-static":
				return filepath.SkipDir
			}

			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !inScope(rel) {
			return nil
		}

		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++

		for _, finding := range fileViolations(rel, string(src)) {
			t.Error(finding)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", webRoot, err)
	}

	if scanned == 0 {
		t.Fatal("scanned no web test or story files; the gate checked nothing")
	}
}

// inScope reports whether the gate reads the file at the slash-separated,
// repository-relative path rel: a script under .storybook, or a test, spec,
// or story file anywhere under frontend/web.
func inScope(rel string) bool {
	ext := filepath.Ext(rel)
	isScript := false
	for _, e := range scriptExtensions {
		if ext == e {
			isScript = true
		}
	}
	if !isScript {
		return false
	}
	if strings.HasPrefix(rel, "frontend/web/.storybook/") {
		return true
	}

	stem := strings.TrimSuffix(rel, ext)

	return strings.HasSuffix(stem, ".test") ||
		strings.HasSuffix(stem, ".spec") ||
		strings.HasSuffix(stem, ".stories")
}

func fileViolations(rel, src string) []string {
	var findings []string
	for i, line := range strings.Split(src, "\n") {
		at := rel + ":" + strconv.Itoa(i+1) + ": "

		if m := strippedAttribute.FindStringSubmatch(line); m != nil {
			findings = append(findings, at+"removes "+strings.ToLower(m[1])+" from the rendered DOM; fix the component instead of stripping what the axe audit reads")
		}
		if m := clearedReflection.FindStringSubmatch(line); m != nil {
			findings = append(findings, at+"clears "+m[1]+" on a rendered element; fix the component instead of stripping what the axe audit reads")
		}
		if disabledRule.MatchString(line) && !allowedRuleLine(rel, line) {
			findings = append(findings, at+"disables an axe rule; only "+auditFile+" may, and only for a rule test/conformance/a11y allows")
		}
		if narrowedRun.MatchString(line) && rel != auditFile {
			findings = append(findings, at+"narrows the axe rules that run; the audit's options live in "+auditFile)
		}
		if droppedViolations.MatchString(line) {
			findings = append(findings, at+"drops axe violations before they are asserted; fix the component instead")
		}
		if axeConfigure.MatchString(line) {
			findings = append(findings, at+"reconfigures axe globally; the audit's options live in "+auditFile)
		}
		if mockedAxe.MatchString(line) {
			findings = append(findings, at+"replaces axe-core in a test; the audit must run the real engine")
		}
		if m := addonTestMode.FindStringSubmatch(line); m != nil {
			findings = append(findings, at+"sets the Storybook a11y test mode to "+m[1]+", which lets a violation pass")
		}
	}

	return findings
}

func allowedRuleLine(rel, line string) bool {
	if rel != auditFile {
		return false
	}
	m := disabledRuleKey.FindStringSubmatch(line)

	return m != nil && allowedDisabledRules[m[1]]
}

// TestFileViolations is the positive control. The real tree holds no
// violation, so TestA11ySuppressionPolicy alone cannot tell a working
// classifier from one that returns nil.
func TestFileViolations(t *testing.T) {
	const story = "frontend/web/src/ui/card/UiCard.stories.ts"

	strip := func(rel string, line int, attr string) string {
		return rel + ":" + strconv.Itoa(line) + ": removes " + attr + " from the rendered DOM; fix the component instead of stripping what the axe audit reads"
	}
	disable := func(rel string, line int) string {
		return rel + ":" + strconv.Itoa(line) + ": disables an axe rule; only " + auditFile + " may, and only for a rule test/conformance/a11y allows"
	}

	tests := []struct {
		name string
		rel  string
		src  string
		want []string
	}{
		{
			name: "the audit's allowed rule and tags",
			rel:  auditFile,
			src:  "  runOnly: {\n    type: 'tag',\n  },\n  rules: {\n    'color-contrast': { enabled: false },\n  },\n",
		},
		{
			name: "another rule in the audit",
			rel:  auditFile,
			src:  "    region: { enabled: false },\n",
			want: []string{disable(auditFile, 1)},
		},
		{
			name: "an allowed rule outside the audit",
			rel:  story,
			src:  "  a11y: { config: { rules: [{ id: 'color-contrast', enabled: false }] } },\n",
			want: []string{disable(story, 1)},
		},
		{
			name: "an allowed rule sharing a line with another",
			rel:  auditFile,
			src:  "  rules: { 'color-contrast': { enabled: false }, label: { enabled: false } },\n",
			want: []string{disable(auditFile, 1)},
		},
		{
			name: "aria-hidden stripped in test setup",
			rel:  auditFile,
			src:  "\n  el.removeAttribute('aria-hidden')\n",
			want: []string{strip(auditFile, 2, "aria-hidden")},
		},
		{
			name: "role stripped in a story",
			rel:  story,
			src:  "node.removeAttribute(\"role\")\n",
			want: []string{strip(story, 1, "role")},
		},
		{
			name: "namespaced, toggled, named-item, and mixed-case strips",
			rel:  story,
			src:  "el.removeAttributeNS(null, 'aria-hidden')\nel.toggleAttribute('aria-hidden', false)\nel.attributes.removeNamedItem('role')\nel.removeAttribute('Aria-Hidden')\n",
			want: []string{strip(story, 1, "aria-hidden"), strip(story, 2, "aria-hidden"), strip(story, 3, "role"), strip(story, 4, "aria-hidden")},
		},
		{
			name: "an ARIA reflection cleared",
			rel:  story,
			src:  "el.ariaHidden = null\n",
			want: []string{story + ":1: clears ariaHidden on a rendered element; fix the component instead of stripping what the axe audit reads"},
		},
		{
			name: "a role compared, not cleared",
			rel:  "frontend/web/src/domain/fleet.test.ts",
			src:  "expect(device.role === 'gateway').toBe(true)\n",
		},
		{
			name: "a role set to build a fixture",
			rel:  "frontend/web/src/FleetView.test.ts",
			src:  "    popover.setAttribute('role', 'dialog')\n",
		},
		{
			name: "a non-accessibility attribute removed",
			rel:  story,
			src:  "el.removeAttribute('data-state')\n",
		},
		{
			name: "runOnly outside the audit",
			rel:  story,
			src:  "axe.run(el, { runOnly: ['label'] })\n",
			want: []string{story + ":1: narrows the axe rules that run; the audit's options live in " + auditFile},
		},
		{
			name: "violations filtered before the assertion",
			rel:  auditFile,
			src:  "const kept = results.violations.filter((v) => v.id !== 'label')\n",
			want: []string{auditFile + ":1: drops axe violations before they are asserted; fix the component instead"},
		},
		{
			name: "violations read, not dropped",
			rel:  auditFile,
			src:  "const found = results.violations.find((v) => v.id === 'label')\nexpect(results.violations).toHaveLength(0)\n",
		},
		{
			name: "axe reconfigured",
			rel:  "frontend/web/.storybook/preview.ts",
			src:  "axe.configure({ rules: [] })\n",
			want: []string{"frontend/web/.storybook/preview.ts:1: reconfigures axe globally; the audit's options live in " + auditFile},
		},
		{
			name: "axe mocked",
			rel:  auditFile,
			src:  "vi.mock('axe-core', () => ({}))\nvi.spyOn(axe, 'run')\n",
			want: []string{
				auditFile + ":1: replaces axe-core in a test; the audit must run the real engine",
				auditFile + ":2: replaces axe-core in a test; the audit must run the real engine",
			},
		},
		{
			name: "addon test mode off",
			rel:  story,
			src:  "  parameters: { a11y: { test: 'off' } },\n",
			want: []string{story + ":1: sets the Storybook a11y test mode to off, which lets a violation pass"},
		},
		{
			name: "addon test mode error",
			rel:  "frontend/web/.storybook/preview.ts",
			src:  "      test: 'error',\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fileViolations(tt.rel, tt.src)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d findings %q, want %d %q", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("finding %d: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestInScope(t *testing.T) {
	tests := []struct {
		rel  string
		want bool
	}{
		{rel: "frontend/web/src/ui/a11y.test.ts", want: true},
		{rel: "frontend/web/src/ui/card/UiCard.stories.ts", want: true},
		{rel: "frontend/web/src/x.spec.tsx", want: true},
		{rel: "frontend/web/tests/view.test.mjs", want: true},
		{rel: "frontend/web/.storybook/preview.ts", want: true},
		{rel: "frontend/web/.storybook/main.js", want: true},
		{rel: "frontend/web/src/ui/card/UiCard.vue"},
		{rel: "frontend/web/src/main.ts"},
		{rel: "frontend/web/src/ui/a11y.test.snap"},
	}

	for _, tt := range tests {
		t.Run(tt.rel, func(t *testing.T) {
			if got := inScope(tt.rel); got != tt.want {
				t.Errorf("got %t, want %t", got, tt.want)
			}
		})
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting working directory: %v", err)
	}

	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(data), "module go.aledante.io/FlowSeer\n") {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found above the conformance package")
		}

		dir = parent
	}
}
