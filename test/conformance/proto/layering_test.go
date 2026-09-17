package conformance

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// importOrder declares, for every schema-bearing package under the roots in
// orderedRoots, the packages it may import. A package imports itself freely;
// anything else it imports must be listed here. Adding a package to the tree
// is one line in this table — leaving it out fails
// TestImportOrderCoversEveryPackage rather than silently escaping the order.
//
// A package that declares a service is a sink: importing one is always
// rejected, so it never appears as a value in this table. layeringViolation
// enforces that independently of any row here, and TestModelDeclaresNoService
// keeps every package under model/ from declaring one.
var importOrder = map[string][]string{
	"net/addr":   nil,
	"net/packet": nil,
	"net/phy":    nil,

	"net/switching": {"net/addr", "net/packet"},
	"net/ip":        {"net/addr"},
	"net/capture":   {"net/addr", "net/packet", "net/switching"},

	"net/interface": {"net/addr", "net/packet", "net/phy", "net/switching", "net/ip"},

	// A protocol may import any layer below it, and never another protocol.
	"net/protocol/lldp": {"net/addr", "net/packet", "net/phy", "net/switching", "net/ip", "net/interface"},
	"net/protocol/lacp": {"net/addr", "net/packet", "net/phy", "net/switching", "net/ip", "net/interface"},
	"net/protocol/stp":  {"net/addr", "net/packet", "net/phy", "net/switching", "net/ip", "net/interface"},

	"api/edge":         {"model/edge", "model/policy", "model/credential", "net/addr"},
	"model/edge":       nil,
	"model/credential": nil,
	"model/policy":     nil,

	// The error wire payload, a leaf like model/policy: it imports nothing,
	// and every boundary that carries an error imports it.
	"errs": nil,

	// A capture session's identity, lifecycle, and the chunk frames its two
	// services share. It takes the owning ref and the assertion its upload
	// stream re-verifies from model/edge, and holds net/capture's counters,
	// link type and packet records rather than copies of their fields.
	"model/capture": {"model/edge", "net/capture"},

	// The two Connect services around a capture session: the one an operator
	// calls to create, control, and read one back, and the one an edge calls
	// to upload one. Neither adds an import model/capture does not already
	// carry.
	"api/capture": {"model/capture", "model/edge", "net/capture"},

	"model/inventory": {"model/edge", "model/policy", "net/addr", "net/packet", "net/phy", "net/switching", "net/ip", "net/interface", "net/protocol/lldp"},

	// The operation values every device-access boundary shares. They reach
	// model/edge for the responsible edge, so a boundary that imports them
	// reaches model/edge only through here.
	"model/access": {"model/edge", "model/inventory", "model/policy", "net/addr", "net/packet", "net/phy", "net/switching", "net/ip", "net/interface", "net/protocol/lldp"},

	// The operator API, the execution envelope, and the audit event are
	// sibling boundary consumers of model/access, and none of the three
	// imports another. This row is an allowlist and is wider than the tree:
	// api/device's files reach model/access and model/inventory, while
	// model/policy and errs are permitted and unused. The envelope carries no
	// device or edge ref at all (the transport already names both), and the
	// audit event needs model/inventory directly because it is read outside
	// any live transport context.
	"api/device": {"model/inventory", "model/access", "model/policy", "errs", "net/addr", "net/packet", "net/phy", "net/switching", "net/ip", "net/interface", "net/protocol/lldp"},

	"integration/device": {"model/access", "errs"},

	"event/device": {"model/inventory", "model/access", "errs"},

	// The device service's own files: the records it writes to its stores and
	// the operator-written prototext it reads at start. One process owns both,
	// so this root sits above every boundary it embeds and is imported by
	// none.
	"store/device": {"model/edge", "model/inventory", "model/access", "model/credential", "model/policy", "errs", "net/addr", "net/packet", "net/phy", "net/switching", "net/ip", "net/interface", "net/protocol/lldp"},

	// The agent's own deployment file. It imports nothing FlowSeer-owned and
	// is imported by nothing: what an edge is told about central lives in
	// model/edge's EdgeProvisioning, which this package names by path rather
	// than by type, so the dependency an entry here would suggest does not
	// exist.
	"store/edge": nil,
}

// orderedRoots are the trees the import order governs, relative to spec/proto.
var orderedRoots = []string{
	"flowseer/net", "flowseer/api", "flowseer/model",
	"flowseer/errs", "flowseer/integration", "flowseer/event",
	"flowseer/store",
}

// unorderedRoots are the trees deliberately outside the import order, relative
// to spec/proto. flowseer/service is the process-local bus contract rather than
// a boundary between packages, so "which packages may it import" has no answer
// to put in importOrder; what keeps it out of everyone's way instead is that it
// imports nothing FlowSeer-owned, which
// TestUnorderedRootsImportNothingFlowSeerOwned holds it to.
var unorderedRoots = []string{"flowseer/service"}

func TestImportOrder(t *testing.T) {
	files := orderedProtoFiles(t)
	services := servicePackages(files)

	for _, file := range files {
		pkg := protoPackage(file.rel)
		for _, imported := range file.imports {
			if !strings.HasPrefix(imported, "flowseer/") {
				continue
			}

			if reason := layeringViolation(pkg, protoPackage(imported), services); reason != "" {
				t.Errorf("%s: %s", file.rel, reason)
			}
		}
	}
}

// TestModelDeclaresNoService fails on any service declaration under model/,
// the sink rule's other half: a package that carries identity never gets to
// be the thing everything else waits on.
func TestModelDeclaresNoService(t *testing.T) {
	files := protoFilesUnder(t, filepath.Join(repoRoot(t), "spec", "proto"), "flowseer/model")
	for _, rel := range modelServiceViolations(files) {
		t.Errorf("%s: package under model declares a service", rel)
	}

	synthetic := []protoFile{{
		rel:        "flowseer/model/access/v1/x.proto",
		hasService: scanServices("service Probe {}\n"),
	}}
	got := modelServiceViolations(synthetic)
	want := []string{"flowseer/model/access/v1/x.proto"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// modelServiceViolations returns the relative paths of files that declare a
// service, so TestModelDeclaresNoService can check the real tree and a
// synthetic case with the same logic.
func modelServiceViolations(files []protoFile) []string {
	var violations []string
	for _, file := range files {
		if file.hasService {
			violations = append(violations, file.rel)
		}
	}

	return violations
}

func TestImportOrderCoversEveryPackage(t *testing.T) {
	seen := map[string]struct{}{}
	for _, file := range orderedProtoFiles(t) {
		seen[protoPackage(file.rel)] = struct{}{}
	}

	for _, pkg := range undeclaredPackages(slices.Sorted(maps.Keys(seen))) {
		t.Errorf("%s carries schemas but declares no layer in importOrder", pkg)
	}
}

// TestOrderedRootsCoverEveryTopLevelTree fails when a new top-level tree lands
// under spec/proto/flowseer without being added to orderedRoots or to
// unorderedRoots, so a whole new root cannot escape the coverage and layering
// checks above the way a package inside an existing root cannot.
func TestOrderedRootsCoverEveryTopLevelTree(t *testing.T) {
	flowseerRoot := filepath.Join(repoRoot(t), "spec", "proto", "flowseer")

	entries, err := os.ReadDir(flowseerRoot)
	if err != nil {
		t.Fatalf("reading %s: %v", flowseerRoot, err)
	}

	declared := map[string]bool{}
	for _, root := range slices.Concat(orderedRoots, unorderedRoots) {
		declared[strings.TrimPrefix(root, "flowseer/")] = true
	}

	for _, entry := range entries {
		if !entry.IsDir() || declared[entry.Name()] {
			continue
		}
		if len(protoFilesUnder(t, filepath.Join(repoRoot(t), "spec", "proto"), "flowseer/"+entry.Name())) == 0 {
			continue
		}
		t.Errorf("flowseer/%s carries schemas but is in neither orderedRoots nor unorderedRoots", entry.Name())
	}
}

// TestUnorderedRootsImportNothingFlowSeerOwned holds the roots importOrder does
// not govern. Nothing in the table constrains what such a root imports, so
// without this a schema under flowseer/service could reach a Connect service
// package or another process's private store with every other gate green.
func TestUnorderedRootsImportNothingFlowSeerOwned(t *testing.T) {
	protoRoot := filepath.Join(repoRoot(t), "spec", "proto")
	for _, root := range unorderedRoots {
		for _, found := range crossPackageImports(protoFilesUnder(t, protoRoot, root)) {
			t.Errorf("%s: %s is outside the import order and may import nothing FlowSeer-owned", found, root)
		}
	}

	synthetic := []protoFile{{
		rel: "flowseer/service/v1/x.proto",
		imports: []string{
			"google/protobuf/timestamp.proto",
			"flowseer/service/v1/message.proto",
			"flowseer/model/edge/v1/edge.proto",
		},
	}}
	got := crossPackageImports(synthetic)
	want := []string{"flowseer/service/v1/x.proto imports flowseer/model/edge/v1/edge.proto"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// crossPackageImports returns one "<file> imports <schema>" line per
// FlowSeer-owned import a file declares outside its own package.
func crossPackageImports(files []protoFile) []string {
	var found []string
	for _, file := range files {
		pkg := protoPackage(file.rel)
		for _, imported := range file.imports {
			if !strings.HasPrefix(imported, "flowseer/") || protoPackage(imported) == pkg {
				continue
			}
			found = append(found, fmt.Sprintf("%s imports %s", file.rel, imported))
		}
	}

	return found
}

// TestLayeringViolationRules pins the order's shape against synthetic pairs, so
// the walk above keeps meaning something on a tree that happens to be clean.
func TestLayeringViolationRules(t *testing.T) {
	tests := []struct {
		name     string
		importer string
		imported string
		// services replaces the tree's service-declaring packages, for a case
		// that must be rejected by the sink rule alone.
		services map[string]bool
		want     bool
		// wantReason, when set, is the reason layeringViolation must give, so
		// the case pins which rule rejected the import and not merely that one
		// did.
		wantReason string
	}{
		{name: "leaf imports nothing FlowSeer-owned", importer: "net/addr", imported: "net/packet"},
		{name: "package imports itself", importer: "net/addr", imported: "net/addr", want: true},
		{name: "switching imports a leaf", importer: "net/switching", imported: "net/packet", want: true},
		{name: "ip imports a leaf it did not declare", importer: "net/ip", imported: "net/phy"},
		{name: "interface imports a layer", importer: "net/interface", imported: "net/switching", want: true},
		{name: "layer imports upward", importer: "net/switching", imported: "net/interface"},
		{name: "layer imports a protocol", importer: "net/interface", imported: "net/protocol/lldp"},
		{name: "protocol imports a layer", importer: "net/protocol/lldp", imported: "net/interface", want: true},
		{name: "protocol imports another protocol", importer: "net/protocol/lldp", imported: "net/protocol/stp"},
		{name: "package outside the table", importer: "net/routing", imported: "net/addr"},
		{name: "primitive imports a boundary", importer: "net/interface", imported: "model/inventory"},
		{name: "inventory imports a leaf boundary", importer: "model/inventory", imported: "model/policy", want: true},
		{name: "edge imports its credential handles", importer: "api/edge", imported: "model/policy", want: true},
		{name: "leaf boundary imports edge", importer: "model/policy", imported: "api/edge"},
		{name: "access values import inventory", importer: "model/access", imported: "model/inventory", want: true},
		{name: "access values import the operator api", importer: "model/access", imported: "api/device"},
		{name: "operator api imports access values", importer: "api/device", imported: "model/access", want: true},
		{name: "operator api imports the bus contract", importer: "api/device", imported: "service"},
		{name: "leaf boundary imports inventory", importer: "model/policy", imported: "model/inventory"},
		{name: "execution envelope imports access values", importer: "integration/device", imported: "model/access", want: true},
		{name: "execution envelope imports errs", importer: "integration/device", imported: "errs", want: true},
		{name: "execution envelope imports the audit event", importer: "integration/device", imported: "event/device"},
		{name: "audit event imports access values", importer: "event/device", imported: "model/access", want: true},
		{name: "audit event imports inventory", importer: "event/device", imported: "model/inventory", want: true},
		{name: "audit event imports the execution envelope", importer: "event/device", imported: "integration/device"},
		{name: "audit event imports api/edge directly", importer: "event/device", imported: "api/edge"},
		{name: "operator api imports errs", importer: "api/device", imported: "errs", want: true},
		{name: "edge imports credential material", importer: "api/edge", imported: "model/credential", want: true},
		{name: "credential material imports edge", importer: "model/credential", imported: "api/edge"},
		{name: "credential material imports policy handles", importer: "model/credential", imported: "model/policy"},
		{name: "storage imports access values", importer: "store/device", imported: "model/access", want: true},
		{name: "storage imports credential material", importer: "store/device", imported: "model/credential", want: true},
		{name: "storage imports the edge service package", importer: "store/device", imported: "api/edge"},
		{name: "storage imports the edge entity", importer: "store/device", imported: "model/edge", want: true},
		{name: "access values import storage", importer: "model/access", imported: "store/device"},
		{name: "operator api imports storage", importer: "api/device", imported: "store/device"},
		{name: "operator api imports the execution envelope", importer: "api/device", imported: "integration/device"},
		{
			name:       "storage imports a sink",
			importer:   "store/device",
			imported:   "api/device",
			wantReason: "api/device declares a service and is imported by nothing",
		},
		{
			name:       "access values import a sink",
			importer:   "model/access",
			imported:   "integration/device",
			wantReason: "integration/device declares a service and is imported by nothing",
		},
		// The sink rule's own case. Every other rejection above is one the
		// table would make anyway, so this is the pair that fails when the rule
		// goes: model/access declares model/inventory, and only a service
		// declaration in it can stand in the way.
		{
			name:       "declared import of a package that declares a service",
			importer:   "model/access",
			imported:   "model/inventory",
			services:   map[string]bool{"model/inventory": true},
			wantReason: "model/inventory declares a service and is imported by nothing",
		},
	}

	treeServices := servicePackages(orderedProtoFiles(t))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			services := treeServices
			if tt.services != nil {
				services = tt.services
			}

			reason := layeringViolation(tt.importer, tt.imported, services)
			if allowed := reason == ""; allowed != tt.want {
				t.Errorf("got allowed=%t, want %t", allowed, tt.want)
			}
			if tt.wantReason != "" && reason != tt.wantReason {
				t.Errorf("got reason %q, want %q", reason, tt.wantReason)
			}
		})
	}
}

// TestScanImports pins the forms the walk must recognize. Edition 2024's
// option-only import is the one that would silently hide a dependency.
func TestScanImports(t *testing.T) {
	source := `edition = "2024";

package flowseer.net.interface.v1;

import "flowseer/net/switching/v1/vlan_tag_stack.proto";
import option "flowseer/net/switching/v1/vlan_id.proto";
import public "flowseer/net/addr/v1/eui.proto";
  import "google/protobuf/duration.proto";
// import "flowseer/net/phy/v1/ethernet_facet.proto";
`

	got := scanImports(source)
	want := []string{
		"flowseer/net/switching/v1/vlan_tag_stack.proto",
		"flowseer/net/switching/v1/vlan_id.proto",
		"flowseer/net/addr/v1/eui.proto",
		"google/protobuf/duration.proto",
	}

	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestScanServices pins the forms the sink rule must recognize. An indented
// declaration is the one that would let a model package declare a service and
// still read as service-free.
func TestScanServices(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{name: "declaration at the margin", source: "service Probe {\n}\n", want: true},
		{name: "indented declaration", source: "  service Probe {\n  }\n", want: true},
		{name: "commented out", source: "// service Probe {}\n"},
		{name: "message whose name begins with the keyword", source: "message ServiceProbe {\n}\n"},
		{name: "rpc inside a service body", source: "  rpc Probe(ProbeRequest) returns (ProbeResponse);\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scanServices(tt.source); got != tt.want {
				t.Errorf("got %t, want %t", got, tt.want)
			}
		})
	}
}

func TestUndeclaredPackages(t *testing.T) {
	got := undeclaredPackages([]string{"net/addr", "net/routing", "net/interface", "net/wlan"})
	want := []string{"net/routing", "net/wlan"}

	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// layeringViolation reports why importer may not import imported, or "" when the
// import is within the declared order. services holds the service-declaring
// packages the sink rule blocks, from servicePackages.
func layeringViolation(importer, imported string, services map[string]bool) string {
	if importer == imported {
		return ""
	}
	if services[imported] {
		return fmt.Sprintf("%s declares a service and is imported by nothing", imported)
	}

	allowed, declared := importOrder[importer]
	if !declared {
		return fmt.Sprintf("package %s declares no layer in importOrder", importer)
	}
	if slices.Contains(allowed, imported) {
		return ""
	}

	return fmt.Sprintf("importing %s is outside %s's declared layer (%s)",
		imported, importer, strings.Join(allowed, ", "))
}

// servicePackages returns the packages among files that declare at least one
// service, the sink rule's blocklist.
func servicePackages(files []protoFile) map[string]bool {
	services := map[string]bool{}
	for _, file := range files {
		if file.hasService {
			services[protoPackage(file.rel)] = true
		}
	}

	return services
}

// undeclaredPackages returns the schema-bearing packages the table does not cover.
func undeclaredPackages(packages []string) []string {
	var missing []string
	for _, pkg := range packages {
		if _, declared := importOrder[pkg]; !declared {
			missing = append(missing, pkg)
		}
	}

	return missing
}

// protoPackage strips the version segment and file name from a path under
// spec/proto, so flowseer/net/ip/v1/ip_facet.proto becomes net/ip.
func protoPackage(protoPath string) string {
	parts := strings.Split(strings.TrimPrefix(filepath.ToSlash(protoPath), "flowseer/"), "/")
	for i, part := range parts {
		if versionSegment.MatchString(part) {
			return strings.Join(parts[:i], "/")
		}
	}

	return strings.Join(parts[:max(len(parts)-1, 0)], "/")
}

var (
	versionSegment = regexp.MustCompile(`^v\d+(alpha|beta)?\d*$`)
	importLine     = regexp.MustCompile(`^\s*import\s+(?:option\s+|public\s+|weak\s+)?"([^"]+)"\s*;`)
	serviceLine    = regexp.MustCompile(`^\s*service\s+\w+\s*\{`)
)

type protoFile struct {
	// rel is the file's path relative to spec/proto, in slash form.
	rel        string
	imports    []string
	hasService bool
}

// orderedProtoFiles collects every .proto file under the ordered roots with the
// import paths it declares. Directories holding no .proto file never appear, so
// placeholders such as net/wlan/v1 stay out of the completeness check.
func orderedProtoFiles(t *testing.T) []protoFile {
	t.Helper()

	protoRoot := filepath.Join(repoRoot(t), "spec", "proto")

	var files []protoFile
	for _, root := range orderedRoots {
		files = append(files, protoFilesUnder(t, protoRoot, root)...)
	}
	if len(files) == 0 {
		t.Fatalf("no schemas found under %s", strings.Join(orderedRoots, ", "))
	}

	return files
}

func protoFilesUnder(t *testing.T, protoRoot, root string) []protoFile {
	t.Helper()

	var files []protoFile
	err := filepath.WalkDir(filepath.Join(protoRoot, root), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".proto" {
			return nil
		}

		rel, err := filepath.Rel(protoRoot, path)
		if err != nil {
			return err
		}

		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		files = append(files, protoFile{
			rel:        filepath.ToSlash(rel),
			imports:    scanImports(string(source)),
			hasService: scanServices(string(source)),
		})

		return nil
	})
	if err != nil {
		t.Fatalf("collecting schemas under %s: %v", root, err)
	}

	return files
}

func scanImports(source string) []string {
	var imports []string
	for line := range strings.SplitSeq(source, "\n") {
		if match := importLine.FindStringSubmatch(line); match != nil {
			imports = append(imports, match[1])
		}
	}

	return imports
}

// scanServices reports whether source declares at least one service.
func scanServices(source string) bool {
	for line := range strings.SplitSeq(source, "\n") {
		if serviceLine.MatchString(line) {
			return true
		}
	}

	return false
}
