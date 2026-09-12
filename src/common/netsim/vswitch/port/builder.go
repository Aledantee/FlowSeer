package port

import (
	"fmt"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Builder accumulates ports and builds a validated [Table].
//
// A Builder is not safe for concurrent use. The zero value is an empty builder ready for use.
type Builder struct {
	ports []Port
	seen  map[string]struct{}
	err   error
}

// NewBuilder returns an empty [Builder].
func NewBuilder() *Builder {
	return &Builder{
		seen: make(map[string]struct{}),
	}
}

// Add appends a port to the builder. If a port with the same name was already added,
// Add records an error carrying the duplicate name as an attribute, which will be
// returned by [Builder.Build].
func (b *Builder) Add(p Port) *Builder {
	if b.err != nil {
		return b
	}
	if b.seen == nil {
		b.seen = make(map[string]struct{})
	}
	if p.Name == "" {
		b.err = errs.New().Attr("name", "").Msg("port name cannot be empty")

		return b
	}
	if _, exists := b.seen[p.Name]; exists {
		b.err = errs.New().Attr("name", p.Name).Msgf("duplicate port name %q", p.Name)

		return b
	}
	b.seen[p.Name] = struct{}{}
	b.ports = append(b.ports, p)

	return b
}

// Range formats pattern with sequential integers from from through to (inclusive),
// creating a port for each index using attrs with attrs.Name overridden by the formatted
// string. The pattern must contain exactly one %d verb.
func (b *Builder) Range(pattern string, from, to int, attrs Port) *Builder {
	if b.err != nil {
		return b
	}
	if err := validatePattern(pattern); err != nil {
		b.err = err

		return b
	}
	for i := from; i <= to; i++ {
		p := attrs
		p.Name = fmt.Sprintf(pattern, i)
		b.Add(p)
		if b.err != nil {
			return b
		}
	}

	return b
}

// Build validates the accumulated ports, applies standard defaults, and returns a [Table].
// It returns an error if a pattern lacked exactly one %d verb, if a duplicate name was added,
// if an enum value is invalid, if MTU is negative, if a port's LAG parent is not a LAG,
// or if a LAG has a parent.
func (b *Builder) Build() (Table, error) {
	if b.err != nil {
		return Table{}, b.err
	}
	t := Table{
		ports:  make([]Port, len(b.ports)),
		byName: make(map[string]Port, len(b.ports)),
	}
	copy(t.ports, b.ports)
	for _, p := range t.ports {
		t.byName[p.Name] = p
	}
	if err := t.Validate(); err != nil {
		return Table{}, err
	}

	return t.Normalize(), nil
}

func validatePattern(pattern string) error {
	count := 0
	for i := 0; i < len(pattern); i++ {
		if pattern[i] != '%' {
			continue
		}
		if i+1 < len(pattern) && pattern[i+1] == '%' {
			i++

			continue
		}
		i++
		for i < len(pattern) && (pattern[i] == '+' || pattern[i] == '-' || pattern[i] == '#' ||
			pattern[i] == ' ' || pattern[i] == '0' || (pattern[i] >= '0' && pattern[i] <= '9') || pattern[i] == '.') {
			i++
		}
		if i >= len(pattern) {
			return errs.New().Attr("pattern", pattern).Msgf("pattern %q ends with incomplete format specifier", pattern)
		}
		if pattern[i] != 'd' {
			return errs.New().Attr("pattern", pattern).Msgf("pattern %q format verb must be %%d, got %%%c", pattern, pattern[i])
		}
		count++
	}
	if count != 1 {
		return errs.New().Attr("pattern", pattern).Msgf("pattern %q must contain exactly one %%d verb, found %d", pattern, count)
	}

	return nil
}
