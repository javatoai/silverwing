package desktop

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func skillSourceCompletionForm(t *testing.T, ref string) *app {
	t.Helper()
	s := newApp(nil, Options{Page: "settings", CapturePath: "skill-source-completion-test"})
	s.loading = false
	s.snapshot.Home = t.TempDir()
	s.settingsUnit.state.configJSON = `{}`
	s.settingsUnit.state.settingsPage = "skill-sources"
	s.openForm("skills.sources/save", "添加 Skill 来源", "", "", "", "",
		formField{key: "name", label: "显示名称", value: "fixture"},
		formField{key: "repositoryUrl", label: "Git 仓库地址", value: "https://example.invalid/team/skills.git"},
		formField{key: "ref", label: "分支或标签（可留空）", value: ref})
	t.Cleanup(func() {
		s.closeSourceBranchForm()
		if state := s.settingsUnit.state.settingsExtensionRequests; state != nil && state.jobs != nil {
			for request := range state.jobs.running {
				s.completeSettingsExtensionRead(request)
			}
		}
		if err := s.settingsUnit.closeSettingsExtensionReads(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s
}

func skillSourceCompletionRead(t *testing.T, s *app, at time.Time) *settingsExtensionReadJob {
	t.Helper()
	s.observeSourceBranchFormAt(at)
	action, ok := s.sourceBranchesActionAt(at.Add(skillSourceBranchDelay), false)
	if !ok {
		t.Fatal("valid quiescent URL did not produce an automatic branch request")
	}
	request, err := s.beginSettingsExtensionRead(action)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func TestSkillSourceBranchesDebounceAndManualRefresh(t *testing.T) {
	s := skillSourceCompletionForm(t, "")
	at := time.Unix(100, 0)
	s.observeSourceBranchFormAt(at)
	if _, ok := s.sourceBranchesActionAt(at.Add(499*time.Millisecond), false); ok {
		t.Fatal("branch query ran before 500ms")
	}
	*s.input("form:repositoryUrl", "") = "https://example.invalid/team/other.git"
	s.observeSourceBranchFormAt(at.Add(499 * time.Millisecond))
	if _, ok := s.sourceBranchesActionAt(at.Add(500*time.Millisecond), false); ok {
		t.Fatal("old URL deadline dispatched the replacement URL")
	}
	action, ok := s.sourceBranchesActionAt(at.Add(999*time.Millisecond), false)
	if !ok || action.Kind != "skills.sources/branches/list" || payloadString(action.Payload, "repositoryUrl") != "https://example.invalid/team/other.git" {
		t.Fatal("automatic read did not bind latest URL", action)
	}
	if _, ok := s.sourceBranchesActionAt(at.Add(time.Second), false); ok {
		t.Fatal("same URL repeatedly auto-queried")
	}
	if _, ok := s.sourceBranchesActionAt(at.Add(time.Second), true); !ok {
		t.Fatal("manual refresh was unavailable after automatic query")
	}
	*s.input("form:repositoryUrl", "") = "https://"
	s.observeSourceBranchFormAt(at.Add(2 * time.Second))
	if _, ok := s.sourceBranchesActionAt(at.Add(3*time.Second), false); ok {
		t.Fatal("invalid URL triggered automatic Git read")
	}
}

func TestSkillSourceAutomaticBranchURLValidation(t *testing.T) {
	for repository, want := range map[string]bool{
		"https://example.invalid/team/skills.git":         true,
		"ssh://git@example.invalid:2222/team/skills.git":  true,
		"git@example.invalid:team/skills.git":             true,
		"git-host-alias:team/skills.git":                  true,
		"":                                                false,
		"https://example.invalid":                         false,
		"https://example.invalid/team with spaces/skills": false,
		"C:/local/skills":                                 false,
	} {
		if got := validSourceBranchURL(repository); got != want {
			t.Errorf("automatic branch probe URL %q: got %t want %t", repository, got, want)
		}
	}
}

func TestSkillSourceBranchesCancelOnIdentityChange(t *testing.T) {
	for _, change := range []string{"url-round-trip", "close", "same-kind-reopen", "home"} {
		t.Run(change, func(t *testing.T) {
			s := skillSourceCompletionForm(t, "release/own")
			request := skillSourceCompletionRead(t, s, time.Unix(200, 0))
			switch change {
			case "url-round-trip":
				original := s.sourceBranchURL()
				*s.input("form:repositoryUrl", "") = "https://example.invalid/another/repo"
				s.observeSourceBranchFormAt(time.Unix(201, 0))
				*s.input("form:repositoryUrl", "") = original
				s.observeSourceBranchFormAt(time.Unix(202, 0))
			case "close":
				s.dialogOpen = false
				s.observeSourceBranchForm()
			case "same-kind-reopen":
				fields := append([]formField(nil), s.form.fields...)
				if !s.openForm("skills.sources/save", "添加 Skill 来源", "", "", "", "", fields...) {
					t.Fatal("could not reopen source form")
				}
			case "home":
				s.snapshot.Home = t.TempDir()
				s.observeSourceBranchForm()
			}
			if request.ctx.Err() == nil || s.settingsExtensionActionRelevant(request.action) {
				t.Fatal("old request remained current after form/URL/home change")
			}
			s.finishSettingsExtensionRead(request, application.ActionResult{Data: []string{"stale-branch"}}, nil)
			s.completeSettingsExtensionRead(request)
			if len(s.settingsUnit.state.settingsBranchChoices) != 0 || *s.input("form:ref", "release/own") != "release/own" {
				t.Fatal("late result changed another form's choices or reference")
			}
		})
	}
}

func TestSkillSourceBranchesPreserveDefaultAndExplicitReferences(t *testing.T) {
	for _, ref := range []string{"", "v1.2.3", "1234567890abcdef1234567890abcdef12345678"} {
		t.Run("ref="+ref, func(t *testing.T) {
			s := skillSourceCompletionForm(t, ref)
			request := skillSourceCompletionRead(t, s, time.Unix(300, 0))
			value := s.input("form:ref", ref)
			// An unrelated save is not part of this read-only URL's identity.
			s.settingsUnit.state.configJSON = `{"theme":"DARK"}`
			s.finishSettingsExtensionRead(request, application.ActionResult{Data: []string{"master", "main", "release/team"}}, nil)
			s.completeSettingsExtensionRead(request)
			if *value != ref || len(s.settingsUnit.state.settingsBranchChoices) != 3 {
				t.Fatal("branch result selected a default or replaced explicit ref", *value)
			}
			// A second request is explicit: the same URL must not auto-read again.
			action, ok := s.sourceBranchesActionAt(time.Unix(301, 0), true)
			if !ok {
				t.Fatal("manual branch refresh was unavailable")
			}
			request, err := s.beginSettingsExtensionRead(action)
			if err != nil {
				t.Fatal(err)
			}
			*value = "typed-during-read"
			s.finishSettingsExtensionRead(request, application.ActionResult{Data: []string{"main"}}, nil)
			s.completeSettingsExtensionRead(request)
			if *value != "typed-during-read" {
				t.Fatal("branch reply replaced the in-flight draft")
			}
		})
	}
}

func TestSkillSourceBranchesLateReplyKeepsNewChoicesAndDraft(t *testing.T) {
	s := skillSourceCompletionForm(t, "")
	old := skillSourceCompletionRead(t, s, time.Unix(400, 0))
	*s.input("form:repositoryUrl", "") = "https://example.invalid/team/new.git"
	s.observeSourceBranchFormAt(time.Unix(401, 0))
	current := skillSourceCompletionRead(t, s, time.Unix(401, 0))
	s.finishSettingsExtensionRead(current, application.ActionResult{Data: []string{"new-main"}}, nil)
	s.completeSettingsExtensionRead(current)
	*s.input("form:ref", "") = "user-selected-tag"
	s.finishSettingsExtensionRead(old, application.ActionResult{Data: []string{"old-master"}}, nil)
	s.completeSettingsExtensionRead(old)
	choices := s.settingsUnit.state.settingsBranchChoices
	if len(choices) != 1 || choices[0] != "new-main" || *s.input("form:ref", "") != "user-selected-tag" {
		t.Fatal("old repository reply replaced new choices or current selection", choices)
	}
}

func TestSkillSourceBranchComboKeepsTypedRefAndCanChooseDefault(t *testing.T) {
	s := skillSourceCompletionForm(t, "v1.2.3")
	s.settingsUnit.state.settingsBranchChoices = []string{"main", "release/team"}
	v := ui.NewTester(func(c *ui.Context) { s.renderSourceBranchRef(c, setTheme(c, false), &s.form.fields[2]) }, 650, 360)
	if err := v.Click("来源分支、标签或提交"); err != nil {
		t.Fatal(err)
	}
	v.Key(ui.Ctrl, ui.KeyA)
	v.Type("custom-ref-not-a-branch")
	v.Key(0, ui.KeyEnter)
	if got := *s.input("form:ref", ""); got != "custom-ref-not-a-branch" {
		t.Fatal("Enter replaced an explicit tag/commit with the default branch", got)
	}
	v.Key(0, ui.KeyEscape)
	if got := *s.input("form:ref", ""); got != "custom-ref-not-a-branch" {
		t.Fatal("editable combo discarded a tag/commit", got)
	}
	if err := v.Click("来源分支、标签或提交"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("默认分支（留空）"); err != nil {
		t.Fatal(err)
	}
	if got := *s.input("form:ref", ""); got != "" {
		t.Fatal("default choice did not preserve an empty ref", got)
	}
}

func TestSkillSourceRemoteActionsRequireCurrentFingerprint(t *testing.T) {
	for _, status := range []map[string]any{{}, {"sourceFingerprint": "old", "error": "refresh failed"}, {"sourceFingerprint": "old", "stale": true}, {"sourceFingerprint": "old", "needsRefresh": true}} {
		if payload, ok := skillSourceActionPayload("source", "fixture", status); ok || payload != nil || skillSourceSnapshotUnavailable(status) == "" {
			t.Fatal("unverified snapshot remained actionable", status)
		}
	}
	status := map[string]any{"sourceFingerprint": "verified-source"}
	payload, ok := skillSourceActionPayload("source", "fixture", status)
	if !ok || payloadString(payload, "sourceFingerprint") != "verified-source" {
		t.Fatal("remote action lost expected source identity", payload)
	}
	s := skillSourceCompletionForm(t, "")
	s.dialogOpen = false
	s.closeSourceBranchForm()
	s.settingsUnit.state.settingsSkillSources = []map[string]any{{"source": map[string]any{"id": "source"}, "status": status}}
	action := application.Action{Kind: "skills.preview", Payload: payload}
	s.prepareSettingsExtensionRead(&action)
	requests := s.ensureSettingsExtensionRequests()
	requests.previewLoading, requests.previewPath = false, "fixture/SKILL.md"
	copyAction, ok := s.settingsExtensionPreviewFileAction()
	if !ok || payloadString(copyAction.Payload, "sourceFingerprint") != "verified-source" {
		t.Fatal("copy lost preview's expected source identity")
	}
	status["error"] = "refresh failed"
	if _, ok := s.settingsExtensionPreviewFileAction(); ok {
		t.Fatal("old preview could copy after source became stale")
	}
}

func TestSkillInstalledOriginUsesInstallationRecordOnly(t *testing.T) {
	legacy := map[string]any{"ownership": "MANAGED", "sourceId": "old", "repositoryUrl": "https://new.invalid/repo", "ref": "new-main"}
	if text := skillInstalledOriginText(legacy); !strings.Contains(text, "来源未知") || strings.Contains(text, "new.invalid") {
		t.Fatal("legacy origin was guessed from current configuration", text)
	}
	legacy["installedOrigin"] = map[string]any{"repositoryUrl": "https://original.invalid/skills", "ref": "release/old", "commit": "fixture-commit"}
	text := skillInstalledOriginText(legacy)
	if !strings.Contains(text, "original.invalid") || !strings.Contains(text, "release/old") || strings.Contains(text, "new-main") {
		t.Fatal("installation origin was replaced by current source", text)
	}
}

func TestSkillSourceStalePreviewButtonCannotStartRead(t *testing.T) {
	s := skillSourceCompletionForm(t, "")
	s.dialogOpen = false
	s.closeSourceBranchForm()
	s.settingsUnit.state.settingsSkillSources = []map[string]any{{
		"source": map[string]any{"id": "fixture", "name": "fixture source", "repositoryUrl": "https://example.invalid/skills", "ref": "new-main"},
		"status": map[string]any{"sourceFingerprint": "old", "error": "fixture refresh failed", "skills": []map[string]any{{"name": "fixture skill", "ownership": "MANAGED"}}},
	}}
	v := ui.NewTester(func(c *ui.Context) { s.settingsSkills(c, setTheme(c, false)) }, 1000, 700)
	if !v.HasText("fixture refresh failed") || !v.HasText("来源未知") {
		t.Fatal("stale source and unknown historical origin were not explained")
	}
	if err := v.Click("预览"); err != nil {
		t.Fatal(err)
	}
	if requests := s.settingsUnit.state.settingsExtensionRequests; requests != nil && (requests.previewToken != "" || requests.previewLoading) {
		t.Fatal("disabled old-snapshot preview started a backend read")
	}
	if s.settingsUnit.state.settingsSkillPreviewOpen {
		t.Fatal("old-snapshot preview opened despite source error")
	}
}

func TestLocalSkillListShowsRecordedOriginAndUnknownHistory(t *testing.T) {
	s := skillSourceCompletionForm(t, "")
	state := &skillsBrowserState{catalogLoaded: true, skills: []map[string]any{
		{"name": "recorded", "directoryName": "recorded", "ownership": "MANAGED", "installedOrigin": map[string]any{"repositoryUrl": "https://original.invalid/skills", "ref": "release/installed"}},
		{"name": "legacy", "directoryName": "legacy", "ownership": "EXTERNAL"},
	}}
	v := ui.NewTester(func(c *ui.Context) { s.renderSkillsBrowserList(c, setTheme(c, false), state) }, 650, 400)
	if !v.HasText("https://original.invalid/skills") || !v.HasText("release/installed") || !v.HasText("来源未知") {
		t.Fatal("local skill provenance was hidden or guessed")
	}
}
