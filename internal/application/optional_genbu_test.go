package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
)

func setTestGenbuEnabled(t *testing.T, app *App, enabled bool) domain.AppConfig {
	t.Helper()
	cfg, err := app.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.OptionalCLIs.Genbu = &enabled
	if err := app.Config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func useVerifiedGenbuFixture(t *testing.T, app *App) domain.AppConfig {
	t.Helper()
	saveFixturePlatformAccount(t)
	app.optionalCLIRequire = func(ctx context.Context, tool string) error {
		if tool != "genbu" {
			return fmt.Errorf("unexpected CLI requirement %q", tool)
		}
		return ctx.Err()
	}
	return setTestGenbuEnabled(t, app, true)
}

func TestDisabledGenbuLeavesLocalTagHistoryAvailable(t *testing.T) {
	ctx := context.Background()
	a, scope, operations := tagJobProbeFixture(t)
	a.optionalCLIRequire = nil
	setTestGenbuEnabled(t, a, false)
	a.tagJobManager().probe = func(context.Context, domain.AppConfig, []domain.TaskManifest, bool, []string, func(string, domain.TagOperation)) (bool, error) {
		return false, errors.New("disabled Genbu reached the probe")
	}
	result, err := a.DispatchTagProbe(ctx, tagProbeFixtureAction(scope, "tag.history.refresh", ""), nil)
	if err != nil || len(result.Data.(TagHistoryRefreshResult).Records) != len(operations) {
		t.Fatal("disabled Genbu prevented refreshing local history", result, err)
	}
	for _, kind := range []string{"tag.genbu.refresh", "tag.status", "tag.history.build-log", "production.list", "production.delete", "production.diff-files"} {
		action := tagProbeFixtureAction(scope, kind, operations["fast"].OperationID)
		if _, err := a.Dispatch(ctx, action); err == nil || !strings.Contains(err.Error(), "Genbu") {
			t.Fatalf("%s did not reject a disabled integration: %v", kind, err)
		}
	}
	if _, err := a.DispatchTagProbe(ctx, tagProbeFixtureAction(scope, "tag.genbu.refresh", ""), nil); err == nil {
		t.Fatal("disabled Genbu refresh was admitted to the streaming backend")
	}
}

func TestDisabledGenbuBlocksRetagButAllowsGitRetry(t *testing.T) {
	ctx := context.Background()
	a, scopes, tasks := tagJobManagerFixture(t, "synthetic")
	scope, task := scopes["synthetic"], tasks["synthetic"]
	id := delivery.NewTagJobID()
	tag := "1.2.3"
	op := domain.TagOperation{OperationID: id, GroupServiceID: "svc", ModuleID: "module", State: domain.TagOperationSuccess, Tag: &tag,
		GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuFailed}}
	task.records[id] = op
	action := Action{Kind: "tag.retry", TaskID: scope.TaskID, Payload: map[string]any{"operationId": id}}
	if _, err := a.StartTagJob(ctx, scope, action); err == nil || !strings.Contains(err.Error(), "平台连接") {
		t.Fatalf("Genbu retry passed disabled capability checks: %v", err)
	}
	op.State = domain.TagOperationFailed
	task.records[id] = op
	job, err := a.StartTagJob(ctx, scope, action)
	if err != nil {
		t.Fatal("disabled Genbu prevented Git retry", err)
	}
	if snapshot := waitTagJob(t, a, scope, job.ID); snapshot.Error != "" || snapshot.Items[0].Phase != TagJobPhaseCompleted {
		t.Fatal("Git retry failed", snapshot)
	}
}

func TestGenbuConfigTokensRejectRetiredScope(t *testing.T) {
	ctx := context.Background()
	a := &App{}
	retired := false
	check := func(context.Context) error {
		if retired {
			return errOptionalCLIScopeChanged
		}
		return nil
	}
	const identity = "same account and connection"
	scope := genbuConfigPreviewScope{TaskID: "task", ServiceID: "service", Environment: "uat"}
	preview := genbuConfigPreview{scope: scope, identity: identity, checkCLI: check}
	previewToken, _, err := a.genbuConfigPreviews.save(ctx, preview)
	if err != nil {
		t.Fatal(err)
	}
	source := genbuConfigSource{scope: scope, identity: identity, checkCLI: check, target: genbuConfigTarget{file: "app.properties", snapshotID: "snapshot"}}
	sourceToken, err := a.genbuConfigSources.save(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	retired = true
	if _, err := a.genbuConfigPreviews.take(ctx, previewToken, scope, identity); !errors.Is(err, errGenbuConfigPreviewGone) {
		t.Fatal("old preview survived capability retirement", err)
	}
	if _, err := a.genbuConfigSources.target(ctx, sourceToken, scope, identity, source.target.file, source.target.snapshotID); !errors.Is(err, production.ErrGenbuConfigPreviewStale) {
		t.Fatal("old source survived capability retirement", err)
	}
	if _, _, err := a.genbuConfigPreviews.save(ctx, preview); !errors.Is(err, errOptionalCLIScopeChanged) {
		t.Fatal("late preview survived capability retirement", err)
	}
	if _, err := a.genbuConfigSources.save(ctx, source); !errors.Is(err, errOptionalCLIScopeChanged) {
		t.Fatal("late source survived capability retirement", err)
	}
}
