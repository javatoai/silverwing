package production

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

type Git interface {
	RunContext(context.Context, string, time.Duration, bool, bool, ...string) (process.Result, error)
}

type Service struct {
	Paths  storage.Paths
	Config *storage.ConfigStore
	Git    Git
	Genbu  Genbu
	Runner Runner
	Store  Store
	locks  *workspace.OperationLocks
}

func New(paths storage.Paths, config *storage.ConfigStore, git Git, runner Runner) *Service {
	if client, ok := git.(*workspace.GitClient); ok {
		owned := *client
		owned.RemoveEnvironment = append(append([]string{}, client.RemoveEnvironment...), gitScopeEnvironment...)
		git = &owned
	}
	return &Service{Paths: paths, Config: config, Git: git, Runner: runner, Genbu: CLIGenbu{runner}, Store: Store{Directory: filepath.Join(paths.Home, "production-releases")}, locks: workspace.NewOperationLocks(paths.Locks)}
}

func digest(value any) string {
	data, _ := json.Marshal(value)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (s *Service) run(ctx context.Context, repository string, args ...string) (string, error) {
	r, err := s.Git.RunContext(ctx, repository, 2*time.Minute, true, false, args...)
	return strings.TrimSpace(r.Stdout), err
}

func (s *Service) resolve(ctx context.Context, selection Selection) (Target, domain.AppConfig, error) {
	c, err := s.Config.Load()
	if err != nil {
		return Target{}, c, err
	}
	if !c.TagEnabled {
		return Target{}, c, errors.New("全局 Tag 已关闭")
	}
	var service *domain.GroupServiceConfig
	for _, group := range c.Groups {
		if group.ID != selection.GroupID {
			continue
		}
		for _, item := range group.Services {
			if item.ID == selection.ServiceID {
				copy := item
				service = &copy
				break
			}
		}
	}
	if service == nil || !service.Enabled {
		return Target{}, c, errors.New("服务已移除或停用")
	}
	var repository *domain.RepositoryConfig
	for _, item := range c.Repositories {
		if item.ID == service.RepositoryID {
			copy := item
			repository = &copy
			break
		}
	}
	if repository == nil {
		return Target{}, c, errors.New("服务仓库配置不存在")
	}
	ref, err := domain.ParseRemoteBranchRef(service.MasterBranch)
	if err != nil {
		return Target{}, c, err
	}
	if strings.HasPrefix(ref.Remote, "-") || strings.HasPrefix(ref.Branch, "-") {
		return Target{}, c, errors.New("主分支配置无效")
	}
	root, err := filepath.Abs(repository.RootPath)
	if err != nil {
		return Target{}, c, err
	}
	if real, e := filepath.EvalSymlinks(root); e == nil {
		root = real
	}
	common, err := s.run(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return Target{}, c, err
	}
	common, err = filepath.Abs(common)
	if err != nil {
		return Target{}, c, err
	}
	if real, e := filepath.EvalSymlinks(common); e == nil {
		common = real
	}
	remoteURL, err := s.run(ctx, root, "remote", "get-url", ref.Remote)
	if err != nil {
		return Target{}, c, err
	}
	pushURLs, err := s.run(ctx, root, "remote", "get-url", "--push", "--all", ref.Remote)
	if err != nil {
		return Target{}, c, err
	}
	if strings.ContainsAny(pushURLs, "\r\n") || repositoryEndpoint(root, remoteURL) != repositoryEndpoint(root, pushURLs) {
		return Target{}, c, errors.New("生产发布要求远端只推送到同一仓库，请核对 fetch／push URL 配置")
	}
	t := Target{Selection: selection, ServiceName: service.DisplayName, RepositoryID: repository.ID, Repository: root, CommonDirectory: common, Remote: ref.Remote, MainBranch: ref.Branch, RemoteIdentity: digest([]string{remoteURL, pushURLs}), GenbuService: service.GenbuServiceName, IDE: string(service.DevelopmentTool)}
	t.ConfigIdentity = digest([]any{s.Paths.Home, selection, repository.ID, root, common, t.Remote, t.MainBranch, t.RemoteIdentity, service.GenbuServiceName, service.GenbuProbeEnabled, service.DevelopmentTool,
		c.GitExecutablePath, c.GenbuExecutablePath, c.CommandProxyURL, c.CommandProxyTargets, c.CommandProxyUsername, c.CommandProxyPassword, c.CommandProxyNoProxy, c.BlockedGitWriteBranches})
	return t, c, nil
}

var gitScopeEnvironment = []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_NAMESPACE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES"}

func repositoryEndpoint(repository, address string) string {
	if u, err := url.Parse(address); err == nil && u.Host != "" {
		return strings.ToLower(u.Host) + "/" + strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	}
	if at := strings.IndexByte(address, '@'); at >= 0 {
		if colon := strings.IndexByte(address[at+1:], ':'); colon >= 0 {
			return strings.ToLower(address[at+1:at+1+colon]) + "/" + strings.TrimSuffix(strings.Trim(address[at+colon+2:], "/"), ".git")
		}
	}
	if !filepath.IsAbs(address) {
		address = filepath.Join(repository, address)
	}
	if real, err := filepath.EvalSymlinks(address); err == nil {
		address = real
	}
	return filepath.Clean(address)
}

func (s *Service) validate(ctx context.Context, expected Target) (domain.AppConfig, error) {
	t, c, err := s.resolve(ctx, expected.Selection)
	if err != nil {
		return c, err
	}
	if t != expected {
		changes := []string{}
		if t.Selection != expected.Selection || t.ServiceName != expected.ServiceName {
			changes = append(changes, "服务身份")
		}
		if t.RepositoryID != expected.RepositoryID || t.Repository != expected.Repository || t.CommonDirectory != expected.CommonDirectory {
			changes = append(changes, "仓库路径或身份")
		}
		if t.Remote != expected.Remote || t.RemoteIdentity != expected.RemoteIdentity {
			changes = append(changes, "远端地址")
		}
		if t.MainBranch != expected.MainBranch {
			changes = append(changes, "主分支")
		}
		if t.GenbuService != expected.GenbuService {
			changes = append(changes, "Genbu 服务")
		}
		if t.ConfigIdentity != expected.ConfigIdentity || t.IDE != expected.IDE {
			changes = append(changes, "相关命令、代理、IDE 或写保护配置")
		}
		return c, fmt.Errorf("生产发布目标已变化，操作暂停，请重新核对。\n变化项：%s\n已确认：%s · %s · %s/%s\n当前：%s · %s · %s/%s", strings.Join(changes, "、"), expected.ServiceName, expected.Repository, expected.Remote, expected.MainBranch, t.ServiceName, t.Repository, t.Remote, t.MainBranch)
	}
	return c, ctx.Err()
}

type references map[string]string

func (s *Service) remoteRefs(ctx context.Context, t Target) (references, error) {
	out, err := s.run(ctx, t.Repository, "ls-remote", "--heads", "--tags", t.Remote)
	if err != nil {
		return nil, err
	}
	refs := references{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		refs[f[1]] = f[0]
	}
	return refs, nil
}

func tagNames(refs references) []string {
	var names []string
	for ref := range refs {
		if strings.HasPrefix(ref, "refs/tags/") && !strings.HasSuffix(ref, "^{}") {
			names = append(names, strings.TrimPrefix(ref, "refs/tags/"))
		}
	}
	sort.Strings(names)
	return names
}

func namespace(t Target) string {
	return "refs/silverwing/production/" + digest([]string{t.CommonDirectory, t.Remote})[:24]
}

// Fetch only our own namespace. User branches and local Tag refs are untouched.
func (s *Service) fetch(ctx context.Context, t Target) error {
	_, err := s.run(ctx, t.Repository, "fetch", "--prune", "--no-tags", t.Remote,
		"+refs/heads/*:"+namespace(t)+"/heads/*", "+refs/tags/*:"+namespace(t)+"/tags/*")
	return err
}

func (s *Service) commit(ctx context.Context, t Target, ref string) (string, error) {
	return s.run(ctx, t.Repository, "rev-parse", "--verify", ref+"^{commit}")
}

func (s *Service) ancestor(ctx context.Context, t Target, before, after string) (bool, error) {
	r, err := s.Git.RunContext(ctx, t.Repository, time.Minute, false, true, "merge-base", "--is-ancestor", before, after)
	if err != nil {
		return false, err
	}
	if r.ExitCode != 0 && r.ExitCode != 1 {
		return false, fmt.Errorf("无法检查提交包含关系：%s", r.Stderr)
	}
	return r.ExitCode == 0, nil
}

func (s *Service) Inspect(ctx context.Context, selection Selection, manual string) (Preview, error) {
	t, c, err := s.resolve(ctx, selection)
	if err != nil {
		return Preview{}, err
	}
	var p Preview
	err = s.locks.WithRepository(t.CommonDirectory, func() error {
		var e error
		p, e = s.inspect(ctx, t, c, manual)
		return e
	})
	return p, err
}

func (s *Service) inspect(ctx context.Context, t Target, c domain.AppConfig, manual string) (Preview, error) {
	p := Preview{Target: t, ObservedAt: timestamp(), Manual: strings.TrimSpace(manual) != "", ProductionTag: strings.TrimSpace(manual), ProductionStatus: unknownStatus()}
	EmitProgress(ctx, Progress{Selection: t.Selection, Step: 1, State: "running", Message: "读取线上版本及远端引用"})
	if err := s.fetch(ctx, t); err != nil {
		return p, err
	}
	refs, err := s.remoteRefs(ctx, t)
	if err != nil {
		return p, err
	}
	p.MainSHA = refs["refs/heads/"+t.MainBranch]
	if p.MainSHA == "" {
		return p, errors.New("服务主分支在远端不存在")
	}
	p.Tags, err = s.tagsByCreation(ctx, t, refs)
	if err != nil {
		if ctx.Err() != nil {
			return p, ctx.Err()
		}
		p.Warning = "Tag 创建时间读取失败，暂按名称排序：" + commandruntime.Redact(c, err.Error())
	}
	p.BranchHeads = map[string]string{}
	p.SuggestedVersion, _ = SuggestedVersion(p.Tags)
	for ref, sha := range refs {
		if strings.HasPrefix(ref, "refs/heads/") {
			name := strings.TrimPrefix(ref, "refs/heads/")
			p.Branches = append(p.Branches, name)
			p.BranchHeads[name] = sha
		}
	}
	sort.Strings(p.Branches)
	p.ProductionCandidates, err = s.Genbu.Versions(ctx, c, t.GenbuService)
	p.RuntimeAvailable = err == nil
	if ctx.Err() != nil {
		return p, ctx.Err()
	}
	if err != nil {
		p.Warning = strings.TrimSpace(p.Warning + "\n" + commandruntime.Redact(c, err.Error()))
	}
	if p.ProductionTag == "" && len(p.ProductionCandidates) == 1 {
		p.ProductionTag = p.ProductionCandidates[0]
	}
	if p.ProductionTag == "" {
		p.Warning = strings.TrimSpace(p.Warning + "\n无法唯一确定线上生产版本，请选择或填写生产 Tag 后重新检查。")
		EmitProgress(ctx, Progress{Selection: t.Selection, Step: 1, State: "pending", Message: "请从“更多 → 手动确认生产 Tag”确认基线"})
		return p, nil
	}
	if len(p.ProductionCandidates) == 1 && p.Manual && p.ProductionTag != p.ProductionCandidates[0] {
		return p, errors.New("填写的生产 Tag 与当前唯一运行版本不一致，请重新确认")
	}
	if p.Manual && len(p.ProductionCandidates) != 1 {
		p.Warning = strings.TrimSpace(p.Warning + "\n使用人工指定的生产基线，请核对线上版本和所选 Git Tag。")
	}
	if refs["refs/tags/"+p.ProductionTag] == "" {
		return p, errors.New("生产 Tag 在所选远端不存在")
	}
	p.ProductionSHA, err = s.commit(ctx, t, namespace(t)+"/tags/"+p.ProductionTag)
	if err != nil {
		return p, err
	}
	expected := refs["refs/tags/"+p.ProductionTag+"^{}"]
	if expected == "" {
		expected = refs["refs/tags/"+p.ProductionTag]
	}
	if p.ProductionSHA != expected {
		return p, errors.New("生产 Tag 在读取期间发生变化，请重新检查")
	}
	if _, err = s.commit(ctx, t, p.MainSHA); err != nil {
		return p, errors.New("主分支在读取期间发生变化，请重新检查")
	}
	p.ProductionStatus, err = s.Genbu.Status(ctx, c, t.GenbuService, p.ProductionTag)
	p.ProductionStatus = normalizeStatus(p.ProductionStatus)
	if ctx.Err() != nil {
		return p, ctx.Err()
	}
	if err != nil {
		p.Warning = strings.TrimSpace(p.Warning + "\n生产发布状态暂不可用：" + commandruntime.Redact(c, err.Error()))
	}
	productionOnly, err := s.run(ctx, t.Repository, "log", "--oneline", "--max-count=50", p.MainSHA+".."+p.ProductionSHA)
	if err != nil {
		return p, err
	}
	mainOnly, err := s.run(ctx, t.Repository, "log", "--oneline", "--max-count=50", p.ProductionSHA+".."+p.MainSHA)
	if err != nil {
		return p, err
	}
	if productionOnly == "" {
		productionOnly = "无"
	}
	if mainOnly == "" {
		mainOnly = "无"
	}
	p.Commits = "生产 Tag 尚未合入主分支的提交（最多 50 条）：\n" + productionOnly + "\n\n主分支已有提交，全部保留（最多 50 条）：\n" + mainOnly
	p.ProductionInMain, err = s.ancestor(ctx, t, p.ProductionSHA, p.MainSHA)
	if err != nil {
		return p, err
	}
	p.Diff, err = s.run(ctx, t.Repository, "diff", "--stat", p.MainSHA+"..."+p.ProductionSHA, "--")
	if err == nil {
		EmitProgress(ctx, Progress{Selection: t.Selection, Step: 1, State: "complete", Message: "生产 Tag 与主分支差异已就绪"})
	}
	return p, err
}

func (s *Service) save(b *Batch) error { return s.Store.Save(b) }

// A manual choice is valid for the observation the user confirmed, including
// an unavailable runtime query. A changed mixed version set needs confirmation
// even if the chosen Tag and its commit did not change.
func sameProductionBaseline(a, b Preview) bool {
	aVersions, bVersions := slices.Clone(a.ProductionCandidates), slices.Clone(b.ProductionCandidates)
	slices.Sort(aVersions)
	slices.Sort(bVersions)
	return a.ProductionTag == b.ProductionTag && a.ProductionSHA == b.ProductionSHA &&
		a.RuntimeAvailable == b.RuntimeAvailable && slices.Equal(aVersions, bVersions)
}

func (s *Service) withBatch(ctx context.Context, id string, operation func(*Batch, domain.AppConfig) error) (Batch, error) {
	return s.withRecord(ctx, id, false, operation)
}

func (s *Service) withRecord(ctx context.Context, id string, allowEnded bool, operation func(*Batch, domain.AppConfig) error) (Batch, error) {
	b, err := s.Store.Load(id)
	if err != nil {
		return b, err
	}
	err = s.locks.WithRepository(b.Target.CommonDirectory, func() error {
		var e error
		b, e = s.Store.Load(id)
		if e != nil {
			return e
		}
		if b.Ended && !allowEnded {
			return errors.New("该生产批次已结束")
		}
		c, e := s.validate(ctx, b.Target)
		if e != nil {
			return e
		}
		e = operation(&b, c)
		if e != nil && b.Intent != nil {
			b.Message = commandruntime.Redact(c, e.Error())
			if !b.Ended && b.Phase != "CONFLICT" && b.Phase != abortingMerge {
				b.Phase = "PAUSED"
			}
			if writeErr := s.save(&b); writeErr != nil {
				return errors.Join(e, fmt.Errorf("保存中断状态失败：%w", writeErr))
			}
		}
		return e
	})
	return b, err
}
