package desktop

import (
	"path"
	"strings"

	"silverwing-go/internal/application"

	"github.com/egoist/mygo/ui"
)

const taskFilesStatusPrefix = "\x00task-files:"

func taskFilesTreeRows(view *taskFilesView) []FileTreeRow {
	var rows []FileTreeRow
	var appendDirectory func(string, int)
	appendDirectory = func(relative string, depth int) {
		dir := view.directories[relative]
		if dir == nil || !dir.loaded {
			name, kind := "正在读取目录…", "loading:"
			if dir != nil && dir.error != "" {
				name, kind = "读取失败，点击重试", "retry:"
			}
			rows = append(rows, FileTreeRow{ID: taskFilesStatusPrefix + kind + relative, Name: name, Depth: depth})
			return
		}
		if len(dir.entries) == 0 && depth > 0 {
			rows = append(rows, FileTreeRow{ID: taskFilesStatusPrefix + "empty:" + relative, Name: "空目录", Depth: depth})
		}
		for _, entry := range dir.entries {
			name := entry.Name
			if entry.RestrictedReason != "" {
				name += " · 受限"
			}
			row := FileTreeRow{ID: entry.Path, Name: name, Path: taskFilesAbsolute(view.root, entry.Path), Depth: depth,
				Directory: entry.Directory && entry.RestrictedReason == ""}
			rows = append(rows, row)
			if row.Directory && view.expanded[entry.Path] {
				appendDirectory(entry.Path, depth+1)
			}
		}
	}
	appendDirectory("", 0)
	return rows
}

func taskFilesStatusRetry(id string) (string, bool) {
	prefix := taskFilesStatusPrefix + "retry:"
	if !strings.HasPrefix(id, prefix) {
		return "", false
	}
	return strings.TrimPrefix(id, prefix), true
}

func (s *app) taskFilesTreeInput(view *taskFilesView, input FileTreeInput) {
	if !s.taskFilesCurrent(view) {
		return
	}
	if input.Kind == FileTreeDrop {
		return
	}
	if relative, retry := taskFilesStatusRetry(input.Row.ID); retry {
		if input.Kind == FileTreeActivate {
			s.requestTaskFilesDirectory(view, relative, true)
		}
		return
	}
	entry, ok := view.entry(input.Row.ID)
	if !ok {
		return
	}
	view.selected = entry.Path
	if input.Kind == FileTreeContext {
		return
	}
	if entry.RestrictedReason != "" {
		view.operationError = entry.RestrictedReason
		return
	}
	if entry.Directory {
		if input.Kind == FileTreeActivate || input.Kind == FileTreeToggle {
			view.expanded[entry.Path] = !view.expanded[entry.Path]
			if view.expanded[entry.Path] {
				s.requestTaskFilesDirectory(view, entry.Path, false)
			}
		}
		return
	}
	if input.Kind == FileTreeActivate || input.Kind == FileTreeSelect {
		s.openTaskFilePreview(view, entry.Path, false)
	}
}

func (s *app) renderTaskFilesTree(c *ui.Context, p palette, view *taskFilesView) {
	rows := taskFilesTreeRows(view)
	// A returning view reloads only visible expanded directories. Descendants of
	// a pending parent do not occur in rows, so this never becomes a tree scan.
	for _, row := range rows {
		if row.Directory && view.expanded[row.ID] {
			dir := view.directory(row.ID)
			if !dir.loaded && !dir.loading && dir.error == "" {
				s.requestTaskFilesDirectory(view, row.ID, false)
			}
		}
	}
	FileTree(c, p, FileTreeProps{Key: "task-files-tree", HideRoot: true,
		Root: FileTreeRow{ID: "@root", Name: path.Base(strings.ReplaceAll(view.identity.taskPath, `\`, "/")), Directory: true},
		Rows: rows, State: &view.tree, Expanded: view.expanded, Selected: map[string]bool{view.selected: true},
		Opened: view.opened, Empty: "目录为空", AcceptFiles: false,
		OnInput: func(input FileTreeInput) { s.taskFilesTreeInput(view, input) },
		ContextMenu: func(row FileTreeRow, menu *ui.Menu) {
			if relative, retry := taskFilesStatusRetry(row.ID); retry {
				if menu.Item("重试读取目录").Chosen() {
					s.requestTaskFilesDirectory(view, relative, true)
				}
				return
			}
			entry, ok := view.entry(row.ID)
			if ok {
				s.taskFilesPathMenu(c, menu, view, entry.Path, &entry)
			}
		},
	})
	if view.revealPath != "" {
		if handle := view.tree.rowHandles[view.revealPath]; handle != nil {
			c.Resolve(*handle).ScrollIntoView()
			view.revealPath = ""
		}
	}
}

func (s *app) taskFilesPathMenu(c *ui.Context, menu *ui.Menu, view *taskFilesView, relative string, entry *application.TaskFileEntry) {
	current := s.taskFilesCurrent(view)
	restricted := entry != nil && entry.RestrictedReason != ""
	if restricted {
		menu.Item(entry.RestrictedReason).Disabled(true)
		menu.Separator()
	}
	if relative != "" {
		if menu.Item("复制相对路径").Disabled(!current).Chosen() && s.taskFilesCurrent(view) {
			s.copyTextToClipboard(c.Services(), relative, "已复制相对路径")
		}
	}
	if menu.Item("复制绝对路径").Disabled(!current || view.root == "").Chosen() && s.taskFilesCurrent(view) {
		s.copyTextToClipboard(c.Services(), taskFilesAbsolute(view.root, relative), "已复制绝对路径")
	}
	menu.Separator()
	if menu.Item("打开所在目录").Disabled(!current || restricted).Chosen() {
		s.taskFilesExternalAction(view, relative, "task.files.reveal")
	}
	if menu.Item("使用系统应用打开").Disabled(!current || restricted).Chosen() {
		s.taskFilesExternalAction(view, relative, "task.files.open")
	}
	if entry != nil && entry.Directory && !restricted {
		menu.Separator()
		if menu.Item("刷新此目录").Disabled(!current).Chosen() {
			s.requestTaskFilesDirectory(view, relative, true)
		}
	}
}
