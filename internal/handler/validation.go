package handler

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Shared request validation helpers. Every list endpoint funnels its
// query-string parsing through these functions so limits, ids and time
// ranges behave identically across the API.

const (
	defaultLimit = 100
	maxLimit     = 1000
)

// parseLimit reads the "limit" query parameter. Missing or invalid
// values fall back to 100; values above maxLimit are clamped to 1000.
func parseLimit(q url.Values) int {
	n, err := strconv.Atoi(q.Get("limit"))
	if err != nil || n <= 0 || n > maxLimit {
		return defaultLimit
	}
	return n
}

// parseID extracts the {id} path segment as a positive int64.
func parseID(req *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// timeLayouts are the accepted formats for time parameters: full
// timestamps first, bare calendar dates second.
var timeLayouts = []string{time.RFC3339, "2006-01-02"}

// parseTimeParam reads key from q. When the parameter is absent def is
// returned unchanged; when present but unparseable an error naming the
// offending key is returned so handlers can map it to a 400 response.
func parseTimeParam(q url.Values, key string, def time.Time) (time.Time, error) {
	s := q.Get(key)
	if s == "" {
		return def, nil
	}
	for _, layout := range timeLayouts {
		t, err := time.Parse(layout, s)
		if err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid %s %q: expect RFC3339 or YYYY-MM-DD", key, s)
}

// requireFields validates that every named field carries a non-empty
// value. On failure the error lists all missing field names in sorted
// order, giving clients one round trip to fix every problem at once.
func requireFields(fields map[string]string) error {
	var missing []string
	for name, value := range fields {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("missing required fields: %s", strings.Join(missing, ", "))
}

// parsePositiveInt reads key as a strictly positive integer, falling
// back to def when absent or malformed. It backs optional numeric
// knobs such as window sizes and retry counts.
func parsePositiveInt(q url.Values, key string, def int) int {
	n, err := strconv.Atoi(q.Get(key))
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// parseBoolParam reads key as an optional boolean. Only explicit "1",
// "t", "true", "0", "f" and "false" (case-insensitive) are accepted;
// anything else, including an absent parameter, yields def so callers
// never act on half-parsed input.
func parseBoolParam(q url.Values, key string, def bool) bool {
	s := strings.ToLower(q.Get(key))
	switch s {
	case "1", "t", "true":
		return true
	case "0", "f", "false":
		return false
	default:
		return def
	}
}

// normalizeCode upper-cases and length-caps station/channel codes.
// SWIF codes are fixed-width ASCII in the wire format; normalizing at
// the API boundary keeps lookups consistent with what Decode returns.
func normalizeCode(s string, maxLen int) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	return s
}
