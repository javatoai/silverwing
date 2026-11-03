// Package markdownui provides reusable native Markdown reading and editing.
// Hosts own persistence and resource permissions; components own presentation,
// selection, history, and input methods.
package markdownui

import (
	"context"
	"errors"
	"fmt"

	"github.com/egoist/mygo/ui"
	code "github.com/egoist/mygo/ui/editor"
	md "github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

type Mode uint8

const (
	Live Mode = iota
	Preview
	Source
)

// Document.ID must distinguish document owners as well as paths. Path is used
// only to resolve links: providing it never grants filesystem access.
type Document struct{ ID, Source, Path string }

type Snapshot struct {
	DocumentID string
	Version    uint64
	Source     string
	Composing  bool
}

var (
	ErrClosed    = errors.New("Markdown component is closed")
	ErrStale     = errors.New("文档已变化，请重新打开编辑操作")
	ErrComposing = errors.New("请先完成当前输入")
)

// ReaderOptions are shared by the reader and editor. Native supplies optional
// source projections, image/diagram adapters and display annotations. Its
// callbacks must not write Markdown; mutations belong to EditorState.
type ReaderOptions struct {
	Theme       *md.Theme
	Native      *md.Options
	OnNavigate  func(LinkTarget)
	OnError     func(error)
	ContextMenu func(*ui.Context, *ui.Menu)
	// Find joins the search of related document sections when provided.
	Find *FindController
	// ContentOnly embeds the renderer into a host-owned scroll/search surface.
	ContentOnly bool
}

type EditorOptions struct {
	ReaderOptions
	ReadClipboard        func(context.Context) (ClipboardContent, error)
	OnImportResources    func(context.Context, ImportRequest, func(ImportResult, error))
	ReadOnly             bool
	Label                string
	OnChange             func(Snapshot)
	OnSave               func(Snapshot)
	OnCompositionChanged func(bool)
	OnModeChanged        func(Mode)
}

type ReaderState struct {
	document              Document
	version               uint64
	closed                bool
	ctx                   context.Context
	cancel                context.CancelFunc
	mode                  Mode
	zoom                  float32
	scroll                ui.ScrollState
	sourceScroll          ui.ScrollState
	find                  *FindController
	findModeChanged       bool
	findModeQuery         string
	findModeCurrent       int
	onModeChanged         func(Mode)
	native                md.Options
	errorText             string
	focusFind             bool
	parsed                *parser.Document
	sourceView            *code.State
	properties            PropertiesState
	propertiesInteraction func()
	revealLine            int
	revealFragment        string
}

func NewReaderState(document Document) *ReaderState {
	s := &ReaderState{zoom: 1, mode: Preview, find: &FindController{}}
	s.SetDocument(document)
	return s
}

func (s *ReaderState) SetDocument(document Document) {
	if s == nil {
		return
	}
	if s.ctx != nil && !s.closed && s.document == document {
		return
	}
	identityChanged := s.document.ID != document.ID || s.document.Path != document.Path || s.closed
	previousID := s.document.ID
	if s.cancel != nil {
		s.cancel()
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.document, s.closed = document, false
	s.version++
	s.parsed = nil
	s.sourceView = nil
	if identityChanged {
		if s.find != nil {
			s.find.remove(previousID)
		}
		s.mode, s.zoom, s.scroll, s.sourceScroll, s.find = Preview, 1, ui.ScrollState{}, ui.ScrollState{}, &FindController{}
		s.native = md.Options{}
	}
	if s.zoom == 0 {
		s.zoom = 1
	}
}

func (s *ReaderState) Document() Document       { return s.document }
func (s *ReaderState) Context() context.Context { return s.ctx }
func (s *ReaderState) Snapshot() Snapshot {
	return Snapshot{DocumentID: s.document.ID, Version: s.version, Source: s.document.Source}
}
func (s *ReaderState) Find() *FindController { return s.find }
func (s *ReaderState) SetMode(mode Mode) {
	if (mode == Source || mode == Preview) && mode != s.mode {
		s.findModeChanged = true
		s.findModeQuery, s.findModeCurrent = s.find.Query, s.find.Current
		s.mode = mode
	}
}
func (s *ReaderState) Mode() Mode { return s.mode }

// RevealLocation requests navigation once the destination is laid out.
func (s *ReaderState) RevealLocation(line int, fragment string) {
	s.revealLine = line
	s.revealFragment = fragment
	s.properties.setDocument(s.document.ID, s.document.Source)
	if model := s.properties.model; model != nil && line > 0 {
		if at, ok := locationOffset(s.document.Source, line, 1, ""); ok && at < model.End {
			s.SetMode(Source)
		}
	}
}
func (s *ReaderState) Close() {
	if s != nil {
		s.closed = true
		if s.find != nil {
			s.find.remove(s.document.ID)
		}
		if s.cancel != nil {
			s.cancel()
		}
		s.native = md.Options{}
	}
}

type EditorState struct {
	viewHash          string
	viewHashVersion   uint64
	document          Document
	native            *md.EditorState
	reader            *ReaderState
	mode              Mode
	closed            bool
	observed          uint64
	options           EditorOptions
	link              *linkEdit
	insert            *insertDialog
	image             *imageEdit
	sourceFindOwner   *FindController
	sourceFindRequest uint64
}

func NewEditorState(document Document) *EditorState {
	return AdoptEditorState(document, md.NewEditorState(code.NewBufferPreservingLineEndings(document.Source)))
}

// AdoptEditorState migrates an existing native editor without replacing its
// source, selection, IME session or undo history. Both references use one owner.
func AdoptEditorState(document Document, native *md.EditorState) *EditorState {
	if native == nil {
		panic("markdownui: nil native editor")
	}
	document.Source = native.Buffer().OriginalText()
	return &EditorState{document: document, native: native, reader: NewReaderState(document), observed: native.Buffer().Version()}
}

func (s *EditorState) NativeState() *md.EditorState { return s.native }
func (s *EditorState) Snapshot() Snapshot {
	return Snapshot{DocumentID: s.document.ID, Version: s.native.Buffer().Version(), Source: s.native.Buffer().OriginalText(), Composing: s.native.Composing()}
}
func (s *EditorState) Mode() Mode {
	if s.native.SourceMode {
		return Source
	}
	return s.mode
}
func (s *EditorState) SetMode(mode Mode) error {
	if s.closed {
		return ErrClosed
	}
	if mode > Source {
		return fmt.Errorf("unknown Markdown mode %d", mode)
	}
	if _, err := s.CaptureInput(); err != nil {
		return err
	}
	s.native.Buffer().EndUndoGroup()
	s.native.MarkUserInteraction()
	s.mode = mode
	s.native.SourceMode = mode == Source
	s.link = nil
	s.insert = nil
	s.image = nil
	if mode != Preview {
		s.native.Focus()
	}
	if s.options.OnModeChanged != nil {
		s.options.OnModeChanged(mode)
	}
	return nil
}

// ReplaceContent records a confirmed replacement in the shared undo history.
func (s *EditorState) ReplaceContent(source string, expectedVersion uint64) error {
	if s == nil || s.closed {
		return ErrClosed
	}
	if s.options.ReadOnly {
		return fmt.Errorf("Markdown is read-only")
	}
	snapshot, err := s.CaptureInput()
	if err != nil {
		return err
	}
	if snapshot.Version != expectedVersion {
		return ErrStale
	}
	if source == snapshot.Source {
		return nil
	}
	if err := s.native.ApplyEdits([]code.Edit{{Start: 0, End: s.native.Buffer().Len(), Text: source}}, 0, 0, expectedVersion); err != nil {
		return err
	}
	s.notify()
	return nil
}

// Reload is for an explicitly accepted external read or draft recovery, not a
// save acknowledgement. The expected version prevents late reads losing edits.
func (s *EditorState) Reload(source string, expectedVersion uint64) error {
	if s.closed {
		return ErrClosed
	}
	if s.native.Composing() {
		return ErrComposing
	}
	if s.native.Buffer().Version() != expectedVersion {
		return ErrStale
	}
	if s.native.Buffer().OriginalText() == source {
		return nil
	}
	if pendingLinkInput(s.link) || pendingInsertInput(s.insert) || pendingImageInput(s.image) || pendingPropertyTagInput(s.reader.properties.dialog) {
		return fmt.Errorf("请先完成或取消当前编辑，再读取外部内容")
	}
	a, z := s.native.Selection()
	s.native.Buffer().Reset(source)
	s.native.SetSelection(a, z)
	s.observed = s.native.Buffer().Version()
	s.link = nil
	s.image = nil
	return nil
}

func (s *EditorState) Close() {
	if s != nil {
		if s.image != nil && s.image.cancel != nil {
			s.image.cancel()
		}
		s.closed = true
		s.link = nil
		s.image = nil
		s.reader.Close()
	}
}

func (s *EditorState) notify() {
	if s.closed || s.native.Composing() || s.native.Buffer().Version() == s.observed {
		return
	}
	s.observed = s.native.Buffer().Version()
	if s.options.OnChange != nil {
		s.options.OnChange(s.Snapshot())
	}
}

func (s *EditorState) report(err error) {
	if err != nil && s.options.OnError != nil {
		s.options.OnError(err)
	}
}
