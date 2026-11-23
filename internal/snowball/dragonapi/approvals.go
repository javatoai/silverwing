package dragonapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// CreateError reports whether a submission may have created an approval.
type CreateError struct {
	Err             error
	PossiblyCreated bool
}

func (e *CreateError) Error() string { return e.Err.Error() }
func (e *CreateError) Unwrap() error { return e.Err }

func (c *Client) Create(ctx context.Context, input CreateInput) (CreateResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()
	normalized, payload, err := c.prepare(ctx, input)
	if err != nil {
		return CreateResult{}, &CreateError{Err: err}
	}
	data, err := c.authenticated(ctx, http.MethodPost, approvalPath, payload)
	result := c.createReceipt(data, normalized)
	if err != nil {
		possiblyCreated := result.ApprovalID > 0 || !definitelyRejected(err)
		return result, createFailure(err, possiblyCreated)
	}
	if result.ApprovalID == 0 {
		return result, createFailure(errors.New("创建响应缺少有效审批编号"), true)
	}
	return result, nil
}

func (c *Client) createReceipt(data json.RawMessage, input CreateInput) CreateResult {
	var created struct {
		ID int64 `json:"approvalId"`
	}
	if json.Unmarshal(data, &created) != nil || created.ID <= 0 {
		return CreateResult{}
	}
	return CreateResult{ApprovalID: created.ID, URL: c.approvalDetailURL(created.ID), CreateInput: input}
}

func definitelyRejected(err error) bool {
	var local *requestStartError
	if errors.As(err, &local) {
		return true
	}
	var upstream *apiError
	if !errors.As(err, &upstream) {
		return false
	}
	return upstream.status >= 200 && upstream.status < 300 ||
		upstream.status >= 400 && upstream.status < 500 && upstream.status != http.StatusRequestTimeout
}

func createFailure(err error, possiblyCreated bool) error {
	if possiblyCreated {
		err = fmt.Errorf("%w；创建结果未确认，请先核对审批结果", err)
	}
	return &CreateError{Err: err, PossiblyCreated: possiblyCreated}
}

func (c *Client) approvalDetailURL(id int64) string {
	return c.config.DragonURL + "/#/workorder/detail/" + strconv.FormatInt(id, 10)
}

func (c *Client) prepare(ctx context.Context, input CreateInput) (CreateInput, map[string]any, error) {
	normalized, err := NormalizeCreateInput(input)
	if err != nil {
		return CreateInput{}, nil, err
	}
	data, err := c.authenticated(ctx, http.MethodGet, templatePath, nil)
	if err != nil {
		return CreateInput{}, nil, err
	}
	var template processTemplate
	if json.Unmarshal(data, &template) != nil {
		return CreateInput{}, nil, fmt.Errorf("审批模板响应格式错误")
	}
	payload, err := buildPayload(template, normalized)
	return normalized, payload, err
}

func buildPayload(template processTemplate, input CreateInput) (map[string]any, error) {
	input, err := NormalizeCreateInput(input)
	if err != nil {
		return nil, err
	}
	if template.Process.ID != TemplateID || template.StructureID <= 0 || template.Process.ClassifyID <= 0 || template.Process.Name != "提测审批" || len(template.Templates) != 1 {
		return nil, fmt.Errorf("当前提测审批模板结构不受支持，请核对模板 24")
	}
	form := template.Templates[0]
	list, ok := form.Content["list"].([]any)
	if !ok || len(list) != 1 || form.ID <= 0 {
		return nil, fmt.Errorf("提测审批表单结构已变化")
	}
	component, ok := list[0].(map[string]any)
	if !ok || component["el"] != "test-approval" || component["model"] != "testApproval" {
		return nil, fmt.Errorf("提测审批表单组件已变化")
	}
	var start *processNode
	for i := range template.Nodes {
		if template.Nodes[i].Class == "start" {
			if start != nil {
				return nil, fmt.Errorf("审批模板存在多个开始节点")
			}
			start = &template.Nodes[i]
		}
	}
	if start == nil || start.ID == "" || start.Label == "" {
		return nil, fmt.Errorf("审批模板缺少提交节点")
	}
	var outgoing []processEdge
	for _, edge := range template.Edges {
		if edge.Source == start.ID {
			outgoing = append(outgoing, edge)
		}
	}
	if len(outgoing) != 1 || outgoing[0].Label != "提交" {
		return nil, fmt.Errorf("审批模板提交路径已变化")
	}
	edge := outgoing[0]
	var target *processNode
	for i := range template.Nodes {
		if template.Nodes[i].ID == edge.Target {
			target = &template.Nodes[i]
			break
		}
	}
	if target == nil || target.ID == "" || target.Label == "" {
		return nil, fmt.Errorf("审批模板缺少提交目标节点")
	}
	state := map[string]any{"id": target.ID, "label": target.Label}
	if target.AssignType != "" {
		state["processMethod"] = target.AssignType
	}
	if target.AssignValue != nil {
		state["processor"] = uniqueTasks(target.AssignValue)
	}
	apps, _ := ResolveApplications(input.Apps)
	rows := make([]map[string]any, 0, len(apps))
	for _, app := range apps {
		rows = append(rows, map[string]any{"appId": app.AppID, "appName": app.AppName, "appCnName": app.AppCNName, "version": app.Version,
			"url": "", "name": "", "gitUrl": "", "projectSystem": ""})
	}
	formData := map[string]any{"testApproval": map[string]any{
		"type": testType, "env": testEnvironment, "businessLine": businessLine,
		"meegoCode": input.Meego, "remark": input.Remark, "overviewData": rows,
		"fileUrl": "", "fileName": "", "sqlData": []any{}, "codeUser": []any{}, "jiraCode": []any{},
		"onlineStepData": onlineSteps, "rollbackStepData": rollbackSteps, "externalDependencies": "", "internalDependencies": "",
	}}
	// Preserve the current server form definition, including its current form ID.
	encoded, err := json.Marshal(form.Content)
	if err != nil {
		return nil, fmt.Errorf("审批表单定义无法编码")
	}
	var content map[string]any
	if json.Unmarshal(encoded, &content) != nil {
		return nil, fmt.Errorf("审批表单定义无法读取")
	}
	content["id"] = form.ID
	cc := make(map[string][]int64)
	if start.CCApprover {
		cc["cc"+start.ID] = []int64{}
	}
	tasks := uniqueTasks(append(append([]json.RawMessage{}, template.Process.Tasks...), start.Tasks...))
	return map[string]any{
		"title": template.Process.Name, "priority": 1, "processTemplateId": TemplateID,
		"processTemplateStructureId": template.StructureID, "classifyId": template.Process.ClassifyID,
		"source": start.ID, "sourceState": start.Label, "processMethod": "", "state": []any{state},
		"tasks": tasks, "ccAddition": cc, "isExecTask": edge.IsExecuteTask,
		"tpls": map[string]any{"content": []any{content}, "formData": []any{formData}},
	}, nil
}

func uniqueTasks(tasks []json.RawMessage) []json.RawMessage {
	result := make([]json.RawMessage, 0, len(tasks))
	seen := make(map[string]bool)
	for _, task := range tasks {
		if !seen[string(task)] {
			result = append(result, task)
			seen[string(task)] = true
		}
	}
	return result
}

func (c *Client) Query(ctx context.Context, input QueryInput) (QueryResult, error) {
	if input.Scope == "" {
		input.Scope = "mine"
	}
	paths := map[string]string{"mine": "myCreate", "pending": "pending", "processed": "processed", "related": "related", "all": "all"}
	suffix, ok := paths[input.Scope]
	if !ok {
		return QueryResult{}, fmt.Errorf("scope 必须是 mine、pending、processed、related 或 all")
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.Size == 0 {
		input.Size = 10
	}
	if input.Page < 1 || input.Size < 1 || input.Size > 100 {
		return QueryResult{}, fmt.Errorf("page 必须大于零，size 必须在 1–100 之间")
	}
	query := url.Values{"page": {strconv.Itoa(input.Page)}, "size": {strconv.Itoa(input.Size)}, "processTemplateId": {strconv.Itoa(TemplateID)}}
	if input.No != "" {
		query.Set("no", input.No)
	}
	if input.Keyword != "" {
		query.Set("formData", input.Keyword)
	}
	if input.IsEnd != nil {
		if *input.IsEnd {
			query.Set("isEnd", "1")
		} else {
			query.Set("isEnd", "0")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()
	data, err := c.authenticated(ctx, http.MethodGet, approvalPath+"/"+suffix+"?"+query.Encode(), nil)
	if err != nil {
		return QueryResult{}, err
	}
	var page struct {
		Total int            `json:"total"`
		Page  int            `json:"page"`
		Size  int            `json:"size"`
		Data  []wireApproval `json:"data"`
	}
	if json.Unmarshal(data, &page) != nil || page.Page < 1 || page.Size < 1 || page.Total < 0 || page.Data == nil {
		return QueryResult{}, fmt.Errorf("审批列表响应格式错误")
	}
	result := QueryResult{Total: page.Total, Page: page.Page, Size: page.Size, Items: make([]Approval, 0, len(page.Data))}
	for _, item := range page.Data {
		if item.ApprovalID <= 0 {
			return QueryResult{}, fmt.Errorf("审批列表缺少有效审批 ID")
		}
		result.Items = append(result.Items, item.summary())
	}
	return result, nil
}

func (c *Client) Detail(ctx context.Context, id int64) (DetailResult, error) {
	if id <= 0 {
		return DetailResult{}, fmt.Errorf("审批 ID 必须是正整数")
	}
	ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()
	data, err := c.authenticated(ctx, http.MethodGet, approvalPath+"/"+strconv.FormatInt(id, 10), nil)
	if err != nil {
		return DetailResult{}, err
	}
	var detail struct {
		Nodes []processNode `json:"nodes"`
		Order wireApproval  `json:"workOrder"`
		Forms []struct {
			Data struct {
				Test struct {
					Meego  string `json:"meegoCode"`
					Remark string `json:"remark"`
					Apps   []struct {
						ID   string `json:"appId"`
						Name string `json:"appName"`
					} `json:"overviewData"`
				} `json:"testApproval"`
			} `json:"formData"`
		} `json:"tpls"`
		History []struct {
			State     string `json:"state"`
			Action    string `json:"circulation"`
			Processor string `json:"processorName"`
			Remark    string `json:"remarks"`
			CreatedAt string `json:"createdAt"`
		} `json:"circulationHistory"`
	}
	if json.Unmarshal(data, &detail) != nil || detail.Order.ApprovalID != id {
		return DetailResult{}, fmt.Errorf("审批详情响应格式错误或 ID 不匹配")
	}
	result := DetailResult{Approval: detail.Order.summary(), URL: c.approvalDetailURL(id), Apps: []string{}, History: []History{}}
	if len(result.Handlers) == 0 {
		seen := make(map[string]bool)
		for _, state := range detail.Order.State {
			selected := make(map[int64]bool)
			for _, id := range state.Processor {
				selected[id] = true
			}
			for _, node := range detail.Nodes {
				if node.ID != state.ID {
					continue
				}
				for _, person := range node.Processors {
					if state.ProcessMethod == "person" && !selected[person.UserID] {
						continue
					}
					if person.RealName != "" && !seen[person.RealName] {
						result.Handlers = append(result.Handlers, person.RealName)
						seen[person.RealName] = true
					}
				}
			}
		}
	}
	for _, form := range detail.Forms {
		if form.Data.Test.Meego != "" {
			result.Meego = form.Data.Test.Meego
		}
		if form.Data.Test.Remark != "" {
			result.Remark = form.Data.Test.Remark
		}
		for _, app := range form.Data.Test.Apps {
			result.Apps = append(result.Apps, appAlias(app.ID, app.Name))
		}
	}
	for _, h := range detail.History {
		result.History = append(result.History, History{h.State, h.Action, h.Processor, h.Remark, h.CreatedAt})
	}
	return result, nil
}
