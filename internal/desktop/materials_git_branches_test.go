package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
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
	if s.materialsGitBranchReady("master") || s.materialsGitBranches.err == "" {
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
	_ = json.Unmarshal([]byte(s.configJSON), &cfg)
	cfg.MaterialsGitSync.RepositoryURL = "https://github.com/other/docs.git"
	cfg.MaterialsGitSync.Branch = "docs/current"
	data, _ := json.Marshal(cfg)
	s.configJSON = string(data)
	s.loadMaterialsGitBranches(false)
	current := nextMaterialsBranchCall(t, b)
	current.reply <- materialsBranchReply{branches: []string{"docs/current"}}
	materialsGitNextUpdate(t, updates)
	old.reply <- materialsBranchReply{branches: []string{"master"}}
	materialsGitNextUpdate(t, updates)
	if !slices.Equal(s.materialsGitBranches.branches, []string{"docs/current"}) || !s.materialsGitBranchReady("docs/current") {
		t.Fatal("late branches overwrote new selection")
	}
	var saved domain.AppConfig
	_ = json.Unmarshal([]byte(s.configJSON), &saved)
	if saved.MaterialsGitSettings().Branch != "docs/current" || b.materialsGitFakeBackend.count.Load() != 0 {
		t.Fatal("query changed saved branch or launched sync")
	}
}

func TestMaterialsGitEmptyAndMissingBranchUI(t *testing.T) {
	for _, branches := range [][]string{{}, {"docs/another"}} {
		s, _, _ := materialsBranchesFixture(t)
		s.materialsGitCapture = true
		cfg, _ := s.materialsGitConfig()
		s.materialsGitBranches = &materialsGitBranchesState{scope: application.MaterialsGitScope(s.snapshot.Home, cfg), loaded: true, branches: branches}
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
	s.materialsGitCapture = true
	s.backend = nil // The existing save action reports an unavailable backend.
	cfg, _ := s.materialsGitConfig()
	s.materialsGitBranches = &materialsGitBranchesState{scope: application.MaterialsGitScope(s.snapshot.Home, cfg), loaded: true, branches: []string{"docs/team", "master"}}
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
				s.configJSON = string(data)
			}
			s.loadMaterialsGitBranches(false)
			current := nextMaterialsBranchCall(t, b)
			current.reply <- materialsBranchReply{branches: []string{"master"}}
			materialsGitNextUpdate(t, updates)
			old.reply <- materialsBranchReply{branches: []string{"old/branch"}}
			materialsGitNextUpdate(t, updates)
			if !s.materialsGitBranchReady("master") || slices.Contains(s.materialsGitBranches.branches, "old/branch") {
				t.Fatal("stale scope result applied")
			}
		})
	}
}
func ptrForMaterialsBranchTest(value string) *string { return &value }
