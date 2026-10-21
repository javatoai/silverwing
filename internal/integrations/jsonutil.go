package integrations

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func object(value any) map[string]any { result, _ := value.(map[string]any); return result }
func array(value any) []any           { result, _ := value.([]any); return result }
func at(value any, path ...string) any {
	current := value
	for _, key := range path {
		current = object(current)[key]
		if current == nil {
			return nil
		}
	}
	return current
}
func text(value any) string {
	switch current := value.(type) {
	case string:
		return strings.TrimSpace(current)
	case json.Number:
		return current.String()
	case float64:
		return strings.TrimSpace(fmt.Sprint(current))
	case bool:
		return fmt.Sprint(current)
	case map[string]any:
		for _, key := range []string{"string_value", "text_value", "long_value", "value", "label", "name", "content"} {
			if candidate, ok := current[key]; ok {
				if resolved := text(candidate); resolved != "" {
					return resolved
				}
			}
		}
	}
	return ""
}
func integer(value any) (int64, bool) {
	switch current := value.(type) {
	case json.Number:
		v, err := current.Int64()
		return v, err == nil
	case float64:
		if current != float64(int64(current)) {
			return 0, false
		}
		return int64(current), true
	case string:
		result, err := strconv.ParseInt(strings.TrimSpace(current), 10, 64)
		return result, err == nil
	}
	return 0, false
}
func boolean(value any) (bool, bool) { result, ok := value.(bool); return result, ok }
func decodeJSON(data string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.UseNumber()
	var result any
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	return result, nil
}
func compactJSON(value any) string { data, _ := json.Marshal(value); return string(data) }
func nonblank(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	result := value
	return &result
}

func collectKeyValues(value any, matches func(string) bool, out *[]string) {
	switch current := value.(type) {
	case map[string]any:
		if key := current["key"]; key != nil && matches(normalizeFieldName(text(key))) {
			if v := text(current["value"]); v != "" {
				*out = append(*out, v)
			}
			return
		}
		for key, child := range current {
			if matches(normalizeFieldName(key)) {
				if v := text(child); v != "" {
					*out = append(*out, v)
				}
			} else {
				collectKeyValues(child, matches, out)
			}
		}
	case []any:
		for _, child := range current {
			collectKeyValues(child, matches, out)
		}
	}
}
func normalizeFieldName(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "_", "")
	value = strings.ReplaceAll(value, " ", "")
	return value
}
