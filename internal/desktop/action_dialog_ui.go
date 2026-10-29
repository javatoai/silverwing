package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
)

func (s *app) actionDialog(c *ui.Context, p palette) {
	if s.renderTaskCreationDialog(c, p) {
		return
	}
	if !s.dialogOpen {
		return
	}
	ui.DialogBase(c.Key("action-dialog"), &s.dialogOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.32))
		w, h := c.Size()
		panel.Width(min(700, w-32)).MaxHeight(h-32).Padding(20).Gap(14).Background(p.surface).Border(1, p.line).Radius(16).Children(func() {
			label(c, s.form.title, 20, p.text).FontWeight(600)
			ui.Scroll(c.Key("action-dialog-body")).FillWidth().MaxHeight(max(180, h-220)).Gap(12).Children(func() {
				for index := range s.form.fields {
					field := &s.form.fields[index]
					value := s.input("form:"+field.key, field.value)
					ui.Column(c).FillWidth().Gap(6).Children(func() {
						ui.Row(c).FillWidth().Gap(8).Children(func() {
							label(c, field.label, 12, p.muted).Grow(1)
							if field.secret {
								visible := s.settingsBoolInput("form-secret:"+s.form.kind+":"+field.key, false)
								if action(c, "form-secret-toggle-"+field.key, "eye", map[bool]string{true: "隐藏", false: "显示"}[*visible], p, false) {
									*visible = !*visible
								}
							}
						})
						if field.multiline {
							ui.TextArea(c.Key("action-form-"+field.key), value).FillWidth().Height(160).Padding(10).Border(1, p.line).Radius(8)
						} else if len(field.options) > 0 {
							ui.Row(c).Gap(6).Wrap().Children(func() {
								for _, option := range field.options {
									choice := option
									s.settingsChoice(c, p, "form-"+field.key+"-"+choice, choice, *value == choice, func() { *value = choice })
								}
							})
						} else {
							input := ui.TextInput(c.Key("action-form-"+field.key), value).FillWidth().Height(38).Placeholder(firstNonEmpty(field.placeholder, field.label))
							if field.secret && !*s.settingsBoolInput("form-secret:"+s.form.kind+":"+field.key, false) {
								input.Password()
							}
						}
						if field.error != "" {
							label(c, field.error, 11, p.danger).Wrap()
						}
						if (s.form.kind == "skills.sources/save" || s.form.kind == "codex.extensions/marketplaces/add-source") && field.key == "repositoryUrl" {
							requests := s.ensureSettingsExtensionRequests()
							if requests.branchError != "" {
								label(c, requests.branchError, 11, p.danger).Wrap()
							}
							if action(c, "source-read-branches", "branch", "读取资料来源分支", p, false) {
								kind := "skills.sources/branches/list"
								if s.form.kind == "codex.extensions/marketplaces/add-source" {
									kind = "codex.extensions/branches/list"
								}
								s.act(kind, "", "", "", "", "", map[string]any{"repositoryUrl": *value})
							}
						}
						if field.key == "ref" {
							ui.Row(c).FillWidth().Gap(5).Wrap().Children(func() {
								for i, branch := range s.settingsUnit.state.settingsBranchChoices {
									if action(c, fmt.Sprintf("source-branch-%d", i), "branch", branch, p, *value == branch) {
										*value = branch
									}
								}
							})
						}
					})
				}
			})
			ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Children(func() {
				if action(c, "action-dialog-cancel", "close", "取消", p, false) {
					s.dialogOpen = false
				}
				if action(c, "action-dialog-submit", "check", "确认", p, true) {
					s.submitForm()
				}
			})
		})
	})
}
