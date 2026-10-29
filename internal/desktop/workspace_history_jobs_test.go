package desktop

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

type workspaceHistoryBlockingRead struct {
	action   application.Action
	ctx      context.Context
	reply    chan workspaceHistoryBlockingReply
	returned chan struct{}
}

type workspaceHistoryBlockingReply struct {
	result application.ActionResult
	err    error
}

type workspaceHistoryBlockingBackend struct {
	reads    chan *workspaceHistoryBlockingRead
	releases chan string
	stop     chan struct{}
}

func (*workspaceHistoryBlockingBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}

func (b *workspaceHistoryBlockingBackend) Dispatch(ctx context.Context, action application.Action) (application.ActionResult, error) {
	if action.Kind == workspace.CompareSessionCloseAction {
		b.releases <- stringPayloadValue(action.Payload["sessionId"])
		return application.ActionResult{}, nil
	}
	read := &workspaceHistoryBlockingRead{action: action, ctx: ctx, reply: make(chan workspaceHistoryBlockingReply, 1), returned: make(chan struct{})}
	defer close(read.returned)
	select {
	case b.reads <- read:
	case <-b.stop:
		return application.ActionResult{}, context.Canceled
	}
	// Deliberately finish only when released, including after cancellation. This
	// exposes stale callbacks and proves close waits for the backend to return.
	select {
	case reply := <-read.reply:
		return reply.result, reply.err
	case <-b.stop:
		return application.ActionResult{}, context.Canceled
	}
}

type workspaceHistoryPendingRead struct {
	read *workspaceHistoryBlockingRead
	job  *workspaceHistoryJob
}

func newWorkspaceHistoryJobsFixture(t *testing.T) (*app, *workspaceHistoryState, *workspaceHistoryBlockingBackend) {
	t.Helper()
	backend := &workspaceHistoryBlockingBackend{reads: make(chan *workspaceHistoryBlockingRead, 16), releases: make(chan string, 16), stop: make(chan struct{})}
	item := application.Workspace{ID: "workspace", ServiceID: "service", ModuleID: "module", Path: filepath.Join(t.TempDir(), "repo"), Branch: "feature/task"}
	taskItem := task{id: "task", path: filepath.Join(t.TempDir(), "task"), createdAt: "original", workspaces: []application.Workspace{item}}
	state := &workspaceHistoryState{
		TaskID: taskItem.id, TaskPath: taskItem.path, TaskCreatedAt: taskItem.createdAt,
		ServiceID: item.ServiceID, ModuleID: item.ModuleID, Path: item.Path,
		Mode: "review", Open: true, CompareMode: workspaceReviewCommits, BaselineRef: "origin/main", ReviewGeneration: 1,
		StartCommit: "one", EndCommit: "one", CommitHashes: []string{"one"},
		Commits:       []domain.WorkspaceGitCommit{{FullHash: "three", ParentHashes: []string{"two"}}, {FullHash: "two", ParentHashes: []string{"one"}}, {FullHash: "one"}},
		CommitsLoaded: true, HistoryHeadSHA: "head", HistoryBaselineSHA: "baseline", HistoryNextOffset: 3, HistoryHasMore: true, HistorySummaries: true,
		ExpandedCommits: make(map[string]bool),
	}
	s := &app{backend: backend, win: &mygo.Window{}, page: "tasks", snapshot: application.Snapshot{Home: filepath.Join(t.TempDir(), "home")},
		taskUnit:      taskController{state: taskState{tasks: []task{taskItem}}},
		workspaceUnit: workspaceController{state: workspaceState{workspaceHistory: state}},
	}
	s.settingsUnit.state.configJSON = `{"gitExecutablePath":"isolated-git"}`
	t.Cleanup(func() {
		s.dismissWorkspaceHistory()
		close(backend.stop)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.workspaceUnit.closeWorkspaceHistoryJobs(ctx); err != nil {
			t.Error(err)
		}
	})
	return s, state, backend
}

func waitWorkspaceHistoryRead(t *testing.T, s *app, backend *workspaceHistoryBlockingBackend, kind string) workspaceHistoryPendingRead {
	t.Helper()
	select {
	case read := <-backend.reads:
		job := s.workspaceUnit.state.workspaceHistoryJobs.currentFor(read.action)
		if read.action.Kind != kind || job == nil {
			t.Fatalf("unexpected history read: %+v, owner=%p", read.action, job)
		}
		return workspaceHistoryPendingRead{read, job}
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not reach the blocking backend", kind)
		return workspaceHistoryPendingRead{}
	}
}

func finishWorkspaceHistoryRead(t *testing.T, s *app, pending workspaceHistoryPendingRead, data any) {
	t.Helper()
	finishWorkspaceHistoryReadResult(t, s, pending, data, nil)
}

func finishWorkspaceHistoryReadResult(t *testing.T, s *app, pending workspaceHistoryPendingRead, data any, err error) {
	t.Helper()
	result := application.ActionResult{Data: data}
	pending.read.reply <- workspaceHistoryBlockingReply{result, err}
	select {
	case <-pending.read.returned:
	case <-time.After(2 * time.Second):
		t.Fatal("backend did not return the released result")
	}
	// A zero native window queues Update without a GUI loop. Deliver the exact
	// callback on this test's owner thread, as the existing cold-entry fixture does.
	s.finishWorkspaceHistoryJob(pending.job, result, err)
	select {
	case <-pending.job.done:
	case <-time.After(2 * time.Second):
		t.Fatal("history worker did not finish after its callback")
	}
}

func startWorkspaceHistoryComparison(t *testing.T, s *app, state *workspaceHistoryState, backend *workspaceHistoryBlockingBackend, kind string) workspaceHistoryPendingRead {
	t.Helper()
	if kind == "workspace.diff-files" {
		s.readWorkspaceReviewFiles(state)
	} else {
		state.DiffSessionID, state.BaseCommit, state.TargetCommit = "original-session", "base", "target"
		state.Files = []workspace.CompareChange{{Path: "current.go", Kind: "MODIFIED"}}
		s.selectWorkspaceReviewFile(state, state.Files[0])
	}
	return waitWorkspaceHistoryRead(t, s, backend, kind)
}

func finishWorkspaceHistoryComparison(t *testing.T, s *app, state *workspaceHistoryState, backend *workspaceHistoryBlockingBackend, pending workspaceHistoryPendingRead) {
	t.Helper()
	if pending.read.action.Kind == "workspace.diff-files" {
		finishWorkspaceHistoryRead(t, s, pending, workspace.CompareFilesResult{SessionID: "new-session", BaseCommit: "base", TargetCommit: "target",
			Files: []workspace.CompareChange{{Path: "current.go", Kind: "MODIFIED"}}})
		pending = waitWorkspaceHistoryRead(t, s, backend, "workspace.diff-preview")
	}
	finishWorkspaceHistoryRead(t, s, pending, workspace.WorkspaceGitFilePreview{Content: &workspace.WorkspaceFilePreviewContent{Text: "current comparison"}})
	if state.Preview == nil || state.Preview.Content == nil || state.Preview.Content.Text != "current comparison" || state.PreviewLoading || state.FilesLoading {
		t.Fatal("the independent comparison did not reach its visible preview")
	}
}

func TestWorkspaceHistoryDetailsAndPagesDoNotCancelSlowDiff(t *testing.T) {
	for _, kind := range []string{"workspace.diff-files", "workspace.diff-preview"} {
		t.Run(kind, func(t *testing.T) {
			s, state, backend := newWorkspaceHistoryJobsFixture(t)
			comparison := startWorkspaceHistoryComparison(t, s, state, backend, kind)
			s.readWorkspaceCommitMessage(state, "three")
			message := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
			if comparison.read.ctx.Err() != nil {
				t.Fatal("opening a message cancelled the comparison")
			}
			s.readWorkspaceHistoryPage(state, false)
			page := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
			if comparison.read.ctx.Err() != nil || message.read.ctx.Err() != nil {
				t.Fatal("loading another page cancelled the comparison or expanded message")
			}
			finishWorkspaceHistoryRead(t, s, page, workspace.WorkspaceGitHistoryPage{HeadSHA: "head", BaselineSHA: "baseline", Offset: 3, NextOffset: 4,
				Commits: []domain.WorkspaceGitCommit{{FullHash: "older"}}, SummariesOnly: true})
			finishWorkspaceHistoryRead(t, s, message, workspace.WorkspaceGitCommitMessage{SHA: "three", Message: "complete message"})
			if len(state.Commits) != 4 || state.HistoryMessageSHA != "" || state.HistoryMessages["three"].Message != "complete message" || comparison.read.ctx.Err() != nil {
				t.Fatal("page/message completion lost its state or the comparison owner")
			}
			finishWorkspaceHistoryComparison(t, s, state, backend, comparison)
		})
	}
}

func TestWorkspaceHistoryDiffAfterCommitDebounceKeepsMessageOwner(t *testing.T) {
	s, state, backend := newWorkspaceHistoryJobsFixture(t)
	s.readWorkspaceCommitMessage(state, "three")
	message := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
	s.selectWorkspaceReviewCommit(state, "three")
	if message.read.ctx.Err() != nil || state.HistoryMessageSHA != "three" {
		t.Fatal("selecting a range cancelled or hid its independent message read")
	}
	// Enter the same read that the debounce callback schedules, without waiting
	// for a native UI event loop or a timing-dependent timer race.
	s.readWorkspaceReviewFiles(state)
	comparison := waitWorkspaceHistoryRead(t, s, backend, "workspace.diff-files")
	if message.read.ctx.Err() != nil || state.HistoryMessageSHA != "three" {
		t.Fatal("the debounced comparison replaced the message owner")
	}
	finishWorkspaceHistoryComparison(t, s, state, backend, comparison)
	finishWorkspaceHistoryRead(t, s, message, workspace.WorkspaceGitCommitMessage{SHA: "three", Message: "finished after diff"})
	if state.HistoryMessageSHA != "" || state.HistoryMessages["three"].Message != "finished after diff" {
		t.Fatal("the expanded message stayed in its loading state")
	}
}

func TestWorkspaceHistoryPurposeReplacementAndScopeInvalidation(t *testing.T) {
	for _, changed := range []string{"task", "home", "config", "workspace", "page"} {
		t.Run(changed, func(t *testing.T) {
			s, state, backend := newWorkspaceHistoryJobsFixture(t)
			comparison := startWorkspaceHistoryComparison(t, s, state, backend, "workspace.diff-preview")
			s.readWorkspaceCommitMessage(state, "three")
			oldMessage := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
			s.readWorkspaceCommitMessage(state, "two")
			message := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
			if oldMessage.read.ctx.Err() == nil || message.read.ctx.Err() != nil || comparison.read.ctx.Err() != nil || state.HistoryMessageSHA != "two" {
				t.Fatal("message replacement affected another purpose or failed to retire its predecessor")
			}
			s.readWorkspaceHistoryPage(state, false)
			page := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
			switch changed {
			case "task":
				s.taskUnit.state.tasks[0].createdAt = "recreated"
			case "home":
				s.snapshot.Home += "-changed"
			case "config":
				s.settingsUnit.state.configJSON = `{"gitExecutablePath":"changed"}`
			case "workspace":
				s.taskUnit.state.tasks[0].workspaces[0].Branch = "other-branch"
			case "page":
				s.page = "settings"
			}
			s.observeWorkspaceHistoryJobs()
			for _, pending := range []workspaceHistoryPendingRead{comparison, message, page} {
				if pending.read.ctx.Err() == nil || s.workspaceHistoryJobRelevant(pending.job) {
					t.Fatal("a purpose retained writeback permission after its scope changed")
				}
			}
			if state.HistoryMessageSHA != "" || state.HistoryLoading || state.PreviewLoading {
				t.Fatal("scope cancellation retained a loading indicator")
			}
		})
	}
}

func TestWorkspaceHistoryCloseCancelsAndJoinsEveryPurpose(t *testing.T) {
	s, state, backend := newWorkspaceHistoryJobsFixture(t)
	comparison := startWorkspaceHistoryComparison(t, s, state, backend, "workspace.diff-preview")
	s.readWorkspaceCommitMessage(state, "three")
	message := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
	s.readWorkspaceHistoryPage(state, false)
	page := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
	prefetchCtx, cancel := context.WithCancel(context.Background())
	prefetch := &workspaceReviewPrefetchJob{cancel: cancel, done: make(chan struct{})}
	jobs := s.workspaceUnit.state.workspaceHistoryJobs
	jobs.prefetchRunning = map[*workspaceReviewPrefetchJob]struct{}{prefetch: {}}
	closed := make(chan error, 1)
	ctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	go func() { closed <- s.workspaceUnit.closeWorkspaceHistoryJobs(ctx) }()
	for _, pending := range []workspaceHistoryPendingRead{comparison, message, page} {
		select {
		case <-pending.read.ctx.Done():
		case <-ctx.Done():
			t.Fatal("close did not cancel every purpose")
		}
	}
	select {
	case <-prefetchCtx.Done():
	case <-ctx.Done():
		t.Fatal("close did not cancel prefetch")
	}
	select {
	case err := <-closed:
		t.Fatalf("close returned before blocked backend reads completed: %v", err)
	default:
	}
	for _, pending := range []workspaceHistoryPendingRead{comparison, message, page} {
		pending.read.reply <- workspaceHistoryBlockingReply{}
		select {
		case <-pending.job.done:
		case <-ctx.Done():
			t.Fatal("a cancelled owner was not joined")
		}
	}
	select {
	case err := <-closed:
		t.Fatalf("close returned before prefetch completed: %v", err)
	default:
	}
	close(prefetch.done)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("close did not join the completed owners")
	}
	if len(jobs.current) != 0 || s.workspaceHistoryJobRelevant(page.job) {
		t.Fatal("closed jobs still own a writeback slot")
	}
	if _, err := s.beginWorkspaceHistoryJob(page.read.action); err == nil {
		t.Fatal("a closed registry accepted another history read")
	}
}
