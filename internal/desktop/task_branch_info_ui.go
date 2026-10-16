package desktop

import (
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

type taskBranchInfoState struct {
	TaskID, TaskPath, TaskCreatedAt string
	Open                            bool
}

type taskBranchInfoCopyOption struct {
	Key, Label, Text, Message string
	Disabled                  bool
}

func taskBranchInfoManifest(t task) domain.TaskManifest {
	manifest := domain.TaskManifest{RequirementLink: t.requirementURL}
	for _, item := range t.workspaces {
		manifest.Services = append(manifest.Services, domain.ServiceWorkspace{ServiceName: item.Name, Branch: item.Branch})
	}
	return manifest
}

func taskBranchInfoCopyOptions(t task) []taskBranchInfoCopyOption {
	manifest := taskBranchInfoManifest(t)
	noLink := strings.TrimSpace(t.requirementURL) == ""
	return []taskBranchInfoCopyOption{
		{Key: "services", Label: "服务名称 · 不含需求链接", Text: workspace.FormatTaskServices(manifest, false), Message: "服务已复制（不含需求链接）"},
		{Key: "branches", Label: "服务与分支 · 不含需求链接", Text: workspace.FormatTaskBranchInfo(manifest, false), Message: "分支信息已复制（不含需求链接）"},
		{Key: "services-link", Label: "服务名称 · 含需求链接", Text: workspace.FormatTaskServices(manifest, true), Message: "服务已复制（含需求链接）", Disabled: noLink},
		{Key: "branches-link", Label: "服务与分支 · 含需求链接", Text: workspace.FormatTaskBranchInfo(manifest, true), Message: "分支信息已复制（含需求链接）", Disabled: noLink},
	}
}

func taskBranchInfoMatches(state *taskBranchInfoState, t task, page string, tab int) bool {
	return state != nil && state.Open && (page == "tasks" || page == "archive") && tab == 0 &&
		t.id == state.TaskID && t.path == state.TaskPath && t.createdAt == state.TaskCreatedAt
}

func (s *app) openTaskBranchInfo(t task) {
	if t.id == "" {
		return
	}
	s.taskBranchInfo = &taskBranchInfoState{TaskID: t.id, TaskPath: t.path, TaskCreatedAt: t.createdAt, Open: true}
}

func (s *app) renderTaskBranchInfoDialog(c *ui.Context, p palette) {
	state := s.taskBranchInfo
	t := s.currentTask()
	if !taskBranchInfoMatches(state, t, s.page, s.tab) {
		s.taskBranchInfo = nil
		return
	}
	options := taskBranchInfoCopyOptions(t)
	ui.DialogBase(c.Key("task-branch-info-dialog"), &state.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
		width, height := c.Size()
		panel.Width(minFloat32(1040, maxFloat32(0, width-32))).Height(minFloat32(720, maxFloat32(0, height-32))).Padding(20).Gap(12).Background(p.surface).Border(1, p.line).Radius(16)
		panel.Children(func() {
			label(c, "分支信息", 17, p.text).FontWeight(600).Shrink(0)
			ui.Column(c).Grow(1).MinHeight(0).Gap(12).Children(func() {
				for row := 0; row < 2; row++ {
					ui.Row(c).FillWidth().Grow(1).MinHeight(0).Gap(12).AlignItems(ui.Stretch).Children(func() {
						for _, option := range options[row*2 : row*2+2] {
							ui.Column(c.Key("task-branch-info-cell-"+option.Key)).Grow(1).MinWidth(0).MinHeight(0).Padding(10).Gap(6).
								Background(p.soft).Border(1, p.line).Radius(10).Label("分支信息区域 · " + option.Key).Children(func() {
								ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(6).Shrink(0).Children(func() {
									label(c, option.Label, 11, p.text).FontWeight(600).Grow(1).MinWidth(0).SingleLine().Tooltip(option.Label)
									if iconButtonDisabled(c, "copy", "copy", "复制"+option.Label, p, option.Disabled).Clicked() {
										s.copyTextToClipboard(c.Services(), option.Text, option.Message)
									}
								})
								if option.Disabled {
									label(c, "当前任务没有需求链接", 11, p.muted).Shrink(0)
								}
								ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Label("分支信息预览 · " + option.Key).Children(func() {
									label(c, option.Text, 12, p.text).FillWidth().MinWidth(0).Wrap().Selectable()
								})
							})
						}
					})
				}
			})
			ui.Row(c).Shrink(0).Justify(ui.End).Children(func() {
				if action(c, "task-branch-info-close", "close", "关闭", p, false) {
					state.Open = false
				}
			})
		})
	})
}
