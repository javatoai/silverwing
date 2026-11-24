package application

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/snowball/dragonapi"
)

var errCLIDragonPreflightStale = errors.New("预检编号无效，或任务、服务、账号及提测参数已变化，请重新预检")

type CLIDragonSubmitRequest struct {
	Task, Remark, Preflight string
	SyncComment             bool
}

type CLIDragonQAResult struct {
	TaskID         string                `json:"taskId"`
	RequirementURL string                `json:"requirementUrl,omitempty"`
	Phase          string                `json:"phase"`
	Services       []CLIDragonQAService  `json:"services,omitempty"`
	ApprovalID     string                `json:"approvalId,omitempty"`
	ApprovalURL    string                `json:"approvalUrl,omitempty"`
	Message        string                `json:"message,omitempty"`
	CommentStatus  string                `json:"commentStatus,omitempty"`
	CommentError   string                `json:"commentError,omitempty"`
	Candidates     []CLIDragonQAApproval `json:"candidates,omitempty"`
	Preflight      string                `json:"preflight,omitempty"`
	Remark         string                `json:"remark,omitempty"`
	SyncComment    bool                  `json:"syncComment,omitempty"`
}

type CLIDragonQAService struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type CLIDragonQAApproval struct {
	ID        string   `json:"id"`
	URL       string   `json:"url"`
	CreatedAt string   `json:"createdAt"`
	Apps      []string `json:"apps"`
}

func (a *App) CLIDragonSubmit(ctx context.Context, request CLIDragonSubmitRequest) (CLIDragonQAResult, error) {
	cfg, task, manifest, err := a.cliDragonTask(ctx, request.Task)
	if err != nil {
		return CLIDragonQAResult{}, err
	}
	if request.Preflight == "" {
		status, err := a.DragonQAStatus(ctx, task)
		if err != nil || (status.Phase != "" && status.Phase != DragonQAFailed) {
			return cliDragonQAResult(status), err
		}
		check, err := a.platformAdmission(ctx, "dragon")
		if err != nil {
			return CLIDragonQAResult{}, err
		}
		submission, aliases, err := cliDragonSubmission(cfg, task, manifest, request)
		if err != nil {
			return CLIDragonQAResult{}, err
		}
		account, err := loadPlatformAccount()
		if err != nil {
			return CLIDragonQAResult{}, err
		}
		if err := check(ctx); err != nil {
			return CLIDragonQAResult{}, err
		}
		result := cliDragonQAResult(DragonQAState{
			TaskID: task.TaskID, RequirementURL: submission.RequirementURL, Phase: "preview", Services: submission.Services,
		})
		result.Remark, result.SyncComment = request.Remark, request.SyncComment
		result.Preflight = a.cliDragonPreflight(submission, aliases, account.Fingerprint())
		return result, nil
	}
	status, err := a.withDragonQA(ctx, task, func(cfg domain.AppConfig, form DragonQAState, store dragonQAStore, record dragonQARecord) (DragonQAState, error) {
		if record.Phase != "" && record.Phase != DragonQAFailed {
			return record.view(form), nil
		}
		manifest, taskPath, err := a.taskInConfig(Action{TaskID: task.TaskID}, cfg)
		if err != nil {
			return form, err
		}
		if manifest.CreatedAt != task.TaskCreatedAt || !sameTaskContextPath(taskPath, task.TaskPath) || manifest.LifecycleStatus == domain.TaskArchived {
			return form, errCLIDragonPreflightStale
		}
		submission, aliases, err := cliDragonSubmission(cfg, task, manifest, request)
		if err != nil {
			return form, err
		}
		account, err := loadPlatformAccount()
		if err != nil {
			return form, err
		}
		fingerprint := account.Fingerprint()
		if expected := a.cliDragonPreflight(submission, aliases, fingerprint); !hmac.Equal([]byte(request.Preflight), []byte(expected)) {
			return form, errCLIDragonPreflightStale
		}
		return a.submitDragonQA(ctx, submission, cfg, form, store, record, fingerprint)
	})
	return cliDragonQAResult(status), err
}

func (a *App) cliDragonTask(ctx context.Context, taskID string) (domain.AppConfig, DragonQATask, domain.TaskManifest, error) {
	cfg, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return cfg, DragonQATask{}, domain.TaskManifest{}, err
	}
	taskID = strings.TrimSpace(taskID)
	manifest, taskPath, err := a.taskInConfig(Action{TaskID: taskID}, cfg)
	if err != nil {
		return cfg, DragonQATask{}, manifest, err
	}
	return cfg, DragonQATask{TaskID: taskID, TaskPath: taskPath, TaskCreatedAt: manifest.CreatedAt}, manifest, nil
}

func cliDragonSubmission(cfg domain.AppConfig, task DragonQATask, manifest domain.TaskManifest, request CLIDragonSubmitRequest) (DragonQASubmitRequest, []string, error) {
	submission := DragonQASubmitRequest{DragonQATask: task, RequirementURL: manifest.RequirementLink, Remark: request.Remark, SyncComment: request.SyncComment}
	if domain.ParseFeishuWorkItemLink(manifest.RequirementLink) == nil {
		return submission, nil, errors.New("请先为任务关联有效的飞书项目需求")
	}
	seenServices, seenApps := map[string]bool{}, map[string]bool{}
	var aliases []string
	for _, workspace := range manifest.Services {
		if cliDragonExcludeService(cfg, workspace) {
			continue
		}
		id := workspace.GroupServiceID
		if id == "" {
			id = workspace.RepositoryID
		}
		if id == "" || strings.TrimSpace(workspace.ServiceName) == "" {
			return submission, nil, errors.New("任务服务缺少标识或名称，请先修正任务服务")
		}
		if seenServices[id] {
			continue
		}
		seenServices[id] = true
		apps, err := dragonapi.ResolveApplications([]string{workspace.ServiceName})
		if err != nil {
			return submission, nil, err
		}
		if seenApps[apps[0].AppID] {
			continue
		}
		seenApps[apps[0].AppID] = true
		submission.Services = append(submission.Services, DragonQAService{ID: id, Name: workspace.ServiceName})
		aliases = append(aliases, apps[0].Alias)
	}
	if len(submission.Services) == 0 {
		return submission, nil, errors.New("任务没有可提测的服务")
	}
	return submission, aliases, nil
}

func cliDragonExcludeService(cfg domain.AppConfig, workspace domain.ServiceWorkspace) bool {
	repo, _ := configuredRepository(cfg, workspace.RepositoryID)
	for _, name := range []string{workspace.GroupServiceID, workspace.RepositoryID, workspace.ServiceName, repo.Name} {
		if strings.EqualFold(strings.TrimSpace(name), "feign-client") {
			return true
		}
	}
	for _, origin := range []*string{workspace.OriginURL, repo.OriginURL} {
		if origin != nil {
			repoName := path.Base(strings.ReplaceAll(strings.TrimSpace(*origin), "\\", "/"))
			if strings.TrimSuffix(strings.ToLower(repoName), ".git") == "feign-client" {
				return true
			}
		}
	}
	return false
}

func (a *App) cliDragonPreflight(submission DragonQASubmitRequest, aliases []string, fingerprint string) string {
	// The confirmation works across invocations and does not need a preview file.
	payload, _ := json.Marshal(struct {
		Home, ProfileID string
		Submission      DragonQASubmitRequest
		Aliases         []string
	}{a.Paths.Home, a.Paths.ProfileID, submission, aliases})
	signature := hmac.New(sha256.New, []byte(fingerprint))
	signature.Write([]byte("silverwing-dragon-preflight-v1\x00"))
	signature.Write(payload)
	return hex.EncodeToString(signature.Sum(nil))
}

func cliDragonQAResult(state DragonQAState) CLIDragonQAResult {
	out := CLIDragonQAResult{
		TaskID: state.TaskID, RequirementURL: state.RequirementURL, Phase: state.Phase, ApprovalID: state.ApprovalID, ApprovalURL: state.ApprovalURL,
		Message: state.Message, CommentStatus: state.CommentStatus, CommentError: state.CommentError,
	}
	for _, service := range state.Services {
		out.Services = append(out.Services, CLIDragonQAService{service.ID, service.Name})
	}
	for _, candidate := range state.Candidates {
		out.Candidates = append(out.Candidates, CLIDragonQAApproval{candidate.ApprovalID, candidate.ApprovalURL, candidate.CreatedAt, candidate.Apps})
	}
	return out
}

func (a *App) CLIDragonStatus(ctx context.Context, taskID string) (CLIDragonQAResult, error) {
	_, task, _, err := a.cliDragonTask(ctx, taskID)
	if err != nil {
		return CLIDragonQAResult{}, err
	}
	state, err := a.DragonQAStatus(ctx, task)
	return cliDragonQAResult(state), err
}

func (a *App) CLIDragonRecheck(ctx context.Context, taskID, approvalID string) (CLIDragonQAResult, error) {
	_, task, _, err := a.cliDragonTask(ctx, taskID)
	if err != nil {
		return CLIDragonQAResult{}, err
	}
	state, err := a.RecheckDragonQA(ctx, task, approvalID)
	return cliDragonQAResult(state), err
}

func (a *App) CLIDragonRetryComment(ctx context.Context, taskID string) (CLIDragonQAResult, error) {
	_, task, _, err := a.cliDragonTask(ctx, taskID)
	if err != nil {
		return CLIDragonQAResult{}, err
	}
	state, err := a.RetryDragonQAComment(ctx, task)
	return cliDragonQAResult(state), err
}

func (a *App) CLIDragonApprovals(ctx context.Context, input dragonapi.QueryInput) (dragonapi.QueryResult, error) {
	cfg, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return dragonapi.QueryResult{}, err
	}
	client, err := a.embeddedDragon(ctx, cfg)
	if err != nil {
		return dragonapi.QueryResult{}, err
	}
	return client.Query(ctx, input)
}

func (a *App) CLIDragonDetail(ctx context.Context, id int64) (dragonapi.DetailResult, error) {
	cfg, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return dragonapi.DetailResult{}, err
	}
	client, err := a.embeddedDragon(ctx, cfg)
	if err != nil {
		return dragonapi.DetailResult{}, err
	}
	result, err := client.Detail(ctx, id)
	if err == nil && result.ApprovalID != id {
		return dragonapi.DetailResult{}, errors.New("青龙返回的审批 ID 与请求不一致")
	}
	return result, err
}
