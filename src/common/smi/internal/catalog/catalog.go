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
		Code:        "smi/dialect-value-mismatch",
		Tag:         "DialectValueMismatch",
		Format:      "%s value %q belongs to %s, and this module is read as %s",
		Arity:       4,
		Description: "a clause value only one of the two SMI dialects defines, written in a module read as the other one, which is graded and kept because the value still says what its author meant",
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
		Severity:    3,
		Code:        "smi/underscore-in-descriptor",
		Tag:         "UnderscoreInDescriptor",
		Format:      "descriptor %q holds an underscore, which RFC 2578 does not permit",
		Arity:       1,
		Description: "an underscore inside a descriptor, which the RFC leaves out of the character set and which the corpus writes anyway, read as one name because ending the name at the underscore costs the declaration every member after it",
	},
	{
		Severity:    3,
		Code:        "smi/curly-quoted-string",
		Tag:         "CurlyQuotedString",
		Format:      "string is delimited by Windows-1252 curly quotes rather than quotation marks",
		Description: "a string a word processor re-quoted into Windows-1252, read as a string because a file quoted that way throughout has no readable declaration left otherwise",
	},
	{
		Severity:    3,
		Code:        "smi/binary-string-not-octets",
		Tag:         "BinaryStringNotOctets",
		Format:      "binary string has %d digits, which is not a whole number of octets",
		Arity:       1,
		Description: "a binary string literal whose digit count is not a multiple of eight, so its last octet is short of bits",
	},
	{
		Severity:    2,
		Code:        "smi/default-not-permitted",
		Tag:         "DefaultNotPermitted",
		Format:      "DEFVAL is not permitted on a %s object",
		Arity:       1,
		Description: "a default value on a type RFC 2578 gives no default form for, such as a counter whose value only ever means a difference",
	},
	{
		Severity:    3,
		Code:        "smi/non-conforming-oid-default",
		Tag:         "NonConformingOIDDefault",
		Format:      "OBJECT IDENTIFIER default is written as a sub-identifier list rather than a descriptor",
		Description: "an OID default spelled as bare sub-identifiers, a form the RFC does not define but whose meaning is plain enough to keep",
	},
	{
		Severity:    2,
		Code:        "smi/negative-size",
		Tag:         "NegativeSize",
		Format:      "SIZE bound %d is negative, and a length cannot be",
		Arity:       1,
		Description: "a negative bound in a SIZE constraint, which describes no string this or any other implementation can hold",
	},
	{
		Severity:    2,
		Code:        "smi/range-not-ascending",
		Tag:         "RangeNotAscending",
		Format:      "range lower bound %d is above upper bound %d",
		Arity:       2,
		Description: "a range written backwards, which names no value and whose author's intent cannot be recovered from the two bounds alone",
	},
	{
		Severity:    3,
		Code:        "smi/overlapping-range",
		Tag:         "OverlappingRange",
		Format:      "range %d..%d overlaps the preceding %d..%d",
		Arity:       4,
		Description: "two range alternatives covering a value twice, which RFC 2578 forbids and whose union is still the set the author described",
	},
	{
		Severity:    2,
		Code:        "smi/range-outside-base-type",
		Tag:         "RangeOutsideBaseType",
		Format:      "range %s lies outside the values %s permits",
		Arity:       2,
		Description: "a subtype bound the base type could never take, which leaves the constraint describing values the object cannot carry",
	},
	{
		Severity:    2,
		Code:        "smi/display-hint-not-permitted",
		Tag:         "DisplayHintNotPermitted",
		Format:      "DISPLAY-HINT is not permitted on a %s syntax",
		Arity:       1,
		Description: "a display hint on a syntax RFC 2579 gives no hint forms for, where nothing could act on the hint",
	},
	{
		Severity:    2,
		Code:        "smi/display-hint-malformed",
		Tag:         "DisplayHintMalformed",
		Format:      "display hint %q is malformed",
		Arity:       1,
		Description: "a display hint that does not read as either of RFC 2579's two forms, which leaves nothing to render a value with",
	},
	{
		Severity:    2,
		Code:        "smi/display-hint-separator",
		Tag:         "DisplayHintSeparator",
		Format:      "display hint separator %q must not be a decimal digit or '*'",
		Arity:       1,
		Description: "a display-hint separator that cannot be told apart from the octet count or repeat indicator of the specification after it",
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
	{
		Severity:    3,
		Code:        "smi/import-cycle",
		Tag:         "ImportCycle",
		Format:      "IMPORTS of %s closes a cycle back through %s, and the edge is dropped from the load order",
		Arity:       2,
		Description: "an IMPORTS cycle, which is broken at the back edge rather than costing either module because both of them still define everything they define",
	},
	{
		Severity:    3,
		Code:        "smi/missing-import",
		Tag:         "MissingImport",
		Format:      "%s is used without being imported, and resolves to the definition %s carries",
		Arity:       2,
		Description: "a symbol used with no IMPORTS clause naming it, which resolves anyway because refusing a spelling the corpus writes by the thousand would cost far more than it reports",
	},
	{
		Severity:    2,
		Code:        "smi/module-not-found",
		Tag:         "ModuleNotFound",
		Format:      "imported module %s was not found on the search path",
		Arity:       1,
		Description: "an IMPORTS clause naming a module no search path holds, which leaves the symbols it was to supply to the fallback lookup or to nothing at all",
	},
	{
		Severity:    2,
		Code:        "smi/unresolved-declaration",
		Tag:         "UnresolvedDeclaration",
		Format:      "%s stays unresolved because %s does not resolve",
		Arity:       2,
		Description: "a declaration one of whose references did not resolve, reported once on the declaration so that a single missing name costs one diagnostic per dependent rather than one per site",
	},
	{
		Severity:    2,
		Code:        "smi/enumeration-refinement-conflict",
		Tag:         "EnumerationRefinementConflict",
		Format:      "%s(%d) is not a named number of %s, which this SYNTAX may only restrict",
		Arity:       3,
		Description: "a SYNTAX clause that renames or renumbers the enumeration it refines instead of narrowing it, which leaves two contradictory readings of the same wire value",
	},
	{
		Severity:    2,
		Code:        "smi/duplicate-oid",
		Tag:         "DuplicateOID",
		Format:      "%s has no place in the tree: %s is already registered at %s",
		Arity:       3,
		Description: "two descriptors registered at one OBJECT IDENTIFIER value, where only one of them can be the node a walk arrives at",
	},
	{
		Severity:    5,
		Code:        "smi/duplicate-module",
		Tag:         "DuplicateModule",
		Format:      "module %s is also defined in %s, and the file read first keeps the name",
		Arity:       2,
		Description: "one module name defined by two files, where the first in canonical order wins because a module name has to mean one thing",
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
