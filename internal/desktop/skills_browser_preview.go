package desktop

import (
	"fmt"
	"path/filepath"

	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
)

const skillsBrowserCodePreviewLimit = 16

type skillsBrowserReadingPosition struct {
	scroll ui.ScrollState
	zoom   float32
}

func (state *skillsBrowserState) readingKey() string {
	return "local-skill:" + state.root + "/" + state.selectedDirectory + "/" + state.selectedPath
}

func (state *skillsBrowserState) readingPosition(key string) *skillsBrowserReadingPosition {
	if state.readingPositions == nil {
		state.readingPositions = make(map[string]*skillsBrowserReadingPosition)
	}
	position := state.readingPositions[key]
	if position == nil {
		position = &skillsBrowserReadingPosition{zoom: 1}
		state.readingPositions[key] = position
	}
	return position
}

func (state *skillsBrowserState) retireSkillsPreview() {
	if markdown := state.markdown; markdown != nil {
		if reader := ensureReadonlyMarkdownReader(markdown); reader != nil {
			state.readingPosition(markdown.taskID).zoom = reader.zoom
		}
		closeMarkdownPreviewState(markdown)
		state.markdown = nil
	}
}

func (state *skillsBrowserState) bindSkillsReadingPosition() {
	if state.markdown == nil {
		return
	}
	position := state.readingPosition(state.markdown.taskID)
	bindReadonlyMarkdownScroll(state.markdown, &position.scroll)
	if reader := ensureReadonlyMarkdownReader(state.markdown); reader != nil {
		reader.zoom = position.zoom
	}
}

func (state *skillsBrowserState) skillCodePreview(key string) *nativeCodePreviewState {
	if state.codePreviews == nil {
		state.codePreviews = make(map[string]*nativeCodePreviewState)
	}
	for index, existing := range state.codePreviewOrder {
		if existing == key {
			state.codePreviewOrder = append(state.codePreviewOrder[:index], state.codePreviewOrder[index+1:]...)
			break
		}
	}
	if preview := state.codePreviews[key]; preview != nil {
		state.codePreviewOrder = append(state.codePreviewOrder, key)
		return preview
	}
	for len(state.codePreviewOrder) >= skillsBrowserCodePreviewLimit {
		oldest := state.codePreviewOrder[0]
		state.codePreviewOrder = state.codePreviewOrder[1:]
		delete(state.codePreviews, oldest)
	}
	preview := &nativeCodePreviewState{}
	state.codePreviews[key] = preview
	state.codePreviewOrder = append(state.codePreviewOrder, key)
	return preview
}

func (s *app) renderSkillsCodeReader(c *ui.Context, p palette, state *skillsBrowserState) {
	key := state.readingKey()
	position := state.readingPosition(key)
	owner := state.skillCodePreview(key)
	view := owner.bind(key, filepath.Base(state.selectedPath), state.preview)
	view.Font = ui.Font{Family: "monospace", Size: textEditorFontSize * position.zoom}
	view.LineHeight = textEditorLineHeight
	view.ReadOnly = true
	view.LineNumbers = codeeditor.LineNumbersOn
	ui.Column(c.Key("skills-browser-code-reader")).Grow(1).FillWidth().MinHeight(0).MinWidth(0).Gap(6).Children(func() {
		ui.Row(c).FillWidth().Height(32).Gap(5).AlignItems(ui.Center).Children(func() {
			ui.Spacer(c)
			if iconButton(c, "skills-browser-code-find-"+key, "search", "查找文件内容 · Ctrl+F", p).Clicked() {
				view.Run(codeeditor.Find)
			}
			if miniButton(c, p, "skills-browser-code-zoom-out-"+key, "minus", "缩小文字", false).Clicked() {
				position.zoom = max(.7, position.zoom-.1)
			}
			label(c, codePreviewZoomLabel(position.zoom), 10, p.muted).NoWrap()
			if miniButton(c, p, "skills-browser-code-zoom-reset-"+key, "refresh", "重置文字大小", false).Clicked() {
				position.zoom = 1
			}
			if miniButton(c, p, "skills-browser-code-zoom-in-"+key, "plus", "放大文字", false).Clicked() {
				position.zoom = min(1.6, position.zoom+.1)
			}
		})
		view.Font.Size = textEditorFontSize * position.zoom
		renderNativeCodePreview(c, p, owner, key, filepath.Base(state.selectedPath), state.preview, "skills-browser-code-"+key).Grow(1).FillWidth().MinHeight(0).MinWidth(0)
	})
}

func codePreviewZoomLabel(zoom float32) string {
	return fmt.Sprintf("%d%%", int(zoom*100+.5))
}
