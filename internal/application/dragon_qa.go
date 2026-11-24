package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/dragon"
	"silverwing-go/internal/storage"
)

const (
	DragonQASubmitting = "submitting"
	DragonQAUnknown    = "unknown"
	DragonQAApproved   = "approved"
	DragonQAFailed     = "failed"

	DragonQACommentPending = "pending"
	DragonQACommentSynced  = "synced"
	DragonQACommentFailed  = "failed"
	DragonQACommentUnknown = "unknown"
)

type DragonQATask struct {
	TaskID, TaskPath, TaskCreatedAt string
}

type DragonQAService struct {
	ID, Name string
}

type DragonQASubmitRequest struct {
	DragonQATask
	RequirementURL string
	Services       []DragonQAService
	Remark         string
	SyncComment    bool
}

type DragonQACandidate struct {
	ApprovalID, ApprovalURL, CreatedAt string
	Apps                               []string
}

type DragonQAState struct {
	TaskID, RequirementURL, Phase, ApprovalID, ApprovalURL string
	CommentStatus, Message, CommentError                   string
	Services                                               []DragonQAService
	Candidates                                             []DragonQACandidate `json:"-"`
}

type DragonQABackend interface {
	DragonQAStatus(context.Context, DragonQATask) (DragonQAState, error)
	SubmitDragonQA(context.Context, DragonQASubmitRequest) (DragonQAState, error)
	RecheckDragonQA(context.Context, DragonQATask, string) (DragonQAState, error)
	RetryDragonQAComment(context.Context, DragonQATask) (DragonQAState, error)
}

type dragonQAClient interface {
	AccountIdentity(context.Context, domain.AppConfig) (string, error)
	Create(context.Context, domain.AppConfig, string, string, []string) (dragon.Approval, error)
	Query(context.Context, domain.AppConfig, string) ([]dragon.Summary, error)
	Detail(context.Context, domain.AppConfig, string) (dragon.Approval, error)
}

type dragonQACommentClient interface {
	ResolveDragonRequirementCommentScope(context.Context, string) (string, error)
	SyncDragonRequirementComment(context.Context, string, string, string, string) (string, error)
	CheckDragonRequirementComment(context.Context, string, string, string, string) (string, error)
}

func (a *App) DragonQAStatus(ctx context.Context, task DragonQATask) (DragonQAState, error) {
	_, form, err := a.dragonQATaskForm(ctx, task)
	if err != nil {
		return DragonQAState{}, err
	}
	store := a.dragonQAStore(task)
	record, err := store.load()
	if err != nil {
		return DragonQAState{}, err
	}
	if record.Phase != DragonQASubmitting {
		return record.view(form), nil
	}
	unlock, err := store.lock()
	if errors.Is(err, storage.ErrLockBusy) {
		return record.view(form), nil
	}
	if err != nil {
		return DragonQAState{}, err
	}
	defer unlock()
	// If no process owns the lock, a saved submission may have been interrupted.
	record, err = store.load()
	if record.Phase == DragonQASubmitting {
		record.Phase = DragonQAUnknown
		record.Message = "提测提交中断，请先核对审批结果"
	}
	return record.view(form), err
}

func (a *App) SubmitDragonQA(ctx context.Context, request DragonQASubmitRequest) (DragonQAState, error) {
	return a.withDragonQA(ctx, request.DragonQATask, func(cfg domain.AppConfig, form DragonQAState, store dragonQAStore, record dragonQARecord) (DragonQAState, error) {
		return a.submitDragonQA(ctx, request, cfg, form, store, record, "")
	})
}

func (a *App) submitDragonQA(ctx context.Context, request DragonQASubmitRequest, cfg domain.AppConfig, form DragonQAState, store dragonQAStore, record dragonQARecord, expectedAccount string) (DragonQAState, error) {
	if record.Phase != "" && record.Phase != DragonQAFailed {
		return record.view(form), nil
	}
	link := domain.ParseFeishuWorkItemLink(form.RequirementURL)
	if link == nil {
		return form, errors.New("请先为任务关联有效的飞书项目需求")
	}
	if form.RequirementURL != request.RequirementURL {
		return form, errors.New("任务关联需求已变化，请重新打开提测弹框")
	}
	services, err := dragonQASubmissionServices(form.Services, request.Services)
	if err != nil {
		return form, err
	}
	ctx, err = a.platformIntegrationContext(ctx, cfg, "dragon")
	if err != nil {
		return record.view(form), err
	}
	account, err := a.dragonQAClient.AccountIdentity(ctx, cfg)
	if err != nil {
		return form, err
	}
	if expectedAccount != "" {
		current, err := loadPlatformAccount()
		if err != nil || current.Fingerprint() != expectedAccount {
			return form, errCLIDragonPreflightStale
		}
	}
	if err := commandruntime.CheckAdmission(ctx); err != nil {
		return record.view(form), err
	}
	record = dragonQARecord{
		DragonQAState: DragonQAState{TaskID: form.TaskID, RequirementURL: form.RequirementURL, Services: request.Services,
			Phase: DragonQASubmitting, Message: "正在提交提测审批…"},
		Account: account, RequirementID: link.WorkItemID, Remark: request.Remark,
		AttemptedAt: time.Now().UTC(), SyncComment: request.SyncComment,
	}
	if err := store.save(record); err != nil {
		return form, err
	}
	var approval dragon.Approval
	if err = commandruntime.CheckAdmission(ctx); err != nil {
		err = &dragon.CreateError{Err: err}
	} else {
		approval, err = a.dragonQAClient.Create(ctx, cfg, record.RequirementURL, record.Remark, services)
	}
	if err != nil {
		var createErr *dragon.CreateError
		record.Phase = DragonQAUnknown
		record.ApprovalID = approval.ID
		if errors.As(err, &createErr) {
			if createErr.ApprovalID != "" {
				record.ApprovalID = createErr.ApprovalID
			}
			if !createErr.PossiblyCreated && record.ApprovalID == "" {
				record.Phase = DragonQAFailed
			}
		}
		record.Message = err.Error()
		return record.view(form), store.save(record)
	}
	return a.finishDragonQA(ctx, store, record, approval)
}

func (a *App) withDragonQA(ctx context.Context, task DragonQATask, run func(domain.AppConfig, DragonQAState, dragonQAStore, dragonQARecord) (DragonQAState, error)) (DragonQAState, error) {
	store := a.dragonQAStore(task)
	unlock, err := store.lock()
	if errors.Is(err, storage.ErrLockBusy) {
		return a.DragonQAStatus(ctx, task)
	}
	if err != nil {
		return DragonQAState{}, err
	}
	defer unlock()
	cfg, form, err := a.dragonQATaskForm(ctx, task)
	if err != nil {
		return DragonQAState{}, err
	}
	record, err := store.load()
	if err != nil {
		return form, err
	}
	if record.Phase == DragonQASubmitting {
		record.Phase = DragonQAUnknown
		record.Message = "提测提交中断，请先核对审批结果"
	}
	return run(cfg, form, store, record)
}

func (a *App) dragonQATaskForm(ctx context.Context, task DragonQATask) (domain.AppConfig, DragonQAState, error) {
	if err := ctx.Err(); err != nil {
		return domain.AppConfig{}, DragonQAState{}, err
	}
	cfg, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return cfg, DragonQAState{}, err
	}
	manifest, path, err := a.taskInConfig(Action{TaskID: task.TaskID}, cfg)
	if err != nil {
		return cfg, DragonQAState{}, err
	}
	if task.TaskCreatedAt == "" || task.TaskCreatedAt != manifest.CreatedAt || task.TaskPath == "" ||
		!sameTaskContextPath(task.TaskPath, path) || manifest.LifecycleStatus == domain.TaskArchived {
		return cfg, DragonQAState{}, errors.New("提测所属任务已变化，请重新打开")
	}
	form := DragonQAState{TaskID: task.TaskID, RequirementURL: manifest.RequirementLink}
	seen := make(map[string]bool)
	for _, workspace := range manifest.Services {
		id := workspace.GroupServiceID
		if id == "" {
			id = workspace.RepositoryID
		}
		if id != "" && !seen[id] && strings.TrimSpace(workspace.ServiceName) != "" {
			form.Services = append(form.Services, DragonQAService{ID: id, Name: workspace.ServiceName})
			seen[id] = true
		}
	}
	return cfg, form, nil
}

func dragonQASubmissionServices(available, selected []DragonQAService) ([]string, error) {
	if len(selected) == 0 {
		return nil, errors.New("请至少选择一个提测服务")
	}
	current := make(map[string]string, len(available))
	for _, service := range available {
		current[service.ID] = service.Name
	}
	names := make([]string, 0, len(selected))
	seen := make(map[string]bool)
	for _, service := range selected {
		if name, ok := current[service.ID]; !ok || name != service.Name {
			return nil, errors.New("提测服务已变化，请重新打开提测弹框")
		}
		if !seen[service.Name] {
			names = append(names, service.Name)
			seen[service.Name] = true
		}
	}
	return names, nil
}
