package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"sync"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/editor"
	"silverwing-go/internal/workspace"
)

type markdownTaskRootMigrationEntry struct {
	oldKey, newKey string
	before, after  documentSaveRef
}

// Captured on the window thread; the worker reads only this immutable plan.
type markdownTaskRootMigrationPlan struct {
	home     string
	preview  workspace.TaskRootMigrationPreview
	autosave *editor.Autosave
	tasks    []task
	entries  []markdownTaskRootMigrationEntry
	moves    map[string]string
	lockKeys []string
}

func migrationMarkdownPathPairEqual(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return normalizeIdentityPath(*left) == normalizeIdentityPath(*right)
}

func (p *markdownTaskRootMigrationPlan) matchesConfigJSON(value string, after bool) (bool, error) {
	var config domain.AppConfig
	if err := json.Unmarshal([]byte(value), &config); err != nil {
		return false, err
	}
	return p.matchesConfig(config, after), nil
}

func (p *markdownTaskRootMigrationPlan) matchesConfig(config domain.AppConfig, after bool) bool {
	if p == nil || config.SchemaVersion != domain.CurrentAppConfigSchemaVersion {
		return false
	}
	roots := p.preview.Roots.Before
	if after {
		roots = p.preview.Roots.After
	}
	return migrationMarkdownPathPairEqual(config.TaskRoot, roots.TaskRoot) && migrationMarkdownPathPairEqual(config.RequirementMaterialsRoot, roots.MaterialsRoot)
}

// A false result is not commit evidence. Data == nil or the backend's exact
// typed zero value has no result; malformed non-nil data is an error. Only the
// full After pair plus actual transferred-task mappings proves commit,
// including a post-commit error.
func (p *markdownTaskRootMigrationPlan) committedResult(result application.ActionResult) (workspace.TaskRootMigrationResult, bool, error) {
	var typed workspace.TaskRootMigrationResult
	if p == nil || result.Data == nil {
		return typed, false, nil
	}
	if value, ok := result.Data.(workspace.TaskRootMigrationResult); ok && reflect.DeepEqual(value, typed) {
		return typed, false, nil
	}
	if err := decodeWorkspaceValue(result.Data, &typed); err != nil {
		return typed, false, fmt.Errorf("任务目录迁移结果格式错误：%w", err)
	}
	if typed.Config.SchemaVersion != domain.CurrentAppConfigSchemaVersion {
		return typed, false, errors.New("任务目录迁移结果没有可验证的配置")
	}
	if p.matchesConfig(typed.Config, true) {
		if !reflect.DeepEqual(typed.PathMappings, p.preview.PathMappings) || typed.MigratedTasks != len(p.preview.PathMappings) {
			return typed, false, errors.New("任务目录迁移结果与确认的实际任务范围不一致")
		}
		return typed, true, nil
	}
	if p.matchesConfig(typed.Config, false) {
		return typed, false, nil
	}
	return typed, false, errors.New("任务目录迁移提交状态无法确认")
}

func (s *app) hasPendingTaskMarkdownMigrationSource() bool {
	if s.materialsUnit.state.markdown != nil && s.materialsUnit.state.markdown.doc != nil && !s.materialsUnit.state.markdown.closed && (s.materialsUnit.state.documentDirty || s.materialsUnit.state.markdown.doc.Composing()) {
		return true
	}
	s.materialsUnit.state.documentSaveMu.Lock()
	defer s.materialsUnit.state.documentSaveMu.Unlock()
	for key, ref := range s.materialsUnit.state.documentSaveRefs {
		if ref.taskID == "" || ref.kind == "instruction" && ref.scope != "" {
			continue
		}
		if s.materialsUnit.state.markdownAutosave != nil {
			status := s.materialsUnit.state.markdownAutosave.Status(key)
			if status.Dirty || status.Saving || status.Composing {
				return true
			}
		}
	}
	return false
}

// Capture runs before dispatch sets busy and before the worker takes its gate.
// Unplanned inert callers are retained only when no task source is pending.
func (s *app) captureMarkdownTaskRootMigration(action application.Action) (*markdownTaskRootMigrationPlan, error) {
	if action.Kind == "config.task-root.save" {
		if s.materialsUnit.state.pendingTaskRootMigration != nil || s.hasPendingTaskMarkdownMigrationSource() {
			return nil, errors.New("Markdown 任务目录迁移或保存尚未完成，不能直接切换任务存放目录")
		}
		return nil, nil
	}
	if action.Kind != "migration.apply" {
		return nil, nil
	}
	if s.materialsUnit.state.pendingTaskRootMigration != nil {
		return nil, errors.New("任务目录恢复尚未完成，请先完成恢复再迁移任务目录")
	}
	if action.Payload["plan"] == nil {
		if s.hasPendingTaskMarkdownMigrationSource() {
			return nil, errors.New("Markdown 尚未保存，请先预览任务目录迁移")
		}
		return nil, nil
	}
	var preview workspace.TaskRootMigrationPreview
	if err := decodeWorkspaceValue(action.Payload["plan"], &preview); err != nil {
		return nil, err
	}
	if preview.Mode == workspace.TaskRootDirectSwitch && len(preview.PathMappings) == 0 {
		return nil, nil
	}
	if !preview.CanMigrate || len(preview.Blockers) != 0 || len(preview.PathMappings) == 0 || normalizeIdentityPath(action.Path) != normalizeIdentityPath(preview.TargetRoot) {
		return nil, errors.New("请重新预览并确认任务目录迁移范围")
	}
	// Detach nullable roots/slices from mutable action payloads.
	encoded, err := json.Marshal(preview)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(encoded, &preview); err != nil {
		return nil, err
	}
	plan := &markdownTaskRootMigrationPlan{home: s.snapshot.Home, preview: preview, autosave: s.materialsUnit.state.markdownAutosave, moves: map[string]string{}}
	if matched, err := plan.matchesConfigJSON(s.settingsUnit.state.configJSON, false); err != nil || !matched {
		return nil, errors.New("当前任务和资料根目录已变化，请重新预览迁移")
	}
	seenSources, seenTargets := map[string]bool{}, map[string]bool{}
	for _, mapping := range preview.PathMappings {
		if !filepath.IsAbs(mapping.SourceDirectory) || !filepath.IsAbs(mapping.TargetDirectory) || normalizeIdentityPath(filepath.Dir(mapping.SourceDirectory)) != normalizeIdentityPath(preview.SourceRoot) || normalizeIdentityPath(filepath.Dir(mapping.TargetDirectory)) != normalizeIdentityPath(preview.TargetRoot) {
			return nil, errors.New("任务目录迁移缺少有效的实际路径映射")
		}
		source, target := normalizeIdentityPath(mapping.SourceDirectory), normalizeIdentityPath(mapping.TargetDirectory)
		if seenSources[source] || seenTargets[target] {
			return nil, errors.New("任务目录迁移路径映射重复")
		}
		seenSources[source], seenTargets[target] = true, true
	}
	for _, item := range s.taskUnit.state.tasks {
		_, changed, err := workspace.RebaseTaskRootMigrationPath(item.path, preview.PathMappings)
		if err != nil {
			return nil, err
		}
		if changed {
			plan.tasks = append(plan.tasks, item)
		}
	}
	if s.readingSession != nil && !s.readingSession.closed && !s.readingSession.loaded {
		return nil, errors.New("阅读状态尚未加载，请稍后再迁移任务目录")
	}
	if err := s.flushMarkdownTaskRootMigrationInputs(plan); err != nil {
		return nil, err
	}
	s.observeReadingSession()
	s.materialsUnit.state.documentSaveMu.Lock()
	refs := make(map[string]documentSaveRef, len(s.materialsUnit.state.documentSaveRefs))
	for key, ref := range s.materialsUnit.state.documentSaveRefs {
		refs[key] = ref
	}
	s.materialsUnit.state.documentSaveMu.Unlock()
	destinations := map[string]bool{}
	keys := map[string]bool{}
	for key, ref := range refs {
		var owner *task
		for index := range plan.tasks {
			if plan.tasks[index].id == ref.taskID {
				owner = &plan.tasks[index]
				break
			}
		}
		if owner == nil || ref.kind == "instruction" && ref.scope != "" {
			continue
		}
		if plan.autosave != nil && plan.autosave.Status(key).Composing {
			return nil, editor.ErrComposing
		}
		after := ref
		if ref.kind == "instruction" {
			if _, within := pathRelativeToRoot(owner.path, ref.path); !within {
				return nil, errors.New("任务说明保存身份已变化，请重新打开文件再迁移")
			}
			path, changed, err := workspace.RebaseTaskRootMigrationPath(ref.path, preview.PathMappings)
			if err != nil {
				return nil, err
			}
			if !changed {
				continue
			}
			after.path = path
		} else {
			if normalizeIdentityPath(ref.taskPath) != normalizeIdentityPath(owner.path) || normalizeIdentityPath(ref.materialsRoot) != normalizeIdentityPath(owner.materialsPath) {
				return nil, errors.New("Markdown 保存身份已变化，请重新打开文件再迁移")
			}
			var err error
			after.taskPath, _, err = workspace.RebaseTaskRootMigrationPath(ref.taskPath, preview.PathMappings)
			if err != nil {
				return nil, err
			}
			after.materialsRoot, _, err = workspace.RebaseTaskRootMigrationPath(ref.materialsRoot, preview.PathMappings)
			if err != nil {
				return nil, err
			}
			after.path, _, err = workspace.RebaseTaskRootMigrationPath(ref.path, preview.PathMappings)
			if err != nil {
				return nil, err
			}
			after.relativePath = relativeDocumentPath(after.materialsRoot, after.path)
			if after.relativePath == "" {
				return nil, errors.New("Markdown 迁移后路径超出资料目录")
			}
		}
		newKey := s.documentAutosaveKeyForIdentity(ref.taskID, after.taskPath, after.materialsRoot, after.path)
		if ref.kind == "instruction" {
			newKey = s.instructionAutosaveKey("task", ref.taskID, "", after.path)
		}
		if newKey == "" || destinations[newKey] {
			return nil, errors.New("Markdown 迁移后保存身份冲突")
		}
		destinations[newKey] = true
		plan.entries = append(plan.entries, markdownTaskRootMigrationEntry{key, newKey, ref, after})
		plan.moves[key] = newKey
		keys[key], keys[newKey] = true, true
	}
	for _, entry := range plan.entries {
		if entry.newKey == entry.oldKey {
			continue
		}
		if _, isSource := plan.moves[entry.newKey]; isSource {
			continue
		}
		if _, exists := refs[entry.newKey]; exists {
			return nil, errors.New("迁移目标已有 Markdown 保存身份，请先处理目标草稿")
		}
		if plan.autosave != nil {
			if _, exists := plan.autosave.Content(entry.newKey); exists {
				return nil, errors.New("迁移目标已有 Markdown 草稿，请先处理目标草稿")
			}
		}
	}
	for key := range keys {
		plan.lockKeys = append(plan.lockKeys, key)
	}
	sort.Strings(plan.lockKeys)
	// Validate reading and UI remapping before any filesystem transfer.
	if _, err := s.buildMarkdownTaskRootMigrationUI(plan); err != nil {
		return nil, err
	}
	if _, err := s.buildMarkdownTaskRootReadingRelocation(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func (s *app) flushMarkdownTaskRootMigrationInputs(plan *markdownTaskRootMigrationPlan) error {
	for _, owner := range plan.tasks {
		if owner.id != s.taskUnit.currentTask().id {
			continue
		}
		if err := s.captureMarkdownEditorInput(s.materialsUnit.state.markdown); err != nil {
			return err
		}
		if notes := s.taskUnit.state.taskNotesUI; notes != nil && notes.activeKind == "task" {
			if err := s.captureMarkdownEditorInput(notes.activeMarkdown); err != nil {
				return err
			}
			if notes.activeComposing {
				return editor.ErrComposing
			}
			s.preserveActiveInstructionDraft(notes.activeKey)
		}
	}
	return nil
}

type markdownTaskRootMigrationLease struct {
	mu               sync.Mutex
	app              *app
	plan             *markdownTaskRootMigrationPlan
	engine           *editor.AutosaveLease
	closed           bool
	pendingPauseKeys []string
}

func (s *app) prepareMarkdownTaskRootMigration(ctx context.Context, plan *markdownTaskRootMigrationPlan) (*markdownTaskRootMigrationLease, error) {
	if plan == nil {
		return nil, nil
	}
	lease := &markdownTaskRootMigrationLease{app: s, plan: plan}
	if plan.autosave != nil {
		engine, err := plan.autosave.Quiesce(ctx, plan.lockKeys)
		if err != nil {
			return nil, err
		}
		lease.engine = engine
	}
	return lease, nil
}

func sameMarkdownMigrationRefOwner(left, right documentSaveRef) bool {
	return left.taskID == right.taskID && left.kind == right.kind && left.scope == right.scope && left.groupID == right.groupID && left.readOnly == right.readOnly && normalizeIdentityPath(left.path) == normalizeIdentityPath(right.path) && normalizeIdentityPath(left.taskPath) == normalizeIdentityPath(right.taskPath) && normalizeIdentityPath(left.materialsRoot) == normalizeIdentityPath(right.materialsRoot)
}

// Complete runs on the window thread before applySnapshot. The backend owns
// persistent draft relocation; this lease moves only engine and UI ownership.
func (l *markdownTaskRootMigrationLease) Complete(result application.ActionResult) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return errors.New("Markdown 任务迁移保存租约已释放")
	}
	fail := func(err error) error {
		l.closed = true
		if l.engine != nil {
			l.engine.FailClosed("任务目录已迁移，但 Markdown 保存身份更新失败；原文已保留，请重新打开文件确认")
		}
		return err
	}
	_, committed, err := l.plan.committedResult(result)
	if err != nil || !committed {
		if err == nil {
			err = errors.New("任务目录迁移尚未确认提交")
		}
		return fail(err)
	}
	s := l.app
	if s == nil || normalizeIdentityPath(s.snapshot.Home) != normalizeIdentityPath(l.plan.home) {
		return fail(errors.New("任务迁移期间桌面身份已变化"))
	}
	if matches, err := l.plan.matchesConfigJSON(s.settingsUnit.state.configJSON, false); err != nil || !matches {
		return fail(errors.New("任务迁移期间配置身份已变化"))
	}
	for _, captured := range l.plan.tasks {
		current := s.taskUnit.taskByID(captured.id)
		if !sameTaskLifecycleSelection(current, captured) {
			return fail(errors.New("任务迁移期间任务身份已变化"))
		}
	}
	updates := append([]markdownTaskRootMigrationEntry(nil), l.plan.entries...)
	s.materialsUnit.state.documentSaveMu.Lock()
	for index := range updates {
		current, exists := s.materialsUnit.state.documentSaveRefs[updates[index].oldKey]
		if !exists || !sameMarkdownMigrationRefOwner(current, updates[index].before) {
			s.materialsUnit.state.documentSaveMu.Unlock()
			return fail(errors.New("任务迁移期间 Markdown 保存身份已变化"))
		}
		// A save already running at Capture may have advanced its disk baseline.
		updates[index].after.hash, updates[index].after.overwrite = current.hash, current.overwrite
	}
	s.materialsUnit.state.documentSaveMu.Unlock()
	uiState, err := s.buildMarkdownTaskRootMigrationUI(l.plan)
	if err != nil {
		return fail(err)
	}
	reading, err := s.buildMarkdownTaskRootReadingRelocation(l.plan)
	if err != nil {
		return fail(err)
	}
	if l.engine != nil {
		if err := l.engine.Move(l.plan.moves); err != nil {
			return fail(err)
		}
	}
	s.materialsUnit.state.documentSaveMu.Lock()
	for _, entry := range updates {
		delete(s.materialsUnit.state.documentSaveRefs, entry.oldKey)
	}
	for _, entry := range updates {
		s.materialsUnit.state.documentSaveRefs[entry.newKey] = entry.after
	}
	s.materialsUnit.state.documentSaveMu.Unlock()
	s.applyMarkdownTaskRootMigrationUI(l.plan, uiState)
	if err := s.applyMarkdownTaskRootReadingRelocation(reading); err != nil {
		return fail(fmt.Errorf("任务目录已迁移，但阅读状态缓存保存失败：%w", err))
	}
	l.closed = true
	if l.engine != nil {
		l.engine.Release()
	}
	return nil
}

// No UI maps are touched here. Before-dispatch or a proven complete original
// state can release; shutdown with committed/unknown state fails closed.
func (l *markdownTaskRootMigrationLease) Abandon(committedOrUnknown bool) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.closed = true
	if l.engine == nil {
		return
	}
	if committedOrUnknown {
		l.engine.FailClosed("任务目录迁移状态无法确认；原文已保留，请重新打开文件确认")
	} else {
		l.engine.Release()
	}
}
