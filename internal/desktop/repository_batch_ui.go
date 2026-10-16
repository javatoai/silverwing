package desktop

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"silverwing-go/internal/application"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (s *app) openRepositoryBatchDialog() {
	groupID := s.selectedServiceGroupID()
	if groupID == "" {
		s.lastError = "请先创建一个项目组，再添加仓库。"
		return
	}
	s.repositoryBatchOpen = true
	s.repositoryBatchGeneration++
	// Capture the active group at the moment the dialog opens. A later page
	// selection cannot redirect this import into another group.
	s.repositoryBatchGroupID = groupID
	s.repositoryBatchPaths = nil
	s.lastError = ""
}

func (s *app) pickRepositoryBatchDirectories() {
	if s.repositoryBatchPickerBusy || s.win == nil {
		return
	}
	s.repositoryBatchPickerBusy = true
	win := s.win
	generation, home, groupID := s.repositoryBatchGeneration, s.snapshot.Home, s.repositoryBatchGroupID
	win.Invalidate()
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: win, Title: "选择要添加的仓库目录", Message: "可以一次选择多个本地 Git 仓库根目录。",
			Directory: true, Multiple: true,
		})
		if win.IsDestroyed() {
			return
		}
		win.Update(func() {
			s.repositoryBatchPickerBusy = false
			if !s.repositoryBatchOpen || s.repositoryBatchGeneration != generation ||
				s.snapshot.Home != home || s.repositoryBatchGroupID != groupID || !s.hasGroup(groupID) {
				return
			}
			if err != nil {
				s.lastError = err.Error()
				return
			}
			seen := make(map[string]bool, len(s.repositoryBatchPaths)+len(paths))
			for _, existing := range s.repositoryBatchPaths {
				seen[repositoryPathKey(existing)] = true
			}
			for _, selected := range paths {
				path := filepath.Clean(strings.TrimSpace(selected))
				key := repositoryPathKey(path)
				if path == "." || path == "" || seen[key] {
					continue
				}
				seen[key] = true
				s.repositoryBatchPaths = append(s.repositoryBatchPaths, path)
			}
		})
	}()
}

func repositoryPathKey(path string) string {
	return strings.ToLower(filepath.Clean(strings.TrimSpace(path)))
}

func (s *app) submitRepositoryBatch() {
	if s.busy {
		s.lastError = "当前有操作正在执行，请等待完成或取消后重试。"
		return
	}
	if s.repositoryBatchGroupID == "" {
		s.lastError = "请选择项目组。"
		return
	}
	if !s.hasGroup(s.repositoryBatchGroupID) {
		s.lastError = "目标项目组已不存在，请重新打开添加仓库窗口。"
		return
	}
	if len(s.repositoryBatchPaths) == 0 {
		s.lastError = "请选择至少一个仓库目录。"
		return
	}
	paths := append([]string(nil), s.repositoryBatchPaths...)
	groupID := s.repositoryBatchGroupID
	s.lastError = ""
	s.serviceGroupID = groupID
	s.act("service.add-repositories", "", "", "", "", "", map[string]any{
		"groupId": groupID,
		"paths":   paths,
	})
}

func (s *app) repositoryBatchDialog(c *ui.Context, p palette) {
	if !s.repositoryBatchOpen {
		return
	}
	ui.DialogBase(c.Key("repository-batch-add-dialog"), &s.repositoryBatchOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
		w, h := c.Size()
		panel.Width(min(760, w-80)).MaxHeight(min(680, h-32)).Padding(20).Gap(12).Background(p.surface).Border(1, p.line).Radius(16)
		panel.Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				icon(c, "repo", 18, p.blue)
				label(c, "添加仓库", 17, p.text).FontWeight(600).Grow(1)
				if action(c, "repository-batch-close", "close", "关闭", p, false) {
					s.repositoryBatchOpen = false
				}
			})
			targetGroup := s.taskGroup(s.repositoryBatchGroupID)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				label(c, "添加到", 11, p.muted)
				label(c, firstNonEmpty(targetGroup.Name, "项目组已不可用"), 12, p.text).FontWeight(500)
			})
			label(c, "选择一个或多个本地 Git 仓库根目录。重复仓库会跳过，已有配置不会被覆盖。", 12, p.muted).Wrap()
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if action(c, "repository-batch-browse", "folder", "选择目录…", p, true) {
					s.pickRepositoryBatchDirectories()
				}
				if s.repositoryBatchPickerBusy {
					label(c, "正在等待系统文件夹选择器…", 11, p.muted)
				} else {
					label(c, fmt.Sprintf("已选择 %d 个目录", len(s.repositoryBatchPaths)), 11, p.muted)
				}
			})
			ui.Scroll(c).Grow(1).MinHeight(0).MaxHeight(310).Padding(8).Gap(4).Background(p.soft).Border(1, p.line).Radius(9).Children(func() {
				if len(s.repositoryBatchPaths) == 0 {
					label(c, "所选目录会列在这里。", 12, p.muted).Padding(8)
					return
				}
				for index, path := range s.repositoryBatchPaths {
					rowIndex := index
					ui.Row(c).FillWidth().Padding(7, 9).Gap(8).AlignItems(ui.Center).Background(p.surface).Radius(7).Children(func() {
						icon(c, "folder", 15, p.muted)
						label(c, path, 11, p.text).Grow(1).SingleLine().Tooltip(path)
						if iconButton(c, fmt.Sprintf("repository-batch-remove-%d", rowIndex), "delete", "从选择中移除", p).Clicked() {
							s.repositoryBatchPaths = append(s.repositoryBatchPaths[:rowIndex], s.repositoryBatchPaths[rowIndex+1:]...)
						}
					})
				}
			})
			ui.Row(c).FillWidth().Gap(8).Shrink(0).Wrap().Justify(ui.End).Children(func() {
				if action(c, "repository-batch-cancel", "close", "取消", p, false) {
					s.repositoryBatchOpen = false
				}
				if action(c, "repository-batch-submit", "plus", "添加所选仓库", p, true) {
					s.submitRepositoryBatch()
				}
			})
		})
	})
}

func (s *app) repositoryBatchResultDialog(c *ui.Context, p palette) {
	if !s.repositoryBatchResultOpen {
		return
	}
	ui.DialogBase(c.Key("repository-batch-result-dialog"), &s.repositoryBatchResultOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
		w, h := c.Size()
		panel.Width(min(820, w-80)).MaxHeight(min(700, h-32)).Padding(20).Gap(12).Background(p.surface).Border(1, p.line).Radius(16)
		panel.Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				icon(c, "check", 18, p.green)
				label(c, "批量添加结果", 17, p.text).FontWeight(600).Grow(1)
				if action(c, "repository-batch-result-close", "close", "关闭", p, false) {
					s.repositoryBatchResultOpen = false
				}
			})
			label(c, fmt.Sprintf("已加入 %d 个，已跳过 %d 个。", len(s.repositoryBatchResult.Added), len(s.repositoryBatchResult.Skipped)), 13, p.text)
			ui.Scroll(c).Grow(1).MinHeight(0).Padding(10).Gap(9).Background(p.soft).Border(1, p.line).Radius(9).Children(func() {
				if len(s.repositoryBatchResult.Added) > 0 {
					ui.Column(c).FillWidth().Gap(5).Children(func() {
						label(c, "已添加", 12, p.green).FontWeight(600)
						for _, path := range s.repositoryBatchResult.Added {
							ui.Row(c).Gap(7).Children(func() {
								icon(c, "check", 14, p.green)
								label(c, path, 11, p.text).Wrap().Selectable()
							})
						}
					})
				}
				if len(s.repositoryBatchResult.Skipped) > 0 {
					ui.Column(c).FillWidth().Gap(5).Children(func() {
						label(c, "已跳过", 12, p.amber).FontWeight(600)
						for _, skipped := range s.repositoryBatchResult.Skipped {
							ui.Column(c).FillWidth().Padding(8).Gap(3).Background(p.surface).Radius(7).Children(func() {
								label(c, skipped.Path, 11, p.text).Wrap().Selectable()
								label(c, skipped.Reason, 11, p.muted).Wrap().Selectable()
							})
						}
					})
				}
				if len(s.repositoryBatchResult.Added)+len(s.repositoryBatchResult.Skipped) == 0 {
					label(c, "后端没有返回目录处理结果。", 12, p.muted)
				}
			})
		})
	})
}

func (s *app) applyRepositoryBatchResult(data any) {
	encoded, err := json.Marshal(data)
	if err != nil {
		s.lastError = "批量仓库结果格式无效：" + err.Error()
		return
	}
	var result application.RepositoryBatchResult
	if err := json.Unmarshal(encoded, &result); err != nil {
		s.lastError = "批量仓库结果格式无效：" + err.Error()
		return
	}
	if result.Added == nil {
		result.Added = []string{}
	}
	if result.Skipped == nil {
		result.Skipped = []application.RepositoryBatchSkip{}
	}
	s.repositoryBatchOpen = false
	s.repositoryBatchResult = result
	s.repositoryBatchResultOpen = true
}
