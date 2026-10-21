package desktop

import (
	"context"
	"errors"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"path/filepath"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"strings"
	"testing"
)

type headerSyncFake struct {
	calls       []application.Action
	failComment bool
}

func (b *headerSyncFake) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, errors.New("no snapshot expected")
}
func (b *headerSyncFake) Dispatch(_ context.Context, a application.Action) (application.ActionResult, error) {
	b.calls = append(b.calls, a)
	switch a.Kind {
	case "materials.sync":
		return application.ActionResult{Data: application.MaterialsSyncExecution{Result: domain.MaterialsSyncResult{Progress: domain.MaterialsSyncProgress{Phase: domain.MaterialsSyncComplete, CompletedFiles: 3, TotalFiles: 3}}, RequirementDirectoryURL: "https://example.feishu.cn/wiki/ActualTaskRoot123"}}, nil
	case "materials.comment":
		if b.failComment {
			return application.ActionResult{}, errors.New("fake comment permission failure")
		}
		return application.ActionResult{Data: domain.MaterialsCommentResult{Status: domain.MaterialsCommentCreated}}, nil
	default:
		return application.ActionResult{}, errors.New("unexpected fake action: " + a.Kind)
	}
}
func headerSyncFixture(t *testing.T) *app {
	t.Helper()
	root := t.TempDir()
	return &app{page: "tasks", tab: 1, documentsLoaded: true, configJSON: `{"requirementMaterialsFeishuTarget":"https://example.feishu.cn/wiki/ConfiguredTarget123"}`, tasks: []task{{id: "sync-task", title: "fixture", requirementID: "1001", requirementURL: "https://project.feishu.cn/fixture/userstory/detail/1001", path: root, materialsPath: filepath.Join(root, "materials"), materialsStatus: "READY"}}}
}
func TestMaterialsHeaderSyncIsWholeTaskWithSelection(t *testing.T) {
	s := headerSyncFixture(t)
	task := s.currentTask()
	path := filepath.Join(task.materialsPath, "one.md")
	s.documents = []application.Document{{Path: path, Name: "one.md", Kind: "markdown"}, {Path: filepath.Join(task.materialsPath, "two.md"), Name: "two.md", Kind: "markdown"}}
	s.documentPath = path
	state := s.ensureMaterialsFileManagementState()
	entry := materialsFileSelectionEntry{Key: path, Path: path, Relative: "one.md"}
	state.selected = map[string]materialsFileSelectionEntry{path: entry}
	tt := ui.NewTester(func(c *ui.Context) { s.documentTree(c, setTheme(c, false), 300) }, 320, 800)
	if err := tt.Click("更多资料操作"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("已选 1") || !tt.HasText("同步全部资料到飞书") || !tt.HasText("打开飞书资料根目录") {
		t.Fatalf("missing header action %q", tt.Texts())
	}
	// No backend/window means disabled; opening and clicking it must perform no work.
	tt.Click("同步全部资料到飞书")
	if s.materialsFeishuSyncActive() {
		t.Fatal("disabled action started")
	}
	payload, err := s.materialsFeishuSyncStartPayload(task)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["selectedPaths"]; ok {
		t.Fatal("header inherited selected file scope")
	}
	if payload["writeRoot"] != task.materialsPath {
		t.Fatal("whole-task root changed")
	}
	jobs := s.ensureMaterialsFeishuSyncJobs()
	jobs.active = &materialsFeishuSyncSession{TaskKey: materialsFeishuSyncTaskKey(task), Stage: materialsFeishuSyncUploading}
	tt.Frame()
	if !tt.HasText("查看同步进度") || tt.HasText("同步全部资料到飞书") {
		t.Fatal("active sync allowed duplicate start")
	}
	s.tasks = append(s.tasks, task)
	s.tasks[1].id = "other-task"
	s.selected = 1
	s.beginOverflowMenuFrame()
	tt.Frame()
	s.endOverflowMenuFrame()
	tt.Frame()
	if tt.HasText("查看同步进度") {
		t.Fatal("old task popup survived owner change")
	}
}
func TestMaterialsHeaderSyncResultStagesWithFake(t *testing.T) {
	s := headerSyncFixture(t)
	task := s.currentTask()
	session := &materialsFeishuSyncSession{RunID: "fixture-run", TaskID: task.id, TaskKey: materialsFeishuSyncTaskKey(task), TaskPath: task.path, TaskTitle: task.title, TaskRequirementID: task.requirementID, MaterialsRoot: task.materialsPath, RequirementURL: task.requirementURL, Home: s.snapshot.Home, CommentAvailable: true, Request: domain.MaterialsSyncRequest{TargetURL: "https://example.feishu.cn/wiki/ConfiguredTarget123", WriteRoot: task.materialsPath, RequirementID: task.requirementID, RequirementTitle: task.title}, Plan: &domain.MaterialsSyncPlan{ID: "fixture-plan"}}
	jobs := s.ensureMaterialsFeishuSyncJobs()
	jobs.current = session
	jobs.active = session
	fake := &headerSyncFake{failComment: true}
	drive := func(kind materialsFeishuSyncJobKind) {
		job := &materialsFeishuSyncJob{session: session, kind: kind, window: &mygo.Window{}, ctx: context.Background()}
		jobs.operation = job
		action := materialsFeishuSyncAction(session, kind)
		result, err := fake.Dispatch(context.Background(), action)
		s.finishMaterialsFeishuSyncJob(job, result, err)
	}
	upload := materialsFeishuSyncAction(session, materialsFeishuSyncUploadJob)
	if _, ok := upload.Payload["selectedPaths"]; ok {
		t.Fatal("full sync scoped to selection")
	}
	confirm := materialsFeishuSyncAction(session, materialsFeishuSyncConfirmJob)
	if len(confirm.Payload["deletionPaths"].([]string)) != 0 {
		t.Fatal("new deletion intent")
	}
	drive(materialsFeishuSyncUploadJob)
	if session.Stage != materialsFeishuSyncPublishing || session.NextStep != materialsFeishuSyncCommentJob || session.DirectoryURL != "https://example.feishu.cn/wiki/ActualTaskRoot123" {
		t.Fatalf("upload next stage %#v", session)
	}
	drive(materialsFeishuSyncCommentJob)
	if session.CommentResult == nil || session.CommentResult.Status != domain.MaterialsCommentFailed || !strings.Contains(session.Summary, "资料已上传，但需求评论未完成") {
		t.Fatalf("comment failure was hidden %#v", session)
	}
	fake.failComment = false
	// Retry invokes only the captured comment action; it retains the successful upload/root.
	drive(materialsFeishuSyncCommentJob)
	if len(fake.calls) != 3 || fake.calls[0].Kind != "materials.sync" || fake.calls[1].Kind != "materials.comment" || fake.calls[2].Kind != "materials.comment" {
		t.Fatal("retry uploaded again")
	}
	if fake.calls[2].Payload["directoryUrl"] != session.DirectoryURL || session.CommentResult.Status != domain.MaterialsCommentCreated {
		t.Fatal("retry lost actual root")
	}
	for _, invalid := range []string{"partial", "unlinked", "stale-account"} {
		session.CommentResult = nil
		session.CommentAvailable = invalid != "unlinked"
		session.Stage = materialsFeishuSyncUploading
		session.NextStep = ""
		job := &materialsFeishuSyncJob{session: session, kind: materialsFeishuSyncUploadJob, window: &mygo.Window{}, ctx: context.Background()}
		jobs.operation = job
		result := application.MaterialsSyncExecution{Result: domain.MaterialsSyncResult{Progress: domain.MaterialsSyncProgress{Phase: domain.MaterialsSyncComplete}}, RequirementDirectoryURL: session.DirectoryURL}
		if invalid == "partial" {
			result.Result.Progress.Failed = 1
		}
		if invalid == "stale-account" {
			s.snapshot.Home = "other-account"
		}
		s.finishMaterialsFeishuSyncJob(job, application.ActionResult{Data: result}, nil)
		if session.NextStep == materialsFeishuSyncCommentJob {
			t.Fatalf("%s scheduled a comment", invalid)
		}
		if invalid != "stale-account" && (session.CommentResult == nil || session.CommentResult.Status != domain.MaterialsCommentSkipped) {
			t.Fatalf("%s did not explain skipped comment", invalid)
		}
	}
}
