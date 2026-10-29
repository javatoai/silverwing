package desktop

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// Native toasts keep feedback, progress and cancellation outside page layout.
type operationNotificationState struct {
	shown map[string]string
}

func (s *app) renderOperationNotifications(c *ui.Context) {
	loading := ""
	if s.loading {
		loading = "正在读取工作空间…"
	}
	s.operationNotifications.progress(c, "loading-progress", loading, "", "", nil)
	progress, action := "", ""
	var cancel func()
	if s.busy {
		progress = operationProgressTitle(s.operationKind, s.operation)
		if s.canCancelOperation() {
			// Capture this operation's cancellation function. A late toast action
			// must not cancel a newer operation sharing the same kind.
			action, cancel = "取消", s.cancelOperation
		}
	}
	s.operationNotifications.progress(c, "operation-progress", progress, s.operationKind, action, cancel)
	s.renderRequirementRefreshProgressNotification(c)
	s.renderRequirementListProgressNotification(c)
	s.renderProductionProgressNotifications(c)
	if state := s.workspaceUnit.state.workspaceGit; state != nil && s.workspaceGitObservationCurrent(state.observation) {
		empty := ""
		s.operationNotifications.present(c, "workspace-git-refresh", &empty, &state.failure)
	} else {
		empty, failure := "", ""
		s.operationNotifications.present(c, "workspace-git-refresh", &empty, &failure)
	}
	s.operationNotifications.present(c, "operation", &s.lastMessage, &s.lastError)
	emptyWarningError := ""
	s.operationNotifications.presentWithKind(c, "task-create-warning", &s.taskUnit.state.taskCreationWarning, &emptyWarningError, "warning")
	if state := s.settingsUnit.state.pageLocalRead; state != nil {
		empty := ""
		entry := state.entry
		s.operationNotifications.present(c, "local-read", &empty, &state.failure, func() {
			if state.entry == entry {
				s.retryPageLocalRead()
			}
		})
	}
	s.presentSettingsOutputNotification(c)
	s.renderSystemOpenProgressNotifications(c)
	if jobs := s.systemOpenJobs; jobs != nil {
		s.operationNotifications.present(c, "system-open", &jobs.message, &jobs.error)
	}
}

func (s *app) renderRequirementRefreshProgressNotification(c *ui.Context) {
	title := ""
	if jobs := s.requirementUnit.state.requirementRefreshJobs; jobs != nil {
		identity := s.currentRequirementListIdentity()
		for _, job := range jobs.current {
			if job.ctx.Err() == nil && job.identity == identity {
				title = "刷新中"
				break
			}
		}
	}
	s.operationNotifications.progress(c, "requirement-refresh-progress", title, "", "", nil)
}

func operationProgressTitle(kind, title string) string {
	switch kind {
	case "development-tools/detect":
		return "正在检测开发工具…"
	case "development-tools/snapshot", "codex.extensions/snapshot", "codex.extensions/mcp/status",
		"skills.sources/catalog", "instructions.templates.list", "config.backups", "config.view",
		"git.environment", "git.environment/snapshot", "logs.list", "logs.read", "terminal.resolve":
		return "" // Routine local reads update their pages silently.
	}
	if title == "" || title == kind {
		return "正在处理…"
	}
	return title
}

func (s *operationNotificationState) progress(c *ui.Context, scope, title, identity, action string, cancel func()) {
	if s.shown == nil {
		s.shown = make(map[string]string)
	}
	id := "silverwing-notification-" + scope
	if title == "" {
		if s.shown[scope] != "" {
			delete(s.shown, scope)
			c.CloseToast(id)
		}
		return
	}
	key := identity + "\x00" + title + "\x00" + action
	if s.shown[scope] == key {
		return
	}
	s.shown[scope] = key
	c.AddToast(ui.Toast{ID: id, Title: title, Type: "info", Timeout: -1, Action: action, OnAction: cancel})
}

func (s *app) renderSystemOpenProgressNotifications(c *ui.Context) {
	requests := map[string]*systemOpenJob{}
	if jobs := s.systemOpenJobs; jobs != nil {
		jobs.mu.Lock()
		for key, request := range jobs.current {
			requests[key] = request
		}
		if jobs.opening != nil {
			requests[jobs.opening.key] = jobs.opening
		}
		jobs.mu.Unlock()
	}
	keys := make([]string, 0, len(requests))
	for key := range requests {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	active := map[string]bool{}
	for _, key := range keys {
		request := requests[key]
		if request == nil {
			continue
		}
		scope := "system-progress-" + key
		active[scope] = true
		title := "正在交给系统打开…"
		switch key {
		case "reveal":
			title = "正在定位文件…"
		case "link":
			title = "正在打开链接…"
		case "directory":
			title = "正在打开目录…"
		case "terminal":
			title = "正在打开系统终端…"
		case "ide":
			title = "正在打开开发工具…"
		default:
			if strings.HasPrefix(key, "cli-terminal:") {
				title = "正在打开 CLI 终端…"
			}
		}
		action := "停止等待"
		if request.ctx.Err() != nil {
			title, action = "正在等待系统操作结束…", ""
		}
		s.operationNotifications.progress(c, scope, title, fmt.Sprintf("%p", request), action, func() {
			jobs := s.systemOpenJobs
			if jobs == nil {
				return
			}
			jobs.mu.Lock()
			current := jobs.current[key] == request || jobs.opening == request
			jobs.mu.Unlock()
			if current {
				s.cancelSystemOpenJob(key)
			}
		})
	}
	for scope := range s.operationNotifications.shown {
		if strings.HasPrefix(scope, "system-progress-") && !active[scope] {
			s.operationNotifications.progress(c, scope, "", "", "", nil)
		}
	}
}

func settingsOutputNotificationSummary(output string) (string, string) {
	output = strings.TrimSpace(output)
	if output == "" || strings.HasPrefix(output, "{") || strings.HasPrefix(output, "[") || strings.HasPrefix(output, "```") {
		return "", ""
	}
	lines := strings.Split(output, "\n")
	title, kind := strings.TrimSpace(lines[0]), "success"
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "失败") || strings.HasPrefix(line, "错误：") || strings.HasPrefix(line, "检测错误：") {
			title, kind = line, "error"
			break
		}
	}
	if kind == "success" {
		if readCompleteNotification(title) {
			return "", ""
		}
		for _, prefix := range []string{"工具：", "命令：", "版本：", "当前版本：", "日志文件："} {
			if strings.HasPrefix(title, prefix) {
				return "", ""
			}
		}
	}
	runes := []rune(title)
	if len(runes) > 120 {
		title = string(runes[:120]) + "…"
	}
	return title, kind
}

func readCompleteNotification(message string) bool {
	return strings.HasSuffix(strings.TrimRight(strings.TrimSpace(message), "。.!！…"), "已读取")
}

func (s *app) presentSettingsOutputNotification(c *ui.Context) {
	const scope, id = "settings-output", "silverwing-notification-settings-output"
	output := s.settingsUnit.state.settingsOutput
	title, kind := settingsOutputNotificationSummary(output)
	if output == s.lastMessage || output == s.lastError {
		title = ""
	}
	if title == "" {
		if s.operationNotifications.shown[scope] != "" {
			delete(s.operationNotifications.shown, scope)
			c.CloseToast(id)
		}
		return
	}
	key := kind + "\x00" + output
	if s.operationNotifications.shown[scope] == key {
		return
	}
	s.operationNotifications.shown[scope] = key
	timeout := 4 * time.Second
	if kind == "error" {
		timeout = -1
	}
	c.AddToast(ui.Toast{ID: id, Title: title, Type: kind, Timeout: timeout, Action: "关闭", OnClose: func() {
		if s.settingsUnit.state.settingsOutput == output {
			s.settingsUnit.state.settingsOutput = ""
		}
		if s.operationNotifications.shown[scope] == key {
			delete(s.operationNotifications.shown, scope)
		}
	}})
}

func (s *operationNotificationState) present(c *ui.Context, scope string, message, failure *string, retry ...func()) {
	s.presentWithKind(c, scope, message, failure, "success", retry...)
}
func (s *operationNotificationState) presentWithKind(c *ui.Context, scope string, message, failure *string, messageKind string, retry ...func()) {
	if s.shown == nil {
		s.shown = make(map[string]string)
	}
	id := "silverwing-notification-" + scope
	text, kind, source := *message, messageKind, message
	if readCompleteNotification(text) {
		text = ""
	}
	timeout := 4 * time.Second
	if messageKind == "warning" {
		timeout = -1
	}
	if *failure != "" {
		text, kind, source = *failure, "error", failure
		timeout = -1 // Errors remain available until dismissed.
	}
	if text == "" {
		if s.shown[scope] != "" {
			s.shown[scope] = ""
			c.CloseToast(id)
		}
		return
	}
	key := kind + "\x00" + text
	if s.shown[scope] == key {
		return
	}
	s.shown[scope] = key
	action := "关闭"
	var onAction func()
	if kind == "error" && len(retry) > 0 {
		action, onAction = "重试", retry[0]
	}
	toast := ui.Toast{ID: id, Title: text, Type: kind, Timeout: timeout, Action: action, OnAction: onAction, OnClose: func() {
		// A late close must not discard a newer operation's result.
		if *source == text {
			*source = ""
		}
		if s.shown[scope] == key {
			s.shown[scope] = ""
		}
	}}
	if kind == "warning" {
		toast.ContentMaxHeight = 360
	}
	if scope == "operation" && kind == "success" && isTagReleaseNoticeCopiedMessage(text) {
		toast.ContentMaxHeight = 360
	}
	c.AddToast(toast)
}
