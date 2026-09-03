package parse

import (
	"go.aledante.io/FlowSeer/src/protocol/smi/internal/frame"
	"go.aledante.io/FlowSeer/src/protocol/smi/internal/lex"
)

// Dialect is which SMI a module is written in.
//
// SMIv2 is the zero value because it is the current standard and the
// reading a module gets when nothing in it says otherwise. There is no
// third "unknown" state on purpose: every grading rule downstream has to
// pick one of the two anyway, and an unknown would only move that choice
// to the places least equipped to make it.
type Dialect uint8

// The dialects, named as the RFCs name them.
const (
	DialectV2 Dialect = iota
	DialectV1
)

// String returns the dialect's name as a diagnostic spells it.
func (d Dialect) String() string {
	if d == DialectV1 {
		return "SMIv1"
	}

	return "SMIv2"
}

// other returns the dialect a value belongs to when it does not belong
// to this one, which is the only other one there is.
func (d Dialect) other() Dialect {
	if d == DialectV1 {
		return DialectV2
	}

	return DialectV1
}

// requiredIn is what a declaration of kind must carry to be resolvable
// when it is read in d.
//
// Only OBJECT-TYPE differs between the dialects: RFC 1212 leaves
// DESCRIPTION optional and RFC 2578 §8 requires it. Grading an SMIv1
// object against the SMIv2 set would turn most of the pre-1996 corpus
// into bad declarations over a clause its own standard never asked for.
func requiredIn(kind DeclKind, d Dialect) ClauseSet {
	req := requiredClauses[kind]
	if d == DialectV1 && kind == DeclObjectType {
		req &^= setOf(ClauseDescription)
	}

	return req
}

// dialectModules names the definitional modules — the ones that export
// OBJECT-TYPE, the base types and the macros — rather than every module
// of an era, because importing a MIB says nothing about the SMI its
// importer is written in.
//
// The table is read in both directions. A module that imports from one
// of these is written against that SMI, and a module that *is* one of
// these is that SMI: RFC 1155 is the SMIv1 standard, so the file
// carrying its definitions cannot be anything else.
var dialectModules = map[string]Dialect{
	"RFC1155-SMI": DialectV1,
	"RFC1065-SMI": DialectV1,
	"RFC-1212":    DialectV1,
	"RFC1212":     DialectV1,
	"RFC-1215":    DialectV1,
	"RFC1215":     DialectV1,
	"SNMPv2-SMI":  DialectV2,
	"SNMPv2-TC":   DialectV2,
	"SNMPv2-CONF": DialectV2,
}

// detectDialect reads which SMI a module is written in.
//
// A definitional module's own name settles it before anything else is
// read, and it has to: those files are where the two signals below are
// least informative. RFC1155-SMI imports nothing and declares no
// OBJECT-TYPE, so a reading built on imports and clause forms alone
// would drop the SMIv1 standard itself into the SMIv2 default.
//
// Otherwise imports decide it when they point one way, because an IMPORTS list is
// a statement of intent: a module that says FROM SNMPv2-SMI is claiming
// the SMIv2 grammar whatever it then writes. When the imports point both
// ways — which the corpus does whenever a converted module still pulls
// TRAP-TYPE from RFC-1215 — or point nowhere, the clauses the module
// actually writes decide, since the grammar in the file is the only
// evidence left. Nothing here reads a declaration; the counts come from
// tokens the framer already cut, so detection runs before parsing and
// the required-clause gate knows the dialect before it grades anything.
func detectDialect(fm frame.Module, res *lex.Result) Dialect {
	if d, own := dialectModules[fm.Name]; own {
		return d
	}

	var fromV1, fromV2 bool
	for _, fr := range fm.Frames {
		if fr.Kind != frame.KindImports {
			continue
		}
		for i, t := range fr.Tokens {
			if t.Kind != lex.KindKeyword || t.Keyword != lex.KeywordFrom || i+1 >= len(fr.Tokens) {
				continue
			}
			switch dialectModules[res.Text(fr.Tokens[i+1])] {
			case DialectV1:
				fromV1 = true
			case DialectV2:
				fromV2 = true
			}
		}
	}

	switch {
	case fromV1 && !fromV2:
		return DialectV1
	case fromV2 && !fromV1:
		return DialectV2
	}

	return dialectFromClauses(fm)
}

// dialectFromClauses weighs the grammar a module writes. Every signal is
// a construct one dialect has and the other does not, so the count is of
// declarations that could not have been written under the other SMI. A
// tie, and a module that writes neither dialect's distinctive forms,
// both read as SMIv2.
func dialectFromClauses(fm frame.Module) Dialect {
	v1, v2 := 0, 0

	for _, fr := range fm.Frames {
		kind, ok := declKindOf(fr)
		if !ok {
			continue
		}

		switch kind {
		case DeclTrapType:
			v1++
		case DeclObjectType:
			// The access clause's spelling is the signal, and only inside
			// an OBJECT-TYPE: an AGENT-CAPABILITIES VARIATION writes
			// ACCESS too, and that macro is SMIv2's.
			if kw := accessKeyword(fr); kw == lex.KeywordAccess {
				v1++
			} else if kw == lex.KeywordMaxAccess {
				v2++
			}
		case DeclModuleIdentity, DeclObjectIdentity, DeclNotificationType, DeclTextualConvention,
			DeclObjectGroup, DeclNotificationGroup, DeclModuleCompliance, DeclAgentCapabilities:
			v2++
		}
	}

	if v1 > v2 {
		return DialectV1
	}

	return DialectV2
}

// accessKeyword returns which access clause an OBJECT-TYPE writes, or
// [lex.KeywordNone] when it writes neither. The first one found decides,
// because a declaration that writes both has already said whatever it is
// going to say about its dialect.
func accessKeyword(fr frame.Frame) lex.Keyword {
	for _, t := range fr.Tokens {
		if t.Kind != lex.KindKeyword {
			continue
		}
		if t.Keyword == lex.KeywordAccess || t.Keyword == lex.KeywordMaxAccess {
			return t.Keyword
		}
	}

	return lex.KeywordNone
}
