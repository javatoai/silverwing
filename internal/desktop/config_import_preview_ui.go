package desktop

import (
	"strings"

	"silverwing-go/internal/application"
)

// configPreviewText displays the application's source-compatible change
// descriptions, including the warning when the current shards are corrupt.
func configPreviewText(preview application.ConfigImportPreview) string {
	lines := []string{"确认后，当前配置将替换为所选备份。"}
	switch {
	case len(preview.Changes) > 0:
		for _, change := range preview.Changes {
			lines = append(lines, "• "+change)
		}
	case len(preview.ChangedFields) > 0:
		lines = append(lines, "有变更的字段包括 "+strings.Join(preview.ChangedFields, "、"))
	default:
		lines = append(lines, "当前版本可识别的设置没有变化。其他字段以所选备份为准。")
	}
	if len(preview.InvalidDevelopmentTools) > 0 {
		names := map[string]string{
			"INTELLIJ_IDEA": "IntelliJ IDEA", "WEBSTORM": "WebStorm", "PYCHARM": "PyCharm",
			"VISUAL_STUDIO_CODE": "Visual Studio Code", "ANDROID_STUDIO": "Android Studio", "DEVECO_STUDIO": "DevEco Studio",
		}
		invalid := make([]string, 0, len(preview.InvalidDevelopmentTools))
		for _, tool := range preview.InvalidDevelopmentTools {
			invalid = append(invalid, firstNonEmpty(names[tool], tool))
		}
		lines = append(lines, "恢复后需重新配置这些开发工具的本地路径。", strings.Join(invalid, "、"))
	}
	return strings.Join(lines, "\n")
}
