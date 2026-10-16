package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDesktopPreferencesAtomicWriterRetainsPolicy(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	path := filepath.Join("private", "desktop.json")
	if err := atomicWriteFile(path, []byte(`{"width":1200,"height":800,"maximized":false,"settingsSection":"system","exampleUnknown":{"text":"中文"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewDesktopPreferencesStore(path)
	if _, err := store.Update(func(value *DesktopPreferences) { value.Width = 1300 }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || string(raw["exampleUnknown"]) == "" {
		t.Fatalf("replacement lost unknown preference keys: %s %v", data, err)
	}
	value, err := store.Load()
	if err != nil || value.Width != 1300 || value.Height != 800 || value.SettingsSection != "system" {
		t.Fatalf("replacement lost existing navigation/window state: %+v %v", value, err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(filepath.Join(root, path))
		dir, _ := os.Stat(filepath.Join(root, "private"))
		if info.Mode().Perm() != 0o600 || dir.Mode().Perm() != 0o700 {
			t.Fatal("preference private file/directory permissions changed")
		}
	}
	files, err := filepath.Glob(filepath.Join(root, "private", ".silverwing-write-*.tmp"))
	if err != nil || len(files) != 0 {
		t.Fatalf("preference write left temporary files: %v %v", files, err)
	}
}
