// Package parse turns framed MIB declarations into an AST.
//
// # Recovery is the product
//
// Every other property here follows from one: a declaration that cannot
// be parsed costs that declaration and nothing else. The framer has
// already cut the file into declaration-sized pieces, so a declaration
// parser's ultimate sync set is the end of its own frame. libsmi,
// net-snmp and go/parser all recover by skipping forward to a token they
// hope begins something new, with no outer bound on how far a wrong
// guess runs; here the bound is structural, and it is why a vendor MIB
// with one malformed object still yields every other object in the file.
//
// Inside that bound the discipline is go/parser's, because the failure
// modes are the same ones: contextual sync sets built from the enclosing
// macro's clause keywords, at most one diagnostic per source line so one
// wrong token cannot produce a page of noise, a typed bail-out that the
// frame boundary recovers and re-panics if it is not the sentinel, and a
// counter that forces a token to be consumed after ten attempts to sync
// at the same place.
//
// # Every clause is preserved
//
// The AST holds every clause the source carries, including the ones no
// consumer reads today. The model is meant to outlive the code generator
// that reads eleven fields of it, and a clause that was never parsed is
// a clause no later consumer can ask for.
//
// What the AST does not do is resolve. INDEX, AUGMENTS and IMPLIED are
// kept exactly as written and are deliberately left unresolved: the code
// generator never reads index structure and index decoding is generic at
// runtime. Types, DEFVALs, ranges, SIZE constraints and DISPLAY-HINTs
// are read for what they say and kept beside the source they cover, so a
// consumer gets the value and a diagnostic still quotes the file. What
// none of them do is look outside the declaration: a type name this pass
// does not define stays a name, and every rule that would need to know
// what it stands on stays quiet until resolution answers that.
//
// # Two dialects, one AST
//
// SMIv1 and SMIv2 read into the same nodes. The dialect is settled per
// module before any declaration is parsed, because it changes what a
// declaration must carry — RFC 1212 leaves an OBJECT-TYPE's DESCRIPTION
// optional and RFC 2578 requires it — and grading an SMIv1 module
// against the SMIv2 rules would turn most of the pre-1996 corpus into
// bad declarations. What differs between the dialects is then graded
// rather than rewritten: ACCESS and MAX-ACCESS stay separate fields
// because SMIv1 states a minimum and SMIv2 a maximum, a SYNTAX keeps its
// source spelling next to the SMIv2 type it is equivalent to, and a
// TRAP-TYPE carries the notification OID it denotes rather than being
// converted into a NOTIFICATION-TYPE nobody wrote.
//
// # A bad declaration is never absent
//
// A declaration whose macro's required clauses are not all present
// becomes a [BadDecl] rather than disappearing. It keeps its name, its
// span and every clause that did parse, so a resolution pass can report
// which declarations fell with it instead of leaving a hole nobody can
// name.
//
// # Offsets, not text
//
// A node carries byte offsets. Line and column come from the file's line
// table when a diagnostic is rendered, and a clause's text is
// materialized by [Result.Text] or [Result.StringValue] when somebody
// reads it. A corpus sweep parses far more DESCRIPTION clauses than it
// prints.
package parse

import (
	"slices"
	"strings"

	"go.aledante.io/FlowSeer/src/common/smi/internal/diag"
	"go.aledante.io/FlowSeer/src/common/smi/internal/frame"
	"go.aledante.io/FlowSeer/src/common/smi/internal/lex"
)

// Result is one file's parsed modules and the diagnostics raised while
// reading it, the framer's and the lexer's included, since a caller
// judging a file wants one list rather than three.
//
// Modules is empty when a resource limit cost the file. Every other
// condition leaves the declarations that parsed in place.
type Result struct {
	Name        string
	Modules     []Module
	Diagnostics []diag.Diagnostic
	Lines       *diag.LineTable

	src *lex.Result
}

// Text returns the source a span covers.
func (r *Result) Text(s Span) string {
	return string(r.src.Bytes(s.Start, s.End))
}

// StringValue returns the content of a quoted-string clause: the text
// between the quotes, with invalid UTF-8 replaced. MIB text is nominally
// ASCII and routinely is not, and rejecting a file over a Latin-1
// contact address would cost far more than a replacement character does.
func (r *Result) StringValue(s Span) string {
	b := r.src.Bytes(s.Start, s.End)
	if n := len(b); n >= 2 && b[0] == '"' {
		b = b[1:]
		if b[len(b)-1] == '"' {
			b = b[:len(b)-1]
		}
	}

	return strings.ToValidUTF8(string(b), "�")
}

// Parse reads every declaration in a framed file.
//
// It always returns a usable Result. The framer's diagnostics are
// carried forward and counted against the same per-file limit, so a file
// that was already at the bound does not get a second budget here.
func Parse(f *frame.File) *Result {
	r := &Result{
		Name:  f.Name,
		Lines: f.Source.Lines,
		src:   f.Source,
	}

	p := newParser(f.Source, f.Name)
	p.diags = slices.Clone(f.Diagnostics)

	for _, fm := range f.Modules {
		m := newModule(fm)
		m.Dialect = detectDialect(fm, f.Source)
		p.dialect = m.Dialect

		for _, fr := range fm.Frames {
			p.declaration(&m, fr)
			if p.fatal {
				break
			}
		}

		if !p.fatal {
			p.grade(&m)
		}

		r.Modules = append(r.Modules, m)
		if p.fatal {
			break
		}
	}

	r.Diagnostics = p.diags
	if p.fatal {
		r.Modules = nil
	}

	return r
}

// declaration parses one frame into m.
//
// This is the frame boundary in both senses: the scanner is reset to
// this frame's tokens, so no reader can reach past them, and the
// bail-out a limit raises is recovered here. A recovered value that is
// not the sentinel is re-panicked, because swallowing it would turn a
// parser bug into a silently missing declaration.
func (p *parser) declaration(m *Module, fr frame.Frame) {
	kind, ok := declKindOf(fr)
	if !ok {
		return
	}

	defer func() {
		rec := recover()
		if rec == nil {
			return
		}
		if _, isBailout := rec.(bailout); !isBailout {
			panic(rec)
		}
	}()

	p.toks = fr.Tokens
	p.pos = 0
	p.end = fr.Span.End
	p.depth = 0
	p.reset(kind)

	switch kind {
	case DeclBad:
	case DeclValueAssignment:
		// The head is "<descriptor> OBJECT IDENTIFIER", so the assignment
		// operator is the fourth token.
		p.pos = 3
		p.assignment()
	case DeclTypeAssignment:
		p.pos = 2
		from := p.pos
		span := p.typeSpan(0)
		p.d.syntax = p.parseType(p.toks[from:p.pos])
		p.recordIf(ClauseSyntax, span)
	case DeclTextualConvention:
		p.pos = 3
		p.clauses(kind)
	default:
		p.pos = 2
		p.clauses(kind)
		p.assignment()
	}

	p.finish(m, fr, kind)
}

// finish turns the clauses read into a node. A declaration missing any
// clause its kind requires becomes a bad declaration carrying everything
// that did parse, so a consumer sees it as unresolved rather than whole
// and a resolution pass can name what fell with it.
func (p *parser) finish(m *Module, fr frame.Frame, kind DeclKind) {
	p.gradeDeclaration()

	decl := Decl{Name: fr.Name, Span: fr.Span, Present: p.d.present}

	missing := p.d.present.Missing(kind, p.dialect)
	if kind == DeclBad || missing != 0 {
		for c := ClauseNone + 1; c < numClauses; c++ {
			if missing.Has(c) {
				p.raise(fr.Span.Start, diag.ErrCodeMissingClause,
					diag.ArgString(kind.String()), diag.ArgString(c.String()))
			}
		}

		m.Decls = append(m.Decls, Ref{Kind: DeclBad, Index: int32(len(m.Bad))})
		m.Bad = append(m.Bad, BadDecl{
			Decl:     decl,
			Intended: kind,
			Missing:  missing,
			Clauses:  slices.Clone(p.d.clauses),
		})

		return
	}

	m.Decls = append(m.Decls, Ref{Kind: kind, Index: p.project(m, decl, kind)})
}

// project appends the node to its kind's slab and returns its index.
func (p *parser) project(m *Module, decl Decl, kind DeclKind) int32 {
	d := &p.d

	switch kind {
	case DeclObjectType:
		m.ObjectTypes = append(m.ObjectTypes, ObjectType{
			Decl:        decl,
			Syntax:      d.text[ClauseSyntax],
			SyntaxType:  d.syntax,
			Units:       d.text[ClauseUnits],
			MaxAccess:   d.text[ClauseMaxAccess],
			Access:      d.text[ClauseAccess],
			Status:      d.text[ClauseStatus],
			Description: d.text[ClauseDescription],
			Reference:   d.text[ClauseReference],
			Index:       d.index,
			Augments:    d.text[ClauseAugments],
			Defval:      d.text[ClauseDefval],

			DefaultValue: d.defval,
			Assignment:   d.text[ClauseAssignment],
		})

		return int32(len(m.ObjectTypes) - 1)

	case DeclObjectIdentity:
		m.ObjectIdentities = append(m.ObjectIdentities, ObjectIdentity{
			Decl:        decl,
			Status:      d.text[ClauseStatus],
			Description: d.text[ClauseDescription],
			Reference:   d.text[ClauseReference],
			Assignment:  d.text[ClauseAssignment],
		})

		return int32(len(m.ObjectIdentities) - 1)

	case DeclModuleIdentity:
		m.ModuleIdentities = append(m.ModuleIdentities, ModuleIdentity{
			Decl:         decl,
			LastUpdated:  d.text[ClauseLastUpdated],
			Organization: d.text[ClauseOrganization],
			ContactInfo:  d.text[ClauseContactInfo],
			Description:  d.text[ClauseDescription],
			Revisions:    d.revisions,
			Assignment:   d.text[ClauseAssignment],
		})

		return int32(len(m.ModuleIdentities) - 1)

	case DeclTextualConvention:
		m.TextualConventions = append(m.TextualConventions, TextualConvention{
			Decl:        decl,
			DisplayHint: d.text[ClauseDisplayHint],
			Hint:        d.hint,
			Status:      d.text[ClauseStatus],
			Description: d.text[ClauseDescription],
			Reference:   d.text[ClauseReference],
			Syntax:      d.text[ClauseSyntax],
			SyntaxType:  d.syntax,
		})

		return int32(len(m.TextualConventions) - 1)

	case DeclNotificationType:
		m.NotificationTypes = append(m.NotificationTypes, NotificationType{
			Decl:        decl,
			Objects:     d.objects,
			Status:      d.text[ClauseStatus],
			Description: d.text[ClauseDescription],
			Reference:   d.text[ClauseReference],
			Assignment:  d.text[ClauseAssignment],
		})

		return int32(len(m.NotificationTypes) - 1)

	case DeclTrapType:
		m.TrapTypes = append(m.TrapTypes, TrapType{
			Decl:        decl,
			Enterprise:  d.text[ClauseEnterprise],
			Variables:   d.variables,
			Description: d.text[ClauseDescription],
			Reference:   d.text[ClauseReference],
			Assignment:  d.text[ClauseAssignment],
		})

		return int32(len(m.TrapTypes) - 1)

	case DeclObjectGroup:
		m.ObjectGroups = append(m.ObjectGroups, ObjectGroup{
			Decl:        decl,
			Objects:     d.objects,
			Status:      d.text[ClauseStatus],
			Description: d.text[ClauseDescription],
			Reference:   d.text[ClauseReference],
			Assignment:  d.text[ClauseAssignment],
		})

		return int32(len(m.ObjectGroups) - 1)

	case DeclNotificationGroup:
		m.NotificationGroups = append(m.NotificationGroups, NotificationGroup{
			Decl:          decl,
			Notifications: d.notifications,
			Status:        d.text[ClauseStatus],
			Description:   d.text[ClauseDescription],
			Reference:     d.text[ClauseReference],
			Assignment:    d.text[ClauseAssignment],
		})

		return int32(len(m.NotificationGroups) - 1)

	case DeclModuleCompliance:
		m.ModuleCompliances = append(m.ModuleCompliances, ModuleCompliance{
			Decl:        decl,
			Status:      d.text[ClauseStatus],
			Description: d.text[ClauseDescription],
			Reference:   d.text[ClauseReference],
			Modules:     d.modules,
			Assignment:  d.text[ClauseAssignment],
		})

		return int32(len(m.ModuleCompliances) - 1)

	case DeclAgentCapabilities:
		m.AgentCapabilities = append(m.AgentCapabilities, AgentCapabilities{
			Decl:           decl,
			ProductRelease: d.text[ClauseProductRelease],
			Status:         d.text[ClauseStatus],
			Description:    d.text[ClauseDescription],
			Reference:      d.text[ClauseReference],
			Supports:       d.supports,
			Assignment:     d.text[ClauseAssignment],
		})

		return int32(len(m.AgentCapabilities) - 1)

	case DeclValueAssignment:
		m.ValueAssignments = append(m.ValueAssignments, ValueAssignment{
			Decl:       decl,
			Assignment: d.text[ClauseAssignment],
		})

		return int32(len(m.ValueAssignments) - 1)

	default:
		m.TypeAssignments = append(m.TypeAssignments, TypeAssignment{
			Decl:       decl,
			Syntax:     d.text[ClauseSyntax],
			SyntaxType: d.syntax,
		})

		return int32(len(m.TypeAssignments) - 1)
	}
}
