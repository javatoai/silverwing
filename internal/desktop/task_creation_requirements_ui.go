package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (s *app) renderTaskCreationRequirements(c *ui.Context, p palette) {
	form := s.taskUnit.state.taskCreation
	state := s.ensureTaskCreationRequirements()
	if form == nil || state == nil || state.closed {
		return
	}
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		label(c, "需求", 12, p.muted)
		var combo ui.ComboboxParts
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			combo = ui.ComboboxBase(c.Key("task-create-requirement"), &state.query)
			combo.Input.Grow(1).MinWidth(0).FillWidth().Height(36).Padding(0, 30, 0, 9).
				Placeholder("粘贴需求链接，或搜索标题").Background(p.surface).Border(1, p.line).Radius(8).
				Label("需求链接或搜索").Tooltip(firstNonEmpty(state.selectedTitle, taskCreationRequirementTitle(form.draft.RequirementTitle), "粘贴完整需求链接或输入标题搜索")).
				OnChange(s.editTaskCreationRequirementInput)
			if form.taskFieldFocus == "requirement" {
				combo.Input.Focus().ScrollIntoView()
				form.taskFieldFocus = ""
			}
			drawTaskComboboxChevron(combo.Input, p)
			refreshLabel := "刷新需求候选"
			if state.candidatesError != "" {
				refreshLabel = "重试读取需求候选"
			}
			if iconButtonDisabled(c, "task-create-requirement-candidate-refresh", "refresh", refreshLabel, p, state.candidatesLoading).Clicked() {
				state.candidatesLoaded = false
				state.candidatesError, state.candidatesWarning = "", ""
				s.startTaskCreationRequirementCandidates(form, state)
			}
		})
		combo.Popup(func(panel ui.Element) {
			panel.Padding(4).MaxHeight(280).Background(p.surface).Border(1, p.line).Radius(9).Shadow(0, 5, 16, 0, ui.RGBA(0, 0, 0, 0.14))
			ui.Scroll(c).FillWidth().MaxHeight(272).Padding(2).Gap(3).Children(func() {
				options := state.candidates
				if combo.Filtering() {
					options = filterTaskCreationRequirementCandidates(options, state.query)
				}
				for index, candidate := range options {
					candidate := candidate
					value := fmt.Sprintf("%s\x00%s\x00%d", candidate.URL, candidate.Title, index)
					item := combo.Item(value).FillWidth().Height(46).Padding(6, 8).Radius(6)
					if item.Highlighted() {
						item.Background(p.selected)
					}
					item.Children(func() {
						ui.Column(c).FillWidth().Gap(2).Children(func() {
							label(c, firstNonEmpty(candidate.Title, "未读取到需求标题"), 11, p.text).SingleLine().Tooltip(candidate.Title)
							label(c, candidate.URL, 9, p.muted).SingleLine().Tooltip(candidate.URL)
						})
					})
				}
				if len(options) == 0 {
					message := "没有匹配的需求，可直接填写完整链接。"
					if state.candidatesLoading {
						message = "正在读取需求候选…"
					} else if len(form.config.MeegleProjects) == 0 {
						message = "未配置 Meegle 项目，可直接填写完整链接。"
					}
					label(c, message, 10, p.muted).Padding(6, 8).Wrap()
				}
			})
		})
		if chosen, ok := combo.Chosen(); ok {
			parts := strings.SplitN(chosen, "\x00", 3)
			if len(parts) >= 2 {
				s.selectTaskCreationRequirementCandidate(taskCreationRequirementCandidate{URL: parts[0], Title: parts[1]})
			}
		}
		state.candidatesOpen = combo.Open()
		if !state.inputIsQuery {
			if title := firstNonEmpty(taskCreationRequirementTitle(form.draft.RequirementTitle), state.selectedTitle); title != "" {
				label(c, title, 11, p.muted).FillWidth().Wrap()
			}
		}
		if state.inputIsQuery {
			label(c, "请选择匹配需求，或填写完整需求链接。", 10, p.muted).Wrap()
		}
		if state.candidatesError != "" {
			label(c, state.candidatesError, 10, p.amber).Wrap()
		} else if state.candidatesWarning != "" {
			label(c, state.candidatesWarning, 10, p.amber).Wrap()
		}
	})
}

func taskCreationRequirementTitle(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func filterTaskCreationRequirementCandidates(values []taskCreationRequirementCandidate, query string) []taskCreationRequirementCandidate {
	if strings.TrimSpace(query) == "" {
		return values
	}
	needle := strings.ToLower(query)
	filtered := make([]taskCreationRequirementCandidate, 0, len(values))
	for _, candidate := range values {
		if strings.Contains(strings.ToLower(candidate.Title), needle) || strings.Contains(strings.ToLower(candidate.URL), needle) {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}
