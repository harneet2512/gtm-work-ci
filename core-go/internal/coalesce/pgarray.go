package coalesce

import (
	"database/sql/driver"
	"fmt"
	"strings"
)

// pgTextArray scans a Postgres text[] / uuid[]::text[] column through database/sql, whose pgx
// driver hands arrays over as text. Elements are uuids or other quote-free tokens.
type pgTextArray []string

// Scan implements sql.Scanner.
func (a *pgTextArray) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*a = nil
		return nil
	case string:
		return a.parse(v)
	case []byte:
		return a.parse(string(v))
	case []string:
		*a = append((*a)[:0], v...)
		return nil
	case []any:
		out := make([]string, len(v))
		for i, e := range v {
			out[i] = fmt.Sprint(e)
		}
		*a = out
		return nil
	}
	return fmt.Errorf("coalesce: cannot scan %T into a text array", src)
}

func (a *pgTextArray) parse(s string) error {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return fmt.Errorf("coalesce: %q is not a Postgres array literal", s)
	}
	body := s[1 : len(s)-1]
	if body == "" {
		*a = []string{}
		return nil
	}
	parts := strings.Split(body, ",")
	for i, p := range parts {
		parts[i] = strings.Trim(p, `"`)
	}
	*a = parts
	return nil
}

// Value implements driver.Valuer, producing an array literal.
func (a pgTextArray) Value() (driver.Value, error) {
	return "{" + strings.Join(a, ",") + "}", nil
}
