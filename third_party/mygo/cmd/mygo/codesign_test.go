package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/internal/wait"
)

// fakeCodesign puts a codesign on PATH that prints "signed" once the file
// it returns exists, as codesign signs once its dialog is answered.
func fakeCodesign(t *testing.T) (answered string) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("codesign signs on macOS only")
	}
	dir := t.TempDir()
	answered = filepath.Join(dir, "answered")
	script := "#!/bin/sh\nwhile [ ! -e '" + answered + "' ]; do sleep 0.01; done\necho signed\n"
	if err := os.WriteFile(filepath.Join(dir, "codesign"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// A test that fails before it answers must not leave the script waiting.
	t.Cleanup(func() { _ = os.WriteFile(answered, nil, 0o644) })
	return answered
}

// consoleOutput sends what the console writes to a buffer, and returns the
// function that reads it.
func consoleOutput(t *testing.T) func() string {
	t.Helper()
	var b bytes.Buffer
	con.mu.Lock()
	w := con.w
	con.w = &b
	con.mu.Unlock()
	t.Cleanup(func() {
		con.mu.Lock()
		con.w = w
		con.mu.Unlock()
	})
	return func() string {
		con.mu.Lock()
		defer con.mu.Unlock()
		return b.String()
	}
}

// codesignWaitsWhen makes signCode take codesign for waiting when the
// channel it returns says so, rather than after codesignPatience.
func codesignWaitsWhen(t *testing.T) chan time.Time {
	t.Helper()
	waits := make(chan time.Time, 1)
	old := codesignWaits
	codesignWaits = func() <-chan time.Time { return waits }
	t.Cleanup(func() { codesignWaits = old })
	return waits
}

// codesign asks for a locked keychain's password in a dialog and waits for
// it without a word where the build runs: the build says so, and signs
// once the dialog is answered.
func TestSignCodeTellsWhatCodesignWaitsFor(t *testing.T) {
	answered := fakeCodesign(t)
	output := consoleOutput(t)
	codesignWaitsWhen(t) <- time.Time{}

	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := signCode("Notes.app", "Developer ID Application: Jane Doe (ABCDE12345)", "--sign", "Developer ID Application: Jane Doe (ABCDE12345)", "Notes.app")
		done <- result{out, err}
	}()
	wait.Until(t, "the build to say what codesign waits for", func() bool { return strings.Contains(output(), "keychain") })
	said := output()
	for _, want := range []string{"Notes.app", "locked keychain", "security unlock-keychain", "security set-key-partition-list"} {
		if !strings.Contains(said, want) {
			t.Errorf("the build said %q, without %q", said, want)
		}
	}
	select {
	case r := <-done:
		t.Fatalf("signCode returned %q, %v before codesign's dialog was answered", r.out, r.err)
	default:
	}

	if err := os.WriteFile(answered, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	r := wait.For(t, "codesign to sign once answered", done)
	if r.err != nil || strings.TrimSpace(string(r.out)) != "signed" {
		t.Errorf("signCode = %q, %v, want codesign's output and no error", r.out, r.err)
	}
	if said := output(); strings.Count(said, "locked keychain") != 1 {
		t.Errorf("the build said it %d times, want once:\n%s", strings.Count(said, "locked keychain"), said)
	}
}

// A codesign that signs right away leaves the build silent.
func TestSignCodeSilentWhenCodesignSigns(t *testing.T) {
	answered := fakeCodesign(t)
	output := consoleOutput(t)
	codesignWaitsWhen(t) // which never says so
	if err := os.WriteFile(answered, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := signCode("Notes.app", "Developer ID Application: Jane Doe (ABCDE12345)", "Notes.app")
	if err != nil || strings.TrimSpace(string(out)) != "signed" {
		t.Errorf("signCode = %q, %v, want codesign's output and no error", out, err)
	}
	if said := output(); said != "" {
		t.Errorf("the build said %q of a codesign that signed", said)
	}
}

// Ad hoc signing reads no keychain, so it has nothing to wait for.
func TestSignCodeAdHocNeverWaits(t *testing.T) {
	answered := fakeCodesign(t)
	output := consoleOutput(t)
	codesignWaitsWhen(t) <- time.Time{}
	if err := os.WriteFile(answered, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := signCode("Notes.app", "-", "--sign", "-", "Notes.app"); err != nil {
		t.Fatalf("signCode = %q, %v", out, err)
	}
	if said := output(); said != "" {
		t.Errorf("the build said %q of ad hoc signing", said)
	}
}
