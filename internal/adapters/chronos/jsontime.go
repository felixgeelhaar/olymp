package chronos

import (
	"strings"
	"time"
)

// jsonTime decodes Chronos's RFC3339 timestamps but tolerates the
// zero-value case where the field is omitted entirely. time.Time's
// default UnmarshalJSON rejects empty strings, which would fail every
// list response that has unfilled detection windows.
type jsonTime struct{ time.Time }

func (t *jsonTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return err
		}
	}
	t.Time = parsed
	return nil
}
