package integrations

import (
	"strings"
	"unicode"
)

// Source's Char.isWhitespace is isWhitespace OR isSpaceChar: it includes
// information separators and excludes NEL. Only document edges are trimmed.
func trimRequirementText(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return unicode.Is(unicode.Zs, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) ||
			(r >= '\t' && r <= '\r') || (r >= 0x1c && r <= 0x1f)
	})
}

func bodyFromWorkItem(root map[string]any) string {
	for _, field := range array(root["work_item_fields"]) {
		f := object(field)
		key, name := text(f["key"]), text(f["name"])
		if oneOfEqual(key, "description", "content", "detail") || oneOfEqual(name, "需求描述", "描述", "需求内容", "内容", "详情", "Description", "Content", "Detail") {
			if body := richText(f["value"]); body != "" {
				return body
			}
		}
	}
	for _, path := range [][]string{{"description"}, {"content"}, {"detail"}, {"work_item_description"}, {"work_item_content"}, {"work_item_attribute", "description"}, {"work_item_attribute", "content"}} {
		if body := richText(at(root, path...)); body != "" {
			return body
		}
	}
	return ""
}

func bodyFromFieldKey(root map[string]any, key string) string {
	for _, field := range array(root["work_item_fields"]) {
		f := object(field)
		if text(f["key"]) == key {
			if body := richText(f["value"]); body != "" {
				return body
			}
		}
	}
	return ""
}

func richText(value any) string {
	parts := []string{}
	var walk func(any)
	walk = func(current any) {
		switch v := current.(type) {
		case string:
			if trimRequirementText(v) != "" {
				parts = append(parts, v)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		case map[string]any:
			for _, key := range []string{"text", "text_value", "string_value", "content", "value", "label", "insert"} {
				if child, ok := v[key]; ok && child != nil {
					walk(child)
					if children, yes := v["children"]; yes {
						walk(children)
					}
					return
				}
			}
			for _, key := range []string{"children", "blocks", "paragraphs", "elements", "nodes", "ops"} {
				walk(v[key])
			}
		}
	}
	walk(value)
	return trimRequirementText(strings.Join(parts, "\n"))
}

// AI context uses Source's lineSequence normalization, separately from the
// full Markdown reader, which retains internal blank lines and indentation.
func normalizeBody(value string) string {
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
	lines := []string{}
	for _, line := range strings.Split(value, "\n") {
		if line = trimRequirementText(line); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
