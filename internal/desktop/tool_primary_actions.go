package desktop

import "silverwing-go/internal/tools"

type developerToolPrimaryAction struct {
	tab       string
	kind      string
	operation string
}

func developerToolPrimaryActionForTab(tab string) (developerToolPrimaryAction, bool) {
	switch tab {
	case "JSON":
		return developerToolPrimaryAction{tab: tab, kind: "tool.json", operation: "format"}, true
	case "日期时间":
		return developerToolPrimaryAction{tab: tab, kind: "tool.time", operation: "convert"}, true
	case "文本对比":
		return developerToolPrimaryAction{tab: tab, kind: "tool.diff"}, true
	case "端口":
		return developerToolPrimaryAction{tab: tab, kind: "tool.ports"}, true
	default:
		return developerToolPrimaryAction{}, false
	}
}

func developerToolPrimaryActionEnabled(page, activeTab, requestedTab string, toolBusy, localPending bool) bool {
	if page != "tools" || activeTab != requestedTab || toolBusy || localPending {
		return false
	}
	if _, ok := developerToolPrimaryActionForTab(requestedTab); !ok {
		return false
	}
	return true
}

func (s *app) toolPrimaryActionEnabled(state *toolPageState, tab string) bool {
	if s == nil || state == nil || s.tools != state {
		return false
	}
	route, ok := developerToolPrimaryActionForTab(tab)
	if !ok {
		return false
	}
	localPending := false
	switch tab {
	case "JSON":
		localPending = state.jsonPending
	case "日期时间":
		localPending = state.timePending
	case "文本对比":
		localPending = state.diffPending
	}
	return developerToolPrimaryActionEnabled(s.page, state.activeTab, tab, s.developerToolJobBusy(route.kind), localPending)
}

func (s *app) developerToolPrimaryActionInScope(state *toolPageState, tab string) bool {
	if s == nil || state == nil || s.tools != state || s.page != "tools" || state.activeTab != tab || !s.developerToolTabVisible(tab) {
		return false
	}
	_, ok := developerToolPrimaryActionForTab(tab)
	return ok
}

// runDeveloperToolPrimaryAction is shared by buttons and shortcuts. Buttons
// apply the per-tool busy gate before calling it; shortcuts intentionally can
// cancel and restart the same tool request as the source controller does.
func (s *app) runDeveloperToolPrimaryAction(state *toolPageState, tab string) bool {
	if !s.developerToolPrimaryActionInScope(state, tab) {
		return false
	}
	route, ok := developerToolPrimaryActionForTab(tab)
	if !ok {
		return false
	}
	switch route.kind {
	case "tool.json":
		s.dispatchJSON(route.operation)
	case "tool.time":
		s.cancelDeveloperToolJob("tool.time")
		revision, shouldDispatch := prepareImmediateTimeConversion(state)
		if !shouldDispatch {
			return true
		}
		zone := state.timeZone
		if zone == "" {
			zone = "Asia/Shanghai"
		}
		s.act(route.kind, "", "", "", "", state.timeInput, map[string]any{
			"operation": route.operation,
			"unit":      state.timeUnit,
			"zone":      zone,
			"revision":  revision,
		})
	case "tool.diff":
		s.dispatchDiff()
	case "tool.ports":
		s.queryPorts()
	default:
		return false
	}
	return true
}

// prepareImmediateTimeConversion mirrors DateTimeToolController.convertNow:
// blank input cancels pending work and clears conversion state without making
// a backend request; nonblank input cancels the debounce and lets the caller
// dispatch immediately.
func prepareImmediateTimeConversion(state *toolPageState) (uint64, bool) {
	if state == nil {
		return 0, false
	}
	if state.timeCancel != nil {
		state.timeCancel()
		state.timeCancel = nil
	}
	state.timeRevision++
	if tools.TimeInputBlank(state.timeInput) {
		state.timeResult = tools.TimeConversion{}
		state.timeHasData = false
		state.timeError = ""
		state.timePending = false
		return state.timeRevision, false
	}
	state.timePending = true
	state.timeError = ""
	return state.timeRevision, true
}

func timeClearEnabled(state *toolPageState) bool {
	return state != nil && state.timeInput != ""
}

func clearTimeConversionInput(state *toolPageState) bool {
	if !timeClearEnabled(state) {
		return false
	}
	if state.timeCancel != nil {
		state.timeCancel()
		state.timeCancel = nil
	}
	state.timeRevision++
	state.timeInput = ""
	state.timeResult = tools.TimeConversion{}
	state.timeHasData = false
	state.timePending = false
	state.timeError = ""
	return true
}
