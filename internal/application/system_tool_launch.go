package application

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"

	"silverwing-go/internal/domain"
)

// SystemLaunchPlan is the executable and argument vector for a native desktop
// launch. Building a plan does not start a process.
type SystemLaunchPlan struct {
	Executable string   `json:"executable"`
	Arguments  []string `json:"arguments"`
}

// SystemTerminalResolution mirrors the result of the platform terminal
// resolver. Terminal launches always use the operating system default.
type SystemTerminalResolution struct {
	DisplayName string `json:"displayName"`
	Command     string `json:"command"`
	Source      string `json:"source"`
	Available   bool   `json:"available"`
}

func (a *App) resolveTerminalForLaunch(ctx context.Context, _ *string) (SystemTerminalResolution, error) {
	if a.Platform == nil {
		return SystemTerminalResolution{}, errors.New("系统终端解析服务未初始化")
	}
	data, err := a.Platform.Execute(ctx, "terminal.resolve", nil)
	if err != nil {
		return SystemTerminalResolution{}, err
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return SystemTerminalResolution{}, err
	}
	var resolution SystemTerminalResolution
	if err := json.Unmarshal(encoded, &resolution); err != nil {
		return SystemTerminalResolution{}, err
	}
	if strings.TrimSpace(resolution.Command) == "" {
		return SystemTerminalResolution{}, errors.New("系统终端解析结果缺少命令")
	}
	return resolution, nil
}

// PlanSystemTerminalLaunch builds the argv used by Source DesktopIntegration.
// osName accepts runtime.GOOS values and Java-style Windows/Mac names so the
// argument plan can be inspected without opening a terminal.
func PlanSystemTerminalLaunch(osName string, resolution SystemTerminalResolution, target string) (SystemLaunchPlan, error) {
	command := strings.TrimSpace(resolution.Command)
	if command == "" {
		return SystemLaunchPlan{}, errors.New("终端命令不能为空")
	}
	if resolution.Source != "SYSTEM_DEFAULT" {
		return SystemLaunchPlan{}, fmt.Errorf("终端解析来源无效：%s", resolution.Source)
	}
	workingDirectory, err := absoluteLaunchPath(osName, target)
	if err != nil {
		return SystemLaunchPlan{}, err
	}
	switch {
	case isWindowsOS(osName):
		script := "Set-Location -LiteralPath " + powerShellLiteral(workingDirectory)
		return SystemLaunchPlan{Executable: "powershell.exe", Arguments: []string{"-NoExit", "-Command", script}}, nil
	case isMacOS(osName):
		return SystemLaunchPlan{Executable: "open", Arguments: []string{"-a", "Terminal", workingDirectory}}, nil
	default:
		return SystemLaunchPlan{Executable: "x-terminal-emulator", Arguments: []string{"--working-directory", workingDirectory}}, nil
	}
}

// PlanSystemToolLaunch builds the executable/argv selected by Source's
// DevelopmentToolLaunchCommand. It has no process or filesystem side effects.
func PlanSystemToolLaunch(osName string, toolType domain.DevelopmentToolType, configuredPath, target string) (SystemLaunchPlan, error) {
	configuredPath = strings.TrimSpace(configuredPath)
	if configuredPath == "" {
		return SystemLaunchPlan{}, errors.New("开发工具路径不能为空")
	}
	applicationPath, err := absoluteLaunchPath(osName, configuredPath)
	if err != nil {
		return SystemLaunchPlan{}, err
	}
	projectPath, err := absoluteLaunchPath(osName, target)
	if err != nil {
		return SystemLaunchPlan{}, err
	}
	arguments := []string{}
	if toolType == domain.DevelopmentToolVisualStudioCode {
		arguments = append(arguments, "--new-window")
	}
	arguments = append(arguments, projectPath)

	if isMacOS(osName) && strings.HasSuffix(strings.ToLower(applicationPath), ".app") {
		if toolType == domain.DevelopmentToolVisualStudioCode {
			return SystemLaunchPlan{Executable: "open", Arguments: []string{"-n", "-a", applicationPath, "--args", "--new-window", projectPath}}, nil
		}
		return SystemLaunchPlan{Executable: "open", Arguments: []string{"-a", applicationPath, projectPath}}, nil
	}
	if isWindowsOS(osName) && isWindowsBatch(applicationPath) {
		return windowsBatchLaunchPlan(applicationPath, arguments), nil
	}
	if isWindowsOS(osName) && isJetBrainsTool(toolType) {
		return windowsJetBrainsLaunchPlan(applicationPath, arguments), nil
	}
	return SystemLaunchPlan{Executable: applicationPath, Arguments: arguments}, nil
}

func absoluteLaunchPath(osName, value string) (string, error) {
	if value == "" {
		return "", errors.New("启动路径不能为空")
	}
	if isWindowsOS(osName) && isWindowsAbsolutePath(value) {
		if runtime.GOOS == "windows" {
			return filepath.Clean(value), nil
		}
		return strings.ReplaceAll(path.Clean(strings.ReplaceAll(value, `\`, "/")), "/", `\`), nil
	}
	if !isWindowsOS(osName) && strings.HasPrefix(value, "/") {
		return path.Clean(value), nil
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func isWindowsOS(osName string) bool {
	return strings.HasPrefix(strings.ToLower(osName), "windows") || strings.EqualFold(osName, "win32")
}

func isMacOS(osName string) bool {
	lower := strings.ToLower(osName)
	return strings.HasPrefix(lower, "mac") || lower == "darwin"
}

func isWindowsAbsolutePath(value string) bool {
	if strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, "//") {
		return true
	}
	return len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && (value[2] == '\\' || value[2] == '/')
}

func isWindowsBatch(value string) bool {
	lower := strings.ToLower(value)
	return strings.HasSuffix(lower, ".cmd") || strings.HasSuffix(lower, ".bat")
}

func isJetBrainsTool(toolType domain.DevelopmentToolType) bool {
	switch toolType {
	case domain.DevelopmentToolIntelliJIdea, domain.DevelopmentToolWebStorm, domain.DevelopmentToolPyCharm,
		domain.DevelopmentToolAndroidStudio, domain.DevelopmentToolDevEcoStudio:
		return true
	default:
		return false
	}
}

func windowsJetBrainsLaunchPlan(application string, arguments []string) SystemLaunchPlan {
	quotedArguments := make([]string, len(arguments))
	for i, argument := range arguments {
		quotedArguments[i] = windowsArgument(argument)
	}
	script := "Start-Process -FilePath " + powerShellLiteral(application) + " -ArgumentList " + powerShellLiteral(strings.Join(quotedArguments, " "))
	return SystemLaunchPlan{
		Executable: "powershell.exe",
		Arguments:  []string{"-NoProfile", "-EncodedCommand", encodePowerShellScript(script)},
	}
}

func windowsBatchLaunchPlan(application string, arguments []string) SystemLaunchPlan {
	const executableVariable = "SILVERWING_BATCH_EXECUTABLE"
	argumentVariables := make([]string, len(arguments))
	commandParts := []string{`"%` + executableVariable + `%"`}
	for i := range arguments {
		argumentVariables[i] = fmt.Sprintf("SILVERWING_BATCH_ARGUMENT_%d", i)
		commandParts = append(commandParts, `"%`+argumentVariables[i]+`%"`)
	}
	command := strings.Join(commandParts, " ")
	lines := []string{
		"$ErrorActionPreference = 'Stop'",
		"$ProgressPreference = 'SilentlyContinue'",
		"$startInfo = New-Object System.Diagnostics.ProcessStartInfo",
		"$startInfo.FileName = 'cmd.exe'",
		"$startInfo.UseShellExecute = $false",
		"$startInfo.CreateNoWindow = $true",
		"$startInfo.EnvironmentVariables['" + executableVariable + "'] = " + powerShellLiteral(application),
	}
	for i, argument := range arguments {
		lines = append(lines, "$startInfo.EnvironmentVariables['"+argumentVariables[i]+"'] = "+powerShellLiteral(argument))
	}
	lines = append(lines,
		"$startInfo.Arguments = "+powerShellLiteral("/d /v:off /s /c \""+command+"\""),
		"$process = [System.Diagnostics.Process]::Start($startInfo)",
		"$process.WaitForExit()",
		"exit $process.ExitCode",
	)
	newline := "\n"
	if runtime.GOOS == "windows" {
		newline = "\r\n"
	}
	return SystemLaunchPlan{
		Executable: "powershell.exe",
		Arguments:  []string{"-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShellScript(strings.Join(lines, newline) + newline)},
	}
}

func encodePowerShellScript(script string) string {
	units := utf16.Encode([]rune(script))
	encoded := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(encoded[i*2:], unit)
	}
	return base64.StdEncoding.EncodeToString(encoded)
}

func windowsArgument(value string) string {
	var result strings.Builder
	result.WriteByte('"')
	backslashes := 0
	for _, character := range value {
		if character == '\\' {
			backslashes++
			continue
		}
		count := backslashes
		if character == '"' {
			count = backslashes*2 + 1
		}
		result.WriteString(strings.Repeat(`\`, count))
		result.WriteRune(character)
		backslashes = 0
	}
	result.WriteString(strings.Repeat(`\`, backslashes*2))
	result.WriteByte('"')
	return result.String()
}

func powerShellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
