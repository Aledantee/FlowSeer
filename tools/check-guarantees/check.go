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
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
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

type unreadableTestFileError struct {
	file string
	err  error
}

func (e *unreadableTestFileError) Error() string {
	return fmt.Sprintf("cannot read %s: %v", e.file, e.err)
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
		var unreadErr *unreadableTestFileError
		switch {
		case errors.As(listErr, &sigErr):
			relTestFile := filepath.Join(pkgDisplay, sigErr.file)
			checkErrors = append(checkErrors, fmt.Sprintf("%s:1: %s:%d: %s has invalid test signature", displayPath, relTestFile, sigErr.line, sigErr.name))
		case errors.As(listErr, &unreadErr):
			relTestFile := filepath.Join(pkgDisplay, unreadErr.file)
			checkErrors = append(checkErrors, fmt.Sprintf("%s:1: cannot read %s: %v", displayPath, relTestFile, unreadErr.err))
		default:
			checkErrors = append(checkErrors, fmt.Sprintf("%s:1: go list failed in %s: %s", displayPath, pkgDisplay, listErr.Error()))
		}
	}

	doc := goldmark.DefaultParser().Parse(text.NewReader(content))

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
				pos := n.Pos()
				if h.Level == 1 && isATXHeading(content, pos, 1) {
					if seenTitle {
						checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: heading", displayPath, line))
					} else {
						seenTitle = true
						_, inlineErrs := extractInlines(n, content, displayPath)
						checkErrors = append(checkErrors, inlineErrs...)
					}
					continue
				}
				if h.Level == 2 && isATXHeading(content, pos, 2) {
					title, inlineErrs := extractHeadingTitle(n, content, displayPath)
					checkErrors = append(checkErrors, inlineErrs...)
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
				checkErrors = append(checkErrors, rejectHTMLText(n, content, displayPath)...)
				continue
			}

			checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: %s", displayPath, line, blockKindName(n)))
			continue
		}

		if n.Kind() == ast.KindHeading {
			h := n.(*ast.Heading)
			pos := n.Pos()
			if h.Level == 2 && isATXHeading(content, pos, 2) {
				finishSection()
				title, inlineErrs := extractHeadingTitle(n, content, displayPath)
				checkErrors = append(checkErrors, inlineErrs...)
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
			visible, inlineErrs := extractInlinesWithOffsets(n, content, displayPath)
			checkErrors = append(checkErrors, inlineErrs...)
			pText := string(visible.value)
			trimmedText := strings.TrimSpace(pText)
			if strings.HasPrefix(trimmedText, "Proved by:") {
				if !seenNormative {
					checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: paragraph", displayPath, line))
					continue
				}
				if !seenProvedBy {
					block, valid := parseProvedByParagraph(n, visible, content)
					if !valid {
						checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: paragraph", displayPath, line))
					} else {
						currentProvedByBlocks = append(currentProvedByBlocks, block)
					}
					seenProvedBy = true
					continue
				}
				block, valid := parseProvedByParagraph(n, visible, content)
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

			if len(inlineErrs) == 0 {
				checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: paragraph", displayPath, line))
			}
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
				var itemContentNode ast.Node
				for ch := c.FirstChild(); ch != nil; ch = ch.NextSibling() {
					childCount++
					if childCount == 1 {
						if ch.Kind() != ast.KindParagraph && ch.Kind() != ast.KindTextBlock {
							invalidChild = ch
							checkErrors = append(checkErrors, fmt.Sprintf("%s:%d: unknown block kind: %s", displayPath, nodeLine(ch, content), blockKindName(ch)))
						} else {
							itemContentNode = ch
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

				itemText, inlineErrs := extractInlines(itemContentNode, content, displayPath)
				checkErrors = append(checkErrors, inlineErrs...)
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
func extractHeadingTitle(n ast.Node, src []byte, displayPath string) (string, []string) {
	text, errs := extractInlines(n, src, displayPath)
	return strings.TrimSpace(text), errs
}

// parseProvedByParagraph parses tests and comma structure from a Proved by: paragraph.
func parseProvedByParagraph(n ast.Node, visible inlineText, src []byte) (provedByBlock, bool) {
	startLine := nodeLine(n, src)
	pText := string(visible.value)
	idx := strings.Index(pText, "Proved by:")
	if idx < 0 {
		return provedByBlock{}, false
	}
	bodyStart := idx + len("Proved by:")
	body := pText[bodyStart:]
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
	partStart := bodyStart
	for _, raw := range rawParts {
		name := strings.Trim(raw, " \t\r\n`")
		if name == "" {
			return provedByBlock{}, false
		}
		if !isGoIdentifier(name) {
			return provedByBlock{}, false
		}

		leading := len(raw) - len(strings.TrimLeft(raw, " \t\r\n`"))
		offset := visible.offsets[partStart+leading]
		testLine := 1 + bytes.Count(src[:offset], []byte{'\n'})
		tests = append(tests, provedByTest{name: name, line: testLine})
		partStart += len(raw) + 1
	}

	return provedByBlock{
		startLine:     startLine,
		lastLine:      lastLine,
		endsWithComma: endsWithComma,
		tests:         tests,
	}, true
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

func nodeLine(n ast.Node, src []byte) int {
	if n == nil {
		return 1
	}
	pos := n.Pos()
	if pos < 0 {
		if n.Type() == ast.TypeBlock && n.Lines() != nil && n.Lines().Len() > 0 {
			pos = n.Lines().At(0).Start
		} else if n.FirstChild() != nil {
			pos = n.FirstChild().Pos()
		}
		if pos < 0 && n.Parent() != nil {
			return nodeLine(n.Parent(), src)
		}
	}
	if pos < 0 {
		return 1
	}
	if pos > len(src) {
		pos = len(src)
	}
	return 1 + bytes.Count(src[:pos], []byte{'\n'})
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

type inlineText struct {
	value   []byte
	offsets []int
}

func (v *inlineText) appendByte(b byte, offset int) {
	if b == 0 {
		v.appendBytes([]byte(string(utf8.RuneError)), offset)
		return
	}
	v.value = append(v.value, b)
	v.offsets = append(v.offsets, offset)
}

func (v *inlineText) appendBytes(b []byte, offset int) {
	for _, c := range b {
		v.appendByte(c, offset)
	}
}

func (v *inlineText) appendSource(b []byte, offset int) {
	for i, c := range b {
		v.appendByte(c, offset+i)
	}
}

func extractInlines(n ast.Node, src []byte, displayPath string) (string, []string) {
	visible, errs := extractInlinesWithOffsets(n, src, displayPath)
	return string(visible.value), errs
}

func extractInlinesWithOffsets(n ast.Node, src []byte, displayPath string) (inlineText, []string) {
	var visible inlineText
	var errs []string
	if n != nil {
		walkInlines(n, src, displayPath, &visible, &errs)
	}
	return visible, errs
}

func rejectHTMLText(n ast.Node, src []byte, displayPath string) []string {
	var errs []string
	var inspect func(ast.Node)
	inspect = func(parent ast.Node) {
		for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
			switch c.Kind() {
			case ast.KindText:
				t := c.(*ast.Text)
				if !t.IsRaw() && hasHTMLStart(t.Segment.Value(src), t.Segment.Start, src) {
					errs = append(errs, fmt.Sprintf("%s:%d: unknown inline kind: raw html", displayPath, nodeLine(c, src)))
				}
			case ast.KindEmphasis:
				inspect(c)
			}
		}
	}
	inspect(n)
	return errs
}

func hasHTMLStart(source []byte, start int, full []byte) bool {
	for i := 0; i < len(source); i++ {
		if source[i] == '\\' && i+1 < len(source) && isASCIIPunct(source[i+1]) {
			i++
			continue
		}
		if source[i] == '<' && start+i+1 < len(full) {
			next := full[start+i+1]
			if next >= 'A' && next <= 'Z' || next >= 'a' && next <= 'z' || next == '/' || next == '!' || next == '?' {
				return true
			}
		}
	}
	return false
}

func walkInlines(parent ast.Node, src []byte, displayPath string, visible *inlineText, errs *[]string) {
	for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
		switch c.Kind() {
		case ast.KindText:
			t := c.(*ast.Text)
			val := bytes.TrimSuffix(t.Segment.Value(src), []byte("\r"))
			if hasHTMLStart(val, t.Segment.Start, src) {
				*errs = append(*errs, fmt.Sprintf("%s:%d: unknown inline kind: raw html", displayPath, nodeLine(c, src)))
			}
			decodeTextInlines(val, t.Segment.Start, visible)
			if t.HardLineBreak() || t.SoftLineBreak() {
				visible.appendByte('\n', t.Segment.Stop)
			}

		case ast.KindString:
			s := c.(*ast.String)
			offset := 0
			if c.Parent() != nil && c.Parent().Pos() >= 0 {
				offset = c.Parent().Pos()
			}
			visible.appendBytes(s.Value, offset)

		case ast.KindCodeSpan:
			for ch := c.FirstChild(); ch != nil; ch = ch.NextSibling() {
				t, ok := ch.(*ast.Text)
				if !ok {
					*errs = append(*errs, fmt.Sprintf("%s:%d: unknown inline kind: %s", displayPath, nodeLine(ch, src), inlineKindName(ch)))
					continue
				}
				val := t.Segment.Value(src)
				switch {
				case bytes.HasSuffix(val, []byte("\r\n")):
					visible.appendSource(val[:len(val)-2], t.Segment.Start)
					visible.appendByte(' ', t.Segment.Stop-1)
				case bytes.HasSuffix(val, []byte("\n")):
					visible.appendSource(val[:len(val)-1], t.Segment.Start)
					visible.appendByte(' ', t.Segment.Stop-1)
				default:
					visible.appendSource(val, t.Segment.Start)
				}
			}

		case ast.KindEmphasis:
			walkInlines(c, src, displayPath, visible, errs)

		default:
			*errs = append(*errs, fmt.Sprintf("%s:%d: unknown inline kind: %s", displayPath, nodeLine(c, src), inlineKindName(c)))
		}
	}
}

func decodeTextInlines(source []byte, sourceOffset int, visible *inlineText) {
	limit := len(source)
	for i := 0; i < limit; i++ {
		b := source[i]
		if b == '\\' {
			if i+1 < limit {
				next := source[i+1]
				if isASCIIPunct(next) {
					visible.appendByte(next, sourceOffset+i)
					i++
					continue
				}
			}
			visible.appendByte(b, sourceOffset+i)
			continue
		}
		if b == '&' {
			if i+1 < limit && source[i+1] == '#' {
				if i+2 < limit {
					nc := source[i+2]
					if (nc == 'x' || nc == 'X') && i+3 < limit {
						start := i + 3
						end := start
						for end < limit && isHexDigit(source[end]) {
							end++
						}
						hexDigits := end - start
						if hexDigits >= 1 && hexDigits <= 6 && end < limit && source[end] == ';' {
							v, err := strconv.ParseUint(string(source[start:end]), 16, 32)
							if err == nil {
								r := rune(v)
								if r == 0 || !utf8.ValidRune(r) {
									r = utf8.RuneError
								}
								visible.appendBytes([]byte(string(r)), sourceOffset+i)
								i = end
								continue
							}
						}
					} else if nc >= '0' && nc <= '9' {
						start := i + 2
						end := start
						for end < limit && source[end] >= '0' && source[end] <= '9' {
							end++
						}
						digits := end - start
						if digits >= 1 && digits <= 7 && end < limit && source[end] == ';' {
							v, err := strconv.ParseUint(string(source[start:end]), 10, 32)
							if err == nil {
								r := rune(v)
								if r == 0 || !utf8.ValidRune(r) {
									r = utf8.RuneError
								}
								visible.appendBytes([]byte(string(r)), sourceOffset+i)
								i = end
								continue
							}
						}
					}
				}
			} else {
				start := i + 1
				end := start
				for end < limit && isAlphaNumeric(source[end]) {
					end++
				}
				if end > start && end < limit && source[end] == ';' {
					name := string(source[start:end])
					if entity, ok := util.LookUpHTML5EntityByName(name); ok {
						visible.appendBytes(entity.Characters, sourceOffset+i)
						i = end
						continue
					}
				}
			}
		}
		visible.appendByte(b, sourceOffset+i)
	}
}

func isASCIIPunct(b byte) bool {
	return (b >= '!' && b <= '/') || (b >= ':' && b <= '@') || (b >= '[' && b <= '`') || (b >= '{' && b <= '~')
}

func isHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func isAlphaNumeric(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func inlineKindName(n ast.Node) string {
	switch n.Kind() {
	case ast.KindRawHTML:
		return "raw html"
	case ast.KindImage:
		return "image"
	case ast.KindLink:
		return "link"
	case ast.KindAutoLink:
		return "autolink"
	default:
		return strings.ToLower(n.Kind().String())
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
			return nil, &unreadableTestFileError{file: fname, err: err}
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
