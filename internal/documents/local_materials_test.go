package documents

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func localMaterialsDirectoryLink(t *testing.T, path, target string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", path, target).CombinedOutput()
		if err != nil {
			t.Skipf("directory junction unavailable: %v: %s", err, output)
		}
	} else if err := os.Symlink(target, path); err != nil {
		t.Skipf("directory link unavailable: %v", err)
	}
}

func TestLocalMaterialsAllowTaskRootAncestorLink(t *testing.T) {
	base := t.TempDir()
	realRoot, linkedRoot := filepath.Join(base, "real"), filepath.Join(base, "configured")
	if err := os.MkdirAll(filepath.Join(realRoot, "task"), 0755); err != nil {
		t.Fatal(err)
	}
	localMaterialsDirectoryLink(t, linkedRoot, realRoot)
	task := filepath.Join(linkedRoot, "task")
	scope, err := PrepareLocalTaskMaterials(task)
	if err != nil {
		t.Fatal("configured TaskRoot ancestor link rejected", err)
	}
	if text, err := scope.DirectoryInstructions(false); err != nil || !strings.Contains(text, scope.Owned) {
		t.Fatalf("instructions did not preserve configured paths: %s %v", text, err)
	}
	description := DescribeMaterials(scope.Owned)
	if description.Kind != MaterialsLocal || !description.CanWrite || description.CanSync || description.ReadRoot != scope.Root {
		t.Fatalf("wrong local description: %+v", description)
	}
	if err := CleanupLocalTaskMaterials(task); err != nil {
		t.Fatal("rollback under configured TaskRoot link failed", err)
	}
	if _, err := os.Stat(scope.Root); !os.IsNotExist(err) {
		t.Fatalf("generated materials remain: %v", err)
	}
}

func TestLocalMaterialsInstructionsDescribeUncreatedTask(t *testing.T) {
	task := filepath.Join(t.TempDir(), "planned-task")
	scope := LocalTaskMaterials(task)
	text, err := scope.DirectoryInstructions(true)
	if err != nil || !strings.Contains(text, scope.Owned) || !strings.Contains(text, "不能据此宣称目录已经创建") {
		t.Fatalf("planned local materials unavailable: %s %v", text, err)
	}
	if _, err := os.Stat(task); !os.IsNotExist(err) {
		t.Fatalf("preview created task directory: %v", err)
	}
}

func TestLocalMaterialsRejectTaskAndWorkspaceLinks(t *testing.T) {
	for _, replacement := range []string{"task", "workspace"} {
		t.Run(replacement, func(t *testing.T) {
			base := t.TempDir()
			external := filepath.Join(base, "external")
			sentinel := filepath.Join(external, "materials", "docs", "keep.md")
			if err := os.MkdirAll(filepath.Dir(sentinel), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			task := filepath.Join(base, "task")
			link := task
			if replacement == "workspace" {
				if err := os.Mkdir(task, 0755); err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(task, ".workspace")
			}
			localMaterialsDirectoryLink(t, link, external)
			if _, err := PrepareLocalTaskMaterials(task); err == nil {
				t.Fatal("linked task or workspace accepted")
			}
			if err := CleanupLocalTaskMaterials(task); err == nil {
				t.Fatal("rollback accepted linked task or workspace")
			}
			if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
				t.Fatalf("linked external data changed: %q %v", data, err)
			}
		})
	}
}

func TestStandaloneMaterialsScopeAndSafeRollback(t *testing.T) {
	task := t.TempDir()
	scope, err := PrepareLocalTaskMaterials(task)
	if err != nil {
		t.Fatal(err)
	}
	if !scope.Writable(filepath.Join(scope.Owned, "file.md"), false) || !scope.Writable(filepath.Join(scope.Temporary, "scratch.txt"), false) || scope.Writable(filepath.Join(scope.Root, "peer", "file.md"), false) || scope.Writable(filepath.Join(scope.Temporary, ".gitignore"), false) {
		t.Fatal("local materials permissions are incorrect")
	}
	if _, ok := AnnualScope(scope.Owned); ok {
		t.Fatal("local material matches the annual Git scope")
	}
	info, err := scope.DirectoryInstructions(false)
	if err != nil || !strings.Contains(info, "不参与需求资料 Git 同步") {
		t.Fatalf("invalid local instructions: %s %v", info, err)
	}
	file := filepath.Join(scope.Owned, "user.md")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CleanupLocalTaskMaterials(task); err == nil {
		t.Fatal("rollback silently removed user material")
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "keep" {
		t.Fatal("user material changed")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(scope.Temporary, "keep.txt")
	if err := os.WriteFile(temporary, []byte("keep temporary content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CleanupLocalTaskMaterials(task); err == nil {
		t.Fatal("rollback removed temporary user content")
	}
	if _, err := os.Stat(filepath.Join(scope.Temporary, ".gitignore")); err != nil {
		t.Fatal("failed cleanup removed temp ignore protection")
	}
	if err := os.Remove(temporary); err != nil {
		t.Fatal(err)
	}
	if err := CleanupLocalTaskMaterials(task); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(scope.Root); !os.IsNotExist(err) {
		t.Fatalf("empty generated materials not cleaned: %v", err)
	}
}
