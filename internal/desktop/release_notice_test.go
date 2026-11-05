package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/releasenotice"
)

func releaseNoticeDesktopFixture(t *testing.T) (*app, *noticeSendState, tagHistoryGroup) {
	t.Helper()
	s := taskServicesDesktopFixture(t)
	s.tab = taskTagTab
	s.settingsUnit.state.configJSON = s.snapshot.ConfigJSON
	task := s.taskUnit.currentTask()
	tag := "1.beta-2"
	op := domain.TagOperation{OperationID: "fixture-operation", GroupServiceID: "s1", ServiceName: "A service", Tag: &tag, State: domain.TagOperationSuccess, GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuSuccess}}
	g := tagHistoryGroup{Item: domain.TagHistoryItem{GroupID: op.OperationID, Operations: []domain.TagOperation{op}}}
	s.snapshot.TagHistory = []application.TagRecord{{ID: op.OperationID, TaskID: task.id, ServiceID: "s1", ServiceName: op.ServiceName, Tag: tag, State: string(op.State), GenbuStatus: op.GenbuStatus}}
	s.ensureTaskTagHistoryView(task)
	cfg, _ := s.readSnapshotAppConfig()
	identity := releasenotice.Identity{AppID: "test-app", Name: "无需展示的姓名", OpenID: "ou_self", BotReady: true, UserState: "expired"}
	session := &releasenotice.Session{Title: "合成需求", Identity: identity, Rows: []releasenotice.Row{{Service: "A service", Version: tag}}}
	session.BindOwner("test-task")
	d := &noticeSendState{owner: s.taskFilesIdentity(), link: task.requirementURL, groupID: g.Item.GroupID, configID: application.ReleaseNoticeConfigIdentity(cfg), batchID: releaseNoticeBatchID(g), session: session}
	s.releaseNotices.pending = d
	return s, d, g
}
func TestReleaseNoticeButtonRequiresWholeBatchSuccess(t *testing.T) {
	s, _, g := releaseNoticeDesktopFixture(t)
	if !s.releaseNoticeReady(g) {
		t.Fatal("successful batch disabled")
	}
	bad := g.Item.Operations[0]
	bad.OperationID = "other"
	bad.GenbuStatus.Build = domain.GenbuUnknown
	g.Item.Operations = append(g.Item.Operations, bad)
	if s.releaseNoticeReady(g) {
		t.Fatal("successful subset enabled")
	}
}
func TestReleaseNoticeTaskConfigAndProgressIsolation(t *testing.T) {
	s, d, g := releaseNoticeDesktopFixture(t)
	if !s.releaseNoticeCurrent(d) || s.noticeButtonText("", g.Item.GroupID) != "发送给自己" {
		t.Fatal("current notice rejected")
	}
	d.loading = true
	if !s.releaseNotices.running() || s.noticeButtonText("", g.Item.GroupID) != "准备中…" {
		t.Fatal("preparation is not busy")
	}
	previous := s.releaseNotices.pending
	s.sendTagNotice(s.releaseUnit.state.tagHistory, g)
	if s.releaseNotices.pending != previous {
		t.Fatal("double click replaced active send")
	}
	d.loading = false
	d.sending = true
	if !s.documentCloseOperationBusy() || s.noticeButtonText("", g.Item.GroupID) != "发送中…" {
		t.Fatal("sending is not busy")
	}
	d.sending = false
	d.session.Receipts = []releasenotice.Receipt{{OpenID: "ou_self", Key: "original", State: "sent", MessageID: "om_ok", StartedAt: time.Now()}}
	if s.noticeButtonText("", g.Item.GroupID) != "再次发送给自己" {
		t.Fatal("Tag resend unavailable")
	}
	s.taskUnit.state.tasks[0].createdAt = "recreated"
	if s.releaseNoticeCurrent(d) {
		t.Fatal("task recreation retained pending request")
	}
	s.observeReleaseNotice()
	if s.releaseNotices.pending != nil {
		t.Fatal("stale pending request retained")
	}
}
func TestReleaseNoticeConfigChangeInvalidatesPendingSend(t *testing.T) {
	s, d, _ := releaseNoticeDesktopFixture(t)
	cfg, _ := s.readSnapshotAppConfig()
	path := "changed-lark.cmd"
	cfg.LarkExecutablePath = &path
	b, _ := json.Marshal(cfg)
	s.snapshot.ConfigJSON = string(b)
	if s.releaseNoticeCurrent(d) {
		t.Fatal("changed CLI configuration retained pending send")
	}
}
func TestReleaseNoticeExecutesPrepareThenDirectSelfSend(t *testing.T) {
	for _, kind := range []string{"", releasenotice.Branches} {
		t.Run(kind, func(t *testing.T) {
			_, d, _ := releaseNoticeDesktopFixture(t)
			session := *d.session
			session.Kind = kind
			session.Identity.Name = ""
			prepare := application.Action{Kind: "prepare"}
			send := application.Action{Kind: "send", Payload: map[string]any{"scope": "test"}}
			calls, prepared := 0, false
			dispatch := func(_ context.Context, a application.Action) (application.ActionResult, error) {
				calls++
				if calls == 1 {
					if a.Kind != "prepare" {
						t.Fatal(a)
					}
					return application.ActionResult{Data: session}, nil
				}
				if a.Kind != "send" || !prepared || a.Payload["confirmed"] != true || a.Payload["sessionId"] != session.ID {
					t.Fatal(a, prepared)
				}
				choice, ok := a.Payload["selection"].(releasenotice.Selection)
				if !ok || !choice.Retry || choice.Resend {
					t.Fatal(choice)
				}
				for _, field := range []string{"userId", "chatId", "openId", "receiver", "recipients"} {
					if a.Payload[field] != nil {
						t.Fatal("UI supplies receiver", field)
					}
				}
				session.Receipts = []releasenotice.Receipt{{OpenID: session.Identity.OpenID, Key: "key", State: "sent", MessageID: "om_self"}}
				return application.ActionResult{Data: session}, nil
			}
			out, e := executeNoticeSend(context.Background(), prepare, send, dispatch, func(releasenotice.Session) error { prepared = true; return nil })
			if e != nil || calls != 2 {
				t.Fatal(out, e, calls)
			}
			if len(send.Payload) != 1 {
				t.Fatal("request template mutated")
			}
			s := &app{}
			s.finishNoticeSend(out, e)
			if s.lastMessage != "已发送给自己" || s.lastError != "" {
				t.Fatal(s.lastMessage, s.lastError)
			}
		})
	}
}
func TestReleaseNoticeDirectSendStopsForMissingIdentityAndStaleContext(t *testing.T) {
	for _, mode := range []string{"missing-id", "missing-bot", "stale-context", "cancelled", "prepare-error"} {
		t.Run(mode, func(t *testing.T) {
			_, d, _ := releaseNoticeDesktopFixture(t)
			session := *d.session
			calls := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "missing-id" {
				session.Identity.OpenID = ""
			}
			if mode == "missing-bot" {
				session.Identity.BotReady = false
			}
			dispatch := func(context.Context, application.Action) (application.ActionResult, error) {
				calls++
				if mode == "prepare-error" {
					return application.ActionResult{}, errors.New("metadata failure")
				}
				if mode == "cancelled" {
					cancel()
				}
				return application.ActionResult{Data: session}, nil
			}
			_, e := executeNoticeSend(ctx, application.Action{}, application.Action{}, dispatch, func(releasenotice.Session) error {
				if mode == "stale-context" {
					return errors.New("task switched")
				}
				return nil
			})
			if e == nil || calls != 1 {
				t.Fatal("send followed invalid preparation", e, calls)
			}
		})
	}
}
func TestReleaseNoticeDirectResendAndUnknownFeedback(t *testing.T) {
	for _, kind := range []string{"", releasenotice.Branches} {
		t.Run(kind, func(t *testing.T) {
			_, d, _ := releaseNoticeDesktopFixture(t)
			session := *d.session
			session.Kind = kind
			session.Receipts = []releasenotice.Receipt{{OpenID: "ou_self", Key: "previous", State: "sent", MessageID: "om_previous"}}
			calls := 0
			dispatch := func(_ context.Context, a application.Action) (application.ActionResult, error) {
				calls++
				if calls == 1 {
					return application.ActionResult{Data: session}, nil
				}
				choice := a.Payload["selection"].(releasenotice.Selection)
				if !choice.Resend || choice.ResendAfter != "previous" {
					t.Fatal("new click did not request guarded resend", choice)
				}
				session.Receipts = append(session.Receipts, releasenotice.Receipt{OpenID: "ou_self", Key: "next", State: "unknown", Error: "发送结果待核实"})
				return application.ActionResult{Data: session}, nil
			}
			out, e := executeNoticeSend(context.Background(), application.Action{}, application.Action{}, dispatch, nil)
			if e != nil || calls != 2 {
				t.Fatal(e, calls)
			}
			s := &app{}
			s.finishNoticeSend(out, e)
			if s.lastMessage != "" || s.lastError != "发送结果待核实" {
				t.Fatal("unknown reported as success", s.lastError)
			}
		})
	}
}
func TestLarkSettingsHaveNoGroupOrContactsControls(t *testing.T) {
	s := newSettingsCLITabsTestApp(t)
	v := ui.NewTester(func(c *ui.Context) {
		s.renderSettingsCLI(c, setTheme(c, false), "lark", "Lark CLI", "larkExecutablePath")
	}, 1000, 760)
	for _, text := range []string{"通知配置", "测试联系人", "上线分支通知群", "添加联系人", "保存通知配置"} {
		if v.HasText(text) {
			t.Fatal("retired settings remain", text)
		}
	}
}

func TestLarkProfileChangeRejectsLateCLIStatus(t *testing.T) {
	s := newSettingsCLITabsTestApp(t)
	request, e := s.beginCLIJob(application.Action{Kind: "cli.status", Payload: map[string]any{"tool": "lark"}})
	if e != nil || request == nil {
		t.Fatal(e)
	}
	defer request.complete(nil)
	if !s.cliJobRelevant(request) {
		t.Fatal("fresh Lark status rejected")
	}
	cfg := domain.DefaultAppConfig()
	notice := cfg.EffectiveLarkNotice()
	notice.Profile = "another-profile"
	cfg.LarkNotice = &notice
	b, _ := json.Marshal(cfg)
	s.settingsUnit.state.configJSON = string(b)
	if s.cliJobRelevant(request) {
		t.Fatal("old profile response accepted")
	}
	request.cancel()
	request.complete(nil)
	if e = s.closeCLIJobs(context.Background()); e != nil {
		t.Fatal(e)
	}
}
