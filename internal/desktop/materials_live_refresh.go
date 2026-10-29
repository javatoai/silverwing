package desktop

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Match Source's exact staging names; ordinary dotfiles remain materials.
var materialsRefreshStagingName = regexp.MustCompile(`(?i)^\.silverwing-(?:import-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.tmp|(?:rename|paste)-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)

// The observer owns only native directory notifications. It never retains
// document bytes or periodically scans the filesystem.
type materialsLiveRefreshState struct {
	ctx                               context.Context
	cancel                            context.CancelFunc
	done                              chan struct{}
	ready                             chan struct{}
	wake                              chan struct{}
	mu                                sync.Mutex
	selected                          string
	catalog, touched, pending, failed bool
}

func (s *app) observeMaterialsLiveRefresh() {
	if owner := s.materialsUnit.state.materialsLiveRefreshLifecycle; owner != nil && (owner.closed || owner.barrier != nil) {
		return
	}
	if !s.materialsBrowserVisible() || !s.taskUnit.currentTask().materialsReady() || s.backend == nil {
		s.materialsUnit.closeMaterialsLiveRefresh()
		return
	}
	state := s.ensureMaterialsFileManagementState()
	live := state.liveRefresh
	if live == nil {
		ctx, cancel := context.WithCancel(context.Background())
		live = &materialsLiveRefreshState{ctx: ctx, cancel: cancel, done: make(chan struct{}), ready: make(chan struct{}), wake: make(chan struct{}, 1)}
		state.liveRefresh = live
		s.materialsUnit.ensureMaterialsLiveRefreshLifecycle().remember(live)
		root := state.root
		win := s.win
		go live.run(root, func() {
			if win != nil && !win.IsDestroyed() {
				win.Update(func() {
					if live.ctx.Err() == nil && s.materialsUnit.state.materialsFileManagement == state && state.liveRefresh == live {
						s.observeMaterialsLiveRefresh()
					}
				})
			}
		})
	}
	relative := s.materialsRelativePath(s.materialsUnit.state.documentPath)
	live.mu.Lock()
	live.selected = filepath.ToSlash(relative)
	if s.materialsFileReadBlocked() || s.loading || !live.pending {
		live.mu.Unlock()
		return
	}
	catalog, touched := live.catalog, live.touched
	live.catalog, live.touched, live.pending = false, false, false
	live.mu.Unlock()
	if catalog {
		s.materialsUnit.state.documentsLoaded, s.materialsUnit.state.documentsRequested = false, false
	}
	if touched && s.materialsUnit.state.documentPath != "" {
		s.rereadSelectedMaterial(false)
	} else if catalog {
		s.loadDocuments()
	}
}

func (s *materialsController) closeMaterialsLiveRefresh() {
	if state := s.state.materialsFileManagement; state != nil && state.liveRefresh != nil {
		s.retireMaterialsLiveRefresh(state.liveRefresh)
		state.liveRefresh = nil
	}
}

func (live *materialsLiveRefreshState) record(relative string) {
	portable := filepath.ToSlash(relative)
	for _, segment := range strings.Split(portable, "/") {
		if strings.EqualFold(segment, ".git") || materialsRefreshStagingName.MatchString(segment) {
			return
		}
	}
	live.mu.Lock()
	live.catalog = true
	live.touched = live.touched || portable == "" || (live.selected != "" && (strings.EqualFold(portable, live.selected) ||
		strings.HasPrefix(strings.ToLower(live.selected), strings.ToLower(portable)+"/")))
	live.mu.Unlock()
	select {
	case live.wake <- struct{}{}:
	default:
	}
}

func (live *materialsLiveRefreshState) run(root string, notify func()) {
	defer close(live.done)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		err := watchMaterialsDirectory(live.ctx, root, live.record, func() { close(live.ready) })
		if err != nil && live.ctx.Err() == nil {
			live.mu.Lock()
			live.failed = true
			live.mu.Unlock()
			live.record("")
		}
	}()
	defer func() { live.cancel(); <-watchDone }()
	for {
		select {
		case <-live.ctx.Done():
			return
		case <-live.wake:
		}
		// This timer merges an event burst; it never reads the filesystem.
		quiet := time.NewTimer(200 * time.Millisecond)
		ceiling := time.NewTimer(time.Second)
	burst:
		for {
			select {
			case <-live.ctx.Done():
				quiet.Stop()
				ceiling.Stop()
				return
			case <-live.wake:
				if !quiet.Stop() {
					select {
					case <-quiet.C:
					default:
					}
				}
				quiet.Reset(200 * time.Millisecond)
			case <-quiet.C:
				break burst
			case <-ceiling.C:
				break burst
			}
		}
		quiet.Stop()
		ceiling.Stop()
		live.mu.Lock()
		live.pending = true
		live.mu.Unlock()
		notify()
	}
}
