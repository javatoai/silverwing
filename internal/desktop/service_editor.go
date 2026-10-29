package desktop

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"silverwing-go/internal/application"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type serviceEditorState struct {
	session, section, groupID, serviceID string
	service                              map[string]any
	modules                              []serviceModuleDraft
	copies                               []copyRuleDraft
	commands                             []bootstrapCommandDraft
	initialText                          map[string]string
	initialBools                         map[string]bool
	initialModules, initialCopies        []string
	initialCommands                      []string
	expandedModules                      map[string]bool
	expandedTags, expandedCommands       map[string]bool
	error                                string
	saving                               bool
	remotes, branches                    []string
	activeRemote                         string
	choicesRequest                       string
	choicesLoading                       bool
	choicesError                         string
	remotesLoaded                        bool
	branchChoices                        map[string][]string
	branchChoicesLoaded                  map[string]bool
	branchChoicesFailed                  map[string]bool
	pendingChoiceRemotes                 []string
	repositoryScope                      serviceRepositoryChoiceScope
	repositoryScopeSet                   bool
	remoteAddresses                      []map[string]any
	addressesRequest                     string
	addressesLoading, addressesLoaded    bool
	addressesError                       string
	copiesMode, commandsMode             string
	exampleOpen                          bool
	exampleTitle, exampleJSON            string
}

type serviceModuleDraft struct {
	raw                                     map[string]any
	id, name, strategy, masterBranch        string
	tagMode, tagTargetRef, tagMessagePrefix string
	commands                                []workspaceCommandDraft
}

type workspaceCommandDraft struct {
	raw                                                          map[string]any
	id, name, iconKey, executable, arguments, directory, timeout string
}

type copyRuleDraft struct {
	raw                map[string]any
	id, source, target string
}

type bootstrapCommandDraft struct {
	raw                                        map[string]any
	id, name, executable, arguments, directory string
	timeout                                    string
}

var serviceEditorSections = []struct{ id, title string }{
	{"basic", "基本信息"}, {"tools", "开发工具"}, {"git", "Git"}, {"genbu", "Genbu"},
	{"modules", "工作区模块"}, {"copies", "复制规则"}, {"commands", "初始化命令"},
}

func (s *app) openServiceEditor(groupID, serviceID, section string) {
	s.cancelServiceRepositoryChoiceReads()
	s.clearServiceEditorInputs()
	state := &serviceEditorState{session: fmt.Sprintf("%x", time.Now().UTC().UnixNano()), section: section, groupID: groupID, serviceID: serviceID,
		initialText: make(map[string]string), initialBools: make(map[string]bool), expandedModules: make(map[string]bool),
		expandedTags: make(map[string]bool), expandedCommands: make(map[string]bool), copiesMode: "form", commandsMode: "form"}
	state.branchChoices = make(map[string][]string)
	state.branchChoicesLoaded = make(map[string]bool)
	state.branchChoicesFailed = make(map[string]bool)
	if state.section == "" {
		state.section = "basic"
	}
	state.service = map[string]any{
		"id": serviceID, "repositoryId": "", "displayName": "", "enabled": true,
		"genbuProbeEnabled": false, "genbuServiceName": "", "masterBranch": "origin/master",
		"testTagBaselineRef": "origin/release/test", "developmentTool": "INTELLIJ_IDEA",
		"modules": []any{map[string]any{"id": "default", "name": "default", "strategy": "STANDARD_WORKTREE", "tagEnabled": true,
			"tagMode": "MERGE_TO_TARGET_BRANCH", "tagMessagePrefix": "Tag", "customCommands": []any{}}},
		"bootstrap": map[string]any{"copyRules": []any{}, "commands": []any{}}, "commitMessageTemplate": "",
	}
	if serviceID != "" {
		config, err := s.settingsConfigMap()
		if err != nil {
			s.lastError = err.Error()
			return
		}
		stored := findServiceConfig(config, serviceID, groupID)
		if stored == nil {
			s.lastError = fmt.Sprintf("服务 %q 不在当前配置中", serviceID)
			return
		}
		state.service = cloneConfigMap(stored)
		if state.service == nil {
			s.lastError = "无法读取服务配置。"
			return
		}
	}
	state.modules = serviceModuleDrafts(state.service["modules"])
	if serviceID == "" && len(state.modules) == 0 {
		state.modules = []serviceModuleDraft{newServiceModuleDraft("default", "default", "STANDARD_WORKTREE")}
	}
	if len(state.modules) > 0 {
		state.expandedModules[state.modules[0].id] = true
	}
	bootstrap := mapData(state.service["bootstrap"])
	state.copies = copyRuleDrafts(bootstrap["copyRules"])
	state.commands = bootstrapCommandDrafts(bootstrap["commands"])
	state.initialModules, state.initialCopies, state.initialCommands = moduleIDs(state.modules), copyIDs(state.copies), bootstrapCommandIDs(state.commands)
	branch := mapString(state.service, "masterBranch")
	state.activeRemote = branchRemote(branch)
	s.workspaceUnit.state.serviceEditor = state
	s.form = actionForm{kind: "service.save", mode: "service-editor", serviceID: serviceID}
	s.dialogTitle = "服务配置"
	s.dialogOpen = true
	s.lastError = ""
	if state.section == "git" || state.section == "modules" {
		s.requestServiceRepositoryChoices("", false)
	}
	if state.section == "git" {
		s.requestServiceRepositoryAddresses(false)
	}
}

func (s *app) clearServiceEditorInputs() {
	for key := range s.inputs {
		if strings.HasPrefix(key, "service-editor:") {
			delete(s.inputs, key)
		}
	}
	for key := range s.settingsUnit.state.settingsBoolInputs {
		if strings.HasPrefix(key, "service-editor:") {
			delete(s.settingsUnit.state.settingsBoolInputs, key)
		}
	}
}

func (s *app) serviceEditorKey(key string) string {
	if s.workspaceUnit.state.serviceEditor == nil {
		return "service-editor:missing:" + key
	}
	return "service-editor:" + s.workspaceUnit.state.serviceEditor.session + ":" + key
}

func (s *app) serviceEditorText(key, initial string) *string {
	storageKey := s.serviceEditorKey(key)
	if s.workspaceUnit.state.serviceEditor != nil {
		if _, exists := s.workspaceUnit.state.serviceEditor.initialText[storageKey]; !exists {
			s.workspaceUnit.state.serviceEditor.initialText[storageKey] = initial
		}
	}
	return s.input(storageKey, initial)
}

func (s *app) serviceEditorBool(key string, initial bool) *bool {
	storageKey := s.serviceEditorKey(key)
	if s.workspaceUnit.state.serviceEditor != nil {
		if _, exists := s.workspaceUnit.state.serviceEditor.initialBools[storageKey]; !exists {
			s.workspaceUnit.state.serviceEditor.initialBools[storageKey] = initial
		}
	}
	return s.settingsBoolInput(storageKey, initial)
}

func serviceModuleDrafts(value any) []serviceModuleDraft {
	drafts := make([]serviceModuleDraft, 0)
	for _, raw := range mapsFromData(value) {
		id := mapString(raw, "id")
		if id == "" {
			continue
		}
		config := cloneConfigMap(raw)
		if _, exists := config["tagEnabled"]; !exists {
			config["tagEnabled"] = true
		}
		commands := make([]workspaceCommandDraft, 0)
		for _, command := range mapsFromData(raw["customCommands"]) {
			commands = append(commands, workspaceCommandFromMap(command))
		}
		drafts = append(drafts, serviceModuleDraft{
			raw: config, id: id, name: firstNonEmpty(mapString(raw, "name"), "default"),
			strategy:     firstNonEmpty(mapString(raw, "strategy"), "STANDARD_WORKTREE"),
			masterBranch: mapString(raw, "masterBranch"), tagMode: firstNonEmpty(mapString(raw, "tagMode"), "MERGE_TO_TARGET_BRANCH"),
			tagTargetRef: mapString(raw, "tagTargetRef"), tagMessagePrefix: firstNonEmpty(mapString(raw, "tagMessagePrefix"), "Tag"), commands: commands,
		})
	}
	return drafts
}

func newServiceModuleDraft(id, name, strategy string) serviceModuleDraft {
	return serviceModuleDraft{raw: map[string]any{"tagEnabled": strategy == "STANDARD_WORKTREE"}, id: id, name: name, strategy: strategy,
		tagMode: "MERGE_TO_TARGET_BRANCH", tagMessagePrefix: "Tag", commands: []workspaceCommandDraft{}}
}

func workspaceCommandFromMap(raw map[string]any) workspaceCommandDraft {
	args := make([]string, 0)
	for _, item := range anySlice(raw["arguments"]) {
		if text := fmt.Sprint(item); strings.TrimSpace(text) != "" {
			args = append(args, text)
		}
	}
	return workspaceCommandDraft{raw: cloneConfigMap(raw), id: mapString(raw, "id"), name: mapString(raw, "name"),
		iconKey: firstNonEmpty(mapString(raw, "iconKey"), "build"), executable: mapString(raw, "executable"),
		arguments: strings.Join(args, "\n"), directory: firstNonEmpty(mapString(raw, "workingDirectory"), "."),
		timeout: configNumberString(raw["timeoutSeconds"], "1800")}
}

func copyRuleDrafts(value any) []copyRuleDraft {
	drafts := make([]copyRuleDraft, 0)
	for _, raw := range mapsFromData(value) {
		source := mapString(raw, "source")
		drafts = append(drafts, copyRuleDraft{raw: cloneConfigMap(raw), id: newDesktopEditorID("copy"), source: source, target: firstNonEmpty(mapString(raw, "target"), source)})
	}
	return drafts
}

func bootstrapCommandDrafts(value any) []bootstrapCommandDraft {
	drafts := make([]bootstrapCommandDraft, 0)
	for _, raw := range mapsFromData(value) {
		args := make([]string, 0)
		for _, item := range anySlice(raw["arguments"]) {
			args = append(args, fmt.Sprint(item))
		}
		drafts = append(drafts, bootstrapCommandDraft{raw: cloneConfigMap(raw), id: newDesktopEditorID("bootstrap-command"),
			name: mapString(raw, "name"), executable: mapString(raw, "executable"), arguments: strings.Join(args, "\n"),
			directory: firstNonEmpty(mapString(raw, "workingDirectory"), "."), timeout: configNumberString(raw["timeoutSeconds"], "600")})
	}
	return drafts
}

func anySlice(value any) []any {
	items, _ := value.([]any)
	return items
}

func configNumberString(value any, fallback string) string {
	if value == nil {
		return fallback
	}
	return fmt.Sprint(value)
}

func mapBool(value map[string]any, key string, fallback bool) bool {
	if result, ok := value[key].(bool); ok {
		return result
	}
	return fallback
}

var desktopEditorIDSequence atomic.Uint64

func newDesktopEditorID(prefix string) string {
	return fmt.Sprintf("%s-%x-%x", prefix, time.Now().UTC().UnixNano(), desktopEditorIDSequence.Add(1))
}

func moduleIDs(items []serviceModuleDraft) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.id)
	}
	return ids
}

func copyIDs(items []copyRuleDraft) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.id)
	}
	return ids
}

func bootstrapCommandIDs(items []bootstrapCommandDraft) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.id)
	}
	return ids
}

func branchRemote(value string) string {
	value = strings.TrimSpace(value)
	if !strings.Contains(value, "/") {
		return "origin"
	}
	return value[:strings.IndexByte(value, '/')]
}

func serviceEditorBranchChoiceKey(scope serviceRepositoryChoiceScope, remote string) string {
	return strings.Join([]string{scope.repositoryID, filepath.Clean(scope.rootPath), remote}, "\x00")
}

func (s *app) serviceEditorBranchChoices(remote string) ([]string, bool, bool) {
	state := s.workspaceUnit.state.serviceEditor
	if state == nil {
		return nil, false, false
	}
	if state.branchChoices == nil {
		state.branchChoices = make(map[string][]string)
	}
	if state.branchChoicesLoaded == nil {
		state.branchChoicesLoaded = make(map[string]bool)
	}
	if state.branchChoicesFailed == nil {
		state.branchChoicesFailed = make(map[string]bool)
	}
	key := serviceEditorBranchChoiceKey(s.serviceRepositoryChoiceScope(), remote)
	return state.branchChoices[key], state.branchChoicesLoaded[key], state.branchChoicesFailed[key]
}

func (s *app) requestServiceRepositoryChoices(remote string, force bool) {
	s.observeServiceRepositoryChoiceScope()
	state := s.workspaceUnit.state.serviceEditor
	if state == nil || state.saving || !s.dialogOpen {
		return
	}
	repositoryID := strings.TrimSpace(*s.serviceEditorText("repositoryId", mapString(state.service, "repositoryId")))
	if repositoryID == "" {
		state.choicesError = "请先选择仓库。"
		return
	}
	if remote == "" {
		if !force && state.remotesLoaded {
			return
		}
		if !force && s.serviceRepositoryChoiceReadCurrent("repository.choices", "") {
			return
		}
		state.remotesLoaded = false
		if force {
			state.branchChoices = make(map[string][]string)
			state.branchChoicesLoaded = make(map[string]bool)
			state.branchChoicesFailed = make(map[string]bool)
		}
	} else {
		_, loaded, failed := s.serviceEditorBranchChoices(remote)
		if !force && (loaded || failed) {
			return
		}
		if s.serviceRepositoryChoiceReadCurrent("repository.choices", remote) {
			return
		}
		if jobs := s.workspaceUnit.state.serviceRepositoryChoices; jobs != nil && !force {
			jobs.mu.Lock()
			current := jobs.current["repository.choices"]
			jobs.mu.Unlock()
			if current != nil && current.ctx.Err() == nil {
				if current.remote != "" {
					queueServiceRepositoryChoiceRemote(state, remote)
				}
				return
			}
		}
	}
	if remote == "" || force {
		s.cancelServiceRepositoryChoiceSlot("repository.choices")
		state.pendingChoiceRemotes = nil
	} else if s.serviceRepositoryChoiceReadCurrent("repository.choices", remote) {
		return
	}
	if remote != "" && state.activeRemote != remote {
		state.branches = nil
	}
	request := newDesktopEditorID("choices")
	state.choicesRequest, state.choicesLoading, state.choicesError = request, true, ""
	if remote != "" {
		state.activeRemote = remote
	}
	payload := map[string]any{"repositoryId": repositoryID, "force": force, "requestKey": request}
	if remote != "" {
		payload["remote"] = remote
	}
	s.startServiceRepositoryChoiceRead(application.Action{Kind: "repository.choices", Payload: payload})
}

func (s *app) applyServiceRepositoryChoices(action application.Action, result application.ActionResult) {
	state := s.workspaceUnit.state.serviceEditor
	if state == nil || payloadString(action.Payload, "requestKey") == "" || payloadString(action.Payload, "requestKey") != state.choicesRequest {
		return
	}
	requestedRepo := payloadString(action.Payload, "repositoryId")
	if requestedRepo != *s.serviceEditorText("repositoryId", mapString(state.service, "repositoryId")) {
		state.choicesLoading = false
		return
	}
	data := mapData(result.Data)
	_, currentRoot := s.currentRepositoryDetails(requestedRepo)
	if mapString(data, "repositoryId") != requestedRepo || currentRoot == "" || dataString(data, "rootPath") != currentRoot {
		state.choicesLoading = false
		state.choicesError = "仓库选择结果已过期，请重新读取远程分支。"
		return
	}
	state.choicesLoading = false
	state.choicesError = ""
	if payloadString(action.Payload, "remote") == "" {
		state.remotes = mapsString(data["remotes"])
		state.remotesLoaded = true
		state.branches = nil
		if len(state.remotes) > 0 {
			if !containsString(state.remotes, state.activeRemote) {
				state.activeRemote = state.remotes[0]
			}
		}
	} else {
		remote := payloadString(action.Payload, "remote")
		branches := mapsString(data["branches"])
		key := serviceEditorBranchChoiceKey(s.serviceRepositoryChoiceScope(), remote)
		if state.branchChoices == nil {
			state.branchChoices = make(map[string][]string)
		}
		if state.branchChoicesLoaded == nil {
			state.branchChoicesLoaded = make(map[string]bool)
		}
		if state.branchChoicesFailed == nil {
			state.branchChoicesFailed = make(map[string]bool)
		}
		state.branchChoices[key] = branches
		state.branchChoicesLoaded[key] = true
		state.branchChoicesFailed[key] = false
		if state.activeRemote == remote {
			state.branches = branches
		}
	}
}

func queueServiceRepositoryChoiceRemote(state *serviceEditorState, remote string) {
	if state == nil || remote == "" || remote == state.activeRemote || containsString(state.pendingChoiceRemotes, remote) {
		return
	}
	state.pendingChoiceRemotes = append(state.pendingChoiceRemotes, remote)
}

func (s *app) startNextServiceRepositoryChoice() {
	state := s.workspaceUnit.state.serviceEditor
	if state == nil || !s.dialogOpen || state.saving || len(state.pendingChoiceRemotes) == 0 || (state.section != "git" && state.section != "modules") {
		return
	}
	remote := state.pendingChoiceRemotes[0]
	state.pendingChoiceRemotes = state.pendingChoiceRemotes[1:]
	if _, loaded, failed := s.serviceEditorBranchChoices(remote); !loaded && !failed {
		s.requestServiceRepositoryChoices(remote, false)
	}
}

func (s *app) renderServiceEditorDialog(c *ui.Context, p palette) {
	s.observeServiceRepositoryChoiceScope()
	state := s.workspaceUnit.state.serviceEditor
	if state == nil {
		return
	}
	if !s.dialogOpen {
		s.cancelServiceRepositoryChoiceReads()
		return
	}
	open := s.dialogOpen
	if ui.DialogBase(c.Key("service-editor-dialog"), &open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.36))
		w, h := c.Size()
		dialogWidth := min(float32(980), max(float32(320), w-32))
		dialogHeight := min(float32(820), max(float32(240), h-32))
		sidebarWidth := float32(150)
		if dialogWidth < 680 {
			sidebarWidth = 112
		}
		panel.Width(dialogWidth).Height(dialogHeight).MaxHeight(dialogHeight).Padding(18).Gap(12).Background(p.surface).Border(1, p.line).Radius(18).Disabled(state.saving)
		panel.Children(func() {
			ui.Row(c).Gap(10).Children(func() {
				ui.Column(c).Grow(1).Gap(3).Children(func() {
					label(c, "服务配置", 19, p.text).FontWeight(600)
					label(c, firstNonEmpty(mapString(state.service, "displayName"), "新服务"), 11, p.muted).SingleLine()
				})
				if state.saving {
					pill(c, "保存中", p)
				}
			})
			ui.Divider(c)
			ui.Row(c).Grow(1).MinHeight(0).Gap(12).AlignItems(ui.Stretch).Children(func() {
				s.renderServiceEditorSectionSidebar(c, p, state, sidebarWidth)
				ui.Scroll(c.Key("service-editor-content-"+state.session+"-"+state.section)).Grow(1).MinWidth(0).MinHeight(0).Padding(4, 4, 8, 2).Children(func() {
					ui.Column(c).FillWidth().MaxWidth(720).Gap(12).Children(func() {
						s.renderServiceEditorSectionContent(c, p, state)
					})
				})
			})
			ui.Divider(c)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				label(c, "修改仅影响后续任务", 11, p.muted).Grow(1)
				if action(c, "service-editor-cancel", "close", "取消", p, false) && !state.saving {
					s.dismissServiceEditor()
				}
				if action(c, "service-editor-save", "save", "保存配置", p, true) && !state.saving {
					s.submitServiceEditor()
				}
			})
		})
	}).Dismissed() {
		s.dismissServiceEditor()
	}
	if state.exampleOpen {
		ui.DialogBase(c.Key("service-editor-example-dialog"), &state.exampleOpen, func(backdrop, panel ui.Element) {
			backdrop.Background(ui.RGBA(0, 0, 0, 0.4))
			w, h := c.Size()
			panel.Width(min(float32(760), max(float32(320), w-32))).Height(min(float32(650), max(float32(240), h-32))).Padding(18).Gap(12).Background(p.surface).Border(1, p.line).Radius(16)
			panel.Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					label(c, state.exampleTitle+" JSON 示例", 16, p.text).Grow(1)
					if action(c, "service-editor-example-copy", "copy", "复制示例", p, false) {
						if s.copyTextToClipboard(c.Services(), state.exampleJSON, "JSON 示例已复制") {
							state.exampleOpen = false
						}
					}
					if action(c, "service-editor-example-close", "close", "关闭", p, false) {
						state.exampleOpen = false
					}
				})
				label(c, "示例仅供参考，查看或复制不会修改当前配置。", 11, p.muted)
				ui.TextArea(c.Key("service-editor-example-json"), &state.exampleJSON).FillWidth().Grow(1).MinHeight(0).Padding(12).Font("Consolas").FontSize(12).Background(p.soft).Border(1, p.line).Radius(8).ReadOnly(true)
			})
		})
	}
}

func (s *app) selectServiceEditorSection(section string) {
	if s.workspaceUnit.state.serviceEditor == nil {
		return
	}
	if s.workspaceUnit.state.serviceEditor.section != section {
		s.cancelServiceRepositoryChoiceReads()
	}
	s.workspaceUnit.state.serviceEditor.section = section
	if (section == "git" || section == "modules") && !s.workspaceUnit.state.serviceEditor.remotesLoaded && s.workspaceUnit.state.serviceEditor.choicesError == "" {
		s.requestServiceRepositoryChoices("", false)
	}
	if section == "git" {
		s.requestServiceRepositoryAddresses(false)
	}
}

func (s *app) renderServiceEditorSectionSidebar(c *ui.Context, p palette, state *serviceEditorState, width float32) {
	ui.Scroll(c.Key("service-editor-sidebar-" + state.session)).Width(width).Shrink(0).MinHeight(0).Padding(6).Gap(4).Background(p.soft).Radius(12).Children(func() {
		for _, section := range serviceEditorSections {
			selected := state.section == section.id
			button := ui.ButtonBase(c.Key("service-editor-section-"+section.id)).FillWidth().Height(38).Padding(0, 10).Radius(9).Label(section.title)
			if selected {
				button.Background(p.selected)
			}
			button.Children(func() { label(c, section.title, 12, map[bool]ui.Color{true: p.blue, false: p.text}[selected]).NoWrap() })
			if button.Clicked() {
				s.selectServiceEditorSection(section.id)
			}
		}
	})
}

func (s *app) renderServiceEditorSectionContent(c *ui.Context, p palette, state *serviceEditorState) {
	switch state.section {
	case "basic":
		s.renderServiceEditorBasic(c, p, state)
	case "tools":
		s.renderServiceEditorTools(c, p, state)
	case "git":
		s.renderServiceEditorGit(c, p, state)
	case "genbu":
		s.renderServiceEditorGenbu(c, p, state)
	case "modules":
		s.renderServiceEditorModules(c, p, state)
	case "copies":
		s.renderServiceEditorCopies(c, p, state)
	case "commands":
		s.renderServiceEditorCommands(c, p, state)
	}
	if state.error != "" {
		ui.Box(c).FillWidth().Padding(9, 11).Background(p.amberBg).Radius(8).Children(func() { label(c, state.error, 12, p.amber) })
	}
}

func (s *app) dismissServiceEditor() {
	state := s.workspaceUnit.state.serviceEditor
	if state == nil {
		s.dialogOpen = false
		return
	}
	if state.saving {
		return
	}
	session := state.session
	dismiss := func() {
		if s.workspaceUnit.state.serviceEditor != state || state.session != session || state.saving {
			return
		}
		s.cancelServiceRepositoryChoiceReads()
		s.workspaceUnit.state.serviceEditor = nil
		s.dialogOpen = false
		s.clearServiceEditorInputs()
	}
	if s.serviceEditorDirty() {
		s.confirmNative("放弃服务配置修改？", "尚未保存的服务配置将丢失。", mapString(state.service, "displayName"), dismiss)
		return
	}
	dismiss()
}

func (s *app) serviceEditorDirty() bool {
	state := s.workspaceUnit.state.serviceEditor
	if state == nil {
		return false
	}
	for key, initial := range state.initialText {
		if current := s.inputs[key]; current != nil && *current != initial {
			return true
		}
	}
	for key, initial := range state.initialBools {
		if current := s.settingsUnit.state.settingsBoolInputs[key]; current != nil && *current != initial {
			return true
		}
	}
	for _, initial := range mapsFromData(state.service["modules"]) {
		module := valueModule(state.modules, mapString(initial, "id"))
		if module == nil {
			continue
		}
		commands := mapsFromData(initial["customCommands"])
		if len(commands) != len(module.commands) {
			return true
		}
		for index, command := range commands {
			if mapString(command, "id") != module.commands[index].id {
				return true
			}
		}
	}
	return !equalStrings(moduleIDs(state.modules), state.initialModules) ||
		!equalStrings(copyIDs(state.copies), state.initialCopies) ||
		!equalStrings(bootstrapCommandIDs(state.commands), state.initialCommands)
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (s *app) renderServiceEditorBasic(c *ui.Context, p palette, state *serviceEditorState) {
	sectionHeading(c, p, "repo", "基本信息", "修改服务名称与启用状态")
	s.serviceEditorField(c, p, "name", "展示名称", mapString(state.service, "displayName"), false)
	s.serviceEditorSwitch(c, p, "enabled", "启用服务", mapBool(state.service, "enabled", true))
	label(c, "关闭后，新任务将无法选择此服务。", 11, p.muted)
}

func (s *app) renderServiceEditorTools(c *ui.Context, p palette, state *serviceEditorState) {
	sectionHeading(c, p, "launch", "默认开发工具", "用于打开服务工作区")
	current := firstNonEmpty(mapString(state.service, "developmentTool"), "INTELLIJ_IDEA")
	labels := map[string]string{"INTELLIJ_IDEA": "IntelliJ IDEA", "WEBSTORM": "WebStorm", "PYCHARM": "PyCharm", "VISUAL_STUDIO_CODE": "Visual Studio Code", "ANDROID_STUDIO": "Android Studio", "DEVECO_STUDIO": "DevEco Studio"}
	for _, option := range developmentToolOptions() {
		value := option
		s.settingsChoice(c, p, "service-editor-tool-"+value, labels[value], *s.serviceEditorText("developmentTool", current) == value, func() { *s.serviceEditorText("developmentTool", current) = value })
	}
}

func (s *app) renderServiceEditorGit(c *ui.Context, p palette, state *serviceEditorState) {
	ui.Column(c).FillWidth().Gap(5).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			icon(c, "branch", 17, p.muted)
			label(c, "分支与提交", 15, p.text).FontWeight(600).Grow(1)
			s.overflowMenu(c, p, "service-editor-git-more-"+state.session, "Git 配置更多操作", state.saving || state.choicesLoading || state.addressesLoading, func(menu *overflowMenuBuilder) {
				if menu.Item("refresh-local", "刷新本地 Git 信息").Icon("refresh").Chosen() {
					s.requestServiceRepositoryChoices("", true)
					s.requestServiceRepositoryAddresses(true)
				}
			})
		})
		label(c, "模块可继承服务的默认分支，也可单独覆盖", 11, p.muted).Padding(0, 25)
	})
	if !state.remotesLoaded && !state.choicesLoading && state.choicesError == "" {
		s.requestServiceRepositoryChoices("", false)
	}
	if state.choicesLoading && !state.remotesLoaded {
		label(c, "正在读取本地 Git 信息…", 10, p.muted)
	}
	if state.choicesError != "" && !state.remotesLoaded {
		label(c, state.choicesError, 11, p.amber)
	}
	label(c, "仓库远程地址", 11, p.muted)
	s.renderServiceRemoteAddresses(c, p, state)
	s.serviceEditorBranchField(c, p, state, "masterBranch", "主分支", mapString(state.service, "masterBranch"), "git-master")
	s.serviceEditorBranchField(c, p, state, "testTagBaselineRef", "测试 Tag 默认目标", mapString(state.service, "testTagBaselineRef"), "git-test-tag")
	s.serviceEditorField(c, p, "commitMessageTemplate", "提交信息模板", mapString(state.service, "commitMessageTemplate"), false)
}

func (s *app) requestServiceRepositoryAddresses(force bool) {
	s.observeServiceRepositoryChoiceScope()
	state := s.workspaceUnit.state.serviceEditor
	if state == nil || state.saving || !s.dialogOpen || (!force && (state.addressesLoaded || s.serviceRepositoryChoiceReadCurrent("repository.addresses", ""))) {
		return
	}
	repositoryID := strings.TrimSpace(*s.serviceEditorText("repositoryId", mapString(state.service, "repositoryId")))
	if repositoryID == "" {
		state.addressesError = "请先选择仓库。"
		return
	}
	s.cancelServiceRepositoryChoiceSlot("repository.addresses")
	request := newDesktopEditorID("addresses")
	state.addressesRequest, state.addressesLoading, state.addressesError = request, true, ""
	s.startServiceRepositoryChoiceRead(application.Action{Kind: "repository.addresses", Payload: map[string]any{"repositoryId": repositoryID, "force": force, "requestKey": request}})
}

func (s *app) applyServiceRepositoryAddresses(action application.Action, result application.ActionResult) {
	state := s.workspaceUnit.state.serviceEditor
	if state == nil || payloadString(action.Payload, "requestKey") == "" || payloadString(action.Payload, "requestKey") != state.addressesRequest {
		return
	}
	repositoryID := payloadString(action.Payload, "repositoryId")
	if repositoryID != *s.serviceEditorText("repositoryId", mapString(state.service, "repositoryId")) {
		state.addressesLoading = false
		return
	}
	_, rootPath := s.currentRepositoryDetails(repositoryID)
	data := mapData(result.Data)
	if mapString(data, "repositoryId") != repositoryID || rootPath == "" || dataString(data, "rootPath") != rootPath {
		state.addressesLoading = false
		state.addressesError = "仓库远程地址结果已过期，请重新刷新。"
		return
	}
	state.addressesLoading, state.addressesLoaded, state.addressesError = false, true, ""
	state.remoteAddresses = mapsFromData(dataValue(data, "remotes"))
}

func (s *app) renderServiceRemoteAddresses(c *ui.Context, p palette, state *serviceEditorState) {
	ui.Column(c).FillWidth().Gap(7).Children(func() {
		switch {
		case state.addressesLoading:
			label(c, "正在读取远程地址…", 11, p.muted)
		case state.addressesError != "":
			label(c, state.addressesError, 11, p.amber)
		case state.addressesLoaded && len(state.remoteAddresses) == 0:
			label(c, "未配置远程地址。", 11, p.muted)
		case !state.addressesLoaded:
			label(c, "尚未读取远程地址。", 11, p.muted)
		default:
			for _, address := range state.remoteAddresses {
				remote, gitURL := mapString(address, "remote"), mapString(address, "gitUrl")
				purpose := " · 推送"
				if dataBool(address, "fetch") && dataBool(address, "push") {
					purpose = ""
				} else if dataBool(address, "fetch") {
					purpose = " · 拉取"
				}
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					label(c, remote+purpose, 10, p.muted).Width(118).SingleLine()
					label(c, gitURL, 11, p.text).Grow(1).MinWidth(0).SingleLine().Tooltip(gitURL).Selectable()
					browserURL := gitRemoteBrowserURL(gitURL)
					s.overflowMenu(c, p, "service-editor-remote-more-"+state.session+"-"+remote, "远程 "+remote+" 操作", state.saving, func(menu *overflowMenuBuilder) {
						if menu.Item("open", "打开远程仓库").Icon("launch").Disabled(browserURL == "").Chosen() && browserURL != "" {
							mygo.Shell.OpenExternal(browserURL)
						}
						if menu.Item("copy", "复制远程地址").Icon("copy").Disabled(strings.TrimSpace(gitURL) == "").Chosen() {
							s.copyTextToClipboard(c.Services(), gitURL, "远程地址已复制")
						}
					})
				})
			}
		}
	})
}

func gitRemoteBrowserURL(raw string) string {
	value := strings.TrimSpace(raw)
	if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https", "ssh":
			parsed.User, parsed.RawQuery, parsed.Fragment = nil, "", ""
			parsed.Scheme = "https"
			parsed.Path = strings.TrimSuffix(parsed.Path, ".git")
			parsed.RawPath = ""
			return parsed.String()
		}
	}
	if at := strings.LastIndex(value, "@"); at >= 0 {
		remote := value[at+1:]
		if colon := strings.IndexByte(remote, ':'); colon > 0 {
			host, path := remote[:colon], strings.TrimPrefix(remote[colon+1:], "/")
			if host != "" && path != "" && !strings.ContainsAny(host+path, " \t\r\n") {
				return (&url.URL{Scheme: "https", Host: host, Path: strings.TrimSuffix(path, ".git")}).String()
			}
		}
	}
	return ""
}

func (s *app) renderServiceEditorGenbu(c *ui.Context, p palette, state *serviceEditorState) {
	sectionHeading(c, p, "tag", "Genbu", "只读取此服务的 Tag 构建与发布状态")
	s.serviceEditorSwitch(c, p, "genbuProbeEnabled", "启用 Genbu 探测", mapBool(state.service, "genbuProbeEnabled", false))
	if *s.serviceEditorBool("genbuProbeEnabled", mapBool(state.service, "genbuProbeEnabled", false)) {
		s.serviceEditorField(c, p, "genbuServiceName", "Genbu 服务名", firstNonEmpty(mapString(state.service, "genbuServiceName"), mapString(state.service, "displayName")), false)
	}
}

func (s *app) renderServiceEditorModules(c *ui.Context, p palette, state *serviceEditorState) {
	sectionHeading(c, p, "code", "工作区模块", "模块保存到服务配置。删除和新增只在点击保存后生效。")
	for index := range state.modules {
		module := &state.modules[index]
		s.renderServiceModuleCard(c, p, state, module)
	}
	ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
		if action(c, "service-editor-module-worktree", "plus", "添加 Worktree 模块", p, false) {
			module := newServiceModuleDraft(newDesktopEditorID("module"), "default", "STANDARD_WORKTREE")
			state.modules = append(state.modules, module)
			state.expandedModules[module.id] = true
		}
		if action(c, "service-editor-module-clone", "plus", "添加克隆模块", p, false) {
			module := newServiceModuleDraft(newDesktopEditorID("module"), "default", "INDEPENDENT_CLONE")
			state.modules = append(state.modules, module)
			state.expandedModules[module.id] = true
		}
	})
}

func (s *app) renderServiceModuleCard(c *ui.Context, p palette, state *serviceEditorState, module *serviceModuleDraft) {
	ui.Column(c).FillWidth().Padding(12).Gap(9).Background(p.soft).Border(1, p.line).Radius(10).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			label(c, firstNonEmpty(module.name, module.id)+" · "+serviceStrategyLabel(*s.serviceEditorModuleText(module, "strategy", module.strategy)), 12, p.text).Grow(1).SingleLine()
			if action(c, "service-editor-module-toggle-"+module.id, map[bool]string{true: "chevron", false: "chevron-left"}[state.expandedModules[module.id]], map[bool]string{true: "收起模块", false: "展开模块"}[state.expandedModules[module.id]], p, false) {
				state.expandedModules[module.id] = !state.expandedModules[module.id]
			}
		})
		if !state.expandedModules[module.id] {
			label(c, "主分支："+firstNonEmpty(module.masterBranch, *s.serviceEditorText("masterBranch", mapString(state.service, "masterBranch"))), 10, p.muted)
			return
		}
		ui.Row(c).Gap(8).Children(func() {
			label(c, "模块 ID · "+module.id, 9, p.muted).Grow(1).SingleLine()
			if len(state.modules) > 1 && action(c, "service-editor-module-remove-"+module.id, "delete", "删除模块", p, false) {
				state.modules = removeServiceModule(state.modules, module.id)
				delete(state.expandedModules, module.id)
				delete(state.expandedTags, module.id)
				delete(state.expandedCommands, module.id)
			}
		})
		s.serviceEditorModuleField(c, p, module, "name", "模块名", module.name, false)
		ui.Column(c).Gap(5).Children(func() {
			label(c, "工作区策略", 11, p.muted)
			ui.Row(c).FillWidth().Wrap().Gap(6).Children(func() {
				for _, strategy := range []struct{ id, title string }{{"STANDARD_WORKTREE", "Worktree"}, {"INDEPENDENT_CLONE", "独立克隆"}} {
					id := strategy.id
					s.settingsChoice(c, p, "service-editor-strategy-"+module.id+"-"+id, strategy.title, *s.serviceEditorModuleText(module, "strategy", module.strategy) == id, func() { *s.serviceEditorModuleText(module, "strategy", module.strategy) = id })
				}
			})
		})
		inheritBranch := s.serviceEditorBool("module:"+module.id+":inherit-master", module.masterBranch == "")
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			label(c, "继承服务主分支", 11, p.text).Grow(1)
			ui.Switch(c.Key("service-editor-inherit-master-"+module.id), inheritBranch)
		})
		if !*inheritBranch {
			s.serviceEditorBranchField(c, p, state, "module:"+module.id+":masterBranch", "模块主分支", module.masterBranch, "module-branch-"+module.id)
		} else {
			label(c, "当前继承："+*s.serviceEditorText("masterBranch", mapString(state.service, "masterBranch")), 10, p.muted)
		}
		if s.configBool("tagEnabled", false) {
			tagEnabledInitial := mapBool(module.raw, "tagEnabled", module.strategy == "STANDARD_WORKTREE")
			if action(c, "service-editor-tag-toggle-"+module.id, "tag", fmt.Sprintf("测试 Tag · %s %s", map[bool]string{true: "已启用", false: "已关闭"}[*s.serviceEditorBool("module:"+module.id+":tagEnabled", tagEnabledInitial)], map[bool]string{true: "▴", false: "▾"}[state.expandedTags[module.id]]), p, false) {
				state.expandedTags[module.id] = !state.expandedTags[module.id]
			}
			if state.expandedTags[module.id] {
				s.serviceEditorSwitch(c, p, "module:"+module.id+":tagEnabled", "启用测试 Tag", tagEnabledInitial)
				tagEnabled := s.serviceEditorBool("module:"+module.id+":tagEnabled", tagEnabledInitial)
				if *tagEnabled {
					ui.Column(c).Gap(6).Children(func() {
						label(c, "Tag 模式", 11, p.muted)
						ui.Row(c).FillWidth().Wrap().Gap(6).Children(func() {
							for _, mode := range []string{"MERGE_TO_TARGET_BRANCH", "CURRENT_BRANCH"} {
								value := mode
								title := map[string]string{"MERGE_TO_TARGET_BRANCH": "合并到目标分支后构建", "CURRENT_BRANCH": "当前分支直接构建"}[value]
								s.settingsChoice(c, p, "service-editor-tag-mode-"+module.id+"-"+value, title, *s.serviceEditorModuleText(module, "tagMode", module.tagMode) == value, func() { *s.serviceEditorModuleText(module, "tagMode", module.tagMode) = value })
							}
						})
					})
					currentMode := *s.serviceEditorModuleText(module, "tagMode", module.tagMode)
					if currentMode == "MERGE_TO_TARGET_BRANCH" {
						inheritTarget := s.serviceEditorBool("module:"+module.id+":inherit-tag-target", module.tagTargetRef == "")
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							label(c, "继承服务 Tag 目标", 11, p.text).Grow(1)
							ui.Switch(c.Key("service-editor-inherit-tag-"+module.id), inheritTarget)
						})
						if *inheritTarget {
							label(c, "当前继承："+*s.serviceEditorText("testTagBaselineRef", mapString(state.service, "testTagBaselineRef")), 10, p.muted)
						} else {
							s.serviceEditorBranchField(c, p, state, "module:"+module.id+":tagTargetRef", "Tag 目标分支", module.tagTargetRef, "module-tag-"+module.id)
						}
					}
					s.serviceEditorModuleField(c, p, module, "tagMessagePrefix", "Tag 信息前缀", module.tagMessagePrefix, false)
				}
			}
		}
		ui.Divider(c)
		if action(c, "service-editor-command-toggle-"+module.id, "terminal", fmt.Sprintf("快捷命令 · %d %s", len(module.commands), map[bool]string{true: "▴", false: "▾"}[state.expandedCommands[module.id]]), p, false) {
			state.expandedCommands[module.id] = !state.expandedCommands[module.id]
		}
		if state.expandedCommands[module.id] {
			for commandIndex := range module.commands {
				s.renderWorkspaceCommand(c, p, "module:"+module.id, &module.commands[commandIndex])
			}
			if action(c, "service-editor-command-add-"+module.id, "plus", "添加快捷命令", p, false) {
				module.commands = append(module.commands, workspaceCommandDraft{raw: map[string]any{}, id: newDesktopEditorID("command"), name: "Maven 命令", iconKey: "maven", executable: "mvn", directory: ".", timeout: "1800"})
			}
		}
	})
}

func serviceStrategyLabel(value string) string {
	if value == "INDEPENDENT_CLONE" {
		return "独立克隆"
	}
	return "Worktree"
}

func (s *app) renderWorkspaceCommand(c *ui.Context, p palette, owner string, command *workspaceCommandDraft) {
	ui.Column(c).FillWidth().Padding(9).Gap(7).Background(p.surface).Border(1, p.line).Radius(8).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			label(c, command.id, 9, p.muted).Grow(1).SingleLine()
			if action(c, "service-editor-command-delete-"+command.id, "delete", "删除快捷命令", p, false) {
				if strings.HasPrefix(owner, "module:") {
					moduleID := strings.TrimPrefix(owner, "module:")
					for index := range s.workspaceUnit.state.serviceEditor.modules {
						if s.workspaceUnit.state.serviceEditor.modules[index].id == moduleID {
							s.workspaceUnit.state.serviceEditor.modules[index].commands = removeWorkspaceCommand(s.workspaceUnit.state.serviceEditor.modules[index].commands, command.id)
							break
						}
					}
				} else {
					s.workspaceUnit.state.serviceEditor.commands = removeBootstrapCommand(s.workspaceUnit.state.serviceEditor.commands, command.id)
				}
			}
		})
		s.serviceCommandField(c, p, owner, command, "name", "命令名称", command.name, false)
		s.serviceCommandField(c, p, owner, command, "executable", "可执行程序", command.executable, false)
		ui.Column(c).Gap(5).Children(func() {
			label(c, "图标", 10, p.muted)
			ui.Row(c).FillWidth().Wrap().Gap(4).Children(func() {
				for _, iconKey := range []string{"build", "maven", "maven-clean", "maven-install", "maven-deploy", "play", "terminal", "package", "upload", "refresh", "code", "database", "settings"} {
					icon := iconKey
					short := icon
					if len(short) > 12 {
						short = short[:12]
					}
					s.settingsChoice(c, p, "service-editor-icon-"+command.id+"-"+icon, short, *s.serviceCommandText(owner, command, "iconKey", command.iconKey) == icon, func() { *s.serviceCommandText(owner, command, "iconKey", command.iconKey) = icon })
				}
			})
		})
		args := s.serviceCommandText(owner, command, "arguments", command.arguments)
		label(c, "参数（每行一个；不经过 Shell）", 10, p.muted)
		ui.TextArea(c.Key("service-editor-"+owner+"-"+command.id+"-arguments"), args).FillWidth().Height(76).Padding(9).Background(p.surface).Border(1, p.line).Radius(7)
		s.serviceCommandField(c, p, owner, command, "directory", "工作目录", firstNonEmpty(command.directory, "."), false)
		s.serviceCommandField(c, p, owner, command, "timeout", "超时（秒）", firstNonEmpty(command.timeout, "600"), false)
		if strings.HasPrefix(owner, "module:") {
			s.serviceCommandSwitch(c, p, owner, command, "enabled", "启用", mapBool(command.raw, "enabled", true))
		}
		preview := strings.Join(append([]string{*s.serviceCommandText(owner, command, "executable", command.executable)}, nonBlankLines(*args)...), " ")
		label(c, "预览："+preview, 10, p.muted).SingleLine().Tooltip(preview)
	})
}

func (s *app) renderServiceEditorCopies(c *ui.Context, p palette, state *serviceEditorState) {
	sectionHeading(c, p, "copy", "初始化复制规则", "创建工作区时从原始仓库复制相对路径，可选择是否覆盖目标")
	s.renderBootstrapEditorModes(c, p, "copies")
	if state.copiesMode == "json" {
		jsonText := s.serviceEditorText("copies-json", s.bootstrapJSONExample("copies", state))
		label(c, "JSON 仅包含复制规则数组；保存时会校验相对路径和字段。", 11, p.muted)
		ui.TextArea(c.Key("service-editor-copies-json"), jsonText).FillWidth().Height(360).Padding(12).Font("Consolas").FontSize(12).Background(p.surface).Border(1, p.line).Radius(8)
		return
	}
	for index := range state.copies {
		rule := &state.copies[index]
		ui.Column(c).FillWidth().Padding(11).Gap(7).Background(p.soft).Border(1, p.line).Radius(9).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				label(c, "规则 "+fmt.Sprint(index+1), 11, p.text).Grow(1)
				if action(c, "service-editor-copy-delete-"+rule.id, "delete", "删除复制规则", p, false) {
					state.copies = removeCopyRule(state.copies, rule.id)
				}
			})
			s.serviceCopyField(c, p, rule, "source", "仓库内源路径", rule.source)
			s.serviceCopyField(c, p, rule, "target", "工作区目标路径", rule.target)
			ui.Row(c).FillWidth().Wrap().Gap(7).Children(func() {
				if action(c, "service-editor-copy-file-"+rule.id, "file", "选择文件", p, false) {
					s.pickServiceCopyPath(rule, false)
				}
				if action(c, "service-editor-copy-directory-"+rule.id, "folder", "选择目录", p, false) {
					s.pickServiceCopyPath(rule, true)
				}
				overwrite := s.serviceEditorBool("copy:"+rule.id+":overwrite", mapBool(rule.raw, "overwrite", true))
				ui.Switch(c.Key("service-editor-copy-overwrite-"+rule.id), overwrite).Label("允许覆盖")
			})
		})
	}
	if action(c, "service-editor-copy-add", "plus", "添加复制规则", p, false) {
		state.copies = append(state.copies, copyRuleDraft{raw: map[string]any{}, id: newDesktopEditorID("copy")})
	}
}

func (s *app) renderServiceEditorCommands(c *ui.Context, p palette, state *serviceEditorState) {
	sectionHeading(c, p, "terminal", "初始化命令", "复制规则完成后按列表顺序执行；参数逐项传递，不经过 Shell")
	s.renderBootstrapEditorModes(c, p, "commands")
	if state.commandsMode == "json" {
		jsonText := s.serviceEditorText("commands-json", s.bootstrapJSONExample("commands", state))
		label(c, "JSON 仅包含初始化命令数组；参数逐项传递，不经过 Shell。", 11, p.muted)
		ui.TextArea(c.Key("service-editor-commands-json"), jsonText).FillWidth().Height(360).Padding(12).Font("Consolas").FontSize(12).Background(p.surface).Border(1, p.line).Radius(8)
		return
	}
	for index := range state.commands {
		command := &state.commands[index]
		commandFields := workspaceCommandDraft{raw: command.raw, id: command.id, name: command.name,
			executable: command.executable, arguments: command.arguments, directory: command.directory, timeout: command.timeout}
		ui.Column(c).FillWidth().Padding(11).Gap(7).Background(p.soft).Border(1, p.line).Radius(9).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				label(c, "命令 "+fmt.Sprint(index+1), 11, p.text).Grow(1)
				if action(c, "service-editor-bootstrap-command-delete-"+command.id, "delete", "删除初始化命令", p, false) {
					state.commands = removeBootstrapCommand(state.commands, command.id)
				}
			})
			s.serviceCommandField(c, p, "bootstrap", &commandFields, "name", "命令名称", command.name, false)
			s.serviceCommandField(c, p, "bootstrap", &commandFields, "executable", "可执行程序", command.executable, false)
			s.serviceCommandField(c, p, "bootstrap", &commandFields, "arguments", "参数（每行一个）", command.arguments, true)
			s.serviceCommandField(c, p, "bootstrap", &commandFields, "directory", "工作目录", firstNonEmpty(command.directory, "."), false)
			s.serviceCommandField(c, p, "bootstrap", &commandFields, "timeout", "超时（秒）", firstNonEmpty(command.timeout, "600"), false)
			s.serviceCommandSwitch(c, p, "bootstrap", &commandFields, "enabled", "启用", mapBool(command.raw, "enabled", true))
			preview := strings.Join(append([]string{*s.serviceCommandText("bootstrap", &commandFields, "executable", command.executable)}, nonBlankLines(*s.serviceCommandText("bootstrap", &commandFields, "arguments", command.arguments))...), " ")
			label(c, "预览："+preview, 10, p.muted).SingleLine().Tooltip(preview)
		})
	}
	if action(c, "service-editor-bootstrap-command-add", "plus", "添加初始化命令", p, false) {
		state.commands = append(state.commands, bootstrapCommandDraft{raw: map[string]any{}, id: newDesktopEditorID("bootstrap-command"), directory: ".", timeout: "600"})
	}
}

func (s *app) renderBootstrapEditorModes(c *ui.Context, p palette, page string) {
	state := s.workspaceUnit.state.serviceEditor
	if state == nil {
		return
	}
	mode := &state.copiesMode
	inputKey, title := "copies-json", "复制规则"
	if page == "commands" {
		mode, inputKey, title = &state.commandsMode, "commands-json", "初始化命令"
	}
	ui.Row(c).FillWidth().Wrap().Gap(7).AlignItems(ui.Center).Children(func() {
		if action(c, "service-editor-"+page+"-form-mode", "", "表单配置", p, *mode == "form") {
			if *mode != "form" {
				text := *s.serviceEditorText(inputKey, "[]")
				if page == "copies" {
					drafts, err := parseBootstrapCopies(text, state.copies)
					if err != nil {
						state.error = err.Error()
					} else {
						state.copies = drafts
						*mode = "form"
						state.error = ""
					}
				} else {
					drafts, err := parseBootstrapCommands(text, state.commands)
					if err != nil {
						state.error = err.Error()
					} else {
						state.commands = drafts
						*mode = "form"
						state.error = ""
					}
				}
			}
		}
		if action(c, "service-editor-"+page+"-json-mode", "code", "高级 JSON", p, *mode == "json") {
			if *mode != "json" {
				var value any
				var err error
				if page == "copies" {
					value, err = s.bootstrapCopyConfig(state)
				} else {
					value, err = s.bootstrapCommandConfig(state)
				}
				if err != nil {
					state.error = err.Error()
				} else if encoded, marshalErr := json.MarshalIndent(value, "", "  "); marshalErr != nil {
					state.error = "无法生成 JSON 示例：" + marshalErr.Error()
				} else {
					s.setServiceEditorTextBaseline(inputKey, string(encoded))
					*mode = "json"
					state.error = ""
				}
			}
		}
		if action(c, "service-editor-"+page+"-example", "info", "查看示例", p, false) {
			state.exampleTitle = title
			state.exampleJSON = bootstrapEditorExample(page)
			state.exampleOpen = true
		}
	})
}

func bootstrapEditorExample(page string) string {
	var value any
	if page == "copies" {
		value = []map[string]any{{"source": ".env.example", "target": ".env.local", "overwrite": false}}
	} else {
		value = []map[string]any{{"name": "安装前端依赖", "executable": "pnpm", "arguments": []string{"install", "--frozen-lockfile"}, "workingDirectory": ".", "timeoutSeconds": 900, "enabled": true}}
	}
	encoded, _ := json.MarshalIndent(value, "", "  ")
	return string(encoded)
}

func (s *app) bootstrapJSONExample(page string, state *serviceEditorState) string {
	var value any
	var err error
	if page == "copies" {
		value, err = s.bootstrapCopyConfig(state)
	} else {
		value, err = s.bootstrapCommandConfig(state)
	}
	if err != nil {
		return "[]"
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func (s *app) setServiceEditorTextBaseline(key, value string) {
	state := s.workspaceUnit.state.serviceEditor
	if state == nil {
		return
	}
	storageKey := s.serviceEditorKey(key)
	text := value
	s.inputs[storageKey] = &text
	state.initialText[storageKey] = value
}

func (s *app) serviceEditorField(c *ui.Context, p palette, key, title, initial string, multiline bool) {
	value := s.serviceEditorText(key, initial)
	ui.Column(c).Gap(5).Children(func() {
		label(c, title, 11, p.muted)
		if multiline {
			ui.TextArea(c.Key("service-editor-"+s.workspaceUnit.state.serviceEditor.session+"-"+key), value).FillWidth().Height(92).Padding(10).Background(p.surface).Border(1, p.line).Radius(7)
		} else {
			ui.TextInput(c.Key("service-editor-"+s.workspaceUnit.state.serviceEditor.session+"-"+key), value).FillWidth().Height(38).Placeholder(title)
		}
	})
}

func (s *app) serviceEditorSwitch(c *ui.Context, p palette, key, title string, initial bool) {
	value := s.serviceEditorBool(key, initial)
	ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(3, 0).Children(func() {
		label(c, title, 12, p.text).Grow(1)
		ui.Switch(c.Key("service-editor-switch-"+key), value)
	})
}

func (s *app) serviceEditorBranchField(c *ui.Context, p palette, state *serviceEditorState, key, title, initial, keySuffix string) {
	s.observeServiceRepositoryChoiceScope()
	value := s.serviceEditorText(key, initial)
	remote := branchRemote(*value)
	branches, loaded, failed := s.serviceEditorBranchChoices(remote)
	if !loaded && !failed && state.choicesError == "" && (state.section == "git" || state.section == "modules") {
		s.requestServiceRepositoryChoices(remote, false)
	}
	ui.Column(c).Gap(5).Children(func() {
		label(c, title, 11, p.muted)
		combo := ui.ComboboxBase(c.Key("service-editor-branch-"+s.workspaceUnit.state.serviceEditor.session+"-"+key), value)
		combo.Input.FillWidth().Height(38).Padding(10, 30, 10, 10).Placeholder("remote/branch").Background(p.surface).Border(1, p.line).Radius(7).Label(title)
		combo.Input.Draw(func(draw *ui.Painter, rect ui.Rect) {
			var arrow ui.Path
			arrow.MoveTo(rect.X+rect.W-21, rect.Y+17).LineTo(rect.X+rect.W-17, rect.Y+21).LineTo(rect.X+rect.W-13, rect.Y+17)
			draw.StrokePath(&arrow, 1.5, p.muted)
		})
		combo.Popup(func(panel ui.Element) {
			panel.Padding(4).MaxHeight(240).Background(p.surface).Border(1, p.line).Radius(9).Shadow(0, 5, 16, 0, ui.RGBA(0, 0, 0, 0.14))
			ui.Scroll(c).FillWidth().MaxHeight(228).Gap(2).Children(func() {
				for _, branch := range branches {
					if combo.Filtering() && !strings.Contains(strings.ToLower(branch), strings.ToLower(strings.TrimSpace(*value))) {
						continue
					}
					item := combo.Item(branch).FillWidth().Height(32).Padding(0, 9).Radius(6)
					if item.Highlighted() {
						item.Background(p.selected)
					}
					item.Children(func() { label(c, branch, 11, p.text).SingleLine() })
				}
			})
		})
		if chosen, ok := combo.Chosen(); ok {
			*value = chosen
			state.activeRemote = branchRemote(chosen)
		}
		if failed {
			label(c, "本地分支读取失败，可在“更多”中刷新。", 10, p.amber)
		} else if loaded && len(branches) == 0 {
			label(c, "未发现本地分支，可手动输入。", 10, p.muted)
		} else if state.choicesLoading && state.activeRemote == remote {
			label(c, "读取本地分支…", 10, p.muted)
		} else if containsString(state.pendingChoiceRemotes, remote) {
			label(c, "等待读取本地分支…", 10, p.muted)
		}
	})
}

func (s *app) serviceEditorModuleText(module *serviceModuleDraft, field, initial string) *string {
	return s.serviceEditorText("module:"+module.id+":"+field, initial)
}

func (s *app) serviceEditorModuleField(c *ui.Context, p palette, module *serviceModuleDraft, key, title, initial string, multiline bool) {
	value := s.serviceEditorModuleText(module, key, initial)
	ui.Column(c).Gap(4).Children(func() {
		label(c, title, 10, p.muted)
		if multiline {
			ui.TextArea(c.Key("service-editor-module-"+module.id+"-"+key), value).FillWidth().Height(72).Padding(9).Background(p.surface).Border(1, p.line).Radius(7)
		} else {
			ui.TextInput(c.Key("service-editor-module-"+module.id+"-"+key), value).FillWidth().Height(36).Placeholder(title)
		}
	})
}

func (s *app) serviceCommandText(owner string, command *workspaceCommandDraft, field, initial string) *string {
	return s.serviceEditorText(owner+":"+command.id+":"+field, initial)
}

func (s *app) serviceCommandField(c *ui.Context, p palette, owner string, command *workspaceCommandDraft, field, title, initial string, multiline bool) {
	value := s.serviceCommandText(owner, command, field, initial)
	ui.Column(c).Gap(4).Children(func() {
		label(c, title, 10, p.muted)
		if multiline {
			ui.TextArea(c.Key("service-editor-"+owner+"-"+command.id+"-"+field), value).FillWidth().Height(74).Padding(9).Background(p.surface).Border(1, p.line).Radius(7)
		} else {
			ui.TextInput(c.Key("service-editor-"+owner+"-"+command.id+"-"+field), value).FillWidth().Height(36).Placeholder(title)
		}
	})
}

func (s *app) serviceCommandSwitch(c *ui.Context, p palette, owner string, command *workspaceCommandDraft, field, title string, initial bool) {
	value := s.serviceEditorBool(owner+":"+command.id+":"+field, initial)
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		label(c, title, 11, p.text).Grow(1)
		ui.Switch(c.Key("service-editor-"+owner+"-"+command.id+"-"+field), value)
	})
}

func (s *app) serviceCopyField(c *ui.Context, p palette, rule *copyRuleDraft, field, title, initial string) {
	value := s.serviceEditorText("copy:"+rule.id+":"+field, initial)
	ui.Column(c).Gap(4).Children(func() {
		label(c, title, 10, p.muted)
		ui.TextInput(c.Key("service-editor-copy-"+rule.id+"-"+field), value).FillWidth().Height(36).Placeholder(title)
	})
}

func (s *app) pickServiceCopyPath(rule *copyRuleDraft, directory bool) {
	if s.workspaceUnit.state.serviceEditor == nil {
		return
	}
	repositoryID := *s.serviceEditorText("repositoryId", mapString(s.workspaceUnit.state.serviceEditor.service, "repositoryId"))
	_, repositoryRoot := s.currentRepositoryDetails(repositoryID)
	if repositoryRoot == "" {
		s.workspaceUnit.state.serviceEditor.error = "请先选择有效仓库。"
		return
	}
	s.pickNativePath("选择仓库内文件或目录", repositoryRoot, directory, nil, func(selected string) {
		relative, err := serviceRepositoryRelativePath(repositoryRoot, selected)
		if err != nil {
			s.workspaceUnit.state.serviceEditor.error = err.Error()
			return
		}
		s.workspaceUnit.state.serviceEditor.error = ""
		previous := *s.serviceEditorText("copy:"+rule.id+":source", rule.source)
		*s.serviceEditorText("copy:"+rule.id+":source", rule.source) = relative
		target := s.serviceEditorText("copy:"+rule.id+":target", rule.target)
		if strings.TrimSpace(*target) == "" || *target == previous {
			*target = relative
		}
	})
}

func serviceRepositoryRelativePath(root, selected string) (string, error) {
	rootPath, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("无法解析仓库目录：%w", err)
	}
	selectedPath, err := filepath.Abs(filepath.Clean(selected))
	if err != nil {
		return "", fmt.Errorf("无法解析选择路径：%w", err)
	}
	relative, err := filepath.Rel(rootPath, selectedPath)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("只能选择原始仓库内的文件或目录")
	}
	for _, part := range strings.Split(filepath.ToSlash(relative), "/") {
		if strings.EqualFold(part, ".git") || part == ".." {
			return "", fmt.Errorf("不能选择 .git 内容或仓库外路径")
		}
	}
	if info, statErr := os.Lstat(selectedPath); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("不能选择符号链接")
	}
	return filepath.ToSlash(relative), nil
}

func (s *app) serviceEditorModuleTextValue(module *serviceModuleDraft, key, fallback string) string {
	return *s.serviceEditorModuleText(module, key, fallback)
}

func (s *app) bootstrapCopyConfig(state *serviceEditorState) ([]any, error) {
	drafts := state.copies
	if state.copiesMode == "json" {
		parsed, err := parseBootstrapCopies(*s.serviceEditorText("copies-json", "[]"), state.copies)
		if err != nil {
			return nil, err
		}
		rules := make([]any, 0, len(parsed))
		for _, draft := range parsed {
			rules = append(rules, draft.raw)
		}
		return rules, nil
	}
	copyRules := make([]any, 0, len(drafts))
	for _, draft := range drafts {
		source := *s.serviceEditorText("copy:"+draft.id+":source", draft.source)
		target := *s.serviceEditorText("copy:"+draft.id+":target", draft.target)
		if strings.TrimSpace(source) == "" || strings.TrimSpace(target) == "" {
			return nil, fmt.Errorf("复制规则的源路径和目标路径都需要填写")
		}
		if err := validateRelativeConfigPath(source, "复制源"); err != nil {
			return nil, err
		}
		if err := validateRelativeConfigPath(target, "复制目标"); err != nil {
			return nil, err
		}
		rule := cloneConfigMap(draft.raw)
		if rule == nil {
			rule = map[string]any{}
		}
		rule["source"], rule["target"] = source, target
		rule["overwrite"] = *s.serviceEditorBool("copy:"+draft.id+":overwrite", mapBool(draft.raw, "overwrite", true))
		copyRules = append(copyRules, rule)
	}
	return copyRules, nil
}

func (s *app) bootstrapCommandConfig(state *serviceEditorState) ([]any, error) {
	drafts := state.commands
	if state.commandsMode == "json" {
		parsed, err := parseBootstrapCommands(*s.serviceEditorText("commands-json", "[]"), state.commands)
		if err != nil {
			return nil, err
		}
		commands := make([]any, 0, len(parsed))
		for _, draft := range parsed {
			commands = append(commands, draft.raw)
		}
		return commands, nil
	}
	commands := make([]any, 0, len(drafts))
	for _, draft := range drafts {
		command := workspaceCommandDraft{raw: draft.raw, id: draft.id, name: draft.name,
			executable: draft.executable, arguments: draft.arguments, directory: draft.directory, timeout: draft.timeout}
		commandMap, err := s.buildWorkspaceCommand("bootstrap", command, false)
		if err != nil {
			return nil, err
		}
		commands = append(commands, commandMap)
	}
	return commands, nil
}

func parseBootstrapObjects(text string) ([]map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("JSON 格式错误：%w", err)
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		return nil, fmt.Errorf("JSON 后存在多余内容")
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed[0] != '[' {
		return nil, fmt.Errorf("JSON 必须是对象数组")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("JSON 格式错误：%w", err)
	}
	objects := make([]map[string]any, 0, len(items))
	for index, item := range items {
		itemDecoder := json.NewDecoder(strings.NewReader(string(item)))
		itemDecoder.UseNumber()
		var object map[string]any
		if err := itemDecoder.Decode(&object); err != nil || object == nil {
			return nil, fmt.Errorf("第 %d 项必须是 JSON 对象", index+1)
		}
		objects = append(objects, object)
	}
	return objects, nil
}

func parseBootstrapCopies(text string, existing []copyRuleDraft) ([]copyRuleDraft, error) {
	objects, err := parseBootstrapObjects(text)
	if err != nil {
		return nil, err
	}
	drafts := make([]copyRuleDraft, 0, len(objects))
	for index, object := range objects {
		source, ok := object["source"].(string)
		if !ok || strings.TrimSpace(source) == "" {
			return nil, fmt.Errorf("第 %d 项的 source 必须是非空字符串", index+1)
		}
		target := source
		if rawTarget, exists := object["target"]; exists {
			var valid bool
			target, valid = rawTarget.(string)
			if !valid {
				return nil, fmt.Errorf("第 %d 项的 target 必须是字符串", index+1)
			}
		}
		if strings.TrimSpace(target) == "" {
			return nil, fmt.Errorf("第 %d 项的 target 不能为空", index+1)
		}
		overwrite := true
		if rawOverwrite, exists := object["overwrite"]; exists {
			var valid bool
			overwrite, valid = rawOverwrite.(bool)
			if !valid {
				return nil, fmt.Errorf("第 %d 项的 overwrite 必须是布尔值", index+1)
			}
		}
		if err := validateRelativeConfigPath(source, "复制源"); err != nil {
			return nil, fmt.Errorf("第 %d 项：%w", index+1, err)
		}
		if err := validateRelativeConfigPath(target, "复制目标"); err != nil {
			return nil, fmt.Errorf("第 %d 项：%w", index+1, err)
		}
		object["target"], object["overwrite"] = target, overwrite
		drafts = append(drafts, copyRuleDraft{raw: object, id: newDesktopEditorID("copy"), source: source, target: target})
	}
	return reconcileCopyDraftIDs(existing, drafts), nil
}

func parseBootstrapCommands(text string, existing []bootstrapCommandDraft) ([]bootstrapCommandDraft, error) {
	objects, err := parseBootstrapObjects(text)
	if err != nil {
		return nil, err
	}
	drafts := make([]bootstrapCommandDraft, 0, len(objects))
	for index, object := range objects {
		stringField := func(key string, fallback string) (string, error) {
			value, exists := object[key]
			if !exists {
				if fallback != "" {
					return fallback, nil
				}
				return "", fmt.Errorf("第 %d 项的 %s 必须是字符串", index+1, key)
			}
			text, ok := value.(string)
			if !ok {
				return "", fmt.Errorf("第 %d 项的 %s 必须是字符串", index+1, key)
			}
			return text, nil
		}
		name, err := stringField("name", "")
		if err != nil {
			return nil, err
		}
		executable, err := stringField("executable", "")
		if err != nil {
			return nil, err
		}
		directory, err := stringField("workingDirectory", ".")
		if err != nil {
			return nil, err
		}
		args := []string{}
		if rawArgs, exists := object["arguments"]; exists {
			values, ok := rawArgs.([]any)
			if !ok {
				return nil, fmt.Errorf("第 %d 项的 arguments 必须是字符串数组", index+1)
			}
			for _, value := range values {
				argument, ok := value.(string)
				if !ok {
					return nil, fmt.Errorf("第 %d 项的 arguments 必须是字符串数组", index+1)
				}
				args = append(args, argument)
			}
		}
		timeout := int64(600)
		if rawTimeout, exists := object["timeoutSeconds"]; exists {
			number, ok := rawTimeout.(json.Number)
			if !ok {
				return nil, fmt.Errorf("第 %d 项的 timeoutSeconds 必须是 JSON 整数", index+1)
			}
			parsed, parseErr := parsePositiveInt(string(number))
			if parseErr != nil {
				return nil, fmt.Errorf("第 %d 项的 timeoutSeconds 必须是大于 0 的整数", index+1)
			}
			timeout = parsed
		}
		enabled := true
		if rawEnabled, exists := object["enabled"]; exists {
			var ok bool
			enabled, ok = rawEnabled.(bool)
			if !ok {
				return nil, fmt.Errorf("第 %d 项的 enabled 必须是布尔值", index+1)
			}
		}
		if strings.TrimSpace(name) == "" || strings.TrimSpace(executable) == "" || strings.TrimSpace(directory) == "" {
			return nil, fmt.Errorf("第 %d 项的名称、可执行程序和工作目录不能为空", index+1)
		}
		object["arguments"], object["workingDirectory"], object["timeoutSeconds"], object["enabled"] = args, directory, timeout, enabled
		drafts = append(drafts, bootstrapCommandDraft{raw: object, id: newDesktopEditorID("bootstrap-command"),
			name: name, executable: executable, arguments: strings.Join(args, "\n"), directory: directory, timeout: fmt.Sprint(timeout)})
	}
	return reconcileBootstrapCommandDraftIDs(existing, drafts), nil
}

func reconcileBootstrapCommandDraftIDs(existing, parsed []bootstrapCommandDraft) []bootstrapCommandDraft {
	used := make(map[string]bool)
	for index := range parsed {
		for _, candidate := range existing {
			if used[candidate.id] || candidate.name != parsed[index].name || candidate.executable != parsed[index].executable ||
				candidate.arguments != parsed[index].arguments || candidate.directory != parsed[index].directory || candidate.timeout != parsed[index].timeout ||
				mapBool(candidate.raw, "enabled", true) != mapBool(parsed[index].raw, "enabled", true) {
				continue
			}
			if configJSONWithoutFields(candidate.raw, "name", "executable", "arguments", "workingDirectory", "timeoutSeconds", "enabled") ==
				configJSONWithoutFields(parsed[index].raw, "name", "executable", "arguments", "workingDirectory", "timeoutSeconds", "enabled") {
				parsed[index].id = candidate.id
				used[candidate.id] = true
				break
			}
		}
	}
	return parsed
}

func reconcileCopyDraftIDs(existing, parsed []copyRuleDraft) []copyRuleDraft {
	used := make(map[string]bool)
	for index := range parsed {
		for _, candidate := range existing {
			if used[candidate.id] || candidate.source != parsed[index].source || candidate.target != parsed[index].target || mapBool(candidate.raw, "overwrite", true) != mapBool(parsed[index].raw, "overwrite", true) {
				continue
			}
			if configJSONWithoutFields(candidate.raw, "source", "target", "overwrite") == configJSONWithoutFields(parsed[index].raw, "source", "target", "overwrite") {
				parsed[index].id = candidate.id
				used[candidate.id] = true
				break
			}
		}
	}
	return parsed
}

func configJSONWithoutFields(value map[string]any, omitted ...string) string {
	copy := cloneConfigMap(value)
	for _, key := range omitted {
		delete(copy, key)
	}
	encoded, _ := json.Marshal(copy)
	return string(encoded)
}

func (s *app) buildServiceEditorConfig() (map[string]any, error) {
	state := s.workspaceUnit.state.serviceEditor
	if state == nil {
		return nil, fmt.Errorf("服务编辑状态已关闭")
	}
	service := cloneConfigMap(state.service)
	if service == nil {
		service = map[string]any{}
	}
	serviceID := strings.TrimSpace(*s.serviceEditorText("serviceId", state.serviceID))
	groupID := strings.TrimSpace(*s.serviceEditorText("groupId", state.groupID))
	repositoryID := strings.TrimSpace(*s.serviceEditorText("repositoryId", mapString(state.service, "repositoryId")))
	name := strings.TrimSpace(*s.serviceEditorText("name", mapString(state.service, "displayName")))
	masterBranch := strings.TrimSpace(*s.serviceEditorText("masterBranch", mapString(state.service, "masterBranch")))
	testTagBaselineRef := strings.TrimSpace(*s.serviceEditorText("testTagBaselineRef", mapString(state.service, "testTagBaselineRef")))
	if serviceID == "" || groupID == "" || repositoryID == "" || name == "" {
		return nil, fmt.Errorf("项目组、服务 ID、名称和仓库均不能为空")
	}
	if masterBranch == "" || !validRemoteBranch(masterBranch) {
		return nil, fmt.Errorf("主分支必须是 remote/branch 格式")
	}
	if testTagBaselineRef == "" || !validRemoteBranch(testTagBaselineRef) {
		return nil, fmt.Errorf("测试 Tag 默认目标必须是 remote/branch 格式")
	}
	service["id"], service["repositoryId"], service["displayName"] = serviceID, repositoryID, name
	service["masterBranch"], service["testTagBaselineRef"] = masterBranch, testTagBaselineRef
	service["enabled"] = *s.serviceEditorBool("enabled", mapBool(state.service, "enabled", true))
	service["developmentTool"] = firstNonEmpty(*s.serviceEditorText("developmentTool", mapString(state.service, "developmentTool")), "INTELLIJ_IDEA")
	service["genbuProbeEnabled"] = *s.serviceEditorBool("genbuProbeEnabled", mapBool(state.service, "genbuProbeEnabled", false))
	genbuName := strings.TrimSpace(*s.serviceEditorText("genbuServiceName", firstNonEmpty(mapString(state.service, "genbuServiceName"), name)))
	if *s.serviceEditorBool("genbuProbeEnabled", mapBool(state.service, "genbuProbeEnabled", false)) && genbuName == "" {
		return nil, fmt.Errorf("启用 Genbu 探测时必须填写 Genbu 服务名")
	}
	service["genbuServiceName"] = genbuName
	service["commitMessageTemplate"] = *s.serviceEditorText("commitMessageTemplate", mapString(state.service, "commitMessageTemplate"))
	modules := make([]any, 0, len(state.modules))
	if len(state.modules) == 0 {
		return nil, fmt.Errorf("至少需要一个模块")
	}
	seenModuleIDs := map[string]bool{}
	seenModuleNames, seenModuleDirectories := map[string]bool{}, map[string]bool{}
	for _, draft := range state.modules {
		id := strings.TrimSpace(draft.id)
		name := strings.TrimSpace(*s.serviceEditorModuleText(valueModule(state.modules, draft.id), "name", draft.name))
		if name == "" {
			name = "default"
		}
		strategy := *s.serviceEditorModuleText(valueModule(state.modules, draft.id), "strategy", draft.strategy)
		if id == "" {
			return nil, fmt.Errorf("每个模块都必须填写 ID")
		}
		if !validServiceModuleName(name) {
			return nil, fmt.Errorf("模块 %s 名称不合法：只允许英文字母、数字、-、_、/，且 / 不能连续或位于首尾", id)
		}
		if seenModuleIDs[strings.ToLower(id)] {
			return nil, fmt.Errorf("模块 ID 重复：%s", id)
		}
		seenModuleIDs[strings.ToLower(id)] = true
		if len(state.modules) > 1 {
			nameKey := strings.ToLower(name)
			directoryKey := strings.ToLower(strings.ReplaceAll(name, "/", "-"))
			if seenModuleNames[nameKey] {
				return nil, fmt.Errorf("多模块名称不能重复（忽略大小写）：%s", name)
			}
			if seenModuleDirectories[directoryKey] {
				return nil, fmt.Errorf("模块名称转换为目录后不能重复：%s", name)
			}
			seenModuleNames[nameKey], seenModuleDirectories[directoryKey] = true, true
		}
		if strategy != "STANDARD_WORKTREE" && strategy != "INDEPENDENT_CLONE" {
			return nil, fmt.Errorf("模块 %s 的工作区策略无效", name)
		}
		module := cloneConfigMap(draft.raw)
		if module == nil {
			module = map[string]any{}
		}
		module["id"], module["name"], module["strategy"] = id, name, strategy
		inheritMaster := *s.serviceEditorBool("module:"+id+":inherit-master", draft.masterBranch == "")
		if inheritMaster {
			module["masterBranch"] = nil
		} else {
			branch := strings.TrimSpace(*s.serviceEditorText("module:"+id+":masterBranch", draft.masterBranch))
			if !validRemoteBranch(branch) {
				return nil, fmt.Errorf("模块 %s 主分支必须是 remote/branch 格式", name)
			}
			module["masterBranch"] = branch
		}
		tagEnabled := *s.serviceEditorBool("module:"+id+":tagEnabled", mapBool(draft.raw, "tagEnabled", strategy == "STANDARD_WORKTREE"))
		tagMode := *s.serviceEditorModuleText(valueModule(state.modules, id), "tagMode", draft.tagMode)
		if tagMode != "MERGE_TO_TARGET_BRANCH" && tagMode != "CURRENT_BRANCH" {
			return nil, fmt.Errorf("模块 %s 的 Tag 模式无效", name)
		}
		module["tagEnabled"], module["tagMode"] = tagEnabled, tagMode
		module["tagMessagePrefix"] = strings.TrimSpace(*s.serviceEditorModuleText(valueModule(state.modules, id), "tagMessagePrefix", draft.tagMessagePrefix))
		if tagMode == "CURRENT_BRANCH" || *s.serviceEditorBool("module:"+id+":inherit-tag-target", draft.tagTargetRef == "") {
			module["tagTargetRef"] = nil
		} else {
			branch := strings.TrimSpace(*s.serviceEditorText("module:"+id+":tagTargetRef", draft.tagTargetRef))
			if !validRemoteBranch(branch) {
				return nil, fmt.Errorf("模块 %s Tag 目标必须是 remote/branch 格式", name)
			}
			module["tagTargetRef"] = branch
		}
		commands := make([]any, 0, len(draft.commands))
		for _, command := range draft.commands {
			commandMap, err := s.buildWorkspaceCommand("module:"+id, command, true)
			if err != nil {
				return nil, fmt.Errorf("模块 %s：%w", name, err)
			}
			commands = append(commands, commandMap)
		}
		module["customCommands"] = commands
		modules = append(modules, module)
	}
	service["modules"] = modules
	bootstrap := cloneConfigMap(mapData(service["bootstrap"]))
	if bootstrap == nil {
		bootstrap = map[string]any{}
	}
	copyRules, err := s.bootstrapCopyConfig(state)
	if err != nil {
		return nil, err
	}
	bootstrapCommands, err := s.bootstrapCommandConfig(state)
	if err != nil {
		return nil, err
	}
	bootstrap["copyRules"], bootstrap["commands"] = copyRules, bootstrapCommands
	service["bootstrap"] = bootstrap
	return service, nil
}

func validServiceModuleName(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" {
			return false
		}
		for _, char := range segment {
			if !((char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' || char == '_') {
				return false
			}
		}
	}
	return true
}

func valueModule(modules []serviceModuleDraft, id string) *serviceModuleDraft {
	for index := range modules {
		if modules[index].id == id {
			return &modules[index]
		}
	}
	return &serviceModuleDraft{id: id}
}

func validRemoteBranch(value string) bool {
	parts := strings.SplitN(strings.TrimSpace(value), "/", 2)
	return len(parts) == 2 && strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != "" && !strings.ContainsAny(value, " \t\r\n")
}

func (s *app) buildWorkspaceCommand(owner string, draft workspaceCommandDraft, module bool) (map[string]any, error) {
	purpose := serviceCommandForBootstrap
	if module {
		purpose = serviceCommandForModule
	}
	values := serviceCommandValues{
		raw: draft.raw, ID: draft.id,
		Name:        *s.serviceCommandText(owner, &draft, "name", draft.name),
		Program:     *s.serviceCommandText(owner, &draft, "executable", draft.executable),
		Directory:   *s.serviceCommandText(owner, &draft, "directory", firstNonEmpty(draft.directory, ".")),
		TimeoutText: *s.serviceCommandText(owner, &draft, "timeout", firstNonEmpty(draft.timeout, "600")),
	}
	// Preserve lazy input/baseline initialization: failed required fields must
	// not initialize argument, enabled or icon inputs that were not read before.
	timeout, err := validateServiceCommandValues(values, purpose)
	if err != nil {
		return nil, err
	}
	values.ArgsText = *s.serviceCommandText(owner, &draft, "arguments", draft.arguments)
	if module || owner == "bootstrap" {
		values.Enabled = *s.serviceEditorBool(owner+":"+draft.id+":enabled", mapBool(draft.raw, "enabled", true))
	}
	if module {
		// ID allocation belongs to the editor, including its existing fallback
		// evaluation. The value unit only validates and preserves the chosen ID.
		values.ID = firstNonEmpty(draft.id, newDesktopEditorID("command"))
		values.IconKey = *s.serviceCommandText(owner, &draft, "iconKey", draft.iconKey)
	}
	return serviceCommandConfig(values, purpose, timeout), nil
}

func (s *app) submitServiceEditor() {
	state := s.workspaceUnit.state.serviceEditor
	if state == nil || s.busy {
		return
	}
	service, err := s.buildServiceEditorConfig()
	if err != nil {
		state.error = err.Error()
		return
	}
	groupID := strings.TrimSpace(*s.serviceEditorText("groupId", state.groupID))
	serviceID := mapString(service, "id")
	s.cancelServiceRepositoryChoiceReads()
	state.saving, state.error = true, ""
	s.act("service.save", "", serviceID, "", "", "", map[string]any{"groupId": groupID, "serviceId": serviceID, "service": service})
}

func removeServiceModule(items []serviceModuleDraft, id string) []serviceModuleDraft {
	result := make([]serviceModuleDraft, 0, len(items))
	for _, item := range items {
		if item.id != id {
			result = append(result, item)
		}
	}
	return result
}

func removeWorkspaceCommand(items []workspaceCommandDraft, id string) []workspaceCommandDraft {
	result := make([]workspaceCommandDraft, 0, len(items))
	for _, item := range items {
		if item.id != id {
			result = append(result, item)
		}
	}
	return result
}

func removeCopyRule(items []copyRuleDraft, id string) []copyRuleDraft {
	result := make([]copyRuleDraft, 0, len(items))
	for _, item := range items {
		if item.id != id {
			result = append(result, item)
		}
	}
	return result
}

func removeBootstrapCommand(items []bootstrapCommandDraft, id string) []bootstrapCommandDraft {
	result := make([]bootstrapCommandDraft, 0, len(items))
	for _, item := range items {
		if item.id != id {
			result = append(result, item)
		}
	}
	return result
}
