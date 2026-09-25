// Package goname builds Go identifiers for FlowSeer's code generators,
// snmp/cmd/mibgen and yang/cmd/yanggen.
//
// Identifiers are converted by splitting input into words at separators
// ('-', '_', '.', space, '/', ':') and camelCase boundaries. A run of
// uppercase letters followed by a lowercase letter ends one letter early
// so the final capital begins the subsequent word. Digits stay attached
// to the preceding word.
//
// Words whose lowercase form appears in the initialism table take their
// canonical spelling. The initialism table contains staticcheck ST1003's
// default list, plus MAC, VLAN, MTU, VRF, OID, SNMP, BGP, OSPF, LLDP, and
// the spellings IPv4 and IPv6. Words not in the table have their first letter
// capitalized while preserving the casing of remaining characters. Empty
// results or identifiers that begin with a digit receive an "X" prefix.
package goname

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

var initialisms = map[string]string{
	"acl":   "ACL",
	"api":   "API",
	"ascii": "ASCII",
	"cpu":   "CPU",
	"css":   "CSS",
	"dns":   "DNS",
	"eof":   "EOF",
	"guid":  "GUID",
	"html":  "HTML",
	"http":  "HTTP",
	"https": "HTTPS",
	"id":    "ID",
	"ip":    "IP",
	"json":  "JSON",
	"lhs":   "LHS",
	"qps":   "QPS",
	"ram":   "RAM",
	"rhs":   "RHS",
	"rpc":   "RPC",
	"sla":   "SLA",
	"smtp":  "SMTP",
	"sql":   "SQL",
	"ssh":   "SSH",
	"tcp":   "TCP",
	"tls":   "TLS",
	"ttl":   "TTL",
	"udp":   "UDP",
	"ui":    "UI",
	"uid":   "UID",
	"uuid":  "UUID",
	"uri":   "URI",
	"url":   "URL",
	"utf8":  "UTF8",
	"vm":    "VM",
	"xml":   "XML",
	"xmpp":  "XMPP",
	"xsrf":  "XSRF",
	"xss":   "XSS",
	"mac":   "MAC",
	"vlan":  "VLAN",
	"mtu":   "MTU",
	"vrf":   "VRF",
	"oid":   "OID",
	"snmp":  "SNMP",
	"bgp":   "BGP",
	"ospf":  "OSPF",
	"lldp":  "LLDP",
	"ipv4":  "IPv4",
	"ipv6":  "IPv6",
}

var sortedInitialisms []string

func init() {
	sortedInitialisms = make([]string, 0, len(initialisms))
	for _, s := range initialisms {
		sortedInitialisms = append(sortedInitialisms, s)
	}
	slices.SortFunc(sortedInitialisms, func(a, b string) int {
		if len(a) != len(b) {
			return cmp.Compare(len(b), len(a))
		}
		return cmp.Compare(a, b)
	})
}

// Exported converts name to an exported Go identifier.
//
// Words are split at '-', '_', '.', space, '/', ':', and at camelCase
// boundaries. A run of uppercase letters followed by a lowercase letter
// ends one letter early so the last capital begins the next word. Digits
// stay attached to the preceding word. Words matching the initialism table
// take their canonical casing, while other words have their first letter
// capitalized and the remainder preserved. An empty input yields an empty
// string. If the joined result is empty or begins with a digit, Exported
// prefixes "X".
func Exported(name string) string {
	if name == "" {
		return ""
	}

	words := splitWords(name)
	if len(words) == 0 {
		return "X"
	}

	var b strings.Builder
	for _, w := range words {
		if canonical, ok := initialisms[strings.ToLower(w)]; ok {
			b.WriteString(canonical)
			continue
		}

		r, size := utf8.DecodeRuneInString(w)
		b.WriteRune(unicode.ToUpper(r))
		b.WriteString(w[size:])
	}

	s := b.String()
	if s == "" || unicode.IsDigit(rune(s[0])) {
		return "X" + s
	}

	return s
}

// Unexported converts an exported-form Go identifier to an unexported identifier.
//
// When the leading word is an initialism from the canonical table, Unexported
// lower-cases the whole leading word. Otherwise, it lower-cases only the first
// letter. An empty input yields an empty string.
func Unexported(name string) string {
	if name == "" {
		return ""
	}

	for _, init := range sortedInitialisms {
		if strings.HasPrefix(name, init) {
			rem := name[len(init):]
			if rem == "" {
				return strings.ToLower(init)
			}
			r, _ := utf8.DecodeRuneInString(rem)
			if unicode.IsUpper(r) || unicode.IsDigit(r) {
				return strings.ToLower(init) + rem
			}
		}
	}

	r, size := utf8.DecodeRuneInString(name)
	return string(unicode.ToLower(r)) + name[size:]
}

func isSeparator(r rune) bool {
	switch r {
	case '-', '_', '.', ' ', '/', ':':
		return true
	default:
		return false
	}
}

func splitWords(name string) []string {
	var words []string
	var chunk []rune

	flush := func() {
		if len(chunk) > 0 {
			words = append(words, splitChunk(string(chunk))...)
			chunk = chunk[:0]
		}
	}

	for _, r := range name {
		if isSeparator(r) {
			flush()
			continue
		}
		chunk = append(chunk, r)
	}
	flush()

	return words
}

func splitChunk(chunk string) []string {
	runes := []rune(chunk)
	if len(runes) == 0 {
		return nil
	}

	var words []string
	start := 0

	for i := 1; i < len(runes); i++ {
		prev := runes[i-1]
		curr := runes[i]

		if (unicode.IsLower(prev) || unicode.IsDigit(prev)) && unicode.IsUpper(curr) {
			words = append(words, string(runes[start:i]))
			start = i
			continue
		}

		if unicode.IsUpper(prev) && unicode.IsUpper(curr) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
			if !isIPvX(runes, i-1) {
				words = append(words, string(runes[start:i]))
				start = i
				continue
			}
		}
	}

	words = append(words, string(runes[start:]))
	return words
}

func isIPvX(r []rune, start int) bool {
	if start < 0 || start+3 >= len(r) {
		return false
	}
	return (r[start] == 'I' || r[start] == 'i') &&
		(r[start+1] == 'P' || r[start+1] == 'p') &&
		(r[start+2] == 'v' || r[start+2] == 'V') &&
		(r[start+3] == '4' || r[start+3] == '6')
}
