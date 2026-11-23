// Package dragon adapts the embedded Dragon client for test approvals.
package dragon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/snowball/dragonapi"
)

const queryPageSize = 100

type Native struct {
	Client   func(context.Context, domain.AppConfig) (*dragonapi.Client, error)
	Identity func(context.Context, domain.AppConfig) (string, error)
}

type Summary struct {
	ID        string   `json:"approval_id"`
	No        string   `json:"no"`
	Title     string   `json:"title"`
	Status    string   `json:"status"`
	Handlers  []string `json:"handlers"`
	Creator   string   `json:"creator"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

type Approval struct {
	Summary
	URL    string   `json:"url"`
	Meego  string   `json:"meego"`
	Remark string   `json:"remark"`
	Apps   []string `json:"apps"`
}

// CreateError distinguishes a definite failure from an approval needing reconciliation.
type CreateError struct {
	PossiblyCreated bool
	ApprovalID      string
	Err             error
}

func (e *CreateError) Error() string { return e.Err.Error() }
func (e *CreateError) Unwrap() error { return e.Err }

func (c Native) AccountIdentity(ctx context.Context, cfg domain.AppConfig) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if c.Identity == nil {
		return "", errors.New("无法读取 Genbu / Dragon 共享账号")
	}
	username, err := c.Identity(ctx, cfg)
	if err != nil {
		return "", err
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return "", errors.New("请先配置 Genbu / Dragon 共享账号")
	}
	// Preserve the digest used by pending recovery records from past versions.
	hash := sha256.Sum256([]byte("dragon-beast-account\x00" + username))
	return hex.EncodeToString(hash[:]), nil
}

func (c Native) Create(ctx context.Context, cfg domain.AppConfig, requirementURL, remark string, services []string) (Approval, error) {
	client, err := c.acquire(ctx, cfg)
	if err != nil {
		return Approval{}, &CreateError{Err: err}
	}
	result, err := client.Create(ctx, dragonapi.CreateInput{Meego: requirementURL, Remark: remark, Apps: services})
	approval := Approval{URL: result.URL, Meego: result.Meego, Remark: result.Remark, Apps: result.Apps}
	if result.ApprovalID > 0 {
		approval.ID = strconv.FormatInt(result.ApprovalID, 10)
	}
	if err != nil {
		var failure *dragonapi.CreateError
		possibility := !errors.As(err, &failure) || failure.PossiblyCreated || approval.ID != ""
		return approval, &CreateError{PossiblyCreated: possibility, ApprovalID: approval.ID, Err: err}
	}
	return approval, nil
}

// Query walks every page submitted by the bound shared account. Callers must
// verify details because keyword search may also match unrelated fields.
func (c Native) Query(ctx context.Context, cfg domain.AppConfig, requirementID string) ([]Summary, error) {
	if strings.TrimSpace(requirementID) == "" {
		return nil, errors.New("查询青龙提测审批需要 Meego 需求编号")
	}
	client, err := c.acquire(ctx, cfg)
	if err != nil {
		return nil, err
	}
	items := []Summary{}
	seen := map[int64]bool{}
	var total, size int
	for page := 1; ; page++ {
		response, err := client.Query(ctx, dragonapi.QueryInput{Scope: "mine", Keyword: requirementID, Page: page, Size: queryPageSize})
		if err != nil {
			return nil, err
		}
		if response.Page != page || response.Size > queryPageSize {
			return nil, errors.New("无法识别青龙提测审批列表")
		}
		if page == 1 {
			total, size = response.Total, response.Size
		}
		if response.Total != total || response.Size != size || len(response.Items) != min(size, total-len(items)) {
			return nil, errors.New("青龙提测审批列表分页结果不完整，请重新查询")
		}
		for _, row := range response.Items {
			if seen[row.ApprovalID] {
				return nil, errors.New("青龙提测审批列表包含重复的审批 ID，请重新查询")
			}
			seen[row.ApprovalID] = true
			items = append(items, summary(row))
		}
		if len(items) == total {
			return items, nil
		}
	}
}

func (c Native) Detail(ctx context.Context, cfg domain.AppConfig, id string) (Approval, error) {
	approvalID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || approvalID <= 0 {
		return Approval{}, errors.New("青龙审批 ID 必须是正整数")
	}
	client, err := c.acquire(ctx, cfg)
	if err != nil {
		return Approval{}, err
	}
	result, err := client.Detail(ctx, approvalID)
	if err != nil {
		return Approval{}, err
	}
	return Approval{Summary: summary(result.Approval), URL: result.URL, Meego: result.Meego, Remark: result.Remark, Apps: result.Apps}, nil
}

func (c Native) acquire(ctx context.Context, cfg domain.AppConfig) (*dragonapi.Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.Client == nil {
		return nil, errors.New("青龙连接不可用")
	}
	return c.Client(ctx, cfg)
}

func summary(row dragonapi.Approval) Summary {
	return Summary{ID: strconv.FormatInt(row.ApprovalID, 10), No: row.No, Title: row.Title, Status: row.Status,
		Handlers: row.Handlers, Creator: row.Creator, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}
