package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func skillSourceSnapshotUnavailable(status map[string]any) string {
	if message := strings.TrimSpace(mapString(status, "error")); message != "" {
		return message
	}
	if dataBool(status, "stale") || dataBool(status, "needsRefresh") {
		return "来源快照已过期，请刷新目录后再预览或安装。"
	}
	if strings.TrimSpace(mapString(status, "sourceFingerprint")) == "" {
		return "来源快照尚未核验，请刷新目录后再预览或安装。"
	}
	return ""
}

func skillSourceActionPayload(sourceID, name string, status map[string]any) (map[string]any, bool) {
	if skillSourceSnapshotUnavailable(status) != "" {
		return nil, false
	}
	return map[string]any{"sourceId": sourceID, "name": name, "sourceFingerprint": mapString(status, "sourceFingerprint")}, true
}

func (s *app) skillSourceFingerprintCurrent(sourceID, fingerprint string) bool {
	if fingerprint == "" {
		return false
	}
	for _, wrapper := range s.settingsUnit.state.settingsSkillSources {
		if mapString(mapData(wrapper["source"]), "id") == sourceID {
			status := mapData(wrapper["status"])
			return skillSourceSnapshotUnavailable(status) == "" && mapString(status, "sourceFingerprint") == fingerprint
		}
	}
	return false
}

// Keep the existing action appearance while making an unavailable source's
// remote operation unavailable to both pointer and keyboard input.
func skillSourceAction(c *ui.Context, key, iconName, title string, p palette, enabled bool) bool {
	clicked := false
	ui.Box(c).Disabled(!enabled).Children(func() { clicked = action(c, key, iconName, title, p, false) })
	return enabled && clicked
}

func skillInstalledOriginText(skill map[string]any) string {
	origin := mapData(skill["installedOrigin"])
	repository := strings.TrimSpace(mapString(origin, "repositoryUrl"))
	if repository == "" {
		return "安装出处：历史或外部安装，来源未知"
	}
	ref := strings.TrimSpace(mapString(origin, "ref"))
	if ref == "" {
		ref = "分支 / 标签未记录"
	}
	return "安装出处：" + repository + " · " + ref
}
