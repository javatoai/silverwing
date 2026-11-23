package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

func TestMaterialsGitScopeAndDocumentMutationGuards(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config, err := a.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "requirements")
	sub := "研发"
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	config.RequirementMaterialsRoot = &root
	config.RequirementMaterialsSubdirectory = &sub
	settings := domain.DefaultMaterialsGitSyncSettings()
	settings.RepositoryURL = "https://example.invalid/docs.git"
	config.MaterialsGitSync = &settings
	if err := a.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	scope := MaterialsGitScope(a.Paths.Home, config)
	scope.Branch = "wrong"
	if _, err := a.SyncMaterialsGit(context.Background(), scope, true, nil); !errors.Is(err, workspace.ErrMaterialsGitScopeChanged) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); !os.IsNotExist(err) {
		t.Fatal("stale scope initialized repository")
	}
	root = MaterialsGitScope(a.Paths.Home, config).Root
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	parentRelease, err := storage.AcquireMaterialsRepositoryLock(filepath.Dir(root))
	if err != nil {
		t.Fatal(err)
	}
	defer parentRelease()
	writeRoot := filepath.Join(root, domain.RequirementsDirectory, "2026", "123-需求", sub)
	release, err := a.lockMaterialsGitDocumentMutation("document.save", writeRoot)
	if err != nil {
		t.Fatalf("document mutation used the parent directory lock: %v", err)
	}
	release()
	unlock, err := storage.AcquireMaterialsRepositoryLock(root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := a.lockMaterialsGitDocumentMutation("document.save", writeRoot); !errors.Is(err, storage.ErrLockBusy) {
		t.Fatalf("document mutation ignored shared root lock: %v", err)
	}
	release, err = a.lockMaterialsGitDocumentMutation("document.read", root)
	if err != nil {
		t.Fatal(err)
	}
	release()
	a.materialsGitGate.Lock()
	if _, err := a.documentAction(context.Background(), Action{Kind: "document.save"}); err == nil {
		t.Fatal("document write ignored Git gate")
	}
	a.materialsGitGate.Unlock()
}

func TestMaterialsGitScopeUsesManagedRootRegardlessOfExternalGitMetadata(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "snb-work-data")
	requirements := filepath.Join(parent, domain.RequirementsDirectory)

	config := domain.DefaultAppConfig()
	config.RequirementMaterialsRoot = &requirements
	settings := domain.DefaultMaterialsGitSyncSettings()
	settings.RepositoryURL = "https://example.invalid/docs"
	config.MaterialsGitSync = &settings

	home := t.TempDir()
	scope := MaterialsGitScope(home, config)
	want := domain.ManagedTaskMaterialsRoot(home, settings)
	if scope.Root != want || scope.RepositoryRole != domain.TaskMaterialsRepositoryRole {
		t.Fatalf("explicit external path affected managed scope: %+v", scope)
	}
	for _, marker := range []string{filepath.Join(parent, ".git"), filepath.Join(requirements, ".git")} {
		if err := os.MkdirAll(marker, 0o755); err != nil {
			t.Fatal(err)
		}
		if current := MaterialsGitScope(home, config); current != scope {
			t.Fatalf("Git marker %s changed scope: %+v", marker, current)
		}
	}
	config.RequirementMaterialsRoot = &parent
	if current := MaterialsGitScope(home, config); current != scope {
		t.Fatalf("external selection changed managed checkout: %+v", current)
	}
}

func TestProjectInstructionsGitScopeUsesOnlySavedRepository(t *testing.T) {
	paths := storage.NewPaths(t.TempDir())
	a := &App{Paths: paths, Config: storage.NewConfigStore(paths)}
	config := domain.DefaultAppConfig()
	config.ProjectInstructionsGitURL = "https://example.invalid/team/instructions.git"
	if err := a.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	old := ProjectInstructionsGitScope(paths.Home, config)
	if status, err := a.ProjectInstructionsGitStatus(context.Background(), old); err != nil || status.LastSuccess != "" {
		t.Fatalf("status read started a sync: %+v %v", status, err)
	}
	if _, err := os.Stat(old.Root); !os.IsNotExist(err) {
		t.Fatal("status created the checkout")
	}
	config.ProjectInstructionsGitURL = "https://example.invalid/team/another.git"
	if err := a.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SyncProjectInstructionsGit(context.Background(), old, true, nil); !errors.Is(err, workspace.ErrMaterialsGitScopeChanged) {
		t.Fatalf("stale scope can sync: %v", err)
	}
	if current := ProjectInstructionsGitScope(paths.Home, config); old.Root == current.Root || current.Branch != "" {
		t.Fatalf("repository scope did not follow saved target: %+v", current)
	}
}

func TestProjectInstructionsGitSettingsSurviveIndependentUpdates(t *testing.T) {
	ctx := context.Background()
	paths := storage.NewPaths(t.TempDir())
	a := &App{Paths: paths, Config: storage.NewConfigStore(paths)}
	original := domain.DefaultAppConfig()
	original.ProjectInstructionsGitURL = "https://example.invalid/team/instructions.git"
	if err := a.Config.Save(original); err != nil {
		t.Fatal(err)
	}
	save := func(fields map[string]any) error {
		_, err := a.Dispatch(ctx, Action{Kind: "config.fields.save", Payload: map[string]any{"update": map[string]any{"fields": fields}}})
		return err
	}
	read := func() domain.AppConfig {
		t.Helper()
		config, err := storage.NewConfigStore(paths).Load()
		if err != nil {
			t.Fatal(err)
		}
		return config
	}
	if settings := read().ProjectInstructionsGitSettings(); !settings.Enabled || settings.Branch != "" || settings.IntervalMinutes != 5 {
		t.Fatalf("URL-only settings = %+v", settings)
	}
	if err := save(map[string]any{"projectInstructionsGitEnabled": false, "projectInstructionsGitBranch": " docs/guides ", "projectInstructionsGitIntervalMinutes": 12}); err != nil {
		t.Fatal(err)
	}
	changed := read()
	oldScope := ProjectInstructionsGitScope(paths.Home, original)
	newScope := ProjectInstructionsGitScope(paths.Home, changed)
	if newScope.Root != oldScope.Root || newScope.Branch != "docs/guides" || newScope.EnvironmentID == oldScope.EnvironmentID {
		t.Fatalf("unexpected project scope after local settings update: %+v", newScope)
	}
	if _, err := a.MaterialsGitBranches(ctx, oldScope); !errors.Is(err, workspace.ErrMaterialsGitScopeChanged) {
		t.Fatalf("branch lookup accepted a stale project scope: %v", err)
	}
	const newURL = "https://example.invalid/team/other.git"
	if err := save(map[string]any{"projectInstructionsGitUrl": " " + newURL + " "}); err != nil {
		t.Fatal(err)
	}
	want := domain.MaterialsGitSyncSettings{RepositoryURL: newURL, Branch: "docs/guides", IntervalMinutes: 12}
	if got := read().ProjectInstructionsGitSettings(); got != want {
		t.Fatalf("saved project settings = %+v, want %+v", got, want)
	}
	for key, invalid := range map[string]any{"projectInstructionsGitBranch": "refs/heads/main", "projectInstructionsGitIntervalMinutes": 0} {
		if err := save(map[string]any{key: invalid}); err == nil {
			t.Errorf("accepted invalid %s = %v", key, invalid)
		}
	}
	if got := read().ProjectInstructionsGitSettings(); got != want {
		t.Fatalf("an invalid update changed saved settings: %+v", got)
	}
	if err := save(map[string]any{"projectInstructionsGitEnabled": nil, "projectInstructionsGitBranch": "", "projectInstructionsGitIntervalMinutes": nil}); err != nil {
		t.Fatal(err)
	}
	if got := read().ProjectInstructionsGitSettings(); got != (domain.MaterialsGitSyncSettings{Enabled: true, RepositoryURL: newURL, IntervalMinutes: 5}) {
		t.Fatalf("reset did not restore project defaults: %+v", got)
	}
}

func TestMaterialsGitTerminalLoginCommandQuotesArguments(t *testing.T) {
	args := []string{"-c", "credential.helper=osxkeychain", "ls-remote", "--heads", "--", "https://git.example.invalid/team/o'neil;docs.git"}
	mac := materialsGitTerminalLoginCommand("darwin", "/opt/Tools O'Neil/git", args)
	if !strings.Contains(mac, "GIT_ALLOW_PROTOCOL=https") || !strings.Contains(mac, `'/opt/Tools O'\''Neil/git'`) ||
		!strings.Contains(mac, `'credential.helper=osxkeychain' 'ls-remote' '--heads' '--' 'https://git.example.invalid/team/o'\''neil;docs.git'`) {
		t.Fatalf("command lost secure protocol, helper or shell argument quoting: %s", mac)
	}
	windows := materialsGitTerminalLoginCommand("windows", `C:\Tools O'Neil\git.exe`, args[2:])
	if !strings.Contains(windows, "GIT_ALLOW_PROTOCOL='https'") || !strings.Contains(windows, `'C:\Tools O''Neil\git.exe'`) ||
		!strings.Contains(windows, `'ls-remote' '--heads' '--' 'https://git.example.invalid/team/o''neil;docs.git'`) {
		t.Fatalf("command lost secure protocol or PowerShell argument quoting: %s", windows)
	}
}

func TestMaterialsGitSettingsSaveUpdatesAutoSyncBasedOnTarget(t *testing.T) {
	const repository = "https://example.invalid/team/docs"
	const otherRepository = "https://example.invalid/team/other"
	for _, tc := range []struct {
		name        string
		oldURL      string
		newURL      string
		branch      string
		changeRoot  bool
		subdir      string
		disable     bool
		wantEnabled bool
	}{
		{name: "add suffix", oldURL: repository, newURL: repository + ".git", wantEnabled: true},
		{name: "remove suffix", oldURL: repository + ".git", newURL: repository, wantEnabled: true},
		{name: "different repository", oldURL: repository, newURL: otherRepository + ".git"},
		{name: "different branch", oldURL: repository, newURL: repository + ".git", branch: "docs/new"},
		{name: "different root", oldURL: repository, newURL: repository + ".git", changeRoot: true},
		{name: "different subdirectory", oldURL: repository, newURL: repository + ".git", subdir: "other-documents"},
		{name: "explicit disable", oldURL: repository, newURL: repository + ".git", disable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := storage.NewPaths(t.TempDir())
			a := &App{Paths: paths, Config: storage.NewConfigStore(paths)}
			root := filepath.Join(paths.Home, "materials")
			subdir := "documents"
			config := domain.DefaultAppConfig()
			config.RequirementMaterialsRoot = &root
			config.RequirementMaterialsSubdirectory = &subdir
			settings := domain.DefaultMaterialsGitSyncSettings()
			settings.RepositoryURL = tc.oldURL
			settings.Enabled = true
			config.MaterialsGitSync = &settings
			if err := a.Config.Save(config); err != nil {
				t.Fatal(err)
			}

			next := settings
			next.RepositoryURL = tc.newURL
			next.Enabled = !tc.disable
			if tc.branch != "" {
				next.Branch = tc.branch
			}
			fields := map[string]any{"materialsGitSync": next}
			wantRoot, wantSubdir := root, subdir
			if tc.changeRoot {
				wantRoot = filepath.Join(paths.Home, "other-materials")
				fields["requirementMaterialsRoot"] = wantRoot
			}
			if tc.subdir != "" {
				wantSubdir = tc.subdir
				fields["requirementMaterialsSubdirectory"] = wantSubdir
			}
			action := Action{
				Kind:    "config.fields.save",
				Payload: map[string]any{"update": map[string]any{"fields": fields}},
			}
			if _, err := a.Dispatch(context.Background(), action); err != nil {
				t.Fatal(err)
			}
			updated, err := a.Config.Load()
			if err != nil {
				t.Fatal(err)
			}
			next.Enabled = tc.wantEnabled
			if got := updated.MaterialsGitSettings(); got != next {
				t.Fatalf("saved Git settings = %+v, want %+v", got, next)
			}
			if gotRoot, gotSubdir := value(updated.RequirementMaterialsRoot), value(updated.RequirementMaterialsSubdirectory); gotRoot != wantRoot || gotSubdir != wantSubdir {
				t.Fatalf("saved root and subdirectory = %q, %q, want %q, %q", gotRoot, gotSubdir, wantRoot, wantSubdir)
			}
		})
	}
}
