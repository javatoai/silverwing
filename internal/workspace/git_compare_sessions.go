package workspace

import (
	"container/list"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"silverwing-go/internal/domain"
)

const (
	CompareSessionCloseAction = "workspace.diff-session-close"
	comparePreviewCacheFiles  = 16
	comparePreviewCacheBytes  = 32 * 1024 * 1024
)

// WorkspaceGitDiffSessionManager retains immutable Git snapshots while a
// review page is open. It is process-local; callers must retire sessions when
// leaving the page and close the manager during application shutdown.
type WorkspaceGitDiffSessionManager struct {
	mu             sync.Mutex
	changed        chan struct{}
	nextOp         uint64
	closed         bool
	pendingCleanup int
	operations     map[uint64]*compareSessionOperation
	sessions       map[string]*compareSession
	closeStateFn   func(*compareState)
}

type compareSessionOperation struct {
	cancel  context.CancelFunc
	session *compareSession
}

type compareSession struct {
	id                 string
	ownerHash          string
	state              *compareState
	files              []CompareChange
	mu                 sync.Mutex
	cache              map[string]*comparePreviewCacheEntry
	cacheLRU           *list.List
	cacheSize          int
	refs               int
	active             map[uint64]context.CancelFunc
	retired            bool
	closed             bool
	readGate           chan struct{}
	foreground         int
	pinned             string
	prefetchCancel     context.CancelFunc
	prefetchGeneration uint64
}

type comparePreviewCacheEntry struct {
	key   string
	value WorkspaceGitFilePreview
	size  int
	node  *list.Element
}

// NewWorkspaceGitDiffSessionManager creates an empty, long-lived session
// registry. A zero-value manager is also usable through the application's
// lazy initialization path.
func NewWorkspaceGitDiffSessionManager() *WorkspaceGitDiffSessionManager {
	return &WorkspaceGitDiffSessionManager{
		changed:    make(chan struct{}),
		operations: make(map[uint64]*compareSessionOperation),
		sessions:   make(map[string]*compareSession),
	}
}

func (m *WorkspaceGitDiffSessionManager) ensureLocked() {
	if m.changed == nil {
		m.changed = make(chan struct{})
	}
	if m.operations == nil {
		m.operations = make(map[uint64]*compareSessionOperation)
	}
	if m.sessions == nil {
		m.sessions = make(map[string]*compareSession)
	}
}

func (m *WorkspaceGitDiffSessionManager) signalLocked() {
	close(m.changed)
	m.changed = make(chan struct{})
}

func (m *WorkspaceGitDiffSessionManager) begin(ctx context.Context) (context.Context, uint64, func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	m.ensureLocked()
	if m.closed {
		m.mu.Unlock()
		return nil, 0, nil, errors.New("Git 差异会话管理器已关闭")
	}
	m.nextOp++
	opID := m.nextOp
	opCtx, cancel := context.WithCancel(ctx)
	m.operations[opID] = &compareSessionOperation{cancel: cancel}
	m.signalLocked()
	m.mu.Unlock()

	var once sync.Once
	finish := func() {
		once.Do(func() { m.finishOperation(opID) })
	}
	return opCtx, opID, finish, nil
}

func (m *WorkspaceGitDiffSessionManager) finishOperation(opID uint64) {
	var closeState *compareState
	var operation *compareSessionOperation
	m.mu.Lock()
	operation = m.operations[opID]
	if operation != nil {
		if session := operation.session; session != nil {
			delete(session.active, opID)
			session.refs--
			if session.retired && session.refs == 0 && !session.closed {
				session.closed = true
				closeState = session.state
				m.pendingCleanup++
			}
		}
	}
	m.mu.Unlock()
	if operation != nil {
		operation.cancel()
	}
	if closeState != nil {
		m.closeState(closeState)
	}
	m.mu.Lock()
	if m.operations[opID] == operation {
		delete(m.operations, opID)
		if closeState != nil {
			m.pendingCleanup--
		}
		m.signalLocked()
	}
	m.mu.Unlock()
}

func (m *WorkspaceGitDiffSessionManager) register(ownerHash string, state *compareState, files []CompareChange) (string, error) {
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		state.close()
		return "", fmt.Errorf("创建 Git 差异会话标识失败：%w", err)
	}
	id := hex.EncodeToString(random[:])
	session := &compareSession{
		id: id, ownerHash: ownerHash, state: state,
		files: append([]CompareChange(nil), files...), cache: make(map[string]*comparePreviewCacheEntry),
		cacheLRU: list.New(), active: make(map[uint64]context.CancelFunc),
		readGate: make(chan struct{}, 1),
	}
	m.mu.Lock()
	m.ensureLocked()
	if m.closed {
		m.mu.Unlock()
		state.close()
		return "", errors.New("Git 差异会话管理器已关闭")
	}
	m.sessions[id] = session
	m.signalLocked()
	m.mu.Unlock()
	return id, nil
}

func (m *WorkspaceGitDiffSessionManager) acquire(ctx context.Context, id, ownerHash string) (context.Context, *compareSession, func(), error) {
	opCtx, opID, finish, err := m.begin(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	m.mu.Lock()
	m.ensureLocked()
	session := m.sessions[id]
	if m.closed || session == nil || session.retired || session.ownerHash != ownerHash {
		m.mu.Unlock()
		finish()
		return nil, nil, nil, errors.New("Git 差异会话已失效，请重新读取文件列表")
	}
	op := m.operations[opID]
	if op == nil {
		m.mu.Unlock()
		finish()
		return nil, nil, nil, errors.New("Git 差异会话读取已取消")
	}
	op.session = session
	session.refs++
	session.active[opID] = op.cancel
	m.signalLocked()
	m.mu.Unlock()
	return opCtx, session, finish, nil
}

// Retire prevents new previews and cancels in-flight reads. Temporary Git
// state is deleted after the last reader releases its reference.
func (m *WorkspaceGitDiffSessionManager) Retire(id string) {
	if m == nil || id == "" {
		return
	}
	var closeState *compareState
	m.mu.Lock()
	m.ensureLocked()
	session := m.sessions[id]
	if session != nil {
		delete(m.sessions, id)
		session.retired = true
		for _, cancel := range session.active {
			cancel()
		}
		if session.refs == 0 && !session.closed {
			session.closed = true
			closeState = session.state
			m.pendingCleanup++
		}
		m.signalLocked()
	}
	m.mu.Unlock()
	if closeState != nil {
		m.closeState(closeState)
		m.mu.Lock()
		m.pendingCleanup--
		m.signalLocked()
		m.mu.Unlock()
	}
}

// Close rejects new work, cancels active preparations and previews, retires
// every snapshot, then waits until temporary directories are released.
func (m *WorkspaceGitDiffSessionManager) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var closeStates []*compareState
	m.mu.Lock()
	m.ensureLocked()
	m.closed = true
	for _, operation := range m.operations {
		operation.cancel()
	}
	for id, session := range m.sessions {
		delete(m.sessions, id)
		session.retired = true
		for _, cancel := range session.active {
			cancel()
		}
		if session.refs == 0 && !session.closed {
			session.closed = true
			closeStates = append(closeStates, session.state)
			m.pendingCleanup++
		}
	}
	m.signalLocked()
	m.mu.Unlock()
	for _, state := range closeStates {
		m.closeState(state)
		m.mu.Lock()
		m.pendingCleanup--
		m.signalLocked()
		m.mu.Unlock()
	}

	for {
		m.mu.Lock()
		if len(m.operations) == 0 && m.pendingCleanup == 0 {
			m.mu.Unlock()
			return nil
		}
		changed := m.changed
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (m *WorkspaceGitDiffSessionManager) closeState(state *compareState) {
	if state == nil {
		return
	}
	if m.closeStateFn != nil {
		m.closeStateFn(state)
		return
	}
	state.close()
}

// FilesSessionContext prepares one immutable tree pair and changed-file list.
// Subsequent file previews reuse exactly this state.
func (s *WorkspaceGitDiffService) FilesSessionContext(ctx context.Context, manager *WorkspaceGitDiffSessionManager, owner, configIdentity string, ws domain.ServiceWorkspace, request CompareRequest, mainBranch WorkspaceMainBranch) (CompareFilesResult, error) {
	if manager == nil {
		return CompareFilesResult{}, errors.New("Git 差异会话管理器不可用")
	}
	opCtx, _, finish, err := manager.begin(ctx)
	if err != nil {
		return CompareFilesResult{}, err
	}
	defer finish()
	started := time.Now()
	ownerHash, err := gitCompareSessionOwnerHash(owner, configIdentity, ws, request, mainBranch)
	if err != nil {
		return CompareFilesResult{}, err
	}
	var state *compareState
	var result CompareFilesResult
	err = s.withWorkspaceIdentity(opCtx, ws, func(root, commonDir string) error {
		prepareStarted := time.Now()
		var prepareErr error
		state, prepareErr = s.prepare(opCtx, root, request, mainBranch)
		observeGitComparePhase(opCtx, "prepare", prepareStarted, map[string]any{"mode": string(request.Mode)})
		if prepareErr != nil {
			return prepareErr
		}
		state.commonDir = commonDir
		state.gitDir, prepareErr = resolveWorkspaceGitDir(root)
		if prepareErr != nil {
			return prepareErr
		}
		state.branch = ws.Branch
		listStarted := time.Now()
		files, filesErr := state.files(opCtx)
		observeGitComparePhase(opCtx, "file_list", listStarted, map[string]any{"files": len(files)})
		if filesErr != nil {
			return filesErr
		}
		if err := opCtx.Err(); err != nil {
			return err
		}
		result = CompareFilesResult{
			Files: files, BaselineRef: state.baselineRef, BaseCommit: state.baseCommit,
			TargetCommit: state.targetCommit, Notice: state.notice,
		}
		return nil
	})
	if err != nil {
		if state != nil {
			state.close()
		}
		return CompareFilesResult{}, err
	}
	result.SessionID, err = manager.register(ownerHash, state, result.Files)
	if err != nil {
		return CompareFilesResult{}, err
	}
	if err := opCtx.Err(); err != nil {
		manager.Retire(result.SessionID)
		return CompareFilesResult{}, err
	}
	observeGitComparePhase(opCtx, "prepare_and_list", started, map[string]any{"mode": string(request.Mode), "files": len(result.Files)})
	return result, nil
}

// PreviewSessionContext verifies the session's task/config/workspace/scope and
// snapshot identity before reading one file. It never prepares or enumerates a
// new diff.
func (s *WorkspaceGitDiffService) PreviewSessionContext(ctx context.Context, manager *WorkspaceGitDiffSessionManager, sessionID, owner, configIdentity string, ws domain.ServiceWorkspace, request CompareRequest, change CompareChange, mainBranch WorkspaceMainBranch) (WorkspaceGitFilePreview, error) {
	if manager == nil {
		return WorkspaceGitFilePreview{}, errors.New("Git 差异会话管理器不可用")
	}
	ownerHash, err := gitCompareSessionOwnerHash(owner, configIdentity, ws, request, mainBranch)
	if err != nil {
		return WorkspaceGitFilePreview{}, err
	}
	ctx, session, release, err := manager.acquire(ctx, sessionID, ownerHash)
	if err != nil {
		return WorkspaceGitFilePreview{}, err
	}
	defer release()
	if request.ExpectedBaseCommit != session.state.baseCommit || request.ExpectedTargetCommit == "" || request.ExpectedTargetCommit != session.state.targetCommit {
		return WorkspaceGitFilePreview{}, errors.New("Git 差异快照已变化，请重新读取文件列表后再预览")
	}
	canonical, found := matchCompareChange(session.files, change)
	if !found {
		return WorkspaceGitFilePreview{}, errors.New("所选文件已不属于当前差异，请重新读取文件列表")
	}
	started := time.Now()
	endRead, err := session.beginForeground(ctx, canonical)
	if err != nil {
		return WorkspaceGitFilePreview{}, err
	}
	defer endRead()
	if _, hit := session.cached(canonical); hit {
		var preview WorkspaceGitFilePreview
		cacheHit := false
		err := s.withCachedPreparedWorkspace(ctx, ws, session.state, func(string) error {
			preview, cacheHit = session.cached(canonical)
			return nil
		})
		if err != nil {
			return WorkspaceGitFilePreview{}, err
		}
		if cacheHit {
			observeGitComparePhase(ctx, "preview_total", started, map[string]any{"mode": string(request.Mode), "cache_hit": true})
			return preview, nil
		}
	}
	var preview WorkspaceGitFilePreview
	err = s.withPreparedWorkspace(ctx, ws, session.state, func(root string) error {
		preview, _, err = session.preview(ctx, canonical)
		observeGitComparePhase(ctx, "preview_total", started, map[string]any{"mode": string(request.Mode), "cache_hit": false})
		return err
	})
	if err != nil {
		return WorkspaceGitFilePreview{}, err
	}
	return preview, nil
}

func (s *WorkspaceGitDiffService) withPreparedWorkspace(ctx context.Context, ws domain.ServiceWorkspace, state *compareState, operation func(string) error) error {
	return s.Locks.WithRepository(state.commonDir, func() error {
		root, err := validatePreparedWorkspacePaths(ctx, ws, state)
		if err != nil {
			return err
		}
		identity, err := s.run(ctx, root, nil, 2*1024*1024, true,
			"rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir", "--abbrev-ref", "HEAD")
		if err != nil {
			return fmt.Errorf("验证 Git 差异会话仓库身份失败：%w", err)
		}
		lines := strings.Split(strings.TrimSpace(identity.Stdout), "\n")
		if len(lines) != 3 || !samePath(strings.TrimSpace(lines[0]), state.root) ||
			!samePath(strings.TrimSpace(lines[1]), state.commonDir) || strings.TrimSpace(lines[2]) != ws.Branch {
			return errors.New("Git 差异会话对应的仓库或分支身份已变化，请重新读取文件列表")
		}
		if ws.Strategy == domain.WorkspaceIndependentClone {
			if ws.OriginURL == nil {
				return errors.New("Git 差异会话缺少独立克隆 origin 身份，请重新读取文件列表")
			}
			origin, originErr := s.run(ctx, root, nil, 2*1024*1024, true, "remote", "get-url", "origin")
			if originErr != nil || strings.TrimSpace(origin.Stdout) != strings.TrimSpace(*ws.OriginURL) {
				return errors.New("Git 差异会话对应的独立克隆 origin 已变化，请重新读取文件列表")
			}
		}
		return operation(root)
	})
}

func (s *WorkspaceGitDiffService) withCachedPreparedWorkspace(ctx context.Context, ws domain.ServiceWorkspace, state *compareState, operation func(string) error) error {
	return s.Locks.WithRepository(state.commonDir, func() error {
		root, err := validatePreparedWorkspacePaths(ctx, ws, state)
		if err != nil {
			return err
		}
		return operation(root)
	})
}

func validatePreparedWorkspacePaths(ctx context.Context, ws domain.ServiceWorkspace, state *compareState) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if ws.Health != domain.WorkspaceReady && ws.Health != domain.WorkspaceReadyWithWarnings {
		return "", errors.New("工作区尚未就绪，不能读取 Git 差异")
	}
	root, err := filepath.Abs(filepath.Clean(ws.WorktreePath))
	if err != nil {
		return "", err
	}
	if !samePath(root, state.root) {
		return "", errors.New("Git 差异会话对应的仓库路径已变化，请重新读取文件列表")
	}
	if info, statErr := os.Stat(root); statErr != nil || !info.IsDir() {
		return "", errors.New("Git 差异会话对应的工作区已不可用，请重新读取文件列表")
	}
	if state.commonDir == "" || state.gitDir == "" {
		return "", errors.New("Git 差异会话缺少仓库锁定信息，请重新读取文件列表")
	}
	if info, statErr := os.Stat(state.commonDir); statErr != nil || !info.IsDir() {
		return "", errors.New("Git 差异会话对应的仓库已不可用，请重新读取文件列表")
	}
	actualGitDir, err := resolveWorkspaceGitDir(root)
	if err != nil || !samePath(actualGitDir, state.gitDir) {
		return "", errors.New("Git 差异会话对应的仓库身份已变化，请重新读取文件列表")
	}
	head, err := os.ReadFile(filepath.Join(actualGitDir, "HEAD"))
	if err != nil {
		return "", errors.New("Git 差异会话对应的分支信息已不可用，请重新读取文件列表")
	}
	branch := ""
	headValue := strings.TrimSpace(string(head))
	if strings.HasPrefix(headValue, "ref: refs/heads/") {
		branch = strings.TrimPrefix(headValue, "ref: refs/heads/")
	}
	if branch != state.branch || branch != ws.Branch {
		return "", errors.New("Git 差异会话对应的分支已变化，请重新读取文件列表")
	}
	return root, nil
}

func resolveWorkspaceGitDir(root string) (string, error) {
	marker := filepath.Join(root, ".git")
	info, err := os.Stat(marker)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return filepath.Abs(marker)
	}
	content, err := os.ReadFile(marker)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(strings.SplitN(string(content), "\n", 2)[0])
	gitDir, ok := strings.CutPrefix(line, "gitdir: ")
	if !ok || strings.TrimSpace(gitDir) == "" {
		return "", errors.New("工作区 .git 文件格式无效")
	}
	gitDir = strings.TrimSpace(gitDir)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(root, gitDir)
	}
	gitDir, err = filepath.Abs(filepath.Clean(gitDir))
	if err != nil {
		return "", err
	}
	info, err = os.Stat(gitDir)
	if err != nil || !info.IsDir() {
		return "", errors.New("工作区 .git 指向的仓库目录不可用")
	}
	return gitDir, nil
}

func (session *compareSession) preview(ctx context.Context, change CompareChange) (WorkspaceGitFilePreview, bool, error) {
	if value, hit := session.cached(change); hit {
		return value, true, nil
	}
	preview, cacheable, err := session.state.previewWithCacheability(ctx, change)
	if err != nil || !cacheable || ctx.Err() != nil {
		return preview, false, err
	}
	session.cachePreview(change, preview)
	return preview, false, nil
}

func (session *compareSession) cached(change CompareChange) (WorkspaceGitFilePreview, bool) {
	key := change.Path + "\x00" + change.OldPath + "\x00" + change.Kind
	session.mu.Lock()
	if entry := session.cache[key]; entry != nil {
		session.cacheLRU.MoveToFront(entry.node)
		value := entry.value
		session.mu.Unlock()
		return value, true
	}
	session.mu.Unlock()
	return WorkspaceGitFilePreview{}, false
}

func (session *compareSession) cachePreview(change CompareChange, preview WorkspaceGitFilePreview) {
	key := change.Path + "\x00" + change.OldPath + "\x00" + change.Kind
	size := workspaceGitFilePreviewCacheSize(preview)
	if size <= 0 || size > comparePreviewCacheBytes {
		return
	}
	session.mu.Lock()
	if existing := session.cache[key]; existing != nil {
		session.cacheLRU.MoveToFront(existing.node)
		session.mu.Unlock()
		return
	}
	entry := &comparePreviewCacheEntry{key: key, value: preview, size: size}
	entry.node = session.cacheLRU.PushFront(entry)
	session.cache[key] = entry
	session.cacheSize += size
	for session.cacheLRU.Len() > comparePreviewCacheFiles || session.cacheSize > comparePreviewCacheBytes {
		oldest := session.cacheLRU.Back()
		if oldest == nil {
			break
		}
		removed := oldest.Value.(*comparePreviewCacheEntry)
		if removed.key == session.pinned && session.cacheLRU.Len() > 1 {
			oldest = oldest.Prev()
			removed = oldest.Value.(*comparePreviewCacheEntry)
		}
		delete(session.cache, removed.key)
		session.cacheSize -= removed.size
		session.cacheLRU.Remove(oldest)
	}
	session.mu.Unlock()
}

func gitCompareSessionOwnerHash(owner, configIdentity string, ws domain.ServiceWorkspace, request CompareRequest, mainBranch WorkspaceMainBranch) (string, error) {
	request.ExpectedBaseCommit = ""
	request.ExpectedTargetCommit = ""
	identity := struct {
		Owner          string
		ConfigIdentity string
		Workspace      struct {
			RepositoryID   string
			RepositoryPath string
			WorktreePath   string
			Branch         string
			GroupServiceID string
			ModuleID       string
			Strategy       domain.WorkspaceStrategy
			OriginURL      string
		}
		Request    CompareRequest
		MainBranch WorkspaceMainBranch
	}{Owner: owner, ConfigIdentity: configIdentity, Request: request, MainBranch: mainBranch}
	identity.Workspace.RepositoryID = ws.RepositoryID
	identity.Workspace.RepositoryPath = ws.RepositoryPath
	identity.Workspace.WorktreePath = ws.WorktreePath
	identity.Workspace.Branch = ws.Branch
	identity.Workspace.GroupServiceID = ws.GroupServiceID
	identity.Workspace.ModuleID = ws.ModuleID
	identity.Workspace.Strategy = ws.Strategy
	if ws.OriginURL != nil {
		identity.Workspace.OriginURL = *ws.OriginURL
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func observeGitComparePhase(ctx context.Context, phase string, started time.Time, attributes map[string]any) {
	logger := slog.Default()
	if !logger.Enabled(ctx, slog.LevelDebug) {
		return
	}
	attrs := []any{"phase", phase, "duration_ms", float64(time.Since(started).Microseconds()) / 1000}
	for key, value := range attributes {
		attrs = append(attrs, key, value)
	}
	logger.Log(ctx, slog.LevelDebug, "workspace git review", attrs...)
}

func workspaceGitFilePreviewCacheSize(preview WorkspaceGitFilePreview) int {
	size := int(unsafe.Sizeof(preview)) + len(preview.Language) + len(preview.Notice) + len(preview.UnavailableReason)
	if preview.Content != nil {
		size += int(unsafe.Sizeof(*preview.Content)) + len(preview.Content.Text)
	}
	if comparison := preview.Comparison; comparison != nil {
		size += int(unsafe.Sizeof(*comparison)) + cap(comparison.Rows)*int(unsafe.Sizeof(WorkspaceFileComparisonRow{}))
		if comparison.OldContent != nil {
			size += int(unsafe.Sizeof(*comparison.OldContent)) + len(comparison.OldContent.Text)
		}
		if comparison.NewContent != nil {
			size += int(unsafe.Sizeof(*comparison.NewContent)) + len(comparison.NewContent.Text)
		}
		for _, row := range comparison.Rows {
			if row.OldLine != nil {
				size += len(row.OldLine.Text) + int(unsafe.Sizeof(*row.OldLine))
			}
			if row.NewLine != nil {
				size += len(row.NewLine.Text) + int(unsafe.Sizeof(*row.NewLine))
			}
		}
	}
	return size
}
