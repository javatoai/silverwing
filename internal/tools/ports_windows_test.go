//go:build windows

package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestWindowsPortTerminationIdentity(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsPortTerminationHelper$")
	cmd.Env = append(os.Environ(), "SILVERWING_PORT_TERMINATION_HELPER=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("owned child was not reaped")
		}
	})
	if ready, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || ready != "ready\n" {
		t.Fatalf("owned child did not become ready: %q %v", ready, err)
	}
	handle, err := syscall.OpenProcess(0x1000|syscall.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(handle)
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		t.Fatal(err)
	}
	started := time.Unix(0, creation.Nanoseconds()).UTC()
	item := PortProcess{PID: cmd.Process.Pid, StartedAt: started.Format(time.RFC3339Nano)}
	service := NewPortService(nil)
	wrong := item
	wrong.StartedAt = started.Add(-time.Second).Format(time.RFC3339Nano)
	if err := service.forceTerminatePortProcess(context.Background(), wrong); err == nil {
		t.Fatal("stale process identity was accepted")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.forceTerminatePortProcess(cancelled, item); err == nil {
		t.Fatal("cancelled termination was accepted")
	}
	if event, err := syscall.WaitForSingleObject(handle, 0); err != nil || event != syscall.WAIT_TIMEOUT {
		t.Fatalf("rejected request ended the owned child: %d %v", event, err)
	}
	if err := service.forceTerminatePortProcess(context.Background(), item); err != nil {
		t.Fatalf("valid exact process identity was rejected: %v", err)
	}
	if event, err := syscall.WaitForSingleObject(handle, 5000); err != nil || event != syscall.WAIT_OBJECT_0 {
		t.Fatalf("owned child did not exit: %d %v", event, err)
	}
}

func TestWindowsPortTerminationHelper(t *testing.T) {
	if os.Getenv("SILVERWING_PORT_TERMINATION_HELPER") != "1" {
		return
	}
	fmt.Print("ready\n")
	time.Sleep(30 * time.Second)
}
