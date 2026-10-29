// Package requirementprovider adapts requirement source protocols to the local
// materials consumer. Documents never constructs commands or decodes source JSON.
package requirementprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/process"
)

type CommandRunner interface {
	Run(context.Context, process.Spec) (process.Result, error)
}

type Meegle struct{ runner CommandRunner }

func NewMeegle(runner CommandRunner) *Meegle {
	if runner == nil {
		runner = process.NewRunner()
	}
	return &Meegle{runner: runner}
}

var _ documents.RequirementProvider = (*Meegle)(nil)

func referenceLink(ref documents.RequirementReference) *domain.FeishuWorkItemLink {
	if ref.Identity == nil {
		return nil
	}
	return &domain.FeishuWorkItemLink{Space: ref.Identity.Space, Kind: ref.Identity.Kind, WorkItemID: ref.WorkItemID, ProjectKey: ref.ProjectKey}
}

func (s *Meegle) Resolve(ctx context.Context, config domain.AppConfig, ref documents.RequirementReference) (documents.RequirementIdentity, error) {
	link := referenceLink(ref)
	project, err := s.resolveRequirementProject(ctx, config, ref.WorkItemID, link)
	if err != nil {
		return documents.RequirementIdentity{}, err
	}
	if project == nil {
		if len(config.MeegleProjects) == 0 {
			return documents.RequirementIdentity{}, &documents.RequirementUnmatchedError{Reason: "未配置 Meegle 项目"}
		}
		return documents.RequirementIdentity{}, &documents.RequirementUnmatchedError{Reason: "未能唯一匹配需求所属的 Meegle 项目"}
	}
	identity := documents.RequirementIdentity{Space: project.SimpleName, WorkItemID: ref.WorkItemID}
	if link != nil {
		identity.Kind = link.Kind
	}
	return identity, nil
}

func (s *Meegle) Title(ctx context.Context, config domain.AppConfig, ref documents.RequirementReference) (string, error) {
	link := referenceLink(ref)
	if link == nil {
		return "", fmt.Errorf("需求链接不是支持的飞书项目工作项链接")
	}
	projectKey, err := resolveMeegleProjectKey(config, *link)
	if err != nil {
		return "", err
	}
	return s.fetchRequirementTitle(ctx, config, fmt.Sprintf("https://project.feishu.cn/%s/%s/detail/%s", link.Space, link.Kind, link.WorkItemID), projectKey), nil
}

func (s *Meegle) resolveRequirementProject(ctx context.Context, config domain.AppConfig, id string, link *domain.FeishuWorkItemLink) (*domain.MeegleProjectConfig, error) {
	candidates := make([]domain.MeegleProjectConfig, 0, len(config.MeegleProjects))
	for _, project := range config.MeegleProjects {
		if link == nil || strings.EqualFold(project.SimpleName, link.Space) {
			candidates = append(candidates, project)
		}
	}
	if len(candidates) == 0 {
		if link != nil {
			return nil, fmt.Errorf("需求链接所属的飞书项目未配置：%s", link.Space)
		}
		return nil, nil
	}
	timeout := 20 * time.Second
	var matches []domain.MeegleProjectConfig
	for _, project := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result, err := s.runMeegle(ctx, config, []string{"workitem", "get", "--project-key", project.ProjectKey, "--work-item-id", id, "--format", "json"}, timeout)
		if err != nil {
			return nil, err
		}
		if !result.Successful() {
			message := strings.TrimSpace(result.Stderr)
			if message == "" {
				message = strings.TrimSpace(result.Stdout)
			}
			if missingRequirement(message) {
				continue
			}
			if runes := []rune(message); len(runes) > 300 {
				message = string(runes[:300])
			}
			message = commandruntime.Redact(config, message)
			return nil, fmt.Errorf("Meegle CLI 执行失败：%s", message)
		}
		var raw any
		decoder := json.NewDecoder(strings.NewReader(result.Stdout))
		decoder.UseNumber()
		if err := decoder.Decode(&raw); err != nil {
			return nil, fmt.Errorf("Meegle CLI 返回不是可解析的 JSON")
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return nil, fmt.Errorf("Meegle CLI 返回不是可解析的 JSON")
		}
		if containsWorkItemID(raw, id) {
			matches = append(matches, project)
		}
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("同一需求编号匹配到多个 Meegle 项目")
	}
	if len(matches) == 1 {
		return &matches[0], nil
	}
	return nil, nil
}

func containsWorkItemID(value any, id string) bool {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), " ", ""))
			if normalized == "itemid" || normalized == "workitemid" {
				if typedText(child) == id {
					return true
				}
			}
			if containsWorkItemID(child, id) {
				return true
			}
		}
	case []any:
		for _, child := range current {
			if containsWorkItemID(child, id) {
				return true
			}
		}
	}
	return false
}

func typedText(value any) string {
	switch current := value.(type) {
	case string:
		return strings.TrimSpace(current)
	case json.Number:
		return current.String()
	case float64:
		return strings.TrimSpace(fmt.Sprint(current))
	case bool:
		return fmt.Sprint(current)
	case map[string]any:
		for _, key := range []string{"long_value", "string_value", "text_value", "bool_value", "label"} {
			if child, ok := current[key]; ok {
				if result := typedText(child); result != "" {
					return result
				}
			}
		}
		if values, ok := current["key_label_value_list"].([]any); ok && len(values) > 0 {
			return typedText(values[0])
		}
	}
	return ""
}

func (s *Meegle) fetchRequirementTitle(ctx context.Context, config domain.AppConfig, requirementLink, projectKey string) string {
	if s.runner == nil {
		return ""
	}
	link := domain.ParseFeishuWorkItemLink(requirementLink)
	if link == nil {
		return ""
	}
	result, err := s.runMeegle(ctx, config, []string{"workitem", "get", "--project-key", projectKey, "--work-item-id", link.WorkItemID, "--format", "json"}, 8*time.Second)
	if err != nil || !result.Successful() {
		return ""
	}
	var root map[string]json.RawMessage
	if json.Unmarshal([]byte(result.Stdout), &root) != nil {
		return ""
	}
	var attributes map[string]json.RawMessage
	_ = json.Unmarshal(root["work_item_attribute"], &attributes)
	for _, candidate := range []json.RawMessage{root["name"], root["work_item_name"], attributes["name"], attributes["work_item_name"]} {
		if title := jsonPrimitiveText(candidate); title != "" {
			return title
		}
	}
	return ""
}

func resolveMeegleProjectKey(config domain.AppConfig, link domain.FeishuWorkItemLink) (string, error) {
	for _, project := range config.MeegleProjects {
		if strings.EqualFold(project.SimpleName, link.Space) {
			return project.ProjectKey, nil
		}
	}
	if link.ProjectKey != nil {
		return *link.ProjectKey, nil
	}
	return "", fmt.Errorf("需求空间 %s 未配置 Meegle project key", link.Space)
}

func jsonPrimitiveText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.TrimSpace(value)
	}
	var primitive any
	if json.Unmarshal(raw, &primitive) != nil {
		return ""
	}
	switch value := primitive.(type) {
	case float64:
		return strings.TrimSpace(fmt.Sprint(value))
	case bool:
		return strings.TrimSpace(fmt.Sprint(value))
	default:
		return ""
	}
}

// Both project verification and optional title lookup share the desktop CLI
// runtime. A reused directory deliberately requires neither remote lookup.
func (s *Meegle) runMeegle(ctx context.Context, config domain.AppConfig, args []string, timeout time.Duration) (process.Result, error) {
	configured := ""
	if config.MeegleExecutablePath != nil {
		configured = strings.TrimSpace(*config.MeegleExecutablePath)
	}
	spec, err := commandruntime.Prepare(ctx, s.runner, &config, domain.ProxyTargetMeegle, executable.ToolMeegle, configured)
	if err != nil {
		return process.Result{}, err
	}
	spec.Arguments, spec.Timeout = args, timeout
	result, err := s.runner.Run(ctx, spec)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		return result, fmt.Errorf("Meegle CLI 执行失败：%s", commandruntime.Redact(config, err.Error()))
	}
	return result, nil
}

func missingRequirement(message string) bool {
	lower := strings.ToLower(message)
	if strings.Contains(lower, "node") || strings.Contains(lower, "env:") || strings.Contains(lower, "command not found") || strings.Contains(lower, "no such file") {
		return false
	}
	explicitItem := strings.Contains(lower, "workitem") || strings.Contains(lower, "work item") || strings.Contains(lower, "work-item") || strings.Contains(message, "需求") || strings.Contains(message, "工作项")
	return strings.Contains(lower, "404") || explicitItem && (strings.Contains(lower, "not found") || strings.Contains(lower, "not exist") || strings.Contains(message, "不存在"))
}
