package simtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

const simImport = "go.aledante.io/FlowSeer/src/common/sim/"

type receiveAudit struct {
	functions []*ssa.Function
	fset      *token.FileSet
}

type receiveObligation struct {
	call *ssa.Call
	axis string
}

type auditBindings map[ssa.Value]ssa.Value

func loadReceiveAudit(t *testing.T) (*receiveAudit, *packages.Package) {
	t.Helper()
	pkgs, err := packages.Load(&packages.Config{Mode: packages.LoadSyntax}, simImport+"internal/simtest")
	if err != nil || len(pkgs) != 1 {
		t.Fatalf("load corpus: %v, packages = %d", err, len(pkgs))
	}
	p := pkgs[0]
	if len(p.Errors) != 0 {
		t.Fatalf("load corpus: %v", p.Errors)
	}
	prog, built := ssautil.Packages(pkgs, ssa.InstantiateGenerics)
	prog.Build()
	return newReceiveAudit(built[0]), p
}

func newReceiveAudit(pkg *ssa.Package) *receiveAudit {
	a := &receiveAudit{fset: pkg.Prog.Fset}
	var add func(*ssa.Function)
	add = func(fn *ssa.Function) {
		a.functions = append(a.functions, fn)
		for _, child := range fn.AnonFuncs {
			add(child)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(pkg.Members)) {
		switch member := pkg.Members[name].(type) {
		case *ssa.Function:
			add(member)
		case *ssa.Type:
			if named, ok := member.Type().(*types.Named); ok {
				for i := 0; i < named.NumMethods(); i++ {
					add(pkg.Prog.FuncValue(named.Method(i)))
				}
			}
		}
	}
	return a
}

func receiveCallObject(call *ssa.Call) *types.Func {
	fn := call.Common().StaticCallee()
	if fn != nil {
		obj, _ := fn.Object().(*types.Func)
		return obj
	}
	return call.Common().Method
}

func receiveCallAxes(call *ssa.Call) []string {
	obj := receiveCallObject(call)
	if obj == nil || obj.Pkg() == nil {
		return nil
	}
	path, name := obj.Pkg().Path(), obj.Name()
	switch {
	case (path == simImport+"device" || strings.HasPrefix(path, simImport+"device/")) && name == "Receive":
		return []string{"Result"}
	case path == simImport+"device/vswitch" && name == "Compare":
		return []string{"Current.Result", "Expected.Result"}
	case path == simImport+"fabric" && name == "Compare":
		return []string{"Current", "Expected"}
	case path == simImport+"fabric" && (name == "Run" || name == "RunScenario"):
		return []string{"report"}
	default:
		return nil
	}
}

func (a *receiveAudit) check() (map[string]int, []string) {
	inventory := make(map[string]int)
	var failures []string
	for _, fn := range a.functions {
		for _, block := range fn.Blocks {
			for i, instr := range block.Instrs {
				call, ok := instr.(*ssa.Call)
				if !ok {
					continue
				}
				axes := receiveCallAxes(call)
				if len(axes) == 0 {
					continue
				}
				parent := fn
				for parent.Parent() != nil {
					parent = parent.Parent()
				}
				key := filepath.Base(a.fset.Position(call.Pos()).Filename) + ":" + parent.Name() + ":" + receiveCallObject(call).Name()
				inventory[key]++
				for _, axis := range axes {
					obligation := receiveObligation{call: call, axis: axis}
					if !a.covered(block, i+1, obligation, nil, make(map[string]bool), 0) {
						failures = append(failures, fmt.Sprintf("%s: %s lacks %s validation before return", a.fset.Position(call.Pos()), parent.Name(), axis))
					}
				}
			}
		}
	}
	return inventory, failures
}

func auditFieldName(value ssa.Value, index int) string {
	typ := value.Type()
	if ptr, ok := typ.Underlying().(*types.Pointer); ok {
		typ = ptr.Elem()
	}
	return typ.Underlying().(*types.Struct).Field(index).Name()
}

func bindAuditCall(call *ssa.Call, bindings auditBindings) auditBindings {
	next := maps.Clone(bindings)
	if next == nil {
		next = make(auditBindings)
	}
	fn := call.Common().StaticCallee()
	for i, param := range fn.Params {
		next[param] = call.Common().Args[i]
	}
	return next
}

func (a *receiveAudit) matches(value ssa.Value, target ssa.Value, axis string, bindings auditBindings, seen map[ssa.Value]bool) bool {
	if value == target {
		return axis == ""
	}
	if value == nil || seen[value] {
		return false
	}
	seen = maps.Clone(seen)
	seen[value] = true
	if bound := bindings[value]; bound != nil {
		return a.matches(bound, target, axis, bindings, seen)
	}
	switch v := value.(type) {
	case *ssa.Field:
		return a.matchField(v.X, auditFieldName(v.X, v.Field), target, axis, bindings, seen)
	case *ssa.FieldAddr:
		return a.matchField(v.X, auditFieldName(v.X, v.Field), target, axis, bindings, seen)
	case *ssa.UnOp:
		return a.matchesLoad(v.X, v, target, axis, bindings, seen)
	case *ssa.ChangeType:
		return a.matches(v.X, target, axis, bindings, seen)
	case *ssa.Phi:
		for _, edge := range v.Edges {
			if a.matches(edge, target, axis, bindings, seen) {
				return true
			}
		}
	case *ssa.Alloc:
		for _, ref := range *v.Referrers() {
			if store, ok := ref.(*ssa.Store); ok && store.Addr == v && a.matches(store.Val, target, axis, bindings, seen) {
				return true
			}
		}
	case *ssa.Call:
		return a.matchHelperReturn(v, 0, target, axis, bindings, seen)
	case *ssa.Extract:
		if call, ok := v.Tuple.(*ssa.Call); ok {
			if call == target && v.Index == 0 {
				return axis == ""
			}
			return a.matchHelperReturn(call, v.Index, target, axis, bindings, seen)
		}
	}
	return false
}

func auditReachingStores(alloc *ssa.Alloc, block *ssa.BasicBlock, before int, seen map[string]bool) []ssa.Value {
	key := fmt.Sprintf("%p:%d", block, before)
	if seen[key] {
		return nil
	}
	seen[key] = true
	for i := before - 1; i >= 0; i-- {
		if store, ok := block.Instrs[i].(*ssa.Store); ok && store.Addr == alloc {
			return []ssa.Value{store.Val}
		}
	}
	var values []ssa.Value
	for _, pred := range block.Preds {
		values = append(values, auditReachingStores(alloc, pred, len(pred.Instrs), seen)...)
	}
	return values
}

func (a *receiveAudit) matchesLoad(address ssa.Value, read *ssa.UnOp, target ssa.Value, axis string, bindings auditBindings, seen map[ssa.Value]bool) bool {
	switch addr := address.(type) {
	case *ssa.FieldAddr:
		field := auditFieldName(addr.X, addr.Field)
		if axis == field {
			return a.matchesLoad(addr.X, read, target, "", bindings, seen)
		}
		if prefix, ok := strings.CutSuffix(axis, "."+field); ok {
			return a.matchesLoad(addr.X, read, target, prefix, bindings, seen)
		}
		return false
	case *ssa.Alloc:
		if bound := bindings[addr]; bound != nil {
			return a.matches(bound, target, axis, bindings, seen)
		}
		index := slices.Index(read.Block().Instrs, ssa.Instruction(read))
		for _, value := range auditReachingStores(addr, read.Block(), index, make(map[string]bool)) {
			if a.matches(value, target, axis, bindings, seen) {
				return true
			}
		}
		return false
	default:
		return a.matches(address, target, axis, bindings, seen)
	}
}

func (a *receiveAudit) matchField(value ssa.Value, field string, target ssa.Value, axis string, bindings auditBindings, seen map[ssa.Value]bool) bool {
	if axis == field {
		return a.matches(value, target, "", bindings, seen)
	}
	if prefix, ok := strings.CutSuffix(axis, "."+field); ok {
		return a.matches(value, target, prefix, bindings, seen)
	}
	return false
}

func (a *receiveAudit) matchHelperReturn(call *ssa.Call, index int, target ssa.Value, axis string, bindings auditBindings, seen map[ssa.Value]bool) bool {
	fn := call.Common().StaticCallee()
	if fn == nil || len(fn.Blocks) == 0 {
		return false
	}
	for _, block := range fn.Blocks {
		if ret, ok := block.Instrs[len(block.Instrs)-1].(*ssa.Return); ok && index < len(ret.Results) {
			if a.matches(ret.Results[index], target, axis, bindAuditCall(call, bindings), seen) {
				return true
			}
		}
	}
	return false
}

func (a *receiveAudit) validates(call *ssa.Call, obligation receiveObligation, bindings auditBindings, depth int) bool {
	fn := call.Common().StaticCallee()
	if fn == nil || depth > 12 {
		return false
	}
	if fn.Name() == "validateReceiveResult" || fn.Name() == "validateReceiveReport" {
		value := call.Common().Args[0]
		if obligation.axis == "report" {
			report, ok := value.(*ssa.Call)
			if !ok || report.Common().StaticCallee() == nil || report.Common().StaticCallee().Name() != "Report" {
				return false
			}
			return a.matches(report.Common().Args[0], obligation.call.Common().Args[0], "", bindings, make(map[ssa.Value]bool))
		}
		return a.matches(value, obligation.call, obligation.axis, bindings, make(map[ssa.Value]bool))
	}
	if len(fn.Blocks) != 0 && fn.Pkg == obligation.call.Parent().Pkg && a.helperHasValidator(fn, make(map[*ssa.Function]bool)) {
		return a.covered(fn.Blocks[0], 0, obligation, bindAuditCall(call, bindings), make(map[string]bool), depth+1)
	}
	return false
}

func (a *receiveAudit) helperHasValidator(fn *ssa.Function, seen map[*ssa.Function]bool) bool {
	if seen[fn] {
		return false
	}
	seen[fn] = true
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			call, ok := instr.(*ssa.Call)
			if !ok {
				continue
			}
			callee := call.Common().StaticCallee()
			if callee == nil {
				continue
			}
			if callee.Name() == "validateReceiveResult" || callee.Name() == "validateReceiveReport" || (callee.Pkg == fn.Pkg && a.helperHasValidator(callee, seen)) {
				return true
			}
		}
	}
	return false
}

func (a *receiveAudit) validatorError(value ssa.Value, obligation receiveObligation, bindings auditBindings) bool {
	if call, ok := value.(*ssa.Call); ok {
		fn := call.Common().StaticCallee()
		if fn == nil || (fn.Name() != "validateReceiveResult" && fn.Name() != "validateReceiveReport") {
			return false
		}
		for _, axis := range receiveCallAxes(obligation.call) {
			if a.validates(call, receiveObligation{call: obligation.call, axis: axis}, bindings, 0) {
				return true
			}
		}
	}
	return false
}

func auditErrorGuard(ret *ssa.Return, value ssa.Value) bool {
	for _, block := range ret.Parent().Blocks {
		branch, ok := block.Instrs[len(block.Instrs)-1].(*ssa.If)
		if !ok {
			continue
		}
		cond, ok := branch.Cond.(*ssa.BinOp)
		if !ok || cond.X != value {
			continue
		}
		nilValue, ok := cond.Y.(*ssa.Const)
		if !ok || nilValue.Value != nil {
			continue
		}
		if cond.Op == token.NEQ && block.Succs[0].Dominates(ret.Block()) ||
			cond.Op == token.EQL && block.Succs[1].Dominates(ret.Block()) {
			return true
		}
	}
	return false
}

func (a *receiveAudit) handlesValidationError(call *ssa.Call) bool {
	if !types.Identical(call.Type(), types.Universe.Lookup("error").Type()) {
		return false
	}
	start := slices.Index(call.Block().Instrs, ssa.Instruction(call)) + 1
	return a.returnsValidationError(call.Block(), start, call, nil, make(map[*ssa.BasicBlock]bool))
}

func (a *receiveAudit) returnsValidationError(block *ssa.BasicBlock, start int, value ssa.Value, bindings auditBindings, seen map[*ssa.BasicBlock]bool) bool {
	if seen[block] {
		return false
	}
	seen = maps.Clone(seen)
	seen[block] = true
	bindings = maps.Clone(bindings)
	if bindings == nil {
		bindings = make(auditBindings)
	}
	matches := func(other ssa.Value) bool {
		return a.matches(other, value, "", bindings, make(map[ssa.Value]bool))
	}
	nonNilSuccessor := -1
	for _, instr := range block.Instrs[start:] {
		switch instr := instr.(type) {
		case *ssa.Store:
			bindings[instr.Addr] = instr.Val
		case *ssa.Return:
			if len(instr.Results) == 0 {
				return false
			}
			err := instr.Results[len(instr.Results)-1]
			if matches(err) {
				return true
			}
			// An earlier validation failure can take precedence over this error.
			other, ok := err.(*ssa.Call)
			if !ok || !auditErrorGuard(instr, other) {
				return false
			}
			fn := other.Common().StaticCallee()
			return fn != nil && (fn.Name() == "validateReceiveResult" || fn.Name() == "validateReceiveReport")
		case *ssa.If:
			cond, ok := instr.Cond.(*ssa.BinOp)
			if !ok || (cond.Op != token.EQL && cond.Op != token.NEQ) {
				continue
			}
			x, y := cond.X, cond.Y
			if !matches(x) {
				x, y = y, x
			}
			nilValue, ok := y.(*ssa.Const)
			if !matches(x) || !ok || nilValue.Value != nil {
				continue
			}
			nonNilSuccessor = 0
			if cond.Op == token.EQL {
				nonNilSuccessor = 1
			}
		}
	}
	for i, succ := range block.Succs {
		if nonNilSuccessor >= 0 && i != nonNilSuccessor {
			continue
		}
		next := maps.Clone(bindings)
		for _, instr := range succ.Instrs {
			phi, ok := instr.(*ssa.Phi)
			if !ok {
				break
			}
			next[phi] = phi.Edges[slices.Index(succ.Preds, block)]
		}
		if !a.returnsValidationError(succ, 0, value, next, seen) {
			return false
		}
	}
	return len(block.Succs) > 0
}

func (a *receiveAudit) covered(block *ssa.BasicBlock, start int, obligation receiveObligation, bindings auditBindings, seen map[string]bool, depth int) bool {
	if depth > 12 {
		return false
	}
	key := fmt.Sprintf("%p:%d", block, start)
	if seen[key] {
		return false
	}
	seen = maps.Clone(seen)
	seen[key] = true
	bindings = maps.Clone(bindings)
	if bindings == nil {
		bindings = make(auditBindings)
	}
	for _, instr := range block.Instrs[start:] {
		if store, ok := instr.(*ssa.Store); ok {
			bindings[store.Addr] = store.Val
		}
		if call, ok := instr.(*ssa.Call); ok && a.validates(call, obligation, bindings, depth) && a.handlesValidationError(call) {
			return true
		}
		if ret, ok := instr.(*ssa.Return); ok {
			if len(ret.Results) > 0 {
				err := ret.Results[len(ret.Results)-1]
				if auditErrorGuard(ret, err) && a.validatorError(err, obligation, bindings) {
					return true
				}
			}
			return a.delegatedReturn(ret, obligation, bindings, depth)
		}
	}
	for _, succ := range block.Succs {
		next := maps.Clone(bindings)
		for _, instr := range succ.Instrs {
			phi, ok := instr.(*ssa.Phi)
			if !ok {
				break
			}
			next[phi] = phi.Edges[slices.Index(succ.Preds, block)]
		}
		if !a.covered(succ, 0, obligation, next, seen, depth) {
			return false
		}
	}
	return true
}

func (a *receiveAudit) delegatedReturn(ret *ssa.Return, obligation receiveObligation, bindings auditBindings, depth int) bool {
	if obligation.axis == "report" {
		return false
	}
	returned := false
	for _, result := range ret.Results {
		axis := obligation.axis
		for {
			if a.matches(result, obligation.call, axis, bindings, make(map[ssa.Value]bool)) {
				returned = true
			}
			if axis == "" {
				break
			}
			if i := strings.LastIndex(axis, "."); i >= 0 {
				axis = axis[:i]
			} else {
				axis = ""
			}
		}
	}
	if !returned {
		return false
	}
	callers := 0
	for _, fn := range a.functions {
		for _, block := range fn.Blocks {
			for i, instr := range block.Instrs {
				call, ok := instr.(*ssa.Call)
				if ok && call.Common().StaticCallee() == ret.Parent() {
					callers++
					if !a.covered(block, i+1, obligation, bindings, make(map[string]bool), depth+1) {
						return false
					}
				}
			}
		}
	}
	return callers > 0
}

func TestCorpusReceiveValidationSources(t *testing.T) {
	a, _ := loadReceiveAudit(t)
	inventory, failures := a.check()
	for _, failure := range failures {
		t.Error(failure)
	}
	want := map[string]int{
		"baseline_cases.go:casePlanningCandidateForkDiverges:Run":                       3,
		"baseline_cases.go:caseTroubleshootingUnicastForwarding:Receive":                1,
		"baseline_cases.go:executePlanningPortVLANChange:Compare":                       1,
		"baseline_cases.go:executeShadowingPartialUnknownPort:Receive":                  1,
		"comparison_cases.go:caseComparisonFabricBudgetExhausted:Compare":               2,
		"comparison_cases.go:caseComparisonFabricCableCutDifferent:Compare":             2,
		"comparison_cases.go:caseComparisonFabricEquivalentComplete:Compare":            2,
		"comparison_cases.go:caseComparisonFabricMediumPathDifferent:Compare":           2,
		"comparison_cases.go:caseComparisonSwitchEquivalentComplete:Compare":            2,
		"comparison_cases.go:caseComparisonSwitchPortDownDifferent:Compare":             2,
		"comparison_cases.go:caseComparisonSwitchUnknownStatus:Compare":                 2,
		"filter_cases.go:caseTroubleshootingFilterDropsMDNSUnicastProbe:Receive":        1,
		"filter_cases.go:executeFilterRuleChange:Compare":                               1,
		"filter_cases.go:executeStatefulReplyAllowed:Receive":                           1,
		"lag_multicast_cases.go:casePlanningLAGMemberFaultKeepsSurvivingFlows:Receive":  4,
		"lag_multicast_cases.go:caseTroubleshootingActiveBackupNoFailback:Receive":      3,
		"lag_multicast_cases.go:caseTroubleshootingSSMRejectsUnjoinedSource:Receive":    3,
		"lag_multicast_cases.go:executeTroubleshootingLeaveLastMemberQuery:Receive":     5,
		"load_cases.go:executeLoadCase:Run":                                             1,
		"loopprotect_cases.go:caseTroubleshootingLoopProtectContainsAccessLoop:Run":     3,
		"loopprotect_cases.go:loopProtectCaseIngressBlocked:Run":                        1,
		"mcast_cases.go:mdnsCaseExecute:Receive":                                        1,
		"mst_load_cases.go:caseShadowingMSTModel:Receive":                               1,
		"reflector_cases.go:caseTroubleshootingMDNSReflectedAcrossVLANs:Run":            1,
		"reflector_cases.go:caseTroubleshootingMDNSTwoReflectorsLoop:Run":               1,
		"routing_cases.go:casePlanningECMPCandidatesRecorded:Receive":                   1,
		"routing_cases.go:caseTroubleshootingRecursiveRouteNotInstalled:Receive":        1,
		"routing_cases.go:executeTroubleshootingNeighborResolutionPending:Receive":      2,
		"routing_cases.go:executeTroubleshootingNeighborResolutionPending:Run":          1,
		"scenario_cases.go:executePeriodicExhaustion:Run":                               1,
		"scenario_cases.go:executeScenarioLinkFlap:RunScenario":                         2,
		"stp_cases.go:caseTroubleshootingBPDUGuardDisablesEdge:Receive":                 2,
		"stp_cases.go:caseTroubleshootingLoopGuardUnidirectionalLink:Receive":           2,
		"stp_cases.go:caseTroubleshootingStaleRootAgesOut:Receive":                      3,
		"stp_cases.go:stpMSTInjectFrameTo:Run":                                          2,
		"topology_cases.go:caseTopologyShadowingUncabledPortDefiniteDrop:Run":           1,
		"topology_cases.go:caseTopologyShadowingUnreportedNegotiation:Run":              1,
		"topology_cases.go:caseTopologyShadowingUnresolvedTransceiver:Run":              1,
		"topology_cases.go:caseTopologyShadowingUnresolvedTransceiverKnownDelivery:Run": 1,
		"topology_cases.go:caseTroubleshootingHostRejectsForeignUnicast:Run":            1,
		"topology_stp_cases.go:executeTopologyShadowingUnknownUplinkSTP:Run":            2,
	}
	if !reflect.DeepEqual(inventory, want) {
		t.Errorf("executor call inventory = %v, want %v", inventory, want)
	}
}

type auditImporter map[string]*types.Package

func (i auditImporter) Import(path string) (*types.Package, error) {
	if pkg := i[path]; pkg != nil {
		return pkg, nil
	}
	return nil, fmt.Errorf("fixture import absent: %s", path)
}

func syntheticReceiveAudit(t *testing.T, imports auditImporter, body string) *receiveAudit {
	t.Helper()
	fset := token.NewFileSet()
	source := `package fixture
import (
 "time"
 "go.aledante.io/FlowSeer/src/common/net/ethernet"
 "go.aledante.io/FlowSeer/src/common/sim/device"
 "go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
 "go.aledante.io/FlowSeer/src/common/sim/fabric"
)
func validateReceiveResult(device.Result) error { return nil }
func validateReceiveReport([]fabric.Journey) error { return nil }
var now time.Time
var frame ethernet.Frame
var _ *vswitch.Switch
` + body
	file, err := parser.ParseFile(fset, "executor.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, _, err := ssautil.BuildPackage(&types.Config{Importer: imports}, fset, types.NewPackage("fixture", "fixture"), []*ast.File{file}, ssa.InstantiateGenerics)
	if err != nil {
		t.Fatal(err)
	}
	return newReceiveAudit(pkg)
}

func TestReceiveValidationAuditFixtures(t *testing.T) {
	_, p := loadReceiveAudit(t)
	imports := make(auditImporter)
	packages.Visit([]*packages.Package{p}, func(pkg *packages.Package) bool {
		imports[pkg.PkgPath] = pkg.Types
		return true
	}, nil)
	fixtures := []struct {
		name string
		body string
		want int
	}{
		{"receive validated", `func execute(sw *vswitch.Switch) error { out := sw.Receive(now, "in", frame); return validateReceiveResult(out.Result) }`, 0},
		{"receive error ignored", `func execute(sw *vswitch.Switch) error { out := sw.Receive(now, "in", frame); _ = validateReceiveResult(out.Result); return nil }`, 1},
		{"report error ignored", `func execute(fab *fabric.Fabric) error { fab.Run(fabric.RunOptions{}); _ = validateReceiveReport(fab.Report()); return nil }`, 1},
		{"delegated error ignored", `func validate(out device.Result) error { return validateReceiveResult(out) }; func execute(sw *vswitch.Switch) error { out := sw.Receive(now, "in", frame); _ = validate(out.Result); return nil }`, 1},
		{"helper ignores error", `func validate(out device.Result) error { _ = validateReceiveResult(out); return nil }; func execute(sw *vswitch.Switch) error { out := sw.Receive(now, "in", frame); return validate(out.Result) }`, 1},
		{"delegated report error ignored", `func validate(fab *fabric.Fabric) error { return validateReceiveReport(fab.Report()) }; func execute(fab *fabric.Fabric) error { fab.Run(fabric.RunOptions{}); _ = validate(fab); return nil }`, 1},
		{"report helper ignores error", `func validate(fab *fabric.Fabric) error { _ = validateReceiveReport(fab.Report()); return nil }; func execute(fab *fabric.Fabric) error { fab.Run(fabric.RunOptions{}); return validate(fab) }`, 1},
		{"checked error discarded", `func execute(sw *vswitch.Switch) error { out := sw.Receive(now, "in", frame); if err := validateReceiveResult(out.Result); err != nil { return nil }; return nil }`, 1},
		{"error branch bypasses return", `func execute(sw *vswitch.Switch, stop bool) error { out := sw.Receive(now, "in", frame); err := validateReceiveResult(out.Result); if stop { return nil }; return err }`, 1},
		{"error alias returned", `func execute(sw *vswitch.Switch) error { out := sw.Receive(now, "in", frame); err := validateReceiveResult(out.Result); alias := err; return alias }`, 0},
		{"checked report error", `func execute(fab *fabric.Fabric) error { fab.Run(fabric.RunOptions{}); if err := validateReceiveReport(fab.Report()); err != nil { return err }; return nil }`, 0},
		{"equal nil guard", `func execute(sw *vswitch.Switch) error { out := sw.Receive(now, "in", frame); err := validateReceiveResult(out.Result); if err == nil { return nil }; return err }`, 0},
		{"error overwritten", `func execute(sw *vswitch.Switch) error { out := sw.Receive(now, "in", frame); err := validateReceiveResult(out.Result); err = nil; return err }`, 1},
		{"error branch overwritten", `func execute(sw *vswitch.Switch, stop bool) error { out := sw.Receive(now, "in", frame); err := validateReceiveResult(out.Result); if stop { err = nil }; return err }`, 1},
		{"error branch retained", `func execute(sw *vswitch.Switch, stop bool) error { out := sw.Receive(now, "in", frame); err := validateReceiveResult(out.Result); alias := err; if stop { alias = err }; return alias }`, 0},
		{"receive omitted", `func execute(sw *vswitch.Switch) error { _ = sw.Receive(now, "in", frame); return nil }`, 1},
		{"report validated", `func execute(fab *fabric.Fabric) error { fab.Run(fabric.RunOptions{}); return validateReceiveReport(fab.Report()) }`, 0},
		{"report omitted", `func execute(fab *fabric.Fabric) error { fab.Run(fabric.RunOptions{}); return nil }`, 1},
		{"early return", `func execute(sw *vswitch.Switch, stop bool) error { out := sw.Receive(now, "in", frame); if stop { return nil }; return validateReceiveResult(out.Result) }`, 1},
		{"alias validated", `func execute(sw *vswitch.Switch) error { out := sw.Receive(now, "in", frame); alias := out.Result; return validateReceiveResult(alias) }`, 0},
		{"alias omitted", `func execute(sw *vswitch.Switch) error { out := sw.Receive(now, "in", frame); _ = out.Result; return validateReceiveResult(device.Result{}) }`, 1},
		{"helper return validated", `func receive(sw *vswitch.Switch) device.Output { return sw.Receive(now, "in", frame) }; func execute(sw *vswitch.Switch) error { out := receive(sw); return validateReceiveResult(out.Result) }`, 0},
		{"helper return omitted", `func receive(sw *vswitch.Switch) device.Output { return sw.Receive(now, "in", frame) }; func execute(sw *vswitch.Switch) error { _ = receive(sw); return nil }`, 1},
		{"delegated validator", `func validate(out device.Result) error { return validateReceiveResult(out) }; func execute(sw *vswitch.Switch) error { out := sw.Receive(now, "in", frame); return validate(out.Result) }`, 0},
		{"nested run", `func execute(fab *fabric.Fabric) error { _ = fab.Run(fabric.RunOptions{}).Steps; return validateReceiveReport(fab.Report()) }`, 0},
		{"nested run omitted", `func execute(fab *fabric.Fabric) error { _ = fab.Run(fabric.RunOptions{}).Steps; return nil }`, 1},
		{"unrelated names", `type unrelated struct{}; func (unrelated) Run() {}; func (unrelated) Receive() {}; func execute() error { u := unrelated{}; u.Run(); u.Receive(); return nil }`, 0},
		{"comparison validated", `func execute(a, b *vswitch.Switch) error { cmp := vswitch.Compare(a, b, now, "in", frame); if err := validateReceiveResult(cmp.Current.Result); err != nil { return err }; return validateReceiveResult(cmp.Expected.Result) }`, 0},
		{"comparison expected omitted", `func execute(a, b *vswitch.Switch) error { cmp := vswitch.Compare(a, b, now, "in", frame); if err := validateReceiveResult(cmp.Current.Result); err != nil { return err }; return nil }`, 1},
		{"report wrong fabric", `func execute(a, b *fabric.Fabric) error { a.Run(fabric.RunOptions{}); return validateReceiveReport(b.Report()) }`, 1},
		{"report early error", `func execute(fab *fabric.Fabric) error { run := fab.Run(fabric.RunOptions{}); if run.Err != nil { return run.Err }; return validateReceiveReport(fab.Report()) }`, 1},
		{"interface receive", `func execute(sw device.Device) error { out := sw.Receive(now, "in", frame); return validateReceiveResult(out.Result) }`, 0},
		{"interface omitted", `func execute(sw device.Device) error { _ = sw.Receive(now, "in", frame); return nil }`, 1},
		{"method executor", `type executor struct { sw *vswitch.Switch }; func (e executor) execute() error { _ = e.sw.Receive(now, "in", frame); return nil }`, 1},
		{"report alias", `func execute(fab *fabric.Fabric) error { alias := fab; alias.Run(fabric.RunOptions{}); report := fab.Report(); return validateReceiveReport(report) }`, 0},
		{"delegated report", `func validate(fab *fabric.Fabric) error { return validateReceiveReport(fab.Report()) }; func execute(fab *fabric.Fabric) error { fab.Run(fabric.RunOptions{}); return validate(fab) }`, 0},
		{"report early return", `func execute(fab *fabric.Fabric, stop bool) error { fab.Run(fabric.RunOptions{}); if stop { return nil }; return validateReceiveReport(fab.Report()) }`, 1},
		{"fabric comparison validated", `func execute(a, b *fabric.Fabric) error { cmp := fabric.Compare(a, b, fabric.Scenario{}, fabric.RunOptions{}); if err := validateReceiveReport(cmp.Current); err != nil { return err }; if err := validateReceiveReport(cmp.Expected); err != nil { return err }; return cmp.Err }`, 0},
		{"fabric comparison early error", `func execute(a, b *fabric.Fabric) error { cmp := fabric.Compare(a, b, fabric.Scenario{}, fabric.RunOptions{}); if cmp.Err != nil { return cmp.Err }; if err := validateReceiveReport(cmp.Current); err != nil { return err }; return validateReceiveReport(cmp.Expected) }`, 2},
		{"fabric comparison omitted", `func execute(a, b *fabric.Fabric) error { cmp := fabric.Compare(a, b, fabric.Scenario{}, fabric.RunOptions{}); return validateReceiveReport(cmp.Current) }`, 1},
		{"helper has early return", `func validate(out device.Result, stop bool) error { if stop { return nil }; return validateReceiveResult(out) }; func execute(sw *vswitch.Switch, stop bool) error { out := sw.Receive(now, "in", frame); return validate(out.Result, stop) }`, 1},
		{"two receives aliased", `func execute(sw *vswitch.Switch) error { first := sw.Receive(now, "in", frame); alias := first.Result; second := sw.Receive(now, "in", frame); err := validateReceiveResult(alias); other := validateReceiveResult(second.Result); if err != nil { return err }; return other }`, 0},
		{"overwritten receive", `func execute(sw *vswitch.Switch) error { out := sw.Receive(now, "in", frame); out = sw.Receive(now, "in", frame); return validateReceiveResult(out.Result) }`, 1},
		{"alias branch loses result", `func execute(sw *vswitch.Switch, stop bool) error { out := sw.Receive(now, "in", frame); alias := out.Result; if stop { alias = device.Result{} }; return validateReceiveResult(alias) }`, 1},
		{"alias branch retains result", `func execute(sw *vswitch.Switch, stop bool) error { out := sw.Receive(now, "in", frame); alias := out.Result; if stop { alias = out.Result }; return validateReceiveResult(alias) }`, 0},
	}
	checked := 0
	for _, tc := range fixtures {
		if t.Run(tc.name, func(t *testing.T) {
			_, failures := syntheticReceiveAudit(t, imports, tc.body).check()
			if len(failures) != tc.want {
				t.Fatalf("failures = %v, want %d", failures, tc.want)
			}
		}) {
			checked++
		}
	}
	if checked != 45 {
		t.Errorf("fixtures reaching expectations = %d, want 45", checked)
	}
}
