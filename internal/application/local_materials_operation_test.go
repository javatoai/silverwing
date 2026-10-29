package application_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

type localMaterialsOperationFixture struct {
	app                 *application.App
	base, actual, alias string
}

func localMaterialsOperationDirectoryLink(t *testing.T, alias, target string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		command, err := exec.LookPath("cmd.exe")
		if err != nil {
			t.Skipf("Windows junction helper unavailable: %v", err)
		}
		output, err := exec.Command(command, "/d", "/c", "mklink", "/J", alias, target).CombinedOutput()
		if err != nil {
			message := strings.ToLower(string(output))
			if strings.Contains(message, "privilege") || strings.Contains(message, "access is denied") || strings.Contains(message, "权限") || strings.Contains(message, "拒绝访问") {
				t.Skipf("Windows junction permission unavailable: %v: %s", err, output)
			}
			t.Fatalf("create isolated directory junction: %v: %s", err, output)
		}
		return
	}
	if err := os.Symlink(target, alias); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			t.Skipf("directory symlink permission unavailable: %v", err)
		}
		t.Fatal(err)
	}
}

func newLocalMaterialsOperationFixture(t *testing.T) *localMaterialsOperationFixture {
	t.Helper()
	base := t.TempDir()
	actual, alias := filepath.Join(base, "physical-tasks"), filepath.Join(base, "configured-tasks")
	if err := os.MkdirAll(actual, 0700); err != nil {
		t.Fatal(err)
	}
	localMaterialsOperationDirectoryLink(t, alias, actual)
	// Snapshot reads the OS user's local skill catalog. Keep that read inside
	// this fixture as well as the application home and configured task root.
	isolatedUser := filepath.Join(base, "isolated-user")
	if err := os.MkdirAll(isolatedUser, 0700); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", isolatedUser)
	} else {
		t.Setenv("HOME", isolatedUser)
	}
	paths := storage.NewPaths(filepath.Join(base, "application-home"))
	config := domain.DefaultAppConfig()
	config.TaskRoot = &alias
	config.DefaultWorkspaceToolIDs = []string{}
	config.RequirementMaterialsRoot, config.RequirementMaterialsSubdirectory = nil, nil
	missingGit := filepath.Join(base, "git-must-not-run")
	config.GitExecutablePath = &missingGit
	if err := storage.NewConfigStore(paths).Save(config); err != nil {
		t.Fatal(err)
	}
	store := storage.NewManifestStore()
	for _, id := range []string{"task-a", "task-b"} {
		manifest := domain.DefaultTaskManifest()
		manifest.TaskDirectoryName, manifest.FolderName = id, id
		manifest.FeatureBranch = "feature/local-operation-regression"
		manifest.CreatedAt, manifest.UpdatedAt = "2026-10-28T10:00:00Z", "2026-10-28T10:00:00Z"
		if err := store.Save(filepath.Join(alias, id), manifest); err != nil {
			t.Fatal(err)
		}
	}
	app, err := application.New(paths.Home)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &localMaterialsOperationFixture{app: app, base: base, actual: actual, alias: alias}
	for _, id := range []string{"task-a", "task-b"} {
		result, err := app.Dispatch(context.Background(), application.Action{Kind: "task.materials-retry", TaskID: id, Path: filepath.Join(alias, id)})
		if err != nil {
			t.Fatalf("prepare local materials under the configured TaskRoot alias: %v", err)
		}
		manifest, err := application.ResultData[domain.TaskManifest](result)
		if err != nil {
			t.Fatal(err)
		}
		want := documents.LocalTaskMaterials(filepath.Join(alias, id)).Owned
		if manifest.RequirementMaterials.Status != domain.MaterialsReady || manifest.RequirementMaterials.WriteRoot == nil || *manifest.RequirementMaterials.WriteRoot != want {
			t.Fatalf("preparation exposed the physical namespace: %+v want=%s", manifest.RequirementMaterials, want)
		}
	}
	return fixture
}

func (f *localMaterialsOperationFixture) physical(t *testing.T, public string) string {
	t.Helper()
	relative, err := filepath.Rel(f.alias, public)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatalf("fixture setup escaped its isolated configured TaskRoot: %s %v", public, err)
	}
	return filepath.Join(f.actual, relative)
}

func localMaterialsOperationRequireBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("file changed at %s: got=%q want=%q error=%v", path, got, want, err)
	}
}

func TestLocalMaterialsApplicationOperationsPreserveTaskRootAlias(t *testing.T) {
	fixture := newLocalMaterialsOperationFixture(t)
	ctx := context.Background()
	taskPath := filepath.Join(fixture.alias, "task-a")
	scope := documents.LocalTaskMaterials(taskPath)
	config, err := fixture.app.Config.Load()
	if err != nil || config.TaskRoot == nil || *config.TaskRoot != fixture.alias {
		t.Fatalf("preparation rewrote the configured TaskRoot: %+v %v", config.TaskRoot, err)
	}
	manifest, err := fixture.app.Manifests.Load(taskPath)
	if err != nil || manifest.RequirementMaterials.WriteRoot == nil || *manifest.RequirementMaterials.WriteRoot != scope.Owned {
		t.Fatalf("persisted manifest lost the public alias: %+v %v", manifest.RequirementMaterials, err)
	}
	snapshot, err := fixture.app.Snapshot(ctx)
	if err != nil || snapshot.Error != "" {
		t.Fatalf("snapshot failed after real local preparation: %+v %v", snapshot, err)
	}
	found := false
	for _, task := range snapshot.Tasks {
		if task.ID == "task-a" {
			found = true
			if task.Path != taskPath || task.MaterialsPath != scope.Root || task.Materials.Kind != documents.MaterialsLocal || task.Materials.ReadRoot != scope.Root || task.Materials.OwnedRoot != scope.Owned || task.Materials.TemporaryRoot != scope.Temporary || !task.Materials.CanWrite || task.Materials.CanSync {
				t.Fatalf("snapshot advertised the wrong public local capability: %+v", task)
			}
		}
	}
	if !found {
		t.Fatal("prepared local task was absent from the real snapshot")
	}
	knownPath := filepath.Join(scope.Owned, "known.txt")
	knownBytes := []byte("known source\r\n中文\n")
	if err := os.WriteFile(fixture.physical(t, knownPath), knownBytes, 0600); err != nil {
		t.Fatal(err)
	}
	listed, err := fixture.app.Dispatch(ctx, application.Action{Kind: "document.list", TaskID: "task-a"})
	if err != nil || listed.Path != scope.Root {
		t.Fatalf("list did not preserve the alias root: %+v %v", listed, err)
	}
	entries, err := application.ResultData[[]documents.Entry](listed)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	var inspect func([]documents.Entry)
	inspect = func(entries []documents.Entry) {
		for _, entry := range entries {
			relative, err := filepath.Rel(scope.Root, entry.Path)
			if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				t.Fatalf("list exposed a path outside the public alias namespace: %+v %v", entry, err)
			}
			if entry.Path == knownPath {
				found = true
			}
			inspect(entry.Children)
		}
	}
	inspect(entries)
	if !found {
		t.Fatal("list omitted the known physical file under the alias")
	}
	for _, path := range []string{knownPath, filepath.Join(documents.LocalMaterialsSubdirectory, "known.txt")} {
		read, err := fixture.app.Dispatch(ctx, application.Action{Kind: "document.read", TaskID: "task-a", Path: path})
		if err != nil {
			t.Fatal(err)
		}
		content, err := application.ResultData[documents.Content](read)
		if err != nil || read.Path != knownPath || content.Path != knownPath || content.ReadOnly || content.Text != string(knownBytes) || content.Hash != documents.Hash(knownBytes) {
			t.Fatalf("read lost the alias or exact source bytes: %+v %v", read, err)
		}
	}
	createdBytes := []byte("created through public action\r\n")
	createdPath := filepath.Join(scope.Owned, "created.txt")
	created, err := fixture.app.Dispatch(ctx, application.Action{Kind: "document.create", TaskID: "task-a", Path: scope.Owned, Text: string(createdBytes), Payload: map[string]any{"name": "created.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := application.ResultData[documents.FileOperationResult](created)
	if err != nil || operation.Status != documents.FileOperationCompleted || created.Path != createdPath || operation.RelativePath != filepath.ToSlash(filepath.Join(documents.LocalMaterialsSubdirectory, "created.txt")) {
		t.Fatalf("create did not retain the public destination: %+v %v", created, err)
	}
	localMaterialsOperationRequireBytes(t, fixture.physical(t, createdPath), createdBytes)
	localMaterialsOperationRequireBytes(t, fixture.physical(t, knownPath), knownBytes)
}

func TestLocalMaterialsApplicationOperationsRejectScopeEscapes(t *testing.T) {
	fixture := newLocalMaterialsOperationFixture(t)
	scope := documents.LocalTaskMaterials(filepath.Join(fixture.alias, "task-a"))
	other := documents.LocalTaskMaterials(filepath.Join(fixture.alias, "task-b"))
	secondAlias := filepath.Join(fixture.base, "other-namespace")
	localMaterialsOperationDirectoryLink(t, secondAlias, fixture.actual)
	peer := filepath.Join(scope.Root, "colleague")
	if err := os.MkdirAll(fixture.physical(t, peer), 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(fixture.base, "outside", "sentinel.txt")
	if err := os.MkdirAll(filepath.Dir(outside), 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := []byte("external sentinel must survive\r\n")
	paths := []string{outside, fixture.physical(t, filepath.Join(scope.Owned, "sentinel.txt")), fixture.physical(t, filepath.Join(other.Owned, "sentinel.txt")), fixture.physical(t, filepath.Join(peer, "sentinel.txt"))}
	for _, path := range paths {
		if err := os.WriteFile(path, sentinel, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ignore := fixture.physical(t, filepath.Join(scope.Temporary, ".gitignore"))
	ignoreBytes, err := os.ReadFile(ignore)
	if err != nil {
		t.Fatal(err)
	}
	relativeEscape, err := filepath.Rel(scope.Root, outside)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		name   string
		action application.Action
	}{
		{"parent-relative", application.Action{Kind: "document.read", Path: relativeEscape}},
		{"other-task", application.Action{Kind: "document.read", Path: filepath.Join(other.Owned, "sentinel.txt")}},
		{"physical-absolute", application.Action{Kind: "document.read", Path: fixture.physical(t, filepath.Join(scope.Owned, "sentinel.txt"))}},
		{"other-alias-namespace", application.Action{Kind: "document.read", Path: filepath.Join(secondAlias, "task-a", ".workspace", "materials", documents.LocalMaterialsSubdirectory, "sentinel.txt")}},
		{"outside-destination", application.Action{Kind: "document.create", Path: filepath.Dir(outside), Text: "must not write", Payload: map[string]any{"name": "sentinel.txt"}}},
		{"peer-destination", application.Action{Kind: "document.create", Path: peer, Text: "must not write", Payload: map[string]any{"name": "sentinel.txt"}}},
		{"temp-ignore", application.Action{Kind: "document.create", Path: scope.Temporary, Text: "must not write", Payload: map[string]any{"name": ".gitignore"}}},
	} {
		t.Run(item.name, func(t *testing.T) {
			action := item.action
			action.TaskID = "task-a"
			if result, err := fixture.app.Dispatch(context.Background(), action); err == nil {
				t.Fatalf("out-of-scope public action was accepted: %+v result=%+v", action, result)
			}
			for _, path := range paths {
				localMaterialsOperationRequireBytes(t, path, sentinel)
			}
			localMaterialsOperationRequireBytes(t, ignore, ignoreBytes)
		})
	}
}

func TestLocalMaterialsApplicationOperationsRejectInternalDirectoryLinks(t *testing.T) {
	for _, component := range []string{"task", "workspace", "materials", "docs", "temp", "nested"} {
		t.Run(component, func(t *testing.T) {
			fixture := newLocalMaterialsOperationFixture(t)
			taskPath := filepath.Join(fixture.alias, "task-a")
			scope := documents.LocalTaskMaterials(taskPath)
			link, visibleFile := taskPath, filepath.Join(scope.Owned, "sentinel.txt")
			switch component {
			case "workspace":
				link = filepath.Join(taskPath, ".workspace")
			case "materials":
				link = scope.Root
			case "docs":
				link = scope.Owned
			case "temp":
				link, visibleFile = scope.Temporary, filepath.Join(scope.Temporary, "sentinel.txt")
			case "nested":
				link = filepath.Join(scope.Owned, "nested")
				visibleFile = filepath.Join(link, "sentinel.txt")
			}
			relative, err := filepath.Rel(link, visibleFile)
			if err != nil {
				t.Fatal(err)
			}
			external := filepath.Join(fixture.base, "external-target")
			if component != "nested" {
				// Preserve the complete valid subtree, including the task
				// manifest and temp ignore rule. A missing generated file must
				// not be the reason an internal link is rejected.
				if err := os.CopyFS(external, os.DirFS(fixture.physical(t, link))); err != nil {
					t.Fatal(err)
				}
			}
			sentinelPath := filepath.Join(external, relative)
			if err := os.MkdirAll(filepath.Dir(sentinelPath), 0700); err != nil {
				t.Fatal(err)
			}
			sentinel := []byte("external linked target must survive\r\n")
			if err := os.WriteFile(sentinelPath, sentinel, 0600); err != nil {
				t.Fatal(err)
			}
			if component != "nested" {
				if err := os.Rename(fixture.physical(t, link), filepath.Join(fixture.base, "original-component")); err != nil {
					t.Fatal(err)
				}
			}
			localMaterialsOperationDirectoryLink(t, link, external)
			for _, action := range []application.Action{
				{Kind: "document.read", TaskID: "task-a", Path: visibleFile},
				{Kind: "document.create", TaskID: "task-a", Path: filepath.Dir(visibleFile), Text: "must not overwrite", Payload: map[string]any{"name": "sentinel.txt"}},
			} {
				if result, err := fixture.app.Dispatch(context.Background(), action); err == nil {
					t.Fatalf("internal %s link was accepted by %s: %+v", component, action.Kind, result)
				}
				localMaterialsOperationRequireBytes(t, sentinelPath, sentinel)
			}
		})
	}
}
