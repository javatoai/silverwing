package application

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
	"silverwing-go/internal/snowball/auth"
	"silverwing-go/internal/snowball/genbuapi"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

type uatPodClient interface {
	CheckScope(context.Context) error
	PreviewUATPodOperation(context.Context, domain.AppConfig, genbuapi.UATPodOperationRequest) (genbuapi.UATPodOperationResult, error)
	SubmitUATPodOperation(context.Context, domain.AppConfig, genbuapi.UATPodOperationRequest, string, func(context.Context, genbuapi.UATPodOperationResult) error) (genbuapi.UATPodOperationResult, error)
}

type uatPodOperation struct {
	config  domain.AppConfig
	request genbuapi.UATPodOperationRequest
	binding uatPodBinding
	recheck func(context.Context) error
}

func (a *App) runUATPodOperation(ctx context.Context, request CLIUATOperationRequest, submit bool, resolve uatServiceResolver, client uatPodClient) (CLIUATOperationResult, error) {
	if submit && request.Preflight == "" {
		return CLIUATOperationResult{}, errors.New("请先预检并确认 UAT 操作")
	}
	release, err := acquireTaskServiceReadSlot(ctx)
	if err != nil {
		return CLIUATOperationResult{}, err
	}
	defer release()
	operation, err := a.prepareUATPodOperation(ctx, request, resolve, client)
	if err != nil {
		return CLIUATOperationResult{}, err
	}
	if !submit {
		wire, err := operation.preview(ctx, client)
		if err != nil {
			return CLIUATOperationResult{}, err
		}
		result := uatPodDisplayResult(wire, operation.binding)
		result.Preflight, err = operation.binding.newPreflight(wire.GroupID, wire.PreviewID)
		return result, err
	}
	preflight, err := operation.binding.checkPreflight(request.Preflight)
	if err != nil {
		return CLIUATOperationResult{}, err
	}
	unlock, err := acquireUATPodGroup(preflight.groupID)
	if err != nil {
		return CLIUATOperationResult{}, err
	}
	defer unlock()
	var result CLIUATOperationResult
	perform := func() error {
		result, err = a.submitUATPodOperation(ctx, operation, preflight, client, nil)
		return err
	}
	if taskPath := operation.binding.Target.TaskPath; taskPath != "" {
		err = workspace.NewTaskOperationLocks(a.Paths).WithTask(taskPath, perform)
	} else {
		err = perform()
	}
	return result, err
}

func (a *App) prepareUATPodOperation(ctx context.Context, request CLIUATOperationRequest, resolve uatServiceResolver, client uatPodClient) (uatPodOperation, error) {
	var wireRequest genbuapi.UATPodOperationRequest
	switch request.Action {
	case "release":
		if request.Tag == "" || strings.TrimSpace(request.Tag) != request.Tag {
			return uatPodOperation{}, errors.New("发布到 UAT 需要指定完整 Tag")
		}
		wireRequest.Action, wireRequest.Tag = "release", request.Tag
	case "restart":
		if request.Tag != "" {
			return uatPodOperation{}, errors.New("重启 UAT Pod 无需指定 Tag")
		}
		wireRequest.Action = "redeploy"
	default:
		return uatPodOperation{}, errors.New("请选择发布到 UAT 或重启 UAT Pod")
	}
	identify := func(ctx context.Context) (domain.AppConfig, uatPodBinding, error) {
		if err := client.CheckScope(ctx); err != nil {
			return domain.AppConfig{}, uatPodBinding{}, err
		}
		cfg, target, err := resolve(ctx)
		if err != nil {
			return cfg, uatPodBinding{}, err
		}
		binding, err := a.uatPodBinding(request, target)
		return cfg, binding, err
	}
	cfg, binding, err := identify(ctx)
	if err != nil {
		return uatPodOperation{}, err
	}
	wireRequest.Service = binding.Target.GenbuService
	recheck := func(ctx context.Context) error {
		_, current, err := identify(ctx)
		if err != nil {
			return err
		}
		if current != binding {
			return production.ErrUATPodPreflightStale
		}
		return nil
	}
	return uatPodOperation{config: cfg, binding: binding, request: wireRequest, recheck: recheck}, nil
}

func (operation uatPodOperation) preview(ctx context.Context, client uatPodClient) (genbuapi.UATPodOperationResult, error) {
	wire, err := client.PreviewUATPodOperation(ctx, operation.config, operation.request)
	if err != nil {
		return genbuapi.UATPodOperationResult{}, err
	}
	if err := operation.recheck(ctx); err != nil {
		return genbuapi.UATPodOperationResult{}, err
	}
	if wire.Status != "preview" || !validUATPodResult(wire, operation.request) {
		return genbuapi.UATPodOperationResult{}, errors.New("无法确认 Genbu 返回的 UAT 预检目标")
	}
	return wire, nil
}

func acquireUATPodGroup(groupID int) (func(), error) {
	shared, err := auth.UserStore()
	if err != nil {
		return nil, err
	}
	// Different data directories use the same platform account and UAT Groups.
	unlock, err := storage.AcquireLock(filepath.Join(shared.Directory, fmt.Sprintf("silverwing-uat-group-%d.lock", groupID)))
	if errors.Is(err, storage.ErrLockBusy) {
		return nil, fmt.Errorf("UAT Pod Group %d 正在处理另一项操作：%w", groupID, err)
	}
	return unlock, err
}

func (a *App) uatPodReceiptPath(preflight uatPodPreflight) string {
	return filepath.Join(a.Paths.Home, "genbu-uat-submissions", preflight.nonce+".json")
}

func (a *App) submitUATPodOperation(ctx context.Context, operation uatPodOperation, preflight uatPodPreflight, client uatPodClient, checkBatch func(context.Context) error) (CLIUATOperationResult, error) {
	request, binding, recheck := operation.request, operation.binding, operation.recheck
	if err := recheck(ctx); err != nil {
		return CLIUATOperationResult{}, err
	}
	receiptPath := a.uatPodReceiptPath(preflight)
	if result, err := readUATPodReceipt(receiptPath); err != nil {
		return result, err
	}
	// Apply the scope check again at transport admission. The adapter retains
	// the platform's own account-generation check and never retries a mutation.
	checked := commandruntime.WithAdmission(ctx, func(requestCtx context.Context) error {
		if err := commandruntime.CheckAdmission(ctx); err != nil {
			return err
		}
		return recheck(requestCtx)
	})
	var pending CLIUATOperationResult
	wire, err := client.SubmitUATPodOperation(checked, operation.config, request, preflight.nativeID, func(ctx context.Context, observed genbuapi.UATPodOperationResult) error {
		if observed.GroupID != preflight.groupID || observed.PreviewID != preflight.nativeID || !validUATPodResult(observed, request) {
			return production.ErrUATPodPreflightStale
		}
		if err := recheck(ctx); err != nil {
			return err
		}
		if checkBatch != nil {
			if err := checkBatch(ctx); err != nil {
				return err
			}
		}
		receipt := uatPodDisplayResult(observed, binding)
		receipt.Status = "unknown"
		if receipt.EnableStatus == "required" {
			// If the process exits mid-operation, neither mutation can be replayed.
			receipt.EnableStatus = "unknown"
		}
		if err := saveUATPodReceipt(receiptPath, receipt); err != nil {
			return err
		}
		pending = receipt
		return nil
	})
	if pending.Status == "" {
		if err == nil {
			err = errors.New("无法核对 Genbu UAT 操作的提交记录")
		}
		return CLIUATOperationResult{}, err
	}
	reserved := pending
	wireMatches := wire.PreviewID == preflight.nativeID && wire.GroupID == preflight.groupID && validUATPodResult(wire, request)
	if wireMatches {
		pending.EnableStatus = wire.EnableStatus
		if pending.EnableStatus == "required" {
			pending.EnableStatus = ""
		}
	}
	var notSent genbuapi.RequestNotSentError
	switch {
	case err == nil && wire.Status == "submitted" && wireMatches:
		pending.Status = "submitted"
	case errors.As(err, &notSent):
		pending.Status = "not-sent"
	case errors.Is(err, production.ErrUATPodSubmitRejected):
		pending.Status = "rejected"
	case !errors.Is(err, production.ErrUATPodSubmitUnknown):
		err = errors.Join(production.ErrUATPodSubmitUnknown, err)
	}
	if pending != reserved {
		if saveErr := saveUATPodReceipt(receiptPath, pending); saveErr != nil {
			err = errors.Join(err, saveErr)
		}
	}
	if err != nil && pending.EnableStatus == "enabled" {
		err = fmt.Errorf("UAT 发布开关已自动开启，%w", err)
	}
	return pending, err
}

func validUATPodResult(result genbuapi.UATPodOperationResult, request genbuapi.UATPodOperationRequest) bool {
	return result.Action == request.Action && result.Tag == request.Tag && result.Environment == "UAT" &&
		result.Service == genbuapi.ResolveService(request.Service).Name && result.AppID != "" && result.GroupID > 0 && result.Group != "" &&
		result.Namespace != "" && result.PodCount > 0 && validUATPodHex(result.PreviewID, 64)
}

func uatPodDisplayResult(result genbuapi.UATPodOperationResult, binding uatPodBinding) CLIUATOperationResult {
	return CLIUATOperationResult{Action: binding.Action, Status: result.Status, Service: binding.Target.Service,
		GenbuService: binding.Target.GenbuService, Environment: result.Environment, GroupID: result.GroupID,
		Group: result.Group, Namespace: result.Namespace, Tag: result.Tag, PodCount: result.PodCount, EnableStatus: result.EnableStatus}
}
