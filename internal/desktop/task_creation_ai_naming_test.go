package desktop

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

const fixtureNamingAccount = "confirmed-test-account"

type taskCreationNamingCacheFixture struct {
	cache          map[string]domain.RequirementAiNamingSuggestion
	cacheReads     []string
	dispatches     atomic.Int32
	identityProbes atomic.Int32
	started        chan application.Action
}

func (*taskCreationNamingCacheFixture) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, errors.New("unexpected snapshot")
}

func (b *taskCreationNamingCacheFixture) RequirementIdentity(context.Context) (application.RequirementIdentitySnapshot, error) {
	b.identityProbes.Add(1)
	return application.RequirementIdentitySnapshot{}, errors.New("unexpected identity probe")
}

func (b *taskCreationNamingCacheFixture) CachedRequirementAINaming(_ context.Context, link, account string) (domain.RequirementAiNamingSuggestion, bool, error) {
	b.cacheReads = append(b.cacheReads, link)
	if account != fixtureNamingAccount {
		return domain.RequirementAiNamingSuggestion{}, false, errors.New("unexpected cache identity")
	}
	suggestion, found := b.cache[link]
	return suggestion, found, nil
}

func (b *taskCreationNamingCacheFixture) Dispatch(ctx context.Context, action application.Action) (application.ActionResult, error) {
	b.dispatches.Add(1)
	if b.started != nil {
		b.started <- action
	}
	<-ctx.Done()
	return application.ActionResult{}, ctx.Err()
}

func setupTaskCreationNamingCacheFixture(s *app, backend *taskCreationNamingCacheFixture) {
	s.backend = backend
	s.requirementUnit.state.requirementIdentityJobs = &requirementIdentityJobState{
		hasContext: true, context: s.currentRequirementIdentityContext(),
		confirmed: true, state: application.RequirementIdentityAuthenticated, identity: fixtureNamingAccount,
	}
	form := s.taskUnit.state.taskCreation
	form.config.AIRequirementNamingEnabled = true
	form.config.DefaultBranchPrefix = "feature/{num}_{ai}"
}

func TestTaskCreationOpensWithLongCachedNamingInFirstFrame(t *testing.T) {
	profile := t.TempDir()
	t.Setenv("USERPROFILE", profile)
	t.Setenv("HOME", profile)
	const (
		link   = "https://project.feishu.cn/obt/userstory/detail/1000000004"
		folder = "公交支付渠道批量对账流程优化"
		branch = "feature/1000000004_batch_reconciliation"
	)
	backend := &taskCreationNamingCacheFixture{cache: map[string]domain.RequirementAiNamingSuggestion{
		link: {FolderName: folder, BranchSuffix: "batch_reconciliation"},
	}}
	s := newApp(backend, Options{Page: "requirements"})
	s.snapshot.Home = t.TempDir()
	s.snapshot.ConfigJSON = `{"aiRequirementNamingEnabled":true,"defaultBranchPrefix":"feature/{num}_{ai}"}`
	s.win = &mygo.Window{}
	s.requirementUnit.state.requirementIdentityJobs = &requirementIdentityJobState{
		hasContext: true, context: s.currentRequirementIdentityContext(),
		confirmed: true, state: application.RequirementIdentityAuthenticated, identity: fixtureNamingAccount,
	}
	// Hold the backend to prove the first frame can use the local naming cache.
	if err := s.acquireBackend(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.releaseBackend)
	t.Cleanup(func() {
		s.cancelTaskCreationReads(s.taskUnit.state.taskCreation, false)
		s.closeTaskCreationAINaming()
	})

	s.openTaskCreateForm("基于需求创建任务", "项目需求原始标题", link)
	form := s.taskUnit.state.taskCreation
	if form == nil {
		t.Fatal("task creation form did not open")
	}
	if form.readRequest != nil || s.busy {
		t.Fatal("task creation submitted an unexpected read before rendering")
	}
	if form.draft.TaskName != folder || form.draft.Branch != branch || form.aiPending || form.aiError != "" {
		t.Fatalf("opening did not apply the cached suggestion synchronously: name=%q branch=%q pending=%t error=%q", form.draft.TaskName, form.draft.Branch, form.aiPending, form.aiError)
	}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { s.renderTaskCreationDialog(c, setTheme(c, false)) })
	}, 960, 800)
	nameInput, nameFound := tester.Find("任务名称")
	branchInput, branchFound := tester.Find("任务分支输入")
	if !nameFound || nameInput.W <= 0 || !branchFound || branchInput.W <= 0 ||
		form.draft.TaskName != folder || form.draft.Branch != branch || tester.HasText("正在获取命名建议") {
		t.Fatalf("first frame did not show the cached fields: name=%q branch=%q text=%v", form.draft.TaskName, form.draft.Branch, tester.Texts())
	}
	if len(backend.cacheReads) != 1 || backend.cacheReads[0] != link || backend.dispatches.Load() != 0 || backend.identityProbes.Load() != 0 {
		t.Fatalf("opening did not use only the local naming cache: reads=%v dispatches=%d probes=%d", backend.cacheReads, backend.dispatches.Load(), backend.identityProbes.Load())
	}
}

func TestTaskCreationCandidateUsesNamingCacheInSelection(t *testing.T) {
	profile := t.TempDir()
	t.Setenv("USERPROFILE", profile)
	t.Setenv("HOME", profile)
	s, tester := taskCreationCompactFixture(t, 960, false, 1)
	form := s.taskUnit.state.taskCreation
	first, second := form.requirements.candidates[0], form.requirements.candidates[1]
	backend := &taskCreationNamingCacheFixture{cache: map[string]domain.RequirementAiNamingSuggestion{
		first.URL:  {FolderName: "支付优化", BranchSuffix: "payment"},
		second.URL: {FolderName: "扩容适配", BranchSuffix: "capacity"},
	}}
	setupTaskCreationNamingCacheFixture(s, backend)
	s.selectTaskCreationRequirementCandidate(first)
	if form.aiPending || form.aiError != "" || form.draft.MetadataLoading || form.draft.TaskName != "支付优化" || form.draft.Branch != "feature/1000000005_payment" {
		t.Fatalf("cache hit was not applied during candidate selection: draft=%+v pending=%t error=%q", form.draft, form.aiPending, form.aiError)
	}
	if module := form.moduleData["service"]["api"]; module.TargetBranch != form.draft.Branch {
		t.Fatalf("untouched target does not match cached naming: %+v", module)
	}
	s.setTaskCreationServiceSelected("service", true)
	compactCreationEdit(t, tester, "任务名称", "手工任务名")
	compactCreationEdit(t, tester, "任务分支输入", "feature/manual")
	s.selectTaskCreationRequirementCandidate(second)
	tester.Frame()
	if form.draft.TaskName != "手工任务名" || form.draft.Branch != "feature/manual" || tester.HasText("正在获取命名建议…") {
		t.Fatalf("cache hit overwrote manual fields or showed a pending request: %+v", form.draft)
	}
	if len(backend.cacheReads) != 2 || backend.dispatches.Load() != 0 || backend.identityProbes.Load() != 0 {
		t.Fatalf("cache hit used remote request path: reads=%v dispatches=%d probes=%d", backend.cacheReads, backend.dispatches.Load(), backend.identityProbes.Load())
	}
}

func TestTaskCreationNamingCacheRequiresCurrentConfirmedIdentity(t *testing.T) {
	for _, scope := range []string{"unconfirmed", "changed profile"} {
		t.Run(scope, func(t *testing.T) {
			profile := t.TempDir()
			t.Setenv("USERPROFILE", profile)
			t.Setenv("HOME", profile)
			s, _ := taskCreationCompactFixture(t, 960, false, 1)
			form := s.taskUnit.state.taskCreation
			candidate := form.requirements.candidates[0]
			backend := &taskCreationNamingCacheFixture{cache: map[string]domain.RequirementAiNamingSuggestion{
				candidate.URL: {FolderName: "支付优化", BranchSuffix: "payment"},
			}}
			setupTaskCreationNamingCacheFixture(s, backend)
			identity := s.requirementUnit.state.requirementIdentityJobs
			if scope == "unconfirmed" {
				identity.confirmed = false
			} else {
				identity.context.profileModified--
			}
			s.selectTaskCreationRequirementCandidate(candidate)
			if len(backend.cacheReads) != 0 || form.draft.TaskName != "" || form.draft.Branch != "" {
				t.Fatalf("unconfirmed or stale profile authorized naming: %+v %v", form.draft, backend.cacheReads)
			}
		})
	}
}

func TestTaskCreationNamingRejectsRetiredResultsAfterReselectingColdCandidate(t *testing.T) {
	profile := t.TempDir()
	t.Setenv("USERPROFILE", profile)
	t.Setenv("HOME", profile)
	s, _ := taskCreationCompactFixture(t, 960, false, 1)
	form := s.taskUnit.state.taskCreation
	first, second := form.requirements.candidates[0], form.requirements.candidates[1]
	backend := &taskCreationNamingCacheFixture{started: make(chan application.Action, 3)}
	setupTaskCreationNamingCacheFixture(s, backend)
	s.win = &mygo.Window{}
	selectPending := func(candidate taskCreationRequirementCandidate) *taskCreationAINamingRequest {
		t.Helper()
		s.selectTaskCreationRequirementCandidate(candidate)
		select {
		case action := <-backend.started:
			if action.Kind != "requirements.ai-name" || action.Payload["expectedIdentity"] != fixtureNamingAccount || action.Payload["requirementLink"] != candidate.URL {
				t.Fatalf("unexpected candidate request: %+v", action)
			}
		case <-time.After(time.Second):
			t.Fatal("missing candidate naming request")
		}
		if !form.aiPending || form.draft.TaskName != "" || form.draft.Branch != "" || form.draft.AIBranchSuffix != nil || form.moduleData["service"]["api"].TargetBranch != "" {
			t.Fatalf("cache miss retained stale naming or exposed a branch placeholder: %+v", form.draft)
		}
		return form.aiNaming.request
	}
	oldFirst := selectPending(first)
	s.beginTaskCreationPreflight()
	if !form.aiPending || form.aiNaming.request != oldFirst || form.preview != nil {
		t.Fatal("pressing create with an unfinished automatic name must leave the request pending")
	}
	oldSecond := selectPending(second)
	current := selectPending(first)
	defer current.cancel()
	if oldFirst.ctx.Err() == nil || oldSecond.ctx.Err() == nil || current == oldFirst {
		t.Fatal("candidate switch did not retire prior subscribers")
	}
	result := application.ActionResult{Data: domain.RequirementAiNamingSuggestion{FolderName: "旧命名", BranchSuffix: "obsolete"}}
	s.finishTaskCreationAINaming(oldFirst, result, nil)
	s.finishTaskCreationAINaming(oldSecond, result, nil)
	if !form.aiPending || form.aiNaming.request != current || form.draft.TaskName != "" || form.draft.Branch != "" {
		t.Fatalf("retired request replaced current form: %+v", form.draft)
	}
	s.finishTaskCreationAINaming(current, application.ActionResult{}, errors.New("fixture generation failed"))
	if form.aiPending || !strings.Contains(form.aiError, "可继续手工填写") {
		t.Fatalf("failed generation did not return form to editable state: %q", form.aiError)
	}
	form.draft.TaskName, form.draft.Branch = "手工补填", "feature/manual"
	s.syncTaskCreationDraft()
	if !form.draft.NameEdited || !form.draft.BranchEdited || backend.identityProbes.Load() != 0 {
		t.Fatal("manual edit after a failed shared generation did not persist or repeated identity probes")
	}
}
