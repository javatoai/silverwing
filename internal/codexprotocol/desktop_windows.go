//go:build windows

package codexprotocol

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"silverwing-go/internal/process"
)

// FindWindowSW obtains the automation object owned by the real Explorer
// desktop, rather than creating a Shell.Application in the caller's elevation.
// URI data travels in a child-only environment variable, never as script text.
const desktopActivationScript = `$ErrorActionPreference='Stop'
[Console]::OutputEncoding=New-Object System.Text.UTF8Encoding($false)
try {
 $shell=New-Object -ComObject Shell.Application
 $windows=$shell.Windows()
 $desktopHandle=0
 $desktop=$windows.FindWindowSW(0,0,8,[ref]$desktopHandle,1)
 if($null -eq $desktop) { throw 'Interactive desktop unavailable' }
 $document=$desktop.Document
 $application=$document.Application
 $application.ShellExecute($env:SILVERWING_CODEX_OPEN_URI,'','','open',1)
 @{accepted=$true;hresult=0} | ConvertTo-Json -Compress
} catch {
 $failure=$_.Exception
 while($null -ne $failure.InnerException) { $failure=$failure.InnerException }
 @{accepted=$false;hresult=[long]$failure.HResult} | ConvertTo-Json -Compress
 exit 1
} finally {
 foreach($item in @($application,$document,$desktop,$windows,$shell)) {
  if($null -ne $item -and [Runtime.InteropServices.Marshal]::IsComObject($item)) {
   try { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($item) } catch {}
  }
 }
}`

func tokenElevated(token windows.Token) (bool, error) {
	var elevated, length uint32
	err := windows.GetTokenInformation(token, windows.TokenElevation, (*byte)(unsafe.Pointer(&elevated)), 4, &length)
	return elevated != 0, err
}

func currentProcessElevated() (bool, error) {
	return tokenElevated(windows.GetCurrentProcessToken())
}

func validateDesktopUser() error {
	window, _, _ := windows.NewLazySystemDLL("user32.dll").NewProc("GetShellWindow").Call()
	if window == 0 {
		return errors.New("未找到普通用户桌面，无法转交 Codex 打开请求")
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(windows.HWND(window), &pid); err != nil {
		return err
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	current, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	if user.User.Sid.String() != current.User.Sid.String() {
		return errors.New("应用与桌面属于不同用户，请用当前桌面账号启动 Silverwing 后重试")
	}
	elevated, err := tokenElevated(token)
	if err != nil {
		return err
	}
	if elevated {
		return errors.New("当前桌面也在管理员模式运行，请从普通用户桌面打开 Codex")
	}
	return nil
}

func desktopActivationSpec(uri string) (process.Spec, error) {
	systemDir, err := windows.GetSystemDirectory()
	if err != nil {
		return process.Spec{}, err
	}
	units := utf16.Encode([]rune(desktopActivationScript))
	bytes := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(bytes[i*2:], unit)
	}
	return process.Spec{
		Executable: filepath.Join(systemDir, "WindowsPowerShell", "v1.0", "powershell.exe"),
		Arguments:  []string{"-NoProfile", "-NonInteractive", "-STA", "-EncodedCommand", base64.StdEncoding.EncodeToString(bytes)},
		Env:        map[string]string{"SILVERWING_CODEX_OPEN_URI": uri},
		Timeout:    8 * time.Second, MaxStdoutBytes: 8 << 10, MaxStderrBytes: 8 << 10,
	}, nil
}

func desktopActivationResult(result process.Result) error {
	var reply struct {
		Accepted bool  `json:"accepted"`
		HResult  int64 `json:"hresult"`
	}
	if result.StdoutTruncated || json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(result.Stdout), "\ufeff")), &reply) != nil {
		return errors.New("普通桌面打开请求的结果无法确认，请检查 Codex 后重试")
	}
	if reply.Accepted && result.Successful() {
		return nil
	}
	code := uint32(reply.HResult)
	if code&0xffff0000 == 0x80070000 {
		return activationError(syscall.Errno(code & 0xffff))
	}
	return fmt.Errorf("普通桌面未接受 Codex 打开请求（HRESULT 0x%08X）", code)
}

func (s nativeSystem) activateDesktop(ctx context.Context, uri string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateDesktopUser(); err != nil {
		return err
	}
	spec, err := desktopActivationSpec(uri)
	if err != nil {
		return err
	}
	result, err := s.runner.Run(ctx, spec)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("转交普通桌面失败：%w", err)
	}
	return desktopActivationResult(result)
}
