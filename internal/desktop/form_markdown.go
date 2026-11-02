package desktop

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"

	markdownui "silverwing-go/internal/ui/markdown"

	"github.com/egoist/mygo/ui"
	md "github.com/egoist/mygo/ui/markdown"
)

// Markdown is explicit field metadata. JSON, commands and other multiline
// configuration keep their existing plain-text inputs.
type formFieldFormat uint8

const (
	formFieldPlain formFieldFormat = iota
	formFieldMarkdown
	formFieldFileBody
)

type transientMarkdownDraft struct {
	editor   *markdownui.EditorState
	value    *string
	synced   string
	active   bool
	error    string
	onChange func()
}

type actionFormMarkdownState struct {
	id     string
	drafts map[string]*transientMarkdownDraft
}

var actionFormMarkdownSequence atomic.Uint64

func newActionFormMarkdownState() *actionFormMarkdownState {
	return &actionFormMarkdownState{id: fmt.Sprintf("action-form-%d", actionFormMarkdownSequence.Add(1)), drafts: make(map[string]*transientMarkdownDraft)}
}

func newTransientMarkdownDraft(id string, value *string) *transientMarkdownDraft {
	return &transientMarkdownDraft{editor: markdownui.NewEditorState(markdownui.Document{ID: id, Source: *value}), value: value, synced: *value, active: true}
}

func (draft *transientMarkdownDraft) commit(snapshot markdownui.Snapshot) {
	if draft == nil || draft.value == nil || snapshot.Composing {
		return
	}
	if *draft.value != snapshot.Source {
		*draft.value = snapshot.Source
		if draft.onChange != nil {
			draft.onChange()
		}
	}
	draft.synced = snapshot.Source
}

func (draft *transientMarkdownDraft) syncExternal() error {
	if draft == nil || draft.editor == nil || draft.value == nil || *draft.value == draft.synced {
		return nil
	}
	snapshot := draft.editor.Snapshot()
	if snapshot.Composing || snapshot.Source != draft.synced {
		return markdownui.ErrStale
	}
	source := *draft.value
	captured, err := draft.editor.CaptureInput()
	*draft.value = source
	if err != nil {
		return err
	}
	if err := draft.editor.ReplaceContent(source, captured.Version); err != nil {
		return err
	}
	draft.synced = source
	return nil
}

func (draft *transientMarkdownDraft) capture() error {
	if draft == nil || draft.editor == nil {
		return nil
	}
	if err := draft.syncExternal(); err != nil {
		draft.error = err.Error()
		return err
	}
	snapshot, err := draft.editor.CaptureInput()
	if snapshot.DocumentID != "" {
		draft.commit(snapshot)
	}
	if err != nil {
		draft.error = err.Error()
		return err
	}
	draft.error = ""
	return nil
}

// replace is used only for an accepted template replacement or for edits from
// the same field's plain-text presentation. The editor and Buffer keep their
// document owner; changing a filename never creates a second draft.
func (draft *transientMarkdownDraft) replace(source string) error {
	if err := draft.capture(); err != nil {
		return err
	}
	if err := draft.editor.ReplaceContent(source, draft.editor.Snapshot().Version); err != nil {
		draft.error = err.Error()
		return err
	}
	draft.commit(draft.editor.Snapshot())
	return nil
}

func (draft *transientMarkdownDraft) close() {
	if draft != nil && draft.editor != nil {
		draft.editor.Close()
		draft.active = false
	}
}

func formFieldUsesMarkdown(field formField, name string) bool {
	if field.format == formFieldMarkdown {
		return true
	}
	if field.format == formFieldFileBody {
		switch strings.ToLower(filepath.Ext(strings.TrimSpace(name))) {
		case ".md", ".markdown":
			return true
		}
	}
	return false
}

func transientMarkdownTheme(p palette) *md.Theme {
	return &md.Theme{Font: markdownSansFontFamily, FontSize: 15, LineHeight: 1.6,
		Text: p.text, Muted: p.muted, Link: p.blue, Strong: p.text, Code: p.text, CodeFont: "monospace",
		CodeBackground: p.soft, CodeBlockBackground: p.soft, CodeBlockBorder: p.line,
		CodeColors: textEditorTheme(p), TableBorder: p.line, TableHeader: p.soft, QuoteBar: p.line, QuoteText: p.muted}
}

func (s *app) transientMarkdownReaderOptions(p palette, onError func(error)) markdownui.ReaderOptions {
	return markdownui.ReaderOptions{Theme: transientMarkdownTheme(p), OnError: onError, OnNavigate: func(target markdownui.LinkTarget) {
		if target.URL != "" {
			s.act("system.open", "", "", "", target.URL, "", nil)
		} else if onError != nil {
			onError(fmt.Errorf("当前草稿尚无文件位置，请保存后再打开本地链接"))
		}
	}}
}

func (s *app) renderTransientMarkdown(c *ui.Context, p palette, draft *transientMarkdownDraft, current func() bool, changed, save func(), labelText string, height float32) {
	draft.onChange = changed
	if err := draft.syncExternal(); err != nil {
		draft.error = err.Error()
	}
	ui.Column(c).FillWidth().MinWidth(0).Height(height).Padding(10).Gap(6).Background(p.surface).Border(1, p.line).Radius(8).Children(func() {
		if draft.error != "" {
			label(c, draft.error, 11, p.amber).FillWidth().Wrap()
		}
		markdownui.MarkdownEditor(c, draft.editor, markdownui.EditorOptions{
			ReaderOptions: s.transientMarkdownReaderOptions(p, func(err error) {
				if err != nil && current() {
					draft.error = err.Error()
				}
			}),
			Label: labelText, ReadClipboard: s.markdownClipboardContent,
			OnChange: func(snapshot markdownui.Snapshot) {
				if current() {
					draft.error = ""
					draft.commit(snapshot)
				}
			},
			OnSave: func(markdownui.Snapshot) {
				if current() && draft.capture() == nil && save != nil {
					save()
				}
			},
		}).Grow(1).FillWidth().MinHeight(0)
	})
}

func (s *app) renderActionFormMarkdown(c *ui.Context, p palette, field *formField) bool {
	if field.format == formFieldPlain {
		return false
	}
	if s.form.markdown == nil {
		s.form.markdown = newActionFormMarkdownState()
	}
	owner := s.form.markdown
	value := s.input("form:"+field.key, field.value)
	useMarkdown := formFieldUsesMarkdown(*field, *s.input("form:name", ""))
	draft := owner.drafts[field.key]
	if !useMarkdown {
		if draft == nil || !draft.active {
			return false
		}
		if draft.capture() == nil {
			draft.active = false
			return false
		}
	} else if draft == nil {
		draft = newTransientMarkdownDraft(owner.id+":"+field.key, value)
		owner.drafts[field.key] = draft
	} else if !draft.active {
		if draft.replace(*value) == nil {
			draft.active = true
		}
	}
	current := func() bool {
		return s.dialogOpen && s.form.markdown == owner && owner.drafts[field.key] == draft && draft.active
	}
	s.renderTransientMarkdown(c, p, draft, current, nil, s.submitForm, field.label+" Markdown 编辑区", 240)
	return true
}

func (s *app) captureActionFormMarkdown() bool {
	if s.form.markdown == nil {
		return true
	}
	for _, draft := range s.form.markdown.drafts {
		if draft.active {
			if err := draft.capture(); err != nil {
				s.lastError = err.Error()
				return false
			}
		}
	}
	return true
}

func (s *app) closeActionFormMarkdown() {
	if s.form.markdown != nil {
		for _, draft := range s.form.markdown.drafts {
			draft.close()
		}
		s.form.markdown = nil
	}
}
