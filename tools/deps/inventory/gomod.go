package inventory

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"

	"go.aledante.io/FlowSeer/src/common/errs"
)

func parseModule(root, manifestPath string) (Module, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return Module{}, errs.Wrap(err, "read Go module manifest")
	}
	parsed, err := modfile.Parse(manifestPath, data, nil)
	if err != nil {
		return Module{}, errs.Wrapf(err, "parse Go module manifest %s", manifestPath)
	}
	if parsed.Module == nil {
		return Module{}, errs.Msgf("Go module manifest %s has no module path", manifestPath)
	}

	manifest := filepath.ToSlash(relativePath(root, manifestPath))
	module := Module{
		Dir:      filepath.Dir(manifestPath),
		Manifest: manifest,
		Path:     parsed.Module.Mod.Path,
		Direct:   make(map[string]bool),
	}
	goSum := filepath.Join(module.Dir, "go.sum")
	if _, err := os.Stat(goSum); err == nil {
		module.GoSum = filepath.ToSlash(relativePath(root, goSum))
	}

	toolPaths := make([]string, 0, len(parsed.Tool))
	for _, tool := range parsed.Tool {
		toolPaths = append(toolPaths, tool.Path)
	}
	for _, require := range parsed.Require {
		isDirect := !require.Indirect
		for _, toolPath := range toolPaths {
			if toolPath == require.Mod.Path || strings.HasPrefix(toolPath, require.Mod.Path+"/") {
				isDirect = true
				break
			}
		}
		if !isDirect || ownedModule(require.Mod.Path, parsed.Module.Mod.Path) {
			continue
		}
		module.Direct[require.Mod.Path] = true
		module.Requires = append(module.Requires, DirectDependency{
			Ecosystem: "go",
			Name:      require.Mod.Path,
			Version:   require.Mod.Version,
			Manifests: []string{manifest},
		})
	}
	return module, nil
}

func ownedModule(name, root string) bool {
	return name == root || strings.HasPrefix(name, root+"/")
}

func relativePath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}
