package handler

import (
	"net/url"
	"strconv"
)

// Offset/limit pagination shared by list endpoints. ParsePage reads
// the query string once and Clamp turns a page into slice bounds, so
// handlers never hand-write boundary arithmetic again.

// Page is one window over an ordered collection.
type Page struct {
	Offset int
	Limit  int
}

const (
	defaultPageOffset = 0
	defaultPageLimit  = 50
	maxPageLimit      = 500
)

// ParsePage extracts offset/limit from q. Defaults are 50 items from
// the start; negative offsets fall back to 0 and limits above
// maxPageLimit are clamped to 500 so a single client cannot force an
// unbounded result set.
func ParsePage(q url.Values) Page {
	p := Page{Offset: defaultPageOffset, Limit: defaultPageLimit}
	if n, err := strconv.Atoi(q.Get("offset")); err == nil && n >= 0 {
		p.Offset = n
	}
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 && n <= maxPageLimit {
		p.Limit = n
	}
	return p
}

// Clamp returns the [start,end) bounds for slicing a collection of n
// items according to p. Both bounds stay within 0..n even when the
// requested window lies entirely past the end, in which case start
// equals end and callers simply emit an empty page.
func (p Page) Clamp(n int) (int, int) {
	start := p.Offset
	if start < 0 {
		start = 0
	}
	if start > n {
		start = n
	}
	end := start + p.Limit
	if end < start {
		end = start
	}
	if end > n {
		end = n
	}
	return start, end
}

// SQLArgs returns the pair to append after "LIMIT ? OFFSET ?" clauses.
func (p Page) SQLArgs() (limit, offset int) {
	return p.Limit, p.Offset
}

// IsDefault reports whether the page was left untouched by the caller,
// letting handlers skip pagination metadata in responses when the full
// collection fits on the implicit first page.
func (p Page) IsDefault() bool {
	return p.Offset == defaultPageOffset && p.Limit == defaultPageLimit
}

// PageFromQuery behaves like ParsePage but lets the caller choose a
// smaller default limit, which endpoints with heavy rows (reports,
// exports) need to keep responses bounded.
func PageFromQuery(q url.Values, defLimit int) Page {
	p := ParsePage(q)
	if p.IsDefault() && defLimit > 0 && defLimit <= maxPageLimit {
		p.Limit = defLimit
	}
	return p
}
