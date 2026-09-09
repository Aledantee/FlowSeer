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

// importOrder declares, for every schema-bearing package under
// spec/proto/flowseer/net, the packages it may import. A package imports itself
// freely; anything else it imports must be listed here. Adding a package to the
// tree is one line in this table — leaving it out fails
// TestNetImportOrderCoversEveryPackage rather than silently escaping the order.
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
}

// netRoot is the tree the import order governs, relative to spec/proto.
const netRoot = "flowseer/net"

func TestNetImportOrder(t *testing.T) {
	for _, file := range netProtoFiles(t) {
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

func TestNetImportOrderCoversEveryPackage(t *testing.T) {
	seen := map[string]struct{}{}
	for _, file := range netProtoFiles(t) {
		seen[protoPackage(file.rel)] = struct{}{}
	}

	for _, pkg := range undeclaredPackages(slices.Sorted(maps.Keys(seen))) {
		t.Errorf("%s carries schemas but declares no layer in importOrder", pkg)
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
		{name: "import outside the net tree", importer: "net/interface", imported: "api/inventory"},
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

// netProtoFiles collects every .proto file under spec/proto/flowseer/net with the
// import paths it declares. Directories holding no .proto file never appear, so
// placeholders such as net/wlan/v1 stay out of the completeness check.
func netProtoFiles(t *testing.T) []protoFile {
	t.Helper()

	protoRoot := filepath.Join(repoRoot(t), "spec", "proto")

	var files []protoFile
	err := filepath.WalkDir(filepath.Join(protoRoot, netRoot), func(path string, d fs.DirEntry, err error) error {
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
		t.Fatalf("collecting schemas under %s: %v", netRoot, err)
	}
	if len(files) == 0 {
		t.Fatalf("no schemas found under %s", netRoot)
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
