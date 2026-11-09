//go:build windows

package codexprotocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"silverwing-go/internal/process"
)

func TestDesktopActivationResultRequiresConfirmedHandoff(t *testing.T) {
	for _, tt := range []struct {
		name, output  string
		exit          int
		fail, missing bool
	}{
		{"accepted", `{"accepted":true,"hresult":0}`, 0, false, false},
		{"nonzero exit", `{"accepted":true,"hresult":0}`, 1, true, false},
		{"missing association", `{"accepted":false,"hresult":-2147023741}`, 1, true, true},
		{"permission denied", `{"accepted":false,"hresult":-2147024891}`, 1, true, false},
		{"invalid reply", `not-json`, 0, true, false},
		{"empty reply", `{}`, 0, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := desktopActivationResult(process.Result{ExitCode: tt.exit, Stdout: tt.output})
			if (err != nil) != tt.fail || errors.Is(err, errAssociationMissing) != tt.missing {
				t.Fatal(err)
			}
		})
	}
}

func TestDesktopActivationKeepsURIOutOfScript(t *testing.T) {
	uri := `codex://threads/new?path=D%3A%5Ctasks%5C%E9%80%80%E5%8D%A1&payload=$(do-not-execute)`
	spec, err := desktopActivationSpec(uri)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Env["SILVERWING_CODEX_OPEN_URI"] != uri || strings.Contains(strings.Join(spec.Arguments, " "), "do-not-execute") || !filepath.IsAbs(spec.Executable) {
		t.Fatal("URI was embedded in command text", spec)
	}
	if _, err := currentProcessElevated(); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (nativeSystem{}).activateDesktop(canceled, uri); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// This opt-in probe targets a private protocol and a windowless marker writer,
// never Codex or a real task. It exercises Explorer's actual COM handoff.
func TestDesktopActivationIsolatedNativeHandoff(t *testing.T) {
	if os.Getenv("SILVERWING_CODEX_DESKTOP_TEST") != "1" {
		t.Skip("opt-in interactive Windows desktop required")
	}
	var packageSize uint32
	status, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCurrentPackageFullName").Call(uintptr(unsafe.Pointer(&packageSize)), 0)
	if status != 15700 { // APPMODEL_ERROR_NO_PACKAGE
		t.Fatal("run the native probe outside MSIX; packaged registry writes are not visible to Explorer")
	}
	if err := validateDesktopUser(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "桌面转交 空格")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "uri.txt")
	source := filepath.Join(dir, "sink.go")
	sink := filepath.Join(dir, "sink.exe")
	code := fmt.Sprintf(`package main
import ("os"; "encoding/json"; "unsafe"; "time"; "golang.org/x/sys/windows")
func main(){
 if len(os.Args)!=2{return}
 var elevated,size uint32
 if windows.GetTokenInformation(windows.GetCurrentProcessToken(),windows.TokenElevation,(*byte)(unsafe.Pointer(&elevated)),4,&size)!=nil{return}
 data,_:=json.Marshal(struct{URI string; Elevated uint32}{os.Args[1],elevated})
 _=os.WriteFile(%q,data,0600)
 time.Sleep(750*time.Millisecond)
 _=os.WriteFile(%q,[]byte("survived"),0600)
}`, marker, marker+".alive")
	if err := os.WriteFile(source, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-ldflags=-H=windowsgui", "-o", sink, source)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build marker writer: %v %s", err, output)
	}
	scheme := fmt.Sprintf("silverwing-codex-desktop-test-%d", time.Now().UnixNano())
	path := `Software\Classes\` + scheme
	t.Cleanup(func() {
		for _, suffix := range []string{`\shell\open\command`, `\shell\open`, `\shell`, ""} {
			if err := registry.DeleteKey(registry.CURRENT_USER, path+suffix); err != nil && !errors.Is(err, registry.ErrNotExist) {
				t.Error(err)
			}
		}
		notifyAssociation.Call(0x08000000, 0, 0, 0)
	})
	key, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	err = key.SetStringValue("URL Protocol", "")
	key.Close()
	if err != nil {
		t.Fatal(err)
	}
	key, _, err = registry.CreateKey(registry.CURRENT_USER, path+`\shell\open\command`, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	err = key.SetStringValue("", syscall.EscapeArg(sink)+` "%1"`)
	key.Close()
	if err != nil {
		t.Fatal(err)
	}
	notifyAssociation.Call(0x08000000, 0, 0, 0)
	uri := scheme + `://threads/new?path=D%3A%5Ctasks%5CDPF%20%E6%96%87%E4%BB%B6%E8%87%AA%E5%8A%A8%E8%A7%A3%E6%9E%90`
	if err := (nativeSystem{runner: process.NewRunner()}).activateDesktop(ctx, uri); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		if data, err := os.ReadFile(marker); err == nil {
			var received struct {
				URI      string
				Elevated uint32
			}
			if err := json.Unmarshal(data, &received); err != nil {
				t.Fatal(err)
			}
			if received.URI != uri || received.Elevated != 0 {
				t.Fatal("desktop must deliver the original URI with ordinary user permissions", string(data))
			}
			if _, err := os.Stat(marker + ".alive"); err == nil {
				return
			}
		}
		select {
		case <-deadline.C:
			t.Fatal("desktop accepted request but marker writer did not receive it")
		case <-time.After(20 * time.Millisecond):
		}
	}
}
