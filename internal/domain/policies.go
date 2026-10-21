package domain

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var requirementLinkPattern = regexp.MustCompile(`(?i)^https?://project\.feishu\.cn/([A-Za-z0-9_-]+)/(userstory|othertask|bug|technical)/detail/(\d+)(?:[/?#].*)?$`)
var requirementNumberPattern = regexp.MustCompile(`\d+`)
var aiBranchSuffixPattern = regexp.MustCompile(`^[a-z0-9]+(?:_[a-z0-9]+){0,3}$`)

type FeishuWorkItemLink struct {
	Space      string
	Kind       string
	WorkItemID string
	ProjectKey *string
}

func ParseFeishuWorkItemLink(value string) *FeishuWorkItemLink {
	normalized := trimRequirementPunctuation(strings.TrimSpace(value))
	match := requirementLinkPattern.FindStringSubmatch(normalized)
	if match == nil {
		return nil
	}
	space := strings.ToLower(match[1])
	var projectKey *string
	switch space {
	case "obt":
		v := "67c17e40bf0d47db9549cb08"
		projectKey = &v
	case "rta":
		v := "680b2de0f1ddf8d50a24dff8"
		projectKey = &v
	}
	return &FeishuWorkItemLink{Space: space, Kind: strings.ToLower(match[2]), WorkItemID: match[3], ProjectKey: projectKey}
}

func trimRequirementPunctuation(value string) string {
	return strings.TrimRight(value, ",，。;；")
}

func ResolveBranchPrefix(prefix, requirement string) *string {
	if !strings.Contains(prefix, "{num}") {
		value := prefix
		return &value
	}
	number := ExtractRequirementNumber(requirement)
	if number == "" {
		return nil
	}
	value := strings.ReplaceAll(prefix, "{num}", number)
	return &value
}

func ContainsUnresolvedBranchPlaceholder(branch string) bool {
	return strings.Contains(branch, "{num}") || strings.Contains(branch, "{ai}")
}

func ExtractRequirementNumber(requirement string) string {
	if link := ParseFeishuWorkItemLink(requirement); link != nil {
		return link.WorkItemID
	}
	trimmed := trimRequirementPunctuation(strings.TrimSpace(requirement))
	path := trimmed
	if parsed, err := url.Parse(trimmed); err == nil && (strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")) {
		path = parsed.Path
	}
	matches := requirementNumberPattern.FindAllString(path, -1)
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1]
}

func IsHTTPURL(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || !(strings.HasPrefix(strings.ToLower(trimmed), "http://") || strings.HasPrefix(strings.ToLower(trimmed), "https://")) {
		return false
	}
	parsed, err := url.Parse(trimmed)
	return err == nil && parsed.Scheme != "" && parsed.Hostname() != ""
}

func CanonicalOrNormalizedPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		if absolute, absErr := filepath.Abs(resolved); absErr == nil {
			return filepath.Clean(absolute)
		}
		return filepath.Clean(resolved)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(absolute)
}

func RequirementAiNamingModelRequireValid(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("AI 命名模型不能为空")
	}
	if strings.TrimSpace(value) != value {
		return "", fmt.Errorf("AI 命名模型不能包含首尾空白")
	}
	if strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return "", fmt.Errorf("AI 命名模型不能包含空白字符")
	}
	return value, nil
}

const (
	MaxAIFolderCodePoints = 12
	MaxAIFolderHanChars   = 6
	MaxAIBranchWords      = 4
)

func ValidateRequirementAiNamingSuggestion(suggestion RequirementAiNamingSuggestion) (RequirementAiNamingSuggestion, error) {
	folderName, branchSuffix := suggestion.FolderName, suggestion.BranchSuffix
	if strings.TrimSpace(folderName) != folderName {
		return suggestion, fmt.Errorf("AI 生成的文件夹名不能包含首尾空白")
	}
	if strings.TrimSpace(branchSuffix) != branchSuffix {
		return suggestion, fmt.Errorf("AI 生成的分支后缀不能包含首尾空白")
	}
	if err := DirectoryNameValidationError(folderName); err != nil {
		return suggestion, err
	}
	runes := []rune(folderName)
	if len(runes) > MaxAIFolderCodePoints {
		return suggestion, fmt.Errorf("AI 生成的文件夹名不能超过 %d 个字符", MaxAIFolderCodePoints)
	}
	hanCount := 0
	for _, r := range runes {
		if unicode.Is(unicode.Han, r) {
			hanCount++
		}
	}
	if hanCount < 1 || hanCount > MaxAIFolderHanChars {
		return suggestion, fmt.Errorf("AI 生成的文件夹名必须含有 1 到 %d 个汉字", MaxAIFolderHanChars)
	}
	if hanCount < len(runes)-hanCount {
		return suggestion, fmt.Errorf("AI 生成的文件夹名应以中文为主")
	}
	if !aiBranchSuffixPattern.MatchString(branchSuffix) {
		return suggestion, fmt.Errorf("AI 生成的分支后缀只能包含小写英文、数字和下划线，且最多 %d 个单词", MaxAIBranchWords)
	}
	return suggestion, nil
}

func ComposeAIRequirementBranch(resolvedPrefix, branchSuffix string) string {
	return strings.ReplaceAll(resolvedPrefix, "{ai}", branchSuffix)
}

func TagNavigationVisible(config AppConfig) bool {
	return config.TagNavigationVisible()
}

func GroupBranchPrefixOnChange(currentBranch, previousPrefix string, manuallyEdited bool, nextPrefix string) string {
	if !manuallyEdited || currentBranch == previousPrefix {
		return nextPrefix
	}
	return currentBranch
}

func ModuleDisplayName(configuredName, serviceName, baseRef string, moduleCount int) string {
	configured := strings.TrimSpace(configuredName)
	if configured == "" || (moduleCount == 1 && configured == "default") {
		if moduleCount == 1 {
			return serviceName
		}
		trimmedBase := strings.TrimRight(baseRef, "/")
		if index := strings.LastIndexByte(trimmedBase, '/'); index >= 0 {
			return trimmedBase[index+1:]
		}
		return trimmedBase
	}
	return configured
}

type GitWritePolicy struct{ blocked map[string]struct{} }

func NewGitWritePolicy(blockedBranches []string) GitWritePolicy {
	blocked := make(map[string]struct{}, len(blockedBranches))
	for _, branch := range blockedBranches {
		if normalized := strings.ToLower(strings.TrimSpace(branch)); normalized != "" {
			blocked[normalized] = struct{}{}
		}
	}
	return GitWritePolicy{blocked: blocked}
}

func (p GitWritePolicy) IsBlocked(branch string) bool {
	_, found := p.blocked[strings.ToLower(strings.TrimSpace(branch))]
	return found
}

func (p GitWritePolicy) RequireAllowed(branch, operation string) error {
	if p.IsBlocked(branch) {
		return fmt.Errorf("分支 %s 已被 Git 写保护，禁止%s", branch, operation)
	}
	return nil
}

type EffectiveTagTarget struct {
	Workspace        ServiceWorkspace
	SourceBranch     string
	Remote           string
	TargetBranch     *string
	TagMessagePrefix string
	Mode             TagBuildMode
}

// A name alone is not evidence of a published Tag: candidate names are saved
// before creation/push. ASCII whitespace is invalid in Git refs and also
// excludes the reader's "尚未生成 Tag" placeholder without restricting Unicode.
func TagGenbuProbeEligible(state TagOperationState, tag string) bool {
	return (state == TagOperationSuccess || state == TagOperationTagPushed) &&
		strings.TrimSpace(tag) != "" && !strings.ContainsAny(tag, " \t\r\n\v\f\x00")
}

func ResolveEffectiveTagTarget(config AppConfig, manifest TaskManifest, selection string) (EffectiveTagTarget, error) {
	if !config.TagEnabled {
		return EffectiveTagTarget{}, fmt.Errorf("全局测试Tag已关闭")
	}
	workspace, err := requireTagWorkspace(config, manifest, selection)
	if err != nil {
		return EffectiveTagTarget{}, err
	}
	var targetBranch *string
	remote := workspace.PushRemote
	if workspace.TagMode == TagBuildMergeToTargetBranch {
		if workspace.TagTargetRef == nil || strings.TrimSpace(*workspace.TagTargetRef) == "" {
			return EffectiveTagTarget{}, fmt.Errorf("模块缺少测试目标分支")
		}
		ref, err := ParseRemoteBranchRef(*workspace.TagTargetRef)
		if err != nil {
			return EffectiveTagTarget{}, err
		}
		remote = ref.Remote
		branch := ref.Branch
		targetBranch = &branch
	}
	return EffectiveTagTarget{Workspace: workspace, SourceBranch: workspace.Branch, Remote: remote, TargetBranch: targetBranch, TagMessagePrefix: workspace.TagMessagePrefix, Mode: workspace.TagMode}, nil
}

func ResolveHistoricalTagTarget(config AppConfig, manifest TaskManifest, operation TagOperation) (EffectiveTagTarget, error) {
	selection := operation.GroupServiceID + ":" + operation.ModuleID
	workspace, err := requireTagWorkspace(config, manifest, selection)
	if err != nil {
		return EffectiveTagTarget{}, err
	}
	if operation.FolderName != manifest.FolderName || operation.RepositoryID != workspace.RepositoryID {
		return EffectiveTagTarget{}, fmt.Errorf("历史测试Tag与当前任务模块不匹配，无法恢复")
	}
	if operation.SourceBranch != workspace.Branch {
		return EffectiveTagTarget{}, fmt.Errorf("历史测试Tag的研发分支与当前工作区不一致，无法恢复")
	}
	if strings.TrimSpace(operation.Remote) == "" {
		return EffectiveTagTarget{}, fmt.Errorf("历史测试Tag缺少原远程，无法恢复；请新建测试Tag")
	}
	if strings.HasPrefix(operation.Remote, "-") {
		return EffectiveTagTarget{}, fmt.Errorf("历史测试Tag的原远程名称不合法，无法恢复")
	}
	branch := operation.SourceBranch
	var target *string
	if operation.TagMode == TagBuildMergeToTargetBranch {
		if operation.TargetBranch == nil || strings.TrimSpace(*operation.TargetBranch) == "" {
			return EffectiveTagTarget{}, fmt.Errorf("历史测试Tag缺少原目标分支，无法恢复；请新建测试Tag")
		}
		branch = *operation.TargetBranch
		target = &branch
	}
	if _, err := ParseRemoteBranchRef(operation.Remote + "/" + branch); err != nil {
		return EffectiveTagTarget{}, err
	}
	return EffectiveTagTarget{Workspace: workspace, SourceBranch: operation.SourceBranch, Remote: operation.Remote, TargetBranch: target, TagMessagePrefix: workspace.TagMessagePrefix, Mode: operation.TagMode}, nil
}

func requireTagWorkspace(config AppConfig, manifest TaskManifest, selection string) (ServiceWorkspace, error) {
	if !config.TagEnabled {
		return ServiceWorkspace{}, fmt.Errorf("全局测试Tag已关闭")
	}
	candidates := []ServiceWorkspace{}
	for _, workspace := range manifest.Services {
		if workspace.SelectionKey() == selection || workspace.RepositoryID == selection {
			candidates = append(candidates, workspace)
		}
	}
	if len(candidates) == 0 {
		return ServiceWorkspace{}, fmt.Errorf("任务中不存在测试Tag目标：%s", selection)
	}
	if len(candidates) > 1 {
		return ServiceWorkspace{}, fmt.Errorf("测试Tag目标不唯一，请选择具体模块：%s", selection)
	}
	workspace := candidates[0]
	if _, err := config.Group(manifest.GroupID); err != nil {
		return ServiceWorkspace{}, err
	}
	if !workspace.TagEnabled {
		return ServiceWorkspace{}, fmt.Errorf("模块 %s 已关闭测试Tag", workspace.ModuleName)
	}
	return workspace, nil
}
