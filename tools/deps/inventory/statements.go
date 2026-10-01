package inventory

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
	"gopkg.in/yaml.v3"

	"go.aledante.io/FlowSeer/src/common/errs"
)

const statementRoot = "docs/dependencies/statements"

// Finding is a dependency statement gate finding. Path is relative to the
// repository root when the finding refers to a statement file.
type Finding struct {
	Path    string
	Message string
}

// CheckStatements verifies that every direct dependency has exactly one
// statement and that each statement has valid admission metadata and prose.
func CheckStatements(root string) ([]Finding, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, errs.Wrap(err, "resolve statement root")
	}
	direct, err := statementDependencies(root)
	if err != nil {
		return nil, err
	}
	statements, findings, err := readStatements(root)
	if err != nil {
		return nil, err
	}

	directByKey := make(map[string]statementDependency, len(direct))
	for _, dependency := range direct {
		directByKey[statementKey(dependency.Ecosystem, dependency.Name)] = dependency
	}
	for key, dependency := range directByKey {
		if _, ok := statements[key]; !ok {
			findings = append(findings, Finding{
				Path:    statementPath(dependency.Ecosystem, dependency.Name),
				Message: "direct dependency has no statement",
			})
		}
	}
	for key, statement := range statements {
		dependency, ok := directByKey[key]
		if !ok {
			findings = append(findings, Finding{Path: statement.Path, Message: "statement has no direct dependency"})
			continue
		}
		findings = append(findings, validateStatement(statement, dependency)...)
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Message < findings[j].Message
	})
	return findings, nil
}

// DirectDependencies returns the external direct requirements that the
// statement gate checks. Repository-owned module paths are excluded.
func DirectDependencies(root string) ([]DirectDependency, error) {
	return directRequirements(root)
}

type statementDependency struct {
	Ecosystem string
	Name      string
	Versions  []string
	Manifests []string
	Criteria  string
}

func statementDependencies(root string) ([]statementDependency, error) {
	all, err := directRequirements(root)
	if err != nil {
		return nil, err
	}
	merged := make(map[string]statementDependency)
	for _, dependency := range all {
		key := statementKey(dependency.Ecosystem, dependency.Name)
		current := merged[key]
		current.Ecosystem = dependency.Ecosystem
		current.Name = dependency.Name
		current.Versions = appendUnique(current.Versions, dependency.Version)
		current.Manifests = appendUnique(current.Manifests, dependency.Manifests...)
		if dependency.Deploy {
			current.Criteria = "deploy"
		} else if current.Criteria == "" {
			current.Criteria = "run"
		}
		merged[key] = current
	}

	result := make([]statementDependency, 0, len(merged))
	for _, dependency := range merged {
		dependency.Versions = sortedUnique(dependency.Versions)
		dependency.Manifests = sortedUnique(dependency.Manifests)
		if dependency.Criteria == "" {
			dependency.Criteria = "run"
		}
		result = append(result, dependency)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Ecosystem != result[j].Ecosystem {
			return result[i].Ecosystem < result[j].Ecosystem
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func directRequirements(root string) ([]DirectDependency, error) {
	modules, err := DiscoverModules(root)
	if err != nil {
		return nil, err
	}
	all := make([]DirectDependency, 0)
	for _, module := range modules {
		all = append(all, module.Requires...)
		tools, err := toolRequirements(root, module)
		if err != nil {
			return nil, err
		}
		all = append(all, tools...)
	}
	lockPath := filepath.Join(root, "frontend", "web", "pnpm-lock.yaml")
	if _, err := os.Stat(lockPath); err == nil {
		lock, err := readPnpmLock(lockPath, "frontend/web/pnpm-lock.yaml")
		if err != nil {
			return nil, err
		}
		all = append(all, lock.Direct...)
	} else if !os.IsNotExist(err) {
		return nil, errs.Wrap(err, "stat pnpm lockfile")
	}

	rootModule := moduleForManifest(modules, "go.mod")
	filtered := make([]DirectDependency, 0, len(all))
	for _, dependency := range all {
		if dependency.Ecosystem == "go" && ownedModule(dependency.Name, rootModule.Path) {
			continue
		}
		filtered = append(filtered, dependency)
	}
	return mergeDirect(filtered), nil
}

func toolRequirements(root string, module Module) ([]DirectDependency, error) {
	path := filepath.Join(root, filepath.FromSlash(module.Manifest))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errs.Wrap(err, "read Go module manifest for tool requirements")
	}
	parsed, err := modfile.Parse(path, data, nil)
	if err != nil {
		return nil, errs.Wrap(err, "parse Go module manifest for tool requirements")
	}
	var tools []string
	for _, tool := range parsed.Tool {
		tools = append(tools, tool.Path)
	}
	var dependencies []DirectDependency
	for _, require := range parsed.Require {
		for _, tool := range tools {
			if require.Mod.Path != tool && !strings.HasPrefix(tool, require.Mod.Path+"/") {
				continue
			}
			dependencies = append(dependencies, DirectDependency{
				Ecosystem: "go",
				Name:      require.Mod.Path,
				Version:   require.Mod.Version,
				Manifests: []string{module.Manifest},
			})
			break
		}
	}
	return dependencies, nil
}

type statement struct {
	Path     string
	Metadata statementMetadata
	Body     string
}

type statementMetadata struct {
	Name       string   `yaml:"name"`
	Ecosystem  string   `yaml:"ecosystem"`
	RequiredBy []string `yaml:"required_by"`
	Criteria   string   `yaml:"criteria"`
	Verdict    string   `yaml:"verdict"`
	Approved   string   `yaml:"approved"`
}

func readStatements(root string) (map[string]statement, []Finding, error) {
	statements := make(map[string]statement)
	var findings []Finding
	for _, ecosystem := range []string{"go", "npm"} {
		dir := filepath.Join(root, statementRoot, ecosystem)
		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(path) != ".md" {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value, err := parseStatement(filepath.ToSlash(rel), string(data))
			if err != nil {
				findings = append(findings, Finding{Path: filepath.ToSlash(rel), Message: err.Error()})
				return nil
			}
			key := statementKey(value.Metadata.Ecosystem, value.Metadata.Name)
			if _, exists := statements[key]; exists {
				findings = append(findings, Finding{Path: filepath.ToSlash(rel), Message: "duplicate statement"})
				return nil
			}
			statements[key] = value
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, nil, errs.Wrapf(err, "read dependency statements under %s", dir)
		}
	}
	return statements, findings, nil
}

func parseStatement(path, content string) (statement, error) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return statement{}, errs.Msgf("statement %s has no frontmatter", path)
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return statement{}, errs.Msgf("statement %s has unterminated frontmatter", path)
	}
	var metadata statementMetadata
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &metadata); err != nil {
		return statement{}, errs.Wrapf(err, "parse statement frontmatter %s", path)
	}
	return statement{Path: path, Metadata: metadata, Body: strings.Join(lines[end+1:], "\n")}, nil
}

func validateStatement(value statement, dependency statementDependency) []Finding {
	metadata := value.Metadata
	findings := make([]Finding, 0)
	pathName := strings.TrimSuffix(strings.TrimPrefix(value.Path, statementRoot+"/"), ".md")
	parts := strings.SplitN(pathName, "/", 2)
	if len(parts) != 2 || parts[0] != metadata.Ecosystem || parts[1] != metadata.Name {
		findings = append(findings, Finding{Path: value.Path, Message: "statement name or ecosystem does not match its path"})
	}
	if metadata.Name == "" {
		findings = append(findings, Finding{Path: value.Path, Message: "name is empty"})
	}
	if metadata.Ecosystem != "go" && metadata.Ecosystem != "npm" {
		findings = append(findings, Finding{Path: value.Path, Message: "ecosystem is invalid"})
	}
	if metadata.Criteria != "run" && metadata.Criteria != "deploy" {
		findings = append(findings, Finding{Path: value.Path, Message: "criteria is invalid"})
	}
	if metadata.Verdict != "keep" && metadata.Verdict != "cut" {
		findings = append(findings, Finding{Path: value.Path, Message: "verdict is invalid"})
	}
	if metadata.Approved != "" {
		if _, err := time.Parse("2006-01-02", metadata.Approved); err != nil {
			findings = append(findings, Finding{Path: value.Path, Message: "approved must be empty or an ISO date"})
		}
	}
	if !sameStrings(metadata.RequiredBy, dependency.Manifests) {
		findings = append(findings, Finding{Path: value.Path, Message: "required_by does not match direct manifests"})
	}
	for _, heading := range []string{"Why it is required", "Why it is safe", "Why not owned code"} {
		if content, ok := statementSection(value.Body, heading); !ok || strings.TrimSpace(content) == "" {
			findings = append(findings, Finding{Path: value.Path, Message: "section " + heading + " is empty"})
		}
	}
	return findings
}

func statementSection(body, wanted string) (string, bool) {
	lines := strings.Split(body, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "## "+wanted {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return "", false
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n"), true
}

func statementKey(ecosystem, name string) string {
	return ecosystem + "\x00" + name
}

func statementPath(ecosystem, name string) string {
	return filepath.ToSlash(filepath.Join(statementRoot, ecosystem, name+".md"))
}

func sameStrings(left, right []string) bool {
	return strings.Join(sortedUnique(left), "\x00") == strings.Join(sortedUnique(right), "\x00")
}
