package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var configBackupZIPFilters = []mygo.FileFilter{{Name: "配置备份 ZIP", Extensions: []string{"zip"}}}

func configBackupStartDirectory(home string) string {
	directory := filepath.Join(home, "backups")
	if info, err := os.Stat(directory); err == nil && info.IsDir() {
		return directory
	}
	return home
}

func (s *app) backupConfig() {
	home := s.snapshot.Home
	filename := time.Now().Format("silverwing-config-20060102-150405.zip")
	s.chooseSavePath("保存配置备份", filepath.Join(configBackupStartDirectory(home), filename), configBackupZIPFilters, func(path string) {
		if s.snapshot.Home == home && s.settingsConfigRecoveryVisible() {
			s.act("config.export", "", "", "", path, "", nil)
		}
	})
}

func (s *app) restoreConfig() {
	home := s.snapshot.Home
	s.pickNativePath("选择要恢复的配置备份", configBackupStartDirectory(home), false, configBackupZIPFilters, func(path string) {
		if s.snapshot.Home == home && s.settingsConfigRecoveryVisible() {
			s.act("config.import-preview", "", "", "", path, "", nil)
		}
	})
}

func (s *app) pickNativePath(title, start string, directory bool, filters []mygo.FileFilter, selected func(string)) {
	if s.settingsUnit.state.settingsPickerBusy || s.win == nil {
		return
	}
	s.settingsUnit.state.settingsPickerBusy = true
	win := s.win
	win.Invalidate()
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: title, DefaultPath: start, Directory: directory, Filters: filters})
		win.Update(func() {
			s.settingsUnit.state.settingsPickerBusy = false
			if err != nil {
				s.lastError = err.Error()
				return
			}
			if len(paths) > 0 && selected != nil {
				selected(paths[0])
			}
		})
	}()
}

func (s *app) chooseSavePath(title, start string, filters []mygo.FileFilter, selected func(string)) {
	if s.settingsUnit.state.settingsPickerBusy || s.win == nil {
		return
	}
	s.settingsUnit.state.settingsPickerBusy = true
	win := s.win
	win.Invalidate()
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: win, Title: title, DefaultPath: start, Filters: filters})
		win.Update(func() {
			s.settingsUnit.state.settingsPickerBusy = false
			if err != nil {
				s.lastError = err.Error()
				return
			}
			if path != "" && selected != nil {
				selected(path)
			}
		})
	}()
}

func (s *app) confirmNative(title, message, detail string, confirmed func()) {
	s.showConfirmationDialog(title, message, detail, []confirmationDialogButton{
		{label: "继续", primary: true},
		{label: "取消"},
	}, 1, 1, func(button int) {
		if button == 0 && confirmed != nil {
			confirmed()
		}
	})
}

func (s *app) settingsImportDialog(c *ui.Context, p palette) {
	state := s.settingsUnit.state.settingsConfigRecovery
	if state == nil || state.preview == nil || !state.previewOpen {
		return
	}
	ui.DialogBase(c.Key("settings-import-preview"), &state.previewOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
		w, h := c.Size()
		panel.Width(min(720, w-80)).Height(min(640, h-32)).Padding(20).Gap(12).Background(p.surface).Border(1, p.line).Radius(16)
		panel.Children(func() {
			label(c, "配置恢复预览", 17, p.text).FontWeight(600).Shrink(0)
			label(c, state.preview.Path, 10, p.muted).SingleLine().Shrink(0)
			ui.Scroll(c).Grow(1).MinHeight(0).Padding(12).Background(p.soft).Radius(8).Children(func() {
				label(c, state.previewText, 11, p.text).LineHeight(1.45).Wrap().Selectable()
			})
			ui.Row(c).FillWidth().Gap(8).Shrink(0).Wrap().Justify(ui.End).Children(func() {
				if action(c, "settings-import-cancel", "close", "取消", p, false) {
					state.previewOpen = false
				}
				if action(c, "settings-import-apply", "arrow", "确认恢复", p, true) {
					s.confirmSettingsConfigImport()
				}
			})
		})
	})
}

func (s *app) settingsSkillPreviewDialog(c *ui.Context, p palette) {
	if !s.settingsUnit.state.settingsSkillPreviewOpen {
		return
	}
	state := s.settingsUnit.state.settingsExtensionRequests
	markdownPreview := state != nil && state.previewMarkdown != nil
	ui.DialogBase(c.Key("settings-skill-preview"), &s.settingsUnit.state.settingsSkillPreviewOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
		w, h := c.Size()
		panel.Width(min(900, w-80)).Height(min(760, h-32)).Padding(20).Gap(12).Background(p.surface).Border(1, p.line).Radius(16)
		panel.Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				label(c, firstNonEmpty(s.settingsUnit.state.settingsSkillPreviewName, "Skill 预览"), 16, p.text).FontWeight(600).Grow(1)
				if markdownPreview {
					s.renderReadonlyMarkdownReaderActions(c, p, state.previewMarkdown, func(menu *overflowMenuBuilder) {
						if menu.Item("copy-preview", "复制预览内容").Icon("copy").Chosen() {
							s.copyTextToClipboard(c.Services(), s.settingsUnit.state.settingsSkillPreviewText, "预览内容已复制")
						}
						if copyAction, ok := s.settingsExtensionPreviewFileAction(); ok {
							if menu.Item("copy-preview-path", "复制文件路径").Icon("file").Chosen() {
								s.copyTextToClipboard(c.Services(), state.previewPath, "文件路径已复制")
							}
							if menu.Item("copy-preview-file", "复制文件引用").Icon("copy").Chosen() {
								s.dispatch(copyAction)
							}
						}
					})
				} else {
					if iconButton(c, "settings-skill-preview-copy", "copy", "复制预览内容", p).Clicked() {
						s.copyTextToClipboard(c.Services(), s.settingsUnit.state.settingsSkillPreviewText, "预览内容已复制")
					}
					if copyAction, ok := s.settingsExtensionPreviewFileAction(); ok {
						if iconButton(c, "settings-skill-preview-copy-path", "file", "复制文件路径", p).Clicked() {
							s.copyTextToClipboard(c.Services(), state.previewPath, "文件路径已复制")
						}
						if iconButton(c, "settings-skill-preview-copy-file", "copy", "复制文件引用", p).Clicked() {
							s.dispatch(copyAction)
						}
					}
				}
				if action(c, "settings-skill-preview-close", "close", "关闭", p, false) {
					s.settingsUnit.state.settingsSkillPreviewOpen = false
				}
			})
			ui.Column(c).Grow(1).MinHeight(260).Padding(14).Background(p.soft).Border(1, p.line).Radius(10).Children(func() {
				state := s.settingsUnit.state.settingsExtensionRequests
				if state != nil && state.previewLoading {
					label(c, "正在读取预览…", 12, p.muted)
				} else if state != nil && state.previewError != "" {
					label(c, "预览读取失败："+state.previewError, 12, p.danger).Wrap().Selectable()
					if retry, ok := s.settingsExtensionPreviewRetryAction(); ok {
						if action(c, "settings-skill-preview-retry", "refresh", "重新读取", p, false) {
							s.dispatch(retry)
						}
					}
				} else if state != nil && state.previewMarkdown != nil {
					s.renderReadonlyMarkdownReaderContent(c, p, state.previewMarkdown)
				} else if s.settingsUnit.state.settingsSkillPreviewText == "" {
					label(c, "没有可显示的预览内容。", 12, p.muted)
				} else {
					ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
						label(c, s.settingsUnit.state.settingsSkillPreviewText, 12, p.text).LineHeight(1.55).Selectable()
					})
				}
			})
		})
	})
}

func settingsOutputPanel(c *ui.Context, p palette, value string) {
	ui.Scroll(c).FillWidth().Height(110).Padding(11).Background(p.soft).Border(1, p.line).Radius(8).Children(func() {
		label(c, value, 10, p.text).Font("Consolas").LineHeight(1.45).Selectable()
	})
}

func (s *app) settingsActionForm(form actionForm, payload map[string]any) bool {
	switch form.kind {
	case "settings.meegle-project.add":
		s.lastError = "请从 Meegle 项目目录中选择项目空间后添加。"
		return true
	case "skills.sources/save":
		name, repository := strings.TrimSpace(payloadString(payload, "name")), strings.TrimSpace(payloadString(payload, "repositoryUrl"))
		if name == "" || repository == "" {
			s.lastError = "Skill 来源名称和 Git 仓库地址不能为空。"
			return true
		}
		ref := strings.TrimSpace(payloadString(payload, "ref"))
		root := strings.TrimSpace(payloadString(payload, "skillRoot"))
		var refValue, rootValue any
		if ref != "" {
			refValue = ref
		}
		if root != "" {
			rootValue = root
		}
		source := map[string]any{"id": newSettingsSourceID(), "name": name, "repositoryUrl": repository, "ref": refValue, "skillRoot": rootValue}
		s.dialogOpen = false
		s.confirmNative("添加 Skill 来源", "将克隆此 Git 仓库并读取其中的 SKILL.md。", repository, func() {
			s.act("skills.sources/save", "", "", "", "", "", map[string]any{"source": source, "confirmed": true})
		})
		return true
	}
	return false
}
