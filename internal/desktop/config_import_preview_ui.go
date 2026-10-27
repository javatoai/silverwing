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
	invalid := mapsString(dataValue(data, "invalidDevelopmentTools"))
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
	if len(invalid) > 0 {
		names := map[string]string{
			"INTELLIJ_IDEA": "IntelliJ IDEA", "WEBSTORM": "WebStorm", "PYCHARM": "PyCharm",
			"VISUAL_STUDIO_CODE": "Visual Studio Code", "ANDROID_STUDIO": "Android Studio", "DEVECO_STUDIO": "DevEco Studio",
		}
		for i, tool := range invalid {
			if name := names[tool]; name != "" {
				invalid[i] = name
			}
		}
		lines = append(lines, "当前电脑路径无效，导入后需重新选择："+strings.Join(invalid, "、"))
	}
	return strings.Join(lines, "\n")
}
