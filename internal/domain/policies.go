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
	// Project identities come from the user's configured Meegle projects.
	return &FeishuWorkItemLink{Space: space, Kind: strings.ToLower(match[2]), WorkItemID: match[3]}
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
