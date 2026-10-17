package runner

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.aledante.io/FlowSeer/src/common/errs"

	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

// ErrCodeSysctl is the wire identity for a kernel setting that could not be
// read or written.
var ErrCodeSysctl = errs.NewCode("netpen/sysctl")

// procSysctl is the production [check.Sysctl]: each dotted key is a file
// under root, `net.ipv4.ip_forward` at `<root>/net/ipv4/ip_forward`, read
// and written whole, which is how the kernel exposes its settings under
// /proc/sys. A key with an empty segment or a path element is refused
// before the file is touched, so a key never names a file outside root.
type procSysctl struct {
	root string
}

// newProcSysctl returns the sysctl over the kernel's own proc filesystem.
func newProcSysctl() procSysctl {
	return procSysctl{root: "/proc/sys"}
}

func (s procSysctl) path(key string) (string, error) {
	segments := strings.Split(key, ".")
	for _, seg := range segments {
		if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, `/\`) {
			return "", errs.New().
				Code(ErrCodeSysctl).
				Attr("key", key).
				Msg("sysctl key is not dotted segments")
		}
	}
	return filepath.Join(append([]string{s.root}, segments...)...), nil
}

// Get reads the setting with surrounding whitespace trimmed.
func (s procSysctl) Get(key string) (string, error) {
	p, err := s.path(key)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", errs.From(err).
			Code(ErrCodeSysctl).
			Attr("key", key).
			Msgf("read sysctl %q", key)
	}
	return strings.TrimSpace(string(data)), nil
}

// Set writes value to the setting. The file is opened for writing and
// truncated, never created, which is what the baseline's `open(path, "w")`
// does on the proc file.
func (s procSysctl) Set(key, value string) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return errs.From(err).
			Code(ErrCodeSysctl).
			Attr("key", key).
			Msgf("open sysctl %q for writing", key)
	}
	if _, err := f.WriteString(value); err != nil {
		_ = f.Close()
		return errs.From(err).
			Code(ErrCodeSysctl).
			Attr("key", key).
			Msgf("write sysctl %q", key)
	}
	if err := f.Close(); err != nil {
		return errs.From(err).
			Code(ErrCodeSysctl).
			Attr("key", key).
			Msgf("close sysctl %q", key)
	}
	return nil
}

// MapSysctl is the [check.Sysctl] a test passes through [Options.Sysctl]:
// a map of dotted keys to values, with a Get of an absent key an error, as
// a missing proc file is. Construct it with [NewMapSysctl]. The zero value
// has no keys and every Get fails. MapSysctl is safe for concurrent calls.
type MapSysctl struct {
	mu     sync.Mutex
	values map[string]string
}

var _ check.Sysctl = (*MapSysctl)(nil)

// NewMapSysctl returns a MapSysctl holding a copy of values.
func NewMapSysctl(values map[string]string) *MapSysctl {
	m := &MapSysctl{values: make(map[string]string, len(values))}
	for k, v := range values {
		m.values[k] = v
	}
	return m
}

// Get returns the value under key, or an error when the key is absent.
func (m *MapSysctl) Get(key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.values[key]
	if !ok {
		return "", errs.New().
			Code(ErrCodeSysctl).
			Attr("key", key).
			Msgf("sysctl %q is not in the map", key)
	}
	return v, nil
}

// Set stores value under key.
func (m *MapSysctl) Set(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.values == nil {
		m.values = make(map[string]string)
	}
	m.values[key] = value
	return nil
}
