package desktop

import (
	"fmt"
	"path/filepath"

	"github.com/egoist/mygo/ui"
)

func (s *app) settingsBackupReloadButton(c *ui.Context, p palette) {
	state := s.ensureSettingsConfigRecovery()
	button := ui.ButtonBase(c.Key("settings-config-backups")).Row().Gap(7).Height(34).Padding(0, 16).
		Radius(20).AlignItems(ui.Center).Border(1, p.line).Disabled(state.backupDeleteLoading).Label("读取备份")
	if button.Hovered() {
		button.Background(p.soft)
	} else {
		button.Background(p.surface)
	}
	if state.backupFocusReload {
		button.Focus()
		state.backupFocusReload = false
	}
	button.Children(func() { icon(c, "refresh", 17, p.text); label(c, "读取备份", 13, p.text).NoWrap() })
	if button.Clicked() {
		s.act("config.backups", "", "", "", "", "", nil)
	}
}

func (s *app) renderSettingsBackupList(c *ui.Context, p palette) {
	state := s.ensureSettingsConfigRecovery()
	if state.backupSelected == nil {
		state.backupSelected = map[string]bool{}
	}
	locked := s.busy || state.backupDeleteLoading || state.backupsLoading
	all := len(s.settingsBackups) > 0
	selected := 0
	for _, backup := range s.settingsBackups {
		id := mapString(backup, "id")
		if id == "" || !state.backupSelected[id] {
			all = false
		} else {
			selected++
		}
	}
	if len(s.settingsBackups) > 0 {
		ui.Row(c).FillWidth().Height(34).Gap(8).AlignItems(ui.Center).Children(func() {
			check := ui.Checkbox(c.Key("settings-backup-select-all"), &all, "全选").Shrink(0).Disabled(locked)
			if selected > 0 && selected < len(s.settingsBackups) {
				check.Mixed()
			}
			check.OnChange(func() {
				for _, backup := range s.settingsBackups {
					if id := mapString(backup, "id"); id != "" {
						if all {
							state.backupSelected[id] = true
						} else {
							delete(state.backupSelected, id)
						}
					}
				}
			})
			label(c, fmt.Sprintf("已选 %d 个", selected), 11, p.muted).Grow(1).MinWidth(0).SingleLine()
			text := "删除所选"
			if state.backupDeleteLoading {
				text = "正在删除…"
			}
			button := ui.ButtonBase(c.Key("settings-backup-delete-selected")).Height(34).Padding(0, 9).Radius(7).
				Row().Gap(5).AlignItems(ui.Center).Shrink(0).Disabled(locked || selected == 0).Label(text)
			if button.Hovered() {
				button.Background(p.soft)
			}
			button.Children(func() { icon(c, "delete", 15, p.danger); label(c, text, 12, p.danger).NoWrap() })
			if button.Clicked() {
				s.beginSettingsBackupDelete()
			}
		})
	}
	for _, backup := range s.settingsBackups {
		id, path := mapString(backup, "id"), mapString(backup, "path")
		name := filepath.Base(path)
		checked := state.backupSelected[id]
		ui.Column(c).FillWidth().MinWidth(0).Gap(2).Children(func() {
			row := ui.Row(c).FillWidth().MinWidth(0).Height(38).Gap(8).Padding(0, 8).Radius(7)
			if checked {
				row.Background(p.selected)
			} else {
				row.Background(p.soft)
			}
			row.Children(func() {
				ui.Checkbox(c.Key("settings-backup-select-"+id), &checked, "").Shrink(0).
					Disabled(locked || id == "").Label("选择备份 " + name).OnChange(func() {
					if checked {
						state.backupSelected[id] = true
					} else {
						delete(state.backupSelected, id)
					}
				})
				label(c, name, 12, p.text).Grow(1).MinWidth(0).SingleLine().Tooltip(path)
				ui.Row(c).Shrink(0).Disabled(locked).Children(func() {
					if action(c, "settings-backup-restore-"+id, "refresh", "恢复", p, false) {
						scope := s.settingsExtensionCurrentScope()
						s.confirmNative("恢复配置备份", "将先备份当前配置，再使用选中的备份替换配置。任务目录不会被删除。", path, func() {
							if scope == s.settingsExtensionCurrentScope() && s.settingsConfigRecoveryVisible() && !state.backupDeleteLoading {
								s.act("config.restore-backup", "", "", "", path, "", nil)
							}
						})
					}
				})
			})
			if reason := state.backupFailures[id]; reason != "" {
				label(c, reason, 11, p.danger).Padding(2, 8, 4, 30).Wrap()
			}
		})
	}
}
