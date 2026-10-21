package desktop

import (
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (s *app) productionProgressCurrent(state *productionUIState, v *productionView, request *productionWorker) bool {
	if state == nil || s.production != state || v == nil || request == nil || !v.Busy || v.request != request {
		return false
	}
	state.mu.Lock()
	closed := state.closed
	state.mu.Unlock()
	i := request.identity
	return !closed && state.views[productionKey(i.Selection)] == v && v.Generation == i.Generation &&
		v.Selection == i.Selection && v.BatchID == i.BatchID && v.ManualTag == i.ManualTag && v.Branch == i.Branch &&
		s.snapshot.Home == i.Home && s.currentProductionScope() == i.ConfigScope
}

// Use the shared native toast viewport so progress never participates in the
// page's layout. Each cancel action owns one request, even across navigation.
func (s *app) renderProductionProgressNotifications(c *ui.Context) {
	const prefix = "production-progress-"
	active := make(map[string]bool)
	if state := s.production; state != nil {
		keys := make([]string, 0, len(state.views))
		for key, view := range state.views {
			if view != nil && s.productionProgressCurrent(state, view, view.request) && !view.request.silentProgress {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			view := state.views[key]
			request := view.request
			title := s.productionSection(view, nil, view.ActiveStep).Activity
			if s.page != "production" || key != state.selected || len(keys) > 1 {
				name := view.Selection.ServiceID
				for _, choice := range state.choices {
					if choice.GroupID == view.Selection.GroupID && choice.ID == view.Selection.ServiceID {
						name = choice.Name
						break
					}
				}
				title = name + "：" + title
			}
			scope := fmt.Sprintf("%s%p", prefix, view)
			active[scope] = true
			s.operationNotifications.progress(c, scope, title, fmt.Sprintf("%p", request), "取消", func() {
				if s.productionProgressCurrent(state, view, request) {
					request.cancel()
				}
			})
		}
	}
	for scope := range s.operationNotifications.shown {
		if strings.HasPrefix(scope, prefix) && !active[scope] {
			s.operationNotifications.progress(c, scope, "", "", "", nil)
		}
	}
}
