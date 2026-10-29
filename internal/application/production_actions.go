package application

import (
	"context"
	"encoding/json"
	"errors"

	"silverwing-go/internal/production"
	"silverwing-go/internal/workspace"
)

func (a *App) productionService() (*production.Service, error) {
	c, err := a.Config.Load()
	if err != nil {
		return nil, err
	}
	service := production.New(a.Paths, a.Config, a.git(c), a.Runner)
	service.DiffSessions = a.workspaceGitDiffSessions()
	return service, nil
}

func (a *App) productionAction(ctx context.Context, action Action) (ActionResult, error) {
	if action.Kind == "production.delete" {
		if !boolPayload(action, "confirmed") {
			return ActionResult{}, errors.New("请先确认删除本地批次记录")
		}
		// Local history management must work without configured CLIs or a
		// reachable/configured repository, including removed historical services.
		s := production.New(a.Paths, nil, nil, nil)
		if err := s.Delete(ctx, textPayload(action, "batchId"), textPayload(action, "recordToken")); err != nil {
			return ActionResult{}, err
		}
		return ActionResult{Message: "本地批次记录已删除"}, nil
	}
	s, err := a.productionService()
	if err != nil {
		return ActionResult{}, err
	}
	if action.Kind == "production.diff-files" || action.Kind == "production.diff-preview" {
		// Diff reads only need the confirmed target, never build/release status.
		// Read older preview envelopes without decoding their unrelated enums.
		var request struct {
			Target  production.Target `json:"target"`
			Preview struct {
				Target production.Target `json:"target"`
			} `json:"preview"`
		}
		if err := decodePayload(action.Payload, &request); err != nil {
			return ActionResult{}, err
		}
		target := request.Target
		if _, direct := action.Payload["target"]; !direct {
			target = request.Preview.Target
		}
		var change *workspace.CompareChange
		if action.Kind == "production.diff-preview" {
			change = &workspace.CompareChange{Path: action.Path, OldPath: textPayload(action, "oldPath"), Kind: textPayload(action, "fileKind")}
		}
		if !boolPayload(action, "retainSession") && textPayload(action, "sessionId") == "" {
			data, err := s.Difference(ctx, target, textPayload(action, "sourceSha"), textPayload(action, "targetSha"), boolPayload(action, "incoming"), change)
			return ActionResult{Data: data}, err
		}
		data, err := s.DifferenceSession(ctx, target, textPayload(action, "sourceSha"), textPayload(action, "targetSha"), boolPayload(action, "incoming"), textPayload(action, "sessionId"), change)
		return ActionResult{Data: data}, err
	}
	var report production.Report
	var batch production.Batch
	id := textPayload(action, "batchId")
	confirmed := boolPayload(action, "confirmed")
	var preview production.Preview
	if raw, exists := action.Payload["preview"]; exists {
		data, e := json.Marshal(raw)
		if e != nil {
			return ActionResult{}, e
		}
		if action.Kind == "production.prepare-tag" {
			// Build preparation needs the confirmation scope and manual baseline,
			// never display-only status enums from an older or partial preview.
			var baseline struct {
				Target               production.Target `json:"target"`
				Manual               bool              `json:"manual"`
				ProductionTag        string            `json:"productionTag"`
				ProductionSHA        string            `json:"productionSha"`
				ProductionCandidates []string          `json:"productionCandidates"`
				RuntimeAvailable     bool              `json:"runtimeAvailable"`
			}
			if e = json.Unmarshal(data, &baseline); e != nil {
				return ActionResult{}, e
			}
			preview = production.Preview{Target: baseline.Target, Manual: baseline.Manual, ProductionTag: baseline.ProductionTag,
				ProductionSHA: baseline.ProductionSHA, ProductionCandidates: baseline.ProductionCandidates, RuntimeAvailable: baseline.RuntimeAvailable}
		} else if e = json.Unmarshal(data, &preview); e != nil {
			return ActionResult{}, e
		}
	}
	switch action.Kind {
	case "production.list":
	case "production.inspect":
		if id != "" {
			batch, preview, err = s.InspectBatch(ctx, id, textPayload(action, "productionTag"))
		} else {
			preview, err = s.Inspect(ctx, production.Selection{GroupID: textPayload(action, "groupId"), ServiceID: action.ServiceID}, textPayload(action, "productionTag"))
		}
		report.Preview = &preview
	case "production.feature-preview":
		batch, preview, err = s.FeaturePreview(ctx, id, textPayload(action, "branch"))
		report.Preview = &preview
	case "production.pending-preview":
		batch, preview, err = s.PendingPreview(ctx, id)
		report.Preview = &preview
	case "production.sync":
		batch, err = s.CheckForTag(ctx, id, preview)
	case "production.prepare-tag":
		batch, preview, err = s.PrepareTag(ctx, id, preview)
		if preview.Target.ServiceID != "" {
			report.Preview = &preview
		}
	case "production.create", "production.merge-main", "production.merge-release", "production.merge", "production.publish", "production.end", "production.accept-remote", "production.rebase-pending", "production.reprepare-tag":
		if !confirmed {
			return ActionResult{}, errors.New("请先确认生产发布目标和提交范围")
		}
		switch action.Kind {
		case "production.create":
			batch, err = s.Create(ctx, preview, textPayload(action, "version"), id)
		case "production.merge-main":
			batch, err = s.MergeProduction(ctx, id, preview)
		case "production.merge-release":
			batch, err = s.MergeMainToRelease(ctx, id, preview)
		case "production.merge":
			batch, err = s.MergeFeature(ctx, id, textPayload(action, "branch"), textPayload(action, "sourceSha"), textPayload(action, "releaseSha"))
		case "production.publish":
			batch, err = s.Publish(ctx, id, textPayload(action, "token"))
		case "production.end":
			batch, err = s.End(ctx, id)
		case "production.accept-remote":
			batch, err = s.AcceptRemote(ctx, id, textPayload(action, "releaseSha"))
		case "production.rebase-pending":
			batch, err = s.RebasePending(ctx, id, textPayload(action, "remoteSha"))
		case "production.reprepare-tag":
			batch, err = s.ReprepareUnpushedTag(ctx, id)
		}
	case "production.resume":
		if !confirmed {
			return ActionResult{}, errors.New("请确认校验并继续生产发布操作")
		}
		batch, err = s.Resume(ctx, id)
	case "production.abort-merge":
		if !confirmed {
			return ActionResult{}, errors.New("请先确认放弃修改并撤回合并")
		}
		var request struct {
			Target production.Target `json:"target"`
			Merge  production.Merge  `json:"merge"`
		}
		if err = decodePayload(action.Payload, &request); err != nil {
			return ActionResult{}, err
		}
		batch, err = s.AbortMerge(ctx, id, request.Target, request.Merge)
	case "production.refresh-build":
		batch, err = s.RefreshBuild(ctx, id)
	case "production.reconcile-ended":
		batch, err = s.ReconcileEnded(ctx, id)
	case "production.open-ide":
		var path, tool string
		path, tool, err = s.ConflictWorkspace(ctx, id)
		if err == nil {
			return a.systemAction(ctx, Action{Kind: "system.ide", Path: path, Payload: map[string]any{"tool": tool}})
		}
	default:
		return ActionResult{}, errors.New("不支持的生产发布操作")
	}
	if batch.ID != "" {
		if err != nil {
			if saved, loadErr := s.Store.Load(batch.ID); loadErr == nil {
				batch = saved
			}
		}
		report.Batch = &batch
	}
	if action.Kind != "production.refresh-build" {
		var listErr error
		report.Batches, listErr = s.Store.List()
		if listErr != nil {
			err = errors.Join(err, listErr)
		}
	}
	message := "生产发布已更新"
	if batch.Message != "" {
		message = batch.Message
	}
	return ActionResult{Message: message, Data: report}, err
}
