// Package catalog holds the SMI diagnostic table: one row per condition
// the parser can report, carrying the severity it is graded at, the
// stable code that identifies it, the Go identifier its generated
// constant takes, the message format, and the prose a coverage matrix
// renders.
//
// The table is the single source of truth. The exported code variables in
// package smi are generated from it, which is why a row carries its code
// as a plain string here: the literal that reaches errs.NewCode has to
// sit in ordinary source so the repo-wide uniqueness scan can read it,
// and having it in two places would let the two drift.
//
// The package is internal because nothing outside the parser should
// depend on the table's shape. Callers match diagnostics by code.
package catalog

import (
	"fmt"
	"slices"
	"strings"
)

// MaxArgs is the number of format arguments a row may declare. A
// diagnostic stores its arguments inline in a fixed-size array so that
// raising one allocates nothing, and this is that array's length. Raise
// the bound if a row genuinely needs more; do not spill to a slice.
const MaxArgs = 4

// MaxSeverity is the least severe level on the scale, which runs 0 (most
// severe) through 6. The scale is libsmi's, so a reader who knows
// libsmi's -l flag already knows this one.
const MaxSeverity = 6

// Entry is one row of the diagnostic table.
//
// Severity is the raw level rather than a smi.Severity because package
// smi imports this one. Code is the diagnostic's identity across
// releases: append-only, never renamed, never reused for a different
// condition.
type Entry struct {
	Severity    uint8  // 0 (most severe) through MaxSeverity
	Code        string // "smi/<name>", the stable identity
	Tag         string // Go identifier suffix; the generated variable is "ErrCode" + Tag
	Format      string // fmt format, rendered only when the diagnostic is rendered
	Arity       int    // number of format arguments, 0 through MaxArgs
	Description string // phrase completing "ErrCode<Tag> marks ..."
}

// entries is the table. Rows are appended, never removed: a code that has
// shipped is a contract. The order here is editorial; everything that
// consumes the table sorts by code.
var entries = []Entry{
	{
		Severity:    1,
		Code:        "smi/unterminated-string",
		Tag:         "UnterminatedString",
		Format:      "quoted string reaches end of file with no closing quote",
		Description: "a quoted string running to end of file, one of the few conditions that costs the whole file because nothing after the opening quote can be framed",
	},
	{
		Severity:    1,
		Code:        "smi/unterminated-comment",
		Tag:         "UnterminatedComment",
		Format:      "comment reaches end of file with no closing terminator",
		Description: "a comment running to end of file, which costs the whole file because no declaration boundary after it can be trusted",
	},
	{
		Severity:    1,
		Code:        "smi/missing-module-header",
		Tag:         "MissingModuleHeader",
		Format:      "no DEFINITIONS ::= BEGIN header, so this is not a MIB source file",
		Description: "a file with no module header, which costs the whole file because there is no module to attribute a declaration to",
	},
	{
		Severity:    1,
		Code:        "smi/limit-exceeded",
		Tag:         "LimitExceeded",
		Format:      "%s limit of %d exceeded",
		Arity:       2,
		Description: "a declared resource limit being reached, which costs the whole file so that pathological input costs bounded work",
	},
	{
		Severity:    2,
		Code:        "smi/unrecognized-declaration",
		Tag:         "UnrecognizedDeclaration",
		Format:      "declaration beginning with %q is not a recognized declaration head",
		Arity:       1,
		Description: "a declaration the framer could not classify, which costs that declaration and nothing around it",
	},
	{
		Severity:    2,
		Code:        "smi/content-after-end",
		Tag:         "ContentAfterEnd",
		Format:      "content after the final END belongs to no module",
		Description: "text following the last module's END, which is dropped rather than costing the file because a trailer says nothing about the modules already read",
	},
	{
		Severity:    2,
		Code:        "smi/missing-clause",
		Tag:         "MissingClause",
		Format:      "%s is missing the required %s clause",
		Arity:       2,
		Description: "a declaration without a clause its macro requires, which leaves the declaration unresolved and visible rather than dropping it",
	},
	{
		Severity:    2,
		Code:        "smi/unknown-clause",
		Tag:         "UnknownClause",
		Format:      "%s is not a clause of %s",
		Arity:       2,
		Description: "a clause keyword belonging to some other macro, which costs the clause and leaves the rest of the declaration readable",
	},
	{
		Severity:    2,
		Code:        "smi/unexpected-token",
		Tag:         "UnexpectedToken",
		Format:      "unexpected %q where %s was expected",
		Arity:       2,
		Description: "a token the grammar has no place for, which the parser skips past to the next clause keyword inside the same declaration",
	},
	{
		Severity:    3,
		Code:        "smi/clause-out-of-order",
		Tag:         "ClauseOutOfOrder",
		Format:      "%s clause appears after %s, out of the order its macro fixes",
		Arity:       2,
		Description: "a clause written out of its macro's fixed order, whose meaning is unambiguous and which is therefore kept",
	},
	{
		Severity:    3,
		Code:        "smi/duplicate-clause",
		Tag:         "DuplicateClause",
		Format:      "%s clause appears more than once; the first one is kept",
		Arity:       1,
		Description: "a clause repeated in a macro that allows it once, where keeping the first occurrence is the only choice a reader can predict",
	},
	{
		Severity:    3,
		Code:        "smi/odd-hex-string",
		Tag:         "OddHexString",
		Format:      "hexadecimal string has %d digits, an odd count that leaves a half byte",
		Arity:       1,
		Description: "a hexadecimal string literal with an odd digit count, whose trailing nibble has no byte to land in",
	},
	{
		Severity:    3,
		Code:        "smi/trailing-hyphen-identifier",
		Tag:         "TrailingHyphenIdentifier",
		Format:      "identifier %q ends in a hyphen",
		Arity:       1,
		Description: "an identifier ending in a hyphen, which RFC 2578 forbids and which a stricter lexer reads as the start of a comment",
	},
	{
		Severity:    5,
		Code:        "smi/hyphen-separator",
		Tag:         "HyphenSeparator",
		Format:      "separator line of %d hyphens is not a well-formed comment",
		Arity:       1,
		Description: "a hyphen run whose length is 1 mod 4, which pairs off into comments and leaves one stray minus token behind",
	},
	{
		Severity:    6,
		Code:        "smi/paired-comment-mode",
		Tag:         "PairedCommentMode",
		Format:      "file re-read with paired -- comment termination, which resolved %d condition(s) the end-of-line rule reported",
		Arity:       1,
		Description: "a file that only reads cleanly under paired -- comment termination, recording which rule produced the result",
	},
}

// Entries returns the table sorted by code. The result is a fresh slice,
// so a caller that sorts or filters it cannot disturb the table.
func Entries() []Entry {
	out := slices.Clone(entries)
	slices.SortFunc(out, func(a, b Entry) int { return strings.Compare(a.Code, b.Code) })

	return out
}

// Validate reports the first way rows breaks the table's invariants:
// well-formed codes in the smi namespace, codes and tags unique across
// the table, a severity on the scale, and an arity that agrees with the
// format string.
//
// It takes rows as an argument rather than reading the table directly so
// that its own tests can prove each check bites.
func Validate(rows []Entry) error {
	codes := make(map[string]struct{}, len(rows))
	tags := make(map[string]struct{}, len(rows))

	for _, r := range rows {
		if err := validateCode(r.Code); err != nil {
			return err
		}
		if _, dup := codes[r.Code]; dup {
			return fmt.Errorf("code %q appears twice", r.Code)
		}
		codes[r.Code] = struct{}{}

		if err := validateTag(r.Tag, r.Code); err != nil {
			return err
		}
		if _, dup := tags[r.Tag]; dup {
			return fmt.Errorf("tag %q appears twice", r.Tag)
		}
		tags[r.Tag] = struct{}{}

		if r.Severity > MaxSeverity {
			return fmt.Errorf("%s: severity %d is off the 0-%d scale", r.Code, r.Severity, MaxSeverity)
		}
		if r.Format == "" {
			return fmt.Errorf("%s: format is empty", r.Code)
		}
		if r.Description == "" {
			return fmt.Errorf("%s: description is empty", r.Code)
		}
		if verbs := CountVerbs(r.Format); r.Arity != verbs {
			return fmt.Errorf("%s: arity %d but format %q consumes %d", r.Code, r.Arity, r.Format, verbs)
		}
		if r.Arity > MaxArgs {
			return fmt.Errorf("%s: arity %d exceeds the %d inline argument slots", r.Code, r.Arity, MaxArgs)
		}
	}

	return nil
}

// CountVerbs returns the number of arguments format consumes. A doubled
// percent is an escape and consumes none.
//
// The count is deliberately naive: it reads a verb as a percent, any run
// of flag, width and precision bytes, then a letter. Table formats use
// plain verbs, and an argument-index verb such as %[1]d would defeat the
// arity check rather than merely miscount, so it is not accepted.
func CountVerbs(format string) int {
	count := 0

	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}

		i++
		if i < len(format) && format[i] == '%' {
			continue
		}

		for i < len(format) && !isVerbLetter(format[i]) {
			i++
		}

		count++
	}

	return count
}

func isVerbLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// validateCode enforces the namespace and the spelling errs.NewCode will
// accept, so a bad row fails the table's own test rather than panicking
// at init once the constant is generated.
func validateCode(code string) error {
	name, found := strings.CutPrefix(code, "smi/")
	if !found {
		return fmt.Errorf("code %q must live in the smi/ namespace", code)
	}
	if name == "" {
		return fmt.Errorf("code %q has an empty name", code)
	}

	for i := 0; i < len(name); i++ {
		b := name[i]
		switch {
		case b >= 'a' && b <= 'z', b >= '0' && b <= '9':
		case (b == '-' || b == '_') && i > 0:
		default:
			return fmt.Errorf("code %q may hold only lowercase letters, digits, '-' and '_'", code)
		}
	}

	return nil
}

func validateTag(tag, code string) error {
	if tag == "" {
		return fmt.Errorf("%s: tag is empty", code)
	}
	if tag[0] < 'A' || tag[0] > 'Z' {
		return fmt.Errorf("%s: tag %q must start with an uppercase letter so the generated variable is exported", code, tag)
	}

	for i := 0; i < len(tag); i++ {
		b := tag[i]
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') {
			continue
		}

		return fmt.Errorf("%s: tag %q must be a Go identifier", code, tag)
	}

	return nil
}
