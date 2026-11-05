package desktop

import (
	"encoding/json"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/releasenotice"
	"testing"
)

func branchNoticeDesktopFixture(t *testing.T) (*app, *noticeSendState) {
	t.Helper()
	s, _, _ := releaseNoticeDesktopFixture(t)
	s.tab = 0
	task := &s.taskUnit.state.tasks[0]
	task.requirementURL = "https://example.invalid/requirement"
	for i := range task.workspaces {
		task.workspaces[i].Branch = "feature/task"
	}
	cfg, _ := s.readSnapshotAppConfig()
	rows, e := releasenotice.BranchRows(taskBranchInfoManifest(*task).Services)
	if e != nil {
		t.Fatal(e)
	}
	identity := releasenotice.Identity{AppID: "app", OpenID: "ou_self", BotReady: true}
	session := &releasenotice.Session{Kind: releasenotice.Branches, Identity: identity, Title: "需求名称", URL: task.requirementURL, Rows: rows}
	session.BindOwner("task")
	d := &noticeSendState{kind: releasenotice.Branches, owner: s.taskFilesIdentity(), link: task.requirementURL, configID: application.BranchNoticeConfigIdentity(cfg), batchID: releasenotice.Digest(rows), session: session}
	s.releaseNotices.pending = d
	return s, d
}
func TestBranchNoticeOwnsTaskAndIgnoresRetiredGroupConfiguration(t *testing.T) {
	s, d := branchNoticeDesktopFixture(t)
	if !s.releaseNoticeCurrent(d) {
		t.Fatal("current notification disabled")
	}
	action := s.releaseNoticeAction(d, d.actionKind("send"))
	if action.Kind != "task.branch-notice.send" || action.TaskID != d.owner.id || action.Payload["taskFilesContext"] == nil {
		t.Fatal(action)
	}
	cfg, _ := s.readSnapshotAppConfig()
	notice := cfg.EffectiveLarkNotice()
	notice.BranchChatID = "retired-value"
	cfg.LarkNotice = &notice
	b, _ := json.Marshal(cfg)
	s.snapshot.ConfigJSON = string(b)
	if !s.releaseNoticeCurrent(d) {
		t.Fatal("retired group affects current self notification")
	}
	s.taskUnit.state.tasks[0].workspaces[0].Branch = "feature/changed"
	if s.releaseNoticeCurrent(d) {
		t.Fatal("changed branches retained send scope")
	}
}
func TestBranchNoticeButtonRightOfCopyAndLeavesInfoOpen(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(map[bool]string{false: "light", true: "dark"}[dark], func(t *testing.T) {
			s, _ := branchNoticeDesktopFixture(t)
			s.dismissReleaseNotice()
			s.openTaskBranchInfo(s.taskUnit.currentTask())
			v := ui.NewTester(func(c *ui.Context) { s.renderTaskBranchInfoDialog(c, setTheme(c, dark)) }, 1000, 760)
			copy, ok := v.Find("复制服务与分支")
			send, found := v.Find("发送分支通知给自己")
			if !ok || !found || send.X < copy.X+copy.W {
				t.Fatal("self send icon not beside copy", copy, send)
			}
			s.sendBranchNotice(s.taskUnit.currentTask())
			if s.taskUnit.state.taskBranchInfo == nil || !s.taskUnit.state.taskBranchInfo.Open {
				t.Fatal("direct send closes branch info")
			}
			if s.releaseNotices.pending == nil {
				t.Fatal("no direct-send state created")
			}
		})
	}
}
func TestBranchNoticePreparationBlocksRepeatedEntry(t *testing.T) {
	s, d := branchNoticeDesktopFixture(t)
	d.loading = true
	s.sendBranchNotice(s.taskUnit.currentTask())
	if s.releaseNotices.pending != d {
		t.Fatal("second click replaces active preparation")
	}
}
