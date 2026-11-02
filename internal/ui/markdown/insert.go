package markdownui

import (
	"errors"
	"github.com/egoist/mygo/ui"
	md "github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
	"strings"
)

var mdCommandUnavailable = errors.New("当前选区不能执行此编辑操作")

type insertDialog struct {
	open                    bool
	kind                    CommandKind
	version                 uint64
	text, target, errorText string
	originalText            string
	position                ui.Rect
	positioned, needsFocus  bool
	point                   md.InsertionPoint
}

func (s *EditorState) openInsert(kind CommandKind) {
	if _, err := s.CaptureInput(); err != nil {
		s.report(err)
		return
	}
	a, z := s.native.Selection()
	source := s.native.Buffer().String()
	position, positioned := s.native.SelectionBounds()
	fragment := parser.Parse(source[min(a, z):max(a, z)])
	var parts []string
	for b := 1; b < fragment.Len(); b++ {
		if text := parser.PlainText(fragment.Inlines(b)); text != "" {
			parts = append(parts, text)
		}
	}
	text := strings.Join(parts, " ")
	s.insert = &insertDialog{open: true, kind: kind, version: s.Snapshot().Version, text: text, originalText: text, position: position, positioned: positioned, needsFocus: true, point: s.native.CaptureInsertionPoint()}
}

func (s *EditorState) insertPopover(c *ui.Context, anchor ui.Element) {
	dialog := s.insert
	if dialog == nil || !dialog.open {
		return
	}
	width, height := c.Size()
	if !dialog.positioned {
		bounds := anchor.Bounds()
		dialog.position = ui.Rect{X: bounds.X + 12, Y: bounds.Y + 12, W: 1, H: 1}
		dialog.positioned = true
	}
	position := dialog.position
	position.X = max(6, min(position.X, width-min(float32(350), width-12)-6))
	position.Y = max(6, min(position.Y, height-30))
	ui.Overlay(c, func() {
		point := ui.Box(c.Key("markdown-insert-anchor")).Absolute().Left(position.X).Top(position.Y).Width(1).Height(max(1, position.H))
		ui.PopoverBase(c, point, &dialog.open, func(panel ui.Element) {
			th := c.Theme()
			panel.Width(min(float32(350), max(1, width-12))).Margin(6, 0, 0, 0).Padding(12).Radius(8).Background(th.Background).Border(1, th.Border).Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, .18))
			ui.Column(c.Key("markdown-insert")).FillWidth().Gap(8).Children(func() {
				title := "插入链接"
				if dialog.kind == InsertImage {
					title = "插入图片"
				}
				ui.Text(c, title).FontWeight(600)
				textInput := ui.TextInput(c.Key("insert-text"), &dialog.text).Label("显示文字").Placeholder("显示文字或图片说明").FillWidth().Height(34)
				targetInput := ui.TextInput(c.Key("insert-target"), &dialog.target).Label("链接地址").Placeholder("网址或本地路径").FillWidth().Height(34)
				if dialog.needsFocus {
					if dialog.text != "" {
						targetInput.Focus()
					} else {
						textInput.Focus()
					}
					dialog.needsFocus = false
				}
				if dialog.kind == InsertImage && s.options.OnImportResources != nil {
					if ui.Button(c, "选择本地图片").Clicked() {
						if s.Snapshot().Version != dialog.version {
							dialog.errorText = ErrStale.Error()
						} else {
							if err := s.native.RestoreInsertionPoint(dialog.point); err != nil {
								dialog.errorText = err.Error()
								return
							}
							dialog.open = false
							s.insert = nil
							s.importResources(ImportRequest{Pick: true, Text: dialog.text})
						}
					}
				}
				if dialog.errorText != "" {
					ui.Text(c, dialog.errorText).TextColor(c.Theme().Danger).FontSize(12)
				}
				ui.Row(c).Gap(6).Children(func() {
					ui.Spacer(c)
					if ui.Button(c, "取消").Clicked() {
						dialog.open = false
						s.insert = nil
						s.native.Focus()
					}
					if ui.Button(c, "插入").Clicked() {
						if s.closed || s.options.ReadOnly || s.Snapshot().Version != dialog.version {
							dialog.errorText = ErrStale.Error()
							return
						}
						if err := s.native.RestoreInsertionPoint(dialog.point); err != nil {
							dialog.errorText = err.Error()
							return
						}
						text := dialog.text
						if dialog.kind == InsertLink && text == dialog.originalText {
							text = ""
						} // let the SDK preserve the original selected source
						if err := s.Execute(EditorCommand{Kind: dialog.kind, Text: text, Target: dialog.target}); err != nil {
							dialog.errorText = err.Error()
						} else {
							dialog.open = false
							s.insert = nil
						}
					}
				})
			})
		})
	})
}
