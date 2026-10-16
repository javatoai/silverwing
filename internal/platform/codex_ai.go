package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
)

type codexAISkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
	Scope       string `json:"scope"`
	Enabled     bool   `json:"enabled"`
}

type codexInstructionFile struct {
	Title  string `json:"title"`
	File   string `json:"file"`
	Source string `json:"source"`
	Exists bool   `json:"exists"`
}

type codexThreadChoice struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	CWD   string `json:"cwd"`
}

var codexThreadIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// NewServiceWithCodexURIOpener creates a platform service with a per-instance
// Codex URI opener. Passing nil preserves the normal OS URI handler.
func NewServiceWithCodexURIOpener(paths storage.Paths, config *storage.ConfigStore, runner *process.Runner, opener func(context.Context, string) error) *Service {
	service := NewService(paths, config, runner)
	service.codexURIOpener = opener
	return service
}

func (s *Service) executeCodexAI(ctx context.Context, kind string, payload map[string]any) (any, error) {
	cfg, err := s.loadConfig()
	if err != nil {
		return nil, err
	}
	switch kind {
	case "codex.ai/snapshot":
		cwd := payloadString(payload, "cwd")
		if cwd == "" {
			cwd = payloadString(payload, "taskPath")
		}
		if cwd != "" {
			cwd, err = filepath.Abs(cwd)
			if err != nil {
				return nil, err
			}
		}
		snapshot, err := s.readCodexConfig(ctx, cfg, cwd)
		if err != nil {
			return nil, err
		}
		files := []codexInstructionFile{}
		if cwd != "" {
			files = instructionFiles(cwd, filepath.Dir(snapshot["userFile"].(string)), asMap(snapshot["rawConfig"]))
		}
		warnings, _ := snapshot["warnings"].([]string)
		skills := []codexAISkill{}
		if payloadBool(payload, "includeSkills") && cwd != "" {
			result, listErr := s.rpcRequest(ctx, cfg, cwd, nil, "skills/list", map[string]any{
				"cwds": []string{cwd}, "forceReload": true,
			}, 30*time.Second)
			if listErr != nil {
				if process.IsCleanupFailure(listErr) {
					return nil, listErr
				}
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				warnings = append(warnings, "Skill 列表暂时无法读取，可刷新重试")
			} else {
				var listWarnings []string
				skills, listWarnings = parseCodexSkills(result)
				warnings = append(warnings, listWarnings...)
			}
		}
		return map[string]any{
			"cwd": nullableString(cwd), "userFile": snapshot["userFile"], "layers": snapshot["layers"],
			"servers": snapshot["servers"], "skills": skills, "files": files, "warnings": warnings,
		}, nil
	case "codex.ai/threads/list":
		cwd := payloadString(payload, "cwd")
		if cwd == "" {
			cwd = payloadString(payload, "taskPath")
		}
		if cwd == "" {
			return nil, fmt.Errorf("读取 Codex 会话需要任务目录")
		}
		cwd, err = filepath.Abs(cwd)
		if err != nil {
			return nil, err
		}
		threads, err := s.codexThreads(ctx, cfg, cwd)
		if err != nil {
			return nil, err
		}
		bound, err := s.codexBoundThread(cwd)
		if err != nil {
			return nil, err
		}
		return map[string]any{"threads": threads, "boundThreadId": bound}, nil
	case "codex.ai/threads/bind":
		cwd := payloadString(payload, "cwd")
		if cwd == "" {
			cwd = payloadString(payload, "taskPath")
		}
		if cwd == "" {
			return nil, fmt.Errorf("关联 Codex 会话需要任务目录")
		}
		cwd, err = filepath.Abs(cwd)
		if err != nil {
			return nil, err
		}
		rawThreadID, exists := payload["threadId"]
		if !exists {
			return nil, fmt.Errorf("threadId 必须明确提供 UUID 或 null")
		}
		var threadID *string
		if rawThreadID != nil {
			value, ok := rawThreadID.(string)
			if !ok {
				return nil, fmt.Errorf("threadId 必须是 UUID 或 null")
			}
			value = strings.TrimSpace(value)
			if value != "" {
				if !codexThreadIDPattern.MatchString(value) {
					return nil, fmt.Errorf("只能关联有效的 Codex 会话 ID")
				}
				threads, err := s.codexThreads(ctx, cfg, cwd)
				if err != nil {
					return nil, err
				}
				found := false
				for _, thread := range threads {
					if thread.ID == value && samePath(thread.CWD, cwd) {
						found = true
						break
					}
				}
				if !found {
					return nil, fmt.Errorf("只能关联当前任务目录的会话")
				}
				threadID = stringPtr(value)
			}
		}
		if err := s.setCodexBoundThread(cwd, threadID); err != nil {
			return nil, err
		}
		return map[string]any{"cwd": cwd, "threadId": threadID}, nil
	case "codex.ai/file/read":
		path := payloadString(payload, "file")
		if path == "" {
			path = payloadString(payload, "path")
		}
		return s.readCodexFile(path)
	case "codex.ai/open":
		return s.openCodexWorkspace(ctx, payload)
	case "codex.ai/mcp/authenticate":
		name := payloadString(payload, "name")
		if name == "" {
			return nil, fmt.Errorf("MCP 名称不能为空")
		}
		login, ok := payload["login"].(bool)
		if !ok {
			return nil, fmt.Errorf("login 必须明确为布尔值")
		}
		verb := "logout"
		if login {
			verb = "login"
		}
		cwd := payloadString(payload, "cwd")
		if cwd == "" {
			cwd = payloadString(payload, "taskPath")
		}
		if cwd != "" {
			cwd, err = filepath.Abs(cwd)
			if err != nil {
				return nil, err
			}
		}
		_, resolution, target, err := s.resolveCLIExecutableWithSource(ctx, cfg, "codex")
		if err != nil {
			return nil, err
		}
		env := cliEnvironmentWithRunner(ctx, s.runner, "codex", resolution.Command)
		if _, err := s.runToolWithEnv(ctx, cfg, target, resolution.Command, []string{"mcp", verb, name}, cwd, 3*time.Minute, env); err != nil {
			return nil, err
		}
		return map[string]any{"server": name, "authenticated": login}, nil
	default:
		return nil, fmt.Errorf("未知 Codex AI 操作：%s", kind)
	}
}

func (s *Service) openCodexWorkspace(ctx context.Context, payload map[string]any) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	workspacePath := payloadString(payload, "workspacePath")
	if workspacePath == "" {
		return nil, fmt.Errorf("打开 Codex 需要 workspacePath")
	}
	absolute, err := filepath.Abs(workspacePath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("Codex 工作区目录不存在：%s", absolute)
	}
	forceNew := false
	if raw, exists := payload["forceNew"]; exists {
		var ok bool
		forceNew, ok = raw.(bool)
		if !ok {
			return nil, fmt.Errorf("forceNew 必须是布尔值")
		}
	}
	requireUnbound := false
	if raw, exists := payload["requireUnbound"]; exists {
		var ok bool
		requireUnbound, ok = raw.(bool)
		if !ok {
			return nil, fmt.Errorf("requireUnbound 必须是布尔值")
		}
	}
	if forceNew && requireUnbound {
		return nil, fmt.Errorf("forceNew 不能要求工作区未绑定")
	}
	var bound *string
	if !forceNew {
		bound, err = s.codexBoundThread(absolute)
		if err != nil {
			return nil, err
		}
		if requireUnbound && bound != nil {
			return nil, fmt.Errorf("Codex 工作区绑定已变化，请重试打开操作")
		}
	}
	threadID := ""
	if !forceNew {
		threadID = strings.TrimSpace(payloadString(payload, "threadId"))
		if threadID == "" && bound != nil {
			threadID = *bound
		}
	}
	if threadID != "" {
		if !codexThreadIDPattern.MatchString(threadID) {
			return nil, fmt.Errorf("只能打开有效的 Codex 会话 ID")
		}
		if bound == nil || !strings.EqualFold(*bound, threadID) {
			return nil, fmt.Errorf("只能打开当前工作区已关联的 Codex 会话")
		}
		threadID = strings.ToLower(threadID)
	}
	deepLink, err := BuildCodexOpenURI(absolute, threadID, forceNew)
	if err != nil {
		return nil, err
	}
	if s.codexURIOpener != nil {
		if err := s.codexURIOpener(ctx, deepLink); err != nil {
			return nil, fmt.Errorf("打开 Codex 深链失败：%w", err)
		}
		return map[string]any{"opened": true, "url": deepLink, "workspacePath": absolute, "threadId": nullableString(threadID), "forceNew": forceNew}, nil
	}
	if err := openCodexURI(deepLink); err != nil {
		return nil, fmt.Errorf("打开 Codex 深链失败：%w", err)
	}
	return map[string]any{"opened": true, "url": deepLink, "workspacePath": absolute, "threadId": nullableString(threadID), "forceNew": forceNew}, nil
}

func openCodexURI(deepLink string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = process.NewDetachedCommand("rundll32", "url.dll,FileProtocolHandler", deepLink)
	case "darwin":
		command = process.NewDetachedCommand("open", deepLink)
	default:
		command = process.NewDetachedCommand("xdg-open", deepLink)
	}
	command.Stdin = nil
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}

// BoundCodexThread returns the thread already associated with one existing
// workspace. Callers must still pass the returned ID to codex.ai/open, which
// revalidates it against the current binding before opening the URI.
func (s *Service) BoundCodexThread(workspacePath string) (*string, error) {
	if strings.TrimSpace(workspacePath) == "" {
		return nil, fmt.Errorf("读取 Codex 会话绑定需要工作区目录")
	}
	absolute, err := filepath.Abs(workspacePath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("Codex 工作区目录不存在：%s", absolute)
	}
	return s.codexBoundThread(absolute)
}

func (s *Service) codexThreads(ctx context.Context, cfg domain.AppConfig, cwd string) ([]codexThreadChoice, error) {
	response, err := s.rpcRequest(ctx, cfg, cwd, nil, "thread/list", map[string]any{
		"cwd": cwd, "limit": 100, "sortKey": "updated_at", "useStateDbOnly": true,
	}, 30*time.Second)
	if err != nil {
		return nil, err
	}
	data, _ := response["data"].([]any)
	threads := make([]codexThreadChoice, 0, len(data))
	for _, raw := range data {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := firstString(entry, "id")
		threadCWD := firstString(entry, "cwd")
		if id == "" || threadCWD == "" || !samePath(threadCWD, cwd) {
			continue
		}
		title := firstString(entry, "name")
		if title == "" {
			title = firstString(entry, "preview")
			if runes := []rune(title); len(runes) > 120 {
				title = string(runes[:120])
			}
		}
		if title == "" {
			title = id
		}
		threads = append(threads, codexThreadChoice{ID: id, Title: title, CWD: threadCWD})
	}
	return threads, nil
}

func (s *Service) codexBoundThread(cwd string) (*string, error) {
	bindings, err := s.readCodexBindings()
	if err != nil {
		return nil, err
	}
	value := bindings[filepath.Clean(cwd)]
	if value == "" {
		return nil, nil
	}
	return stringPtr(value), nil
}

func (s *Service) setCodexBoundThread(cwd string, threadID *string) error {
	release, err := storage.AcquireLock(filepath.Join(s.paths.Locks, "codex-task-bindings.lock"))
	if err != nil {
		return err
	}
	defer release()
	bindings, err := s.readCodexBindings()
	if err != nil {
		return err
	}
	key := filepath.Clean(cwd)
	if threadID == nil {
		delete(bindings, key)
	} else {
		bindings[key] = *threadID
	}
	data, err := json.MarshalIndent(bindings, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.paths.Codex, "task-bindings.json")
	return atomicWrite(path, append(data, '\n'), 0o600)
}

func (s *Service) readCodexBindings() (map[string]string, error) {
	path := filepath.Join(s.paths.Codex, "task-bindings.json")
	if err := ensureNoExistingSymlinks(path); err != nil {
		return nil, err
	}
	if !exists(path) {
		return map[string]string{}, nil
	}
	if !isRegular(path) {
		return nil, fmt.Errorf("Codex 关联文件不是普通文件")
	}
	data, err := readBounded(path, 1_000_000)
	if err != nil {
		return nil, err
	}
	bindings := map[string]string{}
	if err := json.Unmarshal(data, &bindings); err != nil {
		return nil, fmt.Errorf("Codex 关联文件格式不正确：%w", err)
	}
	return bindings, nil
}

func (s *Service) readCodexFile(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("文件路径不能为空")
	}
	if !isRegular(path) {
		return "", fmt.Errorf("文件不存在或是符号链接")
	}
	data, err := readBounded(path, 1_000_000)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("文件不是有效的 UTF-8 文本，请使用外部应用打开")
	}
	return string(data), nil
}

func parseCodexSkills(result map[string]any) ([]codexAISkill, []string) {
	data, _ := result["data"].([]any)
	skills := []codexAISkill{}
	warnings := []string{}
	for _, raw := range data {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if errors, ok := entry["errors"].([]any); ok && len(errors) > 0 {
			warnings = append(warnings, "部分 Skill 读取失败，请刷新或检查对应 SKILL.md")
		}
		values, _ := entry["skills"].([]any)
		for _, rawSkill := range values {
			skill, ok := rawSkill.(map[string]any)
			if !ok {
				continue
			}
			path := firstString(skill, "path")
			if path == "" {
				continue
			}
			enabled, ok := skill["enabled"].(bool)
			if !ok {
				enabled = true
			}
			skills = append(skills, codexAISkill{
				Name: firstString(skill, "name"), Description: firstString(skill, "description"),
				Path: path, Scope: firstString(skill, "scope"), Enabled: enabled,
			})
		}
	}
	return skills, uniqueStrings(warnings)
}

func instructionFiles(cwd, home string, config map[string]any) []codexInstructionFile {
	current := filepath.Clean(cwd)
	ancestors := []string{}
	for current != "" {
		ancestors = append(ancestors, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	markers := stringSlice(config["project_root_markers"])
	if len(markers) == 0 {
		markers = []string{".git", ".hg"}
	}
	rootIndex := len(ancestors) - 1
	for index, directory := range ancestors {
		found := false
		for _, marker := range markers {
			if exists(filepath.Join(directory, marker)) {
				found = true
				break
			}
		}
		if found {
			rootIndex = index
			break
		}
	}
	guideNames := []string{"AGENTS.override.md", "AGENTS.md"}
	guideNames = append(guideNames, stringSlice(config["project_doc_fallback_filenames"])...)
	result := []codexInstructionFile{}
	addGuide := func(directory, source string, required bool) {
		selected := ""
		for _, name := range guideNames {
			candidate := filepath.Join(directory, name)
			if isRegular(candidate) {
				selected = candidate
				break
			}
		}
		if selected == "" {
			selected = filepath.Join(directory, "AGENTS.md")
		}
		if required || isRegular(selected) {
			result = append(result, codexInstructionFile{Title: filepath.Base(selected), File: selected, Source: source, Exists: isRegular(selected)})
		}
	}
	addGuide(home, "Codex 全局", false)
	for index := rootIndex; index >= 0; index-- {
		directory := ancestors[index]
		source := "上级目录"
		if samePath(directory, cwd) {
			source = "当前任务"
		}
		addGuide(directory, source, samePath(directory, cwd))
	}
	if raw := firstString(config, "model_instructions_file"); raw != "" {
		path := raw
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		result = append(result, codexInstructionFile{Title: filepath.Base(path), File: path, Source: "Codex 模型说明", Exists: isRegular(path)})
	}
	seen := map[string]bool{}
	unique := result[:0]
	for _, item := range result {
		key := filepath.Clean(item.File)
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, item)
	}
	return unique
}

func stringSlice(value any) []string {
	values, _ := value.([]any)
	result := []string{}
	for _, raw := range values {
		if text, ok := raw.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func asMap(value any) map[string]any {
	result, _ := value.(map[string]any)
	if result == nil {
		return map[string]any{}
	}
	return result
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
