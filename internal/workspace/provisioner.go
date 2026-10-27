package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

type BranchReuseKey struct {
	RepositoryID     string `json:"repositoryId"`
	Branch           string `json:"branch"`
	StateFingerprint string `json:"stateFingerprint"`
}

var errRemoteBranchMissing = errors.New("远程分支不存在")

type BranchReuseConflict struct {
	Key                     BranchReuseKey           `json:"key"`
	ServiceID               string                   `json:"serviceId"`
	ServiceName             string                   `json:"serviceName"`
	ModuleName              string                   `json:"moduleName"`
	LocalExists             bool                     `json:"localExists"`
	RemoteRefs              []string                 `json:"remoteRefs"`
	OccupiedWorktreePaths   []string                 `json:"occupiedWorktreePaths"`
	LockedWorktreePaths     []string                 `json:"lockedWorktreePaths"`
	Strategy                domain.WorkspaceStrategy `json:"strategy"`
	ReusesRemoteCloneTarget bool                     `json:"reusesRemoteCloneTarget"`
	RequiresForceAttach     bool                     `json:"requiresForceAttach"`
}

type TaskModuleSelection struct {
	ID               string                   `json:"id"`
	Name             string                   `json:"name"`
	Strategy         domain.WorkspaceStrategy `json:"strategy"`
	BaseRef          string                   `json:"baseRef"`
	BaseRemote       string                   `json:"baseRemote"`
	TargetBranch     string                   `json:"targetBranch"`
	Source           domain.TaskModuleSource  `json:"source"`
	TagEnabled       bool                     `json:"tagEnabled"`
	TagMode          domain.TagBuildMode      `json:"tagMode"`
	TagTargetRef     string                   `json:"tagTargetRef"`
	TagMessagePrefix string                   `json:"tagMessagePrefix"`
}

type TaskServiceSelection struct {
	ServiceID string                `json:"serviceId"`
	Modules   []TaskModuleSelection `json:"modules"`
}
type ModuleBaseOverride struct {
	ServiceID    string `json:"serviceId"`
	ModuleID     string `json:"moduleId"`
	BaseRef      string `json:"baseRef"`
	TargetBranch string `json:"targetBranch"`
}

type WorkspaceProvisionRequest struct {
	TaskDirectory            string
	Repository               domain.RepositoryConfig
	Service                  domain.GroupServiceConfig
	RequestedFeatureBranch   string
	ConfirmedBranchReuseKeys []BranchReuseKey
	ModuleBranches           map[string]string
	ModuleSources            map[string]domain.TaskModuleSource
	ModuleDisplayNames       map[string]string
}

type WorkspaceProvisioner interface {
	Strategy() domain.WorkspaceStrategy
	Provision(context.Context, WorkspaceProvisionRequest, func(process.StreamEvent)) ([]domain.ServiceWorkspace, error)
	Rollback(context.Context, WorkspaceProvisionRequest, []domain.ServiceWorkspace) error
}

// WorkspaceProvisionerWithProgress is an optional per-operation capability. The
// legacy provisioner contract remains usable without a progress observer.
type WorkspaceProvisionerWithProgress interface {
	ProvisionWithProgress(context.Context, WorkspaceProvisionRequest, func(process.StreamEvent), func(TaskCreationProgress)) ([]domain.ServiceWorkspace, error)
}

// WorkspaceRollbackWithProgress is an optional rollback observer capability.
// It does not alter the legacy provisioner result contract.
type WorkspaceRollbackWithProgress interface {
	RollbackWithProgress(context.Context, WorkspaceProvisionRequest, []domain.ServiceWorkspace, func(TaskCreationProgress)) error
}

func gitCleanupProgressFailure(operation string, result process.Result) error {
	if result.Successful() {
		return nil
	}
	details := strings.TrimSpace(result.Stderr)
	if details == "" {
		details = strings.TrimSpace(result.Stdout)
	}
	if details == "" {
		return fmt.Errorf("%s未成功，Git 退出码 %d，请检查仓库状态", operation, result.ExitCode)
	}
	return fmt.Errorf("%s未成功，Git 退出码 %d：%s；请检查仓库状态", operation, result.ExitCode, details)
}

type WorkspaceProvisioningService struct {
	byStrategy map[domain.WorkspaceStrategy]WorkspaceProvisioner
}

type completedProvision struct {
	request    WorkspaceProvisionRequest
	workspaces []domain.ServiceWorkspace
}

func NewWorkspaceProvisioningService(git *GitClient, bootstrap *BootstrapService, locks *OperationLocks) *WorkspaceProvisioningService {
	standard := &StandardWorktreeProvisioner{Git: git, Bootstrap: bootstrap, Locks: locks}
	clone := &IndependentCloneProvisioner{Git: git, Bootstrap: bootstrap}
	return NewWorkspaceProvisioningServiceWith([]WorkspaceProvisioner{standard, clone})
}

func NewWorkspaceProvisioningServiceWith(provisioners []WorkspaceProvisioner) *WorkspaceProvisioningService {
	registered := make(map[domain.WorkspaceStrategy]WorkspaceProvisioner, len(provisioners))
	for _, p := range provisioners {
		if p != nil {
			registered[p.Strategy()] = p
		}
	}
	return &WorkspaceProvisioningService{byStrategy: registered}
}

func (s *WorkspaceProvisioningService) Provision(ctx context.Context, request WorkspaceProvisionRequest, onOutput func(process.StreamEvent)) ([]domain.ServiceWorkspace, error) {
	return s.ProvisionWithProgress(ctx, request, onOutput, nil)
}

func (s *WorkspaceProvisioningService) ProvisionWithProgress(ctx context.Context, request WorkspaceProvisionRequest, onOutput func(process.StreamEvent), onProgress func(TaskCreationProgress)) ([]domain.ServiceWorkspace, error) {
	checking := TaskCreationProgress{Phase: TaskCreationPhaseChecking, ServiceID: request.Service.ID, ServiceName: request.Service.DisplayName}
	var branches, names map[string]string
	if err := runTaskCreationProgress(onProgress, checking, "正在检查模块配置", "模块配置检查完成", func() error {
		var err error
		branches, err = resolveModuleTargets(request)
		if err != nil {
			return err
		}
		names, err = resolveModuleDisplayNames(request)
		return err
	}); err != nil {
		return nil, err
	}
	var completed []completedProvision
	for _, module := range request.Service.Modules {
		moduleRequest := request
		moduleRequest.Service = request.Service
		moduleRequest.Service.Modules = []domain.ServiceModuleConfig{module}
		moduleRequest.ModuleBranches = map[string]string{module.ID: branches[module.ID]}
		moduleRequest.ModuleSources = map[string]domain.TaskModuleSource{}
		if source, ok := request.ModuleSources[module.ID]; ok {
			moduleRequest.ModuleSources[module.ID] = source
		}
		moduleRequest.ModuleDisplayNames = map[string]string{module.ID: names[module.ID]}
		p := s.byStrategy[module.Strategy]
		if p == nil {
			return rollbackProvisioning(ctx, s, completed, fmt.Errorf("未注册工作区策略：%s", module.Strategy), onProgress)
		}
		var workspaces []domain.ServiceWorkspace
		var err error
		if progressProvisioner, ok := p.(WorkspaceProvisionerWithProgress); ok {
			workspaces, err = progressProvisioner.ProvisionWithProgress(ctx, moduleRequest, onOutput, onProgress)
		} else {
			workspaces, err = p.Provision(ctx, moduleRequest, onOutput)
		}
		if err != nil {
			return rollbackProvisioning(ctx, s, completed, err, onProgress)
		}
		completed = append(completed, completedProvision{moduleRequest, workspaces})
	}
	var result []domain.ServiceWorkspace
	for _, item := range completed {
		result = append(result, item.workspaces...)
	}
	return result, nil
}

func rollbackProvisioning(ctx context.Context, s *WorkspaceProvisioningService, completed []completedProvision, primary error, onProgress func(TaskCreationProgress)) ([]domain.ServiceWorkspace, error) {
	for i := len(completed) - 1; i >= 0; i-- {
		item := completed[i]
		if err := s.RollbackWithProgress(ctx, item.request, item.workspaces, onProgress); err != nil {
			primary = errors.Join(primary, err)
		}
	}
	return nil, primary
}

func (s *WorkspaceProvisioningService) Rollback(ctx context.Context, request WorkspaceProvisionRequest, workspaces []domain.ServiceWorkspace) error {
	return s.RollbackWithProgress(ctx, request, workspaces, nil)
}

func (s *WorkspaceProvisioningService) RollbackWithProgress(ctx context.Context, request WorkspaceProvisionRequest, workspaces []domain.ServiceWorkspace, onProgress func(TaskCreationProgress)) error {
	for i := len(workspaces) - 1; i >= 0; i-- {
		ws := workspaces[i]
		event := taskCreationProgressForWorkspace(ws, TaskCreationPhaseRollback)
		var module *domain.ServiceModuleConfig
		for j := range request.Service.Modules {
			if request.Service.Modules[j].ID == ws.ModuleID {
				module = &request.Service.Modules[j]
				break
			}
		}
		if module == nil {
			err := fmt.Errorf("回滚请求缺少模块快照：%s", ws.ModuleID)
			emitTaskCreationProgress(onProgress, TaskCreationProgress{Phase: event.Phase, Status: TaskCreationProgressStarted, ServiceID: event.ServiceID, ServiceName: event.ServiceName, ModuleID: event.ModuleID, ModuleName: event.ModuleName, Message: "正在回滚模块工作区"})
			failed := event
			failed.Status = TaskCreationProgressFailed
			failed.Message = err.Error()
			emitTaskCreationProgress(onProgress, failed)
			return err
		}
		r := request
		r.Service = request.Service
		r.Service.Modules = []domain.ServiceModuleConfig{*module}
		if p := s.byStrategy[ws.Strategy]; p != nil {
			if progresser, ok := p.(WorkspaceRollbackWithProgress); ok {
				if err := progresser.RollbackWithProgress(ctx, r, []domain.ServiceWorkspace{ws}, onProgress); err != nil {
					return err
				}
				continue
			}
			if err := runTaskCreationProgress(onProgress, event, "正在回滚模块工作区", "模块工作区回滚完成", func() error {
				return p.Rollback(ctx, r, []domain.ServiceWorkspace{ws})
			}); err != nil {
				return err
			}
		} else {
			err := fmt.Errorf("未注册工作区策略：%s", ws.Strategy)
			emitTaskCreationProgress(onProgress, TaskCreationProgress{Phase: event.Phase, Status: TaskCreationProgressStarted, ServiceID: event.ServiceID, ServiceName: event.ServiceName, ModuleID: event.ModuleID, ModuleName: event.ModuleName, Message: "正在回滚模块工作区"})
			failed := event
			failed.Status = TaskCreationProgressFailed
			failed.Message = err.Error()
			emitTaskCreationProgress(onProgress, failed)
			return err
		}
	}
	return nil
}

func moduleMasterBranch(service domain.GroupServiceConfig, module domain.ServiceModuleConfig) string {
	if module.MasterBranch != nil && strings.TrimSpace(*module.MasterBranch) != "" {
		return strings.TrimSpace(*module.MasterBranch)
	}
	return strings.TrimSpace(service.MasterBranch)
}
func moduleMasterRemote(service domain.GroupServiceConfig, module domain.ServiceModuleConfig) (string, error) {
	ref, err := ParseRemoteBranchRef(moduleMasterBranch(service, module))
	if err != nil {
		return "", err
	}
	return ref.Remote, nil
}
func moduleEffectiveTagTarget(service domain.GroupServiceConfig, module domain.ServiceModuleConfig) string {
	if !module.TagEnabled || module.TagMode != domain.TagBuildMergeToTargetBranch {
		return ""
	}
	if module.TagTargetRef != nil && strings.TrimSpace(*module.TagTargetRef) != "" {
		return *module.TagTargetRef
	}
	return service.TestTagBaselineRef
}
func moduleEffectiveName(module domain.ServiceModuleConfig) string {
	name := strings.TrimSpace(module.Name)
	if name == "" {
		return "default"
	}
	return name
}

func moduleDirectoryName(service domain.GroupServiceConfig, module domain.ServiceModuleConfig) (string, error) {
	serviceName, err := SafeDirectoryName(service.DisplayName)
	if err != nil {
		return "", err
	}
	moduleName := strings.ReplaceAll(moduleEffectiveName(module), "/", "-")
	moduleName, err = SafeDirectoryName(moduleName)
	if err != nil {
		return "", err
	}
	return serviceName + "-" + moduleName, nil
}
func defaultTargetBranch(requested string, modules []domain.ServiceModuleConfig, module domain.ServiceModuleConfig) string {
	if strings.TrimSpace(requested) == "" {
		return ""
	}
	if len(modules) == 1 {
		return strings.TrimSpace(requested)
	}
	return strings.TrimSpace(requested) + "-" + moduleEffectiveName(module)
}

func resolveModuleTargets(request WorkspaceProvisionRequest) (map[string]string, error) {
	targets := map[string]string{}
	for _, module := range request.Service.Modules {
		target := strings.TrimSpace(request.ModuleBranches[module.ID])
		if target == "" && module.Strategy == domain.WorkspaceStandardWorktree {
			target = defaultTargetBranch(request.RequestedFeatureBranch, request.Service.Modules, module)
		}
		targets[module.ID] = target
	}
	seen := map[string]string{}
	for _, module := range request.Service.Modules {
		if module.Strategy != domain.WorkspaceStandardWorktree {
			continue
		}
		target := targets[module.ID]
		if target == "" {
			continue
		}
		key := strings.ToLower(target)
		if previous := seen[key]; previous != "" {
			return nil, fmt.Errorf("同一服务的 Worktree 模块目标分支不能重复：%s", target)
		}
		seen[key] = target
	}
	return targets, nil
}
func resolveModuleDisplayNames(request WorkspaceProvisionRequest) (map[string]string, error) {
	result := map[string]string{}
	for _, module := range request.Service.Modules {
		name := request.ModuleDisplayNames[module.ID]
		if strings.TrimSpace(name) == "" {
			base := moduleMasterBranch(request.Service, module)
			configured := strings.TrimSpace(module.Name)
			if configured == "" || (len(request.Service.Modules) == 1 && configured == "default") {
				if len(request.Service.Modules) == 1 {
					name = request.Service.DisplayName
				} else {
					name = base[strings.LastIndex(base, "/")+1:]
				}
			} else {
				name = configured
			}
		}
		result[module.ID] = name
	}
	return result, nil
}

func branchReuseFingerprint(localSHA string, remoteRefs []string, worktrees []WorktreeRecord) string {
	refs := append([]string(nil), remoteRefs...)
	sort.Slice(refs, func(i, j int) bool { return strings.SplitN(refs[i], "@", 2)[0] < strings.SplitN(refs[j], "@", 2)[0] })
	records := append([]WorktreeRecord(nil), worktrees...)
	sort.Slice(records, func(i, j int) bool { return records[i].Path < records[j].Path })
	remoteParts := make([]string, 0, len(refs))
	for _, ref := range refs {
		remoteParts = append(remoteParts, ref)
	}
	worktreeParts := make([]string, 0, len(records))
	for _, wt := range records {
		worktreeParts = append(worktreeParts, fmt.Sprintf("%s|%t", wt.Path, wt.Locked))
	}
	return fmt.Sprintf("local=%s;remotes=%s;worktrees=%s", localSHA, strings.Join(remoteParts, ","), strings.Join(worktreeParts, ","))
}
func confirmedReuse(request WorkspaceProvisionRequest, key BranchReuseKey) bool {
	for _, c := range request.ConfirmedBranchReuseKeys {
		if c.RepositoryID == key.RepositoryID && c.Branch == key.Branch && (c.StateFingerprint == "" || c.StateFingerprint == key.StateFingerprint) {
			return true
		}
	}
	return false
}

type WorkspaceBranchReuseInspector struct {
	Git   *GitClient
	Locks *OperationLocks
}

func NewWorkspaceBranchReuseInspector(git *GitClient, locks *OperationLocks) *WorkspaceBranchReuseInspector {
	if git == nil {
		git = NewGitClient(nil, "")
	}
	if locks == nil {
		locks = NewOperationLocks("")
	}
	return &WorkspaceBranchReuseInspector{Git: git, Locks: locks}
}
func (i *WorkspaceBranchReuseInspector) Inspect(ctx context.Context, repository domain.RepositoryConfig, service domain.GroupServiceConfig, requestedBranch string, moduleBranches map[string]string) ([]BranchReuseConflict, error) {
	return i.InspectWithProgress(ctx, repository, service, requestedBranch, moduleBranches, nil)
}

func (i *WorkspaceBranchReuseInspector) InspectWithProgress(ctx context.Context, repository domain.RepositoryConfig, service domain.GroupServiceConfig, requestedBranch string, moduleBranches map[string]string, onProgress func(TaskCreationProgress)) ([]BranchReuseConflict, error) {
	root, err := filepath.Abs(repository.RootPath)
	if err != nil {
		return nil, err
	}
	common, err := i.Git.CommonDirectory(root)
	if err != nil {
		return nil, err
	}
	var conflicts []BranchReuseConflict
	err = i.Locks.WithRepository(common, func() error {
		top, err := i.Git.TopLevel(root)
		if err != nil {
			return err
		}
		top, _ = canonicalPath(top)
		expected, _ := canonicalPath(root)
		if top != expected {
			return fmt.Errorf("配置的仓库路径已不再是 Git 顶层目录：%s", root)
		}
		actualCommon, err := i.Git.CommonDirectory(root)
		if err != nil {
			return err
		}
		actualCommon, _ = canonicalPath(actualCommon)
		expectedCommon, _ := canonicalPath(repository.GitCommonDirectory)
		if actualCommon != expectedCommon {
			return fmt.Errorf("配置的仓库 Git 身份已变化：%s", root)
		}
		if err := i.Git.PruneWorktrees(root); err != nil {
			return err
		}
		request := WorkspaceProvisionRequest{TaskDirectory: root, Repository: repository, Service: service, RequestedFeatureBranch: requestedBranch, ModuleBranches: moduleBranches}
		targets, err := resolveModuleTargets(request)
		if err != nil {
			return err
		}
		for _, module := range service.Modules {
			target := targets[module.ID]
			checking := taskCreationProgressForModule(service, module, TaskCreationPhaseChecking)
			var conflict *BranchReuseConflict
			err := runTaskCreationProgress(onProgress, checking, "正在检查模块工作区与远端分支", "模块工作区与远端分支检查完成", func() error {
				refresh := taskCreationProgressForModule(service, module, TaskCreationPhaseRefreshingRemote)
				return runTaskCreationProgress(onProgress, refresh, "正在复核远端分支引用", "远端分支引用复核完成", func() error {
					if module.Strategy == domain.WorkspaceIndependentClone {
						var inspectErr error
						conflict, inspectErr = i.inspectClone(ctx, repository, service, module, target)
						return inspectErr
					}
					var inspectErr error
					conflict, inspectErr = i.inspectWorktree(ctx, repository, service, module, target, root)
					return inspectErr
				})
			})
			if err != nil {
				return err
			}
			if conflict != nil {
				conflicts = append(conflicts, *conflict)
			}
		}
		return nil
	})
	return conflicts, err
}

func (i *WorkspaceBranchReuseInspector) inspectClone(ctx context.Context, repository domain.RepositoryConfig, service domain.GroupServiceConfig, module domain.ServiceModuleConfig, target string) (*BranchReuseConflict, error) {
	base, err := ParseRemoteBranchRef(moduleMasterBranch(service, module))
	if err != nil {
		return nil, err
	}
	if _, err = i.remoteHeadSHA(ctx, repository, base.Remote, base.Branch); errors.Is(err, errRemoteBranchMissing) {
		return nil, fmt.Errorf("远程主分支不存在：%s/%s", base.Remote, base.Branch)
	} else if err != nil {
		return nil, err
	}
	if target == "" {
		return nil, nil
	}
	sha, err := i.remoteHeadSHA(ctx, repository, base.Remote, target)
	if errors.Is(err, errRemoteBranchMissing) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &BranchReuseConflict{Key: BranchReuseKey{repository.ID, target, fmt.Sprintf("clone-remote=%s/%s@%s", base.Remote, target, sha)}, ServiceID: service.ID, ServiceName: service.DisplayName, ModuleName: moduleEffectiveName(module), RemoteRefs: []string{base.Remote + "/" + target}, Strategy: domain.WorkspaceIndependentClone, ReusesRemoteCloneTarget: true}, nil
}
func (i *WorkspaceBranchReuseInspector) inspectWorktree(ctx context.Context, repository domain.RepositoryConfig, service domain.GroupServiceConfig, module domain.ServiceModuleConfig, branch, root string) (*BranchReuseConflict, error) {
	if strings.TrimSpace(branch) == "" {
		return nil, fmt.Errorf("Worktree 模块目标分支不能为空：%s", module.Name)
	}
	remote, err := moduleMasterRemote(service, module)
	if err != nil {
		return nil, err
	}
	if err = i.Git.Fetch(root, remote); err != nil {
		return nil, err
	}
	local, err := i.Git.RunContext(ctx, root, 2*time.Minute, false, false, "rev-parse", "--verify", "refs/heads/"+branch)
	if err != nil {
		return nil, err
	}
	localSHA := ""
	if local.ExitCode == 0 {
		localSHA = strings.TrimSpace(local.Stdout)
	}
	remoteRef := remote + "/" + branch
	remoteResult, err := i.Git.RunContext(ctx, root, 2*time.Minute, false, false, "rev-parse", "--verify", "refs/remotes/"+remoteRef)
	if err != nil {
		return nil, err
	}
	refs := []string{}
	if remoteResult.ExitCode == 0 {
		refs = append(refs, remoteRef+"@"+strings.TrimSpace(remoteResult.Stdout))
	}
	if localSHA == "" && len(refs) == 0 {
		return nil, nil
	}
	var occupied []WorktreeRecord
	if localSHA != "" {
		all, err := i.Git.Worktrees(root)
		if err != nil {
			return nil, err
		}
		for _, wt := range all {
			if wt.Branch == branch {
				occupied = append(occupied, wt)
			}
		}
	}
	display := moduleEffectiveName(module)
	if len(service.Modules) == 1 && display == "default" {
		display = service.DisplayName
	}
	if len(service.Modules) > 1 && display == "default" {
		base := moduleMasterBranch(service, module)
		display = base[strings.LastIndex(base, "/")+1:]
	}
	var paths, locked []string
	for _, wt := range occupied {
		paths = append(paths, wt.Path)
		if wt.Locked {
			locked = append(locked, wt.Path)
		}
	}
	return &BranchReuseConflict{Key: BranchReuseKey{repository.ID, branch, branchReuseFingerprint(localSHA, refs, occupied)}, ServiceID: service.ID, ServiceName: service.DisplayName, ModuleName: display, LocalExists: localSHA != "", RemoteRefs: stripRemoteRefHashes(refs), OccupiedWorktreePaths: paths, LockedWorktreePaths: locked, Strategy: domain.WorkspaceStandardWorktree, RequiresForceAttach: len(paths) > 0}, nil
}

func stripRemoteRefHashes(refs []string) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		out = append(out, ref[:strings.LastIndex(ref, "@")])
	}
	return out
}
func (i *WorkspaceBranchReuseInspector) remoteHeadSHA(ctx context.Context, repository domain.RepositoryConfig, remote, branch string) (string, error) {
	url, err := sourceRemoteURL(i.Git, repository, remote)
	if err != nil {
		return "", err
	}
	result, err := i.Git.RunContext(ctx, repository.RootPath, 5*time.Minute, false, false, "ls-remote", "--exit-code", "--heads", url, "refs/heads/"+branch)
	if err != nil {
		return "", err
	}
	if result.ExitCode == 2 {
		return "", errRemoteBranchMissing
	}
	if result.ExitCode != 0 {
		return "", &GitException{Message: fmt.Sprintf("检查远程分支失败：%s/%s", remote, branch), Result: result}
	}
	for _, line := range strings.Split(result.Stdout, "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(strings.SplitN(line, "\t", 2)[0]), nil
		}
	}
	return "", fmt.Errorf("远程分支不存在：%s/%s", remote, branch)
}

func sourceRemoteURL(git *GitClient, repository domain.RepositoryConfig, remote string) (string, error) {
	url, err := git.RemoteURL(repository.RootPath, remote)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(url) != "" {
		return strings.TrimSpace(url), nil
	}
	if remote == "origin" && repository.OriginURL != nil && strings.TrimSpace(*repository.OriginURL) != "" {
		return strings.TrimSpace(*repository.OriginURL), nil
	}
	return "", fmt.Errorf("仓库未配置可用于独立克隆的远程：%s", remote)
}

type StandardWorktreeProvisioner struct {
	Git       *GitClient
	Bootstrap *BootstrapService
	Locks     *OperationLocks
}

func (p *StandardWorktreeProvisioner) Strategy() domain.WorkspaceStrategy {
	return domain.WorkspaceStandardWorktree
}
func (p *StandardWorktreeProvisioner) Provision(ctx context.Context, request WorkspaceProvisionRequest, onOutput func(process.StreamEvent)) ([]domain.ServiceWorkspace, error) {
	return p.ProvisionWithProgress(ctx, request, onOutput, nil)
}

func (p *StandardWorktreeProvisioner) ProvisionWithProgress(ctx context.Context, request WorkspaceProvisionRequest, onOutput func(process.StreamEvent), onProgress func(TaskCreationProgress)) ([]domain.ServiceWorkspace, error) {
	checking := TaskCreationProgress{Phase: TaskCreationPhaseChecking, ServiceID: request.Service.ID, ServiceName: request.Service.DisplayName}
	root := ""
	common := ""
	if err := runTaskCreationProgress(onProgress, checking, "正在检查仓库路径", "仓库路径检查完成", func() error {
		var err error
		root, err = filepath.Abs(request.Repository.RootPath)
		if err != nil {
			return err
		}
		common, err = p.Git.CommonDirectory(root)
		return err
	}); err != nil {
		return nil, err
	}
	var result []domain.ServiceWorkspace
	err := p.Locks.WithRepository(common, func() error {
		var inner error
		result, inner = p.provisionLocked(ctx, request, root, onOutput, onProgress)
		return inner
	})
	return result, err
}
func (p *StandardWorktreeProvisioner) provisionLocked(ctx context.Context, request WorkspaceProvisionRequest, root string, onOutput func(process.StreamEvent), onProgress func(TaskCreationProgress)) ([]domain.ServiceWorkspace, error) {
	checking := TaskCreationProgress{Phase: TaskCreationPhaseChecking, ServiceID: request.Service.ID, ServiceName: request.Service.DisplayName}
	var branches map[string]string
	if err := runTaskCreationProgress(onProgress, checking, "正在复核仓库身份和模块分支", "仓库身份和模块分支检查完成", func() error {
		top, err := p.Git.TopLevel(root)
		if err != nil {
			return err
		}
		top, _ = canonicalPath(top)
		expected, _ := canonicalPath(root)
		if top != expected {
			return fmt.Errorf("配置的仓库路径已不再是 Git 顶层目录：%s", root)
		}
		common, err := p.Git.CommonDirectory(root)
		if err != nil {
			return err
		}
		common, _ = canonicalPath(common)
		expectedCommon, _ := canonicalPath(request.Repository.GitCommonDirectory)
		if common != expectedCommon {
			return fmt.Errorf("仓库 Git 身份已变化，请先手动刷新并检查配置")
		}
		if err := p.Git.PruneWorktrees(root); err != nil {
			return err
		}
		if err := os.MkdirAll(request.TaskDirectory, 0o755); err != nil {
			return err
		}
		branches, err = resolveModuleTargets(request)
		if err != nil {
			return err
		}
		if err := requireNoBranchPathConflicts(values(branches)); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return nil, err
	}
	created := []domain.ServiceWorkspace{}
	for _, module := range request.Service.Modules {
		preparing := taskCreationProgressForModule(request.Service, module, TaskCreationPhasePreparingWorktree)
		emitTaskCreationProgress(onProgress, TaskCreationProgress{Phase: preparing.Phase, Status: TaskCreationProgressStarted, ServiceID: preparing.ServiceID, ServiceName: preparing.ServiceName, ModuleID: preparing.ModuleID, ModuleName: preparing.ModuleName, Message: "正在准备 Worktree"})
		failPreparation := func(cause error) ([]domain.ServiceWorkspace, error) {
			failed := preparing
			failed.Status = TaskCreationProgressFailed
			failed.Message = cause.Error()
			emitTaskCreationProgress(onProgress, failed)
			return rollbackStandardWithProgress(ctx, p, request, created, cause, onProgress)
		}
		branch := branches[module.ID]
		if branch == "" {
			return failPreparation(fmt.Errorf("Worktree 模块目标分支不能为空：%s", module.Name))
		}
		directory, err := moduleDirectoryName(request.Service, module)
		if err != nil {
			return failPreparation(err)
		}
		target := filepath.Clean(filepath.Join(request.TaskDirectory, directory))
		taskAbs, _ := filepath.Abs(request.TaskDirectory)
		targetAbs, _ := filepath.Abs(target)
		if filepath.Dir(targetAbs) != filepath.Clean(taskAbs) {
			return failPreparation(fmt.Errorf("Worktree 必须位于任务目录的直接子级"))
		}
		baseRef := moduleMasterBranch(request.Service, module)
		base, err := ParseRemoteBranchRef(baseRef)
		if err != nil {
			return failPreparation(err)
		}
		baseBranch := normalizeBaseRef(baseRef)
		remoteBaseRef := base.Remote + "/" + baseBranch
		refresh := taskCreationProgressForModule(request.Service, module, TaskCreationPhaseRefreshingRemote)
		err = runTaskCreationProgress(onProgress, refresh, "正在刷新远程主分支", "远程主分支已刷新", func() error {
			if err := p.Git.Fetch(root, base.Remote); err != nil {
				return err
			}
			exists, err := p.Git.RefExists(root, "refs/remotes/"+remoteBaseRef)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("远程主分支不存在：%s", remoteBaseRef)
			}
			return nil
		})
		if err != nil {
			return failPreparation(err)
		}
		validator := NewBranchReferenceValidator(p.Git)
		if !validator.IsValid(branch) {
			return failPreparation(fmt.Errorf("分支名不合法：%s", branch))
		}
		localResult, err := p.Git.RunContext(ctx, root, 2*time.Minute, false, false, "rev-parse", "--verify", "refs/heads/"+branch)
		if err != nil {
			return failPreparation(err)
		}
		localSHA := ""
		if localResult.ExitCode == 0 {
			localSHA = strings.TrimSpace(localResult.Stdout)
		}
		localExists := localSHA != ""
		remoteRef := base.Remote + "/" + branch
		remoteResult, err := p.Git.RunContext(ctx, root, 2*time.Minute, false, false, "rev-parse", "--verify", "refs/remotes/"+remoteRef)
		if err != nil {
			return failPreparation(err)
		}
		remoteRefs := []string{}
		if remoteResult.ExitCode == 0 {
			remoteRefs = append(remoteRefs, remoteRef+"@"+strings.TrimSpace(remoteResult.Stdout))
		}
		worktrees, err := p.Git.Worktrees(root)
		if err != nil {
			return failPreparation(err)
		}
		var matching []WorktreeRecord
		if localExists {
			for _, wt := range worktrees {
				if wt.Branch == branch && filepath.Clean(wt.Path) != target {
					matching = append(matching, wt)
				}
			}
		}
		key := BranchReuseKey{RepositoryID: request.Repository.ID, Branch: branch, StateFingerprint: branchReuseFingerprint(localSHA, remoteRefs, matching)}
		if (localExists || len(remoteRefs) > 0) && !confirmedReuse(request, key) {
			return failPreparation(fmt.Errorf("分支复用状态已变化或尚未确认，请重新预检：%s", branch))
		}
		for _, wt := range matching {
			if wt.Locked {
				return failPreparation(fmt.Errorf("分支被锁定 Worktree 占用，不能强制复用：%s", wt.Path))
			}
		}
		forceAttach := len(matching) > 0
		switch {
		case localExists:
			err = p.Git.AddExistingWorktree(root, target, branch, forceAttach)
		case len(remoteRefs) > 0:
			err = p.Git.AddTrackedRemoteWorktree(root, target, branch, base.Remote)
		default:
			err = p.Git.AddWorktree(root, target, branch, remoteBaseRef, base.Remote)
		}
		if err != nil {
			return failPreparation(err)
		}
		completedPreparation := preparing
		completedPreparation.Status = TaskCreationProgressCompleted
		completedPreparation.Message = "Worktree 已准备"
		emitTaskCreationProgress(onProgress, completedPreparation)
		source := domain.TaskModuleConfigured
		if v, ok := request.ModuleSources[module.ID]; ok {
			source = v
		}
		moduleName := request.ModuleDisplayNames[module.ID]
		if moduleName == "" {
			names, _ := resolveModuleDisplayNames(request)
			moduleName = names[module.ID]
		}
		workspace := domain.ServiceWorkspace{RepositoryID: request.Repository.ID, ServiceName: request.Service.DisplayName, RepositoryPath: request.Repository.RootPath, WorktreePath: target, DevelopmentTool: request.Service.DevelopmentTool, Branch: branch, Health: domain.WorkspaceReady, GroupServiceID: request.Service.ID, ModuleID: module.ID, ModuleName: moduleName, Strategy: domain.WorkspaceStandardWorktree, ModuleSource: source, OriginURL: request.Repository.OriginURL, BaseRef: ptr(remoteBaseRef), TargetBranch: ptr(branch), TagEnabled: module.TagEnabled, TagMode: module.TagMode, TagTargetRef: optionalPtr(moduleEffectiveTagTarget(request.Service, module)), TagMessagePrefix: module.TagMessagePrefix, BranchCreatedByTask: !localExists, ForceWorktreeAttach: forceAttach, PushRemote: base.Remote}
		created = append(created, workspace)
		var initialized BootstrapResult
		initializing := taskCreationProgressForModule(request.Service, module, TaskCreationPhaseInitializingWorkspace)
		emitTaskCreationProgress(onProgress, TaskCreationProgress{Phase: initializing.Phase, Status: TaskCreationProgressStarted, ServiceID: initializing.ServiceID, ServiceName: initializing.ServiceName, ModuleID: initializing.ModuleID, ModuleName: initializing.ModuleName, Message: "正在初始化工作区"})
		initialized, bootstrapErr := p.Bootstrap.Initialize(ctx, root, target, request.Service.Bootstrap, onOutput)
		if bootstrapErr != nil {
			failed := initializing
			failed.Status = TaskCreationProgressFailed
			failed.Message = bootstrapErr.Error()
			emitTaskCreationProgress(onProgress, failed)
			return rollbackStandardWithProgress(ctx, p, request, created, bootstrapErr, onProgress)
		}
		initializedEvent := initializing
		initializedEvent.Status = TaskCreationProgressCompleted
		initializedEvent.Message = "工作区初始化已返回"
		if !initialized.Succeeded() {
			initializedEvent.Message = "工作区初始化已返回，存在警告，请查看工作区状态"
		}
		emitTaskCreationProgress(onProgress, initializedEvent)
		workspace = created[len(created)-1]
		workspace.Warnings = initialized.Warnings
		if !initialized.Succeeded() {
			workspace.Health = domain.WorkspaceReadyWithWarnings
		}
		created[len(created)-1] = workspace
	}
	return created, nil
}
func rollbackStandard(ctx context.Context, p *StandardWorktreeProvisioner, request WorkspaceProvisionRequest, workspaces []domain.ServiceWorkspace, cause error) ([]domain.ServiceWorkspace, error) {
	return rollbackStandardWithProgress(ctx, p, request, workspaces, cause, nil)
}

func rollbackStandardWithProgress(ctx context.Context, p *StandardWorktreeProvisioner, request WorkspaceProvisionRequest, workspaces []domain.ServiceWorkspace, cause error, onProgress func(TaskCreationProgress)) ([]domain.ServiceWorkspace, error) {
	// Compensation must finish after the creating request is cancelled. Each Git
	// cleanup command retains its own timeout.
	ctx = context.WithoutCancel(ctx)
	for i := len(workspaces) - 1; i >= 0; i-- {
		ws := workspaces[i]
		event := taskCreationProgressForWorkspace(ws, TaskCreationPhaseRollback)
		emitTaskCreationProgress(onProgress, TaskCreationProgress{Phase: event.Phase, Status: TaskCreationProgressStarted, ServiceID: event.ServiceID, ServiceName: event.ServiceName, ModuleID: event.ModuleID, ModuleName: event.ModuleName, Message: "正在回滚模块工作区"})
		var cleanupErr error
		var progressFailure error
		if err := p.Git.RemoveWorktree(request.Repository.RootPath, ws.WorktreePath, true); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
			progressFailure = errors.Join(progressFailure, err)
		}
		if ws.BranchCreatedByTask {
			result, err := p.Git.RunContext(ctx, request.Repository.RootPath, 2*time.Minute, false, false, "branch", "-D", ws.Branch)
			if err != nil {
				cleanupErr = errors.Join(cleanupErr, err)
				progressFailure = errors.Join(progressFailure, err)
			} else if err := gitCleanupProgressFailure("删除任务分支 "+ws.Branch, result); err != nil {
				progressFailure = errors.Join(progressFailure, err)
			}
		}
		if progressFailure != nil {
			failed := event
			failed.Status = TaskCreationProgressFailed
			failed.Message = progressFailure.Error()
			emitTaskCreationProgress(onProgress, failed)
		} else {
			completed := event
			completed.Status = TaskCreationProgressCompleted
			completed.Message = "模块工作区已回滚"
			emitTaskCreationProgress(onProgress, completed)
		}
		if cleanupErr != nil {
			cause = errors.Join(cause, cleanupErr)
		}
	}
	_, _ = p.Git.RunContext(ctx, request.Repository.RootPath, 2*time.Minute, false, false, "worktree", "prune")
	return nil, cause
}
func (p *StandardWorktreeProvisioner) Rollback(ctx context.Context, request WorkspaceProvisionRequest, workspaces []domain.ServiceWorkspace) error {
	return p.RollbackWithProgress(ctx, request, workspaces, nil)
}

func (p *StandardWorktreeProvisioner) RollbackWithProgress(ctx context.Context, request WorkspaceProvisionRequest, workspaces []domain.ServiceWorkspace, onProgress func(TaskCreationProgress)) error {
	ctx = context.WithoutCancel(ctx)
	root := request.Repository.RootPath
	events := make([]TaskCreationProgress, len(workspaces))
	for i := len(workspaces) - 1; i >= 0; i-- {
		events[i] = taskCreationProgressForWorkspace(workspaces[i], TaskCreationPhaseRollback)
		emitTaskCreationProgress(onProgress, TaskCreationProgress{Phase: events[i].Phase, Status: TaskCreationProgressStarted, ServiceID: events[i].ServiceID, ServiceName: events[i].ServiceName, ModuleID: events[i].ModuleID, ModuleName: events[i].ModuleName, Message: "正在回滚模块工作区"})
	}
	common, err := p.Git.CommonDirectory(root)
	if err != nil {
		for i := len(workspaces) - 1; i >= 0; i-- {
			failed := events[i]
			failed.Status = TaskCreationProgressFailed
			failed.Message = err.Error()
			emitTaskCreationProgress(onProgress, failed)
		}
		return err
	}
	entered := false
	err = p.Locks.WithRepository(common, func() error {
		entered = true
		var failure error
		progressFailures := make([]error, len(workspaces))
		for i := len(workspaces) - 1; i >= 0; i-- {
			ws := workspaces[i]
			target, _ := filepath.Abs(ws.WorktreePath)
			task, _ := filepath.Abs(request.TaskDirectory)
			if filepath.Dir(target) != filepath.Clean(task) {
				cleanupErr := fmt.Errorf("拒绝回滚任务目录之外的 Worktree：%s", target)
				failure = errors.Join(failure, cleanupErr)
				progressFailures[i] = errors.Join(progressFailures[i], cleanupErr)
				continue
			}
			if err := p.Git.RemoveWorktree(root, target, true); err != nil {
				failure = errors.Join(failure, err)
				progressFailures[i] = errors.Join(progressFailures[i], err)
			}
			if ws.BranchCreatedByTask {
				result, err := p.Git.RunContext(ctx, root, 2*time.Minute, false, false, "branch", "-D", ws.Branch)
				if err != nil {
					failure = errors.Join(failure, err)
					progressFailures[i] = errors.Join(progressFailures[i], err)
				} else if err := gitCleanupProgressFailure("删除任务分支 "+ws.Branch, result); err != nil {
					progressFailures[i] = errors.Join(progressFailures[i], err)
				}
			}
		}
		if _, err := p.Git.RunContext(ctx, root, 2*time.Minute, false, false, "worktree", "prune"); err != nil {
			failure = errors.Join(failure, err)
			for i := range progressFailures {
				progressFailures[i] = errors.Join(progressFailures[i], err)
			}
		}
		for i := len(workspaces) - 1; i >= 0; i-- {
			if progressFailures[i] != nil {
				failed := events[i]
				failed.Status = TaskCreationProgressFailed
				failed.Message = progressFailures[i].Error()
				emitTaskCreationProgress(onProgress, failed)
			} else {
				completed := events[i]
				completed.Status = TaskCreationProgressCompleted
				completed.Message = "模块工作区已回滚"
				emitTaskCreationProgress(onProgress, completed)
			}
		}
		return failure
	})
	if err != nil && !entered {
		for i := len(workspaces) - 1; i >= 0; i-- {
			failed := events[i]
			failed.Status = TaskCreationProgressFailed
			failed.Message = err.Error()
			emitTaskCreationProgress(onProgress, failed)
		}
	}
	return err
}

type IndependentCloneProvisioner struct {
	Git       *GitClient
	Bootstrap *BootstrapService
}

func (p *IndependentCloneProvisioner) Strategy() domain.WorkspaceStrategy {
	return domain.WorkspaceIndependentClone
}
func (p *IndependentCloneProvisioner) Provision(ctx context.Context, request WorkspaceProvisionRequest, onOutput func(process.StreamEvent)) ([]domain.ServiceWorkspace, error) {
	return p.ProvisionWithProgress(ctx, request, onOutput, nil)
}

func (p *IndependentCloneProvisioner) ProvisionWithProgress(ctx context.Context, request WorkspaceProvisionRequest, onOutput func(process.StreamEvent), onProgress func(TaskCreationProgress)) ([]domain.ServiceWorkspace, error) {
	created := []domain.ServiceWorkspace{}
	for _, module := range request.Service.Modules {
		fail := func(cause error) ([]domain.ServiceWorkspace, error) {
			return rollbackClonesWithProgress(request, created, p, cause, onProgress)
		}
		checking := taskCreationProgressForModule(request.Service, module, TaskCreationPhaseChecking)
		emitTaskCreationProgress(onProgress, TaskCreationProgress{Phase: checking.Phase, Status: TaskCreationProgressStarted, ServiceID: checking.ServiceID, ServiceName: checking.ServiceName, ModuleID: checking.ModuleID, ModuleName: checking.ModuleName, Message: "正在检查克隆配置和目标目录"})
		failChecking := func(cause error) ([]domain.ServiceWorkspace, error) {
			failed := checking
			failed.Status = TaskCreationProgressFailed
			failed.Message = cause.Error()
			emitTaskCreationProgress(onProgress, failed)
			return fail(cause)
		}
		baseValue := moduleMasterBranch(request.Service, module)
		base, err := ParseRemoteBranchRef(baseValue)
		if err != nil {
			return failChecking(err)
		}
		sourceURL, err := sourceRemoteURL(p.Git, request.Repository, base.Remote)
		if err != nil {
			return failChecking(err)
		}
		targetBranch := strings.TrimSpace(request.ModuleBranches[module.ID])
		if targetBranch == "" {
			targetBranch = ""
		}
		directory, err := moduleDirectoryName(request.Service, module)
		if err != nil {
			return failChecking(err)
		}
		target := filepath.Clean(filepath.Join(request.TaskDirectory, directory))
		taskAbs, _ := filepath.Abs(request.TaskDirectory)
		targetAbs, _ := filepath.Abs(target)
		if filepath.Dir(targetAbs) != filepath.Clean(taskAbs) {
			return failChecking(fmt.Errorf("独立克隆必须位于任务目录的直接子级"))
		}
		if _, err := os.Lstat(target); err == nil {
			return failChecking(fmt.Errorf("目标目录已存在：%s", target))
		}
		checked := checking
		checked.Status = TaskCreationProgressCompleted
		checked.Message = "克隆配置和目标目录检查完成"
		emitTaskCreationProgress(onProgress, checked)
		targetSHA := ""
		refresh := taskCreationProgressForModule(request.Service, module, TaskCreationPhaseRefreshingRemote)
		err = runTaskCreationProgress(onProgress, refresh, "正在检查远程主分支和目标分支", "远程分支检查完成", func() error {
			if _, err := p.remoteHeadSHA(ctx, request.Repository, base.Remote, base.Branch); errors.Is(err, errRemoteBranchMissing) {
				return fmt.Errorf("远程主分支不存在：%s", FormatRemoteBranchRef(base))
			} else if err != nil {
				return err
			}
			if targetBranch != "" {
				targetSHA, err = p.remoteHeadSHA(ctx, request.Repository, base.Remote, targetBranch)
				if errors.Is(err, errRemoteBranchMissing) {
					err = nil
				}
				return err
			}
			return nil
		})
		if err != nil {
			return fail(err)
		}
		ownership := cloneOwnership(request.TaskDirectory, request.Repository.ID, request.Service.ID, module.ID)
		cloning := taskCreationProgressForModule(request.Service, module, TaskCreationPhaseCloningRepository)
		err = runTaskCreationProgress(onProgress, cloning, "正在克隆仓库并准备目标分支", "仓库克隆完成", func() error {
			if targetBranch != "" && targetSHA == "" {
				for _, confirmed := range request.ConfirmedBranchReuseKeys {
					if confirmed.RepositoryID == request.Repository.ID && confirmed.Branch == targetBranch && strings.HasPrefix(confirmed.StateFingerprint, "clone-remote=") {
						return fmt.Errorf("远程目标分支复用状态已变化，请重新预检：%s/%s", base.Remote, targetBranch)
					}
				}
			}
			return cloneIntoPlace(request.TaskDirectory, target, ownership, func(staging string) error {
				if targetSHA != "" {
					key := BranchReuseKey{request.Repository.ID, targetBranch, fmt.Sprintf("clone-remote=%s/%s@%s", base.Remote, targetBranch, targetSHA)}
					if !confirmedReuse(request, key) {
						return fmt.Errorf("远程目标分支复用尚未确认或状态已变化，请重新预检：%s/%s", base.Remote, targetBranch)
					}
					if err := p.Git.CloneRepository(ctx, sourceURL, staging, targetBranch); err != nil {
						return err
					}
					head, err := p.Git.Resolve(staging, "HEAD")
					if err != nil {
						return err
					}
					if head != targetSHA {
						return fmt.Errorf("远程目标分支复用状态已变化，请重新预检：%s/%s", base.Remote, targetBranch)
					}
					return nil
				}
				if err := p.Git.CloneRepository(ctx, sourceURL, staging, base.Branch); err != nil {
					return err
				}
				if targetBranch != "" {
					if !NewBranchReferenceValidator(p.Git).IsValid(targetBranch) {
						return fmt.Errorf("目标分支名不合法：%s", targetBranch)
					}
					_, err := p.Git.RunContext(ctx, staging, 5*time.Minute, true, false, "switch", "-c", targetBranch)
					return err
				}
				return nil
			})
		})
		if err != nil {
			return fail(err)
		}
		moduleName := request.ModuleDisplayNames[module.ID]
		if moduleName == "" {
			names, _ := resolveModuleDisplayNames(request)
			moduleName = names[module.ID]
		}
		source := domain.TaskModuleConfigured
		if v, ok := request.ModuleSources[module.ID]; ok {
			source = v
		}
		branch := targetBranch
		if branch == "" {
			branch = base.Branch
		}
		ws := domain.ServiceWorkspace{RepositoryID: request.Repository.ID, ServiceName: request.Service.DisplayName, RepositoryPath: target, WorktreePath: target, DevelopmentTool: request.Service.DevelopmentTool, Branch: branch, Health: domain.WorkspaceReady, GroupServiceID: request.Service.ID, ModuleID: module.ID, ModuleName: moduleName, Strategy: domain.WorkspaceIndependentClone, ModuleSource: source, OriginURL: ptr(sourceURL), BaseRef: ptr(baseValue), TargetBranch: optionalPtr(targetBranch), TagEnabled: module.TagEnabled, TagMode: module.TagMode, TagTargetRef: optionalPtr(moduleEffectiveTagTarget(request.Service, module)), TagMessagePrefix: module.TagMessagePrefix, PushRemote: "origin"}
		created = append(created, ws)
		initializing := taskCreationProgressForModule(request.Service, module, TaskCreationPhaseInitializingWorkspace)
		emitTaskCreationProgress(onProgress, TaskCreationProgress{Phase: initializing.Phase, Status: TaskCreationProgressStarted, ServiceID: initializing.ServiceID, ServiceName: initializing.ServiceName, ModuleID: initializing.ModuleID, ModuleName: initializing.ModuleName, Message: "正在初始化工作区"})
		initialized, bootstrapErr := p.Bootstrap.Initialize(ctx, request.Repository.RootPath, target, request.Service.Bootstrap, onOutput)
		if bootstrapErr != nil {
			failed := initializing
			failed.Status = TaskCreationProgressFailed
			failed.Message = bootstrapErr.Error()
			emitTaskCreationProgress(onProgress, failed)
			return rollbackClonesWithProgress(request, created, p, bootstrapErr, onProgress)
		}
		initializedEvent := initializing
		initializedEvent.Status = TaskCreationProgressCompleted
		initializedEvent.Message = "工作区初始化已返回"
		if !initialized.Succeeded() {
			created[len(created)-1].Health = domain.WorkspaceReadyWithWarnings
			initializedEvent.Message = "工作区初始化已返回，存在警告，请查看工作区状态"
		}
		emitTaskCreationProgress(onProgress, initializedEvent)
		created[len(created)-1].Warnings = initialized.Warnings
	}
	return created, nil
}
func (p *IndependentCloneProvisioner) remoteHeadSHA(ctx context.Context, repository domain.RepositoryConfig, remote, branch string) (string, error) {
	url, err := sourceRemoteURL(p.Git, repository, remote)
	if err != nil {
		return "", err
	}
	result, err := p.Git.RunContext(ctx, repository.RootPath, 5*time.Minute, false, false, "ls-remote", "--exit-code", "--heads", url, "refs/heads/"+branch)
	if err != nil {
		return "", err
	}
	if result.ExitCode == 2 {
		return "", errRemoteBranchMissing
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("检查远程分支失败：%s/%s", remote, branch)
	}
	if line := nonEmptyLines(result.Stdout); len(line) > 0 {
		return strings.TrimSpace(strings.SplitN(line[0], "\t", 2)[0]), nil
	}
	return "", fmt.Errorf("远程分支不存在：%s/%s", remote, branch)
}
func rollbackClones(request WorkspaceProvisionRequest, workspaces []domain.ServiceWorkspace, p *IndependentCloneProvisioner, cause error) ([]domain.ServiceWorkspace, error) {
	return rollbackClonesWithProgress(request, workspaces, p, cause, nil)
}

func rollbackClonesWithProgress(request WorkspaceProvisionRequest, workspaces []domain.ServiceWorkspace, p *IndependentCloneProvisioner, cause error, onProgress func(TaskCreationProgress)) ([]domain.ServiceWorkspace, error) {
	for i := len(workspaces) - 1; i >= 0; i-- {
		ws := workspaces[i]
		event := taskCreationProgressForWorkspace(ws, TaskCreationPhaseRollback)
		emitTaskCreationProgress(onProgress, TaskCreationProgress{Phase: event.Phase, Status: TaskCreationProgressStarted, ServiceID: event.ServiceID, ServiceName: event.ServiceName, ModuleID: event.ModuleID, ModuleName: event.ModuleName, Message: "正在回滚独立克隆"})
		moduleID := ws.ModuleID
		ownership := cloneOwnership(request.TaskDirectory, request.Repository.ID, request.Service.ID, moduleID)
		if err := deleteOwnedClone(request.TaskDirectory, ws.WorktreePath, ownership); err != nil {
			failed := event
			failed.Status = TaskCreationProgressFailed
			failed.Message = err.Error()
			emitTaskCreationProgress(onProgress, failed)
			cause = errors.Join(cause, err)
			continue
		}
		completed := event
		completed.Status = TaskCreationProgressCompleted
		completed.Message = "独立克隆已回滚"
		emitTaskCreationProgress(onProgress, completed)
	}
	return nil, cause
}
func (p *IndependentCloneProvisioner) Rollback(ctx context.Context, request WorkspaceProvisionRequest, workspaces []domain.ServiceWorkspace) error {
	var failure error
	for i := len(workspaces) - 1; i >= 0; i-- {
		ws := workspaces[i]
		target, _ := filepath.Abs(ws.WorktreePath)
		task, _ := filepath.Abs(request.TaskDirectory)
		if filepath.Dir(target) != filepath.Clean(task) {
			failure = errors.Join(failure, fmt.Errorf("拒绝删除任务目录之外的独立克隆：%s", target))
			continue
		}
		if err := deleteOwnedClone(request.TaskDirectory, target, cloneOwnership(request.TaskDirectory, request.Repository.ID, request.Service.ID, ws.ModuleID)); err != nil {
			failure = errors.Join(failure, err)
		}
	}
	return failure
}

func normalizeBaseRef(value string) string {
	ref, err := ParseRemoteBranchRef(value)
	if err != nil {
		return strings.TrimSpace(value)
	}
	branch := ref.Branch
	branch = strings.TrimPrefix(branch, "refs/heads/")
	return branch
}
func requireNoBranchPathConflicts(branches []string) error {
	seen := map[string]string{}
	for _, branch := range branches {
		if strings.TrimSpace(branch) == "" {
			continue
		}
		key := strings.ToLower(branch)
		if prior := seen[key]; prior != "" {
			return fmt.Errorf("同一仓库的模块目标分支不能重复：%s", branch)
		}
		seen[key] = branch
	}
	for branch := range seen {
		for i, r := range branch {
			if r == '/' {
				parent := branch[:i]
				if prior := seen[parent]; prior != "" {
					return fmt.Errorf("同一仓库的模块目标分支不能互为父子路径：%s 与 %s", prior, seen[branch])
				}
			}
		}
	}
	return nil
}
func values(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
func cloneOwnership(taskDirectory, repositoryID, serviceID, moduleID string) string {
	identity := strings.Join([]string{filepath.Clean(taskDirectory), repositoryID, serviceID, moduleID}, "\x00")
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])
}
func cloneMarker(path string) string { return filepath.Join(path, ".git", "silverwing-owner") }
func cloneIntoPlace(taskDirectory, target, ownership string, clone func(staging string) error) error {
	task, _ := filepath.Abs(taskDirectory)
	targetAbs, _ := filepath.Abs(target)
	if filepath.Dir(targetAbs) != filepath.Clean(task) {
		return fmt.Errorf("独立克隆必须位于任务目录的直接子级")
	}
	if err := os.MkdirAll(task, 0o755); err != nil {
		return err
	}
	if _, err := os.Lstat(targetAbs); err == nil {
		return fmt.Errorf("目标目录已存在：%s", targetAbs)
	}
	stagingRoot, err := os.MkdirTemp(task, ".silverwing-clone-")
	if err != nil {
		return err
	}
	ownerFile := filepath.Join(stagingRoot, ".silverwing-owner")
	if err = os.WriteFile(ownerFile, []byte(ownership), 0o600); err != nil {
		_ = os.RemoveAll(stagingRoot)
		return err
	}
	staging := filepath.Join(stagingRoot, "workspace")
	if err = clone(staging); err == nil {
		err = os.MkdirAll(filepath.Dir(cloneMarker(staging)), 0o755)
	}
	if err == nil {
		err = os.WriteFile(cloneMarker(staging), []byte(ownership), 0o600)
	}
	if err == nil {
		err = os.Rename(staging, targetAbs)
	}
	if err != nil {
		if _, statErr := os.Lstat(targetAbs); statErr == nil {
			_ = deleteOwnedClone(task, targetAbs, ownership)
		}
		_ = os.RemoveAll(stagingRoot)
		return err
	}
	_ = os.Remove(ownerFile)
	_ = os.Remove(stagingRoot)
	return nil
}
func requireOwnedClone(target, ownership string) error {
	marker := cloneMarker(target)
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != ownership {
		return fmt.Errorf("独立克隆所有权标记缺失或已变化：%s", target)
	}
	return nil
}
func deleteOwnedClone(taskDirectory, target, ownership string) error {
	task, _ := filepath.Abs(taskDirectory)
	targetAbs, _ := filepath.Abs(target)
	if filepath.Dir(targetAbs) != filepath.Clean(task) {
		return fmt.Errorf("拒绝删除任务目录之外的独立克隆：%s", targetAbs)
	}
	if _, err := os.Lstat(targetAbs); os.IsNotExist(err) {
		return nil
	}
	if err := requireOwnedClone(targetAbs, ownership); err != nil {
		return err
	}
	return os.RemoveAll(targetAbs)
}
func ptr(value string) *string { return &value }
func optionalPtr(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	v := value
	return &v
}
