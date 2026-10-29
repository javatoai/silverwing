package workspace

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

type localMaterialsCatalogFixture struct {
	service             *AgentDocumentService
	manifest            domain.TaskManifest
	config              domain.AppConfig
	public, physical    documents.MaterialsScope
	aliasRoot, realRoot string
}

func localMaterialsCatalogLink(t *testing.T, alias, target string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", alias, target).CombinedOutput()
		if err != nil {
			t.Fatalf("create isolated directory junction: %v: %s", err, output)
		}
	} else if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
}

func newLocalMaterialsCatalogFixture(t *testing.T) localMaterialsCatalogFixture {
	t.Helper()
	base := t.TempDir()
	realRoot, aliasRoot := filepath.Join(base, "physical-tasks"), filepath.Join(base, "configured-tasks")
	if err := os.MkdirAll(filepath.Join(realRoot, "one"), 0700); err != nil {
		t.Fatal(err)
	}
	localMaterialsCatalogLink(t, aliasRoot, realRoot)
	public, err := documents.PrepareLocalTaskMaterials(filepath.Join(aliasRoot, "one"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := domain.DefaultTaskManifest()
	manifest.TaskDirectoryName, manifest.FolderName, manifest.GroupID = "one", "one", "g"
	manifest.CreatedAt = "2026-10-28T10:00:00Z"
	manifest.RequirementMaterials = domain.RequirementMaterialsDirectory{Status: domain.MaterialsReady, WriteRoot: &public.Owned}
	manifests := storage.NewManifestStore()
	if err := manifests.Save(public.Base, manifest); err != nil {
		t.Fatal(err)
	}
	paths := storage.NewPaths(filepath.Join(base, "application-home"))
	service := NewAgentDocumentService(paths, manifests)
	config := domain.DefaultAppConfig()
	config.TaskRoot = &aliasRoot
	if _, err := service.EnsureTaskDocuments(public.Base, manifest, config); err != nil {
		t.Fatal(err)
	}
	if err := service.MaterialsDirectoryWarnings(); err != nil {
		t.Fatalf("initial local catalog generation produced a warning: %v", err)
	}
	return localMaterialsCatalogFixture{service: service, manifest: manifest, config: config, public: public, physical: documents.LocalTaskMaterials(filepath.Join(realRoot, "one")), aliasRoot: aliasRoot, realRoot: realRoot}
}

func localMaterialsCatalogBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func localMaterialsCatalogExpectBytes(t *testing.T, path string, expected []byte) {
	t.Helper()
	if data := localMaterialsCatalogBytes(t, path); !bytes.Equal(data, expected) {
		t.Fatalf("catalog guard changed sentinel %s: got=%q want=%q", path, data, expected)
	}
}

func localMaterialsCatalogNoStaging(t *testing.T, task string) {
	t.Helper()
	for _, directory := range []string{task, filepath.Join(task, AgentWorkspaceDir, AgentDirectoryName)} {
		paths, err := filepath.Glob(filepath.Join(directory, ".silverwing-materials-*"))
		if err != nil || len(paths) != 0 {
			t.Fatalf("catalog guard leaked staged files: %v %v", paths, err)
		}
	}
}

func TestLocalMaterialsCatalogPreservesCheckAndCancellation(t *testing.T) {
	for _, failure := range []error{context.Canceled, errors.New("task/config identity changed")} {
		t.Run(failure.Error(), func(t *testing.T) {
			f := newLocalMaterialsCatalogFixture(t)
			paths := []string{filepath.Join(f.physical.Base, AgentRouterFileName), filepath.Join(f.physical.Base, AgentWorkspaceDir, AgentDirectoryName, AgentContextFileName)}
			before := map[string][]byte{}
			for _, path := range paths {
				before[path] = localMaterialsCatalogBytes(t, path)
			}
			if err := os.Mkdir(filepath.Join(f.physical.Root, "new-peer"), 0700); err != nil {
				t.Fatal(err)
			}
			checks := 0
			err := f.service.RefreshMaterialsDirectories(f.public.Base, f.manifest, f.config, func() error {
				checks++
				if checks == 2 {
					return failure
				}
				return nil
			})
			if !errors.Is(err, failure) || checks != 2 {
				t.Fatalf("catalog write ignored the caller's live guard: checks=%d error=%v", checks, err)
			}
			for path, data := range before {
				localMaterialsCatalogExpectBytes(t, path, data)
			}
			localMaterialsCatalogNoStaging(t, f.physical.Base)
		})
	}
}

func TestLocalMaterialsCatalogRejectsAliasRetargetBeforeCommit(t *testing.T) {
	f := newLocalMaterialsCatalogFixture(t)
	if err := os.Mkdir(filepath.Join(f.physical.Root, "new-peer"), 0700); err != nil {
		t.Fatal(err)
	}
	newRoot := filepath.Join(filepath.Dir(f.realRoot), "replacement-tasks")
	if err := os.CopyFS(newRoot, os.DirFS(f.realRoot)); err != nil {
		t.Fatal(err)
	}
	routerRelative := filepath.Join("one", AgentRouterFileName)
	contextRelative := filepath.Join("one", AgentWorkspaceDir, AgentDirectoryName, AgentContextFileName)
	before := map[string][]byte{}
	for _, relative := range []string{routerRelative, contextRelative} {
		before[relative] = localMaterialsCatalogBytes(t, filepath.Join(f.realRoot, relative))
	}
	checks := 0
	err := f.service.RefreshMaterialsDirectories(f.public.Base, f.manifest, f.config, func() error {
		checks++
		if checks == 2 {
			if err := os.Remove(f.aliasRoot); err != nil {
				return err
			}
			localMaterialsCatalogLink(t, f.aliasRoot, newRoot)
		}
		return nil
	})
	if err == nil || checks != 2 {
		t.Fatalf("retargeted TaskRoot retained catalog commit authority: checks=%d error=%v", checks, err)
	}
	for relative, data := range before {
		for _, root := range []string{f.realRoot, newRoot, f.aliasRoot} {
			localMaterialsCatalogExpectBytes(t, filepath.Join(root, relative), data)
		}
	}
	localMaterialsCatalogNoStaging(t, f.physical.Base)
	localMaterialsCatalogNoStaging(t, filepath.Join(newRoot, "one"))
}

func TestLocalMaterialsCatalogRejectsAgentReplacementBeforeCommit(t *testing.T) {
	f := newLocalMaterialsCatalogFixture(t)
	router := filepath.Join(f.physical.Base, AgentRouterFileName)
	custom := []byte("# custom user router\r\n")
	if err := os.WriteFile(router, custom, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(f.physical.Root, "new-peer"), 0700); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(f.physical.Base, AgentWorkspaceDir, AgentDirectoryName)
	original := localMaterialsCatalogBytes(t, filepath.Join(agent, AgentContextFileName))
	moved := agent + "-old-identity"
	checks := 0
	err := f.service.RefreshMaterialsDirectories(f.public.Base, f.manifest, f.config, func() error {
		checks++
		// The custom router fails before its pre-write callback. The third
		// callback belongs to context, after its real parent has been pinned.
		if checks == 3 {
			if err := os.Rename(agent, moved); err != nil {
				return err
			}
			return os.CopyFS(agent, os.DirFS(moved))
		}
		return nil
	})
	if err == nil || checks != 3 {
		t.Fatalf("real context-parent replacement retained commit authority: checks=%d error=%v", checks, err)
	}
	for _, directory := range []string{agent, moved} {
		localMaterialsCatalogExpectBytes(t, filepath.Join(directory, AgentContextFileName), original)
	}
	localMaterialsCatalogExpectBytes(t, router, custom)
	localMaterialsCatalogNoStaging(t, f.physical.Base)
	paths, err := filepath.Glob(filepath.Join(moved, ".silverwing-materials-*"))
	if err != nil || len(paths) != 0 {
		t.Fatalf("rejected parent replacement leaked staged files in old directory: %v %v", paths, err)
	}
}

func TestLocalMaterialsCatalogMissingRouterRequiresSameManifest(t *testing.T) {
	f := newLocalMaterialsCatalogFixture(t)
	router := filepath.Join(f.physical.Base, AgentRouterFileName)
	if err := os.Remove(router); err != nil {
		t.Fatal(err)
	}
	changed := f.manifest
	changed.CreatedAt = "2026-10-29T10:00:00Z"
	if err := storage.NewManifestStore().Save(f.public.Base, changed); err != nil {
		t.Fatal(err)
	}
	err := f.service.RefreshMaterialsDirectories(f.public.Base, f.manifest, f.config, nil)
	if err == nil || !strings.Contains(err.Error(), "任务身份已变化") {
		t.Fatalf("missing router was restored for another task identity: %v", err)
	}
	if _, err := os.Lstat(router); !os.IsNotExist(err) {
		t.Fatalf("missing router was recreated after identity drift: %v", err)
	}
	localMaterialsCatalogNoStaging(t, f.physical.Base)
}
