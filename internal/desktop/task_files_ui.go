package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	code "github.com/egoist/mygo/ui/editor"
)

func (s *app) renderTaskFiles(c *ui.Context, p palette) {
	view := s.ensureTaskFilesCurrent()
	if view == nil {
		label(c, "当前任务没有可读取的工作目录。", 13, p.muted).Padding(18).Wrap()
		return
	}
	root := ui.Column(c.Key(view.identity.documentID(""))).Bind(&view.canvas).Grow(1).FillWidth().MinHeight(0).MinWidth(0)
	// This surface has no save/drop owner. Consume the host's save shortcut so
	// a focused read-only preview cannot invoke another page's editor save.
	root.OnShortcut(ui.Ctrl, ui.KeyS, func() {}).OnShortcut(ui.Super, ui.KeyS, func() {})
	root.Children(func() {
		width := view.canvas.Bounds(c).W
		if width <= 0 {
			width, _ = c.Size()
		}
		view.treeWidth = max(160, min(view.treeWidth, max(160, width-280)))
		ui.Split(c.Key("task-files-split"), &view.treeWidth, func() {
			s.renderTaskFilesSidebar(c, p, view)
		}, func() {
			s.renderTaskFilesDocument(c, p, view)
		}).Fill().MinWidth(0).MinHeight(0)
	})
}

func (s *app) renderTaskFilesSidebar(c *ui.Context, p palette, view *taskFilesView) {
	ui.Column(c.Key("task-files-sidebar")).Fill().MinWidth(0).MinHeight(0).Background(p.side).Children(func() {
		ui.Row(c.Key("task-files-toolbar")).Height(36).FillWidth().Padding(0, 8).Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Box(c.Key("task-files-query-field")).Height(30).Grow(1).MinWidth(0).Children(func() {
				input := ui.TextInput(c.Key("task-files-query"), &view.query).Label("搜索文件名或相对路径").Placeholder("文件名或相对路径").Fill().MinWidth(0).Padding(0, 28, 0, 8)
				input.OnChange(func() { s.changeTaskFilesSearchQuery(view) })
				input.OnShortcut(0, ui.KeyEnter, func() { s.requestTaskFilesSearch(view) })
				input.OnShortcut(0, ui.KeyEscape, func() { s.cancelTaskFilesSearch(view) })
				if miniButton(c, p, "task-files-search", "search", "搜索任务文件", false).Absolute().Right(1).Top(1).Width(24).Height(28).Clicked() {
					s.requestTaskFilesSearch(view)
				}
			})
			if miniButton(c, p, "task-files-refresh", "refresh", "刷新任务文件", false).Width(24).Clicked() {
				s.refreshTaskFiles(view)
			}
			miniButton(c, p, "task-files-root-menu", "more", "工作目录操作", false).Width(24).Menu(func(menu *ui.Menu) {
				s.taskFilesPathMenu(c, menu, view, "", nil)
			})
		})
		if view.operationError != "" {
			label(c, view.operationError, 11, p.amber).Padding(5, 10).Wrap()
		}
		if view.operationMessage != "" {
			label(c, view.operationMessage, 11, p.muted).Padding(4, 10).Wrap()
		}
		if view.searching || view.searchDone || view.searchError != "" {
			s.renderTaskFilesSearch(c, p, view)
		} else {
			root := view.directory("")
			if root.error != "" {
				label(c, root.error, 11, p.amber).Padding(5, 10).Wrap()
			}
			s.renderTaskFilesTree(c, p, view)
		}
	})
}

func (s *app) renderTaskFilesSearch(c *ui.Context, p palette, view *taskFilesView) {
	ui.Row(c).FillWidth().Padding(4, 9).Gap(5).AlignItems(ui.Center).Children(func() {
		text := fmt.Sprintf("%d 个结果", len(view.search.Entries))
		if view.searching {
			text = "正在搜索…"
		}
		label(c, view.searchQuery+" · "+text, 11, p.muted).SingleLine().Grow(1).MinWidth(0).Tooltip(view.searchQuery)
		if ui.Button(c.Key("task-files-search-back"), map[bool]string{true: "取消", false: "返回目录"}[view.searching]).Clicked() {
			s.cancelTaskFilesSearch(view)
		}
	})
	if view.searchError != "" {
		label(c, view.searchError, 12, p.amber).Padding(8).Wrap()
		if ui.Button(c, "重试搜索").Clicked() {
			s.requestTaskFilesSearch(view)
		}
	}
	if view.search.Truncated {
		label(c, "仅显示前 500 项，请缩小搜索范围。", 11, p.amber).Padding(5, 9).Wrap()
	}
	if len(view.search.Issues) > 0 {
		label(c, fmt.Sprintf("%d 个目录无法读取，其余结果仍可查看。", len(view.search.Issues)), 11, p.amber).Padding(5, 9).Wrap().Tooltip(view.search.Issues[0].Reason)
	}
	if view.searchDone && len(view.search.Entries) == 0 {
		label(c, "没有匹配的文件或目录。", 12, p.muted).Padding(10).Wrap()
	}
	ui.Scroll(c.Key("task-files-search-results")).TrackScroll(&view.searchScroll).Grow(1).FillWidth().MinHeight(0).Children(func() {
		for _, entry := range view.search.Entries {
			item := entry
			button := ui.ButtonBase(c.Key("task-files-result:"+entry.Path)).FillWidth().MinHeight(44).Padding(6, 10).Radius(4).Tooltip(entry.Path).Label(entry.Path)
			if button.Hovered() || view.selected == entry.Path {
				button.Background(p.selected)
			}
			button.Children(func() {
				name := item.Name
				if item.RestrictedReason != "" {
					name += " · 受限"
				}
				label(c, name, 12, p.text).SingleLine().FillWidth()
				label(c, item.Path, 10, p.muted).SingleLine().FillWidth()
			})
			button.ContextMenu(func(menu *ui.Menu) { s.taskFilesPathMenu(c, menu, view, item.Path, &item) })
			if button.DoubleClicked() || button.Clicked() {
				s.locateTaskFilesEntry(view, item)
			}
		}
	})
}

func (s *app) renderTaskFilesDocument(c *ui.Context, p palette, view *taskFilesView) {
	ui.Column(c.Key("task-files-document")).Fill().MinWidth(0).MinHeight(0).Background(p.surface).Children(func() {
		if view.opened == "" {
			ui.Column(c).Grow(1).FillWidth().Padding(20).Gap(7).Children(func() {
				label(c, "选择文件查看内容", 15, p.text)
				label(c, "点击左侧文件查看内容。此处只读取当前任务工作目录。", 12, p.muted).Wrap()
			})
			return
		}
		ui.Row(c.Key("task-files-preview-heading")).FillWidth().MinHeight(34).Padding(4, 10).Gap(4).AlignItems(ui.Center).Children(func() {
			label(c, view.opened, 11, p.muted).SingleLine().Grow(1).MinWidth(0).Tooltip(taskFilesAbsolute(view.root, view.opened))
			if miniButton(c, p, "task-file-reread", "refresh", "重新读取文件", false).Clicked() {
				s.openTaskFilePreview(view, view.opened, true)
			}
			if preview := view.preview; preview != nil && (preview.reader != nil || preview.code.view != nil) {
				if miniButton(c, p, "task-file-find", "search", "查找正文", false).Clicked() {
					if preview.reader != nil {
						preview.reader.Find().Open = true
					} else {
						preview.code.view.Run(code.Find)
					}
				}
			}
			miniButton(c, p, "task-file-preview-menu", "more", "文件操作", false).Menu(func(menu *ui.Menu) {
				entry, found := view.entry(view.opened)
				if !found {
					entry.Path = view.opened
				}
				s.taskFilesPathMenu(c, menu, view, view.opened, &entry)
			})
		})
		s.renderTaskFilePreview(c, p, view)
	})
}
