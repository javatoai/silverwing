package workspace

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

// The performance contract is a bounded number of real Git processes, not a
// wall-clock threshold that changes with the machine running the test.
func TestGitComparePrepareBatchesWorkspaceIdentity(t *testing.T) {
	for _, strategy := range []domain.WorkspaceStrategy{domain.WorkspaceStandardWorktree, domain.WorkspaceIndependentClone} {
		t.Run(string(strategy), func(t *testing.T) {
			repo := newCompareTestRepo(t)
			writeCompareFile(t, repo.path, "base.txt", "base\n")
			commitCompare(t, repo.path, "base")
			head := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
			root := filepath.Join(t.TempDir(), "workspace with spaces")
			service, ws := repo.serviceWorkspace("feature")
			ws.WorktreePath, ws.Strategy = root, strategy
			if strategy == domain.WorkspaceStandardWorktree {
				gitCompare(t, repo.path, "worktree", "add", "-b", "feature", root)
			} else {
				gitCompare(t, repo.path, "clone", "--no-hardlinks", repo.path, root)
				gitCompare(t, root, "checkout", "-b", "feature")
				origin := gitCompareOutput(t, root, "remote", "get-url", "origin")
				ws.OriginURL = &origin
			}
			writeCompareFile(t, root, "base.txt", "snapshot\n")
			trace := filepath.Join(t.TempDir(), "prepare-trace.jsonl")
			t.Setenv("GIT_TRACE2_EVENT", filepath.ToSlash(trace))
			manager := NewWorkspaceGitDiffSessionManager()
			t.Cleanup(func() { _ = manager.Close(context.Background()) })
			request := CompareRequest{Mode: CompareUncommitted}

			files, err := service.FilesSessionContext(context.Background(), manager, "prepare-task", "prepare-config", ws, request, WorkspaceMainBranch{})
			if err != nil {
				t.Fatal(err)
			}
			assertComparePaths(t, files.Files, "base.txt")
			if files.SessionID == "" || files.BaseCommit != head || !strings.HasPrefix(files.TargetCommit, "worktree:"+head+":") {
				t.Fatalf("prepared snapshot = %+v, want the current HEAD and a worktree snapshot", files)
			}

			identityProcesses, topReads, branchReads, expectedCommonReads, originReads := 0, 0, 0, 0, 0
			for _, argv := range readGitComparePrepareCommands(t, trace) {
				var directory string
				var args []string
				for index := 0; index+2 < len(argv); index++ {
					if argv[index] == "-C" {
						directory, args = argv[index+1], argv[index+2:]
						break
					}
				}
				if len(args) == 0 {
					continue
				}
				identityQuery := false
				if args[0] == "rev-parse" {
					for _, arg := range args[1:] {
						switch arg {
						case "--show-toplevel":
							topReads++
							identityQuery = true
						case "--git-common-dir":
							identityQuery = true
							if samePath(directory, ws.RepositoryPath) {
								expectedCommonReads++
							}
						}
					}
				} else if len(args) == 2 && args[0] == "branch" && args[1] == "--show-current" {
					branchReads++
					identityQuery = true
				} else if len(args) == 3 && args[0] == "remote" && args[1] == "get-url" && args[2] == "origin" {
					originReads++
					identityQuery = true
				}
				if identityQuery {
					identityProcesses++
				}
			}
			if identityProcesses > 6 {
				t.Errorf("cold preparation used %d Git identity processes, want at most 6 while retaining both validations", identityProcesses)
			}
			if topReads != 2 || branchReads != 2 {
				t.Errorf("top/branch reads = %d/%d, want 2/2 for the validations before and after locking", topReads, branchReads)
			}
			if strategy == domain.WorkspaceStandardWorktree && expectedCommonReads != 2 {
				t.Errorf("recorded repository common-directory reads = %d, want 2", expectedCommonReads)
			}
			if strategy == domain.WorkspaceIndependentClone && originReads != 2 {
				t.Errorf("clone origin reads = %d, want 2", originReads)
			}
			t.Logf("identity_processes=%d top_reads=%d branch_reads=%d recorded_common_reads=%d origin_reads=%d", identityProcesses, topReads, branchReads, expectedCommonReads, originReads)

			wantError := "独立克隆 origin 与任务记录不一致"
			if strategy == domain.WorkspaceStandardWorktree {
				other := newCompareTestRepo(t)
				ws.RepositoryPath = other.path
				wantError = "Worktree Git 身份与任务记录不一致"
			} else {
				wrongOrigin := "https://example.invalid/other.git"
				ws.OriginURL = &wrongOrigin
			}
			if _, err := service.FilesSessionContext(context.Background(), manager, "prepare-task", "prepare-config", ws, request, WorkspaceMainBranch{}); err == nil || !strings.Contains(err.Error(), wantError) {
				t.Fatalf("changed workspace identity error = %v, want %q", err, wantError)
			}
		})
	}
}

func TestGitComparePreparePreservesDetachedBranchSemantics(t *testing.T) {
	repo := newCompareTestRepo(t)
	writeCompareFile(t, repo.path, "base.txt", "base\n")
	commitCompare(t, repo.path, "base")
	gitCompare(t, repo.path, "checkout", "--detach")
	writeCompareFile(t, repo.path, "base.txt", "snapshot\n")
	service, ws := repo.serviceWorkspace("main")
	manager := NewWorkspaceGitDiffSessionManager()
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	request := CompareRequest{Mode: CompareUncommitted}
	if _, err := service.FilesSessionContext(context.Background(), manager, "task", "config", ws, request, WorkspaceMainBranch{}); err == nil || !strings.Contains(err.Error(), "工作区分支与任务记录不一致") {
		t.Fatalf("detached workspace with a recorded branch error = %v", err)
	}
	// branch --show-current reports an empty name for detached HEAD. Keep that
	// existing behavior rather than treating the rev-parse spelling HEAD as a branch.
	ws.Branch = ""
	files, err := service.FilesSessionContext(context.Background(), manager, "task", "config", ws, request, WorkspaceMainBranch{})
	if err != nil {
		t.Fatal(err)
	}
	assertComparePaths(t, files.Files, "base.txt")
}

func TestGitComparePrepareAcceptsNewlineWorkspacePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filenames cannot contain newlines")
	}
	repo := newCompareTestRepo(t)
	writeCompareFile(t, repo.path, "base.txt", "base\n")
	commitCompare(t, repo.path, "base")
	root := filepath.Join(t.TempDir(), "workspace\nwith newline")
	gitCompare(t, repo.path, "worktree", "add", "-b", "feature", root)
	writeCompareFile(t, root, "base.txt", "snapshot\n")
	service, ws := repo.serviceWorkspace("feature")
	ws.WorktreePath = root
	manager := NewWorkspaceGitDiffSessionManager()
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	files, err := service.FilesSessionContext(context.Background(), manager, "task", "config", ws, CompareRequest{Mode: CompareUncommitted}, WorkspaceMainBranch{})
	if err != nil {
		t.Fatal(err)
	}
	assertComparePaths(t, files.Files, "base.txt")
}

func readGitComparePrepareCommands(t *testing.T, path string) [][]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var commands [][]string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 2*1024*1024)
	for scanner.Scan() {
		var event struct {
			Event string   `json:"event"`
			Argv  []string `json:"argv"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("decode Git trace event: %v", err)
		}
		if event.Event == "start" {
			commands = append(commands, event.Argv)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return commands
}
