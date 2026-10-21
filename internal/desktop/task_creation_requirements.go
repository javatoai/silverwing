package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

type taskCreationRequirementCandidate struct {
	Title    string
	URL      string
	SourceID string
}

type taskCreationRequirementFailure struct {
	Stage        string `json:"stage"`
	ProjectKey   string `json:"projectKey"`
	SprintID     string `json:"sprintId"`
	WorkItemType string `json:"workItemType"`
	Message      string `json:"message"`
}

// taskCreationRequirementsState owns candidate and metadata reads for one task
// creation form. Each read is independent of the desktop's global busy action.
type taskCreationRequirementsState struct {
	candidates        []taskCreationRequirementCandidate
	candidatesLoading bool
	candidatesLoaded  bool
	candidatesError   string
	candidatesWarning string
	candidatesOpen    bool
	query             string
	inputLink         string
	inputIsQuery      bool
	selectedTitle     string

	candidateGeneration uint64
	metadataGeneration  uint64
	candidateRequest    *taskCreationRequirementRequest
	metadataRequest     *taskCreationRequirementRequest

	scope    taskCreationRequirementsScope
	hasScope bool
	closed   bool
}

type taskCreationRequirementsScope struct {
	token, home, groupID, link, config string
	listIdentity                       requirementListIdentity
	identityContext                    requirementIdentityContext
}

type taskCreationRequirementRequest struct {
	form       *taskCreationState
	token      string
	home       string
	groupID    string
	link       string
	config     string
	identity   requirementListIdentity
	profile    requirementIdentityContext
	account    string
	accountSet bool
	generation uint64
	ctx        context.Context
	cancel     context.CancelFunc
}

func (s *app) ensureTaskCreationRequirements() *taskCreationRequirementsState {
	form := s.taskCreation
	if form == nil {
		return nil
	}
	state := form.requirements
	if state == nil {
		state = &taskCreationRequirementsState{query: form.draft.RequirementLink, inputLink: form.draft.RequirementLink,
			selectedTitle: taskCreationRequirementTitle(form.draft.RequirementTitle)}
		form.requirements = state
	}
	if state.closed {
		return state
	}
	if state.inputLink != form.draft.RequirementLink {
		state.query, state.inputLink = form.draft.RequirementLink, form.draft.RequirementLink
		state.inputIsQuery = false
		state.selectedTitle = taskCreationRequirementTitle(form.draft.RequirementTitle)
	}

	next := s.taskCreationRequirementsScope(form)
	if !state.hasScope {
		state.scope, state.hasScope = next, true
		s.handleTaskCreationRequirementLinkChange(form, state, next.link)
	} else {
		previous := state.scope
		if !taskCreationRequirementBaseScopeEqual(previous, next) {
			s.cancelTaskCreationRequirementCandidates(state)
			s.cancelTaskCreationRequirementMetadata(state)
			state.candidates = nil
			state.candidatesLoaded = false
			state.candidatesError = ""
			state.candidatesWarning = ""
			state.scope = next
			if previous.link != next.link {
				s.handleTaskCreationRequirementLinkChange(form, state, next.link)
			}
		} else if previous.link != next.link {
			s.cancelTaskCreationRequirementCandidates(state)
			s.cancelTaskCreationRequirementMetadata(state)
			state.scope = next
			s.handleTaskCreationRequirementLinkChange(form, state, next.link)
		}
	}

	if form.draft.MetadataLoading && domain.ParseFeishuWorkItemLink(form.draft.RequirementLink) != nil && state.metadataRequest == nil {
		s.startTaskCreationRequirementMetadata(form, state)
	}
	if !state.candidatesLoaded && !state.candidatesLoading && len(form.config.MeegleProjects) > 0 {
		s.startTaskCreationRequirementCandidates(form, state)
	}
	return state
}

func taskCreationRequirementBaseScopeEqual(a, b taskCreationRequirementsScope) bool {
	return a.token == b.token && a.home == b.home && a.groupID == b.groupID && a.config == b.config &&
		a.listIdentity == b.listIdentity && a.identityContext == b.identityContext
}

func (s *app) taskCreationRequirementsScope(form *taskCreationState) taskCreationRequirementsScope {
	return taskCreationRequirementsScope{
		token: form.token, home: s.snapshot.Home, groupID: form.groupID,
		link: form.draft.RequirementLink, config: taskCreationRequirementsConfigFingerprint(s.snapshot.ConfigJSON, form.config),
		listIdentity: s.currentRequirementListIdentity(), identityContext: s.currentRequirementIdentityContext(),
	}
}

func taskCreationRequirementsConfigFingerprint(snapshot string, config domain.AppConfig) string {
	encoded, _ := json.Marshal(config)
	hash := sha256.New()
	_, _ = hash.Write([]byte(snapshot))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(encoded)
	return hex.EncodeToString(hash.Sum(nil))
}

func (s *app) handleTaskCreationRequirementLinkChange(form *taskCreationState, state *taskCreationRequirementsState, link string) {
	if form == nil || state == nil {
		return
	}
	if domain.ParseFeishuWorkItemLink(link) == nil {
		form.draft = form.draft.ApplyMetadata(link, nil)
		return
	}
	if !form.draft.MetadataLoading {
		form.draft.MetadataLoading = true
	}
	s.startTaskCreationRequirementMetadata(form, state)
}

func (s *app) startTaskCreationRequirementCandidates(form *taskCreationState, state *taskCreationRequirementsState) {
	if form == nil || state == nil || state.closed || state.candidatesLoading || state.candidatesLoaded {
		return
	}
	if s.backend == nil {
		state.candidatesLoaded = true
		state.candidatesError = "需求列表读取服务不可用。"
		return
	}
	if s.win == nil || s.win.IsDestroyed() {
		state.candidatesLoaded = true
		state.candidatesError = "需求列表读取窗口不可用，请重新打开任务创建表单。"
		return
	}
	identity, expected, identityErr := taskCreationExpectedRequirementIdentity(s)
	if identityErr != nil {
		state.candidatesLoaded = true
		state.candidatesError = identityErr.Error()
		return
	}
	state.candidateGeneration++
	ctx, cancel := context.WithCancel(context.Background())
	request := &taskCreationRequirementRequest{
		form: form, token: form.token, home: s.snapshot.Home, groupID: form.groupID, link: form.draft.RequirementLink,
		config:   taskCreationRequirementsConfigFingerprint(s.snapshot.ConfigJSON, form.config),
		identity: s.currentRequirementListIdentity(), profile: s.currentRequirementIdentityContext(), account: identity, accountSet: expected,
		generation: state.candidateGeneration, ctx: ctx, cancel: cancel,
	}
	state.candidateRequest = request
	state.candidatesLoading = true
	state.candidatesError, state.candidatesWarning = "", ""
	action := application.Action{Kind: "requirements.candidates", Payload: map[string]any{}}
	backend, win := s.backend, s.win
	go func() {
		account, hasAccount, err := request.account, request.accountSet, error(nil)
		if !hasAccount {
			account, hasAccount, err = taskCreationRequirementIdentityBeforeRead(ctx, backend, "", false)
		}
		if hasAccount {
			action.Payload["expectedIdentity"] = account
		}
		var result application.ActionResult
		if err == nil {
			result, err = backend.Dispatch(ctx, action)
		}
		if ctx.Err() != nil {
			err = errors.Join(ctx.Err(), err)
		}
		if win.IsDestroyed() {
			cancel()
			return
		}
		updated := make(chan struct{})
		win.Update(func() {
			defer close(updated)
			s.finishTaskCreationRequirementCandidates(request, result, err)
		})
		select {
		case <-updated:
		case <-ctx.Done():
		}
		cancel()
	}()
}

func taskCreationExpectedRequirementIdentity(s *app) (string, bool, error) {
	state := s.requirementIdentityJobs
	if state == nil || !state.confirmed {
		return "", false, nil
	}
	if state.state == application.RequirementIdentityLoggedOut {
		return "", false, errors.New("Meegle 已退出登录，请重新登录后读取需求。")
	}
	if state.state == application.RequirementIdentityAuthenticated && strings.TrimSpace(state.identity) != "" {
		return state.identity, true, nil
	}
	return "", false, nil
}

// Source binds metadata reads to the active Meegle profile. The application
// identity interface is read-only; unavailable identity keeps the same
// uncached-read behavior while request generations still guard the UI.
func taskCreationRequirementIdentityBeforeRead(ctx context.Context, backend application.Backend, expected string, confirmed bool) (string, bool, error) {
	provider, ok := backend.(application.RequirementIdentityBackend)
	if !ok {
		return expected, confirmed, nil
	}
	snapshot, err := provider.RequirementIdentity(ctx)
	if ctx.Err() != nil {
		return "", false, ctx.Err()
	}
	if err != nil {
		if confirmed {
			return "", false, &application.RequirementIdentityChangedError{Snapshot: application.RequirementIdentitySnapshot{State: application.RequirementIdentityUnavailable}}
		}
		return "", false, nil
	}
	switch snapshot.State {
	case application.RequirementIdentityLoggedOut:
		return "", false, &application.RequirementIdentityChangedError{Snapshot: snapshot}
	case application.RequirementIdentityAuthenticated:
		identity := strings.TrimSpace(snapshot.Identity)
		if identity == "" {
			break
		}
		if confirmed && identity != expected {
			return "", false, &application.RequirementIdentityChangedError{Snapshot: snapshot}
		}
		return identity, true, nil
	}
	if confirmed {
		return "", false, &application.RequirementIdentityChangedError{Snapshot: snapshot}
	}
	return "", false, nil
}

func taskCreationRequirementIdentityAfterRead(ctx context.Context, backend application.Backend, expected string) error {
	provider, ok := backend.(application.RequirementIdentityBackend)
	if !ok || expected == "" {
		return nil
	}
	snapshot, err := provider.RequirementIdentity(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || snapshot.State != application.RequirementIdentityAuthenticated || strings.TrimSpace(snapshot.Identity) != expected {
		if err != nil {
			snapshot = application.RequirementIdentitySnapshot{State: application.RequirementIdentityUnavailable}
		}
		return &application.RequirementIdentityChangedError{Snapshot: snapshot}
	}
	return nil
}

func (s *app) finishTaskCreationRequirementCandidates(request *taskCreationRequirementRequest, result application.ActionResult, requestErr error) {
	if !s.taskCreationRequirementRequestRelevant(request, false) {
		return
	}
	state := request.form.requirements
	state.candidateRequest = nil
	state.candidatesLoading = false
	if requestErr != nil {
		var changed *application.RequirementIdentityChangedError
		if errors.As(requestErr, &changed) {
			s.applyRequirementListIdentityFailure(changed.Snapshot, requestErr)
		}
		state.candidates = nil
		state.candidatesLoaded = true
		state.candidatesError = strings.TrimSpace(requestErr.Error())
		return
	}
	data := mapData(result.Data)
	items, err := decodeData[application.RequirementCandidate](data["candidates"])
	if err != nil {
		state.candidates = nil
		state.candidatesLoaded = true
		state.candidatesError = "需求候选列表格式错误：" + err.Error()
		return
	}
	candidates := make([]taskCreationRequirementCandidate, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.URL) == "" {
			continue
		}
		candidates = append(candidates, taskCreationRequirementCandidate{Title: item.Title, URL: item.URL, SourceID: item.SourceID})
	}
	state.candidates = candidates
	state.candidatesLoaded = true
	failures, err := decodeData[taskCreationRequirementFailure](data["failures"])
	if err != nil {
		state.candidatesError = "需求候选失败说明格式错误：" + err.Error()
		return
	}
	completed := int(dataInt64(data, "completedQueries"))
	if len(failures) > 0 {
		detail := taskCreationRequirementFailureSummary(failures[0])
		if completed == 0 && len(candidates) == 0 {
			state.candidatesError = "读取需求候选失败：" + detail
		} else {
			state.candidatesWarning = fmt.Sprintf("部分需求读取失败（%d 项）：%s", len(failures), detail)
		}
	}
}

func taskCreationRequirementFailureSummary(failure taskCreationRequirementFailure) string {
	context := make([]string, 0, 3)
	if failure.ProjectKey != "" {
		context = append(context, "项目 "+failure.ProjectKey)
	}
	if failure.SprintID != "" {
		context = append(context, "Sprint "+failure.SprintID)
	}
	if failure.WorkItemType != "" {
		context = append(context, failure.WorkItemType)
	}
	stage := failure.Stage
	switch stage {
	case "query":
		stage = "查询"
	case "parse":
		stage = "解析"
	}
	message := strings.TrimSpace(failure.Message)
	if len(context) > 0 {
		return fmt.Sprintf("%s（%s）：%s", stage, strings.Join(context, " · "), message)
	}
	return fmt.Sprintf("%s：%s", stage, message)
}

func (s *app) startTaskCreationRequirementMetadata(form *taskCreationState, state *taskCreationRequirementsState) {
	if form == nil || state == nil || state.closed || state.metadataRequest != nil || !form.draft.MetadataLoading {
		return
	}
	link := form.draft.RequirementLink
	if domain.ParseFeishuWorkItemLink(link) == nil {
		form.draft = form.draft.ApplyMetadata(link, nil)
		return
	}
	if s.backend == nil {
		form.draft = form.draft.ApplyMetadata(link, nil)
		return
	}
	if s.win == nil || s.win.IsDestroyed() {
		form.draft = form.draft.ApplyMetadata(link, nil)
		return
	}
	state.metadataGeneration++
	ctx, cancel := context.WithCancel(context.Background())
	request := &taskCreationRequirementRequest{
		form: form, token: form.token, home: s.snapshot.Home, groupID: form.groupID, link: link,
		config:   taskCreationRequirementsConfigFingerprint(s.snapshot.ConfigJSON, form.config),
		identity: s.currentRequirementListIdentity(), profile: s.currentRequirementIdentityContext(),
		generation: state.metadataGeneration, ctx: ctx, cancel: cancel,
	}
	if identity, confirmed, identityErr := taskCreationExpectedRequirementIdentity(s); identityErr != nil {
		form.draft = form.draft.ApplyMetadata(link, nil)
		return
	} else {
		request.account, request.accountSet = identity, confirmed
	}
	state.metadataRequest = request
	backend, win := s.backend, s.win
	go func() {
		wait := time.NewTimer(250 * time.Millisecond)
		defer wait.Stop()
		select {
		case <-ctx.Done():
			cancel()
			return
		case <-wait.C:
		}
		account, hasAccount, err := taskCreationRequirementIdentityBeforeRead(ctx, backend, request.account, request.accountSet)
		var result application.ActionResult
		if err == nil {
			result, err = backend.Dispatch(ctx, application.Action{Kind: "requirements.metadata", Path: link})
		}
		if err == nil && hasAccount {
			err = taskCreationRequirementIdentityAfterRead(ctx, backend, account)
		}
		if ctx.Err() != nil {
			err = errors.Join(ctx.Err(), err)
		}
		if win.IsDestroyed() {
			cancel()
			return
		}
		updated := make(chan struct{})
		win.Update(func() {
			defer close(updated)
			s.finishTaskCreationRequirementMetadata(request, result, err)
		})
		select {
		case <-updated:
		case <-ctx.Done():
		}
		cancel()
	}()
}

func (s *app) finishTaskCreationRequirementMetadata(request *taskCreationRequirementRequest, result application.ActionResult, requestErr error) {
	if !s.taskCreationRequirementRequestRelevant(request, true) {
		return
	}
	state := request.form.requirements
	state.metadataRequest = nil
	var metadata *domain.RequirementMetadata
	if requestErr == nil {
		var decoded domain.RequirementMetadata
		if err := decodeTaskCreationRequirementValue(result.Data, &decoded); err == nil {
			metadata = &decoded
		} else {
			requestErr = fmt.Errorf("需求元数据格式错误：%w", err)
		}
	}
	if requestErr != nil {
		var changed *application.RequirementIdentityChangedError
		if errors.As(requestErr, &changed) {
			s.applyRequirementListIdentityFailure(changed.Snapshot, requestErr)
		}
		metadata = nil
	}
	request.form.draft = request.form.draft.ApplyMetadata(request.link, metadata)
}

func decodeTaskCreationRequirementValue(data any, target any) error {
	if data == nil {
		return errors.New("应用未返回需求元数据")
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}

func (s *app) taskCreationRequirementRequestRelevant(request *taskCreationRequirementRequest, metadata bool) bool {
	if s == nil || request == nil || request.ctx == nil || request.ctx.Err() != nil || s.win == nil || s.win.IsDestroyed() {
		return false
	}
	form := s.taskCreation
	if form == nil || form != request.form || form.requirements == nil || form.requirements.closed || form.token != request.token ||
		s.snapshot.Home != request.home || form.groupID != request.groupID ||
		taskCreationRequirementsConfigFingerprint(s.snapshot.ConfigJSON, form.config) != request.config ||
		s.currentRequirementListIdentity() != request.identity || s.currentRequirementIdentityContext() != request.profile {
		return false
	}
	state := form.requirements
	if metadata {
		return form.draft.RequirementLink == request.link && state.metadataRequest == request && state.metadataGeneration == request.generation
	}
	return form.draft.RequirementLink == request.link && state.candidateRequest == request && state.candidateGeneration == request.generation
}

func (s *app) cancelTaskCreationRequirementCandidates(state *taskCreationRequirementsState) {
	if state == nil {
		return
	}
	state.candidateGeneration++
	if state.candidateRequest != nil {
		state.candidateRequest.cancel()
		state.candidateRequest = nil
	}
	state.candidatesLoading = false
}

func (s *app) cancelTaskCreationRequirementMetadata(state *taskCreationRequirementsState) {
	if state == nil {
		return
	}
	state.metadataGeneration++
	if state.metadataRequest != nil {
		state.metadataRequest.cancel()
		state.metadataRequest = nil
	}
}

func (s *app) closeTaskCreationRequirements() {
	if s == nil || s.taskCreation == nil || s.taskCreation.requirements == nil {
		return
	}
	state := s.taskCreation.requirements
	state.closed = true
	s.cancelTaskCreationRequirementCandidates(state)
	s.cancelTaskCreationRequirementMetadata(state)
}

func (s *app) selectTaskCreationRequirementCandidate(candidate taskCreationRequirementCandidate) {
	form := s.taskCreation
	if form == nil || form.requirements == nil || form.requirements.closed {
		return
	}
	oldBranch := form.draft.Branch
	s.cancelTaskCreationRequirementMetadata(form.requirements)
	title := candidate.Title
	form.draft = form.draft.ChangeRequirement(candidate.URL, form.config.DefaultBranchPrefix, &title)
	form.lastLink = form.draft.RequirementLink
	form.lastBranch = form.draft.Branch
	form.lastName = form.draft.TaskName
	form.requirements.query, form.requirements.inputLink = candidate.URL, candidate.URL
	form.requirements.inputIsQuery = false
	form.requirements.selectedTitle = candidate.Title
	form.aiEligible = true
	form.aiError = ""
	if oldBranch != form.draft.Branch {
		s.retargetUntouchedTaskCreationModules(form.draft.Branch)
	}
	s.invalidateTaskCreationPreview("需求候选已变化，请重新预检。")
	if form.config.AIRequirementNamingEnabled {
		s.requestTaskCreationAINaming(false)
	}
}

// A title is a candidate filter, never a requirement link in the request.
// Preserve direct URL and numeric work-item entry, including clearing it.
func taskCreationRequirementInputLink(input string) (string, bool) {
	value := strings.TrimSpace(input)
	if value == "" {
		return "", true
	}
	digits := true
	for _, r := range value {
		if r < '0' || r > '9' {
			digits = false
			break
		}
	}
	if digits {
		return value, true
	}
	parsed, err := url.Parse(value)
	return value, err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != ""
}

func (s *app) editTaskCreationRequirementInput() {
	form := s.taskCreation
	if form == nil || form.requirements == nil || form.requirements.closed {
		return
	}
	state := form.requirements
	value, valid := taskCreationRequirementInputLink(state.query)
	state.inputIsQuery = !valid
	if valid {
		if value != form.draft.RequirementLink {
			state.selectedTitle = ""
		}
		form.draft.RequirementLink = value
		s.syncTaskCreationDraft()
		state.inputLink = value
	}
	s.invalidateTaskCreationPreview("需求输入已变化，请重新预检。")
}
