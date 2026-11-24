package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"sort"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
	"silverwing-go/internal/workspace"
)

type UATBatchReleaseItem struct {
	OperationID string `json:"operationId"`
	ServiceID   string `json:"serviceId"`
	ModuleID    string `json:"moduleId"`
	Tag         string `json:"tag"`
	Preflight   string `json:"preflight,omitempty"`
}

type UATBatchReleaseRow struct {
	Item       UATBatchReleaseItem   `json:"item"`
	Result     CLIUATOperationResult `json:"result"`
	Error      string                `json:"error,omitempty"`
	SharedWith string                `json:"sharedWith,omitempty"`
}

type UATBatchReleaseResult struct {
	Items []UATBatchReleaseRow `json:"items"`
}

type uatBatchMember struct {
	operation uatPodOperation
	preflight uatPodPreflight
	leader    int
}

func (a *App) uatBatchReleaseAction(ctx context.Context, action Action) (ActionResult, error) {
	if _, provided := action.Payload["environment"]; provided && !strings.EqualFold(textPayload(action, "environment"), "uat") {
		return ActionResult{}, errors.New("批量发布仅支持 UAT")
	}
	if _, provided := action.Payload["operation"]; provided && textPayload(action, "operation") != "release" {
		return ActionResult{}, errors.New("Tag 批次仅支持批量发布到 UAT")
	}
	var items []UATBatchReleaseItem
	if err := decodeField(action, "items", &items); err != nil || len(items) == 0 {
		return ActionResult{}, errors.New("请选择 Tag 批次中已开启 Genbu 探测的全部记录后发布到 UAT")
	}
	submit := action.Kind == "tag.history.uat-batch-submit"
	client, err := a.genbuClient(ctx)
	if err != nil {
		return ActionResult{Data: initialUATBatchResult(items, submit)}, err
	}
	result, err := a.runUATBatchRelease(ctx, action, items, submit, client)
	return ActionResult{Data: result}, err
}

func initialUATBatchResult(items []UATBatchReleaseItem, submit bool) UATBatchReleaseResult {
	result := UATBatchReleaseResult{Items: make([]UATBatchReleaseRow, len(items))}
	for i, item := range items {
		result.Items[i].Item = item
		result.Items[i].Item.Preflight = ""
		if submit {
			result.Items[i].Result = CLIUATOperationResult{Action: "release", Status: "not-sent", Tag: item.Tag, Environment: "UAT"}
		}
	}
	return result
}

func (a *App) runUATBatchRelease(ctx context.Context, action Action, items []UATBatchReleaseItem, submit bool, client uatPodClient) (UATBatchReleaseResult, error) {
	result := initialUATBatchResult(items, submit)
	nonce, err := uatBatchNonce(items, submit)
	if err != nil {
		return result, err
	}
	taskPath, identity, err := a.uatBatchIdentity(ctx, action, items, nonce)
	if err != nil {
		return result, err
	}
	readSlot, err := acquireTaskServiceReadSlot(ctx)
	if err != nil {
		return result, err
	}
	defer readSlot()
	checkBatch := func(ctx context.Context) error {
		_, current, err := a.uatBatchIdentity(ctx, action, items, nonce)
		if err != nil {
			return err
		}
		if current != identity {
			return errors.New("Tag 批次已变化，请重新预检")
		}
		return nil
	}
	members := make([]uatBatchMember, len(items))
	failed := false
	for i, item := range items {
		operation, err := a.prepareUATBatchMember(ctx, action, item, identity, client)
		if err != nil {
			result.Items[i].Error, failed = err.Error(), true
			continue
		}
		members[i].operation = operation
		if submit {
			result.Items[i].Result.Service = operation.binding.Target.Service
			result.Items[i].Result.GenbuService = operation.binding.Target.GenbuService
			_, token, _ := strings.Cut(item.Preflight, ":")
			members[i].preflight, err = operation.binding.checkPreflight(token)
		} else {
			err = previewUATBatchMember(ctx, &members[i], &result.Items[i], nonce, client)
		}
		if err != nil {
			result.Items[i].Error, failed = err.Error(), true
		}
	}
	if failed {
		return result, errors.New("批量发布预检失败，请查看各项原因")
	}
	if err := assignUATBatchLeaders(members, result.Items); err != nil {
		return result, err
	}
	if !submit {
		return result, checkBatch(ctx)
	}
	unlock, err := acquireUATBatchGroups(members)
	if err != nil {
		return result, err
	}
	defer unlock()
	err = workspace.NewTaskOperationLocks(a.Paths).WithTask(taskPath, func() error {
		if err := checkBatch(ctx); err != nil {
			return err
		}
		if err := a.recheckUATBatch(ctx, members, result.Items, client); err != nil {
			return err
		}
		if err := checkBatch(ctx); err != nil {
			return err
		}
		return a.submitUATBatch(ctx, members, result.Items, client, checkBatch)
	})
	return result, err
}

func (a *App) prepareUATBatchMember(ctx context.Context, action Action, item UATBatchReleaseItem, identity string, client uatPodClient) (uatPodOperation, error) {
	action.ServiceID, action.ModuleID = item.ServiceID, item.ModuleID
	action.Payload = maps.Clone(action.Payload)
	action.Payload["operationId"] = item.OperationID
	resolve := func(ctx context.Context) (domain.AppConfig, uatServiceTarget, error) {
		cfg, target, err := a.uatTagHistoryTarget(ctx, action, item.Tag)
		target.BatchIdentity = identity
		return cfg, target, err
	}
	return a.prepareUATPodOperation(ctx, CLIUATOperationRequest{Action: "release", Tag: item.Tag}, resolve, client)
}

func previewUATBatchMember(ctx context.Context, member *uatBatchMember, row *UATBatchReleaseRow, nonce string, client uatPodClient) error {
	operation := member.operation
	wire, err := operation.preview(ctx, client)
	if err != nil {
		return err
	}
	member.preflight = uatPodPreflight{groupID: wire.GroupID, nativeID: wire.PreviewID}
	row.Result = uatPodDisplayResult(wire, operation.binding)
	token, err := operation.binding.newPreflight(wire.GroupID, wire.PreviewID)
	if err != nil {
		return err
	}
	row.Result.Preflight = nonce + ":" + token
	return nil
}

func assignUATBatchLeaders(members []uatBatchMember, rows []UATBatchReleaseRow) error {
	leaders := make(map[int]int)
	var conflict bool
	for i := range members {
		preflight := members[i].preflight
		leader, exists := leaders[preflight.groupID]
		if !exists {
			leaders[preflight.groupID], members[i].leader = i, i
			continue
		}
		if rows[i].Item.Tag != rows[leader].Item.Tag || preflight.nativeID != members[leader].preflight.nativeID {
			message := fmt.Sprintf("UAT Pod Group %d 的发布 Tag 或配置不同，无法合并提交", preflight.groupID)
			rows[i].Error, rows[leader].Error, conflict = message, message, true
			continue
		}
		members[i].leader, rows[i].SharedWith = leader, rows[leader].Item.OperationID
	}
	if conflict {
		return errors.New("批次中有相互冲突的 UAT Pod Group")
	}
	return nil
}

func acquireUATBatchGroups(members []uatBatchMember) (func(), error) {
	groupIDs := make([]int, 0, len(members))
	for i, member := range members {
		if i == member.leader {
			groupIDs = append(groupIDs, member.preflight.groupID)
		}
	}
	sort.Ints(groupIDs)
	unlocks := make([]func(), 0, len(groupIDs))
	release := func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}
	for _, id := range groupIDs {
		unlock, err := acquireUATPodGroup(id)
		if err != nil {
			release()
			return nil, err
		}
		unlocks = append(unlocks, unlock)
	}
	return release, nil
}

func (a *App) recheckUATBatch(ctx context.Context, members []uatBatchMember, rows []UATBatchReleaseRow, client uatPodClient) error {
	consumed := false
	for i, member := range members {
		if i != member.leader {
			shareUATBatchResult(&rows[i], rows[member.leader], member.operation.binding.Target)
			continue
		}
		if receipt, err := readUATPodReceipt(a.uatPodReceiptPath(member.preflight)); err != nil {
			if receipt.Status != "" {
				rows[i].Result = receipt
			}
			rows[i].Error, consumed = err.Error(), true
		}
	}
	if consumed {
		return errors.New("此批次预检已有提交记录，请核对各项结果")
	}
	failed := false
	for i, member := range members {
		wire, err := member.operation.preview(ctx, client)
		if err == nil && (wire.GroupID != member.preflight.groupID || wire.PreviewID != member.preflight.nativeID) {
			err = production.ErrUATPodPreflightStale
		}
		if err != nil {
			rows[i].Error, failed = err.Error(), true
		} else {
			rows[i].Result = uatPodDisplayResult(wire, member.operation.binding)
			rows[i].Result.Status = "not-sent"
		}
	}
	if failed {
		return errors.New("UAT 批次目标已变化或不可用，请重新预检")
	}
	return nil
}

func (a *App) submitUATBatch(ctx context.Context, members []uatBatchMember, rows []UATBatchReleaseRow, client uatPodClient, checkBatch func(context.Context) error) error {
	firstGroup := true
	beforeWrite := func(ctx context.Context) error {
		if err := checkBatch(ctx); err != nil {
			return err
		}
		// Recheck every target before the batch's first write.
		if !firstGroup {
			return nil
		}
		for i, member := range members {
			if err := member.operation.recheck(ctx); err != nil {
				rows[i].Error = err.Error()
				return err
			}
		}
		return nil
	}
	for i, member := range members {
		if i != member.leader {
			shareUATBatchResult(&rows[i], rows[member.leader], member.operation.binding.Target)
			continue
		}
		receipt, err := a.submitUATPodOperation(ctx, member.operation, member.preflight, client, beforeWrite)
		if receipt.Status != "" {
			rows[i].Result = receipt
		}
		if err != nil {
			rows[i].Error = err.Error()
			if firstGroup && rows[i].Result.Status == "not-sent" {
				return fmt.Errorf("UAT 批次未提交：%w", err)
			}
		}
		firstGroup = false
	}
	return nil
}

func shareUATBatchResult(row *UATBatchReleaseRow, leader UATBatchReleaseRow, target uatServiceTarget) {
	row.Result, row.Error = leader.Result, leader.Error
	if row.Result.Status != "" {
		row.Result.Service, row.Result.GenbuService = target.Service, target.GenbuService
	}
}

func uatBatchNonce(items []UATBatchReleaseItem, submit bool) (string, error) {
	if !submit {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return "", err
		}
		return hex.EncodeToString(nonce[:]), nil
	}
	nonce, _, _ := strings.Cut(items[0].Preflight, ":")
	if !validUATPodHex(nonce, 32) {
		return "", errors.New("请先预检并确认同一批次的 UAT 发布")
	}
	for _, item := range items {
		current, token, ok := strings.Cut(item.Preflight, ":")
		if !ok || current != nonce || token == "" {
			return "", errors.New("预检编号不属于同一次批量预检，请重新预检")
		}
	}
	return nonce, nil
}

func (a *App) uatBatchIdentity(ctx context.Context, action Action, items []UATBatchReleaseItem, nonce string) (string, string, error) {
	cfg, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return "", "", err
	}
	_, tasks, err := a.tagHistoryActionScope(cfg, action)
	if err != nil {
		return "", "", err
	}
	task := tasks[0]
	if task.LifecycleStatus == domain.TaskArchived {
		return "", "", errors.New("已归档的任务不能批量发布到 UAT")
	}
	groupID := textPayload(action, "groupId")
	if groupID == "" {
		return "", "", errors.New("缺少 Tag 批次标识，请刷新 Tag 历史")
	}
	history, err := a.delivery(cfg).History(ctx, task.TaskDirectoryName)
	if err != nil {
		return "", "", err
	}
	owned := make(map[string]domain.TagOperation, len(items))
	for _, report := range history {
		op := report.Operation
		if uatTagBatchGroupID(op) != groupID {
			continue
		}
		if _, duplicate := owned[op.OperationID]; duplicate {
			return "", "", errors.New("当前 Tag 批次包含重复记录，请检查 Tag 历史")
		}
		owned[op.OperationID] = op
	}
	for id, op := range owned {
		if uatBatchProbeDisabled(cfg, op.GroupServiceID) {
			delete(owned, id)
		}
	}
	if len(owned) != len(items) {
		return "", "", errors.New("Tag 批次已变化或选择不完整，请刷新 Tag 历史")
	}
	var recordIdentity [][6]string
	for _, item := range items {
		op, ok := owned[item.OperationID]
		if !ok || item.ServiceID != op.GroupServiceID || item.ModuleID != op.ModuleID || item.Tag != value(op.Tag) {
			return "", "", errors.New("Tag 批次记录已变化或选择不完整，请刷新 Tag 历史")
		}
		delete(owned, item.OperationID)
		recordIdentity = append(recordIdentity, [6]string{op.OperationID, op.GroupServiceID, op.ModuleID, value(op.Tag), op.CreatedAt, op.RepositoryID})
	}
	data, _ := json.Marshal([]any{groupID, nonce, recordIdentity})
	return filepath.Join(*cfg.TaskRoot, task.TaskDirectoryName), fmt.Sprintf("%x", sha256.Sum256(data)), ctx.Err()
}

func uatBatchProbeDisabled(cfg domain.AppConfig, serviceID string) bool {
	service, err := cfg.Service(serviceID)
	return err == nil && !service.GenbuProbeEnabled
}

func uatTagBatchGroupID(op domain.TagOperation) string {
	if batchID := value(op.BatchID); strings.TrimSpace(batchID) != "" {
		return batchID
	}
	return op.OperationID
}
