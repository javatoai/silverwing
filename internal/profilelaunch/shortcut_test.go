//go:build windows || darwin

package profilelaunch

import (
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/productprofile"
	"silverwing-go/internal/storage"
)

func TestCreateShortcutRequiresConfirmationForNormalizedDestination(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "Profile 空格")
	id, err := storage.LoadOrCreateProfileID(home)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	spec := Shortcut{Executable: executable, Home: home, ProfileID: id}
	suggestion := productprofile.Name + " - Profile 空格" + ShortcutExtension()
	if got := SuggestedShortcutName(home); got != suggestion {
		t.Fatalf("suggested shortcut = %q, want %q", got, suggestion)
	}
	chosen := filepath.Join(root, "Custom name")
	destination := chosen + ShortcutExtension()
	if err := CreateShortcut(chosen, spec, nil); err != nil {
		t.Fatal(err)
	}
	original, err := ShortcutOccupant(destination)
	if err != nil || original == nil {
		t.Fatalf("normalized shortcut missing: %v, %v", original, err)
	}
	if err := CreateShortcut(chosen, spec, nil); err == nil {
		t.Fatal("creation overwrote the normalized destination without confirmation")
	}
	current, err := ShortcutOccupant(destination)
	if err != nil || !os.SameFile(original, current) {
		t.Fatalf("destination changed without confirmation: %v, %v", current, err)
	}
	if err := CreateShortcut(chosen, spec, original); err != nil {
		t.Fatalf("confirmed shortcut replacement failed: %v", err)
	}
	current, err = ShortcutOccupant(destination)
	if err != nil || current == nil || os.SameFile(original, current) {
		t.Fatalf("confirmed shortcut was not replaced: %v, %v", current, err)
	}
}
