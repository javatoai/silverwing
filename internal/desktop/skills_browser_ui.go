package desktop

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"silverwing-go/internal/application"

	"github.com/egoist/mygo/ui"
)

type skillsBrowserState struct {
	app                        *app
	root                       string
	skills                     []map[string]any
	files                      []map[string]any
	selectedDirectory          string
	selectedSkillName          string
	selectedDescription        string
	selectedPath               string
	preview                    string
	previewLoaded              bool
	previewMarkdown            bool
	markdownSourceMode         bool
	truncated                  bool
	catalogLoaded              bool
	catalogStale               bool
	catalogRefreshAfterPending bool
	loadingCatalog             bool
	loadingFiles               bool
	loadingPreview             bool
	error                      string
	fileError                  string
	requestSequence            uint64
	pendingCatalog             string
	pendingFiles               string
	pendingPreview             string
	pendingRemove              string
	expanded                   map[string]bool
	expandedBySkill            map[string]map[string]bool
	treeSelected               map[string]bool
	treeAnchor                 string
	trees                      map[string]*FileTreeState
	markdown                   *markdownEditorState
	layout                     skillsBrowserLayoutState
	readingPositions           map[string]*skillsBrowserReadingPosition
	codePreviews               map[string]*nativeCodePreviewState
	codePreviewOrder           []string
	jobs                       *skillsBrowserJobs
	pageActive                 bool
}

type skillTreeNode struct {
	name      string
	path      string
	directory bool
	file      map[string]any
	children  []*skillTreeNode
	childMap  map[string]*skillTreeNode
}

func (s *app) ensureSkillsBrowser() *skillsBrowserState {
	if s.settingsUnit.state.skillsBrowser == nil {
		s.settingsUnit.state.skillsBrowser = &skillsBrowserState{app: s, expanded: make(map[string]bool)}
	}
	s.settingsUnit.state.skillsBrowser.app = s
	if s.settingsUnit.state.skillsBrowser.expanded == nil {
		s.settingsUnit.state.skillsBrowser.expanded = make(map[string]bool)
	}
	return s.settingsUnit.state.skillsBrowser
}

func (s *app) nextSkillsBrowserRequest(state *skillsBrowserState) string {
	state.requestSequence++
	return fmt.Sprintf("skills-browser-%d", state.requestSequence)
}

func (s *app) requestSkillsCatalog() {
	state := s.ensureSkillsBrowser()
	state.catalogStale = true
	state.catalogRefreshAfterPending = false
	s.cancelSkillsBrowserRead("skills.files/list")
	s.cancelSkillsBrowserRead("skills.files/preview")
	state.pendingFiles = ""
	state.pendingPreview = ""
	state.loadingFiles = false
	state.loadingPreview = false
	id := s.nextSkillsBrowserRequest(state)
	state.pendingCatalog = id
	state.loadingCatalog = true
	state.error = ""
	s.act("skills.catalog", "", "", "", "", "", map[string]any{"browserRequestID": id})
}

func (s *app) requestSkillsFiles(directory string) {
	state := s.ensureSkillsBrowser()
	s.cancelSkillsBrowserRead("skills.files/preview")
	state.pendingPreview = ""
	state.loadingPreview = false
	if directory == "" {
		state.files = nil
		state.selectedPath = ""
		return
	}
	id := s.nextSkillsBrowserRequest(state)
	state.pendingFiles = id
	state.loadingFiles = true
	state.fileError = ""
	s.act("skills.files/list", "", "", "", "", "", map[string]any{"directoryName": directory, "browserRequestID": id})
}

func (s *app) requestSkillFilePreview(directory, path string) {
	s.startSkillFilePreview(directory, path, false)
}

func (s *app) refreshSkillFilePreview(directory, path string) {
	s.startSkillFilePreview(directory, path, true)
}

func (s *app) startSkillFilePreview(directory, path string, preserve bool) {
	state := s.ensureSkillsBrowser()
	if directory == "" || path == "" {
		return
	}
	id := s.nextSkillsBrowserRequest(state)
	state.pendingPreview = id
	state.loadingPreview = true
	state.fileError = ""
	if !preserve {
		state.previewLoaded = false
		state.preview = ""
		state.retireSkillsPreview()
	}
	s.act("skills.files/preview", "", "", "", "", "", map[string]any{
		"directoryName":    directory,
		"relativePath":     path,
		"browserRequestID": id,
	})
}

func (s *app) selectBrowserSkill(skill map[string]any) {
	state := s.ensureSkillsBrowser()
	directory := mapString(skill, "directoryName")
	if directory == state.selectedDirectory {
		return
	}
	state.rememberSkillsExpansion()
	state.retireSkillsPreview()
	state.selectedDirectory = directory
	state.selectedSkillName = mapString(skill, "name")
	state.selectedDescription = mapString(skill, "description")
	state.selectedPath = ""
	state.treeSelected = nil
	state.treeAnchor = ""
	state.files = nil
	state.preview = ""
	state.previewLoaded = false
	state.loadingPreview = false
	state.markdownSourceMode = false
	state.pendingPreview = ""
	state.expanded = state.skillsExpansion(directory)
	s.requestSkillsFiles(directory)
}

func (s *app) selectBrowserFile(file map[string]any) {
	state := s.ensureSkillsBrowser()
	path := mapString(file, "relativePath")
	if path == "" || path == state.selectedPath {
		return
	}
	state.selectedPath = path
	state.previewMarkdown, _ = file["markdown"].(bool)
	state.markdownSourceMode = false
	s.requestSkillFilePreview(state.selectedDirectory, path)
}

func (state *skillsBrowserState) handleResult(action application.Action, result application.ActionResult) {
	s := state.app
	if s == nil || payloadString(action.Payload, "browserRequestID") == "" {
		return
	}
	switch action.Kind {
	case "skills.catalog":
		requestID := payloadString(action.Payload, "browserRequestID")
		if requestID != "" && requestID != state.pendingCatalog {
			return
		}
		refreshAgain := state.catalogRefreshAfterPending
		state.catalogRefreshAfterPending = false
		state.pendingCatalog = ""
		state.loadingCatalog = false
		if refreshAgain && s.localSkillsViewActive() && state.pageActive {
			s.requestSkillsCatalog()
			return
		}
		state.catalogLoaded = true
		state.error = ""
		state.root = mapString(mapData(result.Data), "root")
		state.skills = mapsFromData(dataValue(result.Data, "skills"))
		previous := state.selectedDirectory
		var selected map[string]any
		for _, skill := range state.skills {
			if mapString(skill, "directoryName") == previous {
				selected = skill
				break
			}
		}
		if selected == nil && len(state.skills) > 0 {
			selected = state.skills[0]
		}
		if selected == nil {
			state.retireSkillsPreview()
			state.selectedDirectory, state.selectedSkillName, state.selectedDescription = "", "", ""
			state.files, state.selectedPath, state.preview = nil, "", ""
			state.previewLoaded = false
			state.catalogStale = false
			return
		}
		state.selectedSkillName = mapString(selected, "name")
		state.selectedDescription = mapString(selected, "description")
		state.catalogStale = false
		if mapString(selected, "directoryName") != previous || len(state.files) == 0 {
			state.selectedDirectory = ""
			s.selectBrowserSkill(selected)
		} else {
			// Keep the cached tree and preview visible while the directory is
			// revalidated in the background.
			s.requestSkillsFiles(previous)
		}
	case "skills.files/list":
		requestID := payloadString(action.Payload, "browserRequestID")
		directory := payloadString(action.Payload, "directoryName")
		if (requestID != "" && requestID != state.pendingFiles) || directory != state.selectedDirectory {
			return
		}
		if requestID == "" && !s.localSkillsViewActive() {
			return
		}
		state.pendingFiles = ""
		state.loadingFiles = false
		state.files = mapsFromData(dataValue(result.Data, "files"))
		state.truncated = dataBool(result.Data, "truncated")
		state.fileError = ""
		if state.selectedPath != "" && !browserHasFile(state.files, state.selectedPath) {
			state.selectedPath = ""
			state.previewLoaded = false
		}
		if state.selectedPath == "" {
			for _, file := range state.files {
				if mapString(file, "relativePath") == "SKILL.md" {
					s.selectBrowserFile(file)
					break
				}
			}
		} else {
			for _, file := range state.files {
				if mapString(file, "relativePath") == state.selectedPath {
					state.previewMarkdown, _ = file["markdown"].(bool)
					s.refreshSkillFilePreview(state.selectedDirectory, state.selectedPath)
					break
				}
			}
		}
	case "skills.files/preview":
		requestID := payloadString(action.Payload, "browserRequestID")
		directory := payloadString(action.Payload, "directoryName")
		path := payloadString(action.Payload, "relativePath")
		if (requestID != "" && requestID != state.pendingPreview) || directory != state.selectedDirectory || path != state.selectedPath {
			return
		}
		if requestID == "" && !s.localSkillsViewActive() {
			return
		}
		state.pendingPreview = ""
		state.loadingPreview = false
		state.preview = resultText(result)
		state.previewLoaded = true
		state.fileError = ""
		if state.previewMarkdown {
			key := "local-skill:" + state.root + "/" + state.selectedDirectory + "/" + state.selectedPath
			if state.markdown == nil || state.markdown.taskID != key {
				state.retireSkillsPreview()
				state.markdown = newReadonlyMarkdownState(key, state.preview, nil)
				state.bindSkillsReadingPosition()
			} else {
				updateReadonlyMarkdownState(state.markdown, state.preview, nil)
			}
			s.bindSkillsMarkdownLinkNavigation(state)
			bindSkillMarkdownPreviewImages(state.markdown, state.markdown.path, state.markdown.linkRoot, s.settingsUnit.previewResourceResolver())
		} else {
			state.retireSkillsPreview()
		}
	case "skills.remove":
		requestID := payloadString(action.Payload, "browserRequestID")
		if requestID != "" && requestID != state.pendingRemove {
			return
		}
		state.pendingRemove = ""
		state.error = ""
		state.catalogStale = true
	}
}

func (state *skillsBrowserState) handleFailure(action application.Action, err error) {
	s := state.app
	if s == nil || payloadString(action.Payload, "browserRequestID") == "" {
		return
	}
	message := "操作失败"
	if err != nil {
		message = err.Error()
	}
	requestID := payloadString(action.Payload, "browserRequestID")
	switch action.Kind {
	case "skills.catalog":
		if requestID != "" && requestID != state.pendingCatalog {
			return
		}
		refreshAgain := state.catalogRefreshAfterPending
		state.catalogRefreshAfterPending = false
		state.pendingCatalog = ""
		state.loadingCatalog = false
		if refreshAgain && s.localSkillsViewActive() && state.pageActive {
			s.requestSkillsCatalog()
			return
		}
		state.error = message
		state.catalogStale = true
	case "skills.files/list":
		if (requestID != "" && requestID != state.pendingFiles) || payloadString(action.Payload, "directoryName") != state.selectedDirectory {
			return
		}
		if requestID == "" && !s.localSkillsViewActive() {
			return
		}
		state.pendingFiles = ""
		state.loadingFiles = false
		state.fileError = message
	case "skills.files/preview":
		if (requestID != "" && requestID != state.pendingPreview) || payloadString(action.Payload, "directoryName") != state.selectedDirectory || payloadString(action.Payload, "relativePath") != state.selectedPath {
			return
		}
		if requestID == "" && !s.localSkillsViewActive() {
			return
		}
		state.pendingPreview = ""
		state.loadingPreview = false
		state.fileError = message
	case "skills.remove":
		if requestID != "" && requestID != state.pendingRemove {
			return
		}
		state.pendingRemove = ""
		state.error = message
	}
}

func browserHasFile(files []map[string]any, path string) bool {
	for _, file := range files {
		if mapString(file, "relativePath") == path {
			return true
		}
	}
	return false
}

func (s *app) renderSkillsBrowser(c *ui.Context, p palette) {
	state := s.ensureSkillsBrowser()
	if !s.loading && !state.catalogLoaded && !state.loadingCatalog && state.error == "" {
		s.requestSkillsCatalog()
	}
	ui.Column(c.Key("skills-browser-content")).Grow(1).MinHeight(0).MinWidth(0).Gap(8).Children(func() {
		if state.error != "" {
			ui.Row(c).Padding(8, 10).Background(p.amberBg).Radius(7).Children(func() { label(c, state.error, 11, p.amber).Wrap() })
		}
		s.renderSkillsBrowserPanes(c, p, state)
	})
}

func (s *app) renderSkillsBrowserList(c *ui.Context, p palette, state *skillsBrowserState) {
	ui.Column(c).FillWidth().FillHeight().MinWidth(0).MinHeight(0).Background(p.surface).Padding(10, 8).Gap(7).Children(func() {
		ui.Row(c).Height(34).Gap(6).AlignItems(ui.Center).Children(func() {
			label(c, "已安装", 12, p.text).FontWeight(600).Grow(1)
			label(c, fmt.Sprint(len(state.skills)), 10, p.muted)
			s.overflowMenu(c, p, "skills-browser-more", "更多本机 Skill 操作", state.pendingRemove != "", func(m *overflowMenuBuilder) {
				if m.Item("refresh", "刷新本机 Skills").Icon("refresh").Disabled(state.loadingCatalog || state.pendingRemove != "").Chosen() {
					s.requestSkillsCatalog()
				}
				if state.selectedDirectory != "" {
					m.Separator()
					if m.Item("uninstall", "备份并卸载当前 Skill").Icon("delete").Disabled(state.pendingRemove != "").Dangerous().Chosen() {
						nameCopy, directoryCopy := state.selectedSkillName, state.selectedDirectory
						s.confirmNative("卸载 Skill？", "将从本机 Skill 目录删除此 Skill。SilverWing 会先创建可恢复备份；不会删除任何 Skill 来源配置。", nameCopy, func() {
							id := s.nextSkillsBrowserRequest(state)
							state.pendingRemove = id
							s.act("skills.remove", "", "", "", "", "", map[string]any{"directoryName": directoryCopy, "confirmed": true, "browserRequestID": id})
						})
					}
				}
			})
		})
		if state.loadingCatalog && !state.catalogLoaded {
			label(c, "正在读取本机 Skills…", 11, p.muted).Wrap()
			return
		}
		if state.error != "" && !state.catalogLoaded {
			label(c, "读取失败，请从标题栏更多菜单重试。", 11, p.muted).Wrap()
			return
		}
		if len(state.skills) == 0 {
			label(c, "没有检测到带 SKILL.md 的 Skill 目录。", 11, p.muted).Wrap()
			return
		}
		ui.Scroll(c.Key("skills-browser-list")).Grow(1).MinHeight(0).FillWidth().Gap(3).Children(func() {
			for index, skill := range state.skills {
				directory := mapString(skill, "directoryName")
				name := mapString(skill, "name")
				selected := directory == state.selectedDirectory
				render := func() {
					button := ui.ButtonBase(c.Key(fmt.Sprintf("skills-browser-skill-%d-%s", index, directory))).Row().FillWidth().MinHeight(54).Padding(8, 9).Gap(8).Radius(7).Label(name)
					if s.page == "settings" {
						button.Grow(1).MinWidth(0)
					}
					if selected {
						button.Background(p.selected)
					} else if button.Hovered() {
						button.Background(p.soft)
					}
					button.Children(func() {
						icon(c, "skills", 17, p.muted)
						ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
							label(c, name, 11, p.text).FontWeight(500).SingleLine()
							if s.page != "settings" {
								label(c, mapString(skill, "description"), 9, p.muted).SingleLine()
							}
							label(c, extensionOwnershipLabel(mapString(skill, "ownership")), 8, p.muted).SingleLine()
						})
					})
					if button.Clicked() {
						s.selectBrowserSkill(skill)
						state.layout.compactPane = skillsBrowserDirectoryPane
					}
					if s.page == "settings" && mapString(skill, "description") != "" {
						settingsHelp(c, p, name, mapString(skill, "description"))
					}
				}
				if s.page == "settings" {
					ui.Row(c.Key(fmt.Sprintf("skills-browser-row-%d-%s", index, directory))).FillWidth().MinWidth(0).Gap(4).AlignItems(ui.Center).Children(render)
				} else {
					render()
				}
			}
		})
	})
}

func (s *app) renderSkillsBrowserPreview(c *ui.Context, p palette, state *skillsBrowserState) {
	ui.Column(c).Grow(1).FillWidth().MinWidth(0).MinHeight(0).Background(p.surface).Padding(8).Gap(6).Children(func() {
		ui.Row(c).Height(36).Gap(8).AlignItems(ui.Center).Children(func() {
			label(c, firstNonEmpty(state.selectedPath, "预览"), 12, p.text).FontWeight(600).Grow(1).SingleLine().Tooltip(state.selectedPath)
			if state.previewLoaded {
				if state.previewMarkdown && state.markdown != nil {
					s.renderReadonlyMarkdownReaderActions(c, p, state.markdown, func(menu *overflowMenuBuilder) {
						if menu.Item("copy-markdown", "复制 Markdown 源码").Icon("copy").Chosen() {
							s.copyTextToClipboard(c.Services(), state.preview, "")
						}
						if menu.Item("copy-path", "复制文件路径").Icon("file").Chosen() {
							s.copyTextToClipboard(c.Services(), filepath.Join(state.root, state.selectedDirectory, filepath.FromSlash(state.selectedPath)), "")
						}
					})
				} else {
					if iconButton(c, "skills-browser-copy", "copy", "复制文件内容", p).Clicked() {
						s.copyTextToClipboard(c.Services(), state.preview, "")
					}
					if iconButton(c, "skills-browser-copy-path", "file", "复制文件路径", p).Clicked() {
						s.copyTextToClipboard(c.Services(), filepath.Join(state.root, state.selectedDirectory, filepath.FromSlash(state.selectedPath)), "")
					}
				}
			}
			if state.loadingPreview {
				label(c, "读取中…", 10, p.muted)
			}
		})
		if state.fileError != "" {
			label(c, state.fileError, 11, p.amber).Wrap()
			if !state.previewLoaded {
				return
			}
		}
		if state.selectedDirectory == "" {
			emptyPanel(c, p, "选择本机 Skill", "左侧显示当前用户目录中可识别的 Skills。")
			return
		}
		if state.selectedPath == "" {
			label(c, "请选择文件查看内容。", 11, p.muted)
			return
		}
		if !state.previewLoaded {
			label(c, "选择文件后在此预览；不会执行 Skill 内容。", 11, p.muted)
			return
		}
		if state.previewMarkdown && state.markdown != nil {
			ui.Box(c.Key("skills-browser-markdown-preview")).Grow(1).FillWidth().MinWidth(0).MinHeight(0).Background(p.surface).Border(1, p.line).Radius(8).Children(func() {
				s.renderReadonlyMarkdownReaderContent(c, p, state.markdown)
			})
		} else {
			s.renderSkillsCodeReader(c, p, state)
		}
	})
}

func makeSkillTree(files []map[string]any) []*skillTreeNode {
	root := &skillTreeNode{directory: true, childMap: make(map[string]*skillTreeNode)}
	for _, file := range files {
		path := filepath.ToSlash(mapString(file, "relativePath"))
		segments := strings.Split(path, "/")
		parent := root
		for index, segment := range segments {
			if segment == "" {
				continue
			}
			currentPath := strings.Join(segments[:index+1], "/")
			child := parent.childMap[segment]
			if child == nil {
				child = &skillTreeNode{name: segment, path: currentPath, directory: index < len(segments)-1, childMap: make(map[string]*skillTreeNode)}
				parent.childMap[segment] = child
				parent.children = append(parent.children, child)
			}
			if index < len(segments)-1 {
				child.directory = true
			}
			if index == len(segments)-1 {
				child.file = file
			}
			parent = child
		}
	}
	var sortTree func(*skillTreeNode)
	sortTree = func(parent *skillTreeNode) {
		sort.Slice(parent.children, func(i, j int) bool {
			if parent.children[i].directory != parent.children[j].directory {
				return parent.children[i].directory
			}
			return strings.ToLower(parent.children[i].name) < strings.ToLower(parent.children[j].name)
		})
		for _, child := range parent.children {
			sortTree(child)
		}
	}
	sortTree(root)
	return root.children
}
