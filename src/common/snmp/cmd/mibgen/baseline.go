package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/smi"
)

// The diagnostic baseline is the record of what the configured MIBs are
// already known to be wrong about. The parser grades and continues, so a
// module that renders today renders with a tail of diagnostics behind
// it; without a record of that tail there is no way to tell a condition
// somebody has already read from one that arrived with the last upstream
// re-sync. The baseline is that record, and generation fails on anything
// it does not hold.
//
// An entry is keyed on the diagnostic's code and the name of the
// declaration it landed in, and on nothing else. File, line and column
// are all left out on purpose: an upstream MIB gains a paragraph of
// DESCRIPTION and every offset in the file after it moves, which would
// invalidate a position-keyed baseline without a single condition having
// changed. A code and a descriptor survive that; they change when the
// MIB changes, which is when the gate is supposed to speak up.
//
// The two-tier shape is the one the conformance corpus already uses.
// The always-on gate tolerates a `pending` group so a branch mid-triage
// still builds, and the completeness gate behind the
// mibgen_baseline_complete build tag forbids one, so nothing merges with
// a diagnostic nobody has written down a reason for. An `accepted-risk`
// group needs both a reason and an allowlist entry, which makes waiving
// a condition a diff two people can read rather than a string one person
// can type.

// BaselineStatus is a group's lifecycle state.
type BaselineStatus string

// The group states.
const (
	// BaselinePending is a group that has been recorded but not yet
	// justified. The always-on gate permits it; the completeness gate
	// behind the mibgen_baseline_complete tag does not.
	BaselinePending BaselineStatus = "pending"

	// BaselineRecorded is a group somebody has read and accepted. A
	// group whose diagnostics are graded fatal or error must carry a
	// reason to reach this state.
	BaselineRecorded BaselineStatus = "recorded"

	// BaselineAcceptedRisk is a group deliberately tolerated for as long
	// as the MIB stays as it is. It needs a reason and an entry on the
	// allowlist, so the waiver appears twice in the diff.
	BaselineAcceptedRisk BaselineStatus = "accepted-risk"
)

// FileScopeDeclaration stands in for the declaration name of a
// diagnostic raised before the file's first declaration head, such as a
// malformed module header. It is spelled with angle brackets because
// RFC 2578 gives a descriptor no way to contain one, so it can never
// collide with a real name.
const FileScopeDeclaration = "<file>"

// BaselineGroup is every diagnostic of one code inside one module,
// listed by the declaration each one landed in.
//
// The code and the justification are factored out of the individual
// entries because a module that re-registers eight arcs from an older
// SMI has eight instances of one decision, and eight copies of the same
// sentence is harder to review than one sentence and eight names.
type BaselineGroup struct {
	// Code is the diagnostic's stable identity, such as
	// "smi/duplicate-oid".
	Code string `yaml:"code"`

	// Status is one of [BaselinePending], [BaselineRecorded] or
	// [BaselineAcceptedRisk].
	Status BaselineStatus `yaml:"status"`

	// Reason says why these diagnostics are acceptable here. It is
	// required for an accepted-risk group and for any group whose
	// diagnostics are graded fatal or error.
	Reason string `yaml:"reason,omitempty"`

	// Declarations names each declaration the code was raised inside,
	// sorted, with [FileScopeDeclaration] for a diagnostic that landed
	// before any declaration.
	Declarations []string `yaml:"declarations"`
}

// BaselineModule is one configured module's record.
type BaselineModule struct {
	Module      string          `yaml:"module"`
	Diagnostics []BaselineGroup `yaml:"diagnostics"`
}

// Baseline is the whole committed record, as it sits on disk.
type Baseline struct {
	Modules []BaselineModule `yaml:"modules"`

	// AcceptedRiskAllowlist holds one "MODULE:code:declaration" key per
	// accepted-risk entry. Naming the entry a second time is the point:
	// a waiver cannot be slipped in by flipping one word.
	AcceptedRiskAllowlist []string `yaml:"accepted_risk_allowlist"`
}

// baselineKey is what an entry is keyed on: the module, the diagnostic
// code, and the declaration the diagnostic landed in.
type baselineKey struct {
	module string
	code   string
	decl   string
}

// String renders the key the way the allowlist and the failure messages
// spell it.
func (k baselineKey) String() string {
	return k.module + ":" + k.code + ":" + k.decl
}

// LoadBaseline reads a baseline file. Unknown keys are an error, as they
// are for the config, so a misspelled field cannot quietly turn a gate
// off.
func LoadBaseline(path string) (*Baseline, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		// Preserve os.ErrNotExist so a caller can tell a missing
		// baseline from an unreadable one.
		return nil, errs.Wrapf(err, "baseline %s", path)
	}

	return ParseBaseline(b, path)
}

// ParseBaseline decodes baseline YAML. displayPath only reaches error
// messages, so a test may pass anything readable.
func ParseBaseline(src []byte, displayPath string) (*Baseline, error) {
	var bl Baseline
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	if err := dec.Decode(&bl); err != nil {
		if !errors.Is(err, io.EOF) {
			return nil, errs.Wrapf(err, "baseline %s: parse YAML", displayPath)
		}
		// An empty file is an empty baseline, which is a legitimate
		// state for a config whose modules raise nothing.
	}

	return &bl, nil
}

// entries flattens the grouped file into the keyed form the gate reads.
// A key repeated across two groups is reported rather than silently
// resolved, because the two groups may disagree about the status.
func (b *Baseline) entries() (map[baselineKey]BaselineGroup, error) {
	out := make(map[baselineKey]BaselineGroup)
	for _, m := range b.Modules {
		for _, g := range m.Diagnostics {
			for _, decl := range g.Declarations {
				k := baselineKey{module: m.Module, code: g.Code, decl: decl}
				if _, dup := out[k]; dup {
					return nil, errs.Msgf("baseline: %s is recorded twice", k)
				}
				out[k] = g
			}
		}
	}

	return out, nil
}

// declaredNames returns, per module, the set of declaration names the
// baseline records anything against. It is what the unresolved-
// declaration refusal consults.
func (b *Baseline) declaredNames() map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(b.Modules))
	for _, m := range b.Modules {
		names := out[m.Module]
		if names == nil {
			names = make(map[string]bool)
			out[m.Module] = names
		}
		for _, g := range m.Diagnostics {
			for _, decl := range g.Declarations {
				names[decl] = true
			}
		}
	}

	return out
}

// allowlist returns the accepted-risk keys as a set.
func (b *Baseline) allowlist() map[string]bool {
	out := make(map[string]bool, len(b.AcceptedRiskAllowlist))
	for _, k := range b.AcceptedRiskAllowlist {
		out[k] = true
	}

	return out
}

// PendingGroups returns "MODULE code" for every group still pending, in
// file order. It is what the completeness gate reports.
func (b *Baseline) PendingGroups() []string {
	var out []string
	for _, m := range b.Modules {
		for _, g := range m.Diagnostics {
			if g.Status == BaselinePending {
				out = append(out, m.Module+" "+g.Code)
			}
		}
	}

	return out
}

// knownDiagnosticCodes is every diagnostic code the linked parser
// registers. A baseline naming anything else is a stale row, and a stale
// row is exactly how a renamed code would silently stop covering
// anything.
func knownDiagnosticCodes() map[string]bool {
	out := make(map[string]bool)
	for _, c := range errs.Codes() {
		if strings.HasPrefix(c.String(), "smi/") {
			out[c.String()] = true
		}
	}

	return out
}

// Validate checks what can be checked without loading a MIB: known
// codes, known statuses, the accepted-risk obligations, and a
// declaration list that is neither empty nor duplicated.
//
// Whether a group needs a reason because its diagnostics are graded
// fatal or error is not decided here. A severity belongs to a diagnostic
// rather than to a code — the catalog may regrade one — so that check
// waits until a load has produced the diagnostics themselves.
func (b *Baseline) Validate() error {
	known := knownDiagnosticCodes()
	allow := b.allowlist()

	var problems []string
	seenModule := make(map[string]bool, len(b.Modules))
	for _, m := range b.Modules {
		if m.Module == "" {
			problems = append(problems, "a baseline module entry has no name")

			continue
		}
		if seenModule[m.Module] {
			problems = append(problems, fmt.Sprintf("module %q appears twice", m.Module))
		}
		seenModule[m.Module] = true

		seenCode := make(map[string]bool, len(m.Diagnostics))
		for _, g := range m.Diagnostics {
			problems = append(problems, validateGroup(m.Module, g, known, allow, seenCode)...)
			seenCode[g.Code] = true
		}
	}

	if _, err := b.entries(); err != nil {
		problems = append(problems, err.Error())
	}

	if len(problems) == 0 {
		return nil
	}

	return errs.Msgf("baseline is not valid:\n  %s", strings.Join(problems, "\n  "))
}

// validateGroup reports everything wrong with one group.
func validateGroup(module string, g BaselineGroup, known, allow map[string]bool, seenCode map[string]bool) []string {
	var problems []string
	where := fmt.Sprintf("module %s, code %s", module, g.Code)

	if !known[g.Code] {
		problems = append(problems, where+": no such diagnostic code; the parser's catalog does not define it")
	}
	if seenCode[g.Code] {
		problems = append(problems, where+": the code is listed twice for this module")
	}
	if len(g.Declarations) == 0 {
		problems = append(problems, where+": names no declarations, so it records nothing")
	}
	if !slices.IsSorted(g.Declarations) {
		problems = append(problems, where+": declarations are not sorted, which makes the diff order depend on the editor")
	}

	switch g.Status {
	case BaselinePending, BaselineRecorded:
	case BaselineAcceptedRisk:
		if strings.TrimSpace(g.Reason) == "" {
			problems = append(problems, where+": is accepted-risk with no reason")
		}
		for _, decl := range g.Declarations {
			k := baselineKey{module: module, code: g.Code, decl: decl}
			if !allow[k.String()] {
				problems = append(problems, where+": "+k.String()+" is accepted-risk but not on the allowlist (add it as a reviewable diff)")
			}
		}
	default:
		problems = append(problems, where+fmt.Sprintf(": unknown status %q", g.Status))
	}

	return problems
}

// declarationHead is where one declaration begins in a MIB source file
// and what it is called.
type declarationHead struct {
	offset int
	name   string
}

// scanDeclarationHeads finds the offset and name of every declaration in
// src.
//
// The parser reports a diagnostic's position as a byte offset and does
// not publish the span of the declaration that offset falls in, so
// attributing a diagnostic to a name has to be done from the source. A
// MIB writes every declaration head against the left margin — RFC 2578's
// grammar does not require it, and no file in the corpus departs from
// it — so the head is the identifier at column one of a line that is
// neither a comment nor the inside of a quoted string.
//
// A file that defeats the scan costs a name that is stable but wrong,
// never a name that changes between runs, which is what the baseline
// needs: the key has to mean the same thing on both sides of a diff.
func scanDeclarationHeads(src []byte) []declarationHead {
	var (
		heads       []declarationHead
		inString    bool
		atLineStart = true
	)

	for i := 0; i < len(src); i++ {
		c := src[i]

		if inString {
			if c == '"' {
				inString = false
			}
			atLineStart = c == '\n'

			continue
		}

		switch {
		case c == '"':
			inString = true
			atLineStart = false
		case c == '-' && i+1 < len(src) && src[i+1] == '-':
			// End-of-line comment termination, which is what all but a
			// handful of files in the corpus mean. Reading a paired
			// comment as one that runs to the newline over-skips, and
			// over-skipping only ever costs a head, never invents one.
			for i < len(src) && src[i] != '\n' {
				i++
			}
			atLineStart = true
		case c == '\n':
			atLineStart = true
		case atLineStart && isDeclarationHeadStart(c):
			name, next := readIdentifier(src, i)
			if isDeclarationHeadName(src, name, next) {
				heads = append(heads, declarationHead{offset: i, name: name})
			}
			i = next - 1
			atLineStart = false
		default:
			atLineStart = false
		}
	}

	return heads
}

// isDeclarationHeadStart reports whether c can begin a descriptor.
func isDeclarationHeadStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// readIdentifier returns the identifier beginning at start and the
// offset one past it. The character set is RFC 2578's descriptor plus
// the underscore the corpus writes anyway.
func readIdentifier(src []byte, start int) (string, int) {
	i := start
	for i < len(src) {
		c := src[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			i++

			continue
		}

		break
	}

	return string(src[start:i]), i
}

// nonDeclarationHeads are the keywords a MIB writes at the left margin
// that head no declaration. Attributing a diagnostic to "END" would name
// something no reviewer can go and look at.
var nonDeclarationHeads = map[string]bool{
	"BEGIN":   true,
	"END":     true,
	"EXPORTS": true,
	"FROM":    true,
	"IMPORTS": true,
}

// isDeclarationHeadName reports whether name, ending at next, reads as a
// declaration head rather than as prose or a keyword. A head is followed
// by whitespace, since the macro name or the "::=" comes after it.
func isDeclarationHeadName(src []byte, name string, next int) bool {
	if name == "" || nonDeclarationHeads[name] {
		return false
	}
	if next >= len(src) {
		return false
	}

	switch src[next] {
	case ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}

// declarationAt returns the name of the declaration offset falls in, or
// [FileScopeDeclaration] when it falls before the first one.
func declarationAt(heads []declarationHead, offset int) string {
	idx, found := slices.BinarySearchFunc(heads, offset, func(h declarationHead, target int) int {
		return h.offset - target
	})
	if found {
		return heads[idx].name
	}
	if idx == 0 {
		return FileScopeDeclaration
	}

	return heads[idx-1].name
}

// observation is one diagnostic, attributed.
type observation struct {
	key      baselineKey
	severity smi.Severity
	rendered string
}

// observe attributes every diagnostic raised against a configured
// module's own file to a declaration in it.
//
// Diagnostics raised against the modules the IMPORTS dragged in are left
// alone. A module the config never asked to render is not this config's
// problem, and holding one against it would make adding a module to the
// list a change to some other module's baseline.
func observe(cfg *Config, set *smi.ModuleSet) ([]observation, error) {
	byFile := make(map[string]string, len(cfg.Modules))
	headsByFile := make(map[string][]declarationHead, len(cfg.Modules))

	for _, cm := range cfg.Modules {
		mod, ok := set.Module(cm.Name)
		if !ok {
			return nil, errs.Msgf("module %q resolved to nothing", cm.Name)
		}
		if mod.File == "" {
			continue
		}
		byFile[mod.File] = mod.Name

		src, err := os.ReadFile(mod.File)
		if err != nil {
			return nil, errs.Wrapf(err, "module %q: read source for diagnostic attribution", cm.Name)
		}
		headsByFile[mod.File] = scanDeclarationHeads(src)
	}

	diags := set.Diagnostics()
	rendered := set.Render()

	var out []observation
	for i, d := range diags {
		file := d.Position().File
		module, ok := byFile[file]
		if !ok {
			continue
		}
		out = append(out, observation{
			key: baselineKey{
				module: module,
				code:   d.Code().String(),
				decl:   declarationAt(headsByFile[file], d.Position().Offset),
			},
			severity: d.Severity(),
			rendered: rendered[i].String(),
		})
	}

	return out, nil
}

// maxReportedBaselineProblems bounds how much a failure prints. A
// baseline that is one diagnostic behind is worth reading in full; one
// that is four hundred behind has had an upstream re-sync land on it,
// and the first few lines say so just as well.
const maxReportedBaselineProblems = 20

// CheckBaseline is the gate. It fails on any diagnostic the baseline
// does not record, on any recorded fatal- or error-graded diagnostic
// with no reason written down, and on any unresolved declaration the
// baseline says nothing about.
//
// It never writes the baseline. A gate that repairs itself reports
// nothing, which is the one thing a gate is for; refreshing is a
// separate, deliberate invocation.
func CheckBaseline(cfg *Config, set *smi.ModuleSet, bl *Baseline) error {
	if err := bl.Validate(); err != nil {
		return err
	}

	entries, err := bl.entries()
	if err != nil {
		return err
	}

	observed, err := observe(cfg, set)
	if err != nil {
		return err
	}

	problems := checkObservations(observed, entries)
	problems = append(problems, checkUnresolved(cfg, set, bl.declaredNames())...)

	if len(problems) == 0 {
		return nil
	}

	shown := problems
	truncated := 0
	if len(shown) > maxReportedBaselineProblems {
		truncated = len(shown) - maxReportedBaselineProblems
		shown = shown[:maxReportedBaselineProblems]
	}

	msg := fmt.Sprintf("%d diagnostic(s) are not covered by the committed baseline:\n  %s",
		len(problems), strings.Join(shown, "\n  "))
	if truncated > 0 {
		msg += fmt.Sprintf("\n  ... and %d more", truncated)
	}

	return errs.Msgf("%s\n\nRerun with -refresh-baseline, then record a reason for each new group.", msg)
}

// checkObservations reports every observed diagnostic the baseline does
// not hold, and every held one whose grade demands a reason it does not
// carry. Findings are deduplicated by key so a code raised eight times
// under one declaration costs one line.
//
// A pending group is exempt from the reason obligation, which is the
// whole of what pending means: the condition has been written down but
// not yet read. The completeness gate is what stops the exemption from
// outliving the branch it was convenient on.
func checkObservations(observed []observation, entries map[baselineKey]BaselineGroup) []string {
	var problems []string
	reported := make(map[baselineKey]bool, len(observed))

	for _, o := range observed {
		if reported[o.key] {
			continue
		}
		reported[o.key] = true

		g, ok := entries[o.key]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s is not in the baseline: %s", o.key, o.rendered))

			continue
		}
		if g.Status != BaselinePending && o.severity.NeedsBaselineReason() && strings.TrimSpace(g.Reason) == "" {
			problems = append(problems, fmt.Sprintf(
				"%s is graded %s and needs a recorded reason: %s", o.key, o.severity, o.rendered))
		}
	}

	sort.Strings(problems)

	return problems
}

// maxReportedUnresolved bounds how many names one module's refusal
// lists. A module that lost one declaration is worth reading in full;
// one that lost two hundred is a broken file, and the first few names
// say so just as well.
const maxReportedUnresolved = 10

// checkUnresolved refuses a configured module carrying a declaration the
// resolver could not complete, unless the baseline records that
// declaration.
func checkUnresolved(cfg *Config, set *smi.ModuleSet, declared map[string]map[string]bool) []string {
	var problems []string
	for _, cm := range cfg.Modules {
		mod, ok := set.Module(cm.Name)
		if !ok {
			continue
		}
		if err := refuseUnresolved(mod, declared[mod.Name]); err != nil {
			problems = append(problems, err.Error())
		}
	}

	return problems
}

// refuseUnresolved rejects a module carrying a declaration or type the
// resolver could not complete and the baseline does not record.
//
// Rendering one anyway is the failure mode worth avoiding: a declaration
// missing its SYNTAX still has an OID and a name, so it emits an
// accessor that compiles, ships, and decodes the wrong thing. Refusing
// costs a build; emitting costs a wrong value on a wire nobody is
// watching. A baselined name is one somebody has already looked at and
// signed off, so it does not cost the build a second time.
func refuseUnresolved(mod *smi.Module, baselined map[string]bool) error {
	var lost []string
	for _, n := range mod.Nodes {
		if n.Unresolved && !baselined[n.Name] {
			lost = append(lost, n.Name)
		}
	}
	for _, t := range mod.Types {
		if t.Unresolved && !baselined[t.Name] {
			lost = append(lost, t.Name)
		}
	}
	if len(lost) == 0 {
		return nil
	}

	shown := lost
	if len(shown) > maxReportedUnresolved {
		shown = shown[:maxReportedUnresolved]
	}

	return errs.Msgf("module %q: %d unresolved declaration(s) the baseline does not record, refusing to emit a partial package: %s",
		mod.Name, len(lost), strings.Join(shown, ", "))
}

// RefreshBaseline rebuilds the baseline from what a load actually
// raised, carrying the status and the reason of every group that still
// has diagnostics under it.
//
// A group that gains a declaration keeps the reason it had, because the
// added name is visible in the diff and the sentence covering its
// siblings almost always covers it too. A group that appears for the
// first time comes back pending with no reason, which keeps a branch
// building while leaving the completeness gate to insist somebody
// writes one.
func RefreshBaseline(cfg *Config, set *smi.ModuleSet, old *Baseline) (*Baseline, error) {
	observed, err := observe(cfg, set)
	if err != nil {
		return nil, err
	}

	type groupKey struct{ module, code string }

	prior := make(map[groupKey]BaselineGroup)
	if old != nil {
		for _, m := range old.Modules {
			for _, g := range m.Diagnostics {
				prior[groupKey{module: m.Module, code: g.Code}] = g
			}
		}
	}

	decls := make(map[groupKey]map[string]bool)
	for _, o := range observed {
		k := groupKey{module: o.key.module, code: o.key.code}
		if decls[k] == nil {
			decls[k] = make(map[string]bool)
		}
		decls[k][o.key.decl] = true
	}

	// Modules come back in the config's order so the file reads the way
	// the config does, and a reviewer comparing the two does not have to
	// hold a second ordering in their head.
	fresh := &Baseline{}
	var allow []string
	for _, cm := range cfg.Modules {
		var groups []BaselineGroup
		for k, names := range decls {
			if k.module != cm.Name {
				continue
			}
			g := BaselineGroup{Code: k.code, Status: BaselinePending}
			if p, ok := prior[k]; ok {
				g.Status = p.Status
				g.Reason = p.Reason
			}
			g.Declarations = sortedKeys(names)
			groups = append(groups, g)

			if g.Status == BaselineAcceptedRisk {
				for _, decl := range g.Declarations {
					allow = append(allow, baselineKey{module: k.module, code: k.code, decl: decl}.String())
				}
			}
		}
		if len(groups) == 0 {
			continue
		}
		sort.Slice(groups, func(i, j int) bool { return groups[i].Code < groups[j].Code })
		fresh.Modules = append(fresh.Modules, BaselineModule{Module: cm.Name, Diagnostics: groups})
	}

	sort.Strings(allow)
	fresh.AcceptedRiskAllowlist = allow

	return fresh, nil
}

// sortedKeys returns a set's members in a stable order.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)

	return out
}

// baselineHeader is the preamble a refreshed file carries, so the next
// person to open it knows what it is for before reading a single row.
const baselineHeader = `# mibgen diagnostic baseline.
#
# Every diagnostic the configured MIB modules raise is recorded here.
# Generation fails on anything absent from this file, and never on
# anything present in it.
#
# An entry is keyed on the diagnostic code and the declaration it landed
# in, and on nothing else, so an upstream re-sync that moves a line does
# not invalidate the record.
#
# Refresh with:
#
#	go run ./src/common/snmp/cmd/mibgen -refresh-baseline
#
# A refreshed group arrives as "pending". Read it, write down why the
# condition is acceptable, and set the status to "recorded"; a group
# graded fatal or error cannot be recorded without a reason. Tolerating
# one indefinitely is "accepted-risk", which needs the reason and an
# accepted_risk_allowlist entry as well.
`

// WriteBaseline renders bl to path.
func WriteBaseline(path string, bl *Baseline) error {
	var buf bytes.Buffer
	buf.WriteString(baselineHeader)

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(bl); err != nil {
		return errs.Wrapf(err, "baseline %s: encode YAML", path)
	}
	if err := enc.Close(); err != nil {
		return errs.Wrapf(err, "baseline %s: flush YAML", path)
	}

	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return errs.Wrapf(err, "baseline %s: write", path)
	}

	return nil
}
