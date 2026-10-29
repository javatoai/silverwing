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

	"silverwing-go/internal/productprofile"
)

const MaxDirectoryNameLength = 80

var (
	semverPattern   = regexp.MustCompile("^(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)$")
	sourceIDPattern = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$")
	gitRefPattern   = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9._/@-]*$")
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
	if strings.EqualFold(v, "temp") || strings.EqualFold(v, ".git") || strings.HasPrefix(strings.ToLower(v), ".silverwing-") {
		return "", fmt.Errorf("任务资料子目录不能使用 temp 或系统保留目录名")
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
	if !productprofile.AcceptsEdition(c.Edition) {
		return fmt.Errorf("%s", productprofile.ConfigurationEditionError())
	}
	if c.SchemaVersion != CurrentAppConfigSchemaVersion {
		return fmt.Errorf("配置 schema 不受支持：%s", c.SchemaVersion)
	}
	if strings.TrimSpace(c.AIRequirementNamingModel) == "" || strings.IndexFunc(c.AIRequirementNamingModel, unicode.IsSpace) >= 0 {
		return fmt.Errorf("AI 命名模型不合法")
	}
	if err := uniqueNonblank(c.DefaultWorkspaceToolIDs, "任务打开工具"); err != nil {
		return err
	}
	for _, id := range c.DefaultWorkspaceToolIDs {
		if id != "codex" {
			return fmt.Errorf("%s仅支持 Codex 任务工具", productprofile.ConfigEditionLabel)
		}
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
	if c.MaterialsGitSync != nil {
		if err := c.MaterialsGitSync.Validate(); err != nil {
			return err
		}
	}
	if len(c.Groups) == 0 {
		return fmt.Errorf("至少保留一个项目组")
	}
	ids := []string{}
	groupNames := make([]string, 0, len(c.Groups))
	for _, g := range c.Groups {
		if err := g.Validate(); err != nil {
			return err
		}
		ids = append(ids, g.ID)
		groupNames = append(groupNames, strings.TrimSpace(g.Name))
	}
	if err := uniqueNonblank(ids, "项目组 ID"); err != nil {
		return err
	}
	if err := uniqueLower(groupNames, "项目组名称"); err != nil {
		return err
	}
	if err := unique(projectKeys(c.MeegleProjects), "Meegle 空间 Key"); err != nil {
		return err
	}
	for _, p := range c.MeegleProjects {
		if err := p.Validate(); err != nil {
			return err
		}
	}
	sourceIDs := []string{}
	for _, source := range c.SkillSources {
		if err := source.Validate(); err != nil {
			return err
		}
		sourceIDs = append(sourceIDs, source.ID)
	}
	if err := uniqueLower(sourceIDs, "Skill 来源 ID"); err != nil {
		return err
	}
	if err := uniqueLower(skillKeys(c.SkillSources), "Skill 来源"); err != nil {
		return err
	}
	marketplaceIDs := []string{}
	for _, source := range c.CodexPluginMarketplaceSources {
		if err := source.Validate(); err != nil {
			return err
		}
		marketplaceIDs = append(marketplaceIDs, source.ID)
	}
	if err := uniqueLower(marketplaceIDs, "Codex 插件来源 ID"); err != nil {
		return err
	}
	if err := uniqueLower(marketplaceKeys(c.CodexPluginMarketplaceSources), "Codex 插件来源"); err != nil {
		return err
	}
	for _, target := range c.CommandProxyTargets {
		if target != ProxyTargetCodex && target != ProxyTargetGit && target != ProxyTargetMeegle {
			return fmt.Errorf("%s命令代理目标不合法：%s", productprofile.ConfigEditionLabel, target)
		}
	}
	return nil
}

func (c *AppConfig) NormalizeAndValidate() error {
	c.CommandProxyTargets = NormalizeCommandProxyTargets(c.CommandProxyTargets)
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
	return c.Validate()
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

func (g GroupConfig) Validate() error {
	if g.ID == "" || strings.TrimSpace(g.Name) == "" || !safeDirectoryIdentifier(g.ID) {
		return fmt.Errorf("项目组 ID 或名称不合法")
	}
	return nil
}
func (m TaskManifest) Validate() error {
	if !productprofile.AcceptsEdition(m.Edition) {
		return fmt.Errorf("%s", productprofile.ManifestEditionError())
	}
	if !ValidateSchemaVersion(m.SchemaVersion, CurrentTaskManifestSchemaVersion) {
		return fmt.Errorf("任务 JSON 版本不受支持：%s", m.SchemaVersion)
	}
	if m.FolderName == "" || m.TaskDirectoryName == "" || m.CreatedAt == "" || m.UpdatedAt == "" {
		return fmt.Errorf("任务清单缺少必填字段")
	}
	if !safeDirectoryIdentifier(m.TaskDirectoryName) {
		return fmt.Errorf("任务目录名不安全")
	}
	if m.AgentContext != nil {
		return m.AgentContext.Validate()
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

func (c AppConfig) RequirementMaterialsConfigured() bool {
	return c.RequirementMaterialsRoot != nil && *c.RequirementMaterialsRoot != "" && c.RequirementMaterialsSubdirectory != nil && *c.RequirementMaterialsSubdirectory != ""
}

func (s CodexPluginMarketplaceSource) RequiresRootMarketplaceMigration() bool {
	return s.MarketplaceDirectory != "."
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

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func validProxyTarget(v CommandProxyTarget) bool {
	return v == ProxyTargetCodex || v == ProxyTargetGit || v == ProxyTargetMeegle
}
