//go:build darwin

package profilelaunch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteNativeShortcutCreatesFinderAppThatPreservesArguments(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "profile launch test")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(directory, "application 'binary")
	argumentOutput := filepath.Join(directory, "captured arguments.txt")
	program := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + quotePOSIXShellArgument(argumentOutput) + "\n"
	if err := os.WriteFile(executable, []byte(program), 0o755); err != nil {
		t.Fatal(err)
	}

	home := filepath.Join(directory, "profile ' with spaces")
	spec := Shortcut{Executable: executable, Home: home, ProfileID: `profile ' with " quotes`, Name: `SilverWing - Profile & "One"`}
	app := filepath.Join(directory, spec.Name+shortcutExtension())
	if err := writeNativeShortcut(app, spec); err != nil {
		t.Fatal(err)
	}

	launcher := filepath.Join(app, "Contents", "MacOS", "launcher")
	if err := exec.Command("/bin/sh", launcher).Run(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(argumentOutput)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join(spec.args(), "\n") + "\n"
	if string(data) != want {
		t.Fatalf("launcher arguments = %q, want %q", data, want)
	}

	plist, err := os.ReadFile(filepath.Join(app, "Contents", "Info.plist"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plist), "SilverWing - Profile &amp; &quot;One&quot;") {
		t.Fatalf("Info.plist did not escape the display name: %s", plist)
	}
}

func TestQuotePOSIXShellArgumentHandlesSingleQuotes(t *testing.T) {
	if got, want := quotePOSIXShellArgument(`A path with 'quotes'`), `'A path with '"'"'quotes'"'"''`; got != want {
		t.Fatalf("quotePOSIXShellArgument() = %q, want %q", got, want)
	}
}
