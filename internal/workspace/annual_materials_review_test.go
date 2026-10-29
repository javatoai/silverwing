package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestAnnualGitMergeHistoryRejectsTransientForbiddenTrees(t *testing.T) {
	for _, kind := range []string{"temp", "peer"} {
		t.Run(kind, func(t *testing.T) {
			f := newMaterialsGitFixture(t, true)
			f.sync()
			remoteHead := materialsGitTestRun(t, f.remote, "rev-parse", "master")
			materialsGitTestRun(t, f.root, "branch", "right")
			materialsGitTestRun(t, f.root, "checkout", "-b", "left")
			materialsGitTestFile(t, f.root, "2026/123-需求/研发/left.txt", "left")
			materialsGitTestRun(t, f.root, "add", "--", "2026/123-需求/研发/left.txt")
			materialsGitTestRun(t, f.root, "commit", "-m", "owned left")
			materialsGitTestRun(t, f.root, "checkout", "right")
			materialsGitTestFile(t, f.root, "2026/123-需求/研发/right.txt", "right")
			materialsGitTestRun(t, f.root, "add", "--", "2026/123-需求/研发/right.txt")
			materialsGitTestRun(t, f.root, "commit", "-m", "owned right")
			materialsGitTestRun(t, f.root, "checkout", "master")
			materialsGitTestRun(t, f.root, "merge", "--no-ff", "--no-commit", "left")
			target, original := "2026/123-需求/temp/private.txt", ""
			if kind == "peer" {
				target, original = "2026/123-需求/产品/design.md", "product\n"
			}
			materialsGitTestFile(t, f.root, target, "FORBIDDEN PRIVATE CONTENT")
			materialsGitTestRun(t, f.root, "add", "-f", "--", target)
			materialsGitTestRun(t, f.root, "commit", "-m", "merge introduces forbidden data")
			introduced := materialsGitTestRun(t, f.root, "rev-parse", "HEAD")
			materialsGitTestRun(t, f.root, "merge", "--no-ff", "--no-commit", "right")
			if kind == "temp" {
				materialsGitTestRun(t, f.root, "rm", "--", target)
			} else {
				materialsGitTestFile(t, f.root, target, original)
				materialsGitTestRun(t, f.root, "add", "--", target)
			}
			materialsGitTestRun(t, f.root, "commit", "-m", "merge restores allowed final tree")
			// Neither ordinary commits nor the final net diff reveal this leak.
			if paths := materialsGitTestRun(t, f.root, "diff", "--name-only", remoteHead, "HEAD"); strings.Contains(paths, target) {
				t.Fatal("fixture did not hide the transient forbidden path from the net diff", paths)
			}
			status, err := f.service.Sync(context.Background(), f.scope, true, nil)
			if err == nil || !status.Paused || materialsGitTestRun(t, f.remote, "rev-parse", "master") != remoteHead {
				t.Fatalf("forbidden merge history was published: status=%+v err=%v introduced=%s", status, err, introduced)
			}
			want := "范围外"
			if kind == "temp" {
				want = "temp"
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatal("fixture paused for a different reason than the forbidden merge", err)
			}
			probe := exec.Command("git", "-C", f.remote, "cat-file", "-e", introduced)
			probe.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(t.TempDir(), "missing"), "GIT_TERMINAL_PROMPT=0")
			probeErr := probe.Run()
			var exited *exec.ExitError
			if !errors.As(probeErr, &exited) {
				t.Fatalf("remote object check was not a Git rejection; forbidden commit may be readable: %v", probeErr)
			}
		})
	}
}

func TestAnnualGitMergeMayInheritAlreadyRemotePeerContent(t *testing.T) {
	for _, firstParentPeer := range []bool{false, true} {
		t.Run(map[bool]string{false: "remote-second-parent", true: "remote-first-parent"}[firstParentPeer], func(t *testing.T) {
			f := newMaterialsGitFixture(t, true)
			f.sync()
			base := materialsGitTestRun(t, f.root, "rev-parse", "HEAD")
			materialsGitTestRun(t, f.root, "checkout", "-b", "owned-side", base)
			materialsGitTestFile(t, f.root, "2026/123-需求/研发/local.txt", "owned")
			materialsGitTestRun(t, f.root, "add", "--", "2026/123-需求/研发/local.txt")
			materialsGitTestRun(t, f.root, "commit", "-m", "owned side")
			materialsGitTestFile(t, f.seed, "2026/123-需求/产品/design.md", "already remote peer")
			materialsGitTestRun(t, f.seed, "add", ".")
			materialsGitTestRun(t, f.seed, "commit", "-m", "peer published remotely")
			materialsGitTestRun(t, f.seed, "push", "origin", "master")
			materialsGitTestRun(t, f.root, "fetch", "origin")
			materialsGitTestRun(t, f.root, "checkout", "master")
			if firstParentPeer {
				materialsGitTestRun(t, f.root, "merge", "--ff-only", "origin/master")
				materialsGitTestRun(t, f.root, "merge", "--no-ff", "--no-edit", "owned-side")
			} else {
				materialsGitTestRun(t, f.root, "merge", "--ff-only", "owned-side")
				materialsGitTestRun(t, f.root, "merge", "--no-ff", "--no-edit", "origin/master")
			}
			f.sync()
			if content := materialsGitTestRun(t, f.remote, "show", "master:2026/123-需求/产品/design.md"); content != "already remote peer" {
				t.Fatal("legitimate remote peer ancestry was blocked or changed", content)
			}
		})
	}
}

func TestAnnualGitMergeCannotRestoreOldLocalParentPeerThenHideIt(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	f.sync()
	peer := "2026/123-需求/产品/design.md"
	materialsGitTestRun(t, f.root, "checkout", "-b", "owned-A")
	materialsGitTestFile(t, f.root, "2026/123-需求/研发/a.md", "owned A")
	materialsGitTestRun(t, f.root, "add", "--", "2026/123-需求/研发/a.md")
	materialsGitTestRun(t, f.root, "commit", "-m", "owned A before remote peer update")
	materialsGitTestFile(t, f.seed, peer, "new remote peer\n")
	materialsGitTestRun(t, f.seed, "add", "--", peer)
	materialsGitTestRun(t, f.seed, "commit", "-m", "remote peer update")
	materialsGitTestRun(t, f.seed, "push", "origin", "master")
	remoteHead := materialsGitTestRun(t, f.remote, "rev-parse", "master")
	materialsGitTestRun(t, f.root, "fetch", "origin")
	materialsGitTestRun(t, f.root, "checkout", "master")
	materialsGitTestRun(t, f.root, "merge", "--ff-only", "origin/master")
	materialsGitTestRun(t, f.root, "branch", "owned-C")
	materialsGitTestRun(t, f.root, "merge", "--no-ff", "--no-commit", "owned-A")
	materialsGitTestFile(t, f.root, peer, "product\n")
	materialsGitTestRun(t, f.root, "add", "--", peer)
	materialsGitTestRun(t, f.root, "commit", "-m", "merge manually selects old local parent peer")
	forbidden := materialsGitTestRun(t, f.root, "rev-parse", "HEAD")
	materialsGitTestRun(t, f.root, "checkout", "owned-C")
	materialsGitTestFile(t, f.root, "2026/123-需求/研发/c.md", "owned C")
	materialsGitTestRun(t, f.root, "add", "--", "2026/123-需求/研发/c.md")
	materialsGitTestRun(t, f.root, "commit", "-m", "owned C after remote peer update")
	materialsGitTestRun(t, f.root, "checkout", "master")
	materialsGitTestRun(t, f.root, "merge", "--no-ff", "--no-commit", "owned-C")
	materialsGitTestFile(t, f.root, peer, "new remote peer\n")
	materialsGitTestRun(t, f.root, "add", "--", peer)
	materialsGitTestRun(t, f.root, "commit", "-m", "merge restores remote peer from other parent")
	for _, commit := range []string{forbidden, "HEAD"} {
		if paths := materialsGitTestRun(t, f.root, "diff-tree", "--root", "--no-commit-id", "--name-only", "--no-renames", "-r", "-c", commit); paths != "" {
			t.Fatal("fixture did not hide the inherited peer changes from combined diffs", paths)
		}
	}
	if paths := materialsGitTestRun(t, f.root, "diff", "--name-only", remoteHead, "HEAD"); strings.Contains(paths, peer) {
		t.Fatal("fixture did not hide the peer changes from the final net diff", paths)
	}
	status, err := f.service.Sync(context.Background(), f.scope, true, nil)
	if err == nil || !status.Paused || !strings.Contains(err.Error(), "范围外") || !strings.Contains(err.Error(), peer) {
		t.Fatal("inherited old peer history was not rejected at the scope boundary", status, err)
	}
	if materialsGitTestRun(t, f.remote, "rev-parse", "master") != remoteHead {
		t.Fatal("inherited old peer merge changed the remote branch")
	}
	probe := exec.Command("git", "-C", f.remote, "cat-file", "-e", forbidden)
	probe.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(t.TempDir(), "missing"), "GIT_TERMINAL_PROMPT=0")
	var rejected *exec.ExitError
	if err := probe.Run(); !errors.As(err, &rejected) {
		t.Fatalf("forbidden merge object was readable remotely or its probe did not run: %v", err)
	}
}

func TestAnnualGitOwnedHistoryBehindRemotePeerUsesTrustedAncestor(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	f.sync()
	owned := "2026/123-需求/研发/owned-behind-remote.md"
	materialsGitTestFile(t, f.root, owned, "owned local commit")
	materialsGitTestRun(t, f.root, "add", "--", owned)
	materialsGitTestRun(t, f.root, "commit", "-m", "owned commit based on old remote")
	peer := "2026/123-需求/产品/design.md"
	materialsGitTestFile(t, f.seed, peer, "new remote peer")
	materialsGitTestRun(t, f.seed, "add", "--", peer)
	materialsGitTestRun(t, f.seed, "commit", "-m", "peer moves ahead of owned branch")
	materialsGitTestRun(t, f.seed, "push", "origin", "master")
	f.sync()
	if content := materialsGitTestRun(t, f.remote, "show", "master:"+owned); content != "owned local commit" {
		t.Fatal("ordinary owned history behind the peer update was rejected", content)
	}
	if content := materialsGitTestRun(t, f.remote, "show", "master:"+peer); content != "new remote peer" {
		t.Fatal("ordinary sync reverted the trusted remote peer update", content)
	}
}

func TestAnnualAgentConcurrentUserEditPreservedAndSafeContextUpdated(t *testing.T) {
	service, manifest, config, task, scope := annualAgentFixture(t)
	router := filepath.Join(task, AgentRouterFileName)
	peer := filepath.Join(scope.Root, "new-peer")
	if err := os.Mkdir(peer, 0755); err != nil {
		t.Fatal(err)
	}
	checks := 0
	warning := service.RefreshMaterialsDirectories(task, manifest, config, func() error {
		checks++
		if checks == 2 {
			file, err := os.OpenFile(router, os.O_WRONLY|os.O_APPEND, 0644)
			if err != nil {
				return err
			}
			_, err = file.WriteString("\nCONCURRENT USER NOTE\n")
			return errors.Join(err, file.Close())
		}
		return nil
	})
	if !errors.Is(warning, errMaterialsCatalogChanged) {
		t.Fatalf("document drift did not become an actionable local retry: %v", warning)
	}
	data, err := os.ReadFile(router)
	if err != nil || !strings.Contains(string(data), "CONCURRENT USER NOTE") || strings.Contains(string(data), peer) {
		t.Fatal("a stale router replacement overwrote the concurrent editor", string(data), err)
	}
	contextPath := filepath.Join(task, AgentWorkspaceDir, AgentDirectoryName, AgentContextFileName)
	data, err = os.ReadFile(contextPath)
	if err != nil || !strings.Contains(string(data), peer) {
		t.Fatal("router conflict prevented safe context refresh", string(data), err)
	}
	if err := service.RefreshMaterialsDirectories(task, manifest, config, nil); err != nil {
		t.Fatal("local retry failed", err)
	}
	data, _ = os.ReadFile(router)
	if !strings.Contains(string(data), peer) || !strings.Contains(string(data), "CONCURRENT USER NOTE") {
		t.Fatal("retry lost either the new catalogue or the user's note")
	}
}

func TestAnnualAgentCustomAndMalformedRouterDoNotBlockContext(t *testing.T) {
	for _, custom := range []string{"# custom router\nkeep this note\n", materialsCatalogStart + "\nmissing end"} {
		service, manifest, config, task, scope := annualAgentFixture(t)
		router := filepath.Join(task, AgentRouterFileName)
		if err := os.WriteFile(router, []byte(custom), 0644); err != nil {
			t.Fatal(err)
		}
		peer := filepath.Join(scope.Root, "new-peer")
		if err := os.Mkdir(peer, 0755); err != nil {
			t.Fatal(err)
		}
		if err := service.RefreshMaterialsDirectories(task, manifest, config, nil); err == nil {
			t.Fatal("unmanageable router did not report a warning")
		}
		data, _ := os.ReadFile(router)
		contextData, _ := os.ReadFile(filepath.Join(task, AgentWorkspaceDir, AgentDirectoryName, AgentContextFileName))
		if string(data) != custom || !strings.Contains(string(contextData), peer) {
			t.Fatal("per-document catalogue refresh overwrote the custom router or skipped safe context")
		}
		if _, err := service.WriteTaskDocument(task, manifest, config); err != nil {
			t.Fatal("optional catalogue warning became a fatal ordinary document error", err)
		}
		if service.MaterialsDirectoryWarnings() == nil {
			t.Fatal("ordinary document generation hid its catalogue warning")
		}
	}
}

func TestAnnualAgentHardLinkedRouterPreservesOutsideFileAndUpdatesSafeContext(t *testing.T) {
	service, manifest, config, task, scope := annualAgentFixture(t)
	router := filepath.Join(task, AgentRouterFileName)
	before, err := os.ReadFile(router)
	if err != nil {
		t.Fatal(err)
	}
	before = append(before, []byte("\nUSER NOTE MUST STAY\n")...)
	if err := os.WriteFile(router, before, 0644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside-user-file.md")
	if err := os.Link(router, outside); err != nil {
		t.Fatal(err)
	}
	peer := filepath.Join(scope.Root, "new-peer")
	if err := os.Mkdir(peer, 0755); err != nil {
		t.Fatal(err)
	}
	err = service.RefreshMaterialsDirectories(task, manifest, config, nil)
	if !errors.Is(err, errMaterialsCatalogLinked) || !strings.Contains(err.Error(), "硬链接") {
		t.Fatal("multi-link router was not rejected with an actionable warning", err)
	}
	for _, path := range []string{router, outside} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != string(before) {
			t.Fatal("catalogue update changed a linked user file", path, string(data), err)
		}
	}
	contextData, err := os.ReadFile(filepath.Join(task, AgentWorkspaceDir, AgentDirectoryName, AgentContextFileName))
	if err != nil || !strings.Contains(string(contextData), peer) {
		t.Fatal("hard-linked router prevented safe context refresh", string(contextData), err)
	}
	if err := os.Remove(outside); err != nil {
		t.Fatal(err)
	}
	if err := service.RefreshMaterialsDirectories(task, manifest, config, nil); err != nil {
		t.Fatal("single-link local retry failed", err)
	}
	data, err := os.ReadFile(router)
	if err != nil || !strings.Contains(string(data), peer) || !strings.Contains(string(data), "USER NOTE MUST STAY") {
		t.Fatal("single-link retry lost the managed update or user's note", string(data), err)
	}
}

func TestMaterialsCatalogNewDocumentHasSingleLinkAndCanRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), AgentRouterFileName)
	before, next := []byte("initial complete router\n"), []byte("updated complete router\n")
	if err := writeMaterialsCatalogFile(path, nil, before, true, nil); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	links, err := materialsCatalogLinkCount(file)
	closeErr := file.Close()
	if err != nil || closeErr != nil || links != 1 {
		t.Fatal("new document retained its temporary publication link", links, err, closeErr)
	}
	if err := writeMaterialsCatalogFile(path, before, next, false, nil); err != nil {
		t.Fatal("normally published document could not be refreshed", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(next) {
		t.Fatal("ordinary single-link catalogue update failed", string(data), err)
	}
}

type failingMaterialsCatalogFile struct {
	data   []byte
	stage  string
	failed bool
}

func (f *failingMaterialsCatalogFile) WriteAt(data []byte, offset int64) (int, error) {
	size := int(offset) + len(data)
	if size > len(f.data) {
		f.data = append(f.data, make([]byte, size-len(f.data))...)
	}
	if f.stage == "write" && !f.failed {
		f.failed = true
		copy(f.data[int(offset):], data[:len(data)/2])
		return len(data) / 2, errors.New("injected write failure")
	}
	copy(f.data[int(offset):], data)
	return len(data), nil
}
func (f *failingMaterialsCatalogFile) Truncate(size int64) error {
	if f.stage == "truncate" && !f.failed {
		f.failed = true
		f.data = f.data[:1]
		return errors.New("injected truncate failure")
	}
	f.data = f.data[:int(size)]
	return nil
}
func (f *failingMaterialsCatalogFile) Sync() error {
	if f.stage == "sync" && !f.failed {
		f.failed = true
		return errors.New("injected sync failure")
	}
	return nil
}

func TestMaterialsCatalogWriteFailureRestoresUserFileAndKeepsDurableCopy(t *testing.T) {
	for _, stage := range []string{"write", "truncate", "sync"} {
		t.Run(stage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), AgentRouterFileName)
			before := []byte("USER PREFIX\nold managed region\nUSER SUFFIX\n")
			file := &failingMaterialsCatalogFile{data: append([]byte(nil), before...), stage: stage}
			err := replaceMaterialsCatalogContents(path, file, before, []byte("new managed region with more bytes than the previous contents"), func() error { return nil })
			if err == nil || string(file.data) != string(before) {
				t.Fatalf("failed update left a truncated/changed user file: %q %v", file.data, err)
			}
			backups, globErr := filepath.Glob(filepath.Join(filepath.Dir(path), ".silverwing-materials-*.bak"))
			if globErr != nil || len(backups) != 1 || !strings.Contains(err.Error(), backups[0]) {
				t.Fatal("failed update did not retain/name its durable recovery copy", backups, err)
			}
			data, readErr := os.ReadFile(backups[0])
			if readErr != nil || string(data) != string(before) {
				t.Fatal("recovery copy did not preserve original user bytes", string(data), readErr)
			}
		})
	}
}

func TestMaterialsCatalogMissingCreateFailurePreservesConcurrentFileAndAllowsRetry(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(map[bool]string{false: "scope-failure", true: "concurrent-file"}[concurrent], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), AgentRouterFileName)
			next := []byte("complete generated router\n")
			stale := errors.New("task identity changed")
			userNote := []byte("concurrent user router\n")
			err := writeMaterialsCatalogFile(path, nil, next, true, func() error {
				if concurrent {
					return os.WriteFile(path, userNote, 0644)
				}
				return stale
			})
			if concurrent {
				data, readErr := os.ReadFile(path)
				if !errors.Is(err, errMaterialsCatalogChanged) || readErr != nil || string(data) != string(userNote) {
					t.Fatal("creation failure replaced or deleted the concurrently created user file", string(data), err, readErr)
				}
			} else {
				if _, statErr := os.Stat(path); !errors.Is(err, stale) || !os.IsNotExist(statErr) {
					t.Fatal("failed creation left an empty/custom placeholder", err, statErr)
				}
				if err := writeMaterialsCatalogFile(path, nil, next, true, nil); err != nil {
					t.Fatal("standalone retry after failed creation was blocked", err)
				}
				data, readErr := os.ReadFile(path)
				if readErr != nil || string(data) != string(next) {
					t.Fatal("retry did not publish the complete router", string(data), readErr)
				}
			}
			staged, globErr := filepath.Glob(filepath.Join(filepath.Dir(path), ".silverwing-materials-*.new"))
			if globErr != nil || len(staged) != 0 {
				t.Fatal("creation left its staged document behind", staged, globErr)
			}
		})
	}
}

func TestAnnualAgentRequiredContextFailureStillRollsBackModuleRemoval(t *testing.T) {
	documents, manifest, config, task, _ := annualAgentFixture(t)
	taskRoot := filepath.Dir(task)
	config.TaskRoot = &taskRoot
	service := NewTaskApplicationService(storage.NewManifestStore(), NewGitClient(nil, "git-must-not-run"), documents.Paths)
	missing := filepath.Join(task, "missing-clone")
	manifest.Services = []domain.ServiceWorkspace{{RepositoryID: "repo", GroupServiceID: "service", ModuleID: "module", WorktreePath: missing, RepositoryPath: missing, Branch: "feature/task", Strategy: domain.WorkspaceIndependentClone, Health: domain.WorkspaceReady, ModuleSource: domain.TaskModuleConfigured, DevelopmentTool: domain.DevelopmentToolIntelliJIdea, TagMode: domain.TagBuildMergeToTargetBranch}}
	if err := service.Manifests.Save(task, manifest); err != nil {
		t.Fatal(err)
	}
	if err := service.Documents.WriteTaskNotes(task, "keep rules"); err != nil {
		t.Fatal(err)
	}
	contextPath := filepath.Join(task, AgentWorkspaceDir, AgentDirectoryName, AgentContextFileName)
	if err := os.Remove(contextPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(contextPath, 0700); err != nil {
		t.Fatal(err)
	}
	preview, err := service.ModuleRemoval.Inspect(config, task, missing)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ModuleRemoval.Remove(context.Background(), config, task, preview, WorkspaceModuleRemovalConfirmation{Fingerprint: preview.Fingerprint}); err == nil {
		t.Fatal("required TASK-CONTEXT I/O failure became an optional catalogue warning")
	}
	saved, err := service.Manifests.Load(task)
	if err != nil || len(saved.Services) != 1 || service.Documents.MaterialsDirectoryWarnings() != nil {
		t.Fatal("required document failure lost the old rollback/error contract", saved, err)
	}
	if notes, err := service.Documents.ReadTaskNotes(task); err != nil || notes != "keep rules" {
		t.Fatal("failed removal changed user rules", notes, err)
	}
}
