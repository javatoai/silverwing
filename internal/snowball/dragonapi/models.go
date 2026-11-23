package dragonapi

import (
	"encoding/json"
	"strings"
)

const (
	TemplateID         = 24
	testType           = "Sprint 提测"
	testEnvironment    = "UAT"
	businessLine       = "OBT"
	applicationVersion = "最新"
	onlineSteps        = "Step 1:修改配置\n\nStep 2: 执行 SQL\n\nStep 3: 应用更新\n"
	rollbackSteps      = "Step 1: 配置回滚\n\nStep 2: 应用回滚\n\nStep 3: SQL 回滚\n"
)

// CreateInput contains the three business inputs for a test approval.
type CreateInput struct {
	Meego  string   `json:"meego"`
	Remark string   `json:"remark"`
	Apps   []string `json:"apps"`
}

// Application is an embedded Dragon application snapshot, never fetched at runtime.
type Application struct {
	Alias     string   `json:"alias"`
	Aliases   []string `json:"aliases"`
	AppID     string   `json:"appId"`
	AppName   string   `json:"appName"`
	AppCNName string   `json:"appCnName"`
	Version   string   `json:"version"`
}

type CreateResult struct {
	ApprovalID int64  `json:"approval_id,omitempty"`
	URL        string `json:"url"`
	CreateInput
}

// Approval deliberately omits fixed form fields and nested account information.
type Approval struct {
	ApprovalID int64    `json:"approval_id"`
	No         string   `json:"no"`
	Title      string   `json:"title"`
	Status     string   `json:"status"`
	Handlers   []string `json:"handlers"`
	Creator    string   `json:"creator"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
}

type QueryInput struct {
	Scope   string
	Page    int
	Size    int
	No      string
	Keyword string
	IsEnd   *bool
}

type QueryResult struct {
	Total int        `json:"total"`
	Page  int        `json:"page"`
	Size  int        `json:"size"`
	Items []Approval `json:"items"`
}

type History struct {
	State     string `json:"state"`
	Action    string `json:"action"`
	Processor string `json:"processor"`
	Remark    string `json:"remark"`
	CreatedAt string `json:"created_at"`
}

type DetailResult struct {
	Approval
	URL     string    `json:"url"`
	Meego   string    `json:"meego"`
	Remark  string    `json:"remark"`
	Apps    []string  `json:"apps"`
	History []History `json:"history"`
}

type wireState struct {
	ID            string  `json:"id"`
	Label         string  `json:"label"`
	Processor     []int64 `json:"processor"`
	ProcessMethod string  `json:"processMethod"`
}

type wireApproval struct {
	State           []wireState `json:"state"`
	ApprovalID      int64       `json:"approvalId"`
	No              string      `json:"no"`
	Title           string      `json:"title"`
	StateLabel      string      `json:"stateLabel"`
	IsEnd           int         `json:"isEnd"`
	IsRevoked       int         `json:"isRevoked"`
	IsDenied        int         `json:"isDenied"`
	CurrentHandlers []struct {
		Name string `json:"Name"`
	} `json:"currentHandlers"`
	CreatedUser struct {
		RealName string `json:"realName"`
	} `json:"createdUser"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

func (w wireApproval) summary() Approval {
	status := w.StateLabel
	if status == "" {
		labels := []string{}
		seen := make(map[string]bool)
		for _, state := range w.State {
			if label := strings.TrimSpace(state.Label); label != "" && !seen[label] {
				labels = append(labels, label)
				seen[label] = true
			}
		}
		status = strings.Join(labels, ",")
	}
	switch {
	case w.IsRevoked == 1:
		status = "已撤销"
	case w.IsDenied == 1:
		status = "已拒绝"
	case status == "" && w.IsEnd == 1:
		status = "已结束"
	case status == "":
		status = "未知"
	}
	handlers := make([]string, 0, len(w.CurrentHandlers))
	for _, handler := range w.CurrentHandlers {
		if handler.Name != "" {
			handlers = append(handlers, handler.Name)
		}
	}
	return Approval{w.ApprovalID, w.No, w.Title, status, handlers, w.CreatedUser.RealName, w.CreatedAt, w.UpdatedAt}
}

type processTemplate struct {
	Process struct {
		ID         int64             `json:"processTemplateId"`
		Name       string            `json:"name"`
		ClassifyID int64             `json:"classifyId"`
		Tasks      []json.RawMessage `json:"task"`
	} `json:"process"`
	StructureID int64         `json:"processTemplateStructureId"`
	Nodes       []processNode `json:"nodes"`
	Edges       []processEdge `json:"edges"`
	Templates   []struct {
		ID      int64          `json:"formTemplateId"`
		Content map[string]any `json:"content"`
	} `json:"tpls"`
}

type processNode struct {
	Processors []struct {
		UserID   int64  `json:"userId"`
		RealName string `json:"realName"`
	} `json:"processor"`
	ID          string            `json:"id"`
	Class       string            `json:"clazz"`
	Label       string            `json:"label"`
	AssignType  string            `json:"assignType"`
	AssignValue []json.RawMessage `json:"assignValue"`
	CCApprover  bool              `json:"ccApprover"`
	Tasks       []json.RawMessage `json:"task"`
}

type processEdge struct {
	Source        string `json:"source"`
	Target        string `json:"target"`
	Label         string `json:"label"`
	IsExecuteTask bool   `json:"isExecuteTask"`
}
