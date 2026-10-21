package desktop

import "github.com/egoist/mygo/ui"

const (
	skillsBrowserListPane = iota
	skillsBrowserDirectoryPane
	skillsBrowserDocumentPane
	skillsBrowserCompactWidth = 960
)

type skillsBrowserLayoutState struct {
	canvas                    ui.Handle
	compactPane               int
	listWidth, directoryWidth float32
}

func resolveSkillsBrowserPaneWidths(width, list, directory float32) (float32, float32) {
	content := max(width-2, 800) // MyGO Split has a one DIP line for each divider.
	if list <= 0 {
		list = fileTreeWidth
	}
	if directory <= 0 {
		directory = fileTreeWidth
	}
	list = max(200, min(list, content-240-360))
	directory = max(240, min(directory, content-list-360))
	return list, directory
}

func (s *app) renderSkillsBrowserPanes(c *ui.Context, p palette, state *skillsBrowserState) {
	width := state.layout.canvas.Bounds(c).W
	if width <= 0 {
		width, _ = c.Size()
	}
	ui.Column(c.Key("skills-browser-panes")).Bind(&state.layout.canvas).Grow(1).FillWidth().MinHeight(0).MinWidth(0).Gap(6).Children(func() {
		if width < skillsBrowserCompactWidth {
			ui.Row(c).FillWidth().Gap(5).Children(func() {
				for pane, title := range []string{"Skills", "目录", "文档"} {
					key := pane
					s.settingsChoice(c, p, "skills-browser-pane-"+title, title, state.layout.compactPane == key, func() { state.layout.compactPane = key })
				}
			})
			ui.Box(c).Grow(1).FillWidth().MinHeight(0).MinWidth(0).Children(func() {
				switch state.layout.compactPane {
				case skillsBrowserDirectoryPane:
					s.renderSkillsBrowserDirectory(c, p, state)
				case skillsBrowserDocumentPane:
					s.renderSkillsBrowserPreview(c, p, state)
				default:
					s.renderSkillsBrowserList(c, p, state)
				}
			})
			return
		}
		state.layout.listWidth, state.layout.directoryWidth = resolveSkillsBrowserPaneWidths(width, state.layout.listWidth, state.layout.directoryWidth)
		ui.Split(c.Key("skills-browser-list-split"), &state.layout.listWidth, func() {
			s.renderSkillsBrowserList(c, p, state)
		}, func() {
			ui.Split(c.Key("skills-browser-document-split"), &state.layout.directoryWidth, func() {
				s.renderSkillsBrowserDirectory(c, p, state)
			}, func() {
				s.renderSkillsBrowserPreview(c, p, state)
			}).Fill().MinWidth(0).MinHeight(0)
		}).Fill().MinWidth(0).MinHeight(0)
	})
}

func (s *app) renderSkillsBrowserDirectory(c *ui.Context, p palette, state *skillsBrowserState) {
	ui.Column(c).FillWidth().FillHeight().MinWidth(0).MinHeight(0).Background(p.side).Children(func() {
		ui.Row(c).Height(34).Padding(0, 10).Gap(6).AlignItems(ui.Center).Children(func() {
			label(c, "文件", 13, p.text).FontWeight(600).Grow(1)
			if state.loadingFiles && len(state.files) > 0 {
				label(c, "刷新中…", 9, p.muted)
			}
			if state.truncated {
				label(c, "已截断", 9, p.amber).Tooltip("文件数量超过后端安全上限")
			}
		})
		if state.fileError != "" {
			label(c, state.fileError, 12, p.amber).Padding(6, 10).Wrap()
		}
		if state.loadingFiles && len(state.files) == 0 {
			label(c, "正在读取文件…", 12, p.muted).Padding(10)
		} else if len(state.files) == 0 {
			label(c, "选择一个 Skill 查看文件。", 12, p.muted).Padding(10).Wrap()
		} else {
			s.renderSkillsFileTree(c, p, state)
		}
	})
}

func (state *skillsBrowserState) rememberSkillsExpansion() {
	if state.selectedDirectory == "" {
		return
	}
	if state.expandedBySkill == nil {
		state.expandedBySkill = make(map[string]map[string]bool)
	}
	state.expandedBySkill[state.selectedDirectory] = state.expanded
}

func (state *skillsBrowserState) skillsExpansion(directory string) map[string]bool {
	if expansion := state.expandedBySkill[directory]; expansion != nil {
		return expansion
	}
	return make(map[string]bool)
}
