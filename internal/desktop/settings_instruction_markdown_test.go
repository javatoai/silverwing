package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/editor"
	"silverwing-go/internal/storage"
	markdownui "silverwing-go/internal/ui/markdown"

	"github.com/egoist/mygo/ui"
)

type settingsInstructionBackend struct {
	*application.App
	fail atomic.Bool
}

func (b *settingsInstructionBackend) Dispatch(ctx context.Context, action application.Action) (application.ActionResult, error) {
	if action.Kind == "instructions.save" && b.fail.Load() {
		return application.ActionResult{}, errors.New("fixture instruction write failure")
	}
	return b.App.Dispatch(ctx, action)
}

func settingsInstructionFixture(t *testing.T, source string) (*app, *settingsInstructionBackend, *ui.Tester) {
	t.Helper()
	paths := storage.NewPaths(t.TempDir())
	config := domain.DefaultAppConfig()
	config.TaskRoot = &paths.Tasks
	backend := &settingsInstructionBackend{App: &application.App{Paths: paths, Config: storage.NewConfigStore(paths), Manifests: storage.NewManifestStore(), Documents: documents.NewService(paths.Cache)}}
	if err := backend.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	groupPath, err := paths.GroupAgents(domain.DefaultGroupID)
	if err != nil {
		t.Fatal(err)
	}
	for path, text := range map[string]string{paths.GlobalAgents: source, groupPath: "# 分组\n\n组说明\n"} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s := &app{page: "settings", backend: backend, backendGate: make(chan struct{}, 1),
		materialsUnit: materialsController{state: materialsState{documentSaveRefs: map[string]documentSaveRef{}}}}
	s.snapshot.Home = paths.Home
	s.snapshot.Groups = []application.Group{{ID: domain.DefaultGroupID, Name: "默认分组"}}
	s.settingsUnit.state.settingsPage, s.settingsUnit.state.settingsAgentScope = "agents", "global"
	s.settingsUnit.state.settingsAgentContext = "global:"
	s.initMarkdownAutosave()
	s.materialsUnit.state.markdownAutosave.SetDebounce(time.Hour)
	t.Cleanup(func() {
		for _, state := range s.ensureTaskNotesUI().markdownEditors {
			closeMarkdownPreviewState(state)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		s.materialsUnit.state.markdownAutosave.Close(ctx)
	})
	read := application.Action{Kind: "instructions.read", Payload: map[string]any{"scope": "global"}}
	result, err := backend.Dispatch(context.Background(), read)
	if err != nil {
		t.Fatal(err)
	}
	s.applyInstructionReadResult(read, result)
	view := ui.NewTester(func(c *ui.Context) {
		s.beginOverflowMenuFrame()
		ui.Column(c).Fill().Children(func() { s.settingsAgents(c, setTheme(c, false)) })
		s.endOverflowMenuFrame()
	}, 920, 600)
	return s, backend, view
}

func appendSettingsInstruction(t *testing.T, s *app, view *ui.Tester, text string) *markdownEditorState {
	t.Helper()
	state := s.ensureTaskNotesUI().activeMarkdown
	if state == nil || state.readOnly || state.live == nil || state.componentEditor == nil {
		t.Fatal("settings did not expose the public Markdown editor")
	}
	end := len(state.live.Buffer().Snapshot().String())
	state.live.SetSelection(end, end)
	state.live.Focus()
	view.Frame()
	view.Type(text)
	if err := s.captureMarkdownEditorInput(state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestSettingsInstructionsSharedEditorSaveUndoAndModes(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		t.Run(strings.ReplaceAll(newline, "\r", "CR")+"LF", func(t *testing.T) {
			source := "\xef\xbb\xbf# 全局" + newline + newline + "保留 **原文**、中文😀及尾空格  " + newline
			s, backend, view := settingsInstructionFixture(t, source)
			state := appendSettingsInstruction(t, s, view, "新增文字")
			buffer := state.live.Buffer()
			if _, ok := view.Find("说明 Markdown 编辑区"); !ok || state.componentEditor.Mode() != markdownui.Live {
				t.Fatal("settings must default to the shared live editor")
			}
			if failures := flushMaterialTextTest(t, s); len(failures) > 0 {
				t.Fatal(failures)
			}
			disk, err := os.ReadFile(backend.Paths.GlobalAgents)
			if err != nil || string(disk) != source+"新增文字" {
				t.Fatalf("autosave changed unrelated source/BOM/newlines: %q %v", disk, err)
			}
			for _, mode := range []markdownui.Mode{markdownui.Preview, markdownui.Source, markdownui.Live} {
				if err := state.componentEditor.SetMode(mode); err != nil {
					t.Fatal(err)
				}
				view.Frame()
				if state.live.Buffer() != buffer || state.componentEditor.Mode() != mode {
					t.Fatal("mode switch replaced the shared buffer")
				}
			}
			state.live.Focus()
			view.Frame()
			view.Command("undo")
			if err := s.captureMarkdownEditorInput(state); err != nil {
				t.Fatal(err)
			}
			if s.settingsUnit.state.settingsAgentContent != strings.TrimPrefix(source, "\xef\xbb\xbf") {
				t.Fatalf("save/mode switch lost native undo: %q", s.settingsUnit.state.settingsAgentContent)
			}
			if err := s.captureDocumentCloseInput(); err != nil {
				t.Fatal(err)
			}
			if failures := s.materialsUnit.state.markdownAutosave.PrepareClose(context.Background()); len(failures) > 0 {
				t.Fatal(failures)
			}
			disk, _ = os.ReadFile(backend.Paths.GlobalAgents)
			if string(disk) != source {
				t.Fatalf("normal close did not save the undo: %q", disk)
			}
		})
	}
}

func TestSettingsInstructionsScopeAndLateResultIsolation(t *testing.T) {
	s, backend, view := settingsInstructionFixture(t, "# 全局\n\n原文\n")
	global := appendSettingsInstruction(t, s, view, "全局新增")
	globalKey := global.binding.id
	if !s.leaveSettingsInstructionEditor() {
		t.Fatal(s.lastError)
	}
	s.settingsUnit.state.settingsAgentScope, s.settingsUnit.state.settingsAgentGroupID = domain.DefaultGroupID, domain.DefaultGroupID
	s.settingsUnit.state.settingsAgentContext = "group:" + domain.DefaultGroupID
	read := application.Action{Kind: "instructions.read", Payload: map[string]any{"scope": "group", "groupId": domain.DefaultGroupID}}
	result, err := backend.Dispatch(context.Background(), read)
	if err != nil {
		t.Fatal(err)
	}
	s.applyInstructionReadResult(read, result)
	view.Frame()
	group := appendSettingsInstruction(t, s, view, "分组新增")
	if group == global || group.live.Buffer() == global.live.Buffer() {
		t.Fatal("global and group share an input buffer")
	}
	global.binding.commit("stale callback")
	s.applyInstructionReadResult(application.Action{Kind: "instructions.read", Payload: map[string]any{"scope": "global"}}, application.ActionResult{Path: backend.Paths.GlobalAgents, Text: "late global read"})
	staleHome := application.Action{Kind: "instructions.read", Payload: map[string]any{"scope": "group", "groupId": domain.DefaultGroupID, "instructionHome": filepath.Join(backend.Paths.Home, "old-home")}}
	s.applyInstructionReadResult(staleHome, application.ActionResult{Path: result.Path, Text: "wrong home"})
	if !strings.Contains(s.settingsUnit.state.settingsAgentContent, "分组新增") {
		t.Fatal("a stale scope/home callback replaced the current document")
	}
	if failures := flushMaterialTextTest(t, s); len(failures) > 0 {
		t.Fatal(failures)
	}
	globalDisk, _ := os.ReadFile(backend.Paths.GlobalAgents)
	groupDisk, _ := os.ReadFile(result.Path)
	if string(globalDisk) != "# 全局\n\n原文\n全局新增" || string(groupDisk) != "# 分组\n\n组说明\n分组新增" {
		t.Fatalf("scope saves crossed: global=%q group=%q", globalDisk, groupDisk)
	}
	if ref, ok := s.instructionSaveRef(globalKey); !ok || ref.scope != "global" {
		t.Fatal("inactive save lost the captured scope")
	}
}

func TestSettingsInstructionsConflictAcceptDiskAndNewerInput(t *testing.T) {
	s, backend, view := settingsInstructionFixture(t, "# 原始\n")
	state := appendSettingsInstruction(t, s, view, "本地")
	key := state.binding.id
	if err := os.WriteFile(state.path, []byte("# 外部\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if failures := flushMaterialTextTest(t, s); len(failures) != 1 || !s.materialsUnit.state.markdownAutosave.Status(key).Conflict {
		t.Fatalf("external change must conflict: %v", failures)
	}
	view.Frame()
	for _, name := range []string{"接受外部版本", "确认覆盖"} {
		if _, ok := view.Find(name); !ok {
			t.Fatalf("missing conflict action %s", name)
		}
	}
	read := application.Action{Kind: "instructions.read", Payload: map[string]any{"scope": "global", "acceptDisk": true, "instructionBufferVersion": state.live.Buffer().Version()}}
	result, err := backend.Dispatch(context.Background(), read)
	if err != nil {
		t.Fatal(err)
	}
	s.applyInstructionReadResult(read, result)
	view.Frame()
	if state.live.Buffer().Snapshot().OriginalText() != "# 外部\n" || state.doc.Source() != "# 外部\n" {
		t.Fatal("AcceptDisk did not update the public buffer and derived reader")
	}
	read.Payload["instructionBufferVersion"] = state.live.Buffer().Version()
	appendSettingsInstruction(t, s, view, "保留新输入")
	s.applyInstructionReadResult(read, result)
	view.Frame()
	if !strings.HasSuffix(state.live.Buffer().Snapshot().OriginalText(), "保留新输入") || !s.materialsUnit.state.markdownAutosave.Status(key).Dirty {
		t.Fatal("late accept-disk response overwrote newer input")
	}
}

func TestSettingsInstructionsFailureRetryOverwriteAndClear(t *testing.T) {
	s, backend, view := settingsInstructionFixture(t, "# 原始\n")
	state := appendSettingsInstruction(t, s, view, "本地")
	key := state.binding.id
	backend.fail.Store(true)
	if failures := flushMaterialTextTest(t, s); len(failures) != 1 {
		t.Fatalf("expected failed save, got %v", failures)
	}
	view.Frame()
	if _, ok := view.Find("重试保存"); !ok {
		t.Fatal("settings does not expose save retry")
	}
	backend.fail.Store(false)
	s.retryMarkdownAutosave(key)
	if failures := flushMaterialTextTest(t, s); len(failures) != 0 {
		t.Fatal(failures)
	}
	appendSettingsInstruction(t, s, view, "继续")
	if err := os.WriteFile(state.path, []byte("外部版本"), 0600); err != nil {
		t.Fatal(err)
	}
	if failures := flushMaterialTextTest(t, s); len(failures) != 1 {
		t.Fatal("expected hash conflict")
	}
	s.overwriteMarkdownAfterConflict(key)
	if failures := flushMaterialTextTest(t, s); len(failures) != 0 {
		t.Fatal(failures)
	}
	if err := state.componentEditor.SetMode(markdownui.Source); err != nil {
		t.Fatal(err)
	}
	state.live.Focus()
	view.Frame()
	view.Command("selectAll")
	view.Key(0, ui.KeyBackspace)
	if err := s.captureMarkdownEditorInput(state); err != nil {
		t.Fatal(err)
	}
	if failures := flushMaterialTextTest(t, s); len(failures) != 0 {
		t.Fatal(failures)
	}
	disk, _ := os.ReadFile(state.path)
	if len(disk) != 0 {
		t.Fatalf("clearing instructions did not save an empty document: %q", disk)
	}
}

func TestSettingsInstructionsRejectedIMEKeepsScope(t *testing.T) {
	s, _, view := settingsInstructionFixture(t, "正文")
	state := s.ensureTaskNotesUI().activeMarkdown
	state.live.SetSelection(len("正文"), len("正文"))
	state.live.Focus()
	view.Frame()
	state.live.Buffer().SetMaxSourceBytes(len("正文"))
	view.Compose("输入", 2)
	key := s.ensureTaskNotesUI().activeKey
	if s.leaveSettingsInstructionEditor() || !state.live.Composing() || s.ensureTaskNotesUI().activeKey != key || s.settingsUnit.state.settingsAgentScope != "global" {
		t.Fatal("rejected IME lost the active document")
	}
	if err := s.captureDocumentCloseInput(); err == nil {
		t.Fatal("close must preserve a rejected input draft")
	}
	state.live.Buffer().SetMaxSourceBytes(editor.MaxDocumentBytes)
	if !s.leaveSettingsInstructionEditor() || s.settingsUnit.state.settingsAgentContent != "正文输入" {
		t.Fatalf("valid pending IME did not commit before leaving: %q %s", s.settingsUnit.state.settingsAgentContent, s.lastError)
	}
}

func TestSettingsInstructionsEditingRequiresReadPermission(t *testing.T) {
	s, _, _ := settingsInstructionFixture(t, "正文")
	for _, scope := range []string{"task", "", "unknown", "global", "group"} {
		for _, readOnly := range []bool{false, true} {
			ref := documentSaveRef{kind: "instruction", scope: scope, readOnly: readOnly}
			want := !readOnly && (scope == "global" || scope == "group")
			if markdownSaveRefWritable(ref) != want {
				t.Fatalf("permission widened for %#v", ref)
			}
		}
	}
	if !instructionMarkdownReadOnly(s, "missing read") {
		t.Fatal("missing read capability must fail closed")
	}
}

func TestSettingsInstructionsLateReadAfterSaveKeepsSourceAndUndo(t *testing.T) {
	for _, tc := range []struct {
		name       string
		acceptDisk bool
	}{{"reload", false}, {"accept-disk", true}} {
		t.Run(tc.name, func(t *testing.T) {
			const original = "# 原始\n\n正文\n"
			s, backend, view := settingsInstructionFixture(t, original)
			state := s.ensureTaskNotesUI().activeMarkdown
			read := application.Action{Kind: "instructions.read", Payload: map[string]any{
				"scope": "global", "acceptDisk": tc.acceptDisk, "instructionHome": s.snapshot.Home,
				"instructionBufferVersion": state.live.Buffer().Version(),
			}}
			oldResult, err := backend.Dispatch(context.Background(), read)
			if err != nil {
				t.Fatal(err)
			}
			appendSettingsInstruction(t, s, view, "已经保存的新输入")
			if failures := flushMaterialTextTest(t, s); len(failures) > 0 {
				t.Fatal(failures)
			}
			key, version := state.binding.id, state.live.Buffer().Version()
			ref, _ := s.instructionSaveRef(key)
			s.applyInstructionReadResult(read, oldResult)
			view.Frame()
			after, _ := s.instructionSaveRef(key)
			if state.live.Buffer().Snapshot().OriginalText() != original+"已经保存的新输入" || state.live.Buffer().Version() != version || after.hash != ref.hash {
				t.Fatal("late read reset saved source, native version or baseline hash")
			}
			state.live.Focus()
			view.Frame()
			view.Command("undo")
			if err := s.captureMarkdownEditorInput(state); err != nil || s.settingsUnit.state.settingsAgentContent != original {
				t.Fatalf("late read cleared native undo: %q %v", s.settingsUnit.state.settingsAgentContent, err)
			}
		})
	}
}

func TestSettingsInstructionsReadFailureKeepsEditorUsable(t *testing.T) {
	s, _, view := settingsInstructionFixture(t, "# 原始\n")
	state := appendSettingsInstruction(t, s, view, "保留草稿")
	read := application.Action{Kind: "instructions.read", Payload: map[string]any{"scope": "global", "instructionHome": s.snapshot.Home}}
	s.applyInstructionReadFailure(read, errors.New("读取失败样例"))
	if !s.settingsUnit.state.settingsAgentLoaded || !s.markdownEditorIsCurrent(state) {
		t.Fatal("refresh failure invalidated the retained editing owner")
	}
	view.Frame()
	if _, ok := view.Find("说明 Markdown 编辑区"); !ok {
		t.Fatal("refresh failure hid the usable editor")
	}
	if _, ok := view.Find("重试读取"); !ok {
		t.Fatal("refresh failure does not expose retry")
	}
	if err := s.captureDocumentCloseInput(); err != nil {
		t.Fatal(err)
	}
	if !s.leaveSettingsInstructionEditor() {
		t.Fatal(s.lastError)
	}
	if content, _ := s.materialsUnit.state.markdownAutosave.Content(state.binding.id); !strings.HasSuffix(content, "保留草稿") {
		t.Fatal("refresh failure discarded the committed draft")
	}
	// An old home's failure must not change the current read/error state.
	s.ensureTaskNotesUI().settingsReadError = ""
	read.Payload["instructionHome"] = filepath.Join(s.snapshot.Home, "old-home")
	s.applyInstructionReadFailure(read, errors.New("stale failure"))
	if s.ensureTaskNotesUI().settingsReadError != "" || !s.settingsUnit.state.settingsAgentLoaded {
		t.Fatal("old-home failure changed the current context")
	}
	// With no successful content yet, the first failure remains an error state.
	s.settingsUnit.state.settingsAgentLoaded = false
	read.Payload["instructionHome"] = s.snapshot.Home
	s.applyInstructionReadFailure(read, errors.New("首次读取失败"))
	if s.settingsUnit.state.settingsAgentLoaded || s.ensureTaskNotesUI().settingsReadError != "首次读取失败" {
		t.Fatal("initial read failure incorrectly exposed an editor")
	}
}

func settingsInstructionReadTicket(s *app, acceptDisk bool) application.Action {
	state := s.ensureTaskNotesUI().activeMarkdown
	action := application.Action{Kind: "instructions.read", Payload: map[string]any{
		"scope": "global", "instructionHome": s.snapshot.Home, "acceptDisk": acceptDisk,
		"instructionBufferVersion": state.live.Buffer().Version(),
	}}
	s.captureSettingsInstructionSaveVersion(action.Payload, "global", "")
	return action
}

func TestSettingsInstructionsReadBeforePendingSaveKeepsNewestBaseline(t *testing.T) {
	for _, tc := range []struct {
		name       string
		acceptDisk bool
	}{{"reload", false}, {"accept-disk", true}} {
		t.Run(tc.name, func(t *testing.T) {
			s, backend, view := settingsInstructionFixture(t, "# 原始\n")
			state := appendSettingsInstruction(t, s, view, "待保存的新输入")
			read := settingsInstructionReadTicket(s, tc.acceptDisk)
			oldResult, err := backend.Dispatch(context.Background(), read)
			if err != nil {
				t.Fatal(err)
			}
			if failures := flushMaterialTextTest(t, s); len(failures) > 0 {
				t.Fatal(failures)
			}
			version := state.live.Buffer().Version()
			ref, _ := s.instructionSaveRef(state.binding.id)
			s.applyInstructionReadResult(read, oldResult)
			view.Frame()
			after, _ := s.instructionSaveRef(state.binding.id)
			if state.live.Buffer().Snapshot().OriginalText() != "# 原始\n待保存的新输入" || state.live.Buffer().Version() != version || after.hash != ref.hash || after.saveVersion != ref.saveVersion {
				t.Fatal("a save completed without a buffer edit, but the older read replaced its source/baseline")
			}
		})
	}
}

type settingsInstructionSaveBarrier struct {
	*settingsInstructionBackend
	saved, release chan struct{}
	started        atomic.Bool
}

func (b *settingsInstructionSaveBarrier) Dispatch(ctx context.Context, action application.Action) (application.ActionResult, error) {
	result, err := b.settingsInstructionBackend.Dispatch(ctx, action)
	if action.Kind == "instructions.save" && err == nil && b.started.CompareAndSwap(false, true) {
		close(b.saved)
		select {
		case <-b.release:
		case <-ctx.Done():
			return result, ctx.Err()
		}
	}
	return result, err
}

func TestSettingsInstructionsReadDuringSaveDoesNotInventExternalConflict(t *testing.T) {
	s, backend, view := settingsInstructionFixture(t, "# 原始\n")
	state := appendSettingsInstruction(t, s, view, "正在保存")
	read := settingsInstructionReadTicket(s, false)
	barrier := &settingsInstructionSaveBarrier{settingsInstructionBackend: backend, saved: make(chan struct{}), release: make(chan struct{})}
	s.backend = barrier
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	finished := make(chan []editor.FlushFailure, 1)
	go func() { finished <- s.materialsUnit.state.markdownAutosave.Flush(ctx) }()
	select {
	case <-barrier.saved:
	case <-ctx.Done():
		t.Fatal("save did not reach its publication barrier")
	}
	released := false
	defer func() {
		if !released {
			close(barrier.release)
		}
	}()
	// Model the publication gap: bytes are on disk, but the writer has not
	// published its new hash or left Saving. Reading those bytes is not an
	// external modification and must not seed a conflict against our old ref.
	result, err := backend.App.Dispatch(context.Background(), read)
	if err != nil {
		t.Fatal(err)
	}
	s.applyInstructionReadResult(read, result)
	if status := s.materialsUnit.state.markdownAutosave.Status(state.binding.id); status.Conflict {
		t.Fatal("our own in-flight save was mistaken for an external change")
	}
	close(barrier.release)
	released = true
	if failures := <-finished; len(failures) > 0 {
		t.Fatal(failures)
	}
}

func TestSettingsInstructionsReturningScopeRetainsPendingSave(t *testing.T) {
	s, backend, view := settingsInstructionFixture(t, "# 原始\n")
	state := appendSettingsInstruction(t, s, view, "返回前的新输入")
	if !s.leaveSettingsInstructionEditor() {
		t.Fatal(s.lastError)
	}
	s.settingsUnit.state.settingsAgentLoaded = false
	read := application.Action{Kind: "instructions.read", Payload: map[string]any{"scope": "global", "instructionHome": s.snapshot.Home}}
	s.captureSettingsInstructionSaveVersion(read.Payload, "global", "")
	if _, captured := read.Payload["instructionSaveVersion"]; !captured {
		t.Fatal("returning to an inactive scope did not capture its pending save")
	}
	oldResult, err := backend.Dispatch(context.Background(), read)
	if err != nil {
		t.Fatal(err)
	}
	if failures := flushMaterialTextTest(t, s); len(failures) > 0 {
		t.Fatal(failures)
	}
	s.applyInstructionReadResult(read, oldResult)
	if !s.ensureTaskNotesUI().settingsReadDeferred || s.settingsUnit.state.settingsAgentLoaded {
		t.Fatal("a stale initial read must defer a fresh read instead of showing old content or an empty pane")
	}
	read.Payload = map[string]any{"scope": "global", "instructionHome": s.snapshot.Home}
	s.captureSettingsInstructionSaveVersion(read.Payload, "global", "")
	fresh, err := backend.Dispatch(context.Background(), read)
	if err != nil {
		t.Fatal(err)
	}
	s.applyInstructionReadResult(read, fresh)
	view.Frame()
	if s.ensureTaskNotesUI().activeMarkdown != state || state.live.Buffer().Snapshot().OriginalText() != "# 原始\n返回前的新输入" || !s.settingsUnit.state.settingsAgentLoaded {
		t.Fatal("fresh read failed to restore the retained public editor")
	}
}
