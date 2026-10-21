package desktop

import (
	"fmt"
	"math"
	"strings"
)

func payloadRawString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}

func mcpFormArguments(text string, original map[string]any) ([]any, error) {
	if value, exists := original["args"]; exists && text == strings.Join(stringArray(mustArray(value)), "\n") {
		return mustArray(value), nil
	}
	return parseMCPArgs(text)
}

func mcpFormTimeout(text string, original map[string]any, key string) (any, error) {
	if value, exists := original[key]; exists && text == fmt.Sprint(value) {
		if number, ok := value.(float64); ok && !math.IsNaN(number) && !math.IsInf(number, 0) && number > 0 && number <= 3600 {
			return number, nil
		}
	}
	return parseBoundedInt(text, 1, 3600)
}

func codexMCPFormPairs(text string, original any, field string) (string, error) {
	if text == codexAIMCPMapLines(original) {
		return mcpJSONText(original), nil
	}
	return codexAIMCPPairsJSON(text, field)
}
