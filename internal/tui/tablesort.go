// Column sorting (J/K): sort-key extraction, ordering, and defaults.
package tui

import (
	"sort"
	"strconv"
	"strings"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

// sortKey extracts a column's sort key for a record.
func (v *tableView) sortKey(col catalog.Column, rec map[string]any) any {
	switch {
	case col.Sort != nil:
		return col.Sort(rec)
	case col.Field != "":
		return rec[col.Field]
	default:
		return col.Text(rec)
	}
}

// sortRows orders rows by the active sort column (stable; empties last). It
// sorts a copy: rows can be the fetched slice itself (applyFilter without a
// filter), and that keeps the fetch order "no sort" returns to. Each row's
// key is read once, since a column may build it from the whole row.
func (v *tableView) sortRows() {
	if v.sortCol < 0 || v.sortCol >= len(v.columns()) {
		return
	}
	col := v.columns()[v.sortCol]
	type keyed struct {
		rec   map[string]any
		key   any
		empty bool
	}
	rows := make([]keyed, len(v.rows))
	for i, rec := range v.rows {
		rows[i].rec = rec
		rows[i].key, rows[i].empty = sortable(v.sortKey(col, rec))
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.empty != b.empty {
			return b.empty // empties sink regardless of direction
		}
		if a.empty {
			return false
		}
		if v.sortDesc {
			return cmpSortable(a.key, b.key) > 0
		}
		return cmpSortable(a.key, b.key) < 0
	})
	v.rows = make([]map[string]any, len(rows))
	for i, row := range rows {
		v.rows[i] = row.rec
	}
}

// defaultDesc picks the natural direction for a freshly selected sort column:
// numbers biggest-first (CPU, restarts), text A-to-Z.
func (v *tableView) defaultDesc() bool {
	if v.sortCol < 0 || v.sortCol >= len(v.columns()) {
		return false
	}
	col := v.columns()[v.sortCol]
	for _, rec := range v.rows {
		key, empty := sortable(v.sortKey(col, rec))
		if empty {
			continue
		}
		_, numeric := key.(float64)
		return numeric
	}
	return false
}

// sortable normalizes a sort key: numbers (including Grail's stringified
// longs and durations) become float64, everything else lowercase text.
func sortable(key any) (norm any, empty bool) {
	switch val := key.(type) {
	case nil:
		return nil, true
	case float64:
		return val, false
	case int:
		return float64(val), false
	case int64:
		return float64(val), false
	case bool:
		if val {
			return 1.0, false
		}
		return 0.0, false
	case string:
		if val == "" {
			return nil, true
		}
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f, false
		}
		return strings.ToLower(val), false
	default:
		s := catalog.FormatValue(val)
		if s == "" {
			return nil, true
		}
		return strings.ToLower(s), false
	}
}

// cmpSortable compares two normalized sort keys; numbers order before text.
func cmpSortable(a, b any) int {
	af, aNum := a.(float64)
	bf, bNum := b.(float64)
	switch {
	case aNum && bNum:
		switch {
		case af < bf:
			return -1
		case af > bf:
			return 1
		}
		return 0
	case aNum:
		return -1
	case bNum:
		return 1
	}
	return strings.Compare(a.(string), b.(string))
}

func sortArrow(desc bool) string {
	if desc {
		return "↓"
	}
	return "↑"
}
