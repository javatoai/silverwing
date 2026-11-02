package desktop

import (
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

const skillSourceBranchDelay = 500 * time.Millisecond

// A separate allocation identifies this opening of a source form, even when
// another form is opened with exactly the same kind and repository URL.
type sourceBranchFormState struct {
	home, url  string
	generation uint64
	due        time.Time
	timer      *time.Timer
}

func extensionSourceForm(kind string) bool {
	return kind == "skills.sources/save" || kind == "codex.extensions/marketplaces/add-source"
}

func newSourceBranchForm(kind string) *sourceBranchFormState {
	if !extensionSourceForm(kind) {
		return nil
	}
	return &sourceBranchFormState{}
}

func stopSourceBranchTimer(form *sourceBranchFormState) {
	if form == nil {
		return
	}
	form.generation++
	form.due = time.Time{}
	if form.timer != nil {
		form.timer.Stop()
		form.timer = nil
	}
}

func (s *app) closeSourceBranchForm() {
	if s.form.sourceBranches == nil {
		return
	}
	stopSourceBranchTimer(s.form.sourceBranches)
	s.form.sourceBranches = nil
	s.invalidateSettingsExtensionRead("branches", "")
}

func validSourceBranchURL(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return false
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Hostname() == "" || strings.Trim(parsed.Path, "/") == "" {
			return false
		}
		switch strings.ToLower(parsed.Scheme) {
		case "https", "http", "ssh", "git":
			return true
		}
		return false
	}
	// Git also accepts scp-style URLs and SSH host aliases. Local paths remain
	// available through the explicit refresh action, without automatic probing.
	host, path, ok := strings.Cut(value, ":")
	return ok && len(host) > 1 && path != "" && !strings.ContainsAny(host, "/\\") && !strings.Contains(path, "\\")
}

func (s *app) sourceBranchURL() string {
	for _, field := range s.form.fields {
		if field.key == "repositoryUrl" {
			return strings.TrimSpace(*s.input("form:repositoryUrl", field.value))
		}
	}
	return strings.TrimSpace(*s.input("form:repositoryUrl", ""))
}

func (s *app) observeSourceBranchForm() { s.observeSourceBranchFormAt(time.Now()) }

func (s *app) observeSourceBranchFormAt(now time.Time) {
	if !s.dialogOpen || !extensionSourceForm(s.form.kind) {
		s.closeSourceBranchForm()
		return
	}
	if s.form.sourceBranches == nil {
		s.form.sourceBranches = newSourceBranchForm(s.form.kind)
	}
	form := s.form.sourceBranches
	repository := s.sourceBranchURL()
	if form.home == s.snapshot.Home && form.url == repository {
		return
	}
	stopSourceBranchTimer(form)
	s.invalidateSettingsExtensionRead("branches", "")
	form.home, form.url = s.snapshot.Home, repository
	if s.form.kind != "skills.sources/save" || !validSourceBranchURL(repository) {
		return
	}
	form.due = now.Add(skillSourceBranchDelay)
	if s.win == nil || s.win.IsDestroyed() {
		return
	}
	window, generation := s.win, form.generation
	jobs := ensureSettingsExtensionJobs(s.ensureSettingsExtensionRequests())
	jobs.mu.Lock()
	if jobs.closed {
		jobs.mu.Unlock()
		return
	}
	form.timer = time.AfterFunc(skillSourceBranchDelay, func() {
		if window.IsDestroyed() {
			return
		}
		window.Update(func() {
			jobs.mu.Lock()
			closed := jobs.closed
			jobs.mu.Unlock()
			if closed {
				return
			}
			if s.win != window || s.form.sourceBranches != form || form.generation != generation {
				return
			}
			s.requestSourceBranches(false)
		})
	})
	jobs.branchTimer = form.timer
	jobs.mu.Unlock()
}

func (s *app) sourceBranchesActionAt(now time.Time, force bool) (application.Action, bool) {
	s.observeSourceBranchFormAt(now)
	form := s.form.sourceBranches
	if form == nil || !s.dialogOpen || form.url == "" {
		return application.Action{}, false
	}
	if !force && (form.due.IsZero() || now.Before(form.due)) {
		return application.Action{}, false
	}
	stopSourceBranchTimer(form)
	kind := "skills.sources/branches/list"
	if s.form.kind == "codex.extensions/marketplaces/add-source" {
		kind = "codex.extensions/branches/list"
	}
	return application.Action{Kind: kind, Payload: map[string]any{"repositoryUrl": form.url}}, true
}

func (s *app) requestSourceBranches(force bool) {
	if action, ok := s.sourceBranchesActionAt(time.Now(), force); ok {
		s.act(action.Kind, "", "", "", "", "", action.Payload)
	}
}

func (s *app) applySourceBranches(action application.Action, result application.ActionResult) {
	if !s.settingsExtensionActionRelevant(action) {
		return
	}
	s.settingsUnit.state.settingsBranchChoices = mapsString(result.Data)
	state := s.ensureSettingsExtensionRequests()
	state.branchLoading, state.branchError = false, ""
	// Empty means the repository default. The result must never choose a branch
	// or replace a manually entered tag/commit, including edits made in flight.
}

func (s *app) renderSourceBranchRef(c *ui.Context, p palette, field *formField) {
	value := s.input("form:"+field.key, field.value)
	combo := ui.ComboboxBase(c.Key("action-form-"+field.key), value)
	combo.Input.FillWidth().Height(40).Padding(0, 12).Background(p.surface).Border(1, p.line).Radius(8).Label("来源分支、标签或提交").Placeholder("默认分支；也可输入分支、标签或提交")
	combo.Popup(func(panel ui.Element) {
		panel.Padding(6).Gap(3).MaxHeight(240).Background(p.surface).Border(1, p.line).Radius(8)
		ui.Scroll(c).FillWidth().MaxHeight(220).Gap(2).Children(func() {
			item := func(ref, text string) {
				row := combo.Item(ref).FillWidth().Padding(7, 10).Radius(5).Label(text)
				if row.Highlighted() {
					row.Background(p.selected)
				}
				row.Children(func() { label(c, text, 12, p.text).Wrap() })
			}
			query := ""
			if combo.Filtering() {
				query = strings.ToLower(strings.TrimSpace(*value))
			}
			if query == "" || strings.Contains("默认分支（留空）", query) {
				item("", "默认分支（留空）")
			}
			seen := map[string]bool{"": true}
			for _, branch := range s.settingsUnit.state.settingsBranchChoices {
				if !seen[branch] && strings.Contains(strings.ToLower(branch), query) {
					item(branch, branch)
					seen[branch] = true
				}
			}
		})
	})
	if ref, chosen := combo.Chosen(); chosen {
		*value = ref
	}
}
