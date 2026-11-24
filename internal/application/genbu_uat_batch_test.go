package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
	"silverwing-go/internal/snowball/genbuapi"
	"silverwing-go/internal/storage"
)

func uatBatchTestFixture(t *testing.T, tags ...string) (*App, Action, []UATBatchReleaseItem) {
	t.Helper()
	app, base := taskServicesFixture(t)
	files := base.Payload["taskServicesContext"].(TaskServicesContext).TaskFilesContext
	batchID := delivery.NewTagJobID()
	var items []UATBatchReleaseItem
	for i, tag := range tags {
		module := []string{"one", "two"}[i]
		op := domain.TagOperation{
			OperationID: delivery.NewTagJobID(), GroupServiceID: "service", ModuleID: module, ServiceName: "service", FolderName: base.TaskID,
			RepositoryID: "repo", SourceBranch: "feature/fixture", Remote: "origin", TagMode: domain.TagBuildCurrentBranch,
			State: domain.TagOperationSuccess, CreatedAt: "2026-11-19T08:00:00Z", UpdatedAt: "2026-11-19T08:00:00Z",
			Tag: &tag, BatchID: &batchID, ConflictFiles: []string{},
			GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuSuccess, UAT: domain.GenbuUnknown, Production: domain.GenbuUnknown},
		}
		saveTagProbeFixture(t, files.TaskPath, op)
		items = append(items, UATBatchReleaseItem{OperationID: op.OperationID, ServiceID: op.GroupServiceID, ModuleID: op.ModuleID, Tag: tag})
	}
	action := Action{Kind: "tag.history.uat-batch-preview", TaskID: base.TaskID, Payload: map[string]any{
		"tagHistoryHome": files.Home, "tagHistoryTaskRoot": files.TaskRoot, "tagHistoryTaskPath": files.TaskPath,
		"tagHistoryTaskCreatedAt": files.TaskCreatedAt, "groupId": batchID, "items": items,
	}}
	return app, action, items
}

func uatBatchConfirmedItems(preview UATBatchReleaseResult) []UATBatchReleaseItem {
	items := make([]UATBatchReleaseItem, len(preview.Items))
	for i, row := range preview.Items {
		items[i] = row.Item
		items[i].Preflight = row.Result.Preflight
	}
	return items
}

func TestCLIBatchReleaseKeepsCompleteBatchAndReceipts(t *testing.T) {
	app, action, items := uatBatchTestFixture(t, uatFixtureTag, uatFixtureTag)
	fixture := &uatHTTPFixture{service: "alias"}
	app.connections.transport = fixture
	request := CLIUATBatchRequest{Batch: textPayload(action, "groupId")}
	preview, err := app.CLIUATBatchRelease(t.Context(), request)
	if err != nil || preview.Task != action.TaskID || preview.BatchID != request.Batch || len(preview.Items) != len(items) ||
		preview.Preflight == "" || fixture.writes.Load() != 0 {
		t.Fatalf("CLI batch preview = %+v, error = %v, writes = %d", preview, err, fixture.writes.Load())
	}
	for _, row := range preview.Items {
		if row.Result.Preflight != "" || row.Result.GroupID != 17 || row.Result.Namespace != "fixture-ns" {
			t.Fatalf("preview leaked an individual confirmation or lost the parsed target: %+v", row)
		}
	}
	request.Preflight = "invalid"
	if _, err := app.CLIUATBatchRelease(t.Context(), request); err == nil || fixture.writes.Load() != 0 {
		t.Fatalf("invalid batch confirmation reached Genbu: %v", err)
	}

	taskPath := textPayload(action, "tagHistoryTaskPath")
	op, err := delivery.ReadTagHistoryOperation(taskPath, preview.Items[0].Item.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	op.UpdatedAt = "2026-11-20T08:00:00Z"
	saveTagProbeFixture(t, taskPath, op)
	request.Task, request.Preflight = action.TaskID, preview.Preflight
	for _, replay := range []bool{false, true} {
		receipt, err := app.CLIUATBatchRelease(t.Context(), request)
		if (err != nil) != replay || len(receipt.Items) != 2 || fixture.writes.Load() != 1 || receipt.Preflight != "" ||
			receipt.Items[0].Result.Status != "submitted" || receipt.Items[1].Result.Status != "submitted" ||
			receipt.Items[1].SharedWith != receipt.Items[0].Item.OperationID {
			t.Fatalf("replay = %v, CLI receipt = %+v, error = %v, writes = %d", replay, receipt, err, fixture.writes.Load())
		}
	}
}

func TestCLIBatchReleaseSkipsProbeOffAndReportsUncertainSubmission(t *testing.T) {
	app, action, items := uatBatchTestFixture(t, uatFixtureTag, "1.5.3.beta-2")
	fixture := &uatHTTPFixture{service: "alias", respond: func() (*http.Response, error) {
		return nil, errors.New("simulated disconnect")
	}}
	app.connections.transport = fixture
	cfg, err := app.Config.LoadForPreview(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	skipped := cfg.Services[0]
	skipped.ID, skipped.DisplayName, skipped.GenbuServiceName, skipped.GenbuProbeEnabled = "skipped", "Skipped service", "skipped-genbu", false
	cfg.Services = append(cfg.Services, skipped)
	if err := app.Config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	taskPath := textPayload(action, "tagHistoryTaskPath")
	op, err := delivery.ReadTagHistoryOperation(taskPath, items[1].OperationID)
	if err != nil {
		t.Fatal(err)
	}
	op.GroupServiceID, op.ServiceName = skipped.ID, skipped.DisplayName
	saveTagProbeFixture(t, taskPath, op)

	request := CLIUATBatchRequest{Batch: textPayload(action, "groupId"), Task: action.TaskID}
	preview, err := app.CLIUATBatchRelease(t.Context(), request)
	if err != nil || len(preview.Items) != 1 || preview.Items[0].Item != items[0] || len(preview.Skipped) != 1 ||
		preview.Skipped[0].OperationID != op.OperationID || preview.Preflight == "" || fixture.writes.Load() != 0 {
		t.Fatalf("filtered preview = %+v, error = %v, writes = %d", preview, err, fixture.writes.Load())
	}
	request.Preflight = preview.Preflight
	for range 2 {
		receipt, err := app.CLIUATBatchRelease(t.Context(), request)
		if err == nil || len(receipt.Items) != 1 || receipt.Items[0].Result.Status != "unknown" ||
			receipt.Items[0].Error == "" || fixture.writes.Load() != 1 {
			t.Fatalf("uncertain CLI receipt = %+v, error = %v, writes = %d", receipt, err, fixture.writes.Load())
		}
	}
}

func TestUATBatchCoalescesIdenticalTargetsAndRetainsReceipts(t *testing.T) {
	app, action, items := uatBatchTestFixture(t, uatFixtureTag, uatFixtureTag)
	fixture := &uatHTTPFixture{service: "alias"}
	app.connections.transport = fixture
	previewAction, err := app.Dispatch(t.Context(), action)
	preview, ok := previewAction.Data.(UATBatchReleaseResult)
	if err != nil || !ok || len(preview.Items) != len(items) || fixture.writes.Load() != 0 {
		t.Fatalf("read-only batch preview = %+v, error = %v, writes = %d", previewAction, err, fixture.writes.Load())
	}
	for i, row := range preview.Items {
		if row.Item != items[i] || row.Result.Status != "preview" || row.Result.Tag != uatFixtureTag || row.Result.GenbuService != fixture.service || row.Result.Preflight == "" {
			t.Fatalf("invalid batch member preview = %+v", row)
		}
	}
	if preview.Items[1].SharedWith != items[0].OperationID || preview.Items[1].Result.Preflight == preview.Items[0].Result.Preflight {
		t.Fatalf("identical Group was not coalesced with both confirmations: %+v", preview)
	}
	otherAction, err := app.Dispatch(t.Context(), action)
	if err != nil {
		t.Fatal(err)
	}
	confirmed := uatBatchConfirmedItems(preview)
	mixed := append([]UATBatchReleaseItem{}, confirmed...)
	mixed[1].Preflight = otherAction.Data.(UATBatchReleaseResult).Items[1].Result.Preflight
	action.Kind = "tag.history.uat-batch-submit"
	for _, invalid := range [][]UATBatchReleaseItem{confirmed[:1], mixed, {confirmed[1], confirmed[0]}} {
		action.Payload["items"] = invalid
		unsubmitted, err := app.Dispatch(t.Context(), action)
		if err == nil || fixture.writes.Load() != 0 {
			t.Fatalf("altered batch admitted: %+v, error = %v, writes = %d", unsubmitted, err, fixture.writes.Load())
		}
		for _, row := range unsubmitted.Data.(UATBatchReleaseResult).Items {
			if row.Result.Status != "not-sent" {
				t.Fatalf("invalid batch returned an uncertain outcome: %+v", row)
			}
		}
	}
	action.Payload["items"] = confirmed
	receiptAction, err := app.Dispatch(t.Context(), action)
	receipt, ok := receiptAction.Data.(UATBatchReleaseResult)
	if err != nil || !ok || fixture.writes.Load() != 1 || receipt.Items[0].Result.Status != "submitted" ||
		receipt.Items[1].Result.Status != "submitted" || receipt.Items[1].SharedWith != items[0].OperationID {
		t.Fatalf("identical Group receipt = %+v, error = %v, writes = %d", receiptAction, err, fixture.writes.Load())
	}
	replay, err := app.Dispatch(t.Context(), action)
	if err == nil || fixture.writes.Load() != 1 || replay.Data.(UATBatchReleaseResult).Items[1].Result.Status != "submitted" {
		t.Fatalf("replay lost the shared receipt or sent again: %+v, error = %v, writes = %d", replay, err, fixture.writes.Load())
	}
}

type uatBatchFake struct {
	groups               map[string]int
	settings             map[string]string
	unknownTag           string
	unknownEnableTag     string
	staleBeforeSubmitTag string
	writes               []string
}

type uatBatchTestNotSent struct{ error }

func (uatBatchTestNotSent) RequestNotSent() {}

func (*uatBatchFake) CheckScope(ctx context.Context) error { return ctx.Err() }

func (f *uatBatchFake) PreviewUATPodOperation(ctx context.Context, _ domain.AppConfig, request genbuapi.UATPodOperationRequest) (genbuapi.UATPodOperationResult, error) {
	group := f.groups[request.Tag]
	if group == 0 || request.Action != "release" {
		return genbuapi.UATPodOperationResult{}, errors.New("unexpected mock release")
	}
	hash := sha256.Sum256(fmt.Appendf(nil, "%s-%d-%s", request.Tag, group, f.settings[request.Tag]))
	var enableStatus string
	if request.Tag == f.unknownEnableTag {
		enableStatus = "required"
	}
	return genbuapi.UATPodOperationResult{Action: request.Action, Status: "preview", Service: genbuapi.ResolveService(request.Service).Name,
		AppID: "876", Environment: "UAT", GroupID: group, Group: fmt.Sprintf("group-%d", group), Namespace: "fixture-ns",
		Tag: request.Tag, PodCount: 1, PreviewID: fmt.Sprintf("%x", hash), EnableStatus: enableStatus}, ctx.Err()
}

func (f *uatBatchFake) SubmitUATPodOperation(ctx context.Context, cfg domain.AppConfig, request genbuapi.UATPodOperationRequest, expected string, beforeWrite func(context.Context, genbuapi.UATPodOperationResult) error) (genbuapi.UATPodOperationResult, error) {
	if request.Tag == f.staleBeforeSubmitTag {
		return genbuapi.UATPodOperationResult{}, production.ErrUATPodPreflightStale
	}
	result, err := f.PreviewUATPodOperation(ctx, cfg, request)
	if err != nil {
		return result, err
	}
	if result.PreviewID != expected {
		return result, production.ErrUATPodPreflightStale
	}
	if err := beforeWrite(ctx, result); err != nil {
		return result, err
	}
	if request.Tag == f.unknownEnableTag {
		result.EnableStatus = "unknown"
		return result, uatBatchTestNotSent{errors.New("无法确认 UAT 开关已开启，发布未提交")}
	}
	f.writes = append(f.writes, request.Tag)
	if request.Tag == f.unknownTag {
		result.Status = "unknown"
		return result, production.ErrUATPodSubmitUnknown
	}
	result.Status = "submitted"
	return result, nil
}

func TestUATBatchRejectsDifferentTagsForSameGroup(t *testing.T) {
	const anotherTag = "1.5.3.beta-2"
	app, action, items := uatBatchTestFixture(t, uatFixtureTag, anotherTag)
	client := &uatBatchFake{groups: map[string]int{uatFixtureTag: 7, anotherTag: 7}}
	preview, err := app.runUATBatchRelease(t.Context(), action, items, false, client)
	if err == nil || len(client.writes) != 0 || !strings.Contains(preview.Items[0].Error, "Group 7") || !strings.Contains(preview.Items[1].Error, "Group 7") {
		t.Fatalf("batch accepted conflicting Group targets: %+v, error = %v, writes = %v", preview, err, client.writes)
	}
	action.Kind = "tag.history.uat-batch-submit"
	blocked, err := app.runUATBatchRelease(t.Context(), action, uatBatchConfirmedItems(preview), true, client)
	if err == nil || blocked.Items[0].Result.Status != "not-sent" || blocked.Items[1].Result.Status != "not-sent" || len(client.writes) != 0 {
		t.Fatalf("batch submitted conflicting targets: %+v, error = %v, writes = %v", blocked, err, client.writes)
	}
}

func TestUATBatchRevalidatesEnabledProbeMembers(t *testing.T) {
	const otherTag = "1.5.3.beta-2"
	app, action, items := uatBatchTestFixture(t, uatFixtureTag, otherTag)
	ctx := t.Context()
	taskPath := textPayload(action, "tagHistoryTaskPath")
	cfg, err := app.Config.LoadForPreview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	other := cfg.Services[0]
	other.ID, other.DisplayName, other.GenbuServiceName = "other", "Other service", "other-alias"
	cfg.Services = append(cfg.Services, other)
	setOtherProbe := func(enabled bool) {
		t.Helper()
		cfg.Services[1].GenbuProbeEnabled = enabled
		if err := app.Config.Save(cfg); err != nil {
			t.Fatal(err)
		}
	}
	setOtherProbe(true)
	manifest, err := app.Manifests.Load(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Services[1].GroupServiceID, manifest.Services[1].ServiceName = other.ID, other.DisplayName
	if err := app.Manifests.Save(taskPath, manifest); err != nil {
		t.Fatal(err)
	}
	op, err := delivery.ReadTagHistoryOperation(taskPath, items[1].OperationID)
	if err != nil {
		t.Fatal(err)
	}
	op.GroupServiceID, op.ServiceName = other.ID, other.DisplayName
	saveTagProbeFixture(t, taskPath, op)
	items[1].ServiceID = other.ID

	client := &uatBatchFake{groups: map[string]int{uatFixtureTag: 7, otherTag: 9}}
	full, err := app.runUATBatchRelease(ctx, action, items, false, client)
	if err != nil {
		t.Fatal(err)
	}
	submit := action
	submit.Kind = "tag.history.uat-batch-submit"
	setOtherProbe(false)
	confirmed := uatBatchConfirmedItems(full)
	for _, invalid := range [][]UATBatchReleaseItem{confirmed, confirmed[:1], confirmed[1:]} {
		blocked, err := app.runUATBatchRelease(ctx, submit, invalid, true, client)
		if err == nil || len(client.writes) != 0 || blocked.Items[0].Result.Status != "not-sent" {
			t.Fatalf("accepted outdated confirmation or disabled target: %+v, error = %v, writes = %v", blocked, err, client.writes)
		}
	}

	// A preview must contact only the currently enabled target.
	delete(client.groups, otherTag)
	filtered, err := app.runUATBatchRelease(ctx, action, items[:1], false, client)
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].Item != items[0] {
		t.Fatalf("filtered preview = %+v, error = %v", filtered, err)
	}
	setOtherProbe(true)
	blocked, err := app.runUATBatchRelease(ctx, submit, uatBatchConfirmedItems(filtered), true, client)
	if err == nil || len(client.writes) != 0 || blocked.Items[0].Result.Status != "not-sent" {
		t.Fatalf("accepted a partial batch after re-enabling a member: %+v, error = %v, writes = %v", blocked, err, client.writes)
	}
	setOtherProbe(false)
	filtered, err = app.runUATBatchRelease(ctx, action, items[:1], false, client)
	if err != nil {
		t.Fatal(err)
	}
	results, err := app.runUATBatchRelease(ctx, submit, uatBatchConfirmedItems(filtered), true, client)
	if err != nil || len(client.writes) != 1 || client.writes[0] != uatFixtureTag ||
		len(results.Items) != 1 || results.Items[0].Result.Status != "submitted" {
		t.Fatalf("filtered release = %+v, error = %v, writes = %v", results, err, client.writes)
	}
}

func TestUATBatchValidatesAllGroupsBeforeWritingAndReturnsPartialReceipts(t *testing.T) {
	const anotherTag = "1.5.3.beta-2"
	app, previewAction, items := uatBatchTestFixture(t, uatFixtureTag, anotherTag)
	client := &uatBatchFake{groups: map[string]int{uatFixtureTag: 7, anotherTag: 9}, settings: make(map[string]string), unknownTag: uatFixtureTag}
	preview, err := app.runUATBatchRelease(t.Context(), previewAction, items, false, client)
	if err != nil {
		t.Fatal(err)
	}
	action := previewAction
	action.Kind = "tag.history.uat-batch-submit"
	confirmed := uatBatchConfirmedItems(preview)
	client.settings[anotherTag] = "changed after confirmation"
	stale, err := app.runUATBatchRelease(t.Context(), action, confirmed, true, client)
	if err == nil || len(client.writes) != 0 || stale.Items[0].Result.Status != "not-sent" || stale.Items[1].Result.Status != "not-sent" || stale.Items[1].Error == "" {
		t.Fatalf("expected stale second Group to prevent all writes: %+v, error = %v, writes = %v", stale, err, client.writes)
	}
	preview, err = app.runUATBatchRelease(t.Context(), previewAction, items, false, client)
	if err != nil {
		t.Fatal(err)
	}
	confirmed = uatBatchConfirmedItems(preview)
	unlock, err := acquireUATPodGroup(9)
	if err != nil {
		t.Fatal(err)
	}
	locked, err := app.runUATBatchRelease(t.Context(), action, confirmed, true, client)
	unlock()
	if !errors.Is(err, storage.ErrLockBusy) || len(client.writes) != 0 || locked.Items[0].Result.Status != "not-sent" {
		t.Fatalf("holding second Group lock did not prevent first write: %+v, error = %v, writes = %v", locked, err, client.writes)
	}
	client.staleBeforeSubmitTag = uatFixtureTag
	stopped, err := app.runUATBatchRelease(t.Context(), action, confirmed, true, client)
	if !errors.Is(err, production.ErrUATPodPreflightStale) || stopped.Items[0].Result.Status != "not-sent" ||
		stopped.Items[1].Result.Status != "not-sent" || len(client.writes) != 0 {
		t.Fatalf("first Group failed final recheck but later Groups continued: %+v, error = %v, writes = %v", stopped, err, client.writes)
	}
	client.staleBeforeSubmitTag = ""
	client.unknownEnableTag = uatFixtureTag
	stopped, err = app.runUATBatchRelease(t.Context(), action, confirmed, true, client)
	var notSent genbuapi.RequestNotSentError
	if !errors.As(err, &notSent) || stopped.Items[0].Result.Status != "not-sent" || stopped.Items[0].Result.EnableStatus != "unknown" ||
		stopped.Items[1].Result.Status != "not-sent" || len(client.writes) != 0 {
		t.Fatalf("first switch unconfirmed but release requests continued: %+v, error = %v, writes = %v", stopped, err, client.writes)
	}
	client.unknownEnableTag = ""
	preview, err = app.runUATBatchRelease(t.Context(), previewAction, items, false, client)
	if err != nil {
		t.Fatal(err)
	}
	confirmed = uatBatchConfirmedItems(preview)
	result, err := app.runUATBatchRelease(t.Context(), action, confirmed, true, client)
	if err != nil || len(client.writes) != 2 || result.Items[0].Result.Status != "unknown" || result.Items[0].Error == "" || result.Items[1].Result.Status != "submitted" {
		t.Fatalf("partial receipts = %+v, error = %v, writes = %v", result, err, client.writes)
	}
	if replay, err := app.runUATBatchRelease(t.Context(), action, confirmed, true, client); err == nil || len(client.writes) != 2 || replay.Items[0].Result.Status != "unknown" || replay.Items[1].Result.Status != "submitted" {
		t.Fatalf("batch replay = %+v, error = %v, writes = %v", replay, err, client.writes)
	}
}
