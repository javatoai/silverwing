package desktop

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

const taskCreationOpaqueLink = "https://project.feishu.cn/obt/6785bc520a0621258bcf0583/detail/7143084336"

type taskCreationLinkBackend struct {
	account string
	started chan application.Action
}

func (*taskCreationLinkBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, errors.New("unexpected snapshot")
}

func (b *taskCreationLinkBackend) RequirementIdentity(context.Context) (application.RequirementIdentitySnapshot, error) {
	return application.RequirementIdentitySnapshot{State: application.RequirementIdentityAuthenticated, Identity: b.account}, nil
}

func (b *taskCreationLinkBackend) Dispatch(ctx context.Context, action application.Action) (application.ActionResult, error) {
	select {
	case b.started <- action:
	case <-ctx.Done():
	}
	<-ctx.Done()
	return application.ActionResult{}, ctx.Err()
}

func taskCreationLinkFixture(t *testing.T) (*app, *ui.Tester, *taskCreationLinkBackend) {
	t.Helper()
	s, tester := taskCreationCompactFixture(t, 1100, false, 1)
	s.initTaskCreation("新建任务", "", "")
	form := s.taskUnit.state.taskCreation
	form.config.MeegleProjects = []domain.MeegleProjectConfig{{SimpleName: "obt", ProjectKey: "fixture-project"}}
	backend := &taskCreationLinkBackend{account: "account-a", started: make(chan application.Action, 8)}
	s.backend, s.win = backend, &mygo.Window{}
	s.requirementUnit.state.requirementIdentityJobs = &requirementIdentityJobState{
		confirmed: true, state: application.RequirementIdentityAuthenticated, identity: backend.account,
	}
	t.Cleanup(s.closeTaskCreationRequirements)
	tester.Frame()
	form.requirements.candidatesLoaded = true
	form.requirements.candidates = []taskCreationRequirementCandidate{
		{Title: "其他需求", URL: "https://project.feishu.cn/obt/userstory/detail/1000000005"},
	}
	return s, tester, backend
}

func waitTaskCreationLinkRead(t *testing.T, backend *taskCreationLinkBackend, link string, force bool) {
	t.Helper()
	select {
	case action := <-backend.started:
		if action.Kind != "requirements.read" || action.Path != link || action.TaskID != "" ||
			action.Payload["url"] != link || action.Payload["expectedIdentity"] != backend.account || action.Payload["force"] != force {
			t.Fatalf("wrong linked requirement request: %+v", action)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("did not read linked requirement directly")
	}
}

func TestTaskCreationDirectRequirementLinkRead(t *testing.T) {
	s, tester, backend := taskCreationLinkFixture(t)
	form := s.taskUnit.state.taskCreation
	compactCreationEdit(t, tester, "任务名称", "手填任务名")
	compactCreationEdit(t, tester, "任务分支输入", "feature/manual")
	compactCreationEdit(t, tester, "需求链接或搜索", taskCreationOpaqueLink)
	if !tester.HasText("正在读取需求…") || tester.HasText("没有匹配的需求，可直接填写完整链接。") || form.requirements.candidatesOpen {
		t.Fatalf("pasted link kept the candidate popup or omitted loading status: %v", tester.Texts())
	}
	waitTaskCreationLinkRead(t, backend, taskCreationOpaqueLink, false)

	for _, reason := range []string{"没有权限读取工作项", "工作项不存在", "网络请求超时"} {
		request := form.requirements.metadataRequest
		s.finishTaskCreationRequirementRead(request, application.ActionResult{}, errors.New(reason))
		request.cancel()
		tester.Frame()
		if !tester.HasText("读取需求失败："+reason) || form.draft.MetadataLoading {
			t.Fatalf("failure omitted original reason %q: %v", reason, tester.Texts())
		}
		if err := tester.Click("重试读取需求"); err != nil {
			t.Fatal(err)
		}
		waitTaskCreationLinkRead(t, backend, taskCreationOpaqueLink, true)
	}

	const title = "不在我的需求列表里的工作项"
	request := form.requirements.metadataRequest
	result := application.ActionResult{Data: application.RequirementContent{ID: "7143084336", Title: title, URL: taskCreationOpaqueLink}}
	s.finishTaskCreationRequirementRead(request, result, nil)
	request.cancel()
	tester.Frame()
	payload, err := s.taskCreationPayload()
	if err != nil || payload["requirementLink"] != taskCreationOpaqueLink || payload["folderName"] != "手填任务名" || payload["featureBranch"] != "feature/manual" {
		t.Fatalf("read changed the creation draft: %+v, %v", payload, err)
	}
	if !tester.HasText(title) || form.draft.MetadataLoading || form.requirements.linkReadError != "" {
		t.Fatalf("empty body must retain the readable title: %v", tester.Texts())
	}

	if err := tester.Click("重新读取需求"); err != nil {
		t.Fatal(err)
	}
	waitTaskCreationLinkRead(t, backend, taskCreationOpaqueLink, true)
	request = form.requirements.metadataRequest
	result.Data = application.RequirementContent{Title: title, URL: taskCreationOpaqueLink, Stale: true, RefreshError: "连接中断"}
	s.finishTaskCreationRequirementRead(request, result, nil)
	request.cancel()
	tester.Frame()
	if !tester.HasText(title) || !tester.HasText("读取需求失败：连接中断") {
		t.Fatalf("cached title and failed-refresh reason should both be visible: %v", tester.Texts())
	}
}

func TestTaskCreationDirectLinkStartsAINamingAfterRead(t *testing.T) {
	const suggestedName, suggestedBranch = "支付数据池", "feature/7143084336_payment_pool"
	for _, tc := range []struct {
		name            string
		enabled, manual bool
	}{
		{name: "fills both fields", enabled: true},
		{name: "preserves edits during naming", enabled: true, manual: true},
		{name: "obeys disabled naming setting"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, tester, backend := taskCreationLinkFixture(t)
			t.Cleanup(s.closeTaskCreationAINaming)
			form := s.taskUnit.state.taskCreation
			form.config.AIRequirementNamingEnabled = tc.enabled
			form.config.DefaultBranchPrefix = "feature/{num}_{ai}"
			tester.Frame()
			form.requirements.candidatesLoaded = true
			compactCreationEdit(t, tester, "需求链接或搜索", taskCreationOpaqueLink)
			waitTaskCreationLinkRead(t, backend, taskCreationOpaqueLink, false)
			const title = "支付宝使用单独的卡数据池"
			finishRead := func(content application.RequirementContent) {
				request := form.requirements.metadataRequest
				s.finishTaskCreationRequirementRead(request, application.ActionResult{Data: content}, nil)
				request.cancel()
				tester.Frame()
			}
			finishRead(application.RequirementContent{Title: title, Stale: true, RefreshError: "网络超时"})
			if form.aiEligible || form.aiPending {
				t.Fatal("stale data triggered naming")
			}
			if err := tester.Click("重试读取需求"); err != nil {
				t.Fatal(err)
			}
			waitTaskCreationLinkRead(t, backend, taskCreationOpaqueLink, true)
			finishRead(application.RequirementContent{ID: "7143084336", Title: title, URL: taskCreationOpaqueLink})
			if !tc.enabled {
				if form.aiPending || form.draft.TaskName != "" || form.draft.Branch != "" {
					t.Fatalf("read started naming despite disabled setting: %+v", form.draft)
				}
				return
			}
			if !form.aiPending || form.aiNaming == nil || form.aiNaming.request == nil {
				t.Fatal("the readable direct link did not start AI naming")
			}
			select {
			case action := <-backend.started:
				if action.Kind != "requirements.ai-name" || action.Payload["requirementLink"] != taskCreationOpaqueLink ||
					action.Payload["expectedIdentity"] != backend.account || action.Payload["force"] != false {
					t.Fatalf("unexpected naming request: %+v", action)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("direct link did not request a naming suggestion")
			}
			wantName, wantBranch := suggestedName, suggestedBranch
			if tc.manual {
				wantName, wantBranch = "手填任务名", "feature/manual"
				compactCreationEdit(t, tester, "任务名称", wantName)
				compactCreationEdit(t, tester, "任务分支输入", wantBranch)
			}
			request := form.aiNaming.request
			s.finishTaskCreationAINaming(request, application.ActionResult{Data: domain.RequirementAiNamingSuggestion{
				FolderName: suggestedName, BranchSuffix: "payment_pool",
			}}, nil)
			request.cancel()
			tester.Frame()
			if form.aiPending || form.draft.TaskName != wantName || form.draft.Branch != wantBranch {
				t.Fatalf("unexpected form after naming: %+v", form.draft)
			}
			if err := tester.Click("重新读取需求"); err != nil {
				t.Fatal(err)
			}
			waitTaskCreationLinkRead(t, backend, taskCreationOpaqueLink, true)
			finishRead(application.RequirementContent{Title: title})
			if form.aiPending || form.draft.TaskName != wantName || form.draft.Branch != wantBranch {
				t.Fatalf("refresh repeated naming or changed the form: %+v", form.draft)
			}
		})
	}
}

func TestTaskCreationLinkReadRetiresOldLinkAccountAndForm(t *testing.T) {
	s, tester, backend := taskCreationLinkFixture(t)
	form := s.taskUnit.state.taskCreation
	state := form.requirements
	compactCreationEdit(t, tester, "需求链接或搜索", taskCreationOpaqueLink)
	waitTaskCreationLinkRead(t, backend, taskCreationOpaqueLink, false)
	oldLink := state.metadataRequest

	const otherLink = "https://project.feishu.cn/obt/technical/detail/7143084337"
	compactCreationEdit(t, tester, "需求链接或搜索", otherLink)
	waitTaskCreationLinkRead(t, backend, otherLink, false)
	oldAccount := state.metadataRequest
	newBackend := &taskCreationLinkBackend{account: "account-b", started: backend.started}
	s.backend = newBackend
	s.requirementUnit.state.requirementIdentityJobs.identity = newBackend.account
	tester.Frame()
	waitTaskCreationLinkRead(t, newBackend, otherLink, false)
	current := state.metadataRequest
	late := application.ActionResult{Data: application.RequirementContent{Title: "已经失效的标题"}}
	for _, request := range []*taskCreationRequirementRequest{oldLink, oldAccount} {
		s.finishTaskCreationRequirementRead(request, late, nil)
		if request.ctx.Err() == nil || state.metadataRequest != current || form.draft.RequirementTitle != nil {
			t.Fatal("a retired request was accepted after changing link or account")
		}
	}
	s.discardTaskCreation(form)
	s.finishTaskCreationRequirementRead(current, late, nil)
	if current.ctx.Err() == nil || s.taskUnit.state.taskCreation != nil || form.draft.RequirementTitle != nil {
		t.Fatal("a retired request was accepted after closing the form")
	}
}
