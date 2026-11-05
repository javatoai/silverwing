package application

import (
	"context"
	"silverwing-go/internal/domain"
	"strings"
	"testing"
)

func TestBranchNoticeReadsOwnedTaskWithoutTagOrGenbu(t *testing.T) {
	a, scope, _ := tagJobProbeFixture(t)
	cfg, e := a.Config.Load()
	if e != nil {
		t.Fatal(e)
	}
	cfg.TagEnabled = false
	notice := cfg.EffectiveLarkNotice()
	notice.BranchChatID = "oc_configured"
	cfg.LarkNotice = &notice
	if e = a.Config.Save(cfg); e != nil {
		t.Fatal(e)
	}
	cfg, _ = a.Config.Load()
	action := Action{Kind: "task.branch-notice.prepare", TaskID: scope.TaskID, Payload: map[string]any{
		"noticeConfigIdentity": BranchNoticeConfigIdentity(cfg),
		"taskFilesContext":     TaskFilesContext{Home: scope.Home, TaskRoot: scope.TaskRoot, TaskPath: scope.TaskPath, TaskCreatedAt: scope.TaskCreatedAt}}}
	b, e := a.branchNoticeScope(context.Background(), action)
	if e != nil || len(b.rows) != 2 || b.rows[0].Branch != "feature/synthetic" {
		t.Fatal(b, e)
	}
	// No CLI runner or integrations are supplied: a scope read must not query either.
	if a.Runner != nil || a.Integrations != nil {
		t.Fatal("fixture accidentally permits external queries")
	}
	entry, e := a.Manifests.LoadTask(scope.TaskRoot, scope.TaskID)
	if e != nil {
		t.Fatal(e)
	}
	entry.Manifest.Services[0].Branch = "feature/changed"
	if e = a.Manifests.Save(scope.TaskPath, entry.Manifest); e != nil {
		t.Fatal(e)
	}
	changed, e := a.branchNoticeScope(context.Background(), action)
	if e != nil || changed.scope == b.scope {
		t.Fatal("branch change reused confirmed scope", e)
	}
	action.Kind = "task.branch-notice.send"
	if _, e = a.Dispatch(context.Background(), action); e == nil || !strings.Contains(e.Error(), "显式") {
		t.Fatal("unconfirmed send reached CLI", e)
	}
	notice.BranchChatID = "oc_other"
	cfg.LarkNotice = &notice
	if e = a.Config.Save(cfg); e != nil {
		t.Fatal(e)
	}
	if _, e = a.branchNoticeScope(context.Background(), action); e != nil {
		t.Fatal("retired group changed self-send scope", e)
	}
	executable := "C:/isolated/changed-lark.exe"
	cfg.LarkExecutablePath = &executable
	if e = a.Config.Save(cfg); e != nil {
		t.Fatal(e)
	}
	if _, e = a.branchNoticeScope(context.Background(), action); e == nil {
		t.Fatal("changed CLI accepted stale request")
	}
}
func TestBranchNoticeRejectsRecreatedTaskAndRetainsRetiredConfig(t *testing.T) {
	a, scope, _ := tagJobProbeFixture(t)
	cfg, _ := a.Config.Load()
	action := Action{Kind: "task.branch-notice.prepare", TaskID: scope.TaskID, Payload: map[string]any{
		"noticeConfigIdentity": BranchNoticeConfigIdentity(cfg),
		"taskFilesContext":     TaskFilesContext{Home: scope.Home, TaskRoot: scope.TaskRoot, TaskPath: scope.TaskPath, TaskCreatedAt: "old"}}}
	if _, e := a.branchNoticeScope(context.Background(), action); e == nil {
		t.Fatal("old task identity accepted")
	}
	notice := domain.DefaultLarkNoticeConfig()
	if len(notice.Contacts) != 0 {
		t.Fatal("built-in contacts remain")
	}
	notice.BranchChatID = "ou_person"
	if e := notice.Validate(); e != nil {
		t.Fatal("retired group value blocks settings", e)
	}
	notice.BranchChatID = "oc_group"
	if e := notice.Validate(); e != nil {
		t.Fatal(e)
	}
}

func TestNoticeSelectionRejectsRetiredReceiverOverrides(t *testing.T) {
	for _, payload := range []any{nil, map[string]any{"keys": []string{"ou_other"}}, map[string]any{"selfOpenId": "ou_other"}, map[string]any{"selfName": "untrusted"}, map[string]any{"chatId": "oc_group"}, map[string]any{"receivers": []string{"ou_other"}}} {
		if _, e := noticeSelection(Action{Payload: map[string]any{"selection": payload}}); e == nil {
			t.Fatal("receiver override accepted", payload)
		}
	}
	choice, e := noticeSelection(Action{Payload: map[string]any{"selection": map[string]any{"retry": true, "resend": true, "resendAfter": "previous"}}})
	if e != nil || !choice.Retry || !choice.Resend || choice.ResendAfter != "previous" {
		t.Fatal(choice, e)
	}
}
