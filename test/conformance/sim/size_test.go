package conformance

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	maxFileLines = 1200
	maxFuncLines = 150
)

// oneUnitReason is what counts as a stated reason to exceed maxFuncLines. The
// head comment must say "one unit because" followed by at
// least three more words, so a bare "one unit" does not pass.
var oneUnitReason = regexp.MustCompile(`(?i)\bone unit because\s+\S+(\s+\S+){2,}`)

// sizeFindings reports every limit the named source exceeds: its line count,
// each function declaration and literal, including nested literals, the last
// two unless their doc comment states a reason under oneUnitReason.
func sizeFindings(name string, source []byte) ([]string, error) {
	var findings []string
	lines := strings.Count(string(source), "\n")
	if len(source) > 0 && source[len(source)-1] != '\n' {
		lines++
	}
	if lines > maxFileLines {
		findings = append(findings, fmt.Sprintf("%s: %d lines, limit %d", name, lines, maxFileLines))
	}
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, name, source, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	check := func(label string, node ast.Node, doc *ast.CommentGroup) {
		length := positions.Position(node.End()).Line - positions.Position(node.Pos()).Line + 1
		if length > maxFuncLines && (doc == nil || !oneUnitReason.MatchString(doc.Text())) {
			findings = append(findings, fmt.Sprintf("%s:%d: %s has %d lines, limit %d without a stated reason to keep one unit", name, positions.Position(node.Pos()).Line, label, length, maxFuncLines))
		}
	}
	// A declaration head belongs to a literal only when it initializes that
	// literal alone. Group heads cannot document siblings.
	heads := make(map[*ast.FuncLit]token.Pos)
	ast.Inspect(file, func(node ast.Node) bool {
		var values []ast.Expr
		switch node := node.(type) {
		case *ast.GenDecl:
			if node.Lparen.IsValid() || len(node.Specs) != 1 {
				return true
			}
			if value, ok := node.Specs[0].(*ast.ValueSpec); ok {
				values = value.Values
			}
		case *ast.ValueSpec:
			values = node.Values
		case *ast.AssignStmt:
			values = node.Rhs
		default:
			return true
		}
		if len(values) == 1 {
			if literal, ok := values[0].(*ast.FuncLit); ok {
				if _, exists := heads[literal]; !exists {
					heads[literal] = node.Pos()
				}
			}
		}
		return true
	})
	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.FuncDecl:
			check(node.Name.Name, node, node.Doc)
		case *ast.FuncLit:
			var doc *ast.CommentGroup
			for _, comment := range file.Comments {
				head := node.Pos()
				if declaration := heads[node]; declaration.IsValid() && positions.Position(comment.End()).Line == positions.Position(declaration).Line-1 {
					head = declaration
				}
				if comment.End() >= head || positions.Position(comment.End()).Line != positions.Position(head).Line-1 {
					continue
				}
				start := positions.Position(comment.Pos()).Offset
				lineStart := strings.LastIndexByte(string(source[:start]), '\n') + 1
				end := positions.Position(comment.End()).Offset
				// Lead comments occupy their own lines with no intervening
				// tokens. A trailing reason belongs to the preceding code.
				if strings.TrimSpace(string(source[lineStart:start])) == "" && strings.TrimSpace(string(source[end:positions.Position(head).Offset])) == "" {
					doc = comment
				}
			}
			check("function literal", node, doc)
		}
		return true
	})
	return findings, nil
}

func sourceSizeFindings(root string) ([]string, error) {
	var findings []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		found, err := sizeFindings(path, source)
		if err != nil {
			return err
		}
		findings = append(findings, found...)
		return nil
	})
	return findings, err
}

func TestSimulationSourceSizeLimits(t *testing.T) {
	root := repoRoot(t)
	for _, dir := range []string{"src/common/sim", "src/edge/simload"} {
		findings, err := sourceSizeFindings(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, finding := range findings {
			t.Error(finding)
		}
	}
}

func TestSizeGateReadsNewFiles(t *testing.T) {
	root := t.TempDir()
	findings, err := sourceSizeFindings(root)
	if err != nil || len(findings) != 0 {
		t.Fatalf("empty directory: findings = %q, error = %v", findings, err)
	}
	sub := filepath.Join(root, "new")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	source := []byte("package p\nvar callback = func() {\n" + strings.Repeat("_ = 0\n", 149) + "}\n")
	for _, name := range []string{"callback.go", "ignored_test.go"} {
		if err := os.WriteFile(filepath.Join(sub, name), source, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	findings, err = sourceSizeFindings(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || !strings.Contains(findings[0], "callback.go:2:") {
		t.Fatalf("new file: findings = %q, want one callback finding", findings)
	}
}

// TestSizeGateFiresOnFixtureSources runs the gate over sources at and just past
// each limit. The fixture sizes are written out rather than derived from the
// limits, so a limit raised or a check removed makes a fixture pass the gate.
func TestSizeGateFiresOnFixtureSources(t *testing.T) {
	body := func(n int) string { return strings.Repeat("\t_ = 0\n", n) }
	function := func(doc string, bodyLines int) string {
		// The declaration spans bodyLines plus its signature and closing brace.
		return doc + "func f() {\n" + body(bodyLines) + "}\n"
	}
	literal := func(doc string, bodyLines int) string {
		return doc + "var f = func() {\n" + body(bodyLines) + "}\n"
	}
	reason := "// f is kept as one unit because its cases share one decoder state.\n"
	nested := func(n int) string { return "package p\nvar callbacks = []func(){func() {\n" + body(n) + "}}\n" }
	local := func(n int) string {
		return "package p\n" + reason + "func outer() {\n_ = func() {\n" + body(n) + "}\n}\n"
	}
	checked := 0
	for _, tc := range []struct {
		name   string
		source string
		want   int
	}{
		{"file at the limit", "package p\nvar value = 1\n" + strings.Repeat("\n", 1198), 0},
		{"file over the limit", "package p\nvar value = 1\n" + strings.Repeat("\n", 1199), 1},
		{"function at the limit", "package p\n" + function("", 148), 0},
		{"function over the limit", "package p\n" + function("", 149), 1},
		{"function with a bare one unit", "package p\n// f is one unit.\n" + function("", 149), 1},
		{"function with a reason", "package p\n" + function(reason, 149), 0},
		{"literal at the limit", "package p\n" + literal("", 148), 0},
		{"literal over the limit", "package p\n" + literal("", 149), 1},
		{"literal with a bare one unit", "package p\n" + literal("// f is one unit.\n", 149), 1},
		{"nested literal at the limit", nested(148), 0},
		{"nested literal over the limit", nested(149), 1},
		{"local literal at the limit", local(148), 0},
		{"local literal over the limit", local(149), 1},
		{"outer reason does not cover literal", strings.Replace(local(149), reason, "", 1), 2},
		{"nested literal with a reason", "package p\nvar callbacks = []func(){\n" + reason + "func() {\n" + body(149) + "}}\n", 0},
		{"nested literal with a bare one unit", "package p\nvar callbacks = []func(){\n// one unit\nfunc() {\n" + body(149) + "}}\n", 1},
		{"unrelated reason", "package p\n" + reason + "var other = 0\n" + literal("", 149), 1},
		{"file without trailing newline", "package p\nvar value = 1\n" + strings.Repeat("\n", 1198) + "// end", 1},
		{"literal with a reason", "package p\n" + literal(reason, 149), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings, err := sizeFindings("fixture.go", []byte(tc.source))
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) != tc.want {
				t.Fatalf("findings = %q, want %d", findings, tc.want)
			}
			checked++
		})
	}
	if checked != 19 {
		t.Fatalf("concrete expectations reached %d, want 19", checked)
	}
}

func TestSizeGateLiteralHeadReasons(t *testing.T) {
	body := strings.Repeat("_ = 0\n", 149)
	literal := "func() {\n" + body + "}"
	reason := "// one unit because its cases share one decoder state.\n"
	for _, tc := range []struct {
		name, source string
		wantLine     int
	}{
		{"group reason does not cover beta", reason + "var (\nalpha = func() {}\nbeta = " + literal + "\n)\n", 5},
		{"alpha reason does not cover beta", "var (\n" + reason + "alpha = " + literal + "\nbeta = " + literal + "\n)\n", 155},
		{"trailing declaration comment", "var alpha = 0 " + reason + "var beta = " + literal + "\n", 3},
		{"trailing block comment", "var alpha = 0 /* one unit because its cases share one decoder state. */\nvar beta = " + literal + "\n", 3},
		{"trailing nested comment", "var callbacks = []func(){func() {}, " + reason + literal + "}\n", 3},
		{"shared declaration reason", reason + "var alpha, beta = func() {}, " + literal + "\n", 3},
		{"intervening sibling on head line", "var callbacks = []func(){\n" + reason + "func() {}, " + literal + "}\n", 4},
		{"grouped beta with its own reason", "var (\nalpha = func() {}\n" + reason + "beta = " + literal + "\n)\n", 0},
		{"each grouped literal has its own reason", "var (\n" + reason + "alpha = " + literal + "\n" + reason + "beta = " + literal + "\n)\n", 0},
		{"local literal with its own reason", reason + "func outer() {\n" + reason + "beta := " + literal + "\n}\n", 0},
		{"multiline declaration with its own reason", reason + "var beta =\n" + literal + "\n", 0},
		{"multiline initializer with its own reason", "var beta =\n" + reason + literal + "\n", 0},
		{"standalone block head comment", "var callbacks = []func(){\n/* one unit because its cases share one decoder state. */\n" + literal + "}\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings, err := sizeFindings("fixture.go", []byte("package p\n"+tc.source))
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantLine == 0 {
				if len(findings) != 0 {
					t.Fatalf("findings = %q, want none for justified heads", findings)
				}
				return
			}
			want := fmt.Sprintf("fixture.go:%d: function literal has 151 lines, limit 150 without a stated reason to keep one unit", tc.wantLine)
			if len(findings) != 1 || findings[0] != want {
				t.Fatalf("findings = %q, want [%q]", findings, want)
			}
		})
	}
}

func TestSizeGateRecognizesDecomposition(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		want         int
	}{
		{"before", preDecompositionBPDU, 1},
		{"after", decomposedBPDU, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings, err := sizeFindings("receive.go", []byte("package stp\n"+tc.source))
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) != tc.want {
				t.Fatalf("findings = %q, want %d", findings, tc.want)
			}
		})
	}
}

// Copied from applyBPDU in 85b403fa and its decomposed declaration in
// src/common/sim/layer/stp/receive.go. These fixtures retain the physical lines.
const preDecompositionBPDU = `func (l *Layer) applyBPDU(t *tree, p *portState, now time.Time, b bpdu.BPDU, flushes *[]layer.FlushTarget) {
	link := l.links[p.name]

	// A BPDU is internal when it names this bridge's own region: an MST BPDU
	// (ConfigID set) whose configuration identifier equals this bridge's. An
	// RST or Configuration BPDU, and an MST BPDU from a different region, are
	// external. The classification lives on the link record and changes
	// only when this call stores CIST information. boundary reads that
	// stored classification.
	internal := l.mst != nil && b.ConfigID != nil && *b.ConfigID == *l.configID

	if internal {
		// Internal information ages by hop count, re-originated one hop
		// short of what was received. A record that has already reached the
		// bound is discarded rather than stored, so a BPDU naming a regional
		// root that no longer exists stops refreshing on every hop and the
		// port's own information ages out.
		if b.RemainingHops <= 1 {
			l.recomputeAll(now, flushes)

			return
		}
	} else {
		// IEEE 802.1Q treats message age as a hop count bounded by the max age
		// the BPDU itself carries, not by this bridge's configured one: the
		// received value is the root's, and the fabric builds bridges with
		// differing timers. Information that has reached the bound is
		// discarded rather than stored, so a BPDU naming a root that no
		// longer exists stops refreshing the timer on every hop and the
		// port's own information ages out.
		if b.MessageAge+time.Second > b.MaxAge {
			l.recomputeAll(now, flushes)

			return
		}
	}

	// Built in the same shape rawVector gives the stored vector it is compared
	// against: a non-CIST tree carries its cost in internalRootPathCost, not
	// externalRootPathCost, and a vector built with the cost in the wrong slot
	// compares against a different component than the one it belongs next to.
	incoming := priorityVector{rootID: b.RootID, bridgeID: b.BridgeID, portID: b.PortID}
	switch {
	case t.id == cistID && internal:
		incoming.externalRootPathCost = b.RootPathCost
		incoming.regionalRootID = b.RegionalRootID
		incoming.internalRootPathCost = b.InternalRootPathCost
	case t.id == cistID:
		incoming.externalRootPathCost = b.RootPathCost
		incoming.regionalRootID = b.RootID
	default:
		incoming.regionalRootID = b.RootID
		incoming.internalRootPathCost = b.RootPathCost
	}

	sameSource := p.rcvInfoValid && (b.BridgeID == p.rcvBridgeID && b.PortID == p.rcvPortID)
	isSuperior := false
	if !p.rcvInfoValid {
		isSuperior = true
	} else {
		stored := rawVector(t, p, link.external)
		if compareVectors(incoming, stored) < 0 {
			isSuperior = true
		}
	}

	stored := sameSource || isSuperior
	previousMaxAge := p.rcvMaxAge
	previousForwardDelay := p.rcvForwardDelay
	if stored {
		// The classification updates only when received information is
		// stored (IEEE 802.1Q clause 13.24.10). Assigning it earlier or
		// on an inferior BPDU would rewrite how stored vectors are read.
		// It is written only for the CIST's own call.
		if t.id == cistID {
			link.external = !internal
		}
		l.recordReceivedBPDU(p, b, internal, now)
	}
	receivedMaxAge := p.rcvMaxAge
	receivedForwardDelay := p.rcvForwardDelay
	l.recordAgreement(t, p, link, incoming, b.Role(), b.Agreement())
	if stored {
		p.rcvMaxAge = previousMaxAge
		p.rcvForwardDelay = previousForwardDelay
	}

	var previousRootID bpdu.BridgeID
	var previousRootCost uint32
	var previousRootPort string
	if internal && t.id == cistID && stored {
		// CIST information is role-selected before MSTI records are judged. Keep
		// the previous root values for recompute, which requests changed
		// information after it elects the stored root.
		previousRootID = t.rootID
		previousRootCost = t.rootPathCost
		previousRootPort = t.rootPort
		l.electRoot(t, now)
		l.assignRoles(t, now)
	}

	var mstiProposals []bpdu.MSTID
	if internal {
		heldCISTVec := l.cistPortVector(p)
		if p.role == bpdu.RoleDesignated {
			heldCISTVec = designatedVector(t, p, link.external)
		}
		if t.id == cistID && stored {
			// The topology-change calls below read the root and received times
			// held before this BPDU. MSTI agreement keeps the CIST vector selected
			// above.
			t.rootID = previousRootID
			t.rootPathCost = previousRootCost
			t.rootPort = previousRootPort
		}
		mstiProposals = l.receiveMSTIs(now, p.name, b, heldCISTVec, flushes)
	}

	if l.mst != nil && link.external {
		for _, id := range l.treeOrder {
			mt := l.trees[id]
			if mt.id == cistID {
				continue
			}
			if mp, ok := mt.ports[p.name]; ok {
				mp.agreed = p.agreed
				if mp.agreed {
					mp.proposing = false
				}
			}
		}
	}

	l.propagateReceivedTC(t, p, link, b, now, flushes)
	if stored {
		p.rcvMaxAge = receivedMaxAge
		p.rcvForwardDelay = receivedForwardDelay
	}

	l.recomputeAll(now, flushes)

	answered := l.answerProposals(t, p, link, b, mstiProposals, now)
	for _, answeredTree := range answered {
		l.requestNewInfo(answeredTree, answeredTree.ports[p.name])
	}
	if len(answered) == 0 && p.role == bpdu.RoleDesignated && !b.Agreement() {
		if compareVectors(incoming, designatedVector(t, p, link.external)) > 0 {
			l.requestNewInfo(t, p)
		}
	}
}
`

const decomposedBPDU = `func (l *Layer) applyBPDU(t *tree, p *portState, now time.Time, b bpdu.BPDU, flushes *[]layer.FlushTarget) {
	link := l.links[p.name]

	// A BPDU is internal when it names this bridge's own region: an MST BPDU
	// (ConfigID set) whose configuration identifier equals this bridge's. An
	// RST or Configuration BPDU, and an MST BPDU from a different region, are
	// external. The classification lives on the link record and changes
	// only when this call stores CIST information. boundary reads that
	// stored classification.
	internal := l.mst != nil && b.ConfigID != nil && *b.ConfigID == *l.configID

	if internal {
		// Internal information ages by hop count, re-originated one hop
		// short of what was received. A record that has already reached the
		// bound is discarded rather than stored, so a BPDU naming a regional
		// root that no longer exists stops refreshing on every hop and the
		// port's own information ages out.
		if b.RemainingHops <= 1 {
			l.recomputeAll(now, flushes)

			return
		}
	} else {
		// IEEE 802.1Q treats message age as a hop count bounded by the max age
		// the BPDU itself carries, not by this bridge's configured one: the
		// received value is the root's, and the fabric builds bridges with
		// differing timers. Information that has reached the bound is
		// discarded rather than stored, so a BPDU naming a root that no
		// longer exists stops refreshing the timer on every hop and the
		// port's own information ages out.
		if b.MessageAge+time.Second > b.MaxAge {
			l.recomputeAll(now, flushes)

			return
		}
	}

	incoming := receivedVector(t, b, internal)

	sameSource := p.rcvInfoValid && (b.BridgeID == p.rcvBridgeID && b.PortID == p.rcvPortID)
	isSuperior := false
	if !p.rcvInfoValid {
		isSuperior = true
	} else {
		stored := rawVector(t, p, link.external)
		if compareVectors(incoming, stored) < 0 {
			isSuperior = true
		}
	}

	stored := sameSource || isSuperior
	previousMaxAge := p.rcvMaxAge
	previousForwardDelay := p.rcvForwardDelay
	if stored {
		// The classification updates only when received information is
		// stored (IEEE 802.1Q clause 13.24.10). Assigning it earlier or
		// on an inferior BPDU would rewrite how stored vectors are read.
		// It is written only for the CIST's own call.
		if t.id == cistID {
			link.external = !internal
		}
		l.recordReceivedBPDU(p, b, internal, now)
	}
	receivedMaxAge := p.rcvMaxAge
	receivedForwardDelay := p.rcvForwardDelay
	l.recordAgreement(t, p, link, incoming, b.Role(), b.Agreement())
	if stored {
		p.rcvMaxAge = previousMaxAge
		p.rcvForwardDelay = previousForwardDelay
	}

	var previousRootID bpdu.BridgeID
	var previousRootCost uint32
	var previousRootPort string
	if internal && t.id == cistID && stored {
		// CIST information is role-selected before MSTI records are judged. Keep
		// the previous root values for recompute, which requests changed
		// information after it elects the stored root.
		previousRootID = t.rootID
		previousRootCost = t.rootPathCost
		previousRootPort = t.rootPort
		l.electRoot(t, now)
		l.assignRoles(t, now)
	}

	var mstiProposals []bpdu.MSTID
	if internal {
		heldCISTVec := l.cistPortVector(p)
		if p.role == bpdu.RoleDesignated {
			heldCISTVec = designatedVector(t, p, link.external)
		}
		if t.id == cistID && stored {
			// The topology-change calls below read the root and received times
			// held before this BPDU. MSTI agreement keeps the CIST vector selected
			// above.
			t.rootID = previousRootID
			t.rootPathCost = previousRootCost
			t.rootPort = previousRootPort
		}
		mstiProposals = l.receiveMSTIs(now, p.name, b, heldCISTVec, flushes)
	}

	if l.mst != nil && link.external {
		for _, id := range l.treeOrder {
			mt := l.trees[id]
			if mt.id == cistID {
				continue
			}
			if mp, ok := mt.ports[p.name]; ok {
				mp.agreed = p.agreed
				if mp.agreed {
					mp.proposing = false
				}
			}
		}
	}

	l.propagateReceivedTC(t, p, link, b, now, flushes)
	if stored {
		p.rcvMaxAge = receivedMaxAge
		p.rcvForwardDelay = receivedForwardDelay
	}

	l.recomputeAll(now, flushes)

	answered := l.answerProposals(t, p, link, b, mstiProposals, now)
	for _, answeredTree := range answered {
		l.requestNewInfo(answeredTree, answeredTree.ports[p.name])
	}
	if len(answered) == 0 && p.role == bpdu.RoleDesignated && !b.Agreement() {
		if compareVectors(incoming, designatedVector(t, p, link.external)) > 0 {
			l.requestNewInfo(t, p)
		}
	}
}
`
