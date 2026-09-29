package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/scanner"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"go.aledante.io/FlowSeer/src/common/errs"
)

type signatureError struct {
	file string
	line int
	name string
}

func (e *signatureError) Error() string {
	return fmt.Sprintf("%s:%d: %s has invalid test signature", e.file, e.line, e.name)
}

var (
	mustWordRegex = regexp.MustCompile(`\bMUST\b`)
	thenWordRegex = regexp.MustCompile(`\bTHEN\b`)
)

type provedByTest struct {
	name string
	line int
}

type provedByBlock struct {
	startLine     int
	lastLine      int
	endsWithComma bool
	tests         []provedByTest
}

// findGuaranteesInDir finds a file named exactly GUARANTEES.md in dir, case-sensitive.
func findGuaranteesInDir(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.Name() == "GUARANTEES.md" && !entry.IsDir() {
			return filepath.Join(dir, entry.Name())
		}
	}
	return ""
}

// selectGuaranteeFiles selects the GUARANTEES.md files to check based on changed paths or all.
func selectGuaranteeFiles(paths []string, root string, all bool) ([]string, error) {
	root = filepath.Clean(root)
	if all {
		var guaranteeFiles []string
		skipDirs := map[string]bool{
			".git":         true,
			"node_modules": true,
			"vendor":       true,
			"testdata":     true,
		}
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				name := d.Name()
				if skipDirs[name] || (strings.HasPrefix(name, ".") && path != root) {
					return filepath.SkipDir
				}
				if g := findGuaranteesInDir(path); g != "" {
					guaranteeFiles = append(guaranteeFiles, g)
				}
			}
			return nil
		})
		if err != nil {
			return nil, errs.Wrap(err, "walk failed")
		}
		sort.Strings(guaranteeFiles)
		return uniqueStrings(guaranteeFiles), nil
	}

	selected := make(map[string]bool)
	for _, raw := range paths {
		if raw == "--" {
			continue
		}
		target := raw
		if !filepath.IsAbs(target) {
			target = filepath.Join(root, target)
		}
		target = filepath.Clean(target)

		dir := target
		fi, err := os.Stat(target)
		if err != nil || !fi.IsDir() {
			dir = filepath.Dir(target)
		}
		if g := findGuaranteesInDir(dir); g != "" {
			selected[g] = true
		}
	}

	var result []string
	for g := range selected {
		result = append(result, g)
	}
	sort.Strings(result)
	return result, nil
}

// checkGuarantees selects and checks all relevant GUARANTEES.md files.
func checkGuarantees(paths []string, root string, all bool) ([]string, error) {
	files, err := selectGuaranteeFiles(paths, root, all)
	if err != nil {
		return nil, err
	}
	var allErrors []string
	for _, f := range files {
		allErrors = append(allErrors, checkGuaranteesFile(f, root)...)
	}
	return allErrors, nil
}

type nonInterruptingHTMLBlockParser struct {
	parser.BlockParser
}

func (p *nonInterruptingHTMLBlockParser) CanInterruptParagraph() bool {
	return false
}

func newGuaranteesParser() parser.Parser {
	blockParsers := []util.PrioritizedValue{
		util.Prioritized(parser.NewSetextHeadingParser(), 100),
		util.Prioritized(parser.NewThematicBreakParser(), 200),
		util.Prioritized(parser.NewListParser(), 300),
		util.Prioritized(parser.NewListItemParser(), 400),
		util.Prioritized(parser.NewCodeBlockParser(), 500),
		util.Prioritized(parser.NewATXHeadingParser(), 600),
		util.Prioritized(parser.NewFencedCodeBlockParser(), 700),
		util.Prioritized(parser.NewBlockquoteParser(), 800),
		util.Prioritized(&nonInterruptingHTMLBlockParser{BlockParser: parser.NewHTMLBlockParser()}, 900),
		util.Prioritized(parser.NewParagraphParser(), 1000),
	}
	return parser.NewParser(
		parser.WithBlockParsers(blockParsers...),
		parser.WithInlineParsers(parser.DefaultInlineParsers()...),
		parser.WithParagraphTransformers(parser.DefaultParagraphTransformers()...),
	)
}

// checkGuaranteesFile checks a single GUARANTEES.md file for syntax and test citations.
func checkGuaranteesFile(filePath, root string) []string {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return []string{fmt.Sprintf("%s: cannot read file: %v", filePath, err)}
	}

	rootAbs, err := filepath.Abs(root)
	if err == nil {
		root = rootAbs
	}
	fileAbs, err := filepath.Abs(filePath)
	if err == nil {
		filePath = fileAbs
	}

	displayPath := filePath
	if rel, err := filepath.Rel(root, filePath); err == nil {
		displayPath = rel
	}

	pkgDir := filepath.Dir(filePath)
	pkgDisplay := pkgDir
	if rel, err := filepath.Rel(root, pkgDir); err == nil {
		pkgDisplay = rel
	}

	availableTests, listErr := findTestFunctions(pkgDir)
	var checkErrors []string
	if listErr != nil {
		var sigErr *signatureError
		if errors.As(listErr, &sigErr) {
			relTestFile := filepath.Join(pkgDisplay, sigErr.file)
			checkErrors = append(checkErrors, fmt.Sprintf("%s:1: %s:%d: %s has invalid test signature", displayPath, relTestFile, sigErr.line, sigErr.name))
		} else {
			checkErrors = append(checkErrors, fmt.Sprintf("%s:1: go list failed in %s: %s", displayPath, pkgDisplay, listErr.Error()))
		}
	}

	doc := newGuaranteesParser().Parse(text.NewReader(content))

	seenHeadings := make(map[string]int)
	seenTitle := false

	currentHeading := ""
	currentHeadingLine := 0
	currentHasWhenThen := false
	var currentNormativeLines []int
	var currentProvedByBlocks []provedByBlock
	seenNormative := false
	seenList := false
	seenProvedBy := false

	finishSection := func() {
		if currentHeading == "" {
			return
		}
		if len(currentNormativeLines) == 0 {
			checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: %q has no normative MUST sentence", displayPath, currentHeadingLine, currentHeading))
		} else if len(currentNormativeLines) > 1 {
			for _, extraLine := range currentNormativeLines[1:] {
				checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: %q has more than one normative sentence", displayPath, extraLine, currentHeading))
			}
		}
		if !currentHasWhenThen {
			checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: %q has no - WHEN ... THEN scenario bullet", displayPath, currentHeadingLine, currentHeading))
		}
		switch len(currentProvedByBlocks) {
		case 0:
			checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: %q has no Proved by: line", displayPath, currentHeadingLine, currentHeading))
		case 1:
			block := currentProvedByBlocks[0]
			if block.endsWithComma {
				checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: %q Proved by: list ends with a comma", displayPath, block.lastLine, currentHeading))
			}
			if len(block.tests) == 0 {
				checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: %q Proved by: line names no tests", displayPath, block.startLine, currentHeading))
			} else if listErr == nil {
				for _, t := range block.tests {
					if !availableTests[t.name] {
						checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: %q cites test %q which does not exist in %s", displayPath, t.line, currentHeading, t.name, pkgDisplay))
					}
				}
			}
		default:
			for _, extraBlock := range currentProvedByBlocks[1:] {
				checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: %q has duplicate Proved by: line", displayPath, extraBlock.startLine, currentHeading))
			}
		}
	}

	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		line := nodeLine(n, content)

		if currentHeading == "" {
			if n.Kind() == ast.KindHeading {
				h := n.(*ast.Heading)
				pos := nodeStartOffset(n, content)
				if h.Level == 1 && isATXHeading(content, pos, 1) {
					if seenTitle {
						checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: heading", displayPath, line))
					} else {
						seenTitle = true
					}
					continue
				}
				if h.Level == 2 && isATXHeading(content, pos, 2) {
					title := extractHeadingTitle(n, content)
					if title == "" {
						checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: heading", displayPath, line))
						continue
					}
					currentHeading = title
					currentHeadingLine = line
					currentHasWhenThen = false
					currentNormativeLines = nil
					currentProvedByBlocks = nil
					seenNormative = false
					seenList = false
					seenProvedBy = false
					if _, seen := seenHeadings[title]; seen {
						checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: duplicate guarantee heading %q", displayPath, line, title))
					} else {
						seenHeadings[title] = line
					}
					continue
				}
				checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: heading", displayPath, line))
				continue
			}

			if n.Kind() == ast.KindParagraph {
				// Allowed preamble paragraph
				continue
			}

			checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: %s", displayPath, line, blockKindName(n)))
			continue
		}

		// Inside a section
		if n.Kind() == ast.KindHeading {
			h := n.(*ast.Heading)
			pos := nodeStartOffset(n, content)
			if h.Level == 2 && isATXHeading(content, pos, 2) {
				finishSection()
				title := extractHeadingTitle(n, content)
				if title == "" {
					checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: heading", displayPath, line))
					currentHeading = ""
					continue
				}
				currentHeading = title
				currentHeadingLine = line
				currentHasWhenThen = false
				currentNormativeLines = nil
				currentProvedByBlocks = nil
				seenNormative = false
				seenList = false
				seenProvedBy = false
				if _, seen := seenHeadings[title]; seen {
					checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: duplicate guarantee heading %q", displayPath, line, title))
				} else {
					seenHeadings[title] = line
				}
				continue
			}
			checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: heading", displayPath, line))
			continue
		}

		if n.Kind() == ast.KindParagraph {
			pText := extractVisibleText(n, content)
			trimmedText := strings.TrimSpace(pText)
			if strings.HasPrefix(trimmedText, "Proved by:") {
				if !seenNormative {
					checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: paragraph", displayPath, line))
					continue
				}
				if !seenProvedBy {
					block, valid := parseProvedByParagraph(n, pText, content)
					if !valid {
						checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: paragraph", displayPath, line))
					} else {
						currentProvedByBlocks = append(currentProvedByBlocks, block)
					}
					seenProvedBy = true
					continue
				}
				block, valid := parseProvedByParagraph(n, pText, content)
				if valid {
					currentProvedByBlocks = append(currentProvedByBlocks, block)
				} else {
					checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: paragraph", displayPath, line))
				}
				continue
			}

			if mustWordRegex.MatchString(pText) {
				if seenProvedBy || seenList {
					checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: paragraph", displayPath, line))
					continue
				}
				currentNormativeLines = append(currentNormativeLines, line)
				seenNormative = true
				continue
			}

			checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: paragraph", displayPath, line))
			continue
		}

		if n.Kind() == ast.KindList {
			l := n.(*ast.List)
			if l.IsOrdered() {
				checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: ordered list", displayPath, line))
				continue
			}
			if l.Marker != '-' {
				checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: list", displayPath, line))
				continue
			}
			if !seenNormative || seenList || seenProvedBy {
				checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: list", displayPath, line))
				continue
			}

			seenList = true
			for c := l.FirstChild(); c != nil; c = c.NextSibling() {
				itemLine := nodeLine(c, content)
				childCount := 0
				var invalidChild ast.Node
				for ch := c.FirstChild(); ch != nil; ch = ch.NextSibling() {
					childCount++
					if childCount == 1 {
						if ch.Kind() != ast.KindParagraph && ch.Kind() != ast.KindTextBlock {
							invalidChild = ch
							checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: %s", displayPath, nodeLine(ch, content), blockKindName(ch)))
						}
					} else {
						if invalidChild == nil {
							invalidChild = ch
						}
						checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: %s", displayPath, nodeLine(ch, content), blockKindName(ch)))
					}
				}
				if childCount == 0 {
					checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: list item", displayPath, itemLine))
					continue
				}
				if invalidChild != nil {
					continue
				}

				itemText := extractVisibleText(c, content)
				if strings.Contains(itemText, "Proved by:") {
					checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: list item", displayPath, itemLine))
					continue
				}
				trimmed := strings.TrimSpace(itemText)
				if !strings.HasPrefix(trimmed, "WHEN ") && trimmed != "WHEN" {
					checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: list item", displayPath, itemLine))
					continue
				}
				if thenWordRegex.MatchString(itemText) {
					currentHasWhenThen = true
				}
			}
			continue
		}

		checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: %s", displayPath, line, blockKindName(n)))
	}

	finishSection()

	if len(seenHeadings) == 0 {
		checkErrors = append(checkErrors, fmt.Sprintf("%s:1: no guarantee sections (## headings) found", displayPath))
	}

	return checkErrors
}

// extractHeadingTitle extracts, entity-unescapes, and trims the heading text.
func extractHeadingTitle(n ast.Node, src []byte) string {
	return strings.TrimSpace(extractVisibleText(n, src))
}

// parseProvedByParagraph parses tests and comma structure from a Proved by: paragraph.
func parseProvedByParagraph(n ast.Node, pText string, src []byte) (provedByBlock, bool) {
	startLine := nodeLine(n, src)
	idx := strings.Index(pText, "Proved by:")
	if idx < 0 {
		return provedByBlock{}, false
	}
	body := pText[idx+len("Proved by:"):]
	trimmedBody := strings.TrimRight(body, " \t\r\n")
	if strings.TrimSpace(body) == "" {
		return provedByBlock{
			startLine:     startLine,
			lastLine:      startLine,
			endsWithComma: false,
			tests:         nil,
		}, true
	}
	endsWithComma := strings.HasSuffix(trimmedBody, ",")

	lastLine := startLine
	if n.Lines() != nil && n.Lines().Len() > 0 {
		lastSeg := n.Lines().At(n.Lines().Len() - 1)
		lastLine = 1 + bytes.Count(src[:lastSeg.Start], []byte{'\n'})
	}

	rawParts := strings.Split(body, ",")
	if endsWithComma && len(rawParts) > 0 {
		rawParts = rawParts[:len(rawParts)-1]
	}

	var tests []provedByTest
	for _, raw := range rawParts {
		name := strings.Trim(raw, " \t\r\n`")
		if name == "" {
			return provedByBlock{}, false
		}
		if !isGoIdentifier(name) {
			return provedByBlock{}, false
		}
		testLine := findWordLineInParagraph(src, n, name)
		tests = append(tests, provedByTest{name: name, line: testLine})
	}

	return provedByBlock{
		startLine:     startLine,
		lastLine:      lastLine,
		endsWithComma: endsWithComma,
		tests:         tests,
	}, true
}

func findWordLineInParagraph(src []byte, n ast.Node, word string) int {
	if n.Lines() != nil && n.Lines().Len() > 0 {
		wordBytes := []byte(word)
		for i := 0; i < n.Lines().Len(); i++ {
			seg := n.Lines().At(i)
			segBytes := src[seg.Start:seg.Stop]
			if containsWholeIdentifier(segBytes, wordBytes) {
				return 1 + bytes.Count(src[:seg.Start], []byte{'\n'})
			}
		}
	}
	return nodeLine(n, src)
}

func containsWholeIdentifier(seg, word []byte) bool {
	wLen := len(word)
	if wLen == 0 || len(seg) < wLen {
		return false
	}
	for i := 0; i+wLen <= len(seg); i++ {
		if bytes.Equal(seg[i:i+wLen], word) {
			beforeOk := (i == 0 || !isIdentByte(seg[i-1]))
			afterOk := (i+wLen == len(seg) || !isIdentByte(seg[i+wLen]))
			if beforeOk && afterOk {
				return true
			}
		}
	}
	return false
}

func isIdentByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

func isGoIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return false
			}
		} else {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
				return false
			}
		}
	}
	return true
}

func isATXHeading(src []byte, pos int, expectedLevel int) bool {
	if pos < 0 || pos >= len(src) {
		return false
	}
	lineStart := pos
	for lineStart > 0 && src[lineStart-1] != '\n' {
		lineStart--
	}
	line := src[lineStart:]
	if idx := bytes.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	spaces := 0
	for spaces < len(line) && line[spaces] == ' ' && spaces < 3 {
		spaces++
	}
	trimmed := line[spaces:]
	prefix := bytes.Repeat([]byte{'#'}, expectedLevel)
	if !bytes.HasPrefix(trimmed, prefix) {
		return false
	}
	rest := trimmed[len(prefix):]
	if len(rest) == 0 {
		return true
	}
	return rest[0] == ' ' || rest[0] == '\t'
}

func fencedCodeBlockStart(fcb *ast.FencedCodeBlock, src []byte) int {
	if fcb.Info != nil {
		return fcb.Info.Segment.Start
	}
	if fcb.Lines() != nil && fcb.Lines().Len() > 0 {
		firstLineStart := fcb.Lines().At(0).Start
		idx := firstLineStart - 1
		for idx > 0 && (src[idx] == '\n' || src[idx] == '\r') {
			idx--
		}
		for idx > 0 && src[idx-1] != '\n' {
			idx--
		}
		return idx
	}
	return -1
}

func nodeStartOffset(n ast.Node, src []byte) int {
	if n == nil {
		return -1
	}
	if fcb, ok := n.(*ast.FencedCodeBlock); ok {
		if pos := fencedCodeBlockStart(fcb, src); pos >= 0 {
			return pos
		}
	}
	if n.Type() == ast.TypeBlock && n.Lines() != nil && n.Lines().Len() > 0 {
		return n.Lines().At(0).Start
	}
	if t, ok := n.(*ast.Text); ok {
		return t.Segment.Start
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if pos := nodeStartOffset(c, src); pos >= 0 {
			return pos
		}
	}
	return -1
}

func nodeLine(n ast.Node, src []byte) int {
	pos := nodeStartOffset(n, src)
	if pos >= 0 {
		if pos > len(src) {
			pos = len(src)
		}
		return 1 + bytes.Count(src[:pos], []byte{'\n'})
	}
	searchStart := findPrecedingEndOffset(n, src)
	if searchStart < 0 {
		searchStart = 0
	}
	pos = findNextNonBlankLineOffset(src, searchStart)
	if pos < 0 {
		return 1
	}
	return 1 + bytes.Count(src[:pos], []byte{'\n'})
}

func findPrecedingEndOffset(n ast.Node, src []byte) int {
	for prev := n.PreviousSibling(); prev != nil; prev = prev.PreviousSibling() {
		if end := nodeEndOffset(prev); end >= 0 {
			return end
		}
	}
	if parent := n.Parent(); parent != nil && parent.Kind() != ast.KindDocument {
		return nodeStartOffset(parent, src)
	}
	return 0
}

func nodeEndOffset(n ast.Node) int {
	if n == nil {
		return -1
	}
	maxEnd := -1
	if n.Type() == ast.TypeBlock && n.Lines() != nil && n.Lines().Len() > 0 {
		maxEnd = n.Lines().At(n.Lines().Len() - 1).Stop
	}
	if fcb, ok := n.(*ast.FencedCodeBlock); ok {
		if fcb.Info != nil && fcb.Info.Segment.Stop > maxEnd {
			maxEnd = fcb.Info.Segment.Stop
		}
	}
	if t, ok := n.(*ast.Text); ok {
		if t.Segment.Stop > maxEnd {
			maxEnd = t.Segment.Stop
		}
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if cEnd := nodeEndOffset(c); cEnd > maxEnd {
			maxEnd = cEnd
		}
	}
	return maxEnd
}

func findNextNonBlankLineOffset(src []byte, start int) int {
	if start < 0 {
		start = 0
	}
	if start >= len(src) {
		return -1
	}
	idx := start
	for idx < len(src) {
		lineEnd := bytes.IndexByte(src[idx:], '\n')
		var line []byte
		if lineEnd < 0 {
			line = src[idx:]
		} else {
			line = src[idx : idx+lineEnd]
		}
		if len(bytes.TrimSpace(line)) > 0 {
			return idx
		}
		if lineEnd < 0 {
			break
		}
		idx += lineEnd + 1
	}
	return -1
}

func blockKindName(n ast.Node) string {
	switch n.Kind() {
	case ast.KindCodeBlock:
		return "code block"
	case ast.KindFencedCodeBlock:
		return "fenced code block"
	case ast.KindThematicBreak:
		return "thematic break"
	case ast.KindBlockquote:
		return "blockquote"
	case ast.KindHTMLBlock:
		return "html block"
	case ast.KindHeading:
		return "heading"
	case ast.KindList:
		if l, ok := n.(*ast.List); ok && l.IsOrdered() {
			return "ordered list"
		}
		return "list"
	case ast.KindParagraph:
		return "paragraph"
	default:
		return strings.ToLower(n.Kind().String())
	}
}

func extractVisibleText(n ast.Node, src []byte) string {
	var b strings.Builder
	walkVisibleText(n, src, &b)
	return b.String()
}

func walkVisibleText(n ast.Node, src []byte, b *strings.Builder) {
	if n == nil {
		return
	}
	switch n.Kind() {
	case ast.KindRawHTML, ast.KindHTMLBlock:
		// Ignore raw HTML nodes.
		return
	case ast.KindImage:
		// Skip image subtrees so alt text is not counted.
		return
	case ast.KindAutoLink:
		al := n.(*ast.AutoLink)
		b.Write(al.Label(src))
		return
	case ast.KindText:
		t := n.(*ast.Text)
		val := t.Segment.Value(src)
		if !t.IsRaw() {
			val = util.UnescapePunctuations(val)
			val = util.ResolveNumericReferences(val)
			val = util.ResolveEntityNames(val)
		}
		b.Write(val)
		if t.SoftLineBreak() || t.HardLineBreak() {
			b.WriteByte('\n')
		}
		return
	case ast.KindString:
		s := n.(*ast.String)
		val := s.Value
		if !s.IsRaw() && !s.IsCode() {
			val = util.UnescapePunctuations(val)
			val = util.ResolveNumericReferences(val)
			val = util.ResolveEntityNames(val)
		}
		b.Write(val)
		return
	case ast.KindCodeSpan:
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			walkVisibleText(c, src, b)
		}
		return
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		walkVisibleText(c, src, b)
	}
}

var readFile = os.ReadFile

func findTestFunctions(pkgDir string) (map[string]bool, error) {
	fi, err := os.Stat(pkgDir)
	if err != nil || !fi.IsDir() {
		return nil, errs.New().ExitCode(1).Msg("package directory does not exist")
	}

	cmd := exec.Command("go", "list", "-mod=readonly", "-json", ".")
	cmd.Dir = pkgDir
	cmd.Env = append(os.Environ(), "GOWORK=off")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		firstLine := ""
		for _, line := range strings.Split(stderr.String(), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				firstLine = trimmed
				break
			}
		}
		if firstLine == "" {
			firstLine = fmt.Sprintf("go list exited %v", err)
		}
		return nil, errs.New().ExitCode(1).Msg(firstLine)
	}

	var pkgData struct {
		TestGoFiles  []string `json:"TestGoFiles"`
		XTestGoFiles []string `json:"XTestGoFiles"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &pkgData); err != nil {
		return nil, errs.New().ExitCode(1).Msgf("invalid go list JSON: %v", err)
	}

	testFilenames := make([]string, 0, len(pkgData.TestGoFiles)+len(pkgData.XTestGoFiles))
	testFilenames = append(testFilenames, pkgData.TestGoFiles...)
	testFilenames = append(testFilenames, pkgData.XTestGoFiles...)
	availableTests := make(map[string]bool)

	for _, fname := range testFilenames {
		testFilePath := filepath.Join(pkgDir, fname)
		content, err := readFile(testFilePath)
		if err != nil {
			return nil, errs.Wrap(err, "read test file failed")
		}
		funcs, err := scanTestFile(content, fname)
		if err != nil {
			return nil, err
		}
		for f := range funcs {
			availableTests[f] = true
		}
	}

	return availableTests, nil
}

type tokenItem struct {
	pos token.Pos
	tok token.Token
	lit string
}

func scanTestFile(content []byte, filename string) (map[string]bool, error) {
	fset := token.NewFileSet()
	file := fset.AddFile(filename, fset.Base(), len(content))
	var s scanner.Scanner
	s.Init(file, content, nil, 0)

	var tokens []tokenItem
	for {
		pos, tok, lit := s.Scan()
		tokens = append(tokens, tokenItem{pos: pos, tok: tok, lit: lit})
		if tok == token.EOF {
			break
		}
	}

	testFuncs := make(map[string]bool)
	n := len(tokens)
	braceDepth := 0
	i := 0

	for i < n {
		t := tokens[i]
		if t.tok == token.LBRACE {
			braceDepth++
			i++
			continue
		}
		if t.tok == token.RBRACE {
			if braceDepth > 0 {
				braceDepth--
			}
			i++
			continue
		}
		if braceDepth == 0 && t.tok == token.FUNC {
			funcPos := t.pos
			funcLine := file.Position(funcPos).Line
			i++
			if i >= n {
				break
			}

			isMethod := false
			if tokens[i].tok == token.LPAREN {
				isMethod = true
				parenDepth := 1
				i++
				for i < n && parenDepth > 0 {
					switch tokens[i].tok {
					case token.LPAREN:
						parenDepth++
					case token.RPAREN:
						parenDepth--
					}
					i++
				}
				if i >= n {
					break
				}
			}

			if isMethod {
				continue
			}

			if tokens[i].tok != token.IDENT {
				continue
			}
			name := tokens[i].lit
			i++
			if i >= n {
				break
			}

			if !strings.HasPrefix(name, "Test") {
				continue
			}
			if len(name) > 4 {
				r, _ := utf8.DecodeRuneInString(name[4:])
				if unicode.IsLower(r) {
					continue
				}
			}

			if tokens[i].tok == token.LBRACK {
				return nil, &signatureError{file: filename, line: funcLine, name: name}
			}
			if tokens[i].tok != token.LPAREN {
				return nil, &signatureError{file: filename, line: funcLine, name: name}
			}

			i++
			paramStart := i
			parenDepth := 1
			for i < n && parenDepth > 0 {
				switch tokens[i].tok {
				case token.LPAREN:
					parenDepth++
				case token.RPAREN:
					parenDepth--
				}
				i++
			}
			if parenDepth != 0 {
				break
			}
			paramEnd := i - 1
			paramTokens := tokens[paramStart:paramEnd]

			hasReturn := false
			bodyIdx := i
			if bodyIdx+1 < n && tokens[bodyIdx].tok == token.LPAREN && tokens[bodyIdx+1].tok == token.RPAREN {
				bodyIdx += 2
			}
			if bodyIdx < n && tokens[bodyIdx].tok != token.LBRACE && tokens[bodyIdx].tok != token.SEMICOLON && tokens[bodyIdx].tok != token.EOF {
				hasReturn = true
			}

			validParam := isValidTestParamTokens(paramTokens)

			if validParam && !hasReturn {
				testFuncs[name] = true
				continue
			}

			if name == "TestMain" {
				if isValidTestMainParamTokens(paramTokens) && !hasReturn {
					continue
				}
			}

			return nil, &signatureError{file: filename, line: funcLine, name: name}
		}
		i++
	}

	return testFuncs, nil
}

func isValidTestParamTokens(tokens []tokenItem) bool {
	for len(tokens) > 0 && tokens[len(tokens)-1].tok == token.SEMICOLON {
		tokens = tokens[:len(tokens)-1]
	}
	if len(tokens) > 0 && tokens[len(tokens)-1].tok == token.COMMA {
		tokens = tokens[:len(tokens)-1]
	}
	if len(tokens) == 0 {
		return false
	}
	if len(tokens) > 1 && tokens[0].tok == token.IDENT && tokens[1].tok == token.MUL {
		tokens = tokens[1:]
	}
	if len(tokens) == 2 && tokens[0].tok == token.MUL && tokens[1].tok == token.IDENT && tokens[1].lit == "T" {
		return true
	}
	if len(tokens) == 4 && tokens[0].tok == token.MUL && tokens[1].tok == token.IDENT && tokens[2].tok == token.PERIOD && tokens[3].tok == token.IDENT && tokens[3].lit == "T" {
		return true
	}
	return false
}

func isValidTestMainParamTokens(tokens []tokenItem) bool {
	if len(tokens) > 0 && tokens[len(tokens)-1].tok == token.COMMA {
		tokens = tokens[:len(tokens)-1]
	}
	if len(tokens) == 0 {
		return false
	}
	if len(tokens) > 1 && tokens[0].tok == token.IDENT && tokens[1].tok == token.MUL {
		tokens = tokens[1:]
	}
	if len(tokens) == 2 && tokens[0].tok == token.MUL && tokens[1].tok == token.IDENT && tokens[1].lit == "M" {
		return true
	}
	if len(tokens) == 4 && tokens[0].tok == token.MUL && tokens[1].tok == token.IDENT && tokens[2].tok == token.PERIOD && tokens[3].tok == token.IDENT && tokens[3].lit == "M" {
		return true
	}
	return false
}

func uniqueStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]bool)
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
