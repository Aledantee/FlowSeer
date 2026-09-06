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
var importOrder = map[string][]string{
	"net/addr":   nil,
	"net/packet": nil,
	"net/phy":    nil,

	"net/switching": {"net/addr", "net/packet"},
	"net/ip":        {"net/addr"},

	"net/interface": {"net/addr", "net/packet", "net/phy", "net/switching", "net/ip"},

	// A protocol may import any layer below it, and never another protocol.
	"net/protocol/lldp": {"net/addr", "net/packet", "net/phy", "net/switching", "net/ip", "net/interface"},

	// Boundary packages consume the primitives and never feed them.
	// device/policy imports nothing FlowSeer-owned, the one leaf that lets
	// inventory name a policy without a cycle. api/edge may import
	// device/policy for the credential and host-trust handles its
	// credential RPCs return, because device/policy imports nothing back.
	// device/credential imports nothing either: api/edge carries the typed
	// credential material on its credential responses, so it sits beside
	// device/policy as a second leaf below api/edge.
	"api/edge":          {"device/credential", "device/policy"},
	"device/credential": nil,
	"device/policy":     nil,

	// The error wire payload. A leaf like device/policy: every boundary may
	// carry an error, so nothing may depend on it.
	"errs": nil,

	"api/inventory": {"api/edge", "device/policy", "net/addr", "net/packet", "net/phy", "net/switching", "net/ip", "net/interface", "net/protocol/lldp"},

	// The operation values every device-access boundary shares. They reach
	// api/edge for the responsible edge, so a boundary that imports them
	// reaches api/edge only through here.
	"device/access": {"api/edge", "api/inventory", "device/policy", "net/addr", "net/packet", "net/phy", "net/switching", "net/ip", "net/interface", "net/protocol/lldp"},

	// The operator API, the execution envelope, and the audit event are
	// sibling boundary consumers of device/access and errs, and none of the
	// three imports another. api/inventory and device/policy predate the
	// errs amendment and stay direct api/device dependencies; the envelope
	// carries no device or edge ref at all (the transport already names
	// both), while the audit event needs api/inventory directly because it
	// is read outside any live transport context.
	"api/device": {"api/inventory", "device/access", "device/policy", "errs", "net/addr", "net/packet", "net/phy", "net/switching", "net/ip", "net/interface", "net/protocol/lldp"},

	"integration/device": {"device/access", "errs"},

	"event/device": {"api/inventory", "device/access", "errs"},

	// The device service's own storage: written and read by one process,
	// above every boundary it embeds and imported by none.
	"store/device": {"api/edge", "api/inventory", "device/access", "device/credential", "device/policy", "errs", "net/addr", "net/packet", "net/phy", "net/switching", "net/ip", "net/interface", "net/protocol/lldp"},
}

// orderedRoots are the trees the import order governs, relative to spec/proto.
// flowseer/service stays out: it is the process-local bus contract and no
// boundary package may import it.
var orderedRoots = []string{
	"flowseer/net", "flowseer/api", "flowseer/device",
	"flowseer/errs", "flowseer/integration", "flowseer/event",
	"flowseer/store",
}

func TestImportOrder(t *testing.T) {
	for _, file := range orderedProtoFiles(t) {
		pkg := protoPackage(file.rel)
		for _, imported := range file.imports {
			if !strings.HasPrefix(imported, "flowseer/") {
				continue
			}

			if reason := layeringViolation(pkg, protoPackage(imported)); reason != "" {
				t.Errorf("%s: %s", file.rel, reason)
			}
		}
	}
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
// under spec/proto/flowseer without being added to orderedRoots, so a whole
// new root cannot escape the coverage and layering checks above the way a
// package inside an existing root cannot. flowseer/service is the one
// declared exception: it is the process-local bus contract, and no boundary
// package may import it.
func TestOrderedRootsCoverEveryTopLevelTree(t *testing.T) {
	flowseerRoot := filepath.Join(repoRoot(t), "spec", "proto", "flowseer")

	entries, err := os.ReadDir(flowseerRoot)
	if err != nil {
		t.Fatalf("reading %s: %v", flowseerRoot, err)
	}

	declared := map[string]bool{}
	for _, root := range orderedRoots {
		declared[strings.TrimPrefix(root, "flowseer/")] = true
	}

	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "service" || declared[entry.Name()] {
			continue
		}
		if len(protoFilesUnder(t, filepath.Join(repoRoot(t), "spec", "proto"), "flowseer/"+entry.Name())) == 0 {
			continue
		}
		t.Errorf("flowseer/%s carries schemas but is missing from orderedRoots", entry.Name())
	}
}

// TestLayeringViolationRules pins the order's shape against synthetic pairs, so
// the walk above keeps meaning something on a tree that happens to be clean.
func TestLayeringViolationRules(t *testing.T) {
	tests := []struct {
		name     string
		importer string
		imported string
		want     bool
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
		{name: "primitive imports a boundary", importer: "net/interface", imported: "api/inventory"},
		{name: "inventory imports a leaf boundary", importer: "api/inventory", imported: "device/policy", want: true},
		{name: "edge imports its credential handles", importer: "api/edge", imported: "device/policy", want: true},
		{name: "leaf boundary imports edge", importer: "device/policy", imported: "api/edge"},
		{name: "access values import inventory", importer: "device/access", imported: "api/inventory", want: true},
		{name: "access values import the operator api", importer: "device/access", imported: "api/device"},
		{name: "operator api imports access values", importer: "api/device", imported: "device/access", want: true},
		{name: "operator api imports the bus contract", importer: "api/device", imported: "service"},
		{name: "leaf boundary imports inventory", importer: "device/policy", imported: "api/inventory"},
		{name: "execution envelope imports access values", importer: "integration/device", imported: "device/access", want: true},
		{name: "execution envelope imports errs", importer: "integration/device", imported: "errs", want: true},
		{name: "execution envelope imports the audit event", importer: "integration/device", imported: "event/device"},
		{name: "audit event imports access values", importer: "event/device", imported: "device/access", want: true},
		{name: "audit event imports inventory", importer: "event/device", imported: "api/inventory", want: true},
		{name: "audit event imports the execution envelope", importer: "event/device", imported: "integration/device"},
		{name: "audit event imports api/edge directly", importer: "event/device", imported: "api/edge"},
		{name: "operator api imports errs", importer: "api/device", imported: "errs", want: true},
		{name: "edge imports credential material", importer: "api/edge", imported: "device/credential", want: true},
		{name: "credential material imports edge", importer: "device/credential", imported: "api/edge"},
		{name: "credential material imports policy handles", importer: "device/credential", imported: "device/policy"},
		{name: "storage imports access values", importer: "store/device", imported: "device/access", want: true},
		{name: "storage imports credential material", importer: "store/device", imported: "device/credential", want: true},
		{name: "access values import storage", importer: "device/access", imported: "store/device"},
		{name: "operator api imports storage", importer: "api/device", imported: "store/device"},
		{name: "operator api imports the execution envelope", importer: "api/device", imported: "integration/device"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if allowed := layeringViolation(tt.importer, tt.imported) == ""; allowed != tt.want {
				t.Errorf("got allowed=%t, want %t", allowed, tt.want)
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

func TestUndeclaredPackages(t *testing.T) {
	got := undeclaredPackages([]string{"net/addr", "net/routing", "net/interface", "net/wlan"})
	want := []string{"net/routing", "net/wlan"}

	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// layeringViolation reports why importer may not import imported, or "" when the
// import is within the declared order.
func layeringViolation(importer, imported string) string {
	if importer == imported {
		return ""
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
)

type protoFile struct {
	// rel is the file's path relative to spec/proto, in slash form.
	rel     string
	imports []string
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

		imports, err := protoImports(path)
		if err != nil {
			return err
		}

		files = append(files, protoFile{rel: filepath.ToSlash(rel), imports: imports})

		return nil
	})
	if err != nil {
		t.Fatalf("collecting schemas under %s: %v", root, err)
	}

	return files
}

func protoImports(path string) ([]string, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	return scanImports(string(source)), nil
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
