//go:build windows

package profilelaunch

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWriteNativeShortcutCreatesWindowsShellLink(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "SilverWing - profile.lnk")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	spec := Shortcut{Executable: executable, Home: filepath.Join(directory, "profile data"), ProfileID: "profile-123", Name: "SilverWing - profile"}
	if err := writeNativeShortcut(path, spec); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 20 {
		t.Fatalf("shortcut is unexpectedly short: %d bytes", len(data))
	}
	if !bytes.Equal(data[:4], []byte{0x4c, 0, 0, 0}) {
		t.Fatalf("shortcut has an invalid Shell Link header: %x", data[:4])
	}
	expectedCLSID := []byte{0x01, 0x14, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}
	if !bytes.Equal(data[4:20], expectedCLSID) {
		t.Fatalf("shortcut has an invalid Shell Link class ID: %x", data[4:20])
	}
}

func TestQuoteWindowsShortcutArgumentsPreservesPathsAndQuotes(t *testing.T) {
	arguments := []string{"--conf-dir", `C:\Users\Jane Doe\SilverWing\`, "--expected-profile-id", `id with "quotes"`}
	got, err := windows.DecomposeCommandLine("silverwing.exe " + quoteWindowsShortcutArguments(arguments))
	if err != nil || len(got) == 0 || !slices.Equal(got[1:], arguments) {
		t.Fatalf("parsed arguments = %q, want %q, error = %v", got, arguments, err)
	}
}
