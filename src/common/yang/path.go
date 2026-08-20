package yang

import (
	"encoding/xml"
	"net/url"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// KeyValue is one list-key predicate on a [Segment]: the key leaf's
// name and its value in canonical lexical form. Key order follows the
// YANG `key` statement order of the list it addresses.
type KeyValue struct {
	Name  string
	Value string
}

// Segment is one step of a [Path]: a module-qualified node name with
// optional list-key predicates. Module is the YANG module name owning
// the node; an empty Module inherits the previous segment's module
// (the RFC 7951 / RESTCONF qualification rule). Namespace optionally
// carries the module's XML namespace URI — generated descriptors fill
// it so [Path.SubtreeFilterXML] can emit correctly-namespaced filters
// without schema lookups; path parsing leaves it empty.
type Segment struct {
	Module    string
	Namespace string
	Name      string
	Keys      []KeyValue
}

// Path addresses one node in a YANG data tree: a sequence of
// module-qualified segments with list-key predicates, the shared
// path model all three protocol libraries consume. A Path renders to
// the three wire forms via [Path.SubtreeFilterXML],
// [Path.RESTCONFURI], and [Path.String] (the gNMI structured-path
// string form); the gNMI library converts Segment-for-PathElem into
// the gNMI Path proto.
//
// The zero Path addresses the datastore root. Path values are
// immutable by convention: render methods never modify the receiver,
// and callers must not share a Segments slice they still append to.
type Path struct {
	Segments []Segment
}

// String renders the path in the gNMI string form:
// /module:name[key=value]/child[k1=v1][k2=v2]. A segment is
// module-qualified only on module boundaries — a module equal to the
// previous segment's is elided, matching the inherit rule
// [ParsePath] applies on the way back in. Within key values, `\` and
// `]` are escaped with a backslash per the gNMI path specification.
func (p Path) String() string {
	if len(p.Segments) == 0 {
		return "/"
	}
	var b strings.Builder
	mod := ""
	for _, seg := range p.Segments {
		b.WriteByte('/')
		if seg.Module != "" && seg.Module != mod {
			b.WriteString(seg.Module)
			b.WriteByte(':')
			mod = seg.Module
		}
		b.WriteString(seg.Name)
		for _, kv := range seg.Keys {
			b.WriteByte('[')
			b.WriteString(kv.Name)
			b.WriteByte('=')
			b.WriteString(escapeKeyValue(kv.Value))
			b.WriteByte(']')
		}
	}
	return b.String()
}

// escapeKeyValue backslash-escapes the two characters that are
// structural inside a bracketed gNMI key predicate.
func escapeKeyValue(v string) string {
	if !strings.ContainsAny(v, `]\`) {
		return v
	}
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] == ']' || v[i] == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(v[i])
	}
	return b.String()
}

// ParsePath parses the gNMI string form produced by [Path.String]:
// slash-separated module-qualified segments with bracketed key
// predicates, backslash-escaping `]` and `\` inside key values. The
// empty string and "/" parse to the root path.
func ParsePath(s string) (Path, error) {
	if s == "" || s == "/" {
		return Path{}, nil
	}
	rest, ok := strings.CutPrefix(s, "/")
	if !ok {
		return Path{}, errs.New().Code(ErrCodePathParse).Msgf("path %q does not start with '/'", s)
	}

	var p Path
	for len(rest) > 0 {
		seg, remainder, err := parseSegment(rest, s)
		if err != nil {
			return Path{}, err
		}
		p.Segments = append(p.Segments, seg)
		rest = remainder
	}
	if len(p.Segments) == 0 {
		return Path{}, errs.New().Code(ErrCodePathParse).Msgf("path %q has no segments", s)
	}
	return p, nil
}

// parseSegment consumes one segment (through its key predicates and
// any trailing '/') from rest. full is the original input, quoted in
// errors.
func parseSegment(rest, full string) (Segment, string, error) {
	var seg Segment

	// Node name runs to the first '[' or '/'.
	end := strings.IndexAny(rest, "[/")
	name := rest
	if end >= 0 {
		name = rest[:end]
		rest = rest[end:]
	} else {
		rest = ""
	}
	if mod, n, ok := strings.Cut(name, ":"); ok {
		seg.Module, name = mod, n
	}
	if name == "" {
		return Segment{}, "", errs.New().Code(ErrCodePathParse).Msgf("path %q has an empty segment name", full)
	}
	seg.Name = name

	// Key predicates.
	for strings.HasPrefix(rest, "[") {
		kv, remainder, err := parseKeyPredicate(rest, full)
		if err != nil {
			return Segment{}, "", err
		}
		seg.Keys = append(seg.Keys, kv)
		rest = remainder
	}

	if rest != "" {
		var ok bool
		rest, ok = strings.CutPrefix(rest, "/")
		if !ok {
			return Segment{}, "", errs.New().Code(ErrCodePathParse).Msgf("path %q has trailing characters after segment %q", full, seg.Name)
		}
	}
	return seg, rest, nil
}

// parseKeyPredicate consumes one "[name=value]" predicate from rest,
// honoring backslash escapes inside the value.
func parseKeyPredicate(rest, full string) (KeyValue, string, error) {
	rest = rest[1:] // consume '['
	eq := strings.IndexByte(rest, '=')
	if eq < 0 {
		return KeyValue{}, "", errs.New().Code(ErrCodePathParse).Msgf("path %q has a key predicate without '='", full)
	}
	name := rest[:eq]
	rest = rest[eq+1:]

	var val strings.Builder
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case '\\':
			if i+1 >= len(rest) {
				return KeyValue{}, "", errs.New().Code(ErrCodePathParse).Msgf("path %q ends inside a key escape", full)
			}
			i++
			val.WriteByte(rest[i])
		case ']':
			return KeyValue{Name: name, Value: val.String()}, rest[i+1:], nil
		default:
			val.WriteByte(rest[i])
		}
	}
	return KeyValue{}, "", errs.New().Code(ErrCodePathParse).Msgf("path %q has an unterminated key predicate", full)
}

// RESTCONFURI renders the path as an RFC 8040 data-resource
// identifier relative to {+restconf}/data: each segment is
// "module:name" on a module boundary and bare "name" within a module,
// list instances select with "=value1,value2" (key values in YANG key
// order), and key values percent-encode every byte outside the RFC
// 3986 unreserved set, so ',', '/', and ':' inside a value never read
// as structure. [ParseRESTCONFURI] inverts it.
func (p Path) RESTCONFURI() string {
	var b strings.Builder
	mod := ""
	for _, seg := range p.Segments {
		b.WriteByte('/')
		if seg.Module != "" && seg.Module != mod {
			b.WriteString(seg.Module)
			b.WriteByte(':')
			mod = seg.Module
		}
		b.WriteString(seg.Name)
		for i, kv := range seg.Keys {
			if i == 0 {
				b.WriteByte('=')
			} else {
				b.WriteByte(',')
			}
			b.WriteString(percentEncode(kv.Value))
		}
	}
	if b.Len() == 0 {
		return "/"
	}
	return b.String()
}

// percentEncode encodes every byte outside the RFC 3986 unreserved
// set. Stricter than [url.PathEscape], which leaves sub-delims like
// ',' bare — a comma inside a key value must not read as a key
// separator.
func percentEncode(s string) string {
	const upperhex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(upperhex[c>>4])
			b.WriteByte(upperhex[c&0xf])
		}
	}
	return b.String()
}

// ParseRESTCONFURI parses an RFC 8040 data-resource identifier
// produced by [Path.RESTCONFURI] back into a Path. keyNames supplies
// the key leaf names for each keyed segment in order of appearance,
// since the URI form carries only key values: keyNames[i] lists the
// key names of the i-th segment that has an "=" instance selector.
// Segments without a selector consume no keyNames entry.
func ParseRESTCONFURI(s string, keyNames ...[]string) (Path, error) {
	if s == "" || s == "/" {
		return Path{}, nil
	}
	rest, ok := strings.CutPrefix(s, "/")
	if !ok {
		return Path{}, errs.New().Code(ErrCodePathParse).Msgf("RESTCONF path %q does not start with '/'", s)
	}

	var (
		p     Path
		mod   string
		keyed int
	)
	for _, raw := range strings.Split(rest, "/") {
		if raw == "" {
			return Path{}, errs.New().Code(ErrCodePathParse).Msgf("RESTCONF path %q has an empty segment", s)
		}
		name, sel, hasSel := strings.Cut(raw, "=")
		var seg Segment
		if m, n, qualified := strings.Cut(name, ":"); qualified {
			seg.Module, seg.Name = m, n
			mod = m
		} else {
			seg.Module, seg.Name = mod, name
		}
		if seg.Name == "" {
			return Path{}, errs.New().Code(ErrCodePathParse).Msgf("RESTCONF path %q has an empty segment name", s)
		}
		if hasSel {
			if keyed >= len(keyNames) {
				return Path{}, errs.New().Code(ErrCodePathParse).Msgf("RESTCONF path %q has more keyed segments than key-name sets", s)
			}
			names := keyNames[keyed]
			keyed++
			values := strings.Split(sel, ",")
			if len(values) != len(names) {
				return Path{}, errs.New().Code(ErrCodePathParse).
					Attr("got", len(values)).
					Attr("want", len(names)).
					Msgf("RESTCONF path %q key count mismatch on segment %q", s, seg.Name)
			}
			for i, v := range values {
				dec, err := url.PathUnescape(v)
				if err != nil {
					return Path{}, errs.From(err).Code(ErrCodePathParse).Msgf("RESTCONF path %q has a malformed key value", s)
				}
				seg.Keys = append(seg.Keys, KeyValue{Name: names[i], Value: dec})
			}
		}
		p.Segments = append(p.Segments, seg)
	}
	return p, nil
}

// SubtreeFilterXML renders the path as a NETCONF subtree filter (RFC
// 6241 §6): one nested element per segment, key predicates as
// content-match child elements, and an empty innermost element
// selecting the whole subtree. Segments with a Namespace emit an
// xmlns attribute when the namespace differs from the parent
// segment's; segments without one emit unqualified elements.
func (p Path) SubtreeFilterXML() ([]byte, error) {
	var b strings.Builder
	parentNS := ""
	for _, seg := range p.Segments {
		b.WriteByte('<')
		b.WriteString(seg.Name)
		if seg.Namespace != "" && seg.Namespace != parentNS {
			b.WriteString(` xmlns="`)
			if err := xml.EscapeText(&b, []byte(seg.Namespace)); err != nil {
				return nil, errs.Wrap(err, "escape namespace")
			}
			b.WriteString(`"`)
			parentNS = seg.Namespace
		}
		b.WriteByte('>')
		for _, kv := range seg.Keys {
			b.WriteByte('<')
			b.WriteString(kv.Name)
			b.WriteByte('>')
			if err := xml.EscapeText(&b, []byte(kv.Value)); err != nil {
				return nil, errs.Wrap(err, "escape key value")
			}
			b.WriteString("</")
			b.WriteString(kv.Name)
			b.WriteByte('>')
		}
	}
	for i := len(p.Segments) - 1; i >= 0; i-- {
		b.WriteString("</")
		b.WriteString(p.Segments[i].Name)
		b.WriteByte('>')
	}
	return []byte(b.String()), nil
}
