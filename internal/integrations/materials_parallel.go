package integrations

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

// The coordinator owns manifest revisions and progress. Workers own only
// independent remote IO, immutable snapshots, and immediate durable receipts.
type materialsResourceOperations struct {
	worker     func(context.Context) domain.FeishuMaterialsGateway
	checkpoint func(remoteMaterialsState, domain.MaterialsSyncManifest) (remoteMaterialsState, error)
	journal    *materialsReceiptJournal
	verify     func(localMaterial) error
	markdown   func(localMaterial, domain.MaterialsSyncBinding) (string, error)
	verifyScan func() error
}

var materialsRetainedScopeLocks = struct {
	sync.Mutex
	owners map[string][]func()
}{owners: map[string][]func(){}}

// Joining workers does not prove that a CLI with CleanupError has stopped its
// descendants. Keep the OS lock owner reachable until process exit, as Source
// does. Ordinary failures and confirmed cancellation release normally.
func finishMaterialsScopeLock(key string, release func(), err error) {
	if !process.IsCleanupFailure(err) {
		release()
		return
	}
	materialsRetainedScopeLocks.Lock()
	materialsRetainedScopeLocks.owners[key] = append(materialsRetainedScopeLocks.owners[key], release)
	materialsRetainedScopeLocks.Unlock()
}

func (g *feishuGateway) materialsOperations(plan domain.MaterialsSyncPlan, scan localMaterialScan, state remoteMaterialsState) (materialsResourceOperations, error) {
	journal, err := g.service.openMaterialsReceiptJournal(plan, state)
	if err != nil {
		return materialsResourceOperations{}, err
	}
	// Keep raw hashes for targeted checks; Markdown's plan hash also includes its
	// dependencies. Verify the complete scan once before recording this baseline.
	current, err := scanMaterialsForRequest(scan.root, plan.Request.SelectedPaths)
	if err != nil || current.fingerprint != scan.fingerprint {
		return materialsResourceOperations{}, errors.New("同步前本地资料发生变化，请重新预览")
	}
	raw := map[string]string{}
	for path, file := range scan.files {
		if file.kind != domain.MaterialsSyncDirectory {
			hash, err := hashLocalFile(filepath.Join(scan.root, filepath.FromSlash(path)))
			if err != nil {
				return materialsResourceOperations{}, err
			}
			raw[path] = hash
		}
	}
	verify := func(file localMaterial) error {
		if file.kind == domain.MaterialsSyncDirectory {
			return nil
		}
		check := func(path string) error {
			absolute := filepath.Join(scan.root, filepath.FromSlash(path))
			real, err := filepath.EvalSymlinks(absolute)
			if err != nil || filepath.Clean(real) != filepath.Clean(absolute) {
				return fmt.Errorf("资料路径包含符号链接或重解析点：%s", path)
			}
			hash, err := hashLocalFile(absolute)
			if err != nil || hash != raw[path] {
				return fmt.Errorf("同步期间本地资料发生变化：%s", path)
			}
			return nil
		}
		if err := check(file.path); err != nil {
			return err
		}
		if file.kind == domain.MaterialsSyncMarkdown {
			content, err := readMarkdownLocal(scan.root, file.path)
			if err != nil {
				return err
			}
			dependencyFiles := scan.allFiles
			if dependencyFiles == nil {
				dependencyFiles = scan.files
			}
			dependencies, _, err := markdownDependencies(scan.root, file.path, content, dependencyFiles)
			if err != nil {
				return err
			}
			for _, dependency := range dependencies {
				path := dependency[:strings.LastIndexByte(dependency, ':')]
				dependencyFile, inScope := scan.files[path]
				if inScope && dependencyFile.kind != domain.MaterialsSyncDirectory {
					if err := check(path); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	verifyScan := func() error {
		fresh, err := scanMaterialsForRequest(scan.root, plan.Request.SelectedPaths)
		if err != nil {
			return err
		}
		if fresh.fingerprint != scan.fingerprint {
			return errors.New("同步时本地资料发生变化，已停止删除")
		}
		return nil
	}
	if err := verifyScan(); err != nil {
		return materialsResourceOperations{}, err
	}
	return materialsResourceOperations{
		worker: func(ctx context.Context) domain.FeishuMaterialsGateway { copy := *g; copy.ctx = ctx; return &copy },
		checkpoint: func(previous remoteMaterialsState, value domain.MaterialsSyncManifest) (remoteMaterialsState, error) {
			return g.saveManifest(previous, plan.Target, plan.Request.WriteRoot, value)
		},
		journal: journal, verify: verify, verifyScan: verifyScan,
		markdown: func(file localMaterial, binding domain.MaterialsSyncBinding) (string, error) {
			if err := verify(file); err != nil {
				return "", err
			}
			content, err := readMarkdownLocal(scan.root, file.path)
			if err != nil {
				return "", err
			}
			return rewriteRemoteMarkdown(scan, file.path, content, binding, plan.Request.TargetURL)
		},
	}, nil
}

func cloneMaterialsManifest(value domain.MaterialsSyncManifest) domain.MaterialsSyncManifest {
	copy := value
	copy.Bindings = make(map[string]domain.MaterialsSyncBinding, len(value.Bindings))
	for id, binding := range value.Bindings {
		copy.Bindings[id] = cloneMaterialsBinding(binding)
	}
	return copy
}

func cloneMaterialsBinding(binding domain.MaterialsSyncBinding) domain.MaterialsSyncBinding {
	copy := binding
	copy.Entries = make(map[string]domain.MaterialsSyncEntry, len(binding.Entries))
	for path, entry := range binding.Entries {
		copy.Entries[path] = entry
	}
	copy.Pending = append([]domain.MaterialsSyncIntent(nil), binding.Pending...)
	return copy
}

// Each wave has its own read-only, single-flight cache. Siblings share parent
// preflights, while the next wave always observes fresh remote state.
type materialsSnapshotRead[T any] struct {
	done  chan struct{}
	value T
	err   error
}
type materialsSnapshots struct {
	mu       sync.Mutex
	gateway  domain.FeishuMaterialsGateway
	nodes    map[string]*materialsSnapshotRead[*domain.FeishuWikiNode]
	versions map[string]*materialsSnapshotRead[domain.FeishuRemoteVersion]
	children map[string]*materialsSnapshotRead[[]domain.FeishuWikiNode]
}

func newMaterialsSnapshots(gateway domain.FeishuMaterialsGateway) *materialsSnapshots {
	return &materialsSnapshots{gateway: gateway, nodes: map[string]*materialsSnapshotRead[*domain.FeishuWikiNode]{},
		versions: map[string]*materialsSnapshotRead[domain.FeishuRemoteVersion]{}, children: map[string]*materialsSnapshotRead[[]domain.FeishuWikiNode]{}}
}

func materialsSnapshot[T any](mu *sync.Mutex, reads map[string]*materialsSnapshotRead[T], key string, load func() (T, error)) (T, error) {
	mu.Lock()
	read, exists := reads[key]
	if !exists {
		read = &materialsSnapshotRead[T]{done: make(chan struct{})}
		reads[key] = read
	}
	mu.Unlock()
	if !exists {
		read.value, read.err = load()
		close(read.done)
	} else {
		<-read.done
	}
	return read.value, read.err
}

func (s *materialsSnapshots) node(token string) (*domain.FeishuWikiNode, error) {
	return materialsSnapshot(&s.mu, s.nodes, token, func() (*domain.FeishuWikiNode, error) { return s.gateway.GetNode(token) })
}

func (s *materialsSnapshots) version(node domain.FeishuWikiNode, comments bool) (domain.FeishuRemoteVersion, error) {
	key := node.NodeToken + ":" + strconv.FormatBool(comments)
	return materialsSnapshot(&s.mu, s.versions, key, func() (domain.FeishuRemoteVersion, error) { return s.gateway.Version(node, comments) })
}

func (s *materialsSnapshots) list(parent domain.FeishuWikiNode) ([]domain.FeishuWikiNode, error) {
	return materialsSnapshot(&s.mu, s.children, parent.NodeToken, func() ([]domain.FeishuWikiNode, error) { return s.gateway.ListChildren(parent) })
}

func materialsSameIdentity(node domain.FeishuWikiNode, entry domain.MaterialsSyncEntry, space string) bool {
	return node.SpaceID == space && node.NodeToken == entry.NodeToken && node.ObjectToken == entry.ObjectToken &&
		node.ObjectType == entry.ObjectType && node.ParentNodeToken == entry.ParentNodeToken
}

func (s *materialsSnapshots) owned(target domain.FeishuWikiNode, binding domain.MaterialsSyncBinding, entry domain.MaterialsSyncEntry) (domain.FeishuWikiNode, error) {
	ancestry := []domain.MaterialsSyncEntry{}
	seen := map[string]bool{}
	for {
		if entry.NodeToken == "" || seen[entry.NodeToken] {
			return domain.FeishuWikiNode{}, errors.New("同步清单目录关系缺失或存在循环")
		}
		seen[entry.NodeToken] = true
		ancestry = append(ancestry, entry)
		if entry.Path == materialsRequirementRoot {
			break
		}
		parent, exists := binding.Entries[materialsParentPath(entry.Path)]
		if !exists {
			return domain.FeishuWikiNode{}, errors.New("同步清单缺少父目录")
		}
		entry = parent
	}
	expectedParent := target.NodeToken
	var result domain.FeishuWikiNode
	for index := len(ancestry) - 1; index >= 0; index-- {
		entry := ancestry[index]
		node, err := s.node(entry.NodeToken)
		if err != nil {
			return result, err
		}
		if node == nil || !materialsSameIdentity(*node, entry, target.SpaceID) || node.NodeToken == target.NodeToken || node.ParentNodeToken != expectedParent {
			return result, errors.New("远端资料已移动或归属不明确，已保留")
		}
		if entry.Kind == domain.MaterialsSyncDirectory {
			version, err := s.version(*node, materialsIncludeComments(entry.Path))
			if err != nil {
				return result, err
			}
			expected := entry.RemoteVersion
			if !materialsIncludeComments(entry.Path) {
				expected.CommentsFingerprint = ""
			}
			if node.Title != entry.Title || version != expected {
				return result, errors.New("需求根或资料目录在预览后被修改，已停止写入")
			}
		}
		expectedParent, result = node.NodeToken, *node
	}
	return result, nil
}

type materialsWaveJob struct {
	path string
	run  func(context.Context, domain.FeishuMaterialsGateway, *materialsSnapshots) (any, error)
}
type materialsWaveEvent struct {
	index   int
	started bool
	value   any
	err     error
}

type materialsWriteCoordinator struct {
	ctx       context.Context
	service   *Service
	config    domain.AppConfig
	plan      domain.MaterialsSyncPlan
	approval  domain.MaterialsSyncApproval
	scan      localMaterialScan
	state     remoteMaterialsState
	result    *domain.MaterialsSyncResult
	notify    func()
	ops       materialsResourceOperations
	failed    map[string]bool
	completed map[string]bool
	created   map[string]bool
	failures  []string
}

func materialsFatalWrite(err error) bool {
	return isMaterialsManifestStateError(err) || process.IsCleanupFailure(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// Submit the entire level before collecting any result. No pool, permit or
// batch limit is imposed. All result/progress writes stay on this goroutine.
func (c *materialsWriteCoordinator) wave(phase domain.MaterialsSyncPhase, jobs []materialsWaveJob) ([]materialsWaveEvent, error) {
	if err := c.ctx.Err(); err != nil {
		return nil, err
	}
	if err := c.ops.journal.healthy(); err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithCancel(c.ctx)
	defer cancel()
	events := make(chan materialsWaveEvent, len(jobs)*2)
	snapshots := newMaterialsSnapshots(c.ops.worker(ctx))
	var workers sync.WaitGroup
	for index, job := range jobs {
		workers.Add(1)
		go func(index int, job materialsWaveJob) {
			defer workers.Done()
			events <- materialsWaveEvent{index: index, started: true}
			var value any
			err := ctx.Err()
			if err == nil {
				err = c.ops.journal.healthy()
			}
			if err == nil {
				value, err = job.run(ctx, c.ops.worker(ctx), snapshots)
			}
			if materialsFatalWrite(err) {
				cancel()
			}
			events <- materialsWaveEvent{index: index, value: value, err: err}
		}(index, job)
	}
	active := map[string]bool{}
	results := make([]materialsWaveEvent, len(jobs))
	var fatal error
	finished := 0
	for finished < len(jobs) {
		event := <-events
		path := jobs[event.index].path
		if event.started {
			active[path] = true
		} else {
			delete(active, path)
			results[event.index] = event
			finished++
			if event.err != nil {
				if materialsFatalWrite(event.err) {
					if fatal == nil || process.IsCleanupFailure(event.err) || isMaterialsManifestStateError(event.err) && !process.IsCleanupFailure(fatal) {
						fatal = event.err
					}
					cancel()
				} else {
					c.failure(path, phase, event.err)
				}
			}
		}
		c.result.Progress.ActivePaths = sortedKeys(active)
		c.result.Progress.CurrentPath = path
		c.result.Progress.Phase = phase
		c.result.Progress.Sequence++
		c.notify()
	}
	// Returning a canceled/fatal wave cannot release the task lock while a worker
	// or its CLI cleanup is still executing.
	workers.Wait()
	c.result.Progress.ActivePaths = []string{}
	if fatal == nil {
		fatal = c.ctx.Err()
	}
	return results, fatal
}

func (c *materialsWriteCoordinator) failure(path string, phase domain.MaterialsSyncPhase, err error) {
	if c.failed[path] {
		return
	}
	c.failed[path] = true
	message := c.service.safeDiagnostic(c.config, err.Error())
	c.result.Progress.Failed++
	c.result.Messages = append(c.result.Messages, path+"："+message)
	c.failures = append(c.failures, path+"："+message)
	c.result.Progress.CurrentPath, c.result.Progress.Phase = path, phase
}

func (c *materialsWriteCoordinator) skipped(file localMaterial, reason string, complete bool) {
	c.result.Progress.Skipped++
	if complete && file.kind != domain.MaterialsSyncDirectory && !c.completed[file.path] {
		c.result.Progress.CompletedFiles++
		c.completed[file.path] = true
	}
	c.result.Progress.CurrentPath = file.path
	c.result.Progress.Sequence++
	c.result.Messages = append(c.result.Messages, file.path+"："+reason)
	c.notify()
}

func (c *materialsWriteCoordinator) markCompleted(file localMaterial) {
	if c.completed[file.path] {
		return
	}
	entry, exists := c.state.value.Bindings[c.plan.SourceID].Entries[file.path]
	if !exists || entry.LocalHash != file.hash {
		return
	}
	c.completed[file.path] = true
	if file.kind == domain.MaterialsSyncDirectory {
		c.result.Progress.Directories++
	} else {
		c.result.Progress.CompletedFiles++
		if c.created[file.path] {
			c.result.Progress.Added++
		} else {
			c.result.Progress.Updated++
		}
	}
	c.result.Progress.CurrentPath = file.path
	c.result.Progress.Sequence++
	c.notify()
}

func (c *materialsWriteCoordinator) checkpoint(binding domain.MaterialsSyncBinding) error {
	if err := c.ctx.Err(); err != nil {
		return err
	}
	if err := c.ops.journal.healthy(); err != nil {
		return err
	}
	value := cloneMaterialsManifest(c.state.value)
	value.Bindings[c.plan.SourceID] = cloneMaterialsBinding(binding)
	value.Generation = c.state.value.Generation + 1
	c.result.Progress.Phase = domain.MaterialsSyncSaveRecords
	c.result.Progress.Sequence++
	c.notify()
	next, err := c.ops.checkpoint(c.state, value)
	if err != nil {
		return materialsManifestStateFailure(err)
	}
	c.state = next
	return c.ops.journal.prune(c.state.value.Bindings[c.plan.SourceID])
}

func materialsPutIntent(binding *domain.MaterialsSyncBinding, intent domain.MaterialsSyncIntent, allowOverwrite bool) error {
	for index, old := range binding.Pending {
		if old.Path != intent.Path {
			continue
		}
		if old.OperationID == intent.OperationID && materialsJournalIntentMatches(old, intent) {
			binding.Pending[index] = intent
			return nil
		}
		baseline := old.BaseVersion != nil && intent.BaseVersion != nil && *old.BaseVersion == *intent.BaseVersion
		identity := old.Kind == intent.Kind && old.NodeToken != nil && intent.NodeToken != nil && *old.NodeToken == *intent.NodeToken &&
			old.ParentNodeToken == intent.ParentNodeToken && old.Title == intent.Title
		if old.Phase == "update" && intent.Phase == "update" && identity && (baseline || allowOverwrite) {
			binding.Pending[index] = intent
			return nil
		}
		return fmt.Errorf("已有未确认操作不能安全替换：%s", intent.Path)
	}
	binding.Pending = append(binding.Pending, intent)
	return nil
}

type materialsCreateWork struct {
	file     localMaterial
	parent   domain.FeishuWikiNode
	intent   domain.MaterialsSyncIntent
	markdown *string
	resuming bool
}
type materialsCreated struct {
	work    materialsCreateWork
	receipt materialsOperationReceipt
}
type materialsCreatedBaseline struct {
	created  materialsCreated
	version  domain.FeishuRemoteVersion
	bodyHash *string
}
type materialsEntryResult struct {
	file   localMaterial
	entry  domain.MaterialsSyncEntry
	intent domain.MaterialsSyncIntent
}

func (c *materialsWriteCoordinator) prepareCreate(file localMaterial, binding domain.MaterialsSyncBinding, snapshots *materialsSnapshots, completeWave bool) (materialsCreateWork, error) {
	parentEntry, exists := binding.Entries[materialsParentPath(file.path)]
	if !exists {
		return materialsCreateWork{}, errors.New("父目录未成功登记，已跳过该分支")
	}
	parent := nodeFromEntry(parentEntry, c.plan.Target.SpaceID)
	title := filepath.Base(filepath.FromSlash(file.path))
	if file.path == materialsFilesRoot {
		title = c.plan.Request.Subdirectory
	}
	var old *domain.MaterialsSyncIntent
	for _, intent := range binding.Pending {
		if intent.Path == file.path {
			copy := intent
			old = &copy
			break
		}
	}
	linksReady := true
	for _, linked := range file.linkedPaths {
		if _, exists := binding.Entries[linked]; !exists {
			linksReady = false
		}
	}
	var markdown *string
	if file.kind == domain.MaterialsSyncMarkdown && linksReady && (old == nil || old.ExpectedContentHash != nil) {
		for _, linked := range file.linkedPaths {
			if _, err := snapshots.owned(c.plan.Target, binding, binding.Entries[linked]); err != nil {
				return materialsCreateWork{}, err
			}
		}
		content, err := c.ops.markdown(file, binding)
		if err != nil {
			return materialsCreateWork{}, err
		}
		markdown = &content
	}
	full := false
	if creator, ok := snapshots.gateway.(domain.FeishuCompleteDocumentCreator); ok && completeWave && markdown != nil {
		full = creator.SupportsCompleteDocumentCreation(*markdown)
	}
	placeholder := file.kind == domain.MaterialsSyncMarkdown && !full
	if old != nil {
		placeholder = old.Placeholder
	}
	intent, err := createIntentFor(binding, file.path, file.kind, parent.NodeToken, title, file.hash, placeholder)
	if err != nil {
		return materialsCreateWork{}, err
	}
	if markdown != nil {
		hash := materialsContentHash(*markdown)
		if old != nil && (intent.ExpectedContentHash == nil || *intent.ExpectedContentHash != hash) {
			return materialsCreateWork{}, errors.New("未完成文档正文或引用已改变")
		}
		intent.ExpectedContentHash = &hash
	}
	return materialsCreateWork{file: file, parent: parent, intent: intent, markdown: markdown, resuming: old != nil}, nil
}

func (c *materialsWriteCoordinator) createOne(ctx context.Context, gateway domain.FeishuMaterialsGateway, snapshots *materialsSnapshots, binding domain.MaterialsSyncBinding, work materialsCreateWork) (any, error) {
	parent, err := snapshots.owned(c.plan.Target, binding, binding.Entries[materialsParentPath(work.file.path)])
	if err != nil {
		return nil, err
	}
	if parent != work.parent {
		return nil, errors.New("创建前父目录的身份或位置已改变")
	}
	if err := c.ops.verify(work.file); err != nil {
		return nil, err
	}
	known, found, err := c.ops.journal.find(work.intent)
	if err != nil {
		return nil, err
	}
	var recovered *domain.FeishuWikiNode
	if found {
		recovered, err = snapshots.node(known.Result.Node.NodeToken)
	} else if work.intent.NodeToken != nil {
		recovered, err = snapshots.node(*work.intent.NodeToken)
	} else if work.resuming {
		children, listErr := snapshots.list(parent)
		err = listErr
		for _, child := range children {
			if child.Title == work.intent.TemporaryTitle {
				if recovered != nil {
					return nil, errors.New("同一创建操作存在多个候选节点")
				}
				copy := child
				recovered = &copy
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if recovered == nil && (found || work.intent.NodeToken != nil) {
		return nil, errors.New("待恢复的已知资源已被删除，已保留回执")
	}
	var written domain.FeishuWriteResult
	complete := work.file.kind != domain.MaterialsSyncMarkdown || !work.intent.Placeholder && work.markdown != nil
	completedPlaceholder := false
	if recovered != nil {
		if err := validateMaterialsReceiptNode(c.plan.Target.SpaceID, work.intent, *recovered); err != nil {
			return nil, err
		}
		written = domain.FeishuWriteResult{Node: *recovered}
		if found {
			if recovered.ObjectToken != known.Result.Node.ObjectToken || recovered.ObjectType != known.Result.Node.ObjectType ||
				recovered.Title != known.Result.Node.Title && recovered.Title != work.intent.Title {
				return nil, errors.New("已知回执资源身份已改变")
			}
			written.Version, written.CommentsFingerprint = known.Result.Version, known.Result.CommentsFingerprint
			if known.Result.Version != nil {
				observed, err := snapshots.version(*recovered, materialsIncludeComments(work.file.path))
				if err != nil {
					return nil, err
				}
				completedRename := known.Result.Node.Title == work.intent.TemporaryTitle && recovered.Title == work.intent.Title && work.file.kind != domain.MaterialsSyncFile
				bodyMatches := false
				if completedRename {
					body, err := gateway.ReadDocument(*recovered, false)
					if err != nil {
						return nil, err
					}
					verifiedRevision := strconv.FormatInt(body.Revision, 10) == observed.Version
					completedPlaceholder = work.intent.Placeholder && !known.Complete && work.intent.ExpectedContentHash != nil &&
						verifiedRevision && materialsCreatedBodyMatches(work.intent, body, nil)
					bodyMatches = verifiedRevision && materialsCreatedBodyMatches(work.intent, body, known.ContentHash)
				}
				if observed.Version != *known.Result.Version && !(completedRename && (completedPlaceholder || bodyMatches)) ||
					known.Result.CommentsFingerprint != nil && observed.CommentsFingerprint != *known.Result.CommentsFingerprint {
					return nil, errors.New("创建回执中的资源版本或评论被人工修改，已保留")
				}
				written.Version, written.CommentsFingerprint = &observed.Version, &observed.CommentsFingerprint
			}
			complete = work.file.kind == domain.MaterialsSyncDirectory || known.Complete || completedPlaceholder
		}
		if !found && work.file.kind == domain.MaterialsSyncFile {
			reader, ok := gateway.(domain.FeishuFileFingerprintReader)
			if !ok {
				return nil, errors.New("远端文件缺少恢复内容核对能力")
			}
			proof, err := reader.ReadFileFingerprint(*recovered)
			if err != nil {
				return nil, err
			}
			if proof.SHA256 != work.file.hash || proof.RemoteVersion.CommentsFingerprint != materialsHash("[]") {
				return nil, errors.New("待恢复文件的内容或评论已改变")
			}
			written.Version, written.CommentsFingerprint = &proof.RemoteVersion.Version, &proof.RemoteVersion.CommentsFingerprint
		}
	} else {
		if err := c.ops.journal.requireConfirmedCreate(work.intent); err != nil {
			return nil, err
		}
		if work.intent.Phase == "create-attempted" {
			return nil, errors.New("上次创建已发出但结果尚不可见，已保留恢复记录")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := c.ops.journal.healthy(); err != nil {
			return nil, err
		}
		switch work.file.kind {
		case domain.MaterialsSyncDirectory:
			written, err = gateway.CreateDirectory(parent, work.intent.TemporaryTitle)
		case domain.MaterialsSyncMarkdown:
			if work.intent.Placeholder {
				if reservation, ok := gateway.(domain.FeishuDocumentReservation); ok {
					written, err = reservation.ReserveDocument(parent, work.intent.TemporaryTitle, c.scan.root)
				} else {
					written, err = gateway.CreateDirectory(parent, work.intent.TemporaryTitle)
				}
			} else if creator, ok := gateway.(domain.FeishuCompleteDocumentCreator); ok && work.markdown != nil && creator.SupportsCompleteDocumentCreation(*work.markdown) {
				written, err = creator.CreateCompleteDocument(parent, work.intent.TemporaryTitle, *work.markdown, c.scan.root)
			} else {
				content := ""
				if work.markdown != nil {
					content = *work.markdown
				}
				written, err = gateway.CreateDocument(parent, work.intent.TemporaryTitle, content, c.scan.root, materialsIncludeComments(work.file.path))
			}
		case domain.MaterialsSyncFile:
			written, err = gateway.UploadFile(parent, c.scan.root, work.file.path, work.intent.TemporaryTitle, nil, nil)
		default:
			err = errors.New("不支持的资料类型")
		}
		if err != nil {
			var unconfirmed *domain.FeishuUnconfirmedCreationError
			var gatewayUnconfirmed *materialsUnconfirmedCreationError
			if errors.As(err, &unconfirmed) || errors.As(err, &gatewayUnconfirmed) || process.IsCleanupFailure(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				if journalErr := c.ops.journal.markUnconfirmed(work.intent); journalErr != nil {
					return nil, errors.Join(err, journalErr)
				}
			}
			return nil, err
		}
	}
	var hash *string
	if found {
		hash = known.ContentHash
	}
	if completedPlaceholder {
		hash = work.intent.ExpectedContentHash
	}
	if work.file.kind == domain.MaterialsSyncDirectory || work.intent.Placeholder && !complete {
		blank := materialsContentHash("")
		hash = &blank
	}
	if err := c.ops.journal.record(work.intent, written, complete, hash); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	receipt, _, err := c.ops.journal.find(work.intent)
	return materialsCreated{work: work, receipt: receipt}, err
}

func materialsCreatedBodyMatches(intent domain.MaterialsSyncIntent, body domain.FeishuTextDocument, contentHash *string) bool {
	if contentHash == nil {
		contentHash = intent.ExpectedContentHash
	}
	if contentHash != nil {
		return materialsContentHash(body.Content) == *contentHash
	}
	return strings.TrimSpace(body.Content) == ""
}

func (c *materialsWriteCoordinator) createdBaseline(gateway domain.FeishuMaterialsGateway, created materialsCreated) (any, error) {
	work, receipt := created.work, created.receipt
	node := receipt.Result.Node
	version, err := gateway.Version(node, materialsIncludeComments(work.file.path))
	if err != nil {
		return nil, err
	}
	var hash *string
	bodyMatches := false
	if work.file.kind != domain.MaterialsSyncFile {
		body, err := gateway.ReadDocument(node, materialsIncludeComments(work.file.path))
		if err != nil {
			return nil, err
		}
		if version.Version != strconv.FormatInt(body.Revision, 10) || materialsIncludeComments(work.file.path) && version.CommentsFingerprint != body.CommentsFingerprint {
			return nil, errors.New("新建资料在核对正文期间被修改")
		}
		actual := materialsContentHash(body.Content)
		bodyMatches = materialsCreatedBodyMatches(work.intent, body, receipt.ContentHash)
		// A trustworthy creation revision proves the imported body even when the
		// provider normalizes its Markdown export. Capture that exported form;
		// missing revisions and prior exported hashes still require a body match.
		if (receipt.Result.Version == nil || receipt.ContentHash != nil) && !bodyMatches {
			return nil, errors.New("待登记资料正文与操作回执不一致")
		}
		hash = &actual
	}
	if receipt.Result.Version != nil && version.Version != *receipt.Result.Version && !(node.Title == work.intent.Title && hash != nil && bodyMatches) {
		return nil, errors.New("新建资料在登记前被人工修改")
	}
	if materialsIncludeComments(work.file.path) {
		expected := materialsHash("[]")
		if receipt.Result.CommentsFingerprint != nil {
			expected = *receipt.Result.CommentsFingerprint
		}
		if version.CommentsFingerprint != expected {
			return nil, errors.New("新建资料在登记前新增了人工评论")
		}
	}
	if err := c.ops.journal.record(work.intent, domain.FeishuWriteResult{Node: node, Version: &version.Version,
		CommentsFingerprint: &version.CommentsFingerprint}, receipt.Complete, hash); err != nil {
		return nil, err
	}
	return materialsCreatedBaseline{created: created, version: version, bodyHash: hash}, nil
}

func (c *materialsWriteCoordinator) finishCreate(ctx context.Context, gateway domain.FeishuMaterialsGateway, baseline materialsCreatedBaseline) (any, error) {
	work, receipt := baseline.created.work, baseline.created.receipt
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := c.ops.journal.healthy(); err != nil {
		return nil, err
	}
	fresh, err := gateway.Version(receipt.Result.Node, materialsIncludeComments(work.file.path))
	if err != nil {
		return nil, err
	}
	if fresh != baseline.version {
		return nil, errors.New("新建资料在改名前被人工修改，已保留")
	}
	written := domain.FeishuWriteResult{Node: receipt.Result.Node, Version: &fresh.Version, CommentsFingerprint: &fresh.CommentsFingerprint}
	complete, bodyHash := receipt.Complete, baseline.bodyHash
	if work.intent.Placeholder && work.markdown != nil && !complete {
		if written.Node.Title != work.intent.TemporaryTitle || bodyHash == nil || *bodyHash != materialsContentHash("") {
			return nil, errors.New("文档预留节点已被修改，已保留")
		}
		if err := c.ops.verify(work.file); err != nil {
			return nil, err
		}
		revision, err := strconv.ParseInt(fresh.Version, 10, 64)
		if err != nil || revision < 0 {
			return nil, errors.New("文档预留节点版本无效")
		}
		written, err = gateway.OverwriteDocument(written.Node, *work.markdown, revision, c.scan.root, &fresh.CommentsFingerprint, &work.parent)
		if err != nil {
			return nil, err
		}
		bodyHash = work.intent.ExpectedContentHash
		if written.VerifiedDocument != nil {
			hash := materialsContentHash(written.VerifiedDocument.Content)
			bodyHash = &hash
		}
		if err := c.ops.journal.record(work.intent, written, true, bodyHash); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fresh, err = materialsWrittenVersion(gateway, written, work.file, materialsIncludeComments(work.file.path))
		if err != nil {
			return nil, err
		}
		if fresh.CommentsFingerprint != baseline.version.CommentsFingerprint {
			return nil, errors.New("填写预留正文期间评论被修改")
		}
		complete = true
	}
	if written.Node.Title != work.intent.Title {
		written, err = gateway.Rename(written.Node, work.intent.Title)
		if err != nil {
			return nil, err
		}
		written.CommentsFingerprint = &fresh.CommentsFingerprint
		if err := c.ops.journal.record(work.intent, written, complete, bodyHash); err != nil {
			return nil, err
		}
	}
	if err := validateMaterialsReceiptNode(c.plan.Target.SpaceID, work.intent, written.Node); err != nil {
		return nil, err
	}
	if written.Node.Title != work.intent.Title {
		return nil, errors.New("改名回执的标题不一致")
	}
	version, err := gateway.Version(written.Node, materialsIncludeComments(work.file.path))
	if err != nil {
		return nil, err
	}
	if version.CommentsFingerprint != fresh.CommentsFingerprint {
		return nil, errors.New("新建资料在改名期间评论被修改")
	}
	if work.file.kind != domain.MaterialsSyncFile {
		body, err := gateway.ReadDocument(written.Node, false)
		if err != nil {
			return nil, err
		}
		if strconv.FormatInt(body.Revision, 10) != version.Version || bodyHash == nil || materialsContentHash(body.Content) != *bodyHash {
			return nil, errors.New("新建资料在改名期间正文被修改")
		}
	} else if written.Version != nil && version.Version != *written.Version {
		return nil, errors.New("上传文件在改名期间被修改")
	}
	if err := c.ops.journal.record(work.intent, domain.FeishuWriteResult{Node: written.Node, Version: &version.Version,
		CommentsFingerprint: &version.CommentsFingerprint}, complete, bodyHash); err != nil {
		return nil, err
	}
	if err := c.ops.verify(work.file); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	hash := work.file.hash
	if !complete {
		hash = ""
	}
	entry := domain.MaterialsSyncEntry{Path: work.file.path, Kind: work.file.kind, NodeToken: written.Node.NodeToken,
		ObjectToken: written.Node.ObjectToken, ObjectType: written.Node.ObjectType, ParentNodeToken: work.parent.NodeToken,
		Title: work.intent.Title, LocalHash: hash, RemoteVersion: version}
	return materialsEntryResult{file: work.file, entry: entry, intent: work.intent}, nil
}

func (c *materialsWriteCoordinator) createLevel(files []localMaterial) error {
	binding := cloneMaterialsBinding(c.state.value.Bindings[c.plan.SourceID])
	completeWave := true
	for _, file := range files {
		if file.kind != domain.MaterialsSyncMarkdown {
			completeWave = false
		}
	}
	jobs := []materialsWaveJob{}
	for _, file := range files {
		file := file
		if _, exists := binding.Entries[file.path]; exists {
			continue
		}
		if _, exists := binding.Entries[materialsParentPath(file.path)]; !exists {
			c.failed[file.path] = true
			c.skipped(file, "父目录未成功登记，已跳过该分支", true)
			continue
		}
		jobs = append(jobs, materialsWaveJob{path: file.path, run: func(_ context.Context, _ domain.FeishuMaterialsGateway, snapshots *materialsSnapshots) (any, error) {
			return c.prepareCreate(file, binding, snapshots, completeWave)
		}})
	}
	prepared, err := c.wave(domain.MaterialsSyncReserveNodes, jobs)
	if err != nil {
		return err
	}
	work := []materialsCreateWork{}
	for _, event := range prepared {
		if event.err != nil {
			continue
		}
		item := event.value.(materialsCreateWork)
		if err := materialsPutIntent(&binding, item.intent, false); err != nil {
			c.failure(item.file.path, domain.MaterialsSyncReserveNodes, err)
			continue
		}
		c.created[item.file.path] = true
		work = append(work, item)
	}
	if len(work) == 0 {
		return nil
	}
	if err := c.checkpoint(binding); err != nil {
		return err
	}
	binding = cloneMaterialsBinding(c.state.value.Bindings[c.plan.SourceID])
	jobs = nil
	for _, item := range work {
		item := item
		jobs = append(jobs, materialsWaveJob{path: item.file.path, run: func(ctx context.Context, gateway domain.FeishuMaterialsGateway, snapshots *materialsSnapshots) (any, error) {
			return c.createOne(ctx, gateway, snapshots, binding, item)
		}})
	}
	created, err := c.wave(domain.MaterialsSyncReserveNodes, jobs)
	if err != nil {
		return err
	}
	ready := []materialsCreated{}
	for _, event := range created {
		if event.err != nil {
			continue
		}
		item := event.value.(materialsCreated)
		for index := range binding.Pending {
			if binding.Pending[index].OperationID == item.work.intent.OperationID {
				token := item.receipt.Result.Node.NodeToken
				binding.Pending[index].NodeToken = &token
			}
		}
		ready = append(ready, item)
	}
	if len(ready) == 0 {
		return nil
	}
	// Persist every known token before any rename or body write.
	if err := c.checkpoint(binding); err != nil {
		return err
	}
	jobs = nil
	for _, item := range ready {
		item := item
		jobs = append(jobs, materialsWaveJob{path: item.work.file.path, run: func(_ context.Context, gateway domain.FeishuMaterialsGateway, _ *materialsSnapshots) (any, error) {
			return c.createdBaseline(gateway, item)
		}})
	}
	baselines, err := c.wave(domain.MaterialsSyncVerify, jobs)
	if err != nil {
		return err
	}
	jobs = nil
	for _, event := range baselines {
		if event.err != nil {
			continue
		}
		baseline := event.value.(materialsCreatedBaseline)
		jobs = append(jobs, materialsWaveJob{path: baseline.created.work.file.path, run: func(ctx context.Context, gateway domain.FeishuMaterialsGateway, _ *materialsSnapshots) (any, error) {
			return c.finishCreate(ctx, gateway, baseline)
		}})
	}
	entries, err := c.wave(domain.MaterialsSyncCreateDirectories, jobs)
	if err != nil {
		return err
	}
	return c.registerWave(entries)
}

func (c *materialsWriteCoordinator) registerWave(events []materialsWaveEvent) error {
	binding := cloneMaterialsBinding(c.state.value.Bindings[c.plan.SourceID])
	completed := []localMaterial{}
	for _, event := range events {
		if event.err != nil {
			continue
		}
		entry := event.value.(materialsEntryResult)
		binding.Entries[entry.file.path] = entry.entry
		binding.Pending = removeMaterialIntent(binding.Pending, entry.intent.OperationID)
		completed = append(completed, entry.file)
	}
	if len(completed) == 0 {
		return nil
	}
	if err := c.checkpoint(binding); err != nil {
		return err
	}
	for _, file := range completed {
		c.markCompleted(file)
	}
	return nil
}

type materialsUpdateWork struct {
	file      localMaterial
	entry     domain.MaterialsSyncEntry
	node      domain.FeishuWikiNode
	parent    domain.FeishuWikiNode
	intent    domain.MaterialsSyncIntent
	markdown  string
	overwrite bool
}

func (c *materialsWriteCoordinator) prepareUpdate(file localMaterial, item domain.MaterialsSyncItem, binding domain.MaterialsSyncBinding, snapshots *materialsSnapshots) (any, error) {
	entry, exists := binding.Entries[file.path]
	if !exists {
		return nil, errors.New("远端资料尚未成功登记")
	}
	node, err := snapshots.owned(c.plan.Target, binding, entry)
	if err != nil {
		return nil, err
	}
	_, overwrite := c.approval.OverwritePaths[file.path]
	expected := entry.RemoteVersion
	if overwrite {
		if item.ObservedNode == nil || *item.ObservedNode != node || item.ObservedVersion == nil {
			return nil, errors.New("冲突对象或版本已改变，不能覆盖")
		}
		expected = *item.ObservedVersion
	} else if node.Title != entry.Title {
		return nil, errors.New("远端资料标题已改变，未获覆盖确认")
	}
	if err := c.ops.verify(file); err != nil {
		return nil, err
	}
	op, err := newIntegrationUUID()
	if err != nil {
		return nil, err
	}
	intent := domain.MaterialsSyncIntent{OperationID: op, Path: file.path, Kind: file.kind, ParentNodeToken: entry.ParentNodeToken,
		Title: entry.Title, TemporaryTitle: entry.Title + " [sw-" + op + "]", LocalHash: file.hash, Phase: "update", NodeToken: nonblank(entry.NodeToken), BaseVersion: &expected}
	markdown := ""
	if file.kind == domain.MaterialsSyncMarkdown {
		for _, linked := range file.linkedPaths {
			linkedEntry, exists := binding.Entries[linked]
			if !exists {
				return nil, fmt.Errorf("Markdown 引用的资源尚未登记：%s", linked)
			}
			if _, err := snapshots.owned(c.plan.Target, binding, linkedEntry); err != nil {
				return nil, err
			}
		}
		markdown, err = c.ops.markdown(file, binding)
		if err != nil {
			return nil, err
		}
		hash := materialsContentHash(markdown)
		intent.ExpectedContentHash = &hash
	}
	parentEntry, exists := binding.Entries[materialsParentPath(file.path)]
	if !exists {
		return nil, errors.New("资料清单缺少父目录")
	}
	trial := cloneMaterialsBinding(binding)
	if err := materialsPutIntent(&trial, intent, overwrite); err != nil {
		return nil, err
	}
	return materialsUpdateWork{file: file, entry: entry, node: node, parent: nodeFromEntry(parentEntry, c.plan.Target.SpaceID), intent: intent, markdown: markdown, overwrite: overwrite}, nil
}

func (c *materialsWriteCoordinator) updateOne(ctx context.Context, gateway domain.FeishuMaterialsGateway, work materialsUpdateWork) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := c.ops.journal.healthy(); err != nil {
		return nil, err
	}
	if err := c.ops.verify(work.file); err != nil {
		return nil, err
	}
	var written domain.FeishuWriteResult
	var err error
	if work.file.kind == domain.MaterialsSyncFile {
		written, err = gateway.UploadFile(work.parent, c.scan.root, work.file.path, work.node.Title, &work.node, work.intent.BaseVersion)
	} else {
		revision, parseErr := strconv.ParseInt(work.intent.BaseVersion.Version, 10, 64)
		if parseErr != nil || revision < 0 {
			return nil, errors.New("飞书文档版本不是有效 revision_id")
		}
		written, err = gateway.OverwriteDocument(work.node, work.markdown, revision, c.scan.root, &work.intent.BaseVersion.CommentsFingerprint, &work.parent)
	}
	if err != nil {
		return nil, err
	}
	if err := c.ops.journal.record(work.intent, written, true, nil); err != nil {
		return nil, err
	}
	if !materialsSameIdentity(written.Node, work.entry, c.plan.Target.SpaceID) || written.Node.Title != work.node.Title && written.Node.Title != work.entry.Title {
		return nil, errors.New("写入回执的资源身份或位置已改变")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if written.Node.Title != work.entry.Title {
		if !work.overwrite {
			return nil, errors.New("未确认恢复人工修改的标题")
		}
		before, err := materialsWrittenVersion(gateway, written, work.file, true)
		if err != nil {
			return nil, err
		}
		fresh, err := gateway.Version(written.Node, true)
		if err != nil {
			return nil, err
		}
		if before != fresh {
			return nil, errors.New("恢复标题前资料又被修改")
		}
		written, err = gateway.Rename(written.Node, work.entry.Title)
		if err != nil {
			return nil, err
		}
		// Keep the durable intent identity; the journal records the final title.
		if err := c.ops.journal.record(work.intent, written, true, nil); err != nil {
			return nil, err
		}
	}
	version, err := materialsWrittenVersion(gateway, written, work.file, true)
	if err != nil {
		return nil, err
	}
	if version.CommentsFingerprint != work.intent.BaseVersion.CommentsFingerprint {
		return nil, errors.New("写入期间资料评论发生变化")
	}
	written.Version, written.CommentsFingerprint = &version.Version, &version.CommentsFingerprint
	if err := c.ops.journal.record(work.intent, written, true, nil); err != nil {
		return nil, err
	}
	if err := c.ops.verify(work.file); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entry := work.entry
	entry.LocalHash, entry.RemoteVersion = work.file.hash, version
	return materialsEntryResult{file: work.file, entry: entry, intent: work.intent}, nil
}

func materialsWrittenVersion(gateway domain.FeishuMaterialsGateway, written domain.FeishuWriteResult, file localMaterial, comments bool) (domain.FeishuRemoteVersion, error) {
	version, err := gateway.Version(written.Node, comments)
	if err != nil {
		return version, err
	}
	if written.Version != nil && version.Version != *written.Version {
		return version, errors.New("写入回执版本与远端版本不一致")
	}
	if written.CommentsFingerprint != nil && comments && version.CommentsFingerprint != *written.CommentsFingerprint {
		return version, errors.New("写入回执评论与远端评论不一致")
	}
	return version, nil
}

func (c *materialsWriteCoordinator) updateLevel(items []domain.MaterialsSyncItem) error {
	binding := cloneMaterialsBinding(c.state.value.Bindings[c.plan.SourceID])
	jobs := []materialsWaveJob{}
	for _, item := range items {
		item := item
		file, exists := c.scan.files[item.Path]
		if !exists {
			c.failure(item.Path, domain.MaterialsSyncUpload, errors.New("更新资料已没有对应本地文件"))
			continue
		}
		if c.completed[file.path] || c.failed[file.path] {
			continue
		}
		entry, exists := binding.Entries[file.path]
		if !exists {
			continue
		}
		_, overwrite := c.approval.OverwritePaths[file.path]
		if entry.LocalHash == file.hash && !overwrite {
			c.skipped(file, "本地内容与已登记版本一致", true)
			continue
		}
		jobs = append(jobs, materialsWaveJob{path: file.path, run: func(_ context.Context, _ domain.FeishuMaterialsGateway, snapshots *materialsSnapshots) (any, error) {
			return c.prepareUpdate(file, item, binding, snapshots)
		}})
	}
	prepared, err := c.wave(domain.MaterialsSyncVerify, jobs)
	if err != nil {
		return err
	}
	work := []materialsUpdateWork{}
	for _, event := range prepared {
		if event.err != nil {
			continue
		}
		item := event.value.(materialsUpdateWork)
		if err := materialsPutIntent(&binding, item.intent, item.overwrite); err != nil {
			c.failure(item.file.path, domain.MaterialsSyncUpload, err)
			continue
		}
		work = append(work, item)
	}
	if len(work) == 0 {
		return nil
	}
	if err := c.checkpoint(binding); err != nil {
		return err
	}
	jobs = nil
	for _, item := range work {
		item := item
		jobs = append(jobs, materialsWaveJob{path: item.file.path, run: func(ctx context.Context, gateway domain.FeishuMaterialsGateway, _ *materialsSnapshots) (any, error) {
			return c.updateOne(ctx, gateway, item)
		}})
	}
	entries, err := c.wave(domain.MaterialsSyncUpload, jobs)
	if err != nil {
		return err
	}
	return c.registerWave(entries)
}

func (c *materialsWriteCoordinator) deleteOne(item domain.MaterialsSyncItem) error {
	if item.Path == materialsRequirementRoot || item.Path == materialsFilesRoot {
		return errors.New("受保护的需求根或资料目录不能删除")
	}
	if err := c.ctx.Err(); err != nil {
		return err
	}
	gateway := c.ops.worker(c.ctx)
	binding := cloneMaterialsBinding(c.state.value.Bindings[c.plan.SourceID])
	entry, exists := binding.Entries[item.Path]
	if !exists {
		return nil
	}
	if item.Entry == nil || *item.Entry != entry {
		return errors.New("待删除资源与当前登记不一致")
	}
	node, err := gateway.GetNode(entry.NodeToken)
	if err != nil {
		return err
	}
	if node != nil {
		owned, err := newMaterialsSnapshots(gateway).owned(c.plan.Target, binding, entry)
		if err != nil {
			return err
		}
		version, err := gateway.Version(owned, materialsIncludeComments(item.Path))
		if err != nil {
			return err
		}
		expected := item.ObservedVersion
		if expected == nil {
			expected = &entry.RemoteVersion
		}
		if owned.Title != entry.Title || version != *expected {
			return errors.New("待删除资料已被人工修改，已保留")
		}
		children, err := gateway.ListChildren(owned)
		if err != nil {
			return err
		}
		if len(children) > 0 {
			c.result.Progress.Conflicts++
			c.result.Messages = append(c.result.Messages, item.Path+"：包含人工或尚未删除的子节点，已保留")
			return nil
		}
		var intent domain.MaterialsSyncIntent
		for _, pending := range binding.Pending {
			if pending.Path == item.Path && pending.Phase == "delete" {
				intent = pending
			}
		}
		if intent.OperationID == "" {
			id, err := newIntegrationUUID()
			if err != nil {
				return err
			}
			intent = domain.MaterialsSyncIntent{OperationID: id, Path: item.Path, Kind: item.Kind, ParentNodeToken: entry.ParentNodeToken,
				Title: entry.Title, TemporaryTitle: entry.Title + " [sw-" + id + "]", LocalHash: entry.LocalHash, Phase: "delete", NodeToken: nonblank(entry.NodeToken), BaseVersion: &version}
			binding.Pending = append(binding.Pending, intent)
			if err := c.checkpoint(binding); err != nil {
				return err
			}
		}
		if err := gateway.DeleteNode(owned); err != nil {
			return err
		}
		remaining, err := gateway.GetNode(owned.NodeToken)
		if err != nil {
			return err
		}
		if remaining != nil {
			return errors.New("删除结果尚未确认，保留恢复记录")
		}
	}
	binding = cloneMaterialsBinding(c.state.value.Bindings[c.plan.SourceID])
	delete(binding.Entries, item.Path)
	pending := binding.Pending[:0]
	for _, intent := range binding.Pending {
		if intent.Path != item.Path || intent.Phase != "delete" {
			pending = append(pending, intent)
		}
	}
	binding.Pending = pending
	if err := c.checkpoint(binding); err != nil {
		return err
	}
	c.result.Progress.Deleted++
	return nil
}

func materialsSortedDepths[T any](levels map[int][]T) []int {
	depths := make([]int, 0, len(levels))
	for depth := range levels {
		depths = append(depths, depth)
	}
	sort.Ints(depths)
	return depths
}
