package gnmi

import (
	"encoding/json"
	"sort"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/yang"
)

// rows.go assembles rows from gNMI's leaf-granular updates. The
// stream delivers (path, value) pairs; the descriptor's codec wants
// RFC 7951 subtree JSON. A rowStore keeps one JSON tree per row —
// identified by its keyed instance path, ancestor keys included
// (inner-list keys are unique only within their parent) — applies
// updates and deletes to the trees, and re-decodes a
// row through the descriptor's DecodeJSON whenever it must be
// emitted. That keeps the gnmi package free of schema knowledge: the
// generated codec is the only interpreter of the assembled JSON.

// listLevel is one keyed segment on the way to (and including) the
// target list.
type listLevel struct {
	name string
	keys []yang.KeyValue // sorted by name
}

// trackedRow is one row's assembly state.
type trackedRow struct {
	levels []listLevel    // ancestor list levels, outermost first; last is the target
	tree   map[string]any // members under the row entry, keys included
}

// rowStore tracks rows for one descriptor.
type rowStore[Row any, Key comparable] struct {
	desc      yang.ListDescriptor[Row, Key]
	descNames []string // descriptor path segment names
	rows      map[string]*trackedRow
}

func newRowStore[Row any, Key comparable](desc yang.ListDescriptor[Row, Key]) *rowStore[Row, Key] {
	names := make([]string, 0, len(desc.Path.Segments))
	for _, seg := range desc.Path.Segments {
		names = append(names, seg.Name)
	}
	return &rowStore[Row, Key]{desc: desc, descNames: names, rows: make(map[string]*trackedRow)}
}

// locate matches an update path against the descriptor: it returns
// the row's identity (levels through the target segment) and the
// remaining segments inside the row. ok is false when the path does
// not address the watched subtree.
func (rs *rowStore[Row, Key]) locate(p yang.Path) (levels []listLevel, rest []yang.Segment, ok bool) {
	di := 0
	for si, seg := range p.Segments {
		if di >= len(rs.descNames) {
			return levels, p.Segments[si:], true
		}
		if seg.Name != rs.descNames[di] {
			// Tolerate wrapper segments a peer prepends (origin
			// containers) only before any descriptor progress.
			if di == 0 {
				continue
			}
			return nil, nil, false
		}
		if len(seg.Keys) > 0 {
			levels = append(levels, listLevel{name: seg.Name, keys: sortedKeys(seg.Keys)})
		} else if di == len(rs.descNames)-1 {
			// Keyless target: the synthetic single row.
			levels = append(levels, listLevel{name: seg.Name})
		}
		di++
	}
	if di < len(rs.descNames) {
		return nil, nil, false
	}
	return levels, nil, true
}

// sortedKeys normalizes predicate order for stable identity.
func sortedKeys(kvs []yang.KeyValue) []yang.KeyValue {
	out := append([]yang.KeyValue(nil), kvs...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// rowID renders the identity string for a level chain.
func rowID(levels []listLevel) string {
	var b strings.Builder
	for _, l := range levels {
		b.WriteByte('/')
		b.WriteString(l.name)
		for _, kv := range l.keys {
			b.WriteByte('[')
			b.WriteString(kv.Name)
			b.WriteByte('=')
			b.WriteString(kv.Value)
			b.WriteByte(']')
		}
	}
	return b.String()
}

// applyUpdate merges one update into its row's tree, creating the row
// if needed. It returns the affected row's id; ok is false for paths
// outside the watched subtree.
func (rs *rowStore[Row, Key]) applyUpdate(u Update) (string, bool, error) {
	levels, rest, ok := rs.locate(u.Path)
	if !ok || len(levels) == 0 {
		return "", false, nil
	}
	id := rowID(levels)
	row := rs.rows[id]
	if row == nil {
		row = &trackedRow{levels: levels, tree: map[string]any{}}
		for _, kv := range levels[len(levels)-1].keys {
			row.tree[kv.Name] = kv.Value
		}
		rs.rows[id] = row
	}

	val, err := updateJSONValue(u)
	if err != nil {
		return "", false, err
	}
	if len(rest) == 0 {
		// Whole-row update: the JSON object replaces the tree.
		obj, objOK := val.(map[string]any)
		if !objOK {
			return "", false, errs.New().Code(ErrCodeEncoding).Msgf("row-level update for %s is not a JSON object", id)
		}
		for _, kv := range levels[len(levels)-1].keys {
			obj[kv.Name] = kv.Value
		}
		row.tree = obj
		return id, true, nil
	}
	if err := setTreeValue(row.tree, rest, val); err != nil {
		return "", false, errs.Wrapf(err, "apply update for %s", id)
	}
	return id, true, nil
}

// applyDelete removes what p addresses. Removed row ids are returned
// separately from modified ones.
func (rs *rowStore[Row, Key]) applyDelete(p yang.Path) (modified, removed []string) {
	levels, rest, ok := rs.locate(p)
	if !ok {
		// A delete above the subtree clears everything.
		if len(p.Segments) == 0 {
			for id := range rs.rows {
				removed = append(removed, id)
			}
			rs.rows = make(map[string]*trackedRow)
		}
		return nil, removed
	}
	if len(levels) == 0 {
		// Delete of a wrapper above the target list: every row goes.
		for id := range rs.rows {
			removed = append(removed, id)
		}
		rs.rows = make(map[string]*trackedRow)
		return nil, removed
	}
	id := rowID(levels)
	row := rs.rows[id]
	if row == nil {
		return nil, nil
	}
	if len(rest) == 0 {
		delete(rs.rows, id)
		return nil, []string{id}
	}
	deleteTreeValue(row.tree, rest)
	return []string{id}, nil
}

// decodeRow re-decodes one tracked row through the descriptor codec.
func (rs *rowStore[Row, Key]) decodeRow(id string) (Row, error) {
	var zero Row
	row := rs.rows[id]
	if row == nil {
		return zero, errs.Msgf("row %s is not tracked", id)
	}
	doc := wrapRow(row)
	raw, err := json.Marshal(doc)
	if err != nil {
		return zero, errs.Wrap(err, "assemble row JSON")
	}
	rows, err := rs.desc.Codec.DecodeJSON(raw)
	if err != nil {
		return zero, errs.Wrapf(err, "decode assembled row %s", id)
	}
	if len(rows) != 1 {
		return zero, errs.Msgf("assembled row %s decoded to %d rows", id, len(rows))
	}
	return rows[0], nil
}

// ids returns the tracked row ids, sorted for deterministic emission.
func (rs *rowStore[Row, Key]) ids() []string {
	out := make([]string, 0, len(rs.rows))
	for id := range rs.rows {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// wrapRow rebuilds the nested document the codec expects: the target
// entry wrapped in each ancestor level's array, keys included.
func wrapRow(row *trackedRow) map[string]any {
	levels := row.levels
	entry := row.tree
	if len(levels) == 1 && len(levels[0].keys) == 0 {
		// Synthetic single row: the codec expects the bare object,
		// not a one-entry array.
		return map[string]any{levels[0].name: entry}
	}
	// Build outside-in: innermost is the row entry itself.
	for i := len(levels) - 2; i >= 0; i-- {
		wrapper := map[string]any{}
		for _, kv := range levels[i].keys {
			wrapper[kv.Name] = kv.Value
		}
		wrapper[levels[i+1].name] = []any{entry}
		entry = wrapper
	}
	return map[string]any{levels[0].name: []any{entry}}
}

// updateJSONValue converts an update payload to a JSON tree value.
func updateJSONValue(u Update) (any, error) {
	switch {
	case u.JSON != nil:
		var v any
		if err := json.Unmarshal(u.JSON, &v); err != nil {
			return nil, errs.From(err).Code(ErrCodeEncoding).Msg("update carries invalid JSON")
		}
		return v, nil
	case u.Value != nil:
		raw, err := u.Value.MarshalJSON7951()
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeEncoding).Msg("update value does not render as JSON")
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, errs.From(err).Code(ErrCodeEncoding).Msg("rendered value is invalid JSON")
		}
		return v, nil
	default:
		return nil, nil
	}
}

// setTreeValue stores val under the segment path, materializing
// intermediate containers and keyed inner-list entries.
func setTreeValue(tree map[string]any, segs []yang.Segment, val any) error {
	seg := segs[0]
	if len(segs) == 1 && len(seg.Keys) == 0 {
		tree[seg.Name] = val
		return nil
	}

	if len(seg.Keys) > 0 {
		entry := listEntry(tree, seg)
		if len(segs) == 1 {
			obj, ok := val.(map[string]any)
			if !ok {
				return errs.Msgf("list-entry update for %s is not a JSON object", seg.Name)
			}
			for k, v := range obj {
				entry[k] = v
			}
			return nil
		}
		return setTreeValue(entry, segs[1:], val)
	}

	child, ok := tree[seg.Name].(map[string]any)
	if !ok {
		child = map[string]any{}
		tree[seg.Name] = child
	}
	return setTreeValue(child, segs[1:], val)
}

// listEntry finds or creates the inner-list entry seg addresses.
func listEntry(tree map[string]any, seg yang.Segment) map[string]any {
	arr, _ := tree[seg.Name].([]any)
	for _, e := range arr {
		obj, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if entryMatches(obj, seg.Keys) {
			return obj
		}
	}
	entry := map[string]any{}
	for _, kv := range seg.Keys {
		entry[kv.Name] = kv.Value
	}
	tree[seg.Name] = append(arr, any(entry))
	return entry
}

// entryMatches compares an entry's key members against predicates,
// tolerating number-vs-string spellings.
func entryMatches(obj map[string]any, keys []yang.KeyValue) bool {
	for _, kv := range keys {
		got, ok := obj[kv.Name]
		if !ok {
			return false
		}
		if jsonScalarString(got) != kv.Value {
			return false
		}
	}
	return true
}

// jsonScalarString canonicalizes a JSON scalar for key comparison.
func jsonScalarString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case float64:
		raw, _ := json.Marshal(t)
		return string(raw)
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		raw, _ := json.Marshal(t)
		return string(raw)
	}
}

// deleteTreeValue removes what the segment path addresses; missing
// nodes are a no-op.
func deleteTreeValue(tree map[string]any, segs []yang.Segment) {
	seg := segs[0]
	if len(seg.Keys) > 0 {
		arr, _ := tree[seg.Name].([]any)
		for i, e := range arr {
			obj, ok := e.(map[string]any)
			if !ok || !entryMatches(obj, seg.Keys) {
				continue
			}
			if len(segs) == 1 {
				tree[seg.Name] = append(arr[:i:i], arr[i+1:]...)
				return
			}
			deleteTreeValue(obj, segs[1:])
			return
		}
		return
	}
	if len(segs) == 1 {
		delete(tree, seg.Name)
		return
	}
	if child, ok := tree[seg.Name].(map[string]any); ok {
		deleteTreeValue(child, segs[1:])
	}
}
