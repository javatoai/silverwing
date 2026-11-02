//go:build windows

package documents

import (
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// Exercise the native non-UI API against a disposable folder, find its own
// recycle receipt, verify the bytes and restore it. No user directory is used.
func TestNativeDirectoryRecycleAndRestore(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "资料 原生回收")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	want := "\xef\xbb\xbf中文完整内容\r\n"
	if err := os.WriteFile(filepath.Join(source, ".hidden.txt"), []byte(want), 0600); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(parent, "protected-target")
	if err := os.Mkdir(external, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "sentinel.txt"), []byte("external bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(source, "external-link")
	if output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", junction, external).CombinedOutput(); err != nil {
		t.Fatalf("isolated junction fixture: %s %v", output, err)
	}
	originalTarget, err := os.Readlink(junction)
	if err != nil {
		t.Fatal(err)
	}
	if err := (SystemDirectoryRecycler{}).Recycle(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(source); !os.IsNotExist(err) {
		t.Fatal("native source still present")
	}
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(filepath.VolumeName(parent)+string(filepath.Separator), "$Recycle.Bin", tokenUser.User.Sid.String())
	entries, err := os.ReadDir(bin)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "$I") {
			continue
		}
		receipt := filepath.Join(bin, entry.Name())
		data, err := os.ReadFile(receipt)
		if err != nil || len(data) < 28 {
			continue
		}
		offset := 24
		if binary.LittleEndian.Uint64(data) == 2 {
			offset = 28
		}
		var characters []uint16
		for i := offset; i+1 < len(data); i += 2 {
			characters = append(characters, binary.LittleEndian.Uint16(data[i:]))
		}
		if !strings.EqualFold(windows.UTF16ToString(characters), source) {
			continue
		}
		preserved := filepath.Join(bin, "$R"+entry.Name()[2:])
		// Only this fixture's receipt can authorize moving a recycle-bin item.
		if filepath.Dir(preserved) != bin || filepath.Dir(source) != parent {
			t.Fatal("fixture containment failed")
		}
		if err := os.Rename(preserved, source); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(receipt); err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(filepath.Join(source, ".hidden.txt")); err != nil || string(got) != want {
			t.Fatalf("restored bytes: %q %v", got, err)
		}
		if got, err := os.ReadFile(filepath.Join(external, "sentinel.txt")); err != nil || string(got) != "external bytes" {
			t.Fatalf("external target touched: %q %v", got, err)
		}
		if target, err := os.Readlink(junction); err != nil || target != originalTarget {
			t.Fatalf("junction changed: %s %v", target, err)
		}
		return
	}
	t.Fatal("native operation did not produce a matching recycle receipt")
}
