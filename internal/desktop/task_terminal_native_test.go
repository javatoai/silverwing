package desktop

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
)

// Opt-in headless PTY checks. They neither open a window nor use real task data.
func nativeTerminalTest(t *testing.T, root, script string) *nativeTaskTerminal {
	t.Helper()
	if os.Getenv("SILVERWING_TEST_TERMINAL_RUNTIME") == "" {
		t.Skip("native terminal checks require explicit opt-in and staged runtime")
	}
	if runtime.GOOS != "windows" {
		t.Skip("Windows ConPTY integration; other platforms compile the same adapter")
	}
	shell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Fatal(err)
	}
	term, err := terminal.New(terminal.Options{Dir: root, Command: []string{shell, "-NoLogo", "-NoProfile", "-Command", "[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false); " + script}})
	if err != nil {
		t.Fatal(err)
	}
	adapter := &nativeTaskTerminal{term}
	t.Cleanup(func() {
		if err := adapter.Close(); err != nil {
			t.Error(err)
		}
	})
	return adapter
}

func TestTaskTerminalNativeDirectoryUnicodeExitAndResize(t *testing.T) {
	root := filepath.Join(t.TempDir(), "任务 root 😀")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	term := nativeTerminalTest(t, root, "Write-Output ('__SW_DIR__'+(Get-Location).Path); Write-Output '中文 😀'; exit 7")
	term.Resize(140, 35)
	select {
	case <-term.Done():
	case <-time.After(20 * time.Second):
		t.Fatal("controlled shell did not exit")
	}
	text := term.Text()
	if term.ExitCode() != 7 || !strings.Contains(text, "__SW_DIR__"+root) || !strings.Contains(text, "中文 😀") {
		t.Fatalf("PTY cwd/Unicode/exit mismatch: code=%d output=%q", term.ExitCode(), text)
	}
	if cols, rows := term.Size(); cols != 140 || rows != 35 {
		t.Fatal("resize not applied", cols, rows)
	}
}

func TestTaskTerminalNativeInputAndClosingProcess(t *testing.T) {
	term := nativeTerminalTest(t, t.TempDir(), "Write-Output '__SW_READY__'; $line=[Console]::ReadLine(); Write-Output ('__SW_INPUT__'+$line); exit 0")
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(term.Text(), "__SW_READY__") {
		if time.Now().After(deadline) {
			t.Fatal("controlled reader did not start", term.Text())
		}
		time.Sleep(10 * time.Millisecond)
	}
	term.Send([]byte("hello中文\r"))
	select {
	case <-term.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("input did not reach controlled program", term.Text())
	}
	if !strings.Contains(term.Text(), "__SW_INPUT__hello中文") {
		t.Fatal("input not preserved", term.Text())
	}
	live := nativeTerminalTest(t, t.TempDir(), "Write-Output '__SW_WAITING__'; Start-Sleep -Seconds 60")
	deadline = time.Now().Add(20 * time.Second)
	for !strings.Contains(live.Text(), "__SW_WAITING__") {
		if time.Now().After(deadline) {
			t.Fatal("controlled live program did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := live.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-live.Done():
	default:
		t.Fatal("closing did not release process")
	}
}

func TestTaskTerminalNativeFactoryHonorsCanceledCreation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if process, err := newTaskTerminal(ctx, t.TempDir()); err == nil || process != nil {
		t.Fatal("canceled start created a real shell")
	}
}

func TestTaskTerminalNativeInterrupt(t *testing.T) {
	term := nativeTerminalTest(t, t.TempDir(), "Write-Output '__SW_INTERRUPT_READY__'; Start-Sleep -Seconds 60")
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(term.Text(), "__SW_INTERRUPT_READY__") {
		if time.Now().After(deadline) {
			t.Fatal("controlled interrupt target did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	term.Send([]byte{3})
	select {
	case <-term.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Ctrl+C did not interrupt the controlled command")
	}
}

func TestTaskTerminalNativeDeferredSpawnFailure(t *testing.T) {
	if os.Getenv("SILVERWING_TEST_TERMINAL_RUNTIME") == "" {
		t.Skip("native opt-in required")
	}
	root := filepath.Join(t.TempDir(), "directory-removed-before-spawn")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := newTaskTerminal(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("deferred failure did not finish")
	}
	if p.ExitCode() != -1 {
		t.Fatal("SDK deferred spawn failure contract changed", p.ExitCode())
	}
}
