package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

type materialsBranchReply struct {
	branches []string
	err      error
}
type materialsBranchCall struct {
	scope workspace.MaterialsGitScope
	reply chan materialsBranchReply
}
type materialsBranchesFake struct {
	*materialsGitFakeBackend
	count atomic.Int32
	calls chan materialsBranchCall
}

type materialsGitLoginCall struct {
	ctx   context.Context
	scope workspace.MaterialsGitScope
	reply chan error
}

type materialsLoginFake struct {
	*materialsBranchesFake
	logins chan materialsGitLoginCall
}

func (b *materialsLoginFake) VerifyMaterialsGitLogin(ctx context.Context, scope workspace.MaterialsGitScope) error {
	call := materialsGitLoginCall{ctx: ctx, scope: scope, reply: make(chan error, 1)}
	b.logins <- call
	return <-call.reply
}

func (*materialsLoginFake) MaterialsGitTerminalLoginCommand(context.Context, workspace.MaterialsGitScope) (string, error) {
	return "git ls-remote --heads -- 'https://github.com/example/docs.git'", nil
}

func nextMaterialsGitLoginCall(t *testing.T, b *materialsLoginFake) materialsGitLoginCall {
	t.Helper()
	select {
	case call := <-b.logins:
		return call
	case <-time.After(3 * time.Second):
		t.Fatal("missing Git login verification")
		return materialsGitLoginCall{}
	}
}

func (b *materialsBranchesFake) MaterialsGitBranches(ctx context.Context, scope workspace.MaterialsGitScope) ([]string, error) {
	b.count.Add(1)
	call := materialsBranchCall{scope: scope, reply: make(chan materialsBranchReply, 1)}
	b.calls <- call
	// Deliberately allow a late result after cancellation to test the UI guard.
	reply := <-call.reply
	return reply.branches, reply.err
}
func materialsBranchesFixture(t *testing.T) (*app, *materialsBranchesFake, chan func()) {
	s, base, updates := materialsGitDesktopFixture(t)
	b := &materialsBranchesFake{materialsGitFakeBackend: base, calls: make(chan materialsBranchCall, 8)}
	s.backend = b
	return s, b, updates
}
func nextMaterialsBranchCall(t *testing.T, b *materialsBranchesFake) materialsBranchCall {
	t.Helper()
	select {
	case call := <-b.calls:
		return call
	case <-time.After(3 * time.Second):
		t.Fatal("missing branch query")
		return materialsBranchCall{}
	}
}

func TestMaterialsGitBranchReadsCoalesceAndDoNotSync(t *testing.T) {
	s, b, updates := materialsBranchesFixture(t)
	s.loadMaterialsGitBranches(false)
	call := nextMaterialsBranchCall(t, b)
	s.loadMaterialsGitBranches(false)
	s.loadMaterialsGitBranches(true)
	if b.count.Load() != 1 || b.materialsGitFakeBackend.count.Load() != 0 {
		t.Fatal("duplicate query or sync")
	}
	call.reply <- materialsBranchReply{branches: []string{"docs/team", "master"}}
	materialsGitNextUpdate(t, updates)
	if !s.materialsGitBranchReady("master") {
		t.Fatal("saved branch was not retained")
	}
	s.loadMaterialsGitBranches(false)
	if b.count.Load() != 1 {
		t.Fatal("render repeated branch read")
	}
	s.loadMaterialsGitBranches(true)
	call = nextMaterialsBranchCall(t, b)
	call.reply <- materialsBranchReply{err: errors.New("authentication failed")}
	materialsGitNextUpdate(t, updates)
	if s.materialsGitBranchReady("master") || s.materialsUnit.state.materialsGitBranches.err == "" {
		t.Fatal("failed read used old branches")
	}
	if b.materialsGitFakeBackend.count.Load() != 0 {
		t.Fatal("read triggered build/sync")
	}
}

func TestMaterialsGitLateBranchResultCannotReplaceNewScope(t *testing.T) {
	s, b, updates := materialsBranchesFixture(t)
	s.loadMaterialsGitBranches(false)
	old := nextMaterialsBranchCall(t, b)
	var cfg domain.AppConfig
	_ = json.Unmarshal([]byte(s.settingsUnit.state.configJSON), &cfg)
	cfg.MaterialsGitSync.RepositoryURL = "https://github.com/other/docs.git"
	cfg.MaterialsGitSync.Branch = "docs/current"
	data, _ := json.Marshal(cfg)
	s.settingsUnit.state.configJSON = string(data)
	s.loadMaterialsGitBranches(false)
	current := nextMaterialsBranchCall(t, b)
	current.reply <- materialsBranchReply{branches: []string{"docs/current"}}
	materialsGitNextUpdate(t, updates)
	old.reply <- materialsBranchReply{branches: []string{"master"}}
	materialsGitNextUpdate(t, updates)
	if !slices.Equal(s.materialsUnit.state.materialsGitBranches.branches, []string{"docs/current"}) || !s.materialsGitBranchReady("docs/current") {
		t.Fatal("late branches overwrote new selection")
	}
	var saved domain.AppConfig
	_ = json.Unmarshal([]byte(s.settingsUnit.state.configJSON), &saved)
	if saved.MaterialsGitSettings().Branch != "docs/current" || b.materialsGitFakeBackend.count.Load() != 0 {
		t.Fatal("query changed saved branch or launched sync")
	}
}

func TestMaterialsGitEmptyAndMissingBranchUI(t *testing.T) {
	for _, branches := range [][]string{{}, {"docs/another"}} {
		s, _, _ := materialsBranchesFixture(t)
		s.materialsUnit.state.materialsGitCapture = true
		cfg, _ := s.materialsGitConfig()
		s.materialsUnit.state.materialsGitBranches = &materialsGitBranchesState{scope: application.MaterialsGitScope(s.snapshot.Home, cfg), loaded: true, branches: branches}
		tt := ui.NewTester(func(c *ui.Context) { s.settingsMaterialsGitSync(c, setTheme(c, false)) }, 960, 800)
		want := "请先在远程仓库创建分支"
		if len(branches) > 0 {
			want = "已保存的分支 master 不在远程，请重新选择。"
		}
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
		if s.materialsGitBranchReady("master") {
			t.Fatal("invalid branch enabled sync")
		}
	}
}

func TestMaterialsGitDropdownSaveFailureRetainsSavedBranch(t *testing.T) {
	s, _, _ := materialsBranchesFixture(t)
	s.materialsUnit.state.materialsGitCapture = true
	s.backend = nil // The existing save action reports an unavailable backend.
	cfg, _ := s.materialsGitConfig()
	s.materialsUnit.state.materialsGitBranches = &materialsGitBranchesState{scope: application.MaterialsGitScope(s.snapshot.Home, cfg), loaded: true, branches: []string{"docs/team", "master"}}
	tt := ui.NewTester(func(c *ui.Context) { s.settingsMaterialsGitSync(c, setTheme(c, false)) }, 960, 800)
	if err := tt.Click("选择同步分支"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("docs/team"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	current, _ := s.materialsGitConfig()
	if current.MaterialsGitSettings().Branch != "master" || s.lastError == "" {
		t.Fatal("failed save changed branch or hid failure")
	}
}

func TestMaterialsGitBranchReadHomeAndEnvironmentInvalidation(t *testing.T) {
	for _, kind := range []string{"home", "environment"} {
		t.Run(kind, func(t *testing.T) {
			s, b, updates := materialsBranchesFixture(t)
			s.loadMaterialsGitBranches(false)
			old := nextMaterialsBranchCall(t, b)
			if kind == "home" {
				s.snapshot.Home = t.TempDir()
			} else {
				cfg, _ := s.materialsGitConfig()
				cfg.GitExecutablePath = ptrForMaterialsBranchTest("other-git.exe")
				data, _ := json.Marshal(cfg)
				s.settingsUnit.state.configJSON = string(data)
			}
			s.loadMaterialsGitBranches(false)
			current := nextMaterialsBranchCall(t, b)
			current.reply <- materialsBranchReply{branches: []string{"master"}}
			materialsGitNextUpdate(t, updates)
			old.reply <- materialsBranchReply{branches: []string{"old/branch"}}
			materialsGitNextUpdate(t, updates)
			if !s.materialsGitBranchReady("master") || slices.Contains(s.materialsUnit.state.materialsGitBranches.branches, "old/branch") {
				t.Fatal("stale scope result applied")
			}
		})
	}
}

func TestMaterialsGitLoginRefreshesBranchesAfterExplicitVerification(t *testing.T) {
	s, branches, updates := materialsBranchesFixture(t)
	b := &materialsLoginFake{materialsBranchesFake: branches, logins: make(chan materialsGitLoginCall, 2)}
	s.backend = b
	tt := ui.NewTester(func(c *ui.Context) { s.settingsMaterialsGitSync(c, setTheme(c, false)) }, 960, 800)
	call := nextMaterialsBranchCall(t, branches)
	call.reply <- materialsBranchReply{err: &workspace.GitRemoteFailure{Kind: workspace.GitRemoteAuthentication}}
	materialsGitNextUpdate(t, updates)
	tt.Frame()
	if !tt.HasText("打开终端") || !tt.HasText("复制验证命令") || len(b.logins) != 0 {
		t.Fatalf("missing recovery actions or automatic login: %v", tt.Texts())
	}
	if err := tt.Click("复制验证命令"); err != nil {
		t.Fatal(err)
	}
	materialsGitNextUpdate(t, updates)
	if tt.Clipboard() != "git ls-remote --heads -- 'https://github.com/example/docs.git'" || len(b.logins) != 0 {
		t.Fatal("fallback did not copy the read-only command")
	}
	if err := tt.Click("登录并验证"); err != nil {
		t.Fatal(err)
	}
	login := nextMaterialsGitLoginCall(t, b)
	if login.scope != call.scope || len(branches.calls) != 0 {
		t.Fatal("verification used the wrong scope or tried to refresh before login")
	}
	login.reply <- nil
	refresh := nextMaterialsBranchCall(t, branches)
	if refresh.scope != login.scope {
		t.Fatal("branch refresh used a different scope")
	}
	refresh.reply <- materialsBranchReply{branches: []string{"master", "docs/team"}}
	materialsGitNextUpdate(t, updates)
	if !s.materialsGitBranchReady("master") || strings.Contains(s.lastMessage, "失败") || b.materialsGitFakeBackend.count.Load() != 0 {
		t.Fatal("successful verification did not refresh readiness or unexpectedly synced")
	}
}

func TestMaterialsGitLoginRequiresHTTPSAndDiscardsOldScope(t *testing.T) {
	s, branches, updates := materialsBranchesFixture(t)
	b := &materialsLoginFake{materialsBranchesFake: branches, logins: make(chan materialsGitLoginCall, 2)}
	s.backend = b
	config, _ := s.materialsGitConfig()
	setURL := func(url string) {
		settings := config.MaterialsGitSettings()
		settings.RepositoryURL = url
		config.MaterialsGitSync = &settings
		raw, _ := json.Marshal(config)
		s.settingsUnit.state.configJSON = string(raw)
	}
	setURL("http://git.example.invalid/team/docs.git")
	tt := ui.NewTester(func(c *ui.Context) { s.settingsMaterialsGitSync(c, setTheme(c, false)) }, 960, 800)
	call := nextMaterialsBranchCall(t, branches)
	call.reply <- materialsBranchReply{err: &workspace.GitRemoteFailure{Kind: workspace.GitRemoteAuthentication}}
	materialsGitNextUpdate(t, updates)
	tt.Frame()
	if !tt.HasText("确认改用 HTTPS") || !tt.HasText("https://git.example.invalid/team/docs.git") || tt.HasText("登录并验证") || tt.HasText("打开终端") {
		t.Fatalf("HTTP recovery skipped confirmation: %v", tt.Texts())
	}
	s.beginMaterialsGitLogin()
	if len(b.logins) != 0 {
		t.Fatal("HTTP started explicit login")
	}
	setURL("https://git.example.invalid/team/docs.git")
	syncMaterialsGitTestSettingsInputs(s, config)
	s.loadMaterialsGitBranches(false)
	call = nextMaterialsBranchCall(t, branches)
	call.reply <- materialsBranchReply{err: &workspace.GitRemoteFailure{Kind: workspace.GitRemoteAuthentication}}
	materialsGitNextUpdate(t, updates)
	s.beginMaterialsGitLogin()
	old := nextMaterialsGitLoginCall(t, b)
	setURL("https://git.example.invalid/team/replacement.git")
	syncMaterialsGitTestSettingsInputs(s, config)
	s.observeMaterialsGitBranches()
	s.loadMaterialsGitBranches(false)
	select {
	case <-old.ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("old login was not canceled")
	}
	current := nextMaterialsBranchCall(t, branches)
	current.reply <- materialsBranchReply{branches: []string{"master"}}
	materialsGitNextUpdate(t, updates)
	old.reply <- nil
	materialsGitNextUpdate(t, updates)
	if !s.materialsGitBranchReady("master") || len(branches.calls) != 0 || s.lastMessage != "" {
		t.Fatal("old verification changed the current branch state")
	}
}

func syncMaterialsGitTestSettingsInputs(s *app, config domain.AppConfig) {
	input := s.inputs["setting:materialsGitSync:repositoryUrl"]
	*input = config.MaterialsGitSettings().RepositoryURL
}

func ptrForMaterialsBranchTest(value string) *string { return &value }
