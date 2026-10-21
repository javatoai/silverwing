package desktop

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

type materialsPathCopyRequest struct {
	cancel context.CancelFunc
	taskID string
	root   string
}

func (s *app) closeMaterialsPathCopy() {
	if state := s.materialsFileManagement; state != nil && state.pathCopyRequest != nil {
		state.pathCopyRequest.cancel()
		state.pathCopyRequest = nil
	}
}

func materialsSelectedPaths(state *materialsFileManagementState) []string {
	if state == nil {
		return nil
	}
	keys := make([]string, 0, len(state.selected))
	for key := range state.selected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	paths := make([]string, 0, len(keys))
	for _, key := range keys {
		paths = append(paths, state.selected[key].Path)
	}
	return paths
}

func (s *app) copyMaterialsSelectionPaths(services ui.Services) {
	state := s.ensureMaterialsFileManagementState()
	paths := materialsSelectedPaths(state)
	if len(paths) == 0 || state.pathCopyRequest != nil || s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	request := &materialsPathCopyRequest{cancel: cancel, taskID: state.taskID, root: state.root}
	state.pathCopyRequest = request
	backend, win := s.backend, s.win
	go func() {
		defer cancel()
		result, err := backend.Dispatch(ctx, application.Action{Kind: "document.copy-paths", TaskID: request.taskID, Payload: map[string]any{"paths": paths}})
		if win.IsDestroyed() {
			return
		}
		updated := make(chan struct{})
		win.Update(func() {
			defer close(updated)
			if ctx.Err() != nil || win.IsDestroyed() || s.materialsFileManagement != state || state.pathCopyRequest != request {
				return
			}
			state.pathCopyRequest = nil
			task := s.currentTask()
			if task.id != request.taskID || normalizedReadingRoot(task.materialsPath) != normalizedReadingRoot(request.root) {
				return
			}
			if err != nil {
				s.lastError = err.Error()
				return
			}
			s.applyMaterialsSelectionPaths(services, paths, result)
		})
		select {
		case <-updated:
		case <-ctx.Done():
		}
	}()
}

func (s *app) applyMaterialsSelectionPaths(services ui.Services, paths []string, result application.ActionResult) {
	resolved, decodeErr := decodeData[string](result.Data)
	if decodeErr != nil || len(resolved) != len(paths) {
		s.lastError = "复制路径的读取结果不完整，请刷新后重试。"
		return
	}
	s.copyTextToClipboard(services, strings.Join(resolved, "\n"), fmt.Sprintf("已复制 %d 个路径", len(resolved)))
}
