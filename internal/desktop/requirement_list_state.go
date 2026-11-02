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

type requirementListWindow interface {
	Update(func())
	Invalidate()
	IsDestroyed() bool
}

// Window-thread-owned fields track the current request and identity. Only the
// closed/running fields are shared with workers and guarded by mu.
type requirementListJobState struct {
	generation  uint64
	current     *requirementListJob
	identity    requirementListIdentity
	hasIdentity bool
	snapshots   map[requirementListSnapshotKey]requirementListViewSnapshot
	window      requirementListWindow

	// The catalogue belongs to the whole account/configuration, rather than a
	// per-Sprint item snapshot. A positive timestamp confirms a complete read,
	// including a complete empty directory.
	catalog           []requirementSprint
	catalogFetchedAt  int64
	catalogGeneration uint64
	catalogCurrent    *requirementListJob
	catalogAttempted  bool

	mu      sync.Mutex
	running map[*requirementListJob]struct{}
	closed  bool
}

type requirementListSnapshotKey struct {
	identity requirementListIdentity
	sprint   string
}

type requirementListViewSnapshot struct {
	fetchedAt        int64
	items            []application.Requirement
	selected         string
	sprints          []requirementSprint
	sprintsFetchedAt int64
	failures         []string
	completed        int
	loadError        string
	loaded           bool
	estimate         string // last complete total; scoped by the snapshot's identity/Sprint
}
