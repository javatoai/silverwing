package platform

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

type applicationEvent struct {
	Timestamp string            `json:"timestamp"`
	Level     string            `json:"level"`
	Event     string            `json:"event"`
	Message   string            `json:"message"`
	Metadata  map[string]string `json:"metadata"`
}

var eventLogName = regexp.MustCompile(`^application-\d{4}-\d{2}-\d{2}\.jsonl$`)

func (s *Service) gitVersion(ctx context.Context) (map[string]any, error) {
	cfg, err := s.loadConfig()
	if err != nil {
		return nil, err
	}
	command := s.gitExecutable(ctx, cfg)
	result, err := s.runTool(ctx, cfg, domain.ProxyTargetGit, command, []string{"--version"}, "", 10*time.Second)
	if err != nil {
		return map[string]any{"command": command, "success": false, "error": redactProxy(cfg, err.Error())}, nil
	}
	version := firstNonBlank(result.Stdout, result.Stderr)
	return map[string]any{"command": command, "success": true, "version": version}, nil
}

func (s *Service) proxyTest(ctx context.Context) (map[string]any, error) {
	cfg, err := s.loadConfig()
	if err != nil {
		return nil, err
	}
	if cfg.CommandProxyURL == nil {
		return map[string]any{"success": false, "message": "命令网络代理尚未配置"}, nil
	}
	normalized, err := domain.NormalizeCommandProxyURL(cfg.CommandProxyURL)
	if err != nil || normalized == nil {
		return nil, fmt.Errorf("命令代理地址格式不合法")
	}
	parsed, err := url.Parse(*normalized)
	if err != nil {
		return nil, fmt.Errorf("命令代理地址格式不合法")
	}
	address := net.JoinHostPort(parsed.Hostname(), parsed.Port())
	dialer := net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return map[string]any{"success": false, "endpoint": redactProxy(cfg, *normalized), "message": redactProxy(cfg, fmt.Sprintf("无法连接代理服务器：%v", err))}, nil
	}
	_ = conn.Close()
	return map[string]any{"success": true, "endpoint": redactProxy(cfg, *normalized), "message": "已连接到代理服务器"}, nil
}

func (s *Service) executeLogs(kind string, payload map[string]any) (any, error) {
	logs := filepath.Clean(s.paths.Logs)
	switch kind {
	case "logs.list":
		limit := 10
		if raw, ok := payload["limit"].(float64); ok && raw > 0 {
			limit = int(raw)
		}
		if limit > 100 {
			limit = 100
		}
		if err := ensureNoExistingSymlinks(logs); err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(logs)
		if os.IsNotExist(err) {
			return []applicationEvent{}, nil
		}
		if err != nil {
			return nil, err
		}
		files := []string{}
		for _, entry := range entries {
			if entry.Type().IsRegular() && eventLogName.MatchString(entry.Name()) {
				files = append(files, entry.Name())
			}
		}
		sort.Sort(sort.Reverse(sort.StringSlice(files)))
		result := []applicationEvent{}
		for _, name := range files {
			events, err := readRecentErrorEvents(filepath.Join(logs, name), limit-len(result))
			if err != nil {
				return nil, err
			}
			result = append(result, events...)
			if len(result) >= limit {
				break
			}
		}
		return result, nil
	case "logs.read":
		name := payloadString(payload, "name")
		if name == "" {
			name = payloadString(payload, "path")
		}
		if filepath.Base(name) != name || !eventLogName.MatchString(name) {
			return nil, fmt.Errorf("只能读取 SilverWing 的 application-YYYY-MM-DD.jsonl 日志文件")
		}
		file := filepath.Join(logs, name)
		if err := ensureNoExistingSymlinks(file); err != nil {
			return nil, fmt.Errorf("日志文件不能是符号链接")
		}
		data, err := readBounded(file, 8*1024*1024)
		if err != nil {
			return nil, err
		}
		return map[string]any{"name": name, "text": string(data)}, nil
	default:
		return nil, fmt.Errorf("未知日志操作")
	}
}

func readRecentErrorEvents(path string, limit int) ([]applicationEvent, error) {
	if limit <= 0 {
		return []applicationEvent{}, nil
	}
	if err := ensureNoExistingSymlinks(path); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	recent := make([]applicationEvent, 0, limit)
	for scanner.Scan() {
		var event applicationEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Level != "ERROR" {
			continue
		}
		if len(recent) == limit {
			copy(recent, recent[1:])
			recent = recent[:len(recent)-1]
		}
		recent = append(recent, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	for left, right := 0, len(recent)-1; left < right; left, right = left+1, right-1 {
		recent[left], recent[right] = recent[right], recent[left]
	}
	return recent, nil
}

func (s *Service) exportDiagnostics(ctx context.Context, payload map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cfg, err := s.loadConfig()
	if err != nil {
		return nil, err
	}
	target := payloadString(payload, "path")
	if target != "" {
		if !filepath.IsAbs(target) {
			return nil, fmt.Errorf("诊断导出路径必须是绝对路径")
		}
		target = filepath.Clean(target)
	} else {
		if err := ensureNoExistingSymlinks(s.paths.Diagnostics); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(s.paths.Diagnostics, 0o700); err != nil {
			return nil, err
		}
		target = filepath.Join(s.paths.Diagnostics, fmt.Sprintf("silverwing-diagnostics-%d.zip", time.Now().UnixMilli()))
	}
	if err := ensureNoExistingSymlinks(target); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	archive := zip.NewWriter(file)
	closeWithError := func(cause error) (map[string]any, error) {
		_ = archive.Close()
		_ = file.Close()
		_ = os.Remove(target)
		return nil, cause
	}
	system, err := s.diagnosticSystemSummary(ctx, cfg)
	if err != nil {
		return closeWithError(err)
	}
	if err := zipText(archive, "system.txt", system); err != nil {
		return closeWithError(err)
	}
	safeConfig, err := sanitizedConfig(cfg)
	if err != nil {
		return closeWithError(err)
	}
	configBytes, err := json.MarshalIndent(safeConfig, "", "  ")
	if err != nil {
		return closeWithError(err)
	}
	if err := zipText(archive, "config-summary.json", string(configBytes)); err != nil {
		return closeWithError(err)
	}
	repositories, err := s.diagnosticRepositorySummary(ctx, cfg)
	if err != nil {
		return closeWithError(err)
	}
	if err := zipText(archive, "repositories.txt", repositories); err != nil {
		return closeWithError(err)
	}
	if failures := payloadString(payload, "manifestFailures"); failures != "" {
		if err := zipText(archive, "task-scan-failures.txt", redactHome(failures)); err != nil {
			return closeWithError(err)
		}
	}
	if entries, readErr := os.ReadDir(s.paths.Logs); readErr == nil {
		files := []string{}
		for _, entry := range entries {
			if entry.Type().IsRegular() && eventLogName.MatchString(entry.Name()) {
				files = append(files, entry.Name())
			}
		}
		sort.Sort(sort.Reverse(sort.StringSlice(files)))
		for index, name := range files {
			if index >= 3 {
				break
			}
			if isSymlink(filepath.Join(s.paths.Logs, name)) {
				continue
			}
			if err := zipRegularFile(archive, "logs/"+name, filepath.Join(s.paths.Logs, name)); err != nil {
				return closeWithError(err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		_ = file.Close()
		_ = os.Remove(target)
		return nil, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(target)
		return nil, err
	}
	return map[string]any{"path": target, "size": fileSize(target)}, nil
}

func (s *Service) diagnosticSystemSummary(ctx context.Context, cfg domain.AppConfig) (string, error) {
	var out strings.Builder
	fmt.Fprintf(&out, "silverwing config schema: %s\n", domain.CurrentAppConfigSchemaVersion)
	fmt.Fprintf(&out, "OS: %s %s %s\n", runtime.GOOS, runtime.GOARCH, runtime.Version())
	if result, err := s.runTool(ctx, cfg, domain.ProxyTargetGit, s.gitExecutable(ctx, cfg), []string{"--version"}, "", 10*time.Second); err == nil {
		fmt.Fprintf(&out, "Git: %s\n", firstNonBlank(result.Stdout, result.Stderr))
	} else if process.IsCleanupFailure(err) || ctx.Err() != nil {
		return "", err
	} else {
		fmt.Fprintf(&out, "Git: unavailable: %s\n", sanitizeDiagnosticText(redactProxy(cfg, err.Error())))
	}
	_, resolution, _, resolveErr := s.resolveCLIExecutableWithSource(ctx, cfg, "meegle")
	if process.IsCleanupFailure(resolveErr) || ctx.Err() != nil {
		if resolveErr != nil {
			return "", resolveErr
		}
		return "", ctx.Err()
	}
	if resolveErr == nil && resolution.Command != "" {
		version, err := s.diagnosticCommand(ctx, cfg, domain.ProxyTargetMeegle, resolution.Command, []string{"--version"})
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&out, "Meegle: %s\n", version)
		auth, err := s.diagnosticCommand(ctx, cfg, domain.ProxyTargetMeegle, resolution.Command, []string{"auth", "status", "--format", "json"})
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&out, "Meegle auth: %s\n", auth)
	} else if resolveErr != nil {
		fmt.Fprintf(&out, "Meegle: unavailable: %s\n", sanitizeDiagnosticText(redactProxy(cfg, resolveErr.Error())))
	}
	return out.String(), nil
}

func (s *Service) diagnosticCommand(ctx context.Context, cfg domain.AppConfig, target domain.CommandProxyTarget, executable string, args []string) (string, error) {
	result, err := s.runTool(ctx, cfg, target, executable, args, "", 10*time.Second)
	if err != nil {
		if process.IsCleanupFailure(err) || ctx.Err() != nil {
			return "", err
		}
		return "unavailable: " + sanitizeDiagnosticText(redactProxy(cfg, err.Error())), nil
	}
	return sanitizeDiagnosticText(firstNonBlank(result.Stdout, result.Stderr)), nil
}

func (s *Service) diagnosticRepositorySummary(ctx context.Context, cfg domain.AppConfig) (string, error) {
	var out strings.Builder

	return out.String(), nil
}

func sanitizedConfig(cfg domain.AppConfig) (map[string]any, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if taskRoot, ok := result["taskRoot"].(string); ok {
		result["taskRoot"] = redactHome(taskRoot)
	}
	if repositories, ok := result["repositories"].([]any); ok {
		for _, item := range repositories {
			repository, ok := item.(map[string]any)
			if !ok {
				continue
			}
			for _, key := range []string{"rootPath", "gitCommonDirectory"} {
				if value, ok := repository[key].(string); ok {
					repository[key] = redactHome(value)
				}
			}
			if remote, ok := repository["originUrl"].(string); ok {
				repository["originUrl"] = redactURLCredentials(remote)
			}
		}
	}
	for _, key := range []string{"terminalExecutable", "meegleExecutablePath", "codexExecutablePath", "gitExecutablePath"} {
		if value, ok := result[key].(string); ok && value != "" {
			result[key] = "<configured>"
		}
	}
	if proxy, ok := result["commandProxyUrl"].(string); ok && proxy != "" {
		result["commandProxyUrl"] = "<configured>"
	}
	for _, key := range []string{"commandProxyNoProxy", "commandProxyUsername", "commandProxyPassword"} {
		result[key] = nil
	}
	return result, nil
}

func zipText(archive *zip.Writer, name, text string) error {
	writer, err := archive.Create(name)
	if err != nil {
		return err
	}
	_, err = writer.Write([]byte(text))
	return err
}
func zipRegularFile(archive *zip.Writer, name, path string) error {
	if err := ensureNoExistingSymlinks(path); err != nil {
		return err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("诊断日志不是普通文件：%s", path)
	}
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil {
		return err
	}
	after, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !after.Mode().IsRegular() || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, after) {
		return fmt.Errorf("诊断日志在读取期间发生改变：%s", path)
	}
	output, err := archive.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(output, input)
	return err
}
func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
func redactHome(value string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return value
	}
	pattern := regexp.MustCompile("(?i)" + regexp.QuoteMeta(home))
	return pattern.ReplaceAllString(value, "<USER_HOME>")
}
func redactURLCredentials(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.User == nil {
		return value
	}
	parsed.User = url.User("***")
	return parsed.String()
}

var diagnosticAuthorization = regexp.MustCompile(`(?i)(authorization["']?[ \t]*[:=][ \t]*["']?(?:bearer|basic)[ \t]+)[^\s"'&,}\]]+`)
var diagnosticToken = regexp.MustCompile(`(?i)((?:access[_-]?token|refresh[_-]?token|token)["']?[ \t]*[:=][ \t]*)(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s"'&,}\]]+)`)

func sanitizeDiagnosticText(value string) string {
	value = diagnosticAuthorization.ReplaceAllString(value, "${1}[已隐藏]")
	return diagnosticToken.ReplaceAllString(value, `${1}"[已隐藏]"`)
}
func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
