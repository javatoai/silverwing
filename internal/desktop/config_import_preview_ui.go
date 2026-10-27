package desktop

import "strings"

// configPreviewText displays the application's source-compatible change
// descriptions, including the warning when the current shards are corrupt.
func configPreviewText(data any) string {
	if data == nil {
		return "后端未返回配置预览内容。"
	}
	changes := mapsString(dataValue(data, "changes"))
	changed := mapsString(dataValue(data, "changedFields"))
	lines := []string{"配置文件导入预览"}
	if path := dataString(data, "path"); path != "" {
		lines = append(lines, "来源："+path)
	}
	switch {
	case len(changes) > 0:
		for _, change := range changes {
			lines = append(lines, "• "+change)
		}
	case len(changed) > 0:
		lines = append(lines, "变化字段："+strings.Join(changed, "、"))
	default:
		lines = append(lines, "当前版本可识别的设置没有变化；其他版本的设置字段仍以归档为准。")
	}
	return strings.Join(lines, "\n")
}
