package desktop

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/asyncjob"
	"silverwing-go/internal/tools"

	"github.com/egoist/mygo"
)

// Developer tool actions have their own lifecycle. They do not occupy the
// application's one-at-a-time workflow slot, so JSON and diff can complete
// independently while another action is running.
type developerToolJobsState struct {
	current    map[string]*developerToolJob // UI thread only
	latest     map[string]uint64            // UI thread only
	visibleTab string                       // UI thread only
	lifecycle  asyncjob.Group
}

type developerToolJob struct {
	key           string
	tab           string
	token         uint64
	action        application.Action
	state         *toolPageState
	backend       application.Backend
	window        *mygo.Window
	home          string
	owner         *developerToolJobsState
	lease         *asyncjob.Lease
	ctx           context.Context
	cancel        context.CancelFunc
	preserveHide  bool
	preserveClose bool
	jsonNodeCopy  *jsonNodeCopyRequest
}

func independentDeveloperToolAction(kind string) bool {
	switch kind {
	case "tool.json", "tool.time", "tool.diff", "tool.ports", "tool.port-stop":
		return true
	default:
		return false
	}
}

func developerToolTabForAction(kind string) string {
	switch kind {
	case "tool.json":
		return "JSON"
	case "tool.time":
		return "日期时间"
	case "tool.diff":
		return "文本对比"
	case "tool.ports", "tool.port-stop":
		return "端口"
	default:
		return ""
	}
}

func (s *app) ensureDeveloperToolJobs() *developerToolJobsState {
	if s.toolsUnit.state.toolJobs == nil {
		s.toolsUnit.state.toolJobs = &developerToolJobsState{
			current: make(map[string]*developerToolJob),
			latest:  make(map[string]uint64),
		}
	} else {
		if s.toolsUnit.state.toolJobs.current == nil {
			s.toolsUnit.state.toolJobs.current = make(map[string]*developerToolJob)
		}
		if s.toolsUnit.state.toolJobs.latest == nil {
			s.toolsUnit.state.toolJobs.latest = make(map[string]uint64)
		}
	}
	return s.toolsUnit.state.toolJobs
}

func cloneDeveloperToolAction(action application.Action) application.Action {
	if action.Payload == nil {
		return action
	}
	payload := make(map[string]any, len(action.Payload))
	for key, value := range action.Payload {
		payload[key] = value
	}
	action.Payload = payload
	return action
}

// dispatchDeveloperToolJob is called before the application-wide busy gate.
// The action and all scope values are captured on the window thread.
func (s *app) dispatchDeveloperToolJob(action application.Action) bool {
	if !independentDeveloperToolAction(action.Kind) {
		return false
	}
	state := s.ensureToolState()
	jobs := s.ensureDeveloperToolJobs()
	tab := developerToolTabForAction(action.Kind)
	if state == nil || s.page != "tools" || state.activeTab != tab || !s.developerToolTabVisible(tab) {
		s.applyDeveloperToolJobFailureToState(state, action, errors.New("请先打开对应的研发工具页后重试。"))
		return true
	}
	if s.backend == nil || (s.win != nil && s.win.IsDestroyed()) {
		s.applyDeveloperToolJobFailureToState(state, action, errors.New("研发工具后端或窗口不可用。"))
		return true
	}
	if action.Kind == "tool.port-stop" {
		if !boolPayloadValue(action.Payload, "confirmed") {
			s.applyDeveloperToolJobFailureToState(state, action, errors.New("结束端口进程需要明确确认。"))
			return true
		}
		if _, ok := actionData[tools.PortProcess](action.Payload); !ok {
			s.applyDeveloperToolJobFailureToState(state, action, errors.New("端口结束请求缺少完整进程身份。"))
			return true
		}
		if state.portStoppingPID != 0 {
			active := jobs.current["tool.port-stop"]
			preRegisteredSameRequest := state.portStoppingPID == payloadInt(action.Payload, "pid") &&
				state.portStoppingSequence == payloadUint64(action.Payload, "stopRequest")
			if (active != nil && active.ctx.Err() == nil) || !preRegisteredSameRequest {
				state.portInputError = "已有端口结束操作正在等待真实结果。"
				state.portConfirm = false
				if s.win != nil {
					s.win.Invalidate()
				}
				return true
			}
		}
	}

	if jobs.lifecycle.Closed() {
		s.applyDeveloperToolJobFailureToState(state, action, errors.New("研发工具已进入退出清理阶段。"))
		return true
	}

	key := action.Kind
	if previous := jobs.current[key]; previous != nil {
		if action.Kind == "tool.port-stop" && previous.ctx.Err() == nil {
			s.applyDeveloperToolJobFailureToState(state, action, errors.New("已有端口结束操作正在等待真实结果。"))
			return true
		}
		previous.cancel()
		delete(jobs.current, key)
	}
	jobs.latest[key]++
	request := &developerToolJob{
		key: key, tab: tab, token: jobs.latest[key], action: cloneDeveloperToolAction(action),
		state: state, backend: s.backend, window: s.win, home: s.snapshot.Home,
		preserveHide: action.Kind == "tool.port-stop", preserveClose: action.Kind == "tool.port-stop",
	}
	lease, err := jobs.lifecycle.Begin(context.Background(), asyncjob.Options{CancelOnClose: !request.preserveClose})
	if err != nil {
		s.applyDeveloperToolJobFailureToState(state, action, errors.New("研发工具已进入退出清理阶段。"))
		return true
	}
	request.owner, request.lease = jobs, lease
	request.ctx, request.cancel = lease.Context(), lease.Cancel
	jobs.current[key] = request

	if action.Kind == "tool.port-stop" {
		state.portStoppingPID = payloadInt(action.Payload, "pid")
		state.portStoppingSequence = payloadUint64(action.Payload, "stopRequest")
		state.portInputError = ""
	}
	if s.win != nil {
		s.win.Invalidate()
	}
	go s.runDeveloperToolJob(request)
	return true
}

func boolPayloadValue(payload map[string]any, key string) bool {
	value, _ := payload[key].(bool)
	return value
}

func payloadInt(payload map[string]any, key string) int {
	return int(payloadUint64(payload, key))
}

func (s *app) runDeveloperToolJob(request *developerToolJob) {
	var cleanupErr error
	defer func() {
		request.cancel()
		request.lease.Complete(cleanupErr)
	}()
	result, err := executeToolOperation(request.ctx, request.backend, request.action)
	cleanupErr = asyncJobCleanupError(err)
	window := request.window
	if window == nil || window.IsDestroyed() {
		return
	}
	updateDone := make(chan struct{})
	if !request.owner.lifecycle.Post(func(callback func()) bool {
		if window.IsDestroyed() {
			return false
		}
		window.Update(func() {
			defer close(updateDone)
			callback()
		})
		return true
	}, func() {
		s.finishDeveloperToolJob(request, result, err)
		if !window.IsDestroyed() {
			window.Invalidate()
		}
	}) {
		return
	}
	// Update posts to the MyGO window thread. A superseded/hidden request may
	// finish its cleanup as soon as its context is cancelled; its queued callback
	// is harmless because relevance includes the request pointer and token.
	select {
	case <-updateDone:
	case <-request.ctx.Done():
	case <-request.owner.lifecycle.Closing():
	}
}

func (s *app) finishDeveloperToolJob(request *developerToolJob, result application.ActionResult, err error) {
	if !s.developerToolJobRelevant(request) {
		s.discardDeveloperToolJob(request)
		return
	}
	jobs := s.toolsUnit.state.toolJobs
	delete(jobs.current, request.key)
	if err != nil {
		if errors.Is(err, context.Canceled) && request.ctx.Err() != nil {
			return
		}
		s.applyDeveloperToolJobFailureToState(request.state, request.action, err)
		return
	}
	if request.action.Kind == "tool.json" && payloadString(request.action.Payload, "operation") == "node-value" {
		if jsonNodeCopyMatches(request.state, request.jsonNodeCopy) {
			s.copyTextToClipboard(request.jsonNodeCopy.services, result.Text, result.Message)
		}
		return
	}
	request.state.handleResult(request.action, result)
}

func (s *app) developerToolJobRelevant(request *developerToolJob) bool {
	if request == nil || s.toolsUnit.state.toolJobs != request.owner || request.ctx.Err() != nil ||
		s.toolsUnit.state.tools != request.state || s.snapshot.Home != request.home ||
		request.owner.current[request.key] != request || request.owner.latest[request.key] != request.token {
		return false
	}
	if request.owner.lifecycle.Closed() {
		return false
	}
	if request.action.Kind == "tool.port-stop" {
		return request.state.portStoppingSequence == payloadUint64(request.action.Payload, "stopRequest") &&
			request.state.portStoppingPID == payloadInt(request.action.Payload, "pid")
	}
	return s.page == "tools" && request.state.activeTab == request.tab && s.developerToolTabVisible(request.tab) &&
		developerToolActionInputMatches(request.action, request.state)
}

func (s *app) discardDeveloperToolJob(request *developerToolJob) {
	if request == nil || s.toolsUnit.state.toolJobs == nil || s.toolsUnit.state.toolJobs.current[request.key] != request {
		return
	}
	delete(s.toolsUnit.state.toolJobs.current, request.key)
	s.toolsUnit.state.toolJobs.latest[request.key]++
	if s.toolsUnit.state.tools != request.state {
		return
	}
	switch request.action.Kind {
	case "tool.json":
		if payloadUint64(request.action.Payload, "revision") == request.state.jsonRevision {
			request.state.jsonPending = false
		}
	case "tool.time":
		if payloadUint64(request.action.Payload, "revision") == request.state.timeRevision {
			request.state.timePending = false
		}
	case "tool.diff":
		if payloadUint64(request.action.Payload, "revision") == request.state.diffRevision {
			request.state.diffPending = false
		}
	case "tool.port-stop":
		if request.state.portStoppingSequence == payloadUint64(request.action.Payload, "stopRequest") &&
			request.state.portStoppingPID == payloadInt(request.action.Payload, "pid") {
			request.state.portStoppingSequence = 0
			request.state.portStoppingPID = 0
			request.state.portLastStatus = "UNKNOWN"
			request.state.portLastMessage = "结束请求结果与当前工作区上下文不匹配，请重新查询端口确认。"
		}
	}
}

func developerToolActionInputMatches(action application.Action, state *toolPageState) bool {
	if state == nil {
		return false
	}
	switch action.Kind {
	case "tool.json":
		return payloadUint64(action.Payload, "revision") == state.jsonRevision && action.Text == state.jsonSource
	case "tool.time":
		zone := payloadString(action.Payload, "zone")
		if zone == "" {
			zone = "Asia/Shanghai"
		}
		currentZone := state.timeZone
		if currentZone == "" {
			currentZone = "Asia/Shanghai"
		}
		return payloadUint64(action.Payload, "revision") == state.timeRevision && action.Text == state.timeInput &&
			payloadString(action.Payload, "unit") == state.timeUnit && zone == currentZone
	case "tool.diff":
		left, _ := action.Payload["left"].(string)
		right, _ := action.Payload["right"].(string)
		return payloadUint64(action.Payload, "revision") == state.diffRevision && left == state.diffLeft && right == state.diffRight
	case "tool.ports":
		port := payloadInt(action.Payload, "port")
		filter, err := parseDeveloperToolPort(state.portFilter)
		return err == nil && port == filter && payloadUint64(action.Payload, "request") == state.portRequest &&
			payloadUint64(action.Payload, "filterRevision") == state.portFilterRevision
	default:
		return false
	}
}

func parseDeveloperToolPort(value string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, err
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("端口号必须在 1 到 65535 之间")
	}
	return port, nil
}

func (s *app) applyDeveloperToolJobFailureToState(state *toolPageState, action application.Action, err error) {
	if state == nil || err == nil {
		return
	}
	message := err.Error()
	switch action.Kind {
	case "tool.json":
		if payloadUint64(action.Payload, "revision") == state.jsonRevision {
			state.jsonPending = false
			state.setJSONError(err)
		}
	case "tool.time":
		if payloadUint64(action.Payload, "revision") == state.timeRevision {
			state.timePending = false
			state.timeHasData = false
			state.timeError = message
		}
	case "tool.diff":
		if payloadUint64(action.Payload, "revision") == state.diffRevision {
			state.diffPending = false
			state.diffHasData = false
			state.diffError = message
		}
	case "tool.ports":
		if payloadUint64(action.Payload, "request") == state.portRequest && payloadUint64(action.Payload, "filterRevision") == state.portFilterRevision {
			state.portInputError = message
		}
	case "tool.port-stop":
		if state.portStoppingSequence == payloadUint64(action.Payload, "stopRequest") {
			state.portStoppingPID = 0
			state.portStoppingSequence = 0
			state.portLastStatus = "FAILED"
			state.portLastMessage = message
			state.portConfirm = false
		}
	}
	if s.win != nil && !s.win.IsDestroyed() {
		s.win.Invalidate()
	}
}

func (s *app) cancelDeveloperToolJob(kind string) {
	jobs := s.toolsUnit.state.toolJobs
	if jobs == nil {
		return
	}
	request := jobs.current[kind]
	if request == nil || request.preserveHide {
		return
	}
	delete(jobs.current, kind)
	jobs.latest[kind]++
	request.cancel()
}

func (s *app) developerToolJobBusy(kind string) bool {
	if s.toolsUnit.state.toolJobs == nil {
		return false
	}
	request := s.toolsUnit.state.toolJobs.current[kind]
	return request != nil && request.ctx.Err() == nil
}

func (s *app) toolOperationEnabled(state *toolPageState, kind string) bool {
	if s == nil || state == nil || s.toolsUnit.state.tools != state || s.page != "tools" || state.activeTab != developerToolTabForAction(kind) ||
		!s.developerToolTabVisible(state.activeTab) || s.developerToolJobBusy(kind) {
		return false
	}
	if kind == "tool.json" && state.jsonPending {
		return false
	}
	return true
}

func (s *app) developerToolStatus(tab string) string {
	if s == nil || s.toolsUnit.state.tools == nil {
		return ""
	}
	kind := ""
	switch tab {
	case "JSON":
		kind = "tool.json"
	case "日期时间":
		kind = "tool.time"
	case "文本对比":
		kind = "tool.diff"
	case "端口":
		if s.toolsUnit.state.tools.portStoppingPID != 0 {
			return fmt.Sprintf("正在等待 PID %d 的结束结果…", s.toolsUnit.state.tools.portStoppingPID)
		}
		kind = "tool.ports"
	}
	if kind != "" && s.developerToolJobBusy(kind) {
		return "正在处理…"
	}
	switch tab {
	case "JSON":
		if s.toolsUnit.state.tools.jsonPending {
			return "正在校验…"
		}
	case "日期时间":
		if s.toolsUnit.state.tools.timePending {
			return "正在转换…"
		}
	case "文本对比":
		if s.toolsUnit.state.tools.diffPending {
			return "正在比较…"
		}
	}
	return ""
}

func (s *app) developerToolTabVisible(tab string) bool {
	for _, visible := range s.visibleDeveloperToolTabs() {
		if visible.key == tab {
			return true
		}
	}
	return false
}

func (s *app) cancelDeveloperToolTab(tab string) {
	switch tab {
	case "JSON":
		s.cancelDeveloperToolJob("tool.json")
		if s.toolsUnit.state.tools != nil && s.toolsUnit.state.tools.jsonValidationCancel != nil {
			s.toolsUnit.state.tools.jsonValidationCancel()
			s.toolsUnit.state.tools.jsonValidationCancel = nil
			s.toolsUnit.state.tools.jsonPending = false
		}
		if s.toolsUnit.state.tools != nil {
			s.toolsUnit.state.tools.jsonPending = false
		}
	case "日期时间":
		s.cancelDeveloperToolJob("tool.time")
		if s.toolsUnit.state.tools != nil && s.toolsUnit.state.tools.timeCancel != nil {
			s.toolsUnit.state.tools.timeCancel()
			s.toolsUnit.state.tools.timeCancel = nil
			s.toolsUnit.state.tools.timePending = false
		}
	case "文本对比":
		s.cancelDeveloperToolJob("tool.diff")
		if s.toolsUnit.state.tools != nil && s.toolsUnit.state.tools.diffCancel != nil {
			s.toolsUnit.state.tools.diffCancel()
			s.toolsUnit.state.tools.diffCancel = nil
			s.toolsUnit.state.tools.diffPending = false
		}
	case "端口":
		s.cancelDeveloperToolJob("tool.ports")
	}
}

func (s *app) syncDeveloperToolJobVisibility() {
	jobs := s.ensureDeveloperToolJobs()
	visible := ""
	if s.page == "tools" && s.toolsUnit.state.tools != nil && s.toolsUnit.state.tools.activeTab != "" && s.developerToolTabVisible(s.toolsUnit.state.tools.activeTab) {
		visible = s.toolsUnit.state.tools.activeTab
	}
	if jobs.visibleTab == visible {
		return
	}
	previous := jobs.visibleTab
	jobs.visibleTab = visible
	if previous != "" {
		s.cancelDeveloperToolTab(previous)
	}
	if s.toolsUnit.state.tools == nil || visible == "" {
		return
	}
	switch visible {
	case "JSON":
		if !s.toolsUnit.state.tools.jsonValid && !s.toolsUnit.state.tools.jsonPending && strings.TrimSpace(s.toolsUnit.state.tools.jsonSource) != "" {
			s.queueJSONValidation()
		}
	case "日期时间":
		if strings.TrimSpace(s.toolsUnit.state.tools.timeInput) != "" {
			s.queueTimeConversion()
		}
	case "文本对比":
		if s.toolsUnit.state.tools.diffAuto && (s.toolsUnit.state.tools.diffLeft != "" || s.toolsUnit.state.tools.diffRight != "") {
			s.queueDiff()
		}
	}
}

func (s *toolsController) closeDeveloperToolJobs(ctx context.Context) error {
	jobs := s.state.toolJobs
	if jobs == nil {
		return nil
	}
	jobs.lifecycle.Close()
	jobs.current = make(map[string]*developerToolJob)
	if s.state.tools != nil {
		s.state.tools.cancelWork()
	}
	if err := jobs.lifecycle.Wait(ctx); err != nil {
		return fmt.Errorf("等待研发工具请求清理：%w", err)
	}
	return nil
}
