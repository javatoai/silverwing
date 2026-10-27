package desktop

import (
	"context"
	"sync"

	"silverwing-go/internal/application"
)

type requirementListJob struct {
	generation         uint64
	identity           requirementListIdentity
	identityContext    requirementIdentityContext
	hasIdentityContext bool
	action             application.Action
	ctx                context.Context
	cancel             context.CancelFunc
	done               chan struct{}
	items              []application.Requirement
	selectedKey        string
	sprintKey          string
	sameSprint         bool
	progress           *requirementListProgressState
	onProgress         func(application.RequirementListProgress)
}

// Window-thread-owned fields track the current request and identity. Only the
// closed/running fields are shared with workers and guarded by mu.
type requirementListJobState struct {
	generation  uint64
	current     *requirementListJob
	identity    requirementListIdentity
	hasIdentity bool
	snapshots   map[requirementListSnapshotKey]requirementListViewSnapshot

	mu      sync.Mutex
	running map[*requirementListJob]struct{}
	closed  bool
}

type requirementListSnapshotKey struct {
	identity requirementListIdentity
	sprint   string
}

type requirementListViewSnapshot struct {
	fetchedAt int64
	items     []application.Requirement
	selected  string
	sprints   []requirementSprint
	failures  []string
	completed int
	loadError string
	loaded    bool
	estimate  string // last complete total; scoped by the snapshot's identity/Sprint
}
