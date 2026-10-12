package repositories

import "strings"

// sortColumn is a sortable list column: one or more SQL expressions ordered in
// the same direction. nullable puts NULLs last in both directions — SQLite and
// PostgreSQL disagree on where they go by default.
type sortColumn struct {
	exprs    []string
	nullable bool
}

// orderClause maps ListOptions.SortBy to an ORDER BY clause through a fixed
// whitelist, so the sort column can never be caller-controlled SQL. Unknown or
// empty SortBy falls back to fallback. tiebreak (a unique column) is always
// appended so rows with equal values keep a stable order across pages.
func orderClause(opts ListOptions, columns map[string]sortColumn, fallback, tiebreak string) string {
	col, ok := columns[opts.SortBy]
	if !ok {
		return fallback + ", " + tiebreak
	}
	dir := " ASC"
	if opts.SortDesc {
		dir = " DESC"
	}
	parts := make([]string, 0, 2*len(col.exprs)+1)
	for _, expr := range col.exprs {
		if col.nullable {
			parts = append(parts, expr+" IS NULL")
		}
		parts = append(parts, expr+dir)
	}
	return strings.Join(append(parts, tiebreak), ", ")
}
