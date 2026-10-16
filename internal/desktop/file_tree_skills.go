package desktop

import (
	"path/filepath"

	"github.com/egoist/mygo/ui"
)

func skillFileTreeRows(tree []*skillTreeNode, expanded map[string]bool) []FileTreeRow {
	rows := make([]FileTreeRow, 0)
	var visit func([]*skillTreeNode, int)
	visit = func(nodes []*skillTreeNode, depth int) {
		for _, node := range nodes {
			rows = append(rows, FileTreeRow{ID: node.path, Name: node.name, Path: node.path, Directory: node.directory, Depth: depth})
			if node.directory {
				if _, known := expanded[node.path]; !known {
					expanded[node.path] = true
				}
			}
			visit(node.children, depth+1)
		}
	}
	visit(tree, 0)
	return rows
}

func (s *app) renderSkillsFileTree(c *ui.Context, p palette, state *skillsBrowserState) {
	rows := skillFileTreeRows(makeSkillTree(state.files), state.expanded)
	if state.trees == nil {
		state.trees = make(map[string]*FileTreeState)
	}
	if state.trees[state.selectedDirectory] == nil {
		state.trees[state.selectedDirectory] = &FileTreeState{}
	}
	if state.treeSelected == nil {
		state.treeSelected = make(map[string]bool)
		if state.selectedPath != "" {
			state.treeSelected[state.selectedPath] = true
			state.treeAnchor = state.selectedPath
		}
	}
	copyFile := func(path string) {
		s.act("skills.files/copy", "", "", "", "", "", map[string]any{"directoryName": state.selectedDirectory, "relativePath": path})
	}
	FileTree(c, p, FileTreeProps{
		Key:  "skills-file-tree-" + state.selectedDirectory,
		Root: FileTreeRow{ID: "@root", Name: state.selectedDirectory, Path: filepath.Join(state.root, state.selectedDirectory), Directory: true},
		Rows: rows, Expanded: state.expanded, Selected: state.treeSelected, Opened: state.selectedPath,
		State: state.trees[state.selectedDirectory], Empty: "此 Skill 没有可浏览的文件。",
		SuppressTooltip: s.dialogOpen || s.overflowMenus != nil && s.overflowMenus.openKey != "",
		OnInput: func(input FileTreeInput) {
			switch input.Kind {
			case FileTreeToggle:
				state.expanded[input.Row.ID] = !state.expanded[input.Row.ID]
			case FileTreeSelect, FileTreeActivate:
				state.treeSelected, state.treeAnchor = fileTreeSelection(fileTreeVisibleRows(rows, state.expanded), state.treeSelected, state.treeAnchor, input.Row.ID, input.Modifiers)
				if input.Kind != FileTreeActivate || input.Modifiers&(ui.Ctrl|ui.Super|ui.Shift) != 0 {
					return
				}
				if input.Row.Directory {
					if input.ClickCount == 1 {
						state.expanded[input.Row.ID] = !state.expanded[input.Row.ID]
					}
					return
				}
				for _, file := range state.files {
					if mapString(file, "relativePath") == input.Row.ID {
						s.selectBrowserFile(file)
						state.layout.compactPane = skillsBrowserDocumentPane
						return
					}
				}
			}
		},
		ContextMenu: func(row FileTreeRow, menu *ui.Menu) {
			if !row.Directory && menu.Item("复制文件").Icon(icons["copy"]).Disabled(s.backend == nil).Chosen() {
				copyFile(row.ID)
			}
		},
		RowActionWidth: 22,
		RowActions: func(row FileTreeRow) {
			if fileTreeIconButton(c, p, "skill-tree-copy-"+state.selectedDirectory+"-"+row.ID, "copy", "复制文件").Clicked() {
				copyFile(row.ID)
			}
		},
		RootActionWidth: 44,
		RootActions: func() {
			if fileTreeIconButton(c, p, "skill-tree-refresh-"+state.selectedDirectory, "refresh", "刷新 Skill 文件").Clicked() {
				s.requestSkillsFiles(state.selectedDirectory)
			}
			if fileTreeIconButton(c, p, "skill-tree-collapse-"+state.selectedDirectory, "collapse-folders", "收起全部文件夹").Clicked() {
				for directory := range state.expanded {
					state.expanded[directory] = false
				}
			}
		},
	})
}
