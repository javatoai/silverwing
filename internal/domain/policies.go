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
	folderName := suggestion.FolderName
	if strings.TrimSpace(folderName) != folderName {
		return suggestion, fmt.Errorf("AI 生成的文件夹名不能包含首尾空白")
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

	return suggestion, nil
}
