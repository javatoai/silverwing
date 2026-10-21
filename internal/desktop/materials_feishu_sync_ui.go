package desktop

import (
	"fmt"
	"path/filepath"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo/ui"
)

func (s *app) renderMaterialsFeishuSyncEntry(c *ui.Context, p palette, t task) {
	if t.id == "" {
		return
	}
	jobs := s.ensureMaterialsFeishuSyncJobs()
	jobs.mu.Lock()
	active := jobs.active
	directoryJob := jobs.directory
	jobs.mu.Unlock()
	key := materialsFeishuSyncTaskKey(t)
	result := jobs.unread[key]
	if active != nil {
		result = active
	} else if current := s.currentMaterialsFeishuSyncSession(); current != nil && current.TaskKey == key {
		result = current
	}
	canReveal := active != nil || result != nil
	target, targetErr := s.materialsFeishuSyncTarget()
	canStart := t.materialsReady() && targetErr == nil
	showSync := canReveal || canStart || active != nil
	showOpenDirectory := targetErr == nil
	if !showSync && !showOpenDirectory {
		return
	}
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		if showSync {
			labelText, iconName, disabled := "同步到飞书", "cloud", false
			if active != nil {
				labelText, iconName = "查看同步进度", "refresh"
			} else if canReveal {
				labelText, iconName = "查看同步", "refresh"
				if result != nil && result.Stage == materialsFeishuSyncFinished {
					labelText, iconName = "查看同步结果", "check"
				}
			} else if !canStart {
				disabled = true
			}
			if materialsFeishuSyncEntryButton(c, "materials-feishu-sync-entry", iconName, labelText, p, disabled) {
				if canReveal {
					s.revealMaterialsFeishuSync(result)
				} else {
					s.startMaterialsFeishuSync(t)
				}
			}
		}
		if showOpenDirectory {
			opening := materialsFeishuDirectoryEntryOpening(directoryJob, key, target)
			if materialsFeishuSyncEntryButton(c, "materials-feishu-open-directory", "folder", map[bool]string{true: "正在打开…", false: "打开飞书目录"}[opening], p, opening) {
				s.openMaterialsFeishuDirectory(t, target)
			}
		}
	})
}

func (s *app) materialsRootContextMenu(menu *ui.Menu, t task) {
	s.materialsRootFeishuMenuItems(&overflowMenuBuilder{native: menu}, t)
}

// Both the root context menu and tree header operate on the entire task.
func (s *app) materialsRootFeishuMenuItems(menu *overflowMenuBuilder, t task) {
	if t.id == "" || !t.materialsReady() {
		return
	}
	target, targetErr := s.materialsFeishuSyncTarget()
	jobs := s.ensureMaterialsFeishuSyncJobs()
	jobs.mu.Lock()
	directoryJob := jobs.directory
	active := jobs.active
	closed := jobs.closed
	jobs.mu.Unlock()
	busy := s.backend == nil || s.win == nil || s.win.IsDestroyed() || targetErr != nil || closed
	key := materialsFeishuSyncTaskKey(t)
	previous := s.currentMaterialsFeishuSyncSession()
	if previous == nil || previous.TaskKey != key || previous.Stage != materialsFeishuSyncFinished {
		previous = jobs.unread[key]
		if previous != nil && previous.Stage != materialsFeishuSyncFinished {
			previous = nil
		}
	}
	if active == nil {
		if menu.Item("sync-all-materials", "同步全部资料到飞书").Icon("cloud").Disabled(busy || s.materialsFileInputBusy()).Chosen() {
			s.startMaterialsFeishuSync(t)
		}
	} else {
		labelText := "查看同步进度"
		if active.TaskKey != materialsFeishuSyncTaskKey(t) {
			labelText = "其他任务正在同步"
		} else if active.Stage == materialsFeishuSyncFinished {
			labelText = "查看上次同步结果"
		}
		if menu.Item("reveal-sync", labelText).Icon("cloud").Disabled(active.TaskKey != materialsFeishuSyncTaskKey(t) || closed).Chosen() {
			s.revealMaterialsFeishuSync(active)
		}
	}
	if previous != nil && previous != active {
		if menu.Item("last-sync-result", "查看上次同步结果").Icon("file").Disabled(active != nil).Chosen() {
			s.revealMaterialsFeishuSync(previous)
		}
	}
	opening := materialsFeishuDirectoryEntryOpening(directoryJob, materialsFeishuSyncTaskKey(t), target)
	if menu.Item("open-feishu-root", "打开飞书资料根目录").Icon("folder").Disabled(busy || opening).Chosen() {
		target, err := s.materialsFeishuSyncTarget()
		if err != nil {
			s.lastError = "飞书同步目标已变化，请刷新后重试。"
			return
		}
		s.openMaterialsFeishuDirectory(t, target)
	}
}

func (s *app) materialsFileContextFeishuActions(menu *ui.Menu, t task, entry materialsFileSelectionEntry) {
	if entry.Directory || entry.Path == "" || entry.Relative == "" || t.id == "" {
		return
	}
	target, targetErr := s.materialsFeishuSyncTarget()
	jobs := s.ensureMaterialsFeishuSyncJobs()
	jobs.mu.Lock()
	directoryJob := jobs.directory
	active := jobs.active
	closed := jobs.closed
	jobs.mu.Unlock()
	opening := directoryJob != nil && directoryJob.taskKey == materialsFeishuSyncTaskKey(t) &&
		directoryJob.targetURL == target && directoryJob.relativePath == entry.Relative
	openDisabled := targetErr != nil || s.backend == nil || s.win == nil || s.win.IsDestroyed() || closed || opening
	if menu.Item("打开飞书在线文档").Icon(icons["launch"]).Disabled(openDisabled).Chosen() {
		s.openMaterialsFeishuEntry(t, target, entry.Relative)
	}
	syncDisabled := targetErr != nil || s.backend == nil || s.win == nil || s.win.IsDestroyed() || closed || active != nil
	if menu.Item("同步此文件及父级到飞书").Icon(icons["cloud"]).Disabled(syncDisabled || s.materialsFileInputBusy()).Chosen() {
		s.startMaterialsFeishuSyncFile(t, entry)
	}
}

func materialsFeishuDirectoryEntryOpening(job *materialsFeishuDirectoryJob, taskKey, target string) bool {
	return job != nil && job.taskKey == taskKey && job.targetURL == target && job.relativePath == ""
}

func materialsFeishuSyncEntryButton(c *ui.Context, key, iconName, text string, p palette, disabled bool) bool {
	foreground := p.text
	if disabled {
		foreground = p.muted
	}
	button := ui.ButtonBase(c.Key(key)).Row().Gap(7).Height(34).Padding(0, 16).Radius(20).
		AlignItems(ui.Center).TextColor(foreground).Label(text).Disabled(disabled).Background(p.surface).Border(1, p.line)
	if button.Hovered() && !disabled {
		button.Background(p.soft)
	}
	button.Children(func() {
		if iconName != "" {
			icon(c, iconName, 17, foreground)
		}
		label(c, text, 13, foreground).NoWrap()
	})
	return button.Clicked()
}

func (s *app) currentMaterialsFeishuSyncSession() *materialsFeishuSyncSession {
	if s.materialsFeishuSyncJobs == nil {
		return nil
	}
	return s.materialsFeishuSyncJobs.current
}

func (s *app) startMaterialsFeishuSync(t task) {
	s.startMaterialsFeishuSyncPaths(t, nil)
}

func (s *app) startMaterialsFeishuSyncFile(t task, entry materialsFileSelectionEntry) {
	if s.materialsFeishuSyncActive() {
		s.lastError = "另一项飞书资料同步仍在进行，请完成后重试。"
		if s.win != nil {
			s.win.Invalidate()
		}
		return
	}
	if entry.Directory || entry.Path == "" || entry.Relative == "" || t.id == "" ||
		materialsFeishuSyncTaskKey(t) != materialsFeishuSyncTaskKey(s.currentTask()) ||
		normalizedReadingRoot(t.materialsPath) != normalizedReadingRoot(s.currentTask().materialsPath) ||
		s.materialsRelativePath(entry.Path) != entry.Relative {
		s.lastError = "所选资料已变化，请刷新后重试。"
		if s.win != nil {
			s.win.Invalidate()
		}
		return
	}
	absolute := filepath.Join(t.materialsPath, filepath.FromSlash(entry.Relative))
	resolved, ok := s.findMaterialsEntry(filepath.Clean(absolute), s.documents)
	if !ok || resolved.Directory || resolved.Relative != entry.Relative || normalizedReadingPath(resolved.Path) != normalizedReadingPath(entry.Path) {
		s.lastError = "所选文件不在当前任务资料目录中。"
		if s.win != nil {
			s.win.Invalidate()
		}
		return
	}
	s.startMaterialsFeishuSyncPaths(t, []string{entry.Relative})
}

func (s *app) startMaterialsFeishuSyncPaths(t task, selectedPaths []string) {
	if s.materialsFeishuSyncHasUnsavedDraft(t) {
		s.lastError = materialsFeishuUnsavedDraftMessage
		if s.win != nil {
			s.win.Invalidate()
		}
		return
	}
	payload, err := s.materialsFeishuSyncStartPayload(t)
	if err != nil {
		s.lastError = err.Error()
		if s.win != nil {
			s.win.Invalidate()
		}
		return
	}
	if len(selectedPaths) > 0 {
		payload["selectedPaths"] = append([]string(nil), selectedPaths...)
	}
	s.dispatch(application.Action{Kind: "materials.sync", TaskID: t.id, Payload: payload})
}

func materialsFeishuSelectedPaths(payload map[string]any) ([]string, error) {
	value, exists := payload["selectedPaths"]
	if !exists || value == nil {
		return nil, nil
	}
	paths := make([]string, 0)
	switch items := value.(type) {
	case []string:
		paths = append(paths, items...)
	case []any:
		for _, item := range items {
			path, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("selected path is not text")
			}
			paths = append(paths, path)
		}
	default:
		return nil, fmt.Errorf("selected paths are not a list")
	}
	if len(paths) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(paths))
	for _, relative := range paths {
		if relative == "" || relative != strings.TrimSpace(relative) || strings.ContainsAny(relative, `\:`) || strings.HasPrefix(relative, "/") ||
			filepath.IsAbs(filepath.FromSlash(relative)) || filepath.VolumeName(filepath.FromSlash(relative)) != "" {
			return nil, fmt.Errorf("selected path is not a canonical relative path")
		}
		for _, segment := range strings.Split(relative, "/") {
			if segment == "" || segment == "." || segment == ".." {
				return nil, fmt.Errorf("selected path contains a non-canonical segment")
			}
		}
		if _, duplicate := seen[relative]; duplicate {
			return nil, fmt.Errorf("selected paths contain duplicates")
		}
		seen[relative] = struct{}{}
	}
	return paths, nil
}

func (s *app) validateMaterialsFeishuSelectedPaths(t task, paths []string) error {
	if len(paths) == 0 || len(paths) > 1 || t.id == "" || !t.materialsReady() ||
		materialsFeishuSyncTaskKey(t) != materialsFeishuSyncTaskKey(s.currentTask()) ||
		normalizedReadingRoot(t.materialsPath) != normalizedReadingRoot(s.currentTask().materialsPath) {
		return fmt.Errorf("selected path task scope changed")
	}
	root := filepath.Clean(t.materialsPath)
	for _, relative := range paths {
		absolute := filepath.Clean(filepath.Join(root, filepath.FromSlash(relative)))
		if s.materialsRelativePath(absolute) != relative {
			return fmt.Errorf("selected path escaped the materials root")
		}
		entry, ok := s.findMaterialsEntry(absolute, s.documents)
		if !ok || entry.Directory || entry.Relative != relative {
			return fmt.Errorf("selected path is not a current materials file")
		}
	}
	return nil
}

func (s *app) materialsFeishuSyncStartPayload(t task) (map[string]any, error) {
	if t.id == "" || !t.materialsReady() {
		return nil, fmt.Errorf("当前任务没有可同步的需求资料目录")
	}
	if s.materialsFeishuSyncHasUnsavedDraft(t) {
		return nil, fmt.Errorf("%s", materialsFeishuUnsavedDraftMessage)
	}
	link := strings.TrimSpace(t.requirementURL)
	target, err := s.materialsFeishuSyncTarget()
	if err != nil {
		return nil, fmt.Errorf("请先配置有效的飞书资料同步目标：%w", err)
	}
	root := filepath.Clean(strings.TrimSpace(t.materialsPath))
	id, title, err := materialsFeishuRequirementIdentity(t)
	if err != nil {
		return nil, fmt.Errorf("当前任务缺少有效的需求编号或资料目录名称")
	}
	subdirectory := filepath.Base(root)
	services := make([]map[string]any, 0, len(t.workspaces))
	for _, workspace := range t.workspaces {
		name := strings.TrimSpace(workspace.Name)
		if name == "" {
			name = strings.TrimSpace(workspace.ServiceID)
		}
		branch := strings.TrimSpace(workspace.Branch)
		if name != "" && branch != "" {
			services = append(services, map[string]any{"serviceName": name, "branch": branch})
		}
	}
	return map[string]any{
		"targetUrl": target, "writeRoot": root, "requirementId": id,
		"requirementTitle": title, "subdirectory": subdirectory,
		"requirementLink": link, "taskPath": filepath.Clean(t.path),
		"taskCreatedAt": t.createdAt, "taskGroupId": t.groupID,
		"taskTitle": t.title, "taskRequirementId": t.requirementID,
		"materialsRoot": root, "services": services,
	}, nil
}

const materialsFeishuUnsavedDraftMessage = "请先保存任务资料中的 Markdown 修改，再同步飞书"

func (s *app) materialsFeishuSyncTarget() (string, error) {
	config, err := s.readCurrentAppConfig()
	if err != nil {
		return "", fmt.Errorf("无法读取当前飞书资料同步配置")
	}
	target := ""
	if config.RequirementMaterialsFeishuTarget != nil {
		target = strings.TrimSpace(*config.RequirementMaterialsFeishuTarget)
	}
	if target == "" {
		return "", fmt.Errorf("尚未配置飞书资料同步目标")
	}
	if _, err := domain.ValidateMaterialsFeishuTarget(target); err != nil {
		return "", err
	}
	return target, nil
}

func materialsFeishuRequirementIdentity(t task) (string, string, error) {
	id := strings.TrimSpace(t.requirementID)
	if id == "" {
		id = domain.ExtractRequirementNumber(strings.TrimSpace(t.requirementURL))
	}
	folder := strings.TrimSpace(t.title)
	if id == "" || folder == "" {
		return "", "", fmt.Errorf("missing requirement identity")
	}
	return id, id + "-" + folder, nil
}

func (s *app) materialsFeishuCommentAvailable(t task) bool {
	link := domain.ParseFeishuWorkItemLink(strings.TrimSpace(t.requirementURL))
	if link == nil {
		return false
	}
	if link.ProjectKey != nil && strings.TrimSpace(*link.ProjectKey) != "" {
		return true
	}
	config, err := s.readCurrentAppConfig()
	if err != nil {
		return false
	}
	for _, project := range config.MeegleProjects {
		if strings.EqualFold(strings.TrimSpace(project.SimpleName), link.Space) && strings.TrimSpace(project.ProjectKey) != "" {
			return true
		}
	}
	return false
}

func (s *app) materialsFeishuSyncHasUnsavedDraft(t task) bool {
	if t.id == "" {
		return false
	}
	root, ok := absoluteCleanPath(t.materialsPath)
	if !ok {
		return false
	}
	isMarkdownInTask := func(path string) bool {
		if documentKindForPath(path) != "markdown" {
			return false
		}
		absolute, valid := absoluteCleanPath(path)
		if !valid {
			return false
		}
		_, contained := pathRelativeToRoot(root, absolute)
		return contained
	}
	for path, dirty := range s.documentDirtyMap {
		if dirty && isMarkdownInTask(path) {
			return true
		}
	}
	for path := range s.documentDrafts {
		if isMarkdownInTask(path) {
			return true
		}
	}
	if s.currentTask().id == t.id && s.documentDirty && isMarkdownInTask(s.documentPath) {
		return true
	}
	if s.markdownAutosave == nil {
		return false
	}
	s.documentSaveMu.Lock()
	refs := make(map[string]documentSaveRef, len(s.documentSaveRefs))
	for key, ref := range s.documentSaveRefs {
		refs[key] = ref
	}
	s.documentSaveMu.Unlock()
	for key, ref := range refs {
		if ref.kind == "instruction" || ref.taskID != t.id ||
			normalizeIdentityPath(ref.taskPath) != normalizeIdentityPath(t.path) ||
			normalizeIdentityPath(ref.materialsRoot) != normalizeIdentityPath(t.materialsPath) ||
			!isMarkdownInTask(ref.path) {
			continue
		}
		state := s.markdownAutosave.Status(key)
		if state.Dirty || state.Saving || state.Composing {
			return true
		}
	}
	return false
}

func (s *app) revealMaterialsFeishuSync(session *materialsFeishuSyncSession) {
	if session == nil || s.materialsFeishuSyncJobs == nil {
		return
	}
	jobs := s.materialsFeishuSyncJobs
	jobs.mu.Lock()
	if jobs.active != nil {
		session = jobs.active
	}
	jobs.mu.Unlock()
	session.Visible = true
	jobs.current = session
	delete(jobs.unread, session.TaskKey)
	if s.win != nil {
		s.win.Invalidate()
	}
}

func (s *app) hideMaterialsFeishuSync(session *materialsFeishuSyncSession) {
	if session == nil {
		return
	}
	session.Visible = false
	if session.Stage == materialsFeishuSyncFinished && s.materialsFeishuSyncJobs != nil {
		s.materialsFeishuSyncJobs.unread[session.TaskKey] = session
	}
	if s.win != nil {
		s.win.Invalidate()
	}
}

func (s *app) dismissMaterialsFeishuSyncDialog(session *materialsFeishuSyncSession) {
	if session == nil {
		return
	}
	if session.Stage != materialsFeishuSyncFinished {
		s.hideMaterialsFeishuSync(session)
		return
	}
	session.Visible = false
	if jobs := s.materialsFeishuSyncJobs; jobs != nil {
		if jobs.current == session {
			jobs.current = nil
		}
		if jobs.unread[session.TaskKey] == session {
			delete(jobs.unread, session.TaskKey)
		}
	}
	if s.win != nil {
		s.win.Invalidate()
	}
}

func (s *app) renderMaterialsFeishuSyncDialogs(c *ui.Context, p palette) {
	session := s.currentMaterialsFeishuSyncSession()
	if session == nil || !session.Visible {
		return
	}
	open := true
	dialog := ui.DialogBase(c.Key("materials-feishu-sync-dialog-"+session.RunID), &open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .38))
		width, _ := c.Size()
		panel.Width(maxFloat32(440, minFloat32(720, width-64))).MaxHeight(760).Padding(20).Gap(12).Background(p.surface).Border(1, p.line).Radius(14)
		panel.OnShortcut(0, ui.KeyEscape, func() { s.dismissMaterialsFeishuSyncDialog(session) })
		panel.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
				icon(c, "cloud", 19, p.blue)
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					title := "同步需求资料到飞书"
					if len(session.Request.SelectedPaths) > 0 {
						title = "同步所选资料到飞书"
					}
					label(c, title, 16, p.text).FontWeight(600)
					label(c, session.TaskTitle+" · "+materialsFeishuSyncStageLabel(session.Stage), 11, p.muted).SingleLine()
					if len(session.Request.SelectedPaths) > 0 {
						selectedPath := session.Request.SelectedPaths[0]
						label(c, selectedPath, 10, p.muted).SingleLine().Tooltip(selectedPath)
					}
				})
				if session.Stage != materialsFeishuSyncFinished && action(c, "materials-feishu-sync-hide", "close", "隐藏并继续", p, false) {
					s.hideMaterialsFeishuSync(session)
				}
			})
			ui.Divider(c)
			s.renderMaterialsFeishuSyncBody(c, p, session)
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				ui.Spacer(c)
				if session.Stage != materialsFeishuSyncFinished && session.Stage != materialsFeishuSyncStopping {
					if action(c, "materials-feishu-sync-stop", "close", "停止同步", p, false) {
						s.stopMaterialsFeishuSync(session.RunID)
					}
				}
				if session.Stage == materialsFeishuSyncWaitingApproval {
					if action(c, "materials-feishu-sync-approve", "cloud", "确认并上传", p, true) {
						s.approveMaterialsFeishuSync(session.RunID)
					}
				} else if session.Stage == materialsFeishuSyncFinished {
					if session.Upload != nil && !session.Upload.Partial() && session.CommentResult != nil &&
						(session.CommentResult.Status == domain.MaterialsCommentFailed || session.CommentResult.Status == domain.MaterialsCommentCancelled) &&
						action(c, "materials-feishu-sync-retry-comment", "refresh", "仅重试评论", p, true) {
						s.retryMaterialsFeishuSyncComment(session.RunID)
					}
					if action(c, "materials-feishu-sync-finished", "check", "完成", p, false) {
						s.dismissMaterialsFeishuSyncDialog(session)
					}
				}
			})
		})
	})
	if dialog.Dismissed() && session.Visible {
		s.dismissMaterialsFeishuSyncDialog(session)
	}
}

func (s *app) renderMaterialsFeishuSyncBody(c *ui.Context, p palette, session *materialsFeishuSyncSession) {
	switch session.Stage {
	case materialsFeishuSyncPlanning:
		label(c, "正在读取本地资料与飞书版本，生成只读计划…", 13, p.muted).Wrap()
	case materialsFeishuSyncWaitingApproval:
		s.renderMaterialsFeishuSyncApproval(c, p, session)
	case materialsFeishuSyncUploading:
		label(c, materialsFeishuSyncProgressText(session.Progress), 14, p.text).FontWeight(500)
		if session.Progress.CurrentPath != "" {
			label(c, session.Progress.CurrentPath, 11, p.muted).SingleLine().Tooltip(session.Progress.CurrentPath)
		}
		if session.Progress.RetryCount > 0 {
			label(c, fmt.Sprintf("自动重试 %d 次；等待中的操作 %d 项", session.Progress.RetryCount, len(session.Progress.WaitingRetries)), 11, p.muted)
		}
	case materialsFeishuSyncPublishing:
		label(c, "资料已上传，正在发布需求评论…", 13, p.muted)
	case materialsFeishuSyncStopping:
		label(c, "正在停止并等待当前操作完成清理…", 13, p.amber).Wrap()
	case materialsFeishuSyncFinished:
		if session.Summary != "" {
			label(c, session.Summary, 14, p.text).Wrap()
		}
		if session.Error != "" {
			label(c, session.Error, 12, p.amber).Wrap()
		}
		if session.Upload != nil {
			label(c, materialsFeishuSyncProgressText(session.Upload.Progress), 12, p.muted).Wrap()
			for _, message := range session.Upload.Messages {
				label(c, message, 11, p.muted).Wrap()
			}
		}
		if session.CommentResult != nil {
			label(c, materialsFeishuSyncCommentText(*session.CommentResult), 12, materialsFeishuSyncCommentColor(p, session.CommentResult.Status)).Wrap()
			if session.DirectoryURL != "" {
				label(c, session.DirectoryURL, 11, p.blue).SingleLine().Tooltip(session.DirectoryURL)
			}
		}
	}
}

func (s *app) renderMaterialsFeishuSyncApproval(c *ui.Context, p palette, session *materialsFeishuSyncSession) {
	if session.Plan == nil {
		label(c, "同步计划尚未返回。", 12, p.amber)
		return
	}
	label(c, "请审核本次远端删除与覆盖操作。默认不勾选，只有勾选的项目会执行。", 12, p.muted).Wrap()
	items := make([]domain.MaterialsSyncItem, 0)
	for _, item := range session.Plan.Items {
		if item.Action == domain.MaterialsSyncDelete || item.Action == domain.MaterialsSyncConflict {
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		label(c, "计划不包含删除或覆盖冲突。", 12, p.green)
		return
	}
	ui.Scroll(c).FillWidth().Height(minFloat32(420, float32(52*len(items)+16))).Gap(5).Children(func() {
		for index, item := range items {
			path := item.Path
			deletion := item.Action == domain.MaterialsSyncDelete
			selectable := deletion || domain.MaterialsSyncItemCanOverwrite(item)
			checked := session.DeletionPaths[path]
			if !deletion {
				checked = session.OverwritePaths[path]
			}
			if !selectable {
				checked = false
				session.OverwritePaths[path] = false
			}
			kind := "覆盖冲突"
			if deletion {
				kind = "删除"
			} else if !selectable {
				kind = "保留"
			}
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(9).Padding(8).Background(p.soft).Radius(8).Children(func() {
				ui.Checkbox(c.Key(fmt.Sprintf("materials-sync-approval-%d", index)), &checked, "").Shrink(0).
					Label(kind + " " + path).Disabled(!selectable).OnChange(func() {
					if !selectable {
						return
					}
					if deletion {
						session.DeletionPaths[path] = checked
					} else {
						session.OverwritePaths[path] = checked
					}
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
					label(c, kind, 11, p.amber).FontWeight(600)
					label(c, path, 12, p.text).Wrap()
					if item.Reason != nil && strings.TrimSpace(*item.Reason) != "" {
						label(c, *item.Reason, 10, p.muted).Wrap()
					}
				})
			})
		}
	})
}

func materialsFeishuSyncStageLabel(stage materialsFeishuSyncStage) string {
	switch stage {
	case materialsFeishuSyncPlanning:
		return "正在生成计划"
	case materialsFeishuSyncWaitingApproval:
		return "等待确认"
	case materialsFeishuSyncUploading:
		return "正在上传"
	case materialsFeishuSyncPublishing:
		return "正在发布评论"
	case materialsFeishuSyncStopping:
		return "正在停止"
	case materialsFeishuSyncFinished:
		return "已结束"
	default:
		return string(stage)
	}
}

func materialsFeishuSyncProgressText(progress domain.MaterialsSyncProgress) string {
	text := fmt.Sprintf("已完成 %d / %d 个文件", progress.CompletedFiles, progress.TotalFiles)
	return fmt.Sprintf("%s · 新增 %d · 更新 %d · 删除 %d · 跳过 %d · 冲突 %d · 失败 %d", text, progress.Added, progress.Updated, progress.Deleted, progress.Skipped, progress.Conflicts, progress.Failed)
}

func materialsFeishuSyncCommentText(result domain.MaterialsCommentResult) string {
	if strings.TrimSpace(result.Message) != "" {
		return string(result.Status) + " · " + result.Message
	}
	return string(result.Status)
}

func materialsFeishuSyncCommentColor(p palette, status domain.MaterialsCommentStatus) ui.Color {
	switch status {
	case domain.MaterialsCommentCreated, domain.MaterialsCommentUpdated, domain.MaterialsCommentUnchanged:
		return p.green
	case domain.MaterialsCommentFailed, domain.MaterialsCommentCancelled:
		return p.amber
	default:
		return p.muted
	}
}
