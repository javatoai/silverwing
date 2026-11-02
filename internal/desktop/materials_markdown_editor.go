package desktop

import (
	"context"
	"errors"
	"path/filepath"

	"silverwing-go/internal/application"
	"silverwing-go/internal/editor"
	markdownui "silverwing-go/internal/ui/markdown"

	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

type materialsMarkdownReadVersion struct {
	key, hash   string
	saveVersion uint64
	hasRef      bool
}

// Capture this on the window thread at the same boundary as the read's task
// identity. The backend ignores this desktop-only result-ordering metadata.
func (s *app) captureMaterialsMarkdownReadVersion(action *application.Action) {
	if action.Kind != "document.read" || documentKindForPath(action.Path) != "markdown" {
		return
	}
	key := s.documentAutosaveKeyForTask(action.TaskID, action.Path)
	if key == "" {
		return
	}
	ref, exists := s.materialsUnit.documentSaveRef(key)
	if action.Payload == nil {
		action.Payload = make(map[string]any)
	}
	action.Payload["desktopMarkdownReadVersion"] = materialsMarkdownReadVersion{key: key, hash: ref.hash, saveVersion: ref.saveVersion, hasRef: exists}
}

type materialsMarkdownReadGuard struct {
	app    *app
	locked bool
}

func (guard *materialsMarkdownReadGuard) release() {
	if guard != nil && guard.locked {
		guard.locked = false
		guard.app.materialsUnit.state.documentSaveMu.Unlock()
	}
}

// Keep the version check and Autosave.Seed in the save-ref publication lock.
// A save already underway then remains Saving until seeding finishes, so Seed
// cannot mistake its newest content for a clean entry and replace it.
func (s *app) beginMaterialsMarkdownRead(action application.Action, path string) (*materialsMarkdownReadGuard, bool) {
	if documentKindForPath(path) != "markdown" || s.materialsUnit.state.markdownAutosave == nil {
		return nil, true
	}
	s.materialsUnit.state.documentSaveMu.Lock()
	guard := &materialsMarkdownReadGuard{app: s, locked: true}
	key := s.documentAutosaveKeyForTask(action.TaskID, path)
	// The backend may have written the new bytes and released its task lock
	// before the desktop save callback publishes the new baseline. Such a read
	// is neither an external edit nor an authoritative clean snapshot yet.
	if s.materialsUnit.state.markdownAutosave.Status(key).Saving {
		guard.release()
		s.deferMaterialsMarkdownRead()
		return nil, false
	}
	if captured, ok := action.Payload["desktopMarkdownReadVersion"].(materialsMarkdownReadVersion); ok {
		current, exists := s.materialsUnit.state.documentSaveRefs[captured.key]
		if captured.key != s.documentAutosaveKeyForTask(action.TaskID, path) || exists != captured.hasRef || exists && (current.hash != captured.hash || current.saveVersion != captured.saveVersion) {
			guard.release()
			s.deferMaterialsMarkdownRead()
			return nil, false
		}
	}
	return guard, true
}

func (s *app) deferMaterialsMarkdownRead() {
	if s.win != nil && !s.win.IsDestroyed() {
		s.ensureMaterialsFileManagementState().windowRefreshPending = true
	}
}

// Editing is granted by a task-materials read, never by a filename alone.
func (s *app) materialsMarkdownReadWritable(action application.Action, result application.ActionResult, path string) bool {
	if action.TaskID != s.taskUnit.currentTask().id || payloadString(action.Payload, "markdownEditContext") != taskMaterialsMarkdownEditContext ||
		normalizeIdentityPath(action.Path) != normalizeIdentityPath(path) || dataString(result.Data, "markdownEditContext") != taskMaterialsMarkdownEditContext || dataBool(result.Data, "readOnly") || !s.materialsWritable(path, false) {
		return false
	}
	scope, ok := s.materialsFileScope(action.TaskID)
	return ok && normalizeIdentityPath(payloadString(action.Payload, "draftTaskPath")) == normalizeIdentityPath(scope.taskPath) &&
		normalizeIdentityPath(payloadString(action.Payload, "draftMaterialsRoot")) == normalizeIdentityPath(scope.root) &&
		payloadString(action.Payload, "desktopRenameTaskCreatedAt") == scope.createdAt && relativeDocumentPath(scope.root, path) != ""
}

func (s *app) materialsMarkdownWritable(path string) bool {
	if path == "" || s.materialsUnit.state.documentReadOnly || documentKindForPath(path) != "markdown" || !s.materialsWritable(path, false) {
		return false
	}
	ref, ok := s.materialsUnit.documentSaveRef(s.documentAutosaveKey(path))
	task := s.taskUnit.currentTask()
	return ok && ref.kind == "material-markdown" && markdownSaveRefWritable(ref) && ref.taskID == task.id && ref.createdAt == task.createdAt &&
		normalizeIdentityPath(ref.taskPath) == normalizeIdentityPath(task.path) && normalizeIdentityPath(ref.materialsRoot) == normalizeIdentityPath(task.materialsPath)
}

func (s *app) ensureMaterialsMarkdownLiveState(state *markdownEditorState) {
	if state == nil || state.readOnly || state.live != nil || !s.materialsMarkdownWritable(state.path) {
		return
	}
	scope, ok := s.materialsFileScope(state.taskID)
	if !ok {
		return
	}
	buffer := codeeditor.NewBufferPreservingLineEndings(s.materialsUnit.state.documentContent)
	state.live = markdown.NewEditorState(buffer)
	state.componentEditor = markdownui.AdoptEditorState(markdownui.Document{ID: markdownEditorHostID(state), Source: s.materialsUnit.state.documentContent, Path: state.path}, state.live)
	s.bindMarkdownViewMemory(state)
	state.liveVersion = buffer.Version()
	state.liveSource = s.materialsUnit.state.documentContent
	state.liveIdentity = materialsFileScopeKey(scope)
	s.configureMarkdownSourceLimit(state)
}

// The native Buffer is the only editable source and history. The application
// Document is a derived reader model, updated without creating undo entries.
func (s *app) observeMaterialsMarkdownInput(state *markdownEditorState) {
	if state == nil || state.live == nil || state.readOnly || !s.markdownEditorIsCurrent(state) || state.live.Composing() {
		return
	}
	snapshot := state.live.Buffer().Snapshot()
	if snapshot.Version() == state.liveVersion {
		return
	}
	state.liveVersion = snapshot.Version()
	state.liveError = ""
	key := s.documentAutosaveKey(state.path)
	s.materialsUnit.state.documentSaveMu.Lock()
	if ref, ok := s.materialsUnit.state.documentSaveRefs[key]; ok {
		ref.restoredDraft = false
		s.materialsUnit.state.documentSaveRefs[key] = ref
	}
	s.materialsUnit.state.documentSaveMu.Unlock()
	source := snapshot.OriginalText()
	state.liveSource = source
	if state.doc.Source() != source {
		state.doc.SyncExternal(source)
	}
	s.commitMarkdownEditorSource(state, source)
}

func (s *app) captureMaterialsMarkdownInput(state *markdownEditorState) error {
	if state == nil || state.live == nil || state.readOnly {
		return nil
	}
	if !s.markdownEditorIsCurrent(state) || !s.materialsMarkdownWritable(state.path) {
		return errors.New("Markdown 编辑上下文已失效，请重新读取文件")
	}
	if state.componentEditor != nil {
		if _, err := state.componentEditor.CaptureInput(); err != nil {
			state.liveError = err.Error()
			return err
		}
	} else if !state.live.EndComposition() {
		message := state.liveError
		if message == "" {
			message = "中文输入尚未提交，请处理编辑错误后再继续；当前原文已保留"
		}
		return errors.New(message)
	}
	if state.live.Composing() {
		return editor.ErrComposing
	}
	s.observeMaterialsMarkdownInput(state)
	s.setMarkdownEditorComposing(state, false)
	return nil
}

// Only an accepted disk read, draft recovery or discard reaches this path.
// Autosave acknowledgements update the hash without resetting the Buffer.
func (s *app) syncMaterialsMarkdownBuffer(state *markdownEditorState, source string) {
	if state == nil || state.live == nil || state.live.Composing() {
		return
	}
	if state.liveSource != source {
		anchor, caret := state.live.Selection()
		if state.componentEditor != nil {
			if err := state.componentEditor.Reload(source, state.live.Buffer().Version()); err != nil {
				state.liveError = err.Error()
				return
			}
		} else {
			state.live.Buffer().Reset(source)
		}
		state.live.SetSelection(anchor, caret)
		state.liveVersion = state.live.Buffer().Version()
		state.liveSource = source
	}
	if state.doc.Source() != source {
		state.doc.SyncExternal(source)
	}
}

func (s *app) materialsMarkdownEditorVisible() bool {
	return s.materialsMarkdownWritable(s.materialsUnit.state.documentPath) && !s.materialsSearchSourcePreviewActive()
}

func (s *app) renderMaterialsMarkdownEditor(c *ui.Context, p palette) {
	state := s.ensureMarkdownEditor()
	if state.live == nil || state.readOnly {
		return
	}
	s.observeMaterialsMarkdownInput(state)
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
	if state.componentEditor == nil {
		state.componentEditor = markdownui.AdoptEditorState(markdownui.Document{ID: markdownEditorHostID(state), Path: state.path}, state.live)
	}
	s.bindMarkdownViewMemory(state)
	s.restoreMarkdownViewMemory(state)
	if fragment := s.materialsUnit.state.documentLinkFragment; fragment != "" {
		line, column := markdownLinkLocation(fragment)
		s.materialsUnit.state.documentLinkFragment = ""
		if err := state.componentEditor.RevealLocation(line, column, fragment); err != nil {
			state.liveError = err.Error()
		}
	}
	state.zoom = s.markdownZoom()
	state.imageRenderSeen = make(map[string]bool)
	// Source expansion hides rendered images without removing their references.
	// Keep every resource in the document, including repeated and offscreen ones.
	for index := 1; index < native.document.Len(); index++ {
		for _, node := range native.document.Inlines(index) {
			if node.Kind != parser.Image {
				continue
			}
			part := native.partAt(state, node.Start)
			original := editor.Node{Destination: node.Dest, Range: editor.SourceRange{
				Start: native.originalOffset(node.Start), End: native.originalOffset(node.End),
			}}
			state.imageRenderSeen[markdownImageNodeKey(part, original)] = true
		}
	}
	s.configureFrameworkMarkdown(c, p, state, native)
	defer func() {
		s.pruneMarkdownImagePreviews(state, state.imageRenderSeen)
		state.imageRenderSeen = nil
	}()
	ui.Column(c.Key("materials-markdown-editor-pane")).Grow(1).FillWidth().MinHeight(0).Padding(12).Children(func() {
		markdownui.MarkdownEditor(c.Key("materials-markdown-editor-"+markdownEditorHostID(state)), state.componentEditor, markdownui.EditorOptions{
			ReadClipboard: s.markdownClipboardContent,
			OnImportResources: func(ctx context.Context, request markdownui.ImportRequest, complete func(markdownui.ImportResult, error)) {
				s.importMarkdownResources(state, ctx, request, complete)
			},
			ReaderOptions: markdownui.ReaderOptions{Native: &native.options, OnError: func(err error) {
				if err != nil && s.markdownEditorIsCurrent(state) {
					state.liveError = err.Error()
				}
			}}, Label: "Markdown 编辑区", ReadOnly: !s.materialsUnit.state.documentEditing || s.materialsFileReadBlocked(),
			OnChange: func(markdownui.Snapshot) { s.observeMaterialsMarkdownInput(state) },
			OnCompositionChanged: func(composing bool) {
				if s.markdownEditorIsCurrent(state) {
					s.setMarkdownEditorComposing(state, composing)
					if !composing {
						s.observeMaterialsMarkdownInput(state)
					}
				}
			},
			OnSave: func(markdownui.Snapshot) { s.saveMarkdownEditor(state) },
			OnModeChanged: func(mode markdownui.Mode) {
				if s.markdownEditorIsCurrent(state) {
					state.sourceMode = mode == markdownui.Source
				}
			},
		}).Grow(1).FillWidth().MinHeight(0)
	})
	s.revealMaterialsMarkdownFind(state)
}

func (s *app) setMaterialsMarkdownMode(mode string) {
	if mode != "live" && mode != "source" {
		return
	}
	state := s.ensureMarkdownEditor()
	if state.live == nil || state.readOnly {
		return
	}
	if err := s.captureMaterialsMarkdownInput(state); err != nil {
		state.liveError = err.Error()
		return
	}
	s.materialsUnit.closeMaterialsSearchSourcePreview()
	s.materialsUnit.state.documentEditing = true
	if state.componentEditor != nil {
		next := markdownui.Live
		if mode == "source" {
			next = markdownui.Source
		}
		if err := state.componentEditor.SetMode(next); err != nil {
			state.liveError = err.Error()
			return
		}
	}
	state.live.SourceMode = mode == "source"
	state.sourceMode = state.live.SourceMode
	state.live.Focus()
}

// Successful saves remain silent; only errors and recovered drafts need a header.
func (s *app) renderMaterialsMarkdownHeader(c *ui.Context, p palette) {
	if !s.materialsMarkdownWritable(s.materialsUnit.state.documentPath) {
		return
	}
	state := s.ensureMarkdownEditor()
	if state.live == nil {
		return
	}
	s.observeMaterialsMarkdownInput(state)
	key, status := s.markdownSaveState()
	ref, _ := s.materialsUnit.documentSaveRef(key)
	c.OnShortcut(ui.Cmd, ui.KeyS, func() { s.saveMarkdownEditor(state) })
	message := markdownSaveLabel(status)
	if state.liveError != "" {
		message = state.liveError
	} else if message == "" && ref.restoredDraft {
		message = "有未保存草稿"
	}
	if message == "" {
		return
	}
	ui.Row(c.Key("materials-markdown-header")).Height(32).Shrink(0).Padding(0, 12).Gap(7).AlignItems(ui.Center).Children(func() {
		label(c, message, 11, p.amber).Grow(1).SingleLine().Tooltip(message)
		if status.Conflict {
			if action(c, "materials-markdown-use-disk", "refresh", "使用磁盘版本", p, false) {
				s.confirmNative("使用磁盘版本？", "确认后将丢弃当前文件的本地草稿。", filepath.Base(state.path), s.discardMarkdownAndReload)
			}
			if action(c, "materials-markdown-overwrite", "save", "覆盖磁盘版本", p, true) {
				s.confirmNative("覆盖磁盘版本？", "确认后将用当前本地内容替换磁盘上的外部修改。", filepath.Base(state.path), func() {
					if s.captureMaterialsMarkdownInput(state) == nil {
						s.overwriteMarkdownAfterConflict(key)
					}
				})
			}
		} else if status.Error != "" || status.CacheError != "" || ref.restoredDraft {
			if action(c, "materials-markdown-retry", "refresh", "重试保存", p, false) {
				if s.captureMaterialsMarkdownInput(state) == nil {
					s.retryMarkdownAutosave(key)
				}
			}
			if ref.restoredDraft && action(c, "materials-markdown-discard", "close", "丢弃草稿", p, false) {
				s.confirmNative("丢弃草稿？", "确认后将使用磁盘上的当前版本。", filepath.Base(state.path), s.discardMarkdownAndReload)
			}
		}
	})
}

func (s *app) revealMaterialsMarkdownFind(state *markdownEditorState) {
	find := s.materialsUnit.state.documentFind
	if state == nil || state.live == nil || !s.materialsUnit.state.documentFindOpen || find == nil || find.lastReveal == find.navigation {
		return
	}
	if hit, ok := find.current(); ok && hit.block == "live-source" {
		state.live.SetSelection(hit.start, hit.end)
		find.lastReveal = find.navigation
	}
}
