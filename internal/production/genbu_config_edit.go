package production

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/snowball/genbuapi"
)

var (
	ErrGenbuConfigPreviewStale  = errors.New("配置或编辑内容已变化，请重新预览")
	ErrGenbuConfigTarget        = errors.New("无法唯一定位目标命名空间、ConfigMap 或配置文件，请检查后重新预览")
	ErrGenbuConfigSubmitUnknown = errors.New("配置审批提交结果不明，请查询对应环境和服务的 ConfigMap 修改审批记录")
)

type GenbuPropertyEdit struct {
	Operation string
	Key       string
	Value     string
}

type GenbuConfigEditRequest struct {
	Environment string
	Service     string
	File        string
	Edits       []GenbuPropertyEdit
	// Non-nil supplies the complete UTF-8 body, including an empty body.
	Replacement []byte
	// StdinReplacement binds a complete body to the inline editor snapshot.
	StdinReplacement bool
}

type GenbuConfigChange struct {
	Operation string `json:"operation"`
	Key       string `json:"key"`
}

type GenbuConfigEditResult struct {
	Service     string              `json:"service"`
	Environment string              `json:"environment"`
	Namespace   string              `json:"namespace"`
	Name        string              `json:"name"`
	File        string              `json:"file"`
	UpdateTime  string              `json:"update_time"`
	SnapshotID  string              `json:"snapshot_id,omitempty"`
	Status      string              `json:"status"`
	PreviewID   string              `json:"preview_id,omitempty"`
	ChangeID    string              `json:"-"`
	Changes     []GenbuConfigChange `json:"changes,omitempty"`
	BeforeBytes *int                `json:"before_bytes,omitempty"`
	AfterBytes  *int                `json:"after_bytes,omitempty"`
	ApprovalID  int64               `json:"approve_id,omitempty"`
}

func (g NativeGenbu) PreviewConfigEdit(ctx context.Context, cfg domain.AppConfig, request GenbuConfigEditRequest) (GenbuConfigEditResult, error) {
	return g.editConfig(ctx, cfg, request, "")
}

func (g NativeGenbu) SubmitConfigEdit(ctx context.Context, cfg domain.AppConfig, request GenbuConfigEditRequest, expect string) (GenbuConfigEditResult, error) {
	if !validGenbuSHA256(expect) {
		return GenbuConfigEditResult{}, ErrGenbuConfigPreviewStale
	}
	return g.editConfig(ctx, cfg, request, expect)
}

func (g NativeGenbu) editConfig(ctx context.Context, cfg domain.AppConfig, request GenbuConfigEditRequest, expect string) (GenbuConfigEditResult, error) {
	env, err := serviceEnvironment(request.Environment)
	if err != nil {
		return GenbuConfigEditResult{}, err
	}
	service := strings.TrimSpace(request.Service)
	if !genbuName(service) || !validGenbuConfigIdentifier(request.File) {
		return GenbuConfigEditResult{}, errors.New("请选择有效的 Genbu 服务和目标文件名")
	}
	if (request.Replacement == nil) == (len(request.Edits) == 0) {
		return GenbuConfigEditResult{}, errors.New("请选择按键编辑或完整文件替换")
	}
	if request.StdinReplacement && request.Replacement == nil {
		return GenbuConfigEditResult{}, errors.New("内联配置编辑需要提供完整文件正文")
	}
	wire := genbuapi.ConfigEditRequest{Environment: env, Service: service, File: request.File}
	if request.Replacement != nil {
		if request.StdinReplacement && len(request.Replacement) > GenbuConfigMaxBytes {
			return GenbuConfigEditResult{}, errors.New("替换文件超过 4 MiB，无法内联编辑")
		}
		if !utf8.Valid(request.Replacement) {
			return GenbuConfigEditResult{}, errors.New("替换文件必须是有效的 UTF-8 文本")
		}
		body := string(request.Replacement)
		wire.Replacement = &body
	} else {
		if !strings.EqualFold(filepath.Ext(request.File), ".properties") {
			return GenbuConfigEditResult{}, errors.New("按键编辑仅支持 .properties 文件")
		}
		wire.Edits = make([]genbuapi.PropertyEdit, 0, len(request.Edits))
		seen := make(map[string]bool, len(request.Edits))
		for _, edit := range request.Edits {
			if strings.TrimSpace(edit.Key) == "" || !utf8.ValidString(edit.Key) || !utf8.ValidString(edit.Value) {
				return GenbuConfigEditResult{}, errors.New("配置键或值不是有效的 UTF-8 文本")
			}
			if seen[edit.Key] {
				return GenbuConfigEditResult{}, errors.New("同一个配置键只能指定一次操作")
			}
			seen[edit.Key] = true
			switch edit.Operation {
			case "set", "add":
			case "unset":
				if edit.Value != "" {
					return GenbuConfigEditResult{}, errors.New("删除配置键时无需填写值")
				}
			default:
				return GenbuConfigEditResult{}, errors.New("请选择更新、新增或删除配置键")
			}
			wire.Edits = append(wire.Edits, genbuapi.PropertyEdit(edit))
		}
	}
	ctx, client, err := g.client(ctx, cfg)
	if err != nil {
		return GenbuConfigEditResult{}, err
	}
	var response genbuapi.ConfigEditResult
	if expect == "" {
		response, err = client.PreviewConfigEdit(ctx, wire)
		if scopeErr := checkGenbuReadScope(ctx); scopeErr != nil {
			return GenbuConfigEditResult{}, scopeErr
		}
	} else {
		response, err = client.SubmitConfigEdit(ctx, wire, expect)
	}
	if err != nil {
		if expect != "" {
			return GenbuConfigEditResult{}, genbuConfigError(ctx, err, "Genbu 配置审批提交失败")
		}
		return GenbuConfigEditResult{}, genbuConfigError(ctx, err, "Genbu 配置预览失败")
	}
	result := GenbuConfigEditResult{
		Service: response.Service, Environment: genbuResponseEnvironment(response.Environment), Namespace: response.Namespace,
		Name: response.Name, File: response.File, UpdateTime: response.UpdateTime, SnapshotID: response.SnapshotID,
		Status: response.Status, PreviewID: response.PreviewID, ChangeID: response.ChangeID, ApprovalID: response.ApprovalID,
		BeforeBytes: response.BeforeBytes, AfterBytes: response.AfterBytes,
	}
	for _, change := range response.Changes {
		result.Changes = append(result.Changes, GenbuConfigChange(change))
	}
	if result.Service != genbuapi.ResolveService(service).Name || !validGenbuEditResult(result, request, env, expect) {
		if expect != "" {
			return GenbuConfigEditResult{}, ErrGenbuConfigSubmitUnknown
		}
		return GenbuConfigEditResult{}, errors.New("无法识别 Genbu 配置预览结果")
	}
	return result, nil
}

func genbuConfigError(ctx context.Context, err error, fallback string) error {
	var ambiguity *genbuapi.NamespaceAmbiguityError
	var rejected genbuapi.RequestNotSentError
	switch {
	case errors.As(err, &rejected):
		return rejected
	case errors.Is(err, genbuapi.ErrConfigSubmitUnknown):
		return ErrGenbuConfigSubmitUnknown
	case errors.Is(err, genbuapi.ErrConfigSubmitRejected):
		return errors.New("Genbu 拒绝提交配置审批")
	case errors.Is(err, genbuapi.ErrConfigPreviewStale):
		return ErrGenbuConfigPreviewStale
	case errors.Is(err, genbuapi.ErrConfigTarget), errors.As(err, &ambiguity):
		return ErrGenbuConfigTarget
	case errors.Is(err, genbuapi.ErrConfigPropertyEdit):
		return errors.New("Genbu 无法应用所选配置键，检查键是否存在、重复或格式错误后重新预览")
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, context.DeadlineExceeded):
		return errors.New("Genbu 请求超时")
	case genbuapi.IsAuthenticationError(err):
		return errors.New("Genbu 账号认证失败，请检查共享账号配置")
	default:
		return errors.New(fallback)
	}
}

func validGenbuSHA256(id string) bool {
	if len(id) != hex.EncodedLen(sha256.Size) {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func validGenbuEditResult(result GenbuConfigEditResult, request GenbuConfigEditRequest, env, expect string) bool {
	if result.Environment != env || result.File != request.File ||
		!genbuIdentifier(result.Service) || !genbuIdentifier(result.Namespace) ||
		!genbuIdentifier(result.Name) || !genbuIdentifier(result.UpdateTime) {
		return false
	}
	if request.StdinReplacement && (!validGenbuSHA256(result.SnapshotID) || !genbuName(result.Service) ||
		!validGenbuConfigIdentifier(result.Namespace) || !validGenbuConfigIdentifier(result.Name) || !validGenbuConfigIdentifier(result.UpdateTime)) {
		return false
	}
	if expect != "" {
		if result.Status != "submitted" || result.PreviewID != expect || result.ApprovalID < 0 {
			return false
		}
	} else {
		if result.ApprovalID != 0 {
			return false
		}
		switch result.Status {
		case "no-change":
			if result.PreviewID != "" || len(result.Changes) != 0 {
				return false
			}
		case "preview":
			if !validGenbuSHA256(result.PreviewID) {
				return false
			}
		default:
			return false
		}
	}
	if request.Replacement != nil {
		return len(result.Changes) == 0 && result.BeforeBytes != nil && *result.BeforeBytes >= 0 &&
			result.AfterBytes != nil && *result.AfterBytes == len(request.Replacement) &&
			(result.Status != "no-change" || *result.BeforeBytes == *result.AfterBytes)
	}
	if result.BeforeBytes != nil || result.AfterBytes != nil || result.Status != "no-change" && len(result.Changes) == 0 {
		return false
	}
	edits := make(map[string]string, len(request.Edits))
	for _, edit := range request.Edits {
		edits[edit.Key] = edit.Operation
	}
	for _, change := range result.Changes {
		if operation, exists := edits[change.Key]; !exists || operation != change.Operation {
			return false
		}
		delete(edits, change.Key)
	}
	return true
}
