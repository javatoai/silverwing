package desktop

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"silverwing-go/internal/application"

	"github.com/egoist/mygo/ui"
)

const (
	materialsDirectoryMinWidth        = float32(180)
	materialsDirectoryMaxWidth        = float32(420)
	materialsDirectoryMinPreviewWidth = float32(360)
	materialsDirectoryHandleWidth     = float32(4)
)

func (s *app) materials(c *ui.Context, p palette) {
	t := s.taskUnit.currentTask()
	if t.id == "" {
		s.emptyTask(c, p)
		return
	}
	if !t.materialsReady() {
		s.materialsUnavailable(c, p, t)
		return
	}
	s.observeMaterialsWindowRefresh()
	s.materialsUnit.ensureMaterialsPickerState()
	c.OnShortcut(ui.Ctrl, ui.KeyP, func() { s.materialsUnit.openMaterialsQuickPicker() })
	c.OnShortcut(ui.Ctrl|ui.Shift, ui.KeyF, func() { s.openMaterialsFullTextSearch() })
	c.OnShortcut(ui.Ctrl, ui.KeyF, func() { s.openDocumentFind(c) })
	c.OnShortcut(ui.Super, ui.KeyF, func() { s.openDocumentFind(c) })
	c.OnShortcut(0, ui.KeyF3, func() { s.moveDocumentFind(1) })
	c.OnShortcut(ui.Shift, ui.KeyF3, func() { s.moveDocumentFind(-1) })
	c.OnShortcut(ui.Ctrl, ui.KeyW, func() {
		if s.materialsUnit.state.documentPath != "" {
			s.closeDocumentTab(s.materialsUnit.state.documentPath)
		}
	})
	if !s.materialsUnit.state.documentsLoaded && !s.materialsUnit.state.documentsRequested && !s.materialsFileReadBlocked() {
		s.loadDocuments()
	}
	s.prepareDocumentFind()
	s.initializeMaterialsTreeExpansion()
	materialsRow := ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch)
	materialsDropOver := materialsRow.FileDragOver()
	if materialsDropOver {
		materialsRow.Border(1, p.blue)
	}
	availableWidth := materialsRow.Bounds().W
	if availableWidth <= 0 {
		availableWidth, _ = c.Size()
	}
	materialsRow.Children(func() {
		if !s.materialsUnit.state.documentsCollapsed {
			s.documentTree(c, p, clampMaterialsDirectoryWidth(s.materialsDirectoryWidth(), availableWidth))
			s.materialsDirectoryResizeHandle(c, p, availableWidth)
		}
		ui.Column(c).Grow(1).MinWidth(0).Background(p.surface).Children(func() {
			s.documentToolbar(c, p)
			if s.materialsUnit.state.documentKind == "unsupported" {
				ui.Column(c).Grow(1).FillWidth().Center().Gap(12).Padding(24).Children(func() {
					icon(c, "file", 28, p.muted)
					label(c, filepath.Base(s.materialsUnit.state.documentPath), 14, p.text).FontWeight(600)
					label(c, "此格式暂不支持预览，请使用系统应用打开。", 13, p.muted)
					if action(c, "unsupported-open-external", "arrow", "使用系统应用打开", p, false) {
						s.act("document.open", t.id, "", "", s.materialsUnit.state.documentPath, "", nil)
					}
				})
				return
			}
			if s.materialsUnit.state.documentKind == "pdf" || s.materialsUnit.state.documentKind == "image" {
				s.renderNativeMediaControls(c, p)
				// Media owns its scroll viewport. An outer text Scroll gives its
				// content an automatic height and leaves the inner List unbounded.
				viewport := ui.Column(c).FillWidth().Grow(1).MinHeight(0)
				bounds := viewport.Bounds()
				viewport.Children(func() {
					s.renderNativeMedia(c, p, bounds)
				})
				return
			}
			if s.materialsTextEditingVisible() {
				s.renderMaterialsTextEditor(c, p)
				return
			}
			if s.materialsTextPreviewVisible() {
				if documentKindForPath(s.materialsUnit.state.documentPath) == "markdown" {
					s.renderMarkdownSourceContent(c, p)
				} else {
					s.renderDetectedMaterialsText(c, p)
				}
				return
			}
			// Match the source reader's 12dp pane inset plus 18dp vertical / 26dp
			// horizontal native Markdown reader padding.
			ext := strings.ToLower(filepath.Ext(s.materialsUnit.state.documentPath))
			markdown := ext == ".md" || ext == ".markdown"
			scrollID := "text"
			if markdown {
				scrollID = "markdown-native"
				if s.readingMode() == "SOURCE" {
					scrollID = "source-vertical"
				}
			}
			preview := ui.Scroll(c.Key("materials-document-viewport")).TrackScroll(s.readingScrollState(scrollID)).Grow(1).MinHeight(0)
			preview.Draw(func(_ *ui.Painter, rect ui.Rect) {
				s.ensureDocumentFindState().viewport = rect
			})
			if markdown && s.readingMode() == "SOURCE" {
				// The source screen adds 12dp around the pane, then 15dp around its
				// monospaced source content. Rendered Markdown instead adds 18dp
				// vertical and 26dp horizontal inside the same 12dp pane inset.
				preview = preview.Padding(12)
			} else {
				preview = preview.Padding(30, 38)
			}
			if markdown && s.readingMode() == "RENDERED" {
				preview = preview.HandleInput(s.handleMarkdownZoomInput)
			}
			if markdown {
				state := s.ensureMarkdownEditor()
				s.bindMaterialsReadingContextMenu(state)
				preview.ContextMenu(func(menu *ui.Menu) { menu.EditItems(); state.contextMenu(c, menu) })
			}
			s.applyDocumentFindNavigation(scrollID)
			preview.Children(func() {
				if s.materialsUnit.state.documentPath == "" {
					label(c, "选择一份任务资料，或从工具栏新建文档。", 13, p.muted)
					return
				}
				if s.materialsUnit.state.documentContent == "" && !s.materialsUnit.state.documentReadPending {
					label(c, "文档内容为空。", 13, p.muted)
					return
				}
				if !markdown && s.materialsUnit.state.documentEditing {
					ui.TextArea(c.Key("document-editor-"+s.materialsUnit.state.documentPath), &s.materialsUnit.state.documentContent).FillWidth().Height(540).Padding(14).Font("Consolas").FontSize(13).Background(p.surface).Border(1, p.line).Radius(8).OnChange(func() {
						s.materialsUnit.state.documentDirty = true
						s.materialsUnit.state.documentDrafts[s.materialsUnit.state.documentPath] = s.materialsUnit.state.documentContent
						s.materialsUnit.state.documentDirtyMap[s.materialsUnit.state.documentPath] = true
					})
					return
				}
				if s.renderMaterialsSearchSourcePreview(c, p) {
					return
				}
				switch {
				case markdown:
					if s.readingMode() == "SOURCE" {
						s.renderMarkdownSourceContent(c, p)
					} else {
						s.renderMarkdownDocument(c, p)
					}
				case ext == ".csv":
					s.renderCSVPreview(c, p, s.materialsUnit.state.documentContent)
				}
			})
		})
	})
	if dropped := materialsRow.DroppedFiles(); len(dropped) > 0 {
		state := s.ensureMaterialsFileManagementState()
		_, relative := s.materialsCurrentDirectory(state)
		s.startMaterialsImport(dropped, "document.drop", relative)
	}
	s.materialQuickPickerDialog(c, p)
	s.materialFullTextSearchDialog(c, p)
}

func (s *app) handleMarkdownZoomInput(event ui.InputEvent) bool {
	if event.Kind != ui.InputScroll || event.Mods&ui.Ctrl == 0 || event.DY == 0 {
		return false
	}
	step := -0.1
	if event.DY < 0 {
		step = 0.1
	}
	zoom := math.Round((s.readingMarkdownZoom()+step)*10) / 10
	s.setReadingMarkdownZoom(zoom)
	return true
}

func (s *app) materialsUnavailable(c *ui.Context, p palette, t task) {
	configured := strings.TrimSpace(s.configString("requirementMaterialsRoot")) != "" && strings.TrimSpace(s.configString("requirementMaterialsSubdirectory")) != ""
	ui.Column(c).Grow(1).FillWidth().Center().Gap(10).Padding(24).Background(p.surface).Border(1, p.line).Radius(16).Children(func() {
		icon(c, "folder", 26, p.muted)
		title := "任务资料目录尚未关联"
		if t.materialsFailed() {
			title = "任务资料关联失败"
		}
		label(c, title, 16, p.text).FontWeight(600)
		message := "当前任务没有需求链接，暂时无法关联资料目录。"
		if t.requirementURL != "" && !configured {
			message = "请先配置任务资料根目录和子目录。"
		} else if t.materialsFailed() && strings.TrimSpace(t.materialsFailureReason) != "" {
			message = t.materialsFailureReason
		} else if t.requirementURL != "" {
			message = "关联后可在此浏览和编辑任务资料。"
		}
		label(c, message, 12, p.muted)
		if t.requirementURL != "" && !configured && action(c, "materials-open-settings", "settings", "配置任务资料", p, false) {
			s.page, s.settingsUnit.state.settingsTab, s.settingsUnit.state.settingsPage, s.settingsUnit.state.settingsLoaded = "settings", "存储", "paths", ""
		} else if t.requirementURL != "" && configured && action(c, "materials-associate", "refresh", map[bool]string{true: "重试关联", false: "关联任务资料"}[t.materialsFailed()], p, true) {
			s.act("task.materials-retry", t.id, "", "", "", "", nil)
		}
	})
}

func (s *app) documentTree(c *ui.Context, p palette, width float32) {
	state := s.ensureMaterialsFileManagementState()
	directory, _ := s.materialsCurrentDirectory(state)
	canWriteDirectory := s.materialsWritable(directory, true)
	canTrashSelection := len(state.selected) > 0
	for _, entry := range s.materialsFileSelection(state) {
		canTrashSelection = canTrashSelection && s.materialsWritable(entry.Path, false)
	}
	order := s.readingMaterialsSortOrder()
	projection := s.materialsDocumentProjection()
	expanded := s.readingExpandedDirectories()
	ui.Column(c).Width(width).Shrink(0).MinHeight(0).Background(p.side).Border(1, p.line).BorderWidth(0, 1, 0, 0).Children(func() {
		ui.Row(c).Height(34).Shrink(0).Padding(0, 8).Gap(2).AlignItems(ui.Center).Children(func() {
			label(c, "资料", 13, p.text).FontWeight(600)
			if selectedCount := len(s.materialsFileSelection(state)); selectedCount > 0 {
				label(c, fmt.Sprintf("已选 %d", selectedCount), 11, p.blue).SingleLine().Shrink(1).MinWidth(0)
			} else {
				label(c, fmt.Sprintf("(%d)", countDocuments(s.materialsUnit.state.documents)), 11, p.muted)
			}
			ui.Spacer(c)
			sortLabel := map[string]string{"NAME": "按名称排序", "MODIFIED_DESC": "最近修改排序"}[order]
			miniButton(c, p, "materials-sort", "sort", sortLabel, order == "MODIFIED_DESC").Menu(func(m *ui.Menu) {
				if m.Item("按名称").Chosen() {
					s.setReadingMaterialsSortOrder("NAME")
				}
				if m.Item("最近修改").Chosen() {
					s.setReadingMaterialsSortOrder("MODIFIED_DESC")
				}
			})
			s.overflowMenuOwned(c, p, "materials-more", "materials-more-"+s.snapshot.Home+"-"+s.taskUnit.currentTask().id+"-"+s.taskUnit.currentTask().path, "更多资料操作", false, func(m *overflowMenuBuilder) {
				if m.Item("quick-open", "快速打开文件 (Ctrl+P)").Icon("search").Chosen() {
					s.materialsUnit.openMaterialsQuickPicker()
				}
				if m.Item("search-all", "搜索全部资料 (Ctrl+Shift+F)").Icon("search").Chosen() {
					s.openMaterialsFullTextSearch()
				}
				if m.Item("new-document", "新建文档").Icon("file").Disabled(!canWriteDirectory).Chosen() {
					directory, _ := s.materialsCurrentDirectory(state)
					s.openMaterialsFileNameForm("document.create", "新建文档", directory)
				}
				if m.Item("new-folder", "新建文件夹").Icon("folder").Disabled(!canWriteDirectory).Chosen() {
					directory, _ := s.materialsCurrentDirectory(state)
					s.openMaterialsFileNameForm("document.mkdir", "新建文件夹", directory)
				}
				if m.Item("import", "导入文件…").Icon("plus").Disabled(!canWriteDirectory).Chosen() {
					s.startMaterialsImportPicker()
				}
				if m.Item("paste", "粘贴文件或图片").Icon("copy").Disabled(!canWriteDirectory).Chosen() {
					s.pasteMaterialsIntoCurrentDirectory()
				}
				m.Separator()
				if m.Item("copy-selection", "复制所选项").Icon("copy").Disabled(len(state.selected) == 0 || s.materialsFileInputBusy()).Chosen() {
					s.copyMaterialsSelectionToClipboard()
				}
				m.Separator()
				if m.Item("refresh", "刷新资料").Icon("refresh").Chosen() {
					s.materialsUnit.state.documents, s.materialsUnit.state.documentsLoaded, s.materialsUnit.state.documentsRequested = nil, false, false
					s.loadDocuments()
				}
				m.Separator()
				s.documentMoreMenu(c, m)
				m.Separator()
				if m.Item("trash-selection", "移入回收站…").Icon("delete").Disabled(!canTrashSelection || s.materialsFileInputBusy()).Dangerous().Chosen() {
					s.requestMaterialsTrashPreview()
				}
			})
			if miniButton(c, p, "materials-collapse", "chevron-left", "收起文档列表", false).Clicked() {
				s.materialsUnit.state.documentsCollapsed = true
			}
		})
		rows := s.materialsTreeRows(projection, order)
		treeExpanded, selected := make(map[string]bool), make(map[string]bool)
		for _, row := range rows {
			if row.Directory {
				treeExpanded[row.ID] = expanded[projection.relativePath(row.Path)]
			}
			_, selected[row.ID] = state.selected[row.ID]
		}
		opened := materialsFileEntryKey(projection.relativePath(s.materialsUnit.state.documentPath), false)
		FileTree(c, p, FileTreeProps{
			Key: "materials-file-tree", Root: FileTreeRow{ID: "@root", Name: filepath.Base(state.root), Path: state.root, Directory: true},
			Rows: rows, Expanded: treeExpanded, Selected: selected, Opened: opened, State: &state.tree,
			Scroll: s.readingScrollState("directory"), Empty: "此任务资料目录为空。", AcceptFiles: true,
			SuppressTooltip: s.dialogOpen || s.overflowMenus != nil && s.overflowMenus.openKey != "",
			OnInput:         func(input FileTreeInput) { s.materialsTreeInput(state, input) },
			ContextMenu: func(row FileTreeRow, menu *ui.Menu) {
				if row.ID == "@root" {
				} else if entry, ok := projection.find(row.Path); ok {
					s.materialsEntryContextMenu(c, menu, state, entry)
				}
			},
			Shortcuts: func(element ui.Element) {
				element.OnShortcut(ui.Cmd, ui.KeyC, s.copyMaterialsSelectionToClipboard)
				element.OnShortcut(ui.Cmd, ui.KeyV, s.pasteMaterialsIntoCurrentDirectory)
			},
			RootActionWidth: 88,
			RootActions: func() {
				if fileTreeIconButton(c, p, "materials-root-new-file", "file-plus", "新建文档").Clicked() {
					s.openMaterialsFileNameForm("document.create", "新建文档", state.root)
				}
				if fileTreeIconButton(c, p, "materials-root-new-folder", "folder-plus", "新建文件夹").Clicked() {
					s.openMaterialsFileNameForm("document.mkdir", "新建文件夹", state.root)
				}
				if fileTreeIconButton(c, p, "materials-root-refresh", "refresh", "刷新资料").Clicked() {
					s.reloadMaterialsDocuments()
				}
				if fileTreeIconButton(c, p, "materials-root-collapse", "collapse-folders", "收起全部文件夹").Clicked() {
					for directory := range expanded {
						s.setReadingDirectoryExpanded(directory, false)
					}
				}
			},
		})
	})
}

func (s *app) materialsDirectoryResizeHandle(c *ui.Context, p palette, availableWidth float32) {
	handle := ui.Box(c.Key("materials-directory-resize")).Width(materialsDirectoryHandleWidth).FillHeight().Cursor(ui.CursorResizeEW).
		Focusable().Role(ui.RoleSplitter).Label("拖动调整目录宽度").Tooltip("拖动调整目录宽度")
	dx, _, held := handle.Dragged()
	hovered := handle.Hovered()
	if held {
		width := s.materialsDirectoryWidthRef()
		current := clampMaterialsDirectoryWidth(*width, availableWidth)
		*width = clampMaterialsDirectoryWidth(current+dx, availableWidth)
	}
	line := p.line
	if hovered || held {
		line = p.blue
	}
	handle.Center().Children(func() {
		ui.Box(c).Width(2).FillHeight().Margin(16, 0).Background(line)
	})
}

func clampMaterialsDirectoryWidth(preferredWidth, availableWidth float32) float32 {
	maximum := min(materialsDirectoryMaxWidth, max(materialsDirectoryMinWidth,
		availableWidth-materialsDirectoryHandleWidth-materialsDirectoryMinPreviewWidth))
	return max(materialsDirectoryMinWidth, min(preferredWidth, maximum))
}

func (s *app) materialsRelativePath(path string) string {
	return s.materialsDocumentProjection().relativePath(path)
}

func (s *app) initializeMaterialsTreeExpansion() {
	if !s.materialsUnit.state.documentsLoaded || s.readingExpansionInitialized() {
		return
	}
	material := s.currentReadingMaterials(true)
	if material == nil {
		return
	}
	expanded := []string{}
	limit := s.materialsExpansionDepth()
	if limit > 0 {
		var visit func([]application.Document, int)
		visit = func(items []application.Document, depth int) {
			for _, item := range items {
				if !isDocumentDirectory(item) {
					continue
				}
				path := s.materialsRelativePath(item.Path)
				if path != "" {
					expanded = append(expanded, path)
				}
				if depth < limit {
					visit(item.Children, depth+1)
				}
			}
		}
		visit(s.materialsUnit.state.documents, 1)
	}
	material.ExpandedDirectories = expanded
	s.markReadingExpansionInitialized()
}

func (s *app) documentTabsBar(c *ui.Context, p palette) {
	ui.Row(c).Height(36).Shrink(0).Padding(0, 10).Gap(2).Background(p.soft).Border(1, p.line).BorderWidth(0, 0, 1, 0).AlignItems(ui.Center).Children(func() {
		if s.materialsUnit.state.documentsCollapsed && miniButton(c, p, "materials-expand", "chevron", "展开文档列表", false).Clicked() {
			s.materialsUnit.state.documentsCollapsed = false
		}
		for _, path := range s.materialsUnit.state.documentTabs {
			name := filepath.Base(path)
			selected := path == s.materialsUnit.state.documentPath
			tab := ui.ButtonBase(c.Key("materials-open-tab-"+path)).Row().Height(30).Padding(0, 10).Gap(7).Radius(6).Label(name)
			if selected {
				tab.Background(p.selected)
			}
			tab.Children(func() {
				fg := p.text
				if selected {
					fg = p.blue
				}
				label(c, name, 12, fg).FontWeight(500).NoWrap()
				if s.materialsUnit.state.documentDirtyMap[path] {
					ui.Box(c).Width(6).Height(6).Radius(3).Background(p.amber)
				}
				if iconButton(c, "close-doc-"+path, "close", "关闭文档标签", p).Clicked() {
					s.closeDocumentTab(path)
				}
			})
			if tab.Clicked() {
				s.selectDocument(path)
			}
		}
		ui.Spacer(c)
	})
}

func (s *app) documentToolbar(c *ui.Context, p palette) {
	s.renderMaterialsTextEditorHeader(c, p)
	if s.materialsUnit.state.documentsCollapsed {
		ui.Row(c).Height(36).Shrink(0).Padding(0, 12).Gap(8).AlignItems(ui.Center).Background(p.soft).Border(1, p.line).BorderWidth(0, 0, 1, 0).Children(func() {
			if miniButton(c, p, "materials-expand-list", "chevron", "展开文档列表", false).Clicked() {
				s.materialsUnit.state.documentsCollapsed = false
			}
			label(c, "文档列表已收起", 11, p.muted)
			ui.Spacer(c)
		})
	}
	if state := s.materialsUnit.state.materialsFileManagement; state != nil && state.lastError != "" {
		ui.Row(c).Height(30).Shrink(0).Padding(0, 12).Gap(8).AlignItems(ui.Center).Background(p.soft).Children(func() {
			icon(c, "warning", 13, p.amber)
			label(c, state.lastError, 11, p.amber).Grow(1).SingleLine().Tooltip(state.lastError)
			if action(c, "materials-file-error-dismiss", "close", "关闭错误提示", p, false) {
				state.lastError = ""
			}
		})
	}
	if hit, ok := s.materialsSearchSourcePreviewHit(); ok {
		ui.Row(c).Height(32).Shrink(0).Padding(0, 12).Gap(8).AlignItems(ui.Center).Background(p.soft).Children(func() {
			label(c, "全文搜索命中 · "+searchHitLocation(hit), 11, p.blue).Grow(1).SingleLine()
			if action(c, "materials-search-return-preview", "eye", "返回阅读预览", p, false) {
				s.materialsUnit.closeMaterialsSearchSourcePreview()
			}
		})
	}
	s.renderDocumentFindBar(c, p)
}

func (s *app) documentMoreMenu(c *ui.Context, m *overflowMenuBuilder) {
	if s.materialsUnit.state.documentPath == "" {
		return
	}
	ext := strings.ToLower(filepath.Ext(s.materialsUnit.state.documentPath))
	m.Separator()
	if m.Item("find", "查找当前文档").Icon("search").Chosen() {
		if s.currentMaterialsTextPreview() == nil && s.materialsUnit.state.documentFindOpen {
			s.closeDocumentFind()
		} else {
			s.openDocumentFind(c)
		}
	}
	if ext == ".md" || ext == ".markdown" {
		label, next := "查看 Markdown 原文", "SOURCE"
		if s.readingMode() == "SOURCE" {
			label, next = "返回 Markdown 预览", "RENDERED"
		}
		if m.Item("markdown-mode", label).Icon("file").Chosen() {
			s.setReadingMode(next)
		}
	}
	if s.materialsUnit.state.documentDirty && ext != ".md" && ext != ".markdown" {
		label := "保存"
		if m.Item("save", label).Icon("save").Chosen() {
			if s.currentMaterialTextEditor() != nil {
				s.flushMaterialTextAutosave()
			} else {
				s.act("document.save", s.taskUnit.currentTask().id, "", "", s.materialsUnit.state.documentPath, s.materialsUnit.state.documentContent, map[string]any{"expectedHash": s.materialsUnit.state.documentHash})
			}
		}
	}
	if m.Item("reload", "重新读取").Icon("refresh").Chosen() {
		reload := func() { s.rereadSelectedMaterial(true) }
		if s.materialsUnit.state.documentDirty {
			s.confirmNative("放弃本地修改并重新读取？", "当前尚未保存的内容将被丢弃。", filepath.Base(s.materialsUnit.state.documentPath), reload)
		} else {
			reload()
		}
	}
	m.Separator()
	if m.Item("copy-content", "复制文件原文").Icon("copy").Chosen() {
		s.copyTextToClipboard(c.Services(), s.materialsUnit.state.documentContent, "已复制文档内容")
	}
	if m.Item("rename", "重命名").Icon("edit").Chosen() {
		s.openForm("document.rename", "重命名文档", s.taskUnit.currentTask().id, "", "", s.materialsUnit.state.documentPath, formField{key: "name", label: "新文件名", value: filepath.Base(s.materialsUnit.state.documentPath)})
	}
	if m.Item("duplicate", "复制文档").Icon("copy").Chosen() {
		s.openForm("document.copy", "复制文档", s.taskUnit.currentTask().id, "", "", s.materialsUnit.state.documentPath, formField{key: "name", label: "副本文件名"})
	}
	m.Separator()
	if materialsOpenWithAvailable() && m.Item("open-with", "选择应用打开").Icon("launch").Disabled(s.materialsSystemOpenBusy(s.materialsUnit.state.materialsFileManagement)).Chosen() {
		s.act("system.open-with", s.taskUnit.currentTask().id, "", "", s.materialsUnit.state.documentPath, "", nil)
	}
	if m.Item("open", "打开文档").Icon("launch").Disabled(s.materialsSystemOpenBusy(s.materialsUnit.state.materialsFileManagement)).Chosen() {
		s.act("document.open", s.taskUnit.currentTask().id, "", "", s.materialsUnit.state.documentPath, "", nil)
	}
	if m.Item("reveal", "显示位置").Icon("folder").Disabled(s.materialsSystemRevealBusy(s.materialsUnit.state.materialsFileManagement)).Chosen() {
		s.act("document.reveal", s.taskUnit.currentTask().id, "", "", s.materialsUnit.state.documentPath, "", nil)
	}
	m.Separator()
	if m.Item("trash", "移至回收站").Icon("delete").Dangerous().Chosen() {
		s.selectMaterialsPath(s.materialsUnit.state.documentPath)
		s.requestMaterialsTrashPreview()
	}
}

func miniButton(c *ui.Context, p palette, key, image, accessible string, selected bool) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Row().Width(30).Height(30).Radius(6).AlignItems(ui.Center).Justify(ui.Center).Label(accessible).Tooltip(accessible)
	bg, fg := ui.Transparent, p.muted
	if selected {
		bg, fg = p.selected, p.blue
	} else if b.Hovered() {
		bg = p.soft
	}
	b.Background(bg).Children(func() { icon(c, image, 16, fg) })
	return b
}

func (s *app) closeDocumentTab(path string) {
	if path == s.materialsUnit.state.documentPath {
		if err := s.captureMaterialTextInput(); err != nil {
			s.lastError = err.Error()
			return
		}
	}
	for index, opened := range s.materialsUnit.state.documentTabs {
		if opened == path {
			s.materialsUnit.state.documentTabs = append(s.materialsUnit.state.documentTabs[:index], s.materialsUnit.state.documentTabs[index+1:]...)
			break
		}
	}
	if s.materialsUnit.state.documentPath != path {
		return
	}
	s.materialsUnit.closeNativeMedia()
	s.closeMarkdownEditor()
	if len(s.materialsUnit.state.documentTabs) == 0 {
		s.materialsUnit.state.documentPath, s.materialsUnit.state.documentContent, s.materialsUnit.state.documentHash = "", "", ""
		s.materialsUnit.state.documentKind, s.materialsUnit.state.documentReadOnly = "", false
		s.materialsUnit.state.documentDirty, s.materialsUnit.state.documentEditing = false, false
		return
	}
	s.selectDocument(s.materialsUnit.state.documentTabs[len(s.materialsUnit.state.documentTabs)-1])
}

func (s *app) fileText() string { return s.materialsUnit.state.documentContent }

func countDocuments(items []application.Document) int {
	count := 0
	for _, item := range items {
		count++
		count += countDocuments(item.Children)
	}
	return count
}

func renderMarkdown(c *ui.Context, p palette, source string) {
	renderMarkdownScaled(c, p, source, 1)
}

func renderMarkdownScaled(c *ui.Context, p palette, source string, zoom float32) {
	zoom = clampMarkdownZoom(zoom)
	ui.Column(c).FillWidth().Gap(10).Font(markdownSansFontFamily).FontSize(scaleMarkdownSize(15, zoom)).LineHeight(1.6).Children(func() {
		renderMarkdownFallback(c, p, source, zoom)
	})
}

func renderMarkdownFallback(c *ui.Context, p palette, source string, zoom float32) {
	lines := strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n")
	for index := 0; index < len(lines); {
		line := strings.TrimSpace(lines[index])
		switch {
		case line == "":
			ui.Box(c).Height(8)
			index++
		case strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~"):
			fence := line[:3]
			index++
			var code []string
			for index < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[index]), fence) {
				code = append(code, lines[index])
				index++
			}
			if index < len(lines) {
				index++
			}
			ui.Column(c).FillWidth().Margin(10, 0).Background(p.soft).Radius(6).Children(func() {
				label(c, strings.Join(code, "\n"), scaleMarkdownSize(14, zoom), p.text).FillWidth().Padding(12).Font("Consolas, monospace").LineHeight(1.5).Selectable()
			})
		case strings.HasPrefix(line, "#"):
			level := 0
			for level < len(line) && level < 6 && line[level] == '#' {
				level++
			}
			text := strings.TrimSpace(line[level:])
			label(c, text, scaleMarkdownSize(markdownHeadingSize(level), zoom), p.text).FontWeight(700)
			index++
		case strings.HasPrefix(line, ">"):
			text := strings.TrimSpace(strings.TrimPrefix(line, ">"))
			ui.Row(c).FillWidth().Gap(10).Children(func() {
				ui.Box(c).Width(2).FillHeight().Background(p.blue)
				label(c, text, scaleMarkdownSize(15, zoom), p.muted).LineHeight(1.6)
			})
			index++
		case isTableLine(line):
			rows := make([][]string, 0)
			for index < len(lines) && isTableLine(strings.TrimSpace(lines[index])) {
				cells := tableCells(strings.TrimSpace(lines[index]))
				if !tableDivider(cells) {
					rows = append(rows, cells)
				}
				index++
			}
			renderMarkdownTable(c, p, rows, zoom)
		case strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "+ "):
			for index < len(lines) {
				item := strings.TrimSpace(lines[index])
				if !strings.HasPrefix(item, "- ") && !strings.HasPrefix(item, "* ") && !strings.HasPrefix(item, "+ ") {
					break
				}
				checked := strings.HasPrefix(item, "- [x] ") || strings.HasPrefix(item, "- [X] ")
				if strings.HasPrefix(item, "- [") && len(item) >= 6 {
					item = strings.TrimSpace(item[6:])
				} else {
					item = strings.TrimSpace(item[2:])
				}
				ui.Row(c).Gap(8).Children(func() {
					mark := "•"
					if checked {
						mark = "✓"
					}
					label(c, mark, scaleMarkdownSize(15, zoom), p.blue)
					label(c, item, scaleMarkdownSize(15, zoom), p.text).LineHeight(1.6)
				})
				index++
			}
		case strings.HasPrefix(line, "---") || strings.HasPrefix(line, "***"):
			ui.Divider(c)
			index++
		default:
			paragraph := []string{line}
			index++
			for index < len(lines) {
				next := strings.TrimSpace(lines[index])
				if next == "" || strings.HasPrefix(next, "#") || strings.HasPrefix(next, "-") || strings.HasPrefix(next, "*") || strings.HasPrefix(next, ">") || strings.HasPrefix(next, "```") || isTableLine(next) {
					break
				}
				paragraph = append(paragraph, next)
				index++
			}
			label(c, strings.Join(paragraph, " "), scaleMarkdownSize(15, zoom), p.text).LineHeight(1.6)
		}
	}
}

func isTableLine(line string) bool {
	return strings.HasPrefix(line, "|") && strings.Contains(line, "|")
}

func tableCells(line string) []string {
	line = strings.Trim(line, "|")
	cells := strings.Split(line, "|")
	for index := range cells {
		cells[index] = strings.TrimSpace(cells[index])
	}
	return cells
}

func tableDivider(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		cell = strings.ReplaceAll(cell, ":", "")
		cell = strings.ReplaceAll(cell, "-", "")
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

func renderMarkdownTable(c *ui.Context, p palette, rows [][]string, zoom float32) {
	if len(rows) == 0 {
		return
	}
	ui.Column(c).FillWidth().Border(1, p.line).Radius(8).Children(func() {
		for rowIndex, row := range rows {
			line := ui.Row(c).FillWidth().Padding(9, 12).Gap(10)
			if rowIndex == 0 {
				line.Background(p.soft)
			} else {
				line.Border(1, p.line).BorderWidth(1, 0, 0, 0)
			}
			line.Children(func() {
				for column, cell := range row {
					color := p.muted
					if rowIndex == 0 {
						color = p.text
					}
					content := label(c, cell, scaleMarkdownSize(15, zoom), color).Grow(1)
					if column == 0 {
						content.FontWeight(500)
					}
				}
			})
		}
	})
}
