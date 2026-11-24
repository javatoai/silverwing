package markdownui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	code "github.com/egoist/mygo/ui/editor"
	md "github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
	"silverwing-go/internal/markdowntext"
)

type TagAction = markdowntext.TagAction

const (
	AddTag    = markdowntext.AddTag
	RenameTag = markdowntext.RenameTag
	RemoveTag = markdowntext.RemoveTag
)

type TagChange struct {
	DocumentID string
	Version    uint64
	Action     TagAction
	Index      int
	Value      string
}

// PropertiesState owns presentation only. The document owner applies edits.
type PropertiesState struct {
	documentID, source string
	model              *markdowntext.Properties
	dialog             *propertyTagDialog
	viewportWidth      float32
}
type propertyTagDialog struct {
	open, focus                     bool
	change                          TagChange
	value, originalValue, errorText string
}

func pendingPropertyTagInput(dialog *propertyTagDialog) bool {
	return dialog != nil && (dialog.value != dialog.originalValue || dialog.errorText != "")
}

type PropertiesOptions struct {
	DocumentID, Source string
	Version            uint64
	ReadOnly           bool
	OnTagChange        func(TagChange) error
	OnViewSource       func()
	OnInteraction      func()
}

func NewPropertiesState(document Document) *PropertiesState {
	s := &PropertiesState{}
	s.setDocument(document.ID, document.Source)
	return s
}
func (s *PropertiesState) setDocument(id, source string) {
	if s.documentID == id && s.source == source {
		return
	}
	if s.documentID != id {
		s.dialog = nil
	}
	s.documentID, s.source = id, source
	s.model = markdowntext.ParseProperties(source)
}

// MarkdownProperties is shared by reading/editing and can be embedded by hosts.
// It never reads/writes files, changes dates, or serializes the document.
func MarkdownProperties(c *ui.Context, s *PropertiesState, o PropertiesOptions) ui.Element {
	if s == nil {
		return ui.Box(c).Height(0)
	}
	s.setDocument(o.DocumentID, o.Source)
	if d := s.dialog; d != nil && !d.open && pendingPropertyTagInput(d) {
		d.open = true
	}
	root := ui.Column(c.Key("properties-" + o.DocumentID)).FillWidth().MinWidth(0).HandleInput(func(event ui.InputEvent) bool {
		if o.OnInteraction != nil && (event.Kind == ui.InputPointerDown || event.Kind == ui.InputKeyDown || event.Kind == ui.InputText || event.Kind == ui.InputCommand) {
			o.OnInteraction()
		}
		return false
	})
	if s.viewportWidth > 0 {
		root.MaxWidth(s.viewportWidth)
	}
	m := s.model
	if m == nil {
		return root.Height(0)
	}
	th := c.Theme()
	root.Gap(6).Padding(10, 12).Margin(0, 0, 12, 0).Radius(8).Background(th.SurfaceHover)
	available := root.Bounds().W
	if s.viewportWidth > 0 && (available <= 0 || s.viewportWidth < available) {
		available = s.viewportWidth
	}
	if available <= 0 {
		available, _ = c.Size()
	}
	editable := !o.ReadOnly && o.OnTagChange != nil && m.TagsEditable
	if !editable {
		s.dialog = nil
	}
	var tagAnchor ui.Element
	hasTagAnchor := false
	openTag := func(anchor ui.Element, action TagAction, index int, value string) {
		if pendingPropertyTagInput(s.dialog) {
			s.dialog.open, s.dialog.focus = true, true
			return
		}
		if o.OnInteraction != nil {
			o.OnInteraction()
		}
		tagAnchor, hasTagAnchor = anchor, true
		s.dialog = &propertyTagDialog{open: true, focus: true, value: value, originalValue: value, change: TagChange{DocumentID: o.DocumentID, Version: o.Version, Action: action, Index: index}}
	}
	root.Children(func() {
		ui.Text(c, "文档属性").FontSize(12).FontWeight(600).TextColor(th.Text)
		ui.Box(c).FillWidth().Height(1).Background(th.Border)
		ui.Row(c.Key("properties-tags")).Hidden(len(m.Tags) == 0 && !editable).FillWidth().MinWidth(0).Wrap().GapX(6).GapY(6).AlignItems(ui.Center).Children(func() {
			for i, tag := range m.Tags {
				bg, ink := ui.Hex("#f3efff"), ui.Hex("#7047c7")
				if th.Dark {
					bg, ink = ui.Hex("#352b49"), ui.Hex("#d2bcff")
				}
				chip := ui.Row(c.Key(fmt.Sprintf("property-tag-%d", i))).Radius(999).Background(bg).MinWidth(0).Shrink(1).MaxWidth(max(float32(40), min(float32(480), available-24))).AlignItems(ui.Center)
				chip.Children(func() {
					body := ui.ButtonBase(c.Key("tag-text")).Padding(3, 8).MinWidth(0).Shrink(1).Label("标签：" + tag).Tooltip(tag)
					body.Children(func() { ui.Text(c, strings.ReplaceAll(tag, "/", " / ")).FontSize(12).TextColor(ink).Wrap().MinWidth(0) })
					if editable {
						if body.Clicked() {
							openTag(chip, RenameTag, i, tag)
						}
						body.OnShortcut(0, ui.KeyEnter, func() { openTag(chip, RenameTag, i, tag) }).OnShortcut(0, ui.KeySpace, func() { openTag(chip, RenameTag, i, tag) })
						remove := func() {
							if pendingPropertyTagInput(s.dialog) {
								s.dialog.open, s.dialog.focus = true, true
								return
							}
							if o.OnInteraction != nil {
								o.OnInteraction()
							}
							if err := o.OnTagChange(TagChange{DocumentID: o.DocumentID, Version: o.Version, Action: RemoveTag, Index: i}); err != nil {
								openTag(chip, RenameTag, i, tag)
								s.dialog.errorText = err.Error()
							}
						}
						x := ui.ButtonBase(c.Key("tag-remove")).Padding(4, 5).MinHeight(22).Radius(999).Label("删除标签：" + tag).Tooltip("删除标签").Children(func() { ui.Icon(c, propertyIcons["x"]).Size(10, 10).TextColor(ink) })
						if x.Clicked() {
							remove()
						}
						x.OnShortcut(0, ui.KeyEnter, remove).OnShortcut(0, ui.KeySpace, remove)
					}
				})
				if d := s.dialog; d != nil && d.change.Action != AddTag && d.change.Index == i {
					tagAnchor, hasTagAnchor = chip, true
				}
				chip.ContextMenu(func(menu *ui.Menu) {
					if menu.Item("复制标签").Chosen() {
						c.WriteClipboard(tag)
					}
					if editable {
						if menu.Item("编辑标签").Chosen() {
							openTag(chip, RenameTag, i, tag)
						}
						if menu.Item("删除标签").Chosen() {
							if pendingPropertyTagInput(s.dialog) {
								s.dialog.open, s.dialog.focus = true, true
								return
							}
							if err := o.OnTagChange(TagChange{DocumentID: o.DocumentID, Version: o.Version, Action: RemoveTag, Index: i}); err != nil {
								openTag(chip, RenameTag, i, tag)
								s.dialog.errorText = err.Error()
							}
						}
					}
				})
			}
			if editable {
				b := ui.ButtonBase(c.Key("property-add-tag")).Padding(4, 7).Radius(5).Label("添加标签")
				b.Children(func() {
					ui.Row(c).Gap(3).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, propertyIcons["plus"]).Size(12, 12).TextColor(th.TextMuted)
						ui.Text(c, "添加标签").FontSize(12).TextColor(th.TextMuted)
					})
				})
				if d := s.dialog; d != nil && d.change.Action == AddTag {
					tagAnchor, hasTagAnchor = b, true
				}
				if b.Clicked() {
					openTag(b, AddTag, len(m.Tags), "")
				}
				b.OnShortcut(0, ui.KeyEnter, func() { openTag(b, AddTag, len(m.Tags), "") }).OnShortcut(0, ui.KeySpace, func() { openTag(b, AddTag, len(m.Tags), "") })
			}
		})
		for _, field := range m.Fields {
			if field.Name != "tags" {
				renderPropertyField(c, field, available >= 420)
			}
		}
		if !m.TagsEditable && o.OnViewSource != nil {
			label := "通过源码编辑属性"
			if o.ReadOnly {
				label = "查看属性源码"
			}
			if ui.Button(c, label).Clicked() {
				o.OnViewSource()
			}
		}
	})
	if d := s.dialog; d != nil && d.open && editable && hasTagAnchor {
		windowWidth, _ := c.Size()
		ui.Popover(c, tagAnchor, &d.open, func() {
			ui.Column(c.Key("property-tag-editor")).Width(min(float32(320), max(float32(140), windowWidth-24))).Padding(12).Gap(8).Children(func() {
				title := "编辑标签"
				if d.change.Action == AddTag {
					title = "添加标签"
				}
				ui.Text(c, title).FontSize(13).FontWeight(600)
				input := ui.TextInput(c.Key("property-tag-value"), &d.value).FillWidth().Height(32).Label("标签名称").Placeholder("例如：业务/订单支付")
				if d.focus {
					input.Focus()
					d.focus = false
				}
				apply := func(action TagAction) {
					change := d.change
					change.Action, change.Value = action, d.value
					if err := o.OnTagChange(change); err != nil {
						d.errorText = err.Error()
						return
					}
					d.open = false
					s.dialog = nil
				}
				input.OnShortcut(0, ui.KeyEnter, func() { apply(d.change.Action) }).OnShortcut(0, ui.KeyEscape, func() { d.open = false; s.dialog = nil })
				if d.errorText != "" {
					ui.Text(c, d.errorText).FontSize(12).TextColor(th.Danger).Wrap()
				}
				ui.Row(c).Gap(6).FillWidth().Children(func() {
					if d.change.Action != AddTag && ui.Button(c, "删除").Clicked() {
						apply(RemoveTag)
					}
					ui.Spacer(c)
					if ui.Button(c, "取消").Clicked() {
						d.open = false
						s.dialog = nil
					}
					if ui.Button(c, "保存").Clicked() {
						apply(d.change.Action)
					}
				})
			})
		})
	}
	return root
}
func renderPropertyField(c *ui.Context, field markdowntext.PropertyField, wide bool) {
	th := c.Theme()
	row := ui.Column(c.Key("property-field-" + field.Name)).FillWidth().MinWidth(0).Gap(2)
	if wide {
		row.Row().AlignItems(ui.Start).Gap(12)
	}
	row.Children(func() {
		label := ui.Text(c, propertyFieldName(field.Name)).FontSize(12).TextColor(th.TextMuted).Wrap().MinWidth(0)
		if wide {
			label.Width(120).Shrink(0).Padding(3, 0)
		}
		ui.Row(c).Grow(1).MinWidth(0).Gap(8).AlignItems(ui.Start).Children(func() {
			ui.Text(c, field.Value).FontSize(13).TextColor(th.Text).Wrap().Grow(1).MinWidth(0).Padding(3, 0).Selectable()
			copyValue := func() { c.WriteClipboard(field.Value) }
			copyButton := ui.ButtonBase(c.Key("copy")).Row().Size(24, 24).Shrink(0).Radius(4).
				AlignItems(ui.Center).Justify(ui.Center).Label("复制属性 " + field.Name).Tooltip("复制属性值")
			copyButton.Children(func() { ui.Icon(c, propertyIcons["copy"]).Size(13, 13).TextColor(th.TextMuted) })
			copyButton.OnShortcut(0, ui.KeyEnter, copyValue).OnShortcut(0, ui.KeySpace, copyValue)
			if copyButton.Clicked() {
				copyValue()
			}
		})
	})
}

func propertyFieldName(key string) string {
	switch key {
	case "created_at":
		return "创建时间"
	case "updated_at":
		return "更新时间"
	case "ai_thread_id":
		return "AI 会话 ID"
	}
	return key
}

func (s *ReaderState) configureProperties(native *md.Options, edit func(TagChange) error, version func() uint64) {
	state := &s.properties
	state.setDocument(s.document.ID, s.document.Source)
	native.SourcePrefix = func(source string) int {
		state.setDocument(s.document.ID, source)
		if state.model == nil {
			return 0
		}
		return state.model.End
	}
	base := native.Block
	native.Block = func(c *ui.Context, doc *parser.Document, i int) bool {
		if doc.Block(i).Kind != parser.SourcePrefix {
			if base != nil {
				return base(c, doc, i)
			}
			return false
		}
		v := s.version
		if version != nil {
			v = version()
		}
		MarkdownProperties(c, state, PropertiesOptions{DocumentID: s.document.ID, Source: doc.Source(), Version: v, ReadOnly: edit == nil, OnTagChange: edit, OnInteraction: s.propertiesInteraction, OnViewSource: func() {
			if s.onModeChanged != nil {
				s.onModeChanged(Source)
			} else {
				s.SetMode(Source)
			}
		}})
		return true
	}
}

// ApplyTagChange is the single guarded mutation path used by every property UI.
func (s *EditorState) ApplyTagChange(change TagChange) error {
	if s == nil || s.closed {
		return ErrClosed
	}
	if s.options.ReadOnly || s.Mode() != Live {
		return mdCommandUnavailable
	}
	if s.native.Composing() {
		return ErrComposing
	}
	if change.DocumentID != s.document.ID || change.Version != s.Snapshot().Version {
		return ErrStale
	}
	model := markdowntext.ParseProperties(s.native.Buffer().String())
	if model == nil {
		return ErrStale
	}
	deltas, err := model.TagEdits(change.Action, change.Index, change.Value)
	if err != nil || len(deltas) == 0 {
		return err
	}
	slices.SortFunc(deltas, func(a, b markdowntext.PropertySourceEdit) int { return a.Start - b.Start })
	edits := make([]code.Edit, 0, len(deltas))
	eol := s.native.Buffer().LineEnding()
	for _, e := range deltas {
		text := e.Text
		if eol != "\n" {
			text = strings.ReplaceAll(text, "\n", eol)
		}
		edits = append(edits, code.Edit{Start: e.Start, End: e.End, Text: text})
	}
	mapAt := func(at int) int {
		shift := 0
		for _, e := range deltas {
			if at < e.Start {
				break
			}
			if at < e.End {
				return e.Start + shift + len(e.Text)
			}
			shift += len(e.Text) - (e.End - e.Start)
		}
		return at + shift
	}
	a, z := s.native.Selection()
	s.native.MarkUserInteraction()
	if err := s.native.ApplyMetadataEdits(edits, mapAt(a), mapAt(z), change.Version); err != nil {
		return err
	}
	s.notify()
	return nil
}
