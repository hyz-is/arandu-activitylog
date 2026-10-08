package activitylog

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// DateFormat is how a time is written in an entry: Laravel's serializeDate.
const DateFormat = "2006-01-02T15:04:05.000000Z"

// normalizeValue turns what a record holds into what JSON keeps the same way
// on every engine.
//
// A time is written in UTC as Laravel's serializeDate writes it,
// 2006-01-02T15:04:05.000000Z. A value that knows how it is stored -- an enum, a nullable column --
// is written as stored. Bytes are text. Anything else is left to encoding/json.
func normalizeValue(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case time.Time:
		if v.IsZero() {
			return nil
		}
		return v.UTC().Format(DateFormat)
	case *time.Time:
		if v == nil || v.IsZero() {
			return nil
		}
		return v.UTC().Format(DateFormat)
	case []byte:
		return string(v)
	case json.RawMessage:
		var decoded any
		if json.Unmarshal(v, &decoded) == nil {
			return decoded
		}
		return string(v)
	case map[string]any:
		return normalizeMap(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = normalizeValue(item)
		}
		return out
	case driver.Valuer:
		stored, err := v.Value()
		if err != nil {
			return fmt.Sprint(v)
		}
		return normalizeValue(stored)
	case fmt.Stringer:
		return v.String()
	}
	return derefValue(value)
}

// derefValue follows a pointer to the value it holds, so a nullable column is
// logged as its value or null rather than as an address.
func derefValue(value any) any {
	switch v := value.(type) {
	case *string:
		if v == nil {
			return nil
		}
		return *v
	case *int64:
		if v == nil {
			return nil
		}
		return *v
	case *int:
		if v == nil {
			return nil
		}
		return *v
	case *bool:
		if v == nil {
			return nil
		}
		return *v
	case *float64:
		if v == nil {
			return nil
		}
		return *v
	}
	return value
}

func normalizeMap(values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = normalizeValue(value)
	}
	return out
}

// sameValue says whether two logged values are the same, after both were
// normalized: null-safe, and comparing what JSON would write.
func sameValue(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)
	if errLeft != nil || errRight != nil {
		return fmt.Sprint(a) == fmt.Sprint(b)
	}
	return string(left) == string(right)
}
