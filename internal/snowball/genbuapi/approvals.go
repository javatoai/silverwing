package genbuapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	approvalPath                = "/genbu/basic/api/approve"
	ApprovalListDefaultPageSize = 20
	ApprovalListMaxPageSize     = 100
)

var approvalStatuses = [...]string{"待处理", "已通过", "已拒绝", "已撤销"}
var approvalResults = [...]string{"未处理", "处理成功", "处理失败", "处理中"}
var approvalTypes = [...]string{
	"环境参数修改", "Service 修改", "Ingress 修改", "其它信息修改",
	"数据变更", "Group 修改", "ConfigMap 修改", "ConfigMap 删除", "VirtualService 修改",
}

// ApprovalListRequest filters a page of approval records in the specified environment.
type ApprovalListRequest struct {
	Environment string `json:"environment"`
	Service     string `json:"service"`
	Keyword     string `json:"keyword"`
	Status      string `json:"status"`
	Scope       string `json:"scope"`
	DateKind    string `json:"date_kind"`
	StartDate   string `json:"start_date"`
	EndDate     string `json:"end_date"`
	Type        string `json:"type"`
	Page        int    `json:"page"`
	PageSize    int    `json:"page_size"`
}

// ApprovalListResult contains one server-paginated result set.
type ApprovalListResult struct {
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
	Total    int            `json:"total"`
	Items    []ApprovalItem `json:"items"`
}

// ApprovalItem contains the summary fields returned by the approval commands.
type ApprovalItem struct {
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

// ApprovalDetailRequest identifies one approval by its Genbu record number.
type ApprovalDetailRequest struct {
	ID int64 `json:"id"`
}

// ApprovalDetailResult includes the stored approval proposal and original configuration file contents.
type ApprovalDetailResult struct {
	ApprovalItem
	Details map[string]string `json:"details"`
}

// ListApprovals gets one filtered page from Genbu's approval management list.
func (client *Client) ListApprovals(ctx context.Context, request ApprovalListRequest) (ApprovalListResult, error) {
	query, err := approvalListQuery(request, client.now())
	if err != nil {
		return ApprovalListResult{}, err
	}
	page, _ := strconv.Atoi(query.Get("current"))
	pageSize, _ := strconv.Atoi(query.Get("size"))
	var result ApprovalListResult
	err = client.requestToken(ctx, func(worker *genbuClient, token string) error {
		rows, total, err := worker.queryApprovals(query, token)
		if err != nil {
			return err
		}
		secrets := []string{client.ConfigSnapshot().Password, token}
		items := make([]ApprovalItem, 0, len(rows))
		for _, row := range rows {
			item, err := approvalListItem(row, secrets...)
			if err != nil {
				return err
			}
			if item.Environment != query.Get("env") {
				return fmt.Errorf("Genbu 返回了不符合环境筛选条件的审批记录")
			}
			items = append(items, item)
		}
		result = ApprovalListResult{Page: page, PageSize: pageSize, Total: total, Items: items}
		return nil
	})
	return result, err
}

// ApprovalDetail locates the exact record in the Genbu approval list.
func (client *Client) ApprovalDetail(ctx context.Context, request ApprovalDetailRequest) (ApprovalDetailResult, error) {
	if request.ID < 1 {
		return ApprovalDetailResult{}, fmt.Errorf("审批编号必须为正整数")
	}
	var result ApprovalDetailResult
	err := client.requestToken(ctx, func(worker *genbuClient, token string) error {
		row, err := worker.findApproval(request.ID, token)
		if err != nil {
			return err
		}
		environment := strings.ToUpper(strings.TrimSpace(valueString(row, "env")))
		if environment != "UAT" && environment != "PRD" {
			return fmt.Errorf("该审批记录不在支持的环境范围内")
		}
		secrets := []string{client.ConfigSnapshot().Password, token}
		item, err := approvalListItem(row, secrets...)
		if err != nil {
			return err
		}
		result = ApprovalDetailResult{ApprovalItem: item, Details: approvalDetailFields(row, secrets...)}
		return nil
	})
	return result, err
}

func approvalListQuery(request ApprovalListRequest, now time.Time) (url.Values, error) {
	var environment string
	switch strings.ToLower(strings.TrimSpace(request.Environment)) {
	case "uat":
		environment = "UAT"
	case "prod":
		environment = "PRD"
	default:
		return nil, fmt.Errorf("必须指定环境 uat 或 prod")
	}
	if request.Page == 0 {
		request.Page = 1
	}
	if request.PageSize == 0 {
		request.PageSize = ApprovalListDefaultPageSize
	}
	if request.Page < 1 {
		return nil, fmt.Errorf("页码必须大于或等于 1")
	}
	if request.PageSize < 1 || request.PageSize > ApprovalListMaxPageSize {
		return nil, fmt.Errorf("每页条数必须在 1 到 %d 之间", ApprovalListMaxPageSize)
	}
	query := url.Values{
		"current": {strconv.Itoa(request.Page)},
		"size":    {strconv.Itoa(request.PageSize)},
		"env":     {environment},
	}
	if err := setApprovalDates(query, request, now); err != nil {
		return nil, err
	}
	if err := setApprovalFilters(query, request); err != nil {
		return nil, err
	}
	return query, nil
}

func setApprovalDates(query url.Values, request ApprovalListRequest, now time.Time) error {
	switch strings.ToLower(strings.TrimSpace(request.DateKind)) {
	case "", "created":
		query.Set("time_type", "1")
	case "approved":
		query.Set("time_type", "2")
	default:
		return fmt.Errorf("日期类型必须为 created 或 approved")
	}
	startDate, endDate := strings.TrimSpace(request.StartDate), strings.TrimSpace(request.EndDate)
	if startDate == "" && endDate == "" {
		localNow := now.In(time.FixedZone("Asia/Shanghai", 8*60*60))
		startDate, endDate = localNow.AddDate(0, 0, -6).Format(time.DateOnly), localNow.Format(time.DateOnly)
	}
	if startDate == "" || endDate == "" {
		return fmt.Errorf("开始日期和结束日期必须同时指定")
	}
	start, startErr := time.Parse(time.DateOnly, startDate)
	end, endErr := time.Parse(time.DateOnly, endDate)
	if startErr != nil || endErr != nil {
		return fmt.Errorf("日期必须采用有效的 YYYY-MM-DD 格式")
	}
	if end.Before(start) {
		return fmt.Errorf("开始日期不能晚于结束日期")
	}
	query.Set("date_begin", startDate)
	query.Set("date_end", endDate)
	return nil
}

func setApprovalFilters(query url.Values, request ApprovalListRequest) error {
	keyword, service := strings.TrimSpace(request.Keyword), strings.TrimSpace(request.Service)
	if keyword != "" && service != "" {
		return fmt.Errorf("服务和关键词只能指定一个")
	}
	if service != "" {
		keyword = resolveService(service).Name
	}
	if keyword != "" {
		query.Set("keyword", keyword)
	}
	switch strings.ToLower(strings.TrimSpace(request.Status)) {
	case "", "all":
	case "pending":
		query.Set("status", "0")
	case "approved":
		query.Set("status", "1")
	case "rejected":
		query.Set("status", "2")
	case "revoked":
		query.Set("status", "3")
	default:
		return fmt.Errorf("审批状态必须为 all、pending、approved、rejected 或 revoked")
	}
	switch strings.ToLower(strings.TrimSpace(request.Scope)) {
	case "", "all":
	case "submitted":
		query.Set("result_type", "1")
	case "reviewed":
		query.Set("result_type", "2")
	default:
		return fmt.Errorf("审批分类必须为 all、submitted 或 reviewed")
	}
	approvalType, err := approvalTypeCode(request.Type)
	if err != nil {
		return err
	}
	if approvalType != "" {
		query.Set("approve_type", approvalType)
	}
	return nil
}

// ValidateApprovalType checks a Genbu approval type filter without making a request.
func ValidateApprovalType(input string) error {
	_, err := approvalTypeCode(input)
	return err
}

func approvalTypeCode(input string) (string, error) {
	approvalType := strings.TrimSpace(input)
	if approvalType == "" || strings.EqualFold(approvalType, "all") {
		return "", nil
	}
	for index, name := range approvalTypes {
		code := strconv.Itoa(index + 1)
		if strings.EqualFold(approvalType, name) || approvalType == code {
			return code, nil
		}
	}
	return "", fmt.Errorf("审批类型必须为 all、1 到 9 或对应的类型名称")
}

func (client *genbuClient) queryApprovals(query url.Values, token string) ([]map[string]any, int, error) {
	endpoint := client.genbuURL + approvalPath + "?" + query.Encode()
	response, _, err := client.requestJSON(http.MethodGet, endpoint, nil, token, "")
	if err != nil {
		return nil, 0, err
	}
	envelope, ok := response.(map[string]any)
	if !ok {
		return nil, 0, fmt.Errorf("Genbu 返回了无效的审批列表")
	}
	total, err := valueInt(envelope, "total")
	if err != nil || total < 0 {
		return nil, 0, fmt.Errorf("Genbu 审批列表缺少有效的总记录数")
	}
	items, present := envelope["response"]
	if present && items == nil {
		return nil, total, nil
	}
	data, ok := items.([]any)
	if !ok {
		return nil, 0, fmt.Errorf("Genbu 返回了无效的审批记录列表")
	}
	rows := make([]map[string]any, 0, len(data))
	for _, item := range data {
		row, ok := item.(map[string]any)
		if !ok {
			return nil, 0, fmt.Errorf("Genbu 返回了无效的审批记录")
		}
		rows = append(rows, row)
	}
	return rows, total, nil
}

func (client *genbuClient) findApproval(id int64, token string) (map[string]any, error) {
	// The page opens the selected list row for details. Its keyword filter searches
	// external identifiers, so look up the local record number in descending ID order.
	const pageSize = ApprovalListDefaultPageSize
	query := url.Values{
		"size":      {strconv.Itoa(pageSize)},
		"time_type": {"1"}, "date_begin": {""}, "date_end": {""},
	}
	for attempt := 0; attempt < 2; attempt++ {
		low, high := 1, 1
		expectedTotal := -1
		for low <= high {
			page := low + (high-low)/2
			query.Set("current", strconv.Itoa(page))
			rows, total, err := client.queryApprovals(query, token)
			if err != nil {
				return nil, err
			}
			if expectedTotal < 0 {
				expectedTotal = total
				high = 1 + (total-1)/pageSize
			} else if total != expectedTotal {
				break
			}
			if total == 0 && len(rows) == 0 {
				return nil, fmt.Errorf("未找到审批编号 %d", id)
			}
			if len(rows) == 0 {
				return nil, fmt.Errorf("Genbu 审批列表页与总记录数不一致")
			}
			var first, last int64
			for _, row := range rows {
				candidate, err := approvalRecordID(row)
				if err != nil {
					return nil, err
				}
				if candidate == id {
					return row, nil
				}
				if first == 0 {
					first = candidate
				} else if candidate >= last {
					return nil, fmt.Errorf("Genbu 审批列表未按编号倒序返回")
				}
				last = candidate
			}
			switch {
			case id < last:
				low = page + 1
			case id > first:
				high = page - 1
			default:
				return nil, fmt.Errorf("未找到审批编号 %d", id)
			}
		}
		if low > high {
			return nil, fmt.Errorf("未找到审批编号 %d", id)
		}
	}
	return nil, fmt.Errorf("Genbu 审批列表在查询期间发生变化，请重试")
}

func approvalRecordID(row map[string]any) (int64, error) {
	id, err := strconv.ParseInt(valueString(row, "id"), 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("Genbu 返回的审批记录缺少有效编号")
	}
	return id, nil
}

func approvalListItem(row map[string]any, secrets ...string) (ApprovalItem, error) {
	id, err := approvalRecordID(row)
	if err != nil {
		return ApprovalItem{}, err
	}
	return ApprovalItem{
		ID:          id,
		Service:     redactValues(valueString(row, "app_name"), secrets...),
		Environment: strings.ToUpper(strings.TrimSpace(valueString(row, "env"))),
		Status:      approvalCodeLabel(row, "status", approvalStatuses[:]),
		Creator:     redactValues(valueString(row, "created_user"), secrets...),
		CreatedAt:   redactValues(valueString(row, "created_time"), secrets...),
		Approver:    redactValues(valueString(row, "approve_user"), secrets...),
		ApprovedAt:  redactValues(valueString(row, "approve_time"), secrets...),
		Result:      approvalCodeLabel(row, "result", approvalResults[:]),
	}, nil
}

func approvalCodeLabel(row map[string]any, field string, labels []string) string {
	code, err := valueInt(row, field)
	if err != nil || code < 0 || code >= len(labels) {
		return "未知"
	}
	return labels[code]
}
