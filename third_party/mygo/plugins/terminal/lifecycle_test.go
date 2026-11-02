package terminal

import (
	"io"
	"os/exec"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal/internal/pty"
	"github.com/egoist/mygo/plugins/terminal/internal/vt"
)

type lifecycleMemoryConn struct {
	done chan struct{}
	once sync.Once
}

func (c *lifecycleMemoryConn) Read([]byte) (int, error)    { <-c.done; return 0, io.EOF }
func (c *lifecycleMemoryConn) Write(b []byte) (int, error) { return len(b), nil }
func (c *lifecycleMemoryConn) Close() error                { c.once.Do(func() { close(c.done) }); return nil }

func TestTerminalLifecycleCloseReleasesNativeViewInputs(t *testing.T) {
	loadLib(t)
	term, err := New(Options{Conn: &lifecycleMemoryConn{done: make(chan struct{})}})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	v := &view{t: term}
	term.mu.Lock()
	term.v = v
	v.keys, err = vt.NewKeyEncoder()
	if err == nil {
		v.mouse, err = vt.NewMouseEncoder()
	}
	if err == nil {
		v.gesture, err = vt.NewGesture(500e6, 5)
	}
	term.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := term.Close(); err != nil {
		t.Fatal(err)
	}
	if v.keys != nil || v.mouse != nil || v.gesture != nil {
		t.Fatal("native view input objects survived Close without a final View frame")
	}
	if err := term.Close(); err != nil {
		t.Fatal("Close is not idempotent", err)
	}
}

func TestTerminalLifecycleCloseDuringPendingSpawnWaitsForExit(t *testing.T) {
	loadLib(t)
	command := []string{"/bin/sh", "-c", "sleep 60"}
	if runtime.GOOS == "windows" {
		shell, err := exec.LookPath("powershell.exe")
		if err != nil {
			t.Fatal(err)
		}
		command = []string{shell, "-NoProfile", "-NoLogo", "-Command", "Start-Sleep -Seconds 60"}
	}
	term, err := New(Options{Dir: t.TempDir(), Command: command})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	started := make(chan *pty.PTY, 1)
	term.mu.Lock()
	term.timer.Stop()
	launch := term.launch
	term.launch = nil
	term.startPTY = func(o pty.Options) (*pty.PTY, error) {
		close(entered)
		<-release
		process, err := pty.Start(o)
		started <- process
		return process, err
	}
	term.mu.Unlock()
	go term.start(launch)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("controlled spawn did not enter")
	}
	if err := term.Close(); err != nil {
		t.Fatal(err)
	}
	close(release)
	var process *pty.PTY
	select {
	case process = <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("controlled process did not start")
	}
	if process == nil {
		t.Fatal("controlled spawn failed")
	}
	defer func() {
		go io.Copy(io.Discard, process)
		process.Close()
		select {
		case <-process.Exited():
		case <-time.After(5 * time.Second):
			process.Kill()
			process.Wait()
		}
	}()
	select {
	case <-term.Done():
	case <-time.After(6 * time.Second):
		t.Fatal("closed startup did not drain")
	}
	select {
	case <-process.Exited():
	default:
		t.Fatal("Done preceded owned process exit")
	}
}
