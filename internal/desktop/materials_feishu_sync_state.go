package desktop

import (
	"context"
	"sync"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo"
)

type materialsFeishuSyncStage string

const (
	materialsFeishuSyncPlanning        materialsFeishuSyncStage = "PLANNING"
	materialsFeishuSyncWaitingApproval materialsFeishuSyncStage = "WAITING_APPROVAL"
	materialsFeishuSyncUploading       materialsFeishuSyncStage = "UPLOADING"
	materialsFeishuSyncPublishing      materialsFeishuSyncStage = "PUBLISHING_COMMENT"
	materialsFeishuSyncStopping        materialsFeishuSyncStage = "STOPPING"
	materialsFeishuSyncFinished        materialsFeishuSyncStage = "FINISHED"
)

type materialsFeishuSyncJobKind string

const (
	materialsFeishuSyncPlanJob    materialsFeishuSyncJobKind = "plan"
	materialsFeishuSyncConfirmJob materialsFeishuSyncJobKind = "confirm"
	materialsFeishuSyncUploadJob  materialsFeishuSyncJobKind = "upload"
	materialsFeishuSyncCommentJob materialsFeishuSyncJobKind = "comment"
)

type materialsFeishuSyncSession struct {
	RunID             string
	TaskKey           string
	TaskID            string
	TaskPath          string
	TaskCreatedAt     string
	TaskGroupID       string
	TaskRequirementID string
	MaterialsRoot     string
	RequirementURL    string
	TaskTitle         string
	Home              string

	Request          domain.MaterialsSyncRequest
	Comment          application.MaterialsSyncCommentRequest
	CommentAvailable bool

	Stage          materialsFeishuSyncStage
	Visible        bool
	StartedAt      time.Time
	Plan           *domain.MaterialsSyncPlan
	DeletionPaths  map[string]bool
	OverwritePaths map[string]bool
	Progress       domain.MaterialsSyncProgress
	Upload         *domain.MaterialsSyncResult
	DirectoryURL   string
	CommentResult  *domain.MaterialsCommentResult
	Error          string
	Summary        string
	NextStep       materialsFeishuSyncJobKind
	Done           chan struct{}
}

// materialsFeishuSyncJobsState separates the single Feishu sync slot from
// ordinary desktop operations. current and unread belong to the window thread;
// active, operation, running, and closed are guarded because shutdown and
// cancellation can arrive from another goroutine.
type materialsFeishuSyncJobsState struct {
	current   *materialsFeishuSyncSession
	unread    map[string]*materialsFeishuSyncSession
	directory *materialsFeishuDirectoryJob

	mu                    sync.Mutex
	active                *materialsFeishuSyncSession
	operation             *materialsFeishuSyncJob
	running               map[*materialsFeishuSyncJob]struct{}
	directoryRunning      map[*materialsFeishuDirectoryJob]struct{}
	commentRefreshRunning map[*materialsFeishuCommentRefreshJob]struct{}
	releaseOnCleanup      map[*materialsFeishuSyncSession]bool
	closed                bool
}

type materialsFeishuSyncJob struct {
	session *materialsFeishuSyncSession
	kind    materialsFeishuSyncJobKind
	action  application.Action
	backend application.Backend
	window  *mygo.Window

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

type materialsFeishuDirectoryJob struct {
	taskKey, taskID, taskPath, taskCreatedAt, taskGroupID string
	taskRequirementID                                     string
	materialsRoot, requirementLink, home, targetURL       string
	requirementID, requirementTitle, relativePath         string
	action                                                application.Action
	backend                                               application.Backend
	window                                                *mygo.Window
	ctx                                                   context.Context
	cancel                                                context.CancelFunc
	done                                                  chan struct{}
}

type materialsFeishuCommentRefreshJob struct {
	action                                    application.Action
	reader                                    *requirementReaderState
	home, executable, taskPath, taskCreatedAt string
	configJSON                                string
	backend                                   application.Backend
	window                                    *mygo.Window
	ctx                                       context.Context
	cancel                                    context.CancelFunc
	done                                      chan struct{}
}

func (s *app) ensureMaterialsFeishuSyncJobs() *materialsFeishuSyncJobsState {
	if s.materialsFeishuSyncJobs == nil {
		s.materialsFeishuSyncJobs = &materialsFeishuSyncJobsState{
			unread:                make(map[string]*materialsFeishuSyncSession),
			running:               make(map[*materialsFeishuSyncJob]struct{}),
			directoryRunning:      make(map[*materialsFeishuDirectoryJob]struct{}),
			commentRefreshRunning: make(map[*materialsFeishuCommentRefreshJob]struct{}),
		}
	} else {
		if s.materialsFeishuSyncJobs.unread == nil {
			s.materialsFeishuSyncJobs.unread = make(map[string]*materialsFeishuSyncSession)
		}
		if s.materialsFeishuSyncJobs.running == nil {
			s.materialsFeishuSyncJobs.running = make(map[*materialsFeishuSyncJob]struct{})
		}
		if s.materialsFeishuSyncJobs.directoryRunning == nil {
			s.materialsFeishuSyncJobs.directoryRunning = make(map[*materialsFeishuDirectoryJob]struct{})
		}
		if s.materialsFeishuSyncJobs.commentRefreshRunning == nil {
			s.materialsFeishuSyncJobs.commentRefreshRunning = make(map[*materialsFeishuCommentRefreshJob]struct{})
		}
	}
	return s.materialsFeishuSyncJobs
}

func (s *app) materialsFeishuSyncActive() bool {
	jobs := s.materialsFeishuSyncJobs
	if jobs == nil {
		return false
	}
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	return jobs.active != nil
}

func materialsFeishuSyncTaskKey(t task) string {
	return t.id + "\x00" + normalizedReadingPath(t.path)
}
