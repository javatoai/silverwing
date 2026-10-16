package desktop

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/egoist/mygo/ui"

	"silverwing-go/internal/application"
	"silverwing-go/internal/workspace"
)

type taskRootMigrationProgressRequest struct {
	ctx          context.Context
	cancel       context.CancelFunc
	home, config string
	plan         workspace.TaskRootMigrationPreview
}

func (s *app) newTaskRootMigrationProgressRequest(ctx context.Context, action application.Action) *taskRootMigrationProgressRequest {
	if ctx == nil || action.Kind != "migration.apply" {
		return nil
	}
	var plan workspace.TaskRootMigrationPreview
	if err := decodeWorkspaceValue(action.Payload["plan"], &plan); err != nil || plan.TargetRoot == "" {
		return nil
	}
	progressContext, cancel := context.WithCancel(ctx)
	return &taskRootMigrationProgressRequest{ctx: progressContext, cancel: cancel, home: s.snapshot.Home, config: s.configJSON, plan: plan}
}

func (request *taskRootMigrationProgressRequest) finish() {
	if request != nil {
		request.cancel()
	}
}

func (s *app) taskRootMigrationProgressUpdates(ctx context.Context, action application.Action) (func(workspace.TaskRootMigrationProgress), func()) {
	request := s.newTaskRootMigrationProgressRequest(ctx, action)
	win := s.win
	return func(progress workspace.TaskRootMigrationProgress) {
		if request == nil || request.ctx.Err() != nil || win == nil || win.IsDestroyed() {
			return
		}
		win.Update(func() { s.applyTaskRootMigrationProgress(request, progress) })
	}, request.finish
}

func (s *app) applyTaskRootMigrationProgress(request *taskRootMigrationProgressRequest, progress workspace.TaskRootMigrationProgress) {
	if request == nil || request.ctx.Err() != nil || !s.busy || s.operationKind != "migration.apply" ||
		request.home != s.snapshot.Home || request.config != s.configJSON {
		return
	}
	current, err := s.taskRootMigrationPlan()
	if err != nil || !reflect.DeepEqual(current, request.plan) {
		return
	}
	message := taskRootMigrationProgressText(request.plan, progress)
	if message != "" {
		s.migrationPreview = message
		s.operation = message
	}
}

func taskRootMigrationProgressText(plan workspace.TaskRootMigrationPreview, progress workspace.TaskRootMigrationProgress) string {
	phase := map[workspace.TaskRootMigrationPhase]string{
		workspace.MigrationPreparing:                "正在准备任务目录迁移",
		workspace.MigrationTransferringAndVerifying: "正在传输并校验任务",
		workspace.MigrationUpdatingConfig:           "正在切换任务根目录",
		workspace.MigrationCleaningSource:           "正在清理旧任务目录",
		workspace.MigrationCompleted:                "任务目录迁移完成",
	}[progress.Phase]
	if phase == "" {
		return ""
	}
	lines := []string{phase, plan.SourceRoot + " → " + plan.TargetRoot,
		fmt.Sprintf("已完成任务：%d / %d", progress.CompletedTasks, progress.TotalTasks)}
	if progress.CurrentTask != "" {
		lines = append(lines, "当前任务："+progress.CurrentTask)
	}
	return strings.Join(lines, "\n")
}

func (s *app) taskRootMigrationPathInput(c *ui.Context, p palette, value *string) {
	s.taskRootMigrationPathField(c, p, value, true, false)
}

func (s *app) taskRootMigrationPathControl(c *ui.Context, p palette, value *string, stacked bool) {
	s.taskRootMigrationPathField(c, p, value, false, stacked)
}

func (s *app) taskRootMigrationPathField(c *ui.Context, p palette, value *string, showTitle, stacked bool) {
	var input ui.Element
	field := func() {
		if stacked {
			ui.Column(c).FillWidth().MinWidth(0).Gap(6).Disabled(s.busy || s.settingsPickerBusy).Children(func() {
				input = ui.TextInput(c.Key("settings-path-任务根目录"), value).Grow(1).MinWidth(0).FillWidth().Height(36).Placeholder("选择任务存放目录").Label("任务根目录")
				ui.Row(c).FillWidth().Justify(ui.End).Children(func() { s.taskRootMigrationBrowse(c, p, value) })
			})
			return
		}
		ui.Row(c).FillWidth().MinWidth(0).Gap(7).Disabled(s.busy || s.settingsPickerBusy).Children(func() {
			input = ui.TextInput(c.Key("settings-path-任务根目录"), value).Grow(1).MinWidth(0).FillWidth().Height(36).Placeholder("选择任务存放目录").Label("任务根目录")
			s.taskRootMigrationBrowse(c, p, value)
		})
	}
	if showTitle {
		ui.Column(c).Gap(5).Children(func() {
			label(c, "任务根目录", 11, p.text)
			field()
		})
	} else {
		field()
	}
	focused := input.Focused()
	if s.taskRootTargetFocused && !focused && !s.busy && !s.settingsPickerBusy {
		target := strings.TrimSpace(*value)
		if target != "" && workspaceHealthPathKey(target) != workspaceHealthPathKey(s.configString("taskRoot")) {
			s.requestTaskRootChange(target)
		}
	}
	s.taskRootTargetFocused = focused
}

func (s *app) taskRootMigrationBrowse(c *ui.Context, p palette, value *string) {
	if action(c, "settings-browse-任务根目录", "folder", "浏览", p, false) {
		s.pickNativePath("选择任务存放目录", *value, true, nil, func(path string) {
			*value = path
			s.requestTaskRootChange(path)
		})
	}
}

func (s *app) previewTaskRootMigrationTarget(target string) {
	target = strings.TrimSpace(target)
	if target == "" || workspaceHealthPathKey(target) == workspaceHealthPathKey(s.configString("taskRoot")) {
		s.lastError = "请选择与当前目录不同的目标目录。"
		return
	}
	if s.busy || s.settingsPickerBusy {
		return
	}
	s.migrationReady, s.migrationPlan = false, nil
	s.act("migration.preview", "", "", "", target, "", nil)
}

func (s *app) observeTaskRootMigrationNavigation() {
	if !s.taskRootTargetFocused || s.page == "settings" && s.settingsPage == "paths" {
		return
	}
	s.taskRootTargetFocused = false
	target := strings.TrimSpace(*s.input("setting:taskRootTarget", s.configString("taskRoot")))
	if target != "" && workspaceHealthPathKey(target) != workspaceHealthPathKey(s.configString("taskRoot")) {
		s.requestTaskRootChange(target)
	}
}

func (s *app) taskRootMigrationPlan() (workspace.TaskRootMigrationPreview, error) {
	var preview workspace.TaskRootMigrationPreview
	if s.migrationPlan == nil || !s.migrationReady {
		return preview, fmt.Errorf("请先预览任务根目录迁移")
	}
	if err := decodeWorkspaceValue(s.migrationPlan, &preview); err != nil {
		return preview, fmt.Errorf("迁移预览格式错误：%w", err)
	}
	if !preview.CanMigrate || len(preview.Blockers) != 0 || strings.TrimSpace(preview.TargetRoot) == "" {
		return preview, fmt.Errorf("当前迁移预览不可执行，请重新检查")
	}
	if err := s.taskRootMigrationTargetCurrent(preview); err != nil {
		return preview, err
	}
	return preview, nil
}

func (s *app) taskRootMigrationTargetCurrent(preview workspace.TaskRootMigrationPreview) error {
	target := strings.TrimSpace(*s.input("setting:taskRootTarget", s.configString("taskRoot")))
	if target == "" || workspaceHealthPathKey(target) != workspaceHealthPathKey(preview.TargetRoot) {
		return fmt.Errorf("目标目录已变化，请重新预览迁移")
	}
	source := strings.TrimSpace(s.configString("taskRoot"))
	if (source == "") != (preview.SourceRoot == "") || source != "" && workspaceHealthPathKey(source) != workspaceHealthPathKey(preview.SourceRoot) {
		return fmt.Errorf("当前任务根目录已变化，请重新预览迁移")
	}
	return nil
}

func (s *app) confirmTaskRootMigration() {
	preview, err := s.taskRootMigrationPlan()
	if err != nil {
		s.lastError = err.Error()
		return
	}
	s.confirmNative("迁移任务并切换目录？", "silverwing 将迁移任务并校验 Git 状态和任务清单后再更新配置。", preview.SourceRoot+" → "+preview.TargetRoot+"\n迁移期间请关闭这些工作区的 IDE 和终端。", func() { s.applyConfirmedTaskRootMigration(preview) })
}

func (s *app) applyConfirmedTaskRootMigration(confirmed workspace.TaskRootMigrationPreview) {
	if s.busy {
		s.lastError = "当前有操作正在执行，请等待完成后重试。"
		return
	}
	current, err := s.taskRootMigrationPlan()
	if err != nil {
		s.lastError = err.Error()
		return
	}
	if !reflect.DeepEqual(current, confirmed) {
		s.lastError = "迁移预览已变化，请重新确认。"
		return
	}
	// The captured preview determines both the reviewed target and the payload.
	s.act("migration.apply", "", "", "", confirmed.TargetRoot, "", map[string]any{"plan": confirmed, "confirmed": true})
}

func (s *app) applyTaskRootMigrationPreview(action application.Action, result application.ActionResult) {
	var preview workspace.TaskRootMigrationPreview
	if err := decodeWorkspaceValue(result.Data, &preview); err != nil {
		s.migrationPlan = nil
		s.migrationReady = false
		s.migrationPreview = "迁移预览格式错误：" + err.Error()
		return
	}
	if workspaceHealthPathKey(action.Path) != workspaceHealthPathKey(preview.TargetRoot) {
		s.migrationPlan = nil
		s.migrationReady = false
		s.migrationPreview = "迁移预览与请求目标不一致，请重新检查。"
		return
	}
	if err := s.taskRootMigrationTargetCurrent(preview); err != nil {
		s.migrationPlan = nil
		s.migrationReady = false
		s.migrationPreview = err.Error()
		return
	}
	s.migrationPlan = preview
	s.migrationPreview = migrationPreviewText(preview)
	s.migrationReady = preview.CanMigrate
	s.settingsOutput = ""
}
