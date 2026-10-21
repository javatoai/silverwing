package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"

	"golang.org/x/text/unicode/norm"
)

const MaxDirectoryNameLength = 80

var (
	semverPattern            = regexp.MustCompile("^(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)$")
	sourceIDPattern          = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$")
	gitRefPattern            = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9._/@-]*$")
	moduleNamePattern        = regexp.MustCompile("^[A-Za-z0-9_-]+(?:/[A-Za-z0-9_-]+)*$")
	materialsWikiPathPattern = regexp.MustCompile("^/wiki/[A-Za-z0-9]+/?$")
)

func ValidateSchemaVersion(actual, expected string) bool {
	a, e := semverPattern.FindStringSubmatch(actual), semverPattern.FindStringSubmatch(expected)
	return a != nil && e != nil && a[1] == e[1] && a[2] == e[2]
}

func ValidateRemoteGitURL(value string) error {
	if value == "" || strings.TrimSpace(value) != value || strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return fmt.Errorf("Git 仓库地址不能为空且不能包含空白")
	}
	if regexp.MustCompile(`^git@[A-Za-z0-9.-]+:[^\\\s]+$`).MatchString(value) {
		return nil
	}
	u, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("Git 仓库地址格式不合法")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" && scheme != "ssh" {
		return fmt.Errorf("仅支持 HTTP、HTTPS 或 SSH Git 仓库地址")
	}
	if u.Host == "" || u.Path == "" || u.Path == "/" {
		return fmt.Errorf("Git 仓库地址必须包含主机和仓库路径")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("Git 仓库地址不能包含查询参数或片段")
	}
	if (scheme == "http" || scheme == "https") && u.User != nil {
		return fmt.Errorf("HTTP(S) Git 仓库地址不能包含账号、Token 或密码")
	}
	if scheme == "ssh" && u.User != nil && !regexp.MustCompile("^[A-Za-z0-9._-]+$").MatchString(u.User.Username()) {
		return fmt.Errorf("SSH Git 仓库地址中的账号不合法")
	}
	return nil
}

func ValidateGitRef(value *string) error {
	if value == nil || *value == "" {
		return nil
	}
	v := *value
	if strings.TrimSpace(v) != v || !gitRefPattern.MatchString(v) || strings.Contains(v, "..") ||
		strings.Contains(v, "//") || strings.Contains(v, "@{") || strings.HasSuffix(v, ".") || strings.HasSuffix(v, ".lock") {
		return fmt.Errorf("Git ref 不合法")
	}
	return nil
}

func ValidateExtensionRelativePath(value, label string) error {
	if strings.TrimSpace(value) != value || value == "" {
		return fmt.Errorf("%s 不能为空且不能包含首尾空格", label)
	}
	if strings.ContainsAny(value, "\\\\:") || strings.HasPrefix(value, "/") {
		return fmt.Errorf("%s 必须是仓库内相对路径", label)
	}
	if value != "." {
		for _, part := range strings.Split(value, "/") {
			if part == "" || part == "." || part == ".." {
				return fmt.Errorf("%s 不能包含 .、.. 或空路径段", label)
			}
		}
	}
	return nil
}

func ValidateRequirementMaterialsSubdirectory(value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", nil
	}
	if v == "." || v == ".." {
		return "", fmt.Errorf("任务资料子目录不能是 . 或 ..")
	}
	if strings.ContainsAny(v, "/\\\\") {
		return "", fmt.Errorf("任务资料子目录只能是单层目录名")
	}
	for _, r := range v {
		if r < 0x20 || strings.ContainsRune("<>:\"|?*", r) {
			return "", fmt.Errorf("任务资料子目录包含 Windows 不允许的字符")
		}
	}
	if strings.HasSuffix(v, ".") || strings.HasSuffix(v, " ") {
		return "", fmt.Errorf("任务资料子目录不能以点或空格结尾")
	}
	if isWindowsReserved(v) {
		return "", fmt.Errorf("任务资料子目录不能使用 Windows 保留名称")
	}
	return v, nil
}

func ValidateMaterialsFeishuTarget(raw string) (*string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil, nil
	}
	u, err := url.Parse(v)
	if err != nil {
		return nil, fmt.Errorf("请输入有效的飞书知识库地址")
	}
	if u.Scheme != "https" || u.Host == "" || u.User != nil || !materialsWikiPathPattern.MatchString(u.Path) {
		return nil, fmt.Errorf("飞书同步目标需要 HTTPS 知识库 /wiki/ 地址")
	}
	host := strings.ToLower(u.Hostname())
	if host != "feishu.cn" && !strings.HasSuffix(host, ".feishu.cn") && host != "larksuite.com" && !strings.HasSuffix(host, ".larksuite.com") {
		return nil, fmt.Errorf("请输入飞书或 Lark 知识库地址")
	}
	canonical := (&url.URL{Scheme: "https", Host: strings.ToLower(u.Host), Path: strings.TrimRight(u.Path, "/")}).String()
	return &canonical, nil
}

func NormalizeCommandProxyURL(raw *string) (*string, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, nil
	}
	u, err := url.Parse(strings.TrimSpace(*raw))
	if err != nil {
		return nil, fmt.Errorf("命令代理地址格式不合法")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" && scheme != "socks5" {
		return nil, fmt.Errorf("命令代理仅支持 HTTP、HTTPS 或 SOCKS5")
	}
	if u.Opaque != "" || u.Hostname() == "" || u.Port() == "" {
		return nil, fmt.Errorf("命令代理地址必须包含主机和有效端口")
	}
	var port int
	if _, err := fmt.Sscanf(u.Port(), "%d", &port); err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("命令代理地址必须包含有效端口")
	}
	if u.User != nil {
		return nil, fmt.Errorf("代理账号和密码请填写在独立认证项中")
	}
	if u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("命令代理地址不能包含路径")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("命令代理地址不能包含查询参数或片段")
	}
	value := scheme + "://" + u.Host
	return &value, nil
}

func NormalizeCommandProxyNoProxy(raw *string) (*string, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, nil
	}
	value := strings.TrimSpace(*raw)
	if strings.ContainsAny(value, "\r\n") {
		return nil, fmt.Errorf("不代理地址不能包含换行")
	}
	seen, entries := map[string]bool{}, []string{}
	for _, item := range strings.Split(value, ",") {
		entry := strings.TrimSpace(item)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "://") || strings.Contains(entry, "@") || strings.IndexFunc(entry, unicode.IsSpace) >= 0 {
			return nil, fmt.Errorf("不代理地址只能填写主机、IP 或通配符")
		}
		if !seen[entry] {
			seen[entry] = true
			entries = append(entries, entry)
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("不代理地址不能为空")
	}
	normalized := strings.Join(entries, ",")
	return &normalized, nil
}

func (c AppConfig) Validate() error {
	if c.SchemaVersion != CurrentAppConfigSchemaVersion {
		return fmt.Errorf("配置 schema 不受支持：%s", c.SchemaVersion)
	}
	if c.AIRequirementNamingModel == "" || strings.TrimSpace(c.AIRequirementNamingModel) != c.AIRequirementNamingModel || strings.IndexFunc(c.AIRequirementNamingModel, unicode.IsSpace) >= 0 {
		return fmt.Errorf("AI 命名模型不合法")
	}
	if strings.IndexFunc(c.DefaultBranchPrefix, unicode.IsSpace) >= 0 {
		return fmt.Errorf("默认分支名前缀不能包含空白字符")
	}
	if err := uniqueNonblank(c.DefaultWorkspaceToolIDs, "默认工作区工具"); err != nil {
		return err
	}
	proxy, err := NormalizeCommandProxyURL(c.CommandProxyURL)
	if err != nil {
		return err
	}
	noProxy, err := NormalizeCommandProxyNoProxy(c.CommandProxyNoProxy)
	if err != nil {
		return err
	}
	username, password := strings.TrimSpace(stringValue(c.CommandProxyUsername)), stringValue(c.CommandProxyPassword)
	if (username == "") != (password == "") {
		return fmt.Errorf("代理账号和密码必须同时填写")
	}
	if proxy == nil && (len(c.CommandProxyTargets) > 0 || noProxy != nil || username != "" || password != "") {
		return fmt.Errorf("未配置命令代理地址时不能设置代理命令、例外地址或认证信息")
	}
	if c.RequirementMaterialsSubdirectory != nil {
		if _, err := ValidateRequirementMaterialsSubdirectory(*c.RequirementMaterialsSubdirectory); err != nil {
			return err
		}
	}
	if c.RequirementMaterialsFeishuTarget != nil {
		if _, err := ValidateMaterialsFeishuTarget(*c.RequirementMaterialsFeishuTarget); err != nil {
			return err
		}
	}
	if len(c.Groups) == 0 {
		return fmt.Errorf("至少需要一个组")
	}
	if err := unique(groupIDs(c.Groups), "组 ID"); err != nil {
		return err
	}
	groupNames := make([]string, 0, len(c.Groups))
	for _, group := range c.Groups {
		groupNames = append(groupNames, strings.TrimSpace(group.Name))
	}
	if err := uniqueLower(groupNames, "组名"); err != nil {
		return err
	}
	if err := unique(repositoryIDs(c.Repositories), "仓库 ID"); err != nil {
		return err
	}
	commonDirs, repoIDs := map[string]bool{}, map[string]bool{}
	for _, r := range c.Repositories {
		if strings.TrimSpace(r.ID) == "" || strings.TrimSpace(r.Name) == "" || strings.TrimSpace(r.RootPath) == "" || strings.TrimSpace(r.GitCommonDirectory) == "" {
			return fmt.Errorf("仓库 ID、名称和路径不能为空")
		}
		k := strings.ToLower(r.GitCommonDirectory)
		if commonDirs[k] {
			return fmt.Errorf("同一 Git common directory 只能配置一次")
		}
		commonDirs[k] = true
		repoIDs[r.ID] = true
	}
	if err := unique(projectKeys(c.MeegleProjects), "Meegle 空间 Key"); err != nil {
		return err
	}
	for _, p := range c.MeegleProjects {
		if err := p.Validate(); err != nil {
			return err
		}
	}
	if err := uniqueLower(marketplaceIDs(c.CodexPluginMarketplaceSources), "Codex 插件来源 ID"); err != nil {
		return err
	}
	if err := uniqueLower(marketplaceKeys(c.CodexPluginMarketplaceSources), "Codex 插件来源"); err != nil {
		return err
	}
	for _, s := range c.CodexPluginMarketplaceSources {
		if err := s.Validate(); err != nil {
			return err
		}
	}
	if err := uniqueLower(skillIDs(c.SkillSources), "Skill 来源 ID"); err != nil {
		return err
	}
	if err := uniqueLower(skillKeys(c.SkillSources), "Skill 来源"); err != nil {
		return err
	}
	for _, s := range c.SkillSources {
		if err := s.Validate(); err != nil {
			return err
		}
	}

	tools := map[DevelopmentToolType]bool{}
	for _, t := range c.DevelopmentTools {
		if strings.TrimSpace(t.Path) == "" || tools[t.Type] {
			return fmt.Errorf("开发工具路径不能为空或开发工具重复")
		}
		tools[t.Type] = true
	}
	branches := map[string]bool{}
	for _, b := range c.BlockedGitWriteBranches {
		if strings.TrimSpace(b) == "" || strings.TrimSpace(b) != b {
			return fmt.Errorf("Git 写保护分支不能包含空值或首尾空格")
		}
		k := strings.ToLower(b)
		if branches[k] {
			return fmt.Errorf("Git 写保护分支不能重复（忽略大小写）")
		}
		branches[k] = true
	}
	for _, g := range c.Groups {
		if err := g.Validate(); err != nil {
			return err
		}
		for _, s := range g.Services {
			if !repoIDs[s.RepositoryID] {
				return fmt.Errorf("服务 %s 引用了不存在的仓库：%s", s.DisplayName, s.RepositoryID)
			}
			if err := s.Validate(); err != nil {
				return err
			}
		}
	}
	for _, t := range c.CommandProxyTargets {
		if !validProxyTarget(t) {
			return fmt.Errorf("命令代理目标不合法：%s", t)
		}
	}
	return nil
}

func (c *AppConfig) NormalizeAndValidate() error {
	if c.CommandProxyURL != nil {
		v, e := NormalizeCommandProxyURL(c.CommandProxyURL)
		if e != nil {
			return e
		}
		c.CommandProxyURL = v
	}
	if c.CommandProxyNoProxy != nil {
		v, e := NormalizeCommandProxyNoProxy(c.CommandProxyNoProxy)
		if e != nil {
			return e
		}
		c.CommandProxyNoProxy = v
	}
	if c.RequirementMaterialsSubdirectory != nil {
		v, e := ValidateRequirementMaterialsSubdirectory(*c.RequirementMaterialsSubdirectory)
		if e != nil {
			return e
		}
		c.RequirementMaterialsSubdirectory = &v
	}
	if c.RequirementMaterialsFeishuTarget != nil {
		v, e := ValidateMaterialsFeishuTarget(*c.RequirementMaterialsFeishuTarget)
		if e != nil {
			return e
		}
		c.RequirementMaterialsFeishuTarget = v
	}
	return c.Validate()
}

func (r RepositoryConfig) Validate() error {
	if r.ID == "" || r.Name == "" || r.RootPath == "" || r.GitCommonDirectory == "" {
		return fmt.Errorf("仓库 ID、名称、根目录和 git-common-dir 不能为空")
	}
	return nil
}
func (p MeegleProjectConfig) Validate() error {
	pattern := regexp.MustCompile("^[A-Za-z0-9_-]+$")
	if !pattern.MatchString(p.ProjectKey) || !pattern.MatchString(p.SimpleName) {
		return fmt.Errorf("Meegle 空间 Key 和短名只能包含字母、数字、下划线和连字符")
	}
	return nil
}
func (s CodexPluginMarketplaceSource) Validate() error {
	if !sourceIDPattern.MatchString(s.ID) {
		return fmt.Errorf("插件来源 ID 不合法")
	}
	if s.Name == "" || strings.TrimSpace(s.Name) != s.Name {
		return fmt.Errorf("插件来源名称不能为空且不能包含首尾空格")
	}
	if err := ValidateRemoteGitURL(s.RepositoryURL); err != nil {
		return err
	}
	if err := ValidateGitRef(s.Ref); err != nil {
		return err
	}
	return ValidateExtensionRelativePath(s.MarketplaceDirectory, "插件 Marketplace 目录")
}
func (s SkillSource) Validate() error {
	if !sourceIDPattern.MatchString(s.ID) {
		return fmt.Errorf("Skill 来源 ID 不合法")
	}
	if s.Name == "" || strings.TrimSpace(s.Name) != s.Name {
		return fmt.Errorf("Skill 来源名称不能为空且不能包含首尾空格")
	}
	if err := ValidateRemoteGitURL(s.RepositoryURL); err != nil {
		return err
	}
	if err := ValidateGitRef(s.Ref); err != nil {
		return err
	}
	if s.SkillRoot != nil {
		return ValidateExtensionRelativePath(*s.SkillRoot, "Skill 目录")
	}
	return nil
}
func (s ServiceModuleConfig) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("模块 ID 不能为空")
	}
	if s.MasterBranch != nil {
		if _, e := ParseRemoteBranchRef(*s.MasterBranch); e != nil {
			return e
		}
	}

	ids := map[string]bool{}
	for _, c := range s.CustomCommands {
		if c.ID == "" || c.Name == "" || c.IconKey == "" || c.Executable == "" || c.TimeoutSeconds <= 0 {
			return fmt.Errorf("快捷命令配置不合法")
		}
		k := strings.ToLower(c.ID)
		if ids[k] {
			return fmt.Errorf("同一模块内快捷命令 ID 不能重复（忽略大小写）")
		}
		ids[k] = true
	}
	return nil
}
func (s GroupServiceConfig) Validate() error {
	if s.ID == "" || s.RepositoryID == "" || s.DisplayName == "" {
		return fmt.Errorf("组内服务 ID、仓库 ID 和服务名称不能为空")
	}
	if !safeDirectoryIdentifier(s.ID) {
		return fmt.Errorf("组内服务 ID 必须是安全且稳定的目录片段")
	}

	if _, e := ParseRemoteBranchRef(s.MasterBranch); e != nil {
		return e
	}

	if len(s.Modules) == 0 {
		return fmt.Errorf("服务至少需要一个工作区模块")
	}
	if err := uniqueLower(moduleIDs(s.Modules), "同一服务内模块 ID"); err != nil {
		return err
	}
	if _, err := StandardWorktreeModuleNames(s.Modules); err != nil {
		return err
	}
	for _, m := range s.Modules {
		if err := m.Validate(); err != nil {
			return err
		}
	}
	return nil
}
func (g GroupConfig) Validate() error {
	if g.ID == "" || strings.TrimSpace(g.Name) == "" || !safeDirectoryIdentifier(g.ID) {
		return fmt.Errorf("组 ID 或名称不合法")
	}
	if err := unique(serviceRepositoryIDs(g.Services), "同一仓库在一个组内只能出现一次"); err != nil {
		return err
	}
	return unique(serviceIDs(g.Services), "同一组内服务 ID")
}
func (m TaskManifest) Validate() error {
	if !ValidateSchemaVersion(m.SchemaVersion, CurrentTaskManifestSchemaVersion) {
		return fmt.Errorf("任务 JSON 版本不受支持：%s，当前版本为 %s", m.SchemaVersion, CurrentTaskManifestSchemaVersion)
	}
	if m.FolderName == "" || m.TaskDirectoryName == "" || m.FeatureBranch == "" || m.CreatedAt == "" || m.UpdatedAt == "" {
		return fmt.Errorf("任务清单缺少必填字段")
	}
	if m.AgentContext != nil {
		if err := m.AgentContext.Validate(); err != nil {
			return err
		}
	}
	for _, s := range m.Services {
		if s.RepositoryID == "" || s.ServiceName == "" || s.RepositoryPath == "" || s.WorktreePath == "" || s.Branch == "" {
			return fmt.Errorf("任务工作区缺少必填字段")
		}

	}
	return nil
}

// Validate matches the invariants enforced by the Kotlin AgentTaskContext
// constructor before a manifest can be decoded or serialized.
func (c AgentTaskContext) Validate() error {
	if strings.TrimSpace(c.DocumentationDirectory) == "" {
		return fmt.Errorf("需求过程文档目录不能为空")
	}
	if c.ProtocolVersion != "2" {
		return fmt.Errorf("不支持的 Agent 协议版本：%s，请重建任务", c.ProtocolVersion)
	}
	if c.HandoffRelativePath != ".workspace/HANDOFF.md" {
		return fmt.Errorf("交接文件路径必须是 .workspace/HANDOFF.md")
	}
	return nil
}

func (c AppConfig) Group(groupID string) (GroupConfig, error) {
	for _, g := range c.Groups {
		if g.ID == groupID {
			return g, nil
		}
	}
	return GroupConfig{}, fmt.Errorf("找不到组：%s", groupID)
}
func (c AppConfig) GroupService(groupID, serviceID string) (GroupServiceConfig, error) {
	g, err := c.Group(groupID)
	if err != nil {
		return GroupServiceConfig{}, err
	}
	for _, s := range g.Services {
		if s.ID == serviceID {
			return s, nil
		}
	}
	return GroupServiceConfig{}, fmt.Errorf("组中找不到服务：%s", serviceID)
}
func (c AppConfig) RequirementMaterialsConfigured() bool {
	return c.RequirementMaterialsRoot != nil && *c.RequirementMaterialsRoot != "" && c.RequirementMaterialsSubdirectory != nil && *c.RequirementMaterialsSubdirectory != ""
}

func (s CodexPluginMarketplaceSource) RequiresRootMarketplaceMigration() bool {
	return s.MarketplaceDirectory != "."
}
func (s ServiceWorkspace) SelectionKey() string { return s.GroupServiceID + ":" + s.ModuleID }
func (m TaskManifest) Health() WorkspaceHealth  { return AggregateWorkspaceHealth(m.Services) }
func AggregateWorkspaceHealth(workspaces []ServiceWorkspace) WorkspaceHealth {
	for _, w := range workspaces {
		if w.Health == WorkspaceFailed {
			return WorkspaceFailed
		}
	}
	for _, w := range workspaces {
		if w.Health == WorkspaceCreating {
			return WorkspaceCreating
		}
	}
	for _, w := range workspaces {
		if w.Health == WorkspaceReadyWithWarnings {
			return WorkspaceReadyWithWarnings
		}
	}
	return WorkspaceReady
}
func (r RepositoryConfig) ToInfo() RepositoryInfo {
	return RepositoryInfo{ID: r.ID, Name: r.Name, RootPath: r.RootPath, GitCommonDirectory: r.GitCommonDirectory, RemoteURL: r.OriginURL, CurrentBranch: r.CurrentBranch}
}
func (r RepositoryInfo) ToConfig() RepositoryConfig {
	return RepositoryConfig{ID: r.ID, Name: r.Name, RootPath: r.RootPath, GitCommonDirectory: r.GitCommonDirectory, OriginURL: r.RemoteURL, CurrentBranch: r.CurrentBranch}
}
func (c CommandResult) Succeeded() bool { return c.ExitCode == 0 }

func ParseRemoteBranchRef(value string) (RemoteBranchRef, error) {
	v := strings.TrimSpace(value)
	i := strings.IndexByte(v, '/')
	if i <= 0 || i == len(v)-1 {
		return RemoteBranchRef{}, fmt.Errorf("远程分支必须使用 <remote>/<branch> 格式，例如 origin/release/test")
	}
	r := RemoteBranchRef{Remote: v[:i], Branch: v[i+1:]}
	if r.Remote == "." || r.Remote == ".." || strings.Contains(r.Remote, "/") {
		return RemoteBranchRef{}, fmt.Errorf("远程名称不合法：%s", r.Remote)
	}
	b := r.Branch
	if b == "" || strings.HasPrefix(b, "/") || strings.HasSuffix(b, "/") || b == "@" || strings.Contains(b, "..") || strings.Contains(b, "@{") {
		return RemoteBranchRef{}, fmt.Errorf("远程分支格式不合法：%s", b)
	}
	if strings.IndexFunc(b, func(r rune) bool { return unicode.IsSpace(r) || r < 32 || strings.ContainsRune("~^:?*[\\\\", r) }) >= 0 {
		return RemoteBranchRef{}, fmt.Errorf("远程分支包含 Git 不允许的字符：%s", b)
	}
	for _, part := range strings.Split(b, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".") || strings.HasSuffix(strings.ToLower(part), ".lock") {
			return RemoteBranchRef{}, fmt.Errorf("远程分支路径片段不合法：%s", b)
		}
	}
	return r, nil
}

func NormalizeBaseRef(value string) string {
	r, err := ParseRemoteBranchRef(value)
	if err != nil {
		return strings.TrimPrefix(strings.TrimPrefix(value, "refs/heads/"), "origin/")
	}
	return strings.TrimPrefix(strings.TrimPrefix(r.Branch, "refs/remotes/"+r.Remote+"/"), "refs/heads/")
}
func (r RemoteBranchRef) String() string       { return r.Remote + "/" + r.Branch }
func (r RemoteBranchRef) BaseIdentity() string { return r.Remote + "|" + NormalizeBaseRef(r.String()) }

func safeDirectoryIdentifier(value string) bool {
	return value != "" && value != "." && value != ".." && regexp.MustCompile("^[A-Za-z0-9._-]+$").MatchString(value)
}
func isWindowsReserved(value string) bool {
	switch strings.ToUpper(strings.SplitN(value, ".", 2)[0]) {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
}
func DirectoryNameValidationError(value string) error {
	n := norm.NFKC.String(value)
	if trimTaskNamingWhitespace(n) == "" {
		return fmt.Errorf("任务名称 / 文件夹名称不能为空")
	}
	if n != trimTaskNamingWhitespace(n) {
		return fmt.Errorf("任务名称 / 文件夹名称不能以空白开头或结尾")
	}
	if strings.HasSuffix(n, ".") {
		return fmt.Errorf("任务名称 / 文件夹名称不能以点结尾")
	}
	if strings.IndexFunc(n, invalidTaskDirectoryRune) >= 0 {
		return fmt.Errorf(`任务名称 / 文件夹名称不能包含 < > : " / \ | ? * 等非法字符`)
	}
	if isWindowsReserved(n) {
		return fmt.Errorf("任务名称 / 文件夹名称不能使用 Windows 保留名")
	}
	if len(utf16.Encode([]rune(n))) > MaxDirectoryNameLength {
		return fmt.Errorf("任务名称 / 文件夹名称不能超过 %d 个字符", MaxDirectoryNameLength)
	}
	return nil
}
func RequireValidDirectoryName(value string) (string, error) {
	if err := DirectoryNameValidationError(value); err != nil {
		return "", err
	}
	return norm.NFKC.String(value), nil
}
func DirectoryName(value string) (string, error) {
	if trimTaskNamingWhitespace(value) == "" {
		return "", fmt.Errorf("文件夹名不能为空")
	}
	n := norm.NFKC.String(trimTaskNamingWhitespace(value))
	var b strings.Builder
	dash := false
	for _, r := range n {
		// Source first replaces Cc/invalid characters, then Java's default
		// ASCII \s regex. Other Unicode separators inside a name are preserved.
		if invalidTaskDirectoryRune(r) || r == ' ' {
			r = '-'
		}
		if r == '-' {
			if dash {
				continue
			}
			dash = true
		} else {
			dash = false
		}
		b.WriteRune(r)
	}
	s := strings.Trim(b.String(), " .-")
	if s == "" {
		s = "task"
	}
	if isWindowsReserved(s) {
		s = "_" + s
	}
	if len(utf16.Encode([]rune(s))) <= MaxDirectoryNameLength {
		return s, nil
	}
	hash := sha256.Sum256([]byte(value))
	suffix := hex.EncodeToString(hash[:4])
	limit := MaxDirectoryNameLength - len(suffix) - 1
	return prefixTaskDirectoryUTF16(s, limit) + "-" + suffix, nil
}

func invalidTaskDirectoryRune(r rune) bool {
	return unicode.IsControl(r) || strings.ContainsRune("<>:\"/\\\\|?*", r)
}

// Kotlin Char.isWhitespace is Java isWhitespace OR isSpaceChar. Unlike
// strings.TrimSpace, that union excludes NEL and includes U+001C..U+001F.
func trimTaskNamingWhitespace(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return unicode.Is(unicode.Zs, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) ||
			(r >= '\t' && r <= '\r') || (r >= 0x1c && r <= 0x1f)
	})
}

func prefixTaskDirectoryUTF16(value string, limit int) string {
	var prefix strings.Builder
	units := 0
	for _, r := range value {
		width := 1
		if r > 0xffff {
			width = 2
		}
		if units+width > limit {
			break
		}
		prefix.WriteRune(r)
		units += width
	}
	return prefix.String()
}
func StandardWorktreeModuleNames(modules []ServiceModuleConfig) (map[string]string, error) {
	if len(modules) == 0 {
		return nil, fmt.Errorf("至少需要一个模块")
	}
	names, seen, dirs := map[string]string{}, map[string]bool{}, map[string]bool{}
	for _, m := range modules {
		name := strings.TrimSpace(m.Name)
		if name == "" {
			name = "default"
		}
		if !moduleNamePattern.MatchString(name) {
			return nil, fmt.Errorf("模块 %s 名称不合法：%s", m.ID, name)
		}
		names[m.ID] = name
		if len(modules) > 1 {
			k := strings.ToLower(name)
			if seen[k] {
				return nil, fmt.Errorf("多模块名称不能重复（忽略大小写）")
			}
			seen[k] = true
			d := strings.ToLower(strings.ReplaceAll(name, "/", "-"))
			if dirs[d] {
				return nil, fmt.Errorf("模块名称转换为目录后不能重复")
			}
			dirs[d] = true
		}
	}
	return names, nil
}
func TaskBranchNames(requested string, modules []ServiceModuleConfig, explicit map[string]string) (map[string]string, error) {
	for id := range explicit {
		found := false
		for _, m := range modules {
			if m.ID == id {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("任务分支覆盖引用了不存在的模块")
		}
	}
	if trimTaskNamingWhitespace(requested) == "" {
		return nil, fmt.Errorf("任务分支不能为空")
	}
	if len(modules) == 0 {
		return nil, fmt.Errorf("至少需要一个模块")
	}
	ids := make([]string, 0, len(modules))
	for _, module := range modules {
		ids = append(ids, module.ID)
	}
	if err := unique(ids, "模块 ID"); err != nil {
		return nil, err
	}
	names, err := StandardWorktreeModuleNames(modules)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, m := range modules {
		b := requested
		if len(modules) > 1 {
			b += "-" + names[m.ID]
		}
		if value, ok := explicit[m.ID]; ok {
			b = strings.TrimSpace(value)
			if b == "" {
				return nil, fmt.Errorf("模块任务分支不能为空")
			}
		}
		out[m.ID] = b
	}
	branches := make([]string, 0, len(out))
	for _, b := range out {
		branches = append(branches, b)
	}
	if err := RequireNoBranchConflicts(branches); err != nil {
		return nil, err
	}
	return out, nil
}
func RequireNoBranchConflicts(branches []string) error {
	lowered := map[string]string{}
	for _, b := range branches {
		k := strings.ToLower(b)
		if prev, ok := lowered[k]; ok {
			return fmt.Errorf("同一仓库的模块目标分支不能重复：%s 与 %s", prev, b)
		}
		lowered[k] = b
	}
	for b := range lowered {
		for i, r := range b {
			if r == '/' {
				if p, ok := lowered[b[:i]]; ok {
					return fmt.Errorf("同一仓库的模块目标分支不能互为父子路径：%s 与 %s", p, lowered[b])
				}
			}
		}
	}
	return nil
}

func unique(values []string, label string) error {
	seen := map[string]bool{}
	for _, v := range values {
		if seen[v] {
			return fmt.Errorf("%s 不能重复", label)
		}
		seen[v] = true
	}
	return nil
}
func uniqueLower(values []string, label string) error {
	seen := map[string]bool{}
	for _, v := range values {
		k := strings.ToLower(v)
		if seen[k] {
			return fmt.Errorf("%s 不能重复（忽略大小写）", label)
		}
		seen[k] = true
	}
	return nil
}
func uniqueNonblank(values []string, label string) error {
	seen := map[string]bool{}
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("%s ID 不能为空", label)
		}
		if seen[v] {
			return fmt.Errorf("%s 不能重复", label)
		}
		seen[v] = true
	}
	return nil
}
func groupIDs(v []GroupConfig) []string {
	r := []string{}
	for _, x := range v {
		r = append(r, x.ID)
	}
	return r
}
func repositoryIDs(v []RepositoryConfig) []string {
	r := []string{}
	for _, x := range v {
		r = append(r, x.ID)
	}
	return r
}
func projectKeys(v []MeegleProjectConfig) []string {
	r := []string{}
	for _, x := range v {
		r = append(r, x.ProjectKey)
	}
	return r
}
func marketplaceIDs(v []CodexPluginMarketplaceSource) []string {
	r := []string{}
	for _, x := range v {
		r = append(r, x.ID)
	}
	return r
}
func skillIDs(v []SkillSource) []string {
	r := []string{}
	for _, x := range v {
		r = append(r, x.ID)
	}
	return r
}
func marketplaceKeys(v []CodexPluginMarketplaceSource) []string {
	r := []string{}
	for _, x := range v {
		r = append(r, extensionKey(x.RepositoryURL, x.Ref, x.MarketplaceDirectory))
	}
	return r
}
func skillKeys(v []SkillSource) []string {
	r := []string{}
	for _, x := range v {
		root := ""
		if x.SkillRoot != nil {
			root = *x.SkillRoot
		}
		r = append(r, extensionKey(x.RepositoryURL, x.Ref, root))
	}
	return r
}
func extensionKey(repo string, ref *string, location string) string {
	repo = strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(repo, "/"), ".git"))
	v := ""
	if ref != nil {
		v = *ref
	}
	return repo + "\x00" + v + "\x00" + location
}
func moduleIDs(v []ServiceModuleConfig) []string {
	r := []string{}
	for _, x := range v {
		r = append(r, x.ID)
	}
	return r
}
func serviceIDs(v []GroupServiceConfig) []string {
	r := []string{}
	for _, x := range v {
		r = append(r, x.ID)
	}
	return r
}
func serviceRepositoryIDs(v []GroupServiceConfig) []string {
	r := []string{}
	for _, x := range v {
		r = append(r, x.RepositoryID)
	}
	return r
}
func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func validProxyTarget(v CommandProxyTarget) bool {
	switch v {
	case ProxyTargetCodex, ProxyTargetGit, ProxyTargetWorkspaceCommand, ProxyTargetMeegle, ProxyTargetLark:
		return true
	}
	return false
}
