package larkcli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type preparationError struct{ error }

// npm's Windows shim forwards through cmd.exe, whose argument grammar cannot
// safely represent JSON quotes. Use the native binary from that same, verified
// official package. Never relax shared process quoting or evaluate shell text.
func nativeCommand(command string) (string, error) {
	ext := strings.ToLower(filepath.Ext(command))
	if runtime.GOOS != "windows" || ext != ".cmd" && ext != ".ps1" {
		return command, nil
	}
	if !strings.EqualFold(filepath.Base(command), "lark-cli"+ext) {
		return "", errors.New("请选用 lark-cli.exe 或 npm 原装 lark-cli 启动文件")
	}
	base := filepath.Dir(command)
	pkg := filepath.Join(base, "node_modules", "@larksuite", "cli")
	data, e := os.ReadFile(filepath.Join(pkg, "package.json"))
	if e != nil {
		return "", errors.New("未找到 Lark 原生程序，请选择 lark-cli.exe 或修复本地 CLI 安装")
	}
	var metadata struct {
		Name string
		Bin  map[string]string
	}
	if json.Unmarshal(data, &metadata) != nil || metadata.Name != "@larksuite/cli" || metadata.Bin["lark-cli"] != "scripts/run.js" {
		return "", errors.New("Lark CLI 安装结构无法识别，请选择原生 lark-cli.exe")
	}
	shim, e := os.ReadFile(command)
	if e != nil {
		return "", e
	}
	normalized := strings.ReplaceAll(string(shim), `\`, "/")
	if !strings.Contains(normalized, "@larksuite/cli/scripts/run.js") {
		return "", errors.New("Lark 启动脚本不是已识别的 npm 启动器，请选择原生 lark-cli.exe")
	}
	binary := filepath.Join(pkg, "bin", "lark-cli.exe")
	info, e := os.Stat(binary)
	if e != nil || !info.Mode().IsRegular() {
		return "", errors.New("Lark 原生程序缺失，请修复本地 CLI 安装")
	}
	return binary, nil
}
