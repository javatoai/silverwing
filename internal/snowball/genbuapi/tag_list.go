package genbuapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	TagListDefaultPageSize = 10
	TagListMaxPageSize     = 100
)

// TagListRequest selects a page of versions for one Genbu service.
type TagListRequest struct {
	Service   string `json:"service"`
	Keyword   string `json:"keyword"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	Page      int    `json:"page"`
	PageSize  int    `json:"page_size"`
}

// TagListResult represents one server-filtered page with its match count.
type TagListResult struct {
	Service  string        `json:"service"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
	Total    int           `json:"total"`
	Items    []TagListItem `json:"items"`
}

// TagListItem holds a listed tag and its creation time.
type TagListItem struct {
	Tag       string `json:"tag"`
	CreatedAt string `json:"created_at"`
}

// ListTags queries one page of versions in the order supplied by Genbu.
func (client *Client) ListTags(ctx context.Context, request TagListRequest) (TagListResult, error) {
	request, err := normalizeTagListRequest(request)
	if err != nil {
		return TagListResult{}, err
	}
	conditions := make(map[string]any)
	if request.Keyword != "" {
		conditions["key_word"] = request.Keyword
	}
	if request.StartDate != "" {
		// The version page sends both calendar dates at midnight in local time.
		conditions["created_date"] = []string{request.StartDate + " 00:00:00", request.EndDate + " 00:00:00"}
	}

	var result TagListResult
	err = client.requestToken(ctx, func(worker *genbuClient, token string) error {
		service, err := worker.resolveServiceContext(request.Service, token)
		if err != nil {
			return err
		}
		response, err := worker.queryVersions(token, service.AppID, request.Page, request.PageSize, conditions)
		if err != nil {
			return err
		}
		total, items, err := parseTagListPage(response)
		if err != nil {
			return err
		}
		result = TagListResult{Service: service.Name, Page: request.Page, PageSize: request.PageSize, Total: total, Items: items}
		return nil
	})
	return result, err
}

func normalizeTagListRequest(request TagListRequest) (TagListRequest, error) {
	request.Service = strings.TrimSpace(request.Service)
	if request.Service == "" {
		return TagListRequest{}, fmt.Errorf("必须指定服务名或简称")
	}
	request.Keyword = strings.TrimSpace(request.Keyword)
	if request.Page == 0 {
		request.Page = 1
	}
	if request.PageSize == 0 {
		request.PageSize = TagListDefaultPageSize
	}
	if request.Page < 1 {
		return TagListRequest{}, fmt.Errorf("页码必须大于或等于 1")
	}
	if request.PageSize < 1 || request.PageSize > TagListMaxPageSize {
		return TagListRequest{}, fmt.Errorf("每页条数必须在 1 到 %d 之间", TagListMaxPageSize)
	}
	request.StartDate = strings.TrimSpace(request.StartDate)
	request.EndDate = strings.TrimSpace(request.EndDate)
	if request.StartDate == "" && request.EndDate == "" {
		return request, nil
	}
	if request.StartDate == "" || request.EndDate == "" {
		return TagListRequest{}, fmt.Errorf("开始日期和结束日期必须同时指定")
	}
	start, startErr := time.Parse(time.DateOnly, request.StartDate)
	end, endErr := time.Parse(time.DateOnly, request.EndDate)
	if startErr != nil || endErr != nil {
		return TagListRequest{}, fmt.Errorf("日期必须采用有效的 YYYY-MM-DD 格式")
	}
	if end.Before(start) {
		return TagListRequest{}, fmt.Errorf("开始日期不能晚于结束日期")
	}
	return request, nil
}

func (client *genbuClient) queryVersions(token, appID string, page, pageSize int, conditions map[string]any) (any, error) {
	payload := map[string]any{
		"current":    page,
		"size":       pageSize,
		"conditions": conditions,
	}
	response, _, err := client.requestJSON(http.MethodPost, client.genbuURL+versionPath, payload, token, appID)
	return response, err
}

func parseTagListPage(response any) (int, []TagListItem, error) {
	envelope, ok := response.(map[string]any)
	if !ok {
		return 0, nil, fmt.Errorf("版本列表响应格式错误")
	}
	total, err := valueInt(envelope, "total")
	if err != nil || total < 0 {
		return 0, nil, fmt.Errorf("版本列表响应缺少有效总数")
	}
	rows, ok := envelope["response"].([]any)
	if !ok {
		return 0, nil, fmt.Errorf("版本列表响应缺少版本记录")
	}
	items := make([]TagListItem, 0, len(rows))
	for index, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok {
			return 0, nil, fmt.Errorf("版本列表第 %d 条记录格式错误", index+1)
		}
		tag, _ := row["version_name"].(string)
		if tag == "" {
			return 0, nil, fmt.Errorf("版本列表第 %d 条记录缺少版本名", index+1)
		}
		createdAt, _ := row["created_time"].(string)
		items = append(items, TagListItem{Tag: tag, CreatedAt: createdAt})
	}
	return total, items, nil
}
