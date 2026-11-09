//go:build windows

package codexprotocol

import (
	"context"
	"debug/pe"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"silverwing-go/internal/process"
)

// Read only the main desktop application's declared aliases, never the CLI's bin directory.
const storeDiscoveryScript = `$ErrorActionPreference='Stop'
$entries=@(Get-AppxPackage -Name OpenAI.Codex | ForEach-Object {
 $package=$_
 [xml]$manifest=Get-Content -LiteralPath (Join-Path $package.InstallLocation 'AppxManifest.xml') -Raw
 foreach($app in $manifest.Package.Applications.Application) {
  if($app.Id -ne 'App') { continue }
  foreach($extension in $app.Extensions.Extension) {
   if($extension.Category -eq 'windows.appExecutionAlias') {
    foreach($alias in $extension.AppExecutionAlias.ExecutionAlias) {
     [pscustomobject]@{family=$package.PackageFamilyName;alias=$alias.Alias}
    }
   }
  }
 }
})
ConvertTo-Json -InputObject $entries -Compress`

type storeAlias struct {
	Family string `json:"family"`
	Alias  string `json:"alias"`
}

func storeDesktopCommand(local string, aliases []storeAlias) (desktopCommand, error) {
	for _, alias := range aliases {
		if alias.Family != "OpenAI.Codex_2p2nqsd0c76g0" ||
			(alias.Alias != "chatgpt-desktop.exe" && alias.Alias != "codex-desktop.exe") {
			continue
		}
		path := filepath.Join(local, "Microsoft", "WindowsApps", alias.Family, alias.Alias)
		// AppExecutionAlias files are legitimate zero-byte reparse points.
		if info, err := os.Lstat(path); err == nil && !info.IsDir() {
			return desktopCommand{Executable: path, Arguments: []string{"--single-argument"}}, nil
		}
	}
	return desktopCommand{}, errors.New("未找到已声明的 Codex 桌面启动别名")
}

func (s nativeSystem) discover(ctx context.Context) (desktopCommand, error) {
	systemDir, err := windows.GetSystemDirectory()
	if err != nil {
		return desktopCommand{}, err
	}
	local, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return desktopCommand{}, err
	}
	r, runErr := s.runner.Run(ctx, process.Spec{
		Executable: filepath.Join(systemDir, "WindowsPowerShell", "v1.0", "powershell.exe"),
		Arguments:  []string{"-NoProfile", "-NonInteractive", "-Command", storeDiscoveryScript},
		Timeout:    8 * time.Second, MaxStdoutBytes: 64 << 10, MaxStderrBytes: 8 << 10,
	})
	if err := ctx.Err(); err != nil {
		return desktopCommand{}, err
	}
	if runErr == nil && r.Successful() && !r.StdoutTruncated {
		var aliases []storeAlias
		if json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(r.Stdout), "\ufeff")), &aliases) == nil {
			if command, err := storeDesktopCommand(local, aliases); err == nil {
				return command, nil
			}
		}
	}
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		for _, view := range []uint32{registry.WOW64_64KEY, registry.WOW64_32KEY} {
			for _, name := range []string{"Codex.exe"} {
				key, err := registry.OpenKey(root, `Software\Microsoft\Windows\CurrentVersion\App Paths\`+name, registry.READ|view)
				if err != nil {
					continue
				}
				path, _, _ := key.GetStringValue("")
				key.Close()
				path = installedPath(path)
				if desktopGUI(path) {
					return desktopCommand{Executable: path}, nil
				}
			}
			if command, found := installedDesktop(root, view); found {
				return command, nil
			}
		}
	}
	return desktopCommand{}, errors.New("未找到可确认的 Codex 桌面程序，请安装或修复 Codex 后重试")
}

func installedDesktop(root registry.Key, view uint32) (desktopCommand, bool) {
	key, err := registry.OpenKey(root, `Software\Microsoft\Windows\CurrentVersion\Uninstall`, registry.READ|view)
	if err != nil {
		return desktopCommand{}, false
	}
	defer key.Close()
	names, _ := key.ReadSubKeyNames(-1)
	for _, name := range names {
		entry, err := registry.OpenKey(key, name, registry.READ|view)
		if err != nil {
			continue
		}
		display, _, _ := entry.GetStringValue("DisplayName")
		publisher, _, _ := entry.GetStringValue("Publisher")
		location, _, _ := entry.GetStringValue("InstallLocation")
		entry.Close()
		if !strings.EqualFold(strings.TrimSpace(display), "Codex") || !strings.Contains(strings.ToLower(publisher), "openai") {
			continue
		}
		for _, exe := range []string{"Codex.exe", "ChatGPT.exe"} {
			path := filepath.Join(installedPath(location), exe)
			if desktopGUI(path) {
				return desktopCommand{Executable: path}, true
			}
		}
	}
	return desktopCommand{}, false
}

func installedPath(path string) string {
	path = strings.Trim(strings.TrimSpace(path), `"`)
	if expanded, err := registry.ExpandString(path); err == nil {
		return expanded
	}
	return path
}

func desktopGUI(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	image, err := pe.Open(path)
	if err != nil {
		return false
	}
	defer image.Close()
	switch header := image.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		return header.Subsystem == pe.IMAGE_SUBSYSTEM_WINDOWS_GUI
	case *pe.OptionalHeader32:
		return header.Subsystem == pe.IMAGE_SUBSYSTEM_WINDOWS_GUI
	}
	return false
}
