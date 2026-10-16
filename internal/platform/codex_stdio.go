package platform

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// npm's PowerShell wrapper can query versions, but Windows PowerShell does not
// forward its open stdin pipe to the nested native app-server. Only recognize
// npm's paired Codex wrappers; custom PowerShell scripts retain their behavior.
func codexStdioCommand(command string) (string, error) {
	if runtime.GOOS != "windows" || !filepath.IsAbs(command) || !strings.EqualFold(filepath.Base(command), "codex.ps1") {
		return command, nil
	}
	ps, ok := readCodexNPMShim(command)
	if !ok || !strings.HasPrefix(ps, "#!/usr/bin/env pwsh") ||
		!strings.Contains(ps, "$basedir/node_modules/@openai/codex/bin/codex.js") {
		return command, nil
	}
	paired := strings.TrimSuffix(command, filepath.Ext(command)) + ".cmd"
	cmd, ok := readCodexNPMShim(paired)
	if !ok || !strings.HasPrefix(strings.ToLower(cmd), "@echo off") ||
		!strings.Contains(strings.ToLower(cmd), "set dp0=%~dp0") ||
		!strings.Contains(cmd, "%dp0%/node_modules/@openai/codex/bin/codex.js") {
		return "", fmt.Errorf("Codex npm 的 PowerShell 入口无法转发交互输入；请选择同一安装目录的 codex.cmd 或原生 codex.exe")
	}
	return paired, nil
}

func readCodexNPMShim(path string) (string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return "", false
	}
	data, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		return "", false
	}
	return strings.ReplaceAll(strings.TrimPrefix(string(data), "\ufeff"), `\`, "/"), true
}
