package production

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/networkproxy"
	"silverwing-go/internal/snowball/genbuapi"
)

const genbuOutputLimit = 4 << 20

var ErrGenbuApprovalUnknown = errors.New("Genbu 审批结果待核实，请刷新详情或到 Genbu 核对")

type GenbuTagItem struct {
	Tag       string `json:"tag"`
	CreatedAt string `json:"created_at"`
}

type GenbuTagPage struct {
	Service  string         `json:"service"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
	Total    int            `json:"total"`
	Items    []GenbuTagItem `json:"items"`
}

type GenbuApprovalListRequest struct {
	Environment string
	Service     string
	Status      string
	Type        string
	StartDate   string
	EndDate     string
	Scope       string
	DateKind    string
	Page        int
	PageSize    int
}

type GenbuApprovalItem struct {
	ID          int64  `json:"id"`
	Service     string `json:"service"`
	Environment string `json:"environment"`
	Status      string `json:"status"`
	Creator     string `json:"creator"`
	CreatedAt   string `json:"created_at"`
	Approver    string `json:"approver"`
	ApprovedAt  string `json:"approved_at"`
	Result      string `json:"result"`
}

type GenbuApprovalPage struct {
	Page     int                 `json:"page"`
	PageSize int                 `json:"page_size"`
	Total    int                 `json:"total"`
	Items    []GenbuApprovalItem `json:"items"`
}

type GenbuApprovalDetail struct {
	GenbuApprovalItem
	Details        map[string]string           `json:"details"`
	FileNames      []string                    `json:"file_names"`
	Changes        []GenbuApprovalConfigChange `json:"-"`
	ComparisonNote string                      `json:"comparison_note,omitempty"`
	HasComparison  bool                        `json:"-"`
	CanApprove     bool                        `json:"can_approve"`
	Revision       string                      `json:"-"`
}

type GenbuApprovalConfigChange struct {
	File, Before, After       string
	BeforeExists, AfterExists bool
}

func (g NativeGenbu) BuildLog(ctx context.Context, cfg domain.AppConfig, service, tag string, tail int) (string, error) {
	service, tag = strings.TrimSpace(service), strings.TrimSpace(tag)
	if !genbuName(service) || !genbuName(tag) {
		return "", errors.New("请输入有效的 Genbu 服务名和完整 Tag")
	}
	if tail == 0 {
		tail = genbuapi.DefaultBuildLogTailLines
	}
	if tail < genbuapi.MinBuildLogTailLines || tail > genbuapi.MaxBuildLogTailLines {
		return "", errors.New("构建日志行数必须在 1 到 1000 之间")
	}
	ctx, client, err := g.client(ctx, cfg)
	if err != nil {
		return "", err
	}
	result, err := client.TagBuildLog(ctx, genbuapi.BuildLogRequest{Service: service, Tag: tag, TailLines: tail})
	if scopeErr := checkGenbuReadScope(ctx); scopeErr != nil {
		return "", scopeErr
	}
	if err != nil {
		return "", genbuReadError(ctx, cfg, client, err)
	}
	return genbuDisplayText(cfg, result.Content, "暂无构建日志")
}

func (g NativeGenbu) TagList(ctx context.Context, cfg domain.AppConfig, service, keyword string, page int) (GenbuTagPage, error) {
	service, keyword = strings.TrimSpace(service), strings.TrimSpace(keyword)
	if !genbuName(service) || keyword != "" && !genbuIdentifier(keyword) {
		return GenbuTagPage{}, errors.New("请输入有效的 Genbu 服务名和 Tag 关键词")
	}
	ctx, client, err := g.client(ctx, cfg)
	if err != nil {
		return GenbuTagPage{}, err
	}
	wire, err := client.ListTags(ctx, genbuapi.TagListRequest{Service: service, Keyword: keyword, Page: max(page, 1)})
	if scopeErr := checkGenbuReadScope(ctx); scopeErr != nil {
		return GenbuTagPage{}, scopeErr
	}
	if err != nil {
		return GenbuTagPage{}, genbuReadError(ctx, cfg, client, err)
	}
	result := GenbuTagPage{
		Service: wire.Service, Page: wire.Page, PageSize: wire.PageSize, Total: wire.Total,
		Items: make([]GenbuTagItem, 0, len(wire.Items)),
	}
	for _, item := range wire.Items {
		if !genbuName(item.Tag) {
			return GenbuTagPage{}, errors.New("Genbu Tag 列表包含无效 Tag")
		}
		result.Items = append(result.Items, GenbuTagItem(item))
	}
	return result, nil
}

func (g NativeGenbu) ApprovalList(ctx context.Context, cfg domain.AppConfig, request GenbuApprovalListRequest) (GenbuApprovalPage, error) {
	env, err := serviceEnvironment(request.Environment)
	if err != nil {
		return GenbuApprovalPage{}, err
	}
	service := strings.TrimSpace(request.Service)
	if !genbuName(service) {
		return GenbuApprovalPage{}, errors.New("请先配置有效的 Genbu 服务名")
	}
	ctx, client, err := g.client(ctx, cfg)
	if err != nil {
		return GenbuApprovalPage{}, err
	}
	wire, err := client.ListApprovals(ctx, genbuapi.ApprovalListRequest{
		Environment: env, Service: service, Status: request.Status, Type: request.Type,
		StartDate: request.StartDate, EndDate: request.EndDate, Scope: request.Scope,
		DateKind: request.DateKind, Page: max(request.Page, 1), PageSize: request.PageSize,
	})
	if scopeErr := checkGenbuReadScope(ctx); scopeErr != nil {
		return GenbuApprovalPage{}, scopeErr
	}
	if err != nil {
		return GenbuApprovalPage{}, genbuReadError(ctx, cfg, client, err)
	}
	result := GenbuApprovalPage{
		Page: wire.Page, PageSize: wire.PageSize, Total: wire.Total,
		Items: make([]GenbuApprovalItem, 0, len(wire.Items)),
	}
	canonical := genbuapi.ResolveService(service).Name
	// The server uses a keyword filter. Keep its total for pagination and show exact service matches.
	for _, item := range wire.Items {
		approval := GenbuApprovalItem(item)
		if !normalizeGenbuApproval(&approval, env) {
			return GenbuApprovalPage{}, errors.New("Genbu 返回的审批记录不符合查询环境")
		}
		if approval.Service == canonical {
			result.Items = append(result.Items, approval)
		}
	}
	return result, nil
}

func (g NativeGenbu) ApprovalDetail(ctx context.Context, cfg domain.AppConfig, env, service string, id int64) (GenbuApprovalDetail, error) {
	env, err := serviceEnvironment(env)
	if err != nil {
		return GenbuApprovalDetail{}, err
	}
	service = strings.TrimSpace(service)
	if !genbuName(service) || id < 1 {
		return GenbuApprovalDetail{}, errors.New("请选择有效的 Genbu 服务和审批编号")
	}
	ctx, client, err := g.client(ctx, cfg)
	if err != nil {
		return GenbuApprovalDetail{}, err
	}
	wire, err := client.ApprovalDetail(ctx, genbuapi.ApprovalDetailRequest{ID: id, Service: service, IncludeConfigReview: env == "uat"})
	if scopeErr := checkGenbuReadScope(ctx); scopeErr != nil {
		return GenbuApprovalDetail{}, scopeErr
	}
	if err != nil {
		return GenbuApprovalDetail{}, genbuReadError(ctx, cfg, client, err)
	}
	return genbuApprovalDetail(cfg, wire, env, service, id)
}

func (g NativeGenbu) ReadConfigApproval(ctx context.Context, cfg domain.AppConfig, env, service string, id int64, includeDiff bool) (GenbuApprovalDetail, error) {
	env, err := serviceEnvironment(env)
	if err != nil {
		return GenbuApprovalDetail{}, err
	}
	service = strings.TrimSpace(service)
	if !genbuName(service) || id < 1 {
		return GenbuApprovalDetail{}, errors.New("请选择有效的 Genbu 服务和审批编号")
	}
	ctx, client, err := g.client(ctx, cfg)
	if err != nil {
		return GenbuApprovalDetail{}, err
	}
	wire, err := client.ReadConfigApproval(ctx, genbuapi.ConfigApprovalReadRequest{
		ID: id, Service: service, Environment: env, IncludeDiff: includeDiff,
	})
	if scopeErr := checkGenbuReadScope(ctx); scopeErr != nil {
		return GenbuApprovalDetail{}, scopeErr
	}
	if err != nil {
		return GenbuApprovalDetail{}, genbuReadError(ctx, cfg, client, err)
	}
	return genbuApprovalDetail(cfg, wire, env, service, id)
}

func (g NativeGenbu) ApproveUATConfigChange(ctx context.Context, cfg domain.AppConfig, env, service string, id int64, revision string) (GenbuApprovalDetail, error) {
	if env != "uat" || !genbuName(service) || id < 1 {
		return GenbuApprovalDetail{}, errors.New("请选择有效的 UAT 配置审批")
	}
	ctx, client, err := g.client(ctx, cfg)
	if err != nil {
		return GenbuApprovalDetail{}, err
	}
	wire, err := client.ApproveUATConfigChange(ctx, genbuapi.ApprovalConfigApproveRequest{ID: id, Service: service, Revision: revision})
	if err != nil && !errors.Is(err, genbuapi.ErrApprovalOutcomeUnknown) {
		return GenbuApprovalDetail{}, genbuReadError(ctx, cfg, client, err)
	}
	detail, detailErr := genbuApprovalDetail(cfg, wire, env, service, id)
	if err != nil || detailErr != nil {
		return detail, ErrGenbuApprovalUnknown
	}
	return detail, nil
}

func genbuApprovalDetail(cfg domain.AppConfig, wire genbuapi.ApprovalDetailResult, env, service string, id int64) (GenbuApprovalDetail, error) {
	approval := GenbuApprovalItem(wire.ApprovalItem)
	if approval.ID != id || !normalizeGenbuApproval(&approval, env) {
		return GenbuApprovalDetail{}, errors.New("Genbu 返回的审批编号或环境与查询不匹配")
	}
	if approval.Service != genbuapi.ResolveService(service).Name {
		return GenbuApprovalDetail{}, errors.New("Genbu 返回的审批服务与查询不匹配")
	}
	result := GenbuApprovalDetail{GenbuApprovalItem: approval, Details: map[string]string{}, FileNames: []string{}}
	if genbuApprovalText(wire.ReviewError) {
		result.ComparisonNote = networkproxy.Redact(cfg, wire.ReviewError)
	}
	if review := wire.Review; review != nil {
		result.HasComparison = true
		if env == "uat" {
			result.CanApprove, result.Revision = review.CanApprove, review.Revision
		}
		result.Details["命名空间"], result.Details["ConfigMap"] = review.Namespace, review.ConfigMap
		for _, file := range review.Files {
			result.FileNames = append(result.FileNames, file.Name)
			result.Changes = append(result.Changes, GenbuApprovalConfigChange{
				File: file.Name, Before: networkproxy.Redact(cfg, file.Before), After: networkproxy.Redact(cfg, file.After),
				BeforeExists: file.BeforeExists, AfterExists: file.AfterExists,
			})
		}
	}
	for label := range wire.Details {
		if match := genbuApprovalFileLabel.FindStringSubmatch(label); match != nil && genbuIdentifier(match[1]) {
			result.FileNames = append(result.FileNames, match[1])
		}
	}
	if !result.HasComparison {
		for _, name := range wire.FileNames {
			if genbuIdentifier(name) {
				result.FileNames = append(result.FileNames, networkproxy.Redact(cfg, name))
			}
		}
		for _, label := range []string{"命名空间", "ConfigMap"} {
			if value := wire.Details[label]; genbuIdentifier(value) {
				result.Details[label] = networkproxy.Redact(cfg, value)
			}
		}
	}
	sort.Strings(result.FileNames)
	for _, label := range []string{"工单号", "钉钉审批号"} {
		if value := wire.Details[label]; genbuIdentifier(value) {
			result.Details[label] = value
		}
	}
	for _, label := range []string{"审批备注", "执行消息"} {
		if value := wire.Details[label]; genbuApprovalText(value) {
			result.Details[label] = networkproxy.Redact(cfg, value)
		}
	}
	return result, nil
}

var genbuApprovalFileLabel = regexp.MustCompile(`^配置文件 [1-9][0-9]* \(([^\r\n]+)\)$`)

func normalizeGenbuApproval(item *GenbuApprovalItem, env string) bool {
	item.Environment = genbuResponseEnvironment(item.Environment)
	return item.Environment == env && item.ID > 0 && genbuName(item.Service)
}

func genbuResponseEnvironment(environment string) string {
	environment = strings.ToLower(environment)
	if environment == "prd" {
		return "prod"
	}
	return environment
}

func genbuIdentifier(value string) bool {
	return value != "" && len(value) <= 512 && utf8.ValidString(value) &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

func genbuName(value string) bool {
	return genbuIdentifier(value) && !strings.HasPrefix(value, "-")
}

func genbuApprovalText(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= 4096 && utf8.ValidString(value) &&
		strings.IndexFunc(value, func(char rune) bool {
			return unicode.IsControl(char) && char != '\r' && char != '\n' && char != '\t'
		}) < 0
}

func genbuDisplayText(cfg domain.AppConfig, content, empty string) (string, error) {
	if len(content) > genbuOutputLimit {
		return "", errors.New("Genbu 查询结果超过 4 MiB")
	}
	if content == "" {
		content = empty
	}
	content = networkproxy.Redact(cfg, content)
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content, nil
}
