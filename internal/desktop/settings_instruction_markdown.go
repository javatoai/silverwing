package desktop

import (
	"context"
	"errors"
	"path/filepath"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/editor"
	"silverwing-go/internal/storage"
	markdownui "silverwing-go/internal/ui/markdown"

	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// Settings owns only the file identity and persistence. Editing, preview,
// commands and undo all use the same public component as task materials.
func (s *app) ensureSettingsInstructionLiveState(state *markdownEditorState) {
	if state == nil || state.binding == nil || state.readOnly || state.live != nil || instructionMarkdownReadOnly(s, state.binding.id) {
		return
	}
	source := state.doc.Source()
	state.linkRoot = filepath.Dir(state.path)
	buffer := codeeditor.NewBufferPreservingLineEndings(source)
	state.live = markdown.NewEditorState(buffer)
	state.componentEditor = markdownui.AdoptEditorState(markdownui.Document{ID: state.binding.id, Source: source, Path: state.path}, state.live)
	state.liveVersion, state.liveSource = buffer.Version(), source
	s.configureMarkdownSourceLimit(state)
}

func (s *app) observeSettingsInstructionInput(state *markdownEditorState) {
	if state == nil || state.live == nil || state.readOnly || !s.markdownEditorIsCurrent(state) || state.live.Composing() {
		return
	}
	snapshot := state.live.Buffer().Snapshot()
	if snapshot.Version() == state.liveVersion {
		return
	}
	state.liveVersion, state.liveSource, state.liveError = snapshot.Version(), snapshot.OriginalText(), ""
	state.doc.SyncExternal(state.liveSource)
	s.commitMarkdownEditorSource(state, state.liveSource)
}

func (s *app) captureSettingsInstructionInput(state *markdownEditorState) error {
	if state == nil || state.live == nil || state.readOnly {
		return nil
	}
	if state.binding == nil || !s.markdownEditorIsCurrent(state) || instructionMarkdownReadOnly(s, state.binding.id) {
		return errors.New("说明编辑上下文已失效，请重新读取文件")
	}
	if _, err := state.componentEditor.CaptureInput(); err != nil {
		state.liveError = err.Error()
		return err
	}
	if state.live.Composing() {
		return editor.ErrComposing
	}
	s.observeSettingsInstructionInput(state)
	s.setMarkdownEditorComposing(state, false)
	return nil
}

// Navigation must capture input before changing scope or making the binding stale.
func (s *app) leaveSettingsInstructionEditor() bool {
	if notes := s.taskUnit.state.taskNotesUI; notes != nil && (notes.activeKind == "settings" || notes.activeKind == "task-local") {
		if err := s.captureSettingsInstructionInput(notes.activeMarkdown); err != nil {
			s.lastError = err.Error()
			return false
		}
	}
	s.deactivateInstructionEditor()
	return true
}

func (s *app) instructionReadMayReplaceDraft(action application.Action, key string) bool {
	if _, hasVersion := action.Payload["instructionBufferVersion"]; !hasVersion {
		return true
	}
	if notes := s.taskUnit.state.taskNotesUI; notes != nil && notes.activeKey == key && notes.activeMarkdown != nil && notes.activeMarkdown.live != nil {
		live := notes.activeMarkdown.live
		return !live.Composing() && live.Buffer().Version() == payloadUint64(action.Payload, "instructionBufferVersion")
	}
	return false
}

func (s *app) captureSettingsInstructionSaveVersion(payload map[string]any, scope, groupID string) {
	paths := storage.NewPaths(s.snapshot.Home)
	path := paths.GlobalAgents
	if scope == "group" {
		pathGroup := groupID
		if pathGroup == "" {
			pathGroup = domain.DefaultGroupID
		}
		var err error
		path, err = paths.GroupAgents(pathGroup)
		if err != nil {
			return
		}
	}
	key := s.instructionAutosaveKey(scope, "", groupID, path)
	if ref, ok := s.instructionSaveRef(key); ok {
		// A retained, inactive editor can still have a pending autosave while
		// returning to its scope. Capture its save progress before that read too.
		payload["instructionSaveVersion"], payload["instructionBaselineHash"] = ref.saveVersion, ref.hash
	}
}

func (s *app) renderSettingsInstructionMarkdown(c *ui.Context, p palette, state *markdownEditorState, height float32) {
	s.ensureSettingsInstructionLiveState(state)
	if state.live == nil {
		return
	}
	s.observeSettingsInstructionInput(state)
	source := state.liveSource
	if state.liveFramework == nil || state.liveFramework.original != source {
		snapshot := state.live.Buffer().Snapshot()
		native := &frameworkMarkdownState{original: source, projection: editor.ReadingProjection{Source: snapshot.String()}}
		native.document = parser.Parse(snapshot.String())
		native.imageTargets = frameworkMarkdownImageTargets(native.document)
		native.sourceOffset = func(offset int) int {
			return editor.RuneToByteOffset(source, textEditorSourceRuneOffset(source, offset))
		}
		state.liveFramework = native
	}
	native := state.liveFramework
	state.zoom = s.markdownZoom()
	state.imageRenderSeen = make(map[string]bool)
	for index := 1; index < native.document.Len(); index++ {
		for _, node := range native.document.Inlines(index) {
			if node.Kind == parser.Image {
				part := native.partAt(state, node.Start)
				original := editor.Node{Destination: node.Dest, Range: editor.SourceRange{Start: native.originalOffset(node.Start), End: native.originalOffset(node.End)}}
				state.imageRenderSeen[markdownImageNodeKey(part, original)] = true
			}
		}
	}
	s.configureFrameworkMarkdown(c, p, state, native)
	defer func() {
		s.pruneMarkdownImagePreviews(state, state.imageRenderSeen)
		state.imageRenderSeen = nil
	}()
	pane := ui.Column(c.Key("settings-instruction-markdown")).FillWidth().MinWidth(0).MinHeight(0).Gap(6)
	if height > 0 {
		pane.Height(height)
	} else {
		pane.Grow(1)
	}
	pane.Children(func() {
		if state.liveError != "" {
			label(c, state.liveError, 11, p.amber).Wrap()
		}
		markdownui.MarkdownEditor(c, state.componentEditor, markdownui.EditorOptions{
			Label: firstNonEmpty(state.binding.label, "说明 Markdown 编辑区"), ReadClipboard: s.markdownClipboardContent,
			OnImportResources: func(ctx context.Context, request markdownui.ImportRequest, complete func(markdownui.ImportResult, error)) {
				s.importInstructionMarkdownResources(state, ctx, request, complete)
			},
			ReaderOptions: markdownui.ReaderOptions{Native: &native.options, OnError: func(err error) {
				if err != nil && s.markdownEditorIsCurrent(state) {
					state.liveError = err.Error()
				}
			}},
			OnChange: func(markdownui.Snapshot) { s.observeSettingsInstructionInput(state) },
			OnCompositionChanged: func(composing bool) {
				if s.markdownEditorIsCurrent(state) {
					s.setMarkdownEditorComposing(state, composing)
					if !composing {
						s.observeSettingsInstructionInput(state)
					}
				}
			},
			OnSave:        func(markdownui.Snapshot) { s.saveMarkdownEditor(state) },
			OnModeChanged: func(mode markdownui.Mode) { state.sourceMode = mode == markdownui.Source },
		}).Grow(1).FillWidth().MinHeight(0)
	})
}
