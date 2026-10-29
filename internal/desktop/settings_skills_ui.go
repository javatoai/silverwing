package desktop

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

func (s *app) settingsSkills(c *ui.Context, p palette) {
	settingsHelpCardWithActions(c, p, "Skill 来源", "读取 Git 来源目录后，可预览并安装 Skill。\n\n可添加包含 SKILL.md 的 Git 仓库。", func() {
		s.overflowMenu(c, p, "skill-sources-more", "更多 Skill 来源操作", false, func(m *overflowMenuBuilder) {
			if m.Item("refresh-all", "重新读取所有来源").Icon("refresh").Chosen() {
				s.act("skills.sources/catalog", "", "", "", "", "", nil)
			}
		})
	}, func() {
		if len(s.settingsUnit.state.settingsSkillSources) == 0 {
			settingsMuted(c, p, "还没有 Skill 来源，或尚未读取来源状态。")
		}
		for _, wrapper := range s.settingsUnit.state.settingsSkillSources {
			source := mapData(wrapper["source"])
			status := mapData(wrapper["status"])
			sourceID := mapString(source, "id")
			name := firstNonEmpty(mapString(source, "name"), sourceID)
			skills := mapsFromData(dataValue(status, "skills"))
			ui.Column(c).FillWidth().Padding(12).Gap(8).Background(p.soft).Radius(10).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
						settingsHelpLabel(c, p, name, "刷新来源后显示可用 Skill。", 12, 600)
						label(c, mapString(source, "repositoryUrl"), 10, p.muted).SingleLine()
						roots := stringArray(mustArray(dataValue(status, "discoveredRoots")))
						if len(roots) > 0 {
							label(c, strings.Join(roots, " · "), 9, p.muted).SingleLine()
						}
						if errText := mapString(status, "error"); errText != "" {
							label(c, errText, 10, p.amber).LineHeight(1.35).Wrap()
						}
					})
					s.overflowMenu(c, p, "skill-source-more-"+sourceID, "更多来源操作 · "+name, false, func(m *overflowMenuBuilder) {
						if m.Item("refresh", "刷新目录").Icon("refresh").Chosen() {
							s.act("skills.sources/catalog", "", "", "", "", "", map[string]any{"sourceId": sourceID})
						}
						m.Separator()
						if m.Item("remove", "移除来源").Icon("delete").Dangerous().Chosen() {
							s.confirmNative("移除 Skill 来源", "将删除 SilverWing 中的仓库缓存和发现记录；已安装的 Skill 不会被卸载。", name, func() {
								s.act("skills.sources/remove", "", "", "", "", "", map[string]any{"sourceId": sourceID, "confirmed": true})
							})
						}
					})
				})
				if len(skills) == 0 {
					settingsMuted(c, p, "暂无 Skill。")
				}
				for _, skill := range skills {
					skillName := mapString(skill, "name")
					ownership := mapString(skill, "ownership")
					key := sourceID + ":" + skillName
					ui.Row(c).Padding(8, 10).Gap(8).AlignItems(ui.Center).Background(p.surface).Radius(8).Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
							settingsHelpLabel(c, p, skillName, mapString(skill, "description"), 11, 500)
							label(c, extensionOwnershipLabel(ownership), 10, p.muted)
						})
						if action(c, "skill-preview-"+key, "file", "预览", p, false) {
							s.act("skills.preview", "", "", "", "", "", map[string]any{"sourceId": sourceID, "name": skillName})
						}
						switch ownership {
						case "MANAGED":
							s.overflowMenu(c, p, "skill-more-"+key, "更多 Skill 操作 · "+skillName, false, func(m *overflowMenuBuilder) {
								if m.Item("update", "更新").Icon("refresh").Chosen() {
									s.confirmNative("更新 Skill", "将备份当前版本，再安装此来源中的更新。", skillName, func() {
										s.act("skills.update", "", "", "", "", "", map[string]any{"sourceId": sourceID, "name": skillName, "confirmed": true})
									})
								}
								m.Separator()
								if m.Item("uninstall", "卸载").Icon("delete").Dangerous().Chosen() {
									s.confirmNative("卸载 Skill", "将先备份，再删除此来源中由 SilverWing 管理的 Skill。", skillName, func() {
										s.act("skills.remove", "", "", "", "", "", map[string]any{"sourceId": sourceID, "name": skillName, "confirmed": true})
									})
								}
							})
						case "EXTERNAL":
							if action(c, "skill-takeover-"+key, "link", "接管", p, false) {
								s.confirmNative("接管 Skill", "将备份并覆盖当前外部安装版本，之后由 SilverWing 管理更新与卸载。", skillName, func() {
									s.act("skills.install", "", "", "", "", "", map[string]any{"sourceId": sourceID, "name": skillName, "takeOver": true, "confirmed": true})
								})
							}
						case "NOT_INSTALLED":
							if action(c, "skill-install-"+key, "plus", "安装", p, false) {
								s.confirmNative("安装 Skill", "将把来源中的 Skill 复制到 ~/.agents/skills。", skillName, func() {
									s.act("skills.install", "", "", "", "", "", map[string]any{"sourceId": sourceID, "name": skillName, "takeOver": false, "confirmed": true})
								})
							}
						}
					})
				}
			})
		}
		if action(c, "skill-source-add", "plus", "添加来源", p, false) {
			s.openForm("skills.sources/save", "添加 Skill 来源", "", "", "", "", formField{key: "name", label: "显示名称"}, formField{key: "repositoryUrl", label: "Git 仓库地址"}, formField{key: "ref", label: "分支或标签（可留空）"}, formField{key: "skillRoot", label: "Skills 相对目录", value: "skills"})
		}
	})
}

func (s *app) localSkill(name string) map[string]any {
	for _, skill := range s.settingsUnit.state.settingsLocalSkills {
		if mapString(skill, "name") == name || mapString(skill, "directoryName") == name {
			return skill
		}
	}
	return nil
}

func newSettingsSourceID() string {
	return fmt.Sprintf("source-%x", time.Now().UTC().UnixNano())
}
