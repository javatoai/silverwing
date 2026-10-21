package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

var mcpTaskWriteMu sync.Mutex

type configLayer struct {
	File           string         `json:"file,omitempty"`
	Type           string         `json:"type"`
	Version        string         `json:"version,omitempty"`
	Config         map[string]any `json:"config"`
	DisabledReason string         `json:"disabledReason,omitempty"`
	Fingerprint    string         `json:"fingerprint,omitempty"`
}
type mcpServer struct {
	Name     string         `json:"name"`
	Scope    string         `json:"scope"`
	File     string         `json:"file,omitempty"`
	Version  string         `json:"version,omitempty"`
	Raw      map[string]any `json:"raw"`
	Active   bool           `json:"active"`
	PluginID string         `json:"pluginId,omitempty"`
	Note     string         `json:"note,omitempty"`
}

func (s *Service) executeMCP(ctx context.Context, action string, payload map[string]any) (any, error) {
	cfg, err := s.loadConfig()
	if err != nil {
		return nil, err
	}
	cwd := payloadString(payload, "cwd")
	if cwd == "" {
		cwd = payloadString(payload, "taskPath")
	}
	if cwd != "" {
		abs, err := filepath.Abs(cwd)
		if err != nil {
			return nil, err
		}
		cwd = abs
	}
	snapshot, err := s.readCodexConfig(ctx, cfg, cwd)
	if err != nil {
		return nil, err
	}
	switch action {
	case "status":
		return map[string]any{"cwd": cwd, "userFile": snapshot["userFile"], "layers": snapshot["layers"], "servers": snapshot["servers"], "warnings": snapshot["warnings"]}, nil
	case "check":
		connections, err := s.listMCPConnections(ctx, cfg, cwd)
		if err != nil {
			return nil, err
		}
		return map[string]any{"cwd": cwd, "connections": connections}, nil
	case "add", "edit", "set", "remove":
		name := payloadString(payload, "name")
		if !mcpNamePattern.MatchString(name) {
			return nil, fmt.Errorf("MCP 名称只能包含英文、数字、点、下划线和短横线")
		}
		if action == "remove" {
			if err := requireConfirmed(payload, "从 Codex 配置移除 MCP"); err != nil {
				return nil, err
			}
		}
		target := payloadString(payload, "file")
		if target == "" {
			target = payloadString(payload, "target")
		}
		if target == "" {
			target, _ = snapshot["userFile"].(string)
		}
		layer, err := s.requireMCPConfigTarget(snapshot, cwd, target)
		if err != nil {
			return nil, err
		}
		var edits []map[string]any
		if action == "set" {
			enabled, valid := payload["enabled"].(bool)
			if !valid {
				return nil, fmt.Errorf("enabled 必须明确为布尔值")
			}
			pluginID := payloadString(payload, "pluginId")
			if pluginID != "" {
				userFile, _ := snapshot["userFile"].(string)
				layer, err = s.requireMCPConfigTarget(snapshot, cwd, userFile)
				if err != nil {
					return nil, err
				}
				edits = []map[string]any{mcpEdit("plugins."+quotedKey(pluginID)+".mcp_servers."+quotedKey(name)+".enabled", enabled)}
			} else {
				servers, _ := snapshot["servers"].([]mcpServer)
				var selected *mcpServer
				for i := range servers {
					if servers[i].Name == name && (payloadString(payload, "file") == "" || samePath(servers[i].File, target)) {
						selected = &servers[i]
						break
					}
				}
				if selected == nil {
					return nil, fmt.Errorf("找不到 MCP：%s", name)
				}
				if selected.PluginID != "" {
					return nil, fmt.Errorf("插件提供的 MCP 需要使用 pluginId 更新开关")
				}
				if selected.Scope != "GLOBAL" && selected.Scope != "TASK" {
					return nil, fmt.Errorf("受管理的 MCP 配置不能直接更新开关")
				}
				raw := cloneMap(selected.Raw)
				raw["enabled"] = enabled
				edits = []map[string]any{mcpEdit("mcp_servers."+quotedKey(name), raw)}
			}
		} else if action == "edit" {
			oldName := payloadString(payload, "oldName")
			if oldName == "" {
				return nil, fmt.Errorf("编辑 MCP 需要 oldName")
			}
			servers, _ := snapshot["servers"].([]mcpServer)
			var selected *mcpServer
			for i := range servers {
				if servers[i].Name == oldName && samePath(servers[i].File, target) {
					selected = &servers[i]
					break
				}
			}
			if selected == nil {
				return nil, fmt.Errorf("找不到 MCP：%s", oldName)
			}
			if selected.PluginID != "" || (selected.Scope != "GLOBAL" && selected.Scope != "TASK") {
				return nil, fmt.Errorf("插件或受管理的 MCP 配置不能直接编辑")
			}
			for _, existing := range servers {
				if existing.Name == name && existing.Name != oldName && samePath(existing.File, target) {
					return nil, fmt.Errorf("该配置中已存在同名 MCP：%s", name)
				}
			}
			raw, err := decodeMCPMap(payload, "config")
			if err != nil {
				raw, err = decodeMCPMap(payload, "raw")
			}
			if err != nil {
				return nil, err
			}
			if err := validateMCPConfig(raw); err != nil {
				return nil, err
			}
			if oldName != name {
				edits = append(edits, mcpEdit("mcp_servers."+quotedKey(oldName), nil))
			}
			edits = append(edits, mcpEdit("mcp_servers."+quotedKey(name), raw))
		} else if action == "remove" {
			servers, _ := snapshot["servers"].([]mcpServer)
			selected := false
			for _, server := range servers {
				if server.Name != name || !samePath(server.File, target) {
					continue
				}
				if server.PluginID != "" || (server.Scope != "GLOBAL" && server.Scope != "TASK") {
					return nil, fmt.Errorf("插件或受管理的 MCP 配置不能直接移除")
				}
				selected = true
				break
			}
			if !selected {
				return nil, fmt.Errorf("找不到 MCP：%s", name)
			}
			edits = []map[string]any{mcpEdit("mcp_servers."+quotedKey(name), nil)}
		} else {
			raw, err := decodeMCPMap(payload, "config")
			if err != nil {
				raw, err = decodeMCPMap(payload, "raw")
			}
			if err != nil {
				return nil, err
			}
			if err := validateMCPConfig(raw); err != nil {
				return nil, err
			}
			if err := s.ensureMCPNameAvailable(snapshot, target, name); err != nil {
				return nil, err
			}
			edits = []map[string]any{mcpEdit("mcp_servers."+quotedKey(name), raw)}
		}
		if err := s.writeMCPConfig(ctx, cfg, cwd, layer, edits); err != nil {
			return nil, err
		}
		updated, err := s.readCodexConfig(ctx, cfg, cwd)
		if err != nil {
			if process.IsCleanupFailure(err) {
				return map[string]any{"saved": true}, fmt.Errorf("MCP 配置已保存，但读取最新状态失败：%w", err)
			}
			return map[string]any{"saved": true, "refreshError": redactProxy(cfg, err.Error())}, nil
		}
		return map[string]any{"saved": true, "snapshot": updated}, nil
	default:
		return nil, fmt.Errorf("未知 MCP 操作：%s", action)
	}
}

func (s *Service) readCodexConfig(ctx context.Context, cfg domain.AppConfig, cwd string) (map[string]any, error) {
	params := map[string]any{"includeLayers": true}
	if cwd != "" {
		params["cwd"] = cwd
	}
	response, err := s.rpcRequest(ctx, cfg, cwd, nil, "config/read", params, 30*time.Second)
	if err != nil {
		return nil, err
	}
	effective, _ := nestedMap(response, "config", "mcp_servers")
	origins, _ := response["origins"].(map[string]any)
	layersRaw, _ := response["layers"].([]any)
	layers := make([]configLayer, 0, len(layersRaw))
	servers := []mcpServer{}
	userFile := ""
	for _, raw := range layersRaw {
		layer, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := layer["name"].(map[string]any)
		file := firstString(name, "file")
		if file == "" {
			if folder := firstString(name, "dotCodexFolder"); folder != "" {
				file = filepath.Join(folder, "config.toml")
			}
		}
		typ, _ := name["type"].(string)
		if typ == "user" && file != "" {
			userFile = file
		}
		config, _ := layer["config"].(map[string]any)
		if config == nil {
			config = map[string]any{}
		}
		version := firstString(layer, "version")
		disabled := firstString(layer, "disabledReason")
		fingerprint := ""
		if file != "" {
			if data, readErr := readConfigFile(file); readErr == nil {
				sum := sha256.Sum256(data)
				fingerprint = "sha256:" + hex.EncodeToString(sum[:])
			}
		}
		layers = append(layers, configLayer{File: file, Type: typ, Version: version, Config: config, DisabledReason: disabled, Fingerprint: fingerprint})
		mcp, _ := config["mcp_servers"].(map[string]any)
		for name, value := range mcp {
			rawConfig, ok := value.(map[string]any)
			if !ok {
				continue
			}
			scope := "MANAGED"
			if typ == "user" {
				scope = "GLOBAL"
			} else if typ == "project" {
				scope = "TASK"
			}
			active := disabled == "" && effective[name] != nil
			origin, _ := origins["mcp_servers."+name].(map[string]any)
			if origin == nil {
				for key, value := range origins {
					if strings.HasPrefix(key, "mcp_servers."+name+".") {
						origin, _ = value.(map[string]any)
						if origin != nil {
							break
						}
					}
				}
			}
			originName, _ := origin["name"].(map[string]any)
			originFile := firstString(originName, "file")
			if originFile == "" {
				if folder := firstString(originName, "dotCodexFolder"); folder != "" {
					originFile = filepath.Join(folder, "config.toml")
				}
			}
			if originFile != "" && file != "" && !samePath(originFile, file) {
				active = false
			}
			note := ""
			if disabled != "" {
				note = "目录未受信任，该层配置未生效"
			} else if !active {
				note = "被更高优先级配置覆盖"
			}
			servers = append(servers, mcpServer{Name: name, Scope: scope, File: file, Version: version, Raw: rawConfig, Active: active, Note: note})
		}
	}
	if userFile == "" {
		home := strings.TrimSpace(os.Getenv("CODEX_HOME"))
		if home == "" {
			home, _ = os.UserHomeDir()
			home = filepath.Join(home, ".codex")
		}
		userFile = filepath.Join(home, "config.toml")
	}
	for name, value := range effective {
		found := false
		for _, server := range servers {
			if server.Name == name {
				found = true
				break
			}
		}
		if found {
			continue
		}
		raw, ok := value.(map[string]any)
		if !ok {
			raw = map[string]any{}
		}
		servers = append(servers, mcpServer{Name: name, Scope: "MANAGED", Raw: raw, Active: true})
	}
	warnings := []string{}
	if userFile != "" {
		pluginServers, pluginWarnings := discoverPluginMCPs(userFile, response)
		servers = append(servers, pluginServers...)
		warnings = append(warnings, pluginWarnings...)
	}
	// Layer order is supplied by Codex. Stable ordering keeps the native settings view predictable.
	sortMCPServers(servers)
	return map[string]any{"cwd": cwd, "userFile": userFile, "layers": layers, "servers": servers,
		"warnings": warnings, "rawConfig": response["config"]}, nil
}

func (s *Service) listMCPConnections(ctx context.Context, cfg domain.AppConfig, cwd string) (map[string]map[string]any, error) {
	all := map[string]map[string]any{}
	cursor := ""
	for page := 0; page < 100; page++ {
		params := map[string]any{"limit": 100}
		if cursor != "" {
			params["cursor"] = cursor
		}
		response, err := s.rpcRequest(ctx, cfg, cwd, nil, "mcpServerStatus/list", params, 60*time.Second)
		if err != nil {
			return nil, err
		}
		data, _ := response["data"].([]any)
		for _, raw := range data {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			tools, _ := entry["tools"].(map[string]any)
			ready := false
			if len(tools) > 0 {
				ready = true
			}
			if _, ok := entry["serverInfo"].(map[string]any); ok {
				ready = true
			}
			label := "未就绪，请检查启动命令或认证"
			if ready {
				label = fmt.Sprintf("连接成功 · %d 个工具", len(tools))
			} else if entry["authStatus"] == "notLoggedIn" {
				label = "需要认证"
			}
			name := firstString(entry, "name")
			if name != "" {
				all[name] = map[string]any{"success": ready, "tools": len(tools), "label": label}
			}
		}
		cursor, _ = response["nextCursor"].(string)
		if cursor == "" {
			return all, nil
		}
	}
	return nil, fmt.Errorf("Codex MCP 状态分页超过 100 页")
}

func (s *Service) requireMCPConfigTarget(snapshot map[string]any, cwd, target string) (configLayer, error) {
	if target == "" {
		return configLayer{}, fmt.Errorf("MCP 配置文件路径不能为空")
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return configLayer{}, err
	}
	layers, _ := snapshot["layers"].([]configLayer)
	for _, layer := range layers {
		if !samePath(layer.File, targetAbs) {
			continue
		}
		if layer.Type == "user" || layer.Type == "project" {
			return layer, nil
		}
		return configLayer{}, fmt.Errorf("不能改写此 Codex 配置层")
	}
	userFile, _ := snapshot["userFile"].(string)
	if samePath(userFile, targetAbs) {
		return configLayer{File: targetAbs, Type: "user"}, nil
	}
	if cwd != "" {
		taskFile := filepath.Join(cwd, ".codex", "config.toml")
		if samePath(taskFile, targetAbs) {
			return configLayer{File: targetAbs, Type: "project"}, nil
		}
	}
	return configLayer{}, fmt.Errorf("不能改写此 Codex 配置文件")
}

func (s *Service) ensureMCPNameAvailable(snapshot map[string]any, target, name string) error {
	servers, _ := snapshot["servers"].([]mcpServer)
	for _, server := range servers {
		if server.Name == name && samePath(server.File, target) {
			return fmt.Errorf("该配置中已存在同名 MCP：%s", name)
		}
	}
	return nil
}

func (s *Service) writeMCPConfig(ctx context.Context, cfg domain.AppConfig, cwd string, layer configLayer, edits []map[string]any) error {
	if layer.Type == "user" {
		params := map[string]any{"filePath": layer.File, "edits": edits, "reloadUserConfig": false}
		if layer.Version != "" {
			params["expectedVersion"] = layer.Version
		}
		_, err := s.rpcRequest(ctx, cfg, cwd, nil, "config/batchWrite", params, 30*time.Second)
		return err
	}
	return s.writeProjectMCPConfig(ctx, cfg, layer, edits)
}

func (s *Service) writeProjectMCPConfig(ctx context.Context, cfg domain.AppConfig, layer configLayer, edits []map[string]any) error {
	mcpTaskWriteMu.Lock()
	defer mcpTaskWriteMu.Unlock()
	if strings.TrimSpace(layer.File) == "" {
		return fmt.Errorf("任务配置路径不能为空")
	}
	target, err := filepath.Abs(layer.File)
	if err != nil {
		return err
	}
	if err := ensureNoExistingSymlinks(target); err != nil {
		return fmt.Errorf("配置文件不能是符号链接")
	}
	existsBefore := exists(target)
	var before []byte
	if existsBefore {
		before, err = readBounded(target, 4_000_000)
		if err != nil {
			return err
		}
	}
	if existsBefore && layer.Fingerprint != "sha256:"+sha256Hex(before) {
		return fmt.Errorf("任务配置已改变或尚未读取，请刷新后重试")
	}
	if !existsBefore && layer.Fingerprint != "" {
		return fmt.Errorf("任务配置已改变或尚未读取，请刷新后重试")
	}
	if err := ensureNoExistingSymlinks(s.paths.Temp); err != nil {
		return err
	}
	if err := os.MkdirAll(s.paths.Temp, 0o700); err != nil {
		return err
	}
	if err := ensureNoExistingSymlinks(s.paths.Temp); err != nil {
		return err
	}
	isolated, err := os.MkdirTemp(s.paths.Temp, "codex-edit-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(isolated)
	stagedConfig := filepath.Join(isolated, "config.toml")
	if err := os.WriteFile(stagedConfig, before, 0o600); err != nil {
		return err
	}
	read, err := s.rpcRequest(ctx, cfg, "", stringPtr(isolated), "config/read", map[string]any{"includeLayers": true}, 30*time.Second)
	if err != nil {
		return err
	}
	layers, _ := read["layers"].([]any)
	version := ""
	for _, raw := range layers {
		entry, _ := raw.(map[string]any)
		name, _ := entry["name"].(map[string]any)
		if firstString(name, "type") == "user" {
			version = firstString(entry, "version")
			break
		}
	}
	if existsBefore && layer.Version != version {
		return fmt.Errorf("任务配置已被其他程序修改，请刷新后重试")
	}
	params := map[string]any{"filePath": stagedConfig, "edits": edits, "reloadUserConfig": false}
	if version != "" {
		params["expectedVersion"] = version
	}
	if _, err := s.rpcRequest(ctx, cfg, "", stringPtr(isolated), "config/batchWrite", params, 30*time.Second); err != nil {
		return err
	}
	if exists(target) != existsBefore {
		return fmt.Errorf("任务配置已被其他程序修改，请刷新后重试")
	}
	if existsBefore {
		current, err := os.ReadFile(target)
		if err != nil {
			return err
		}
		if !equalBytes(current, before) {
			return fmt.Errorf("任务配置已被其他程序修改，请刷新后重试")
		}
	}
	if err := ensureNoExistingSymlinks(target); err != nil {
		return fmt.Errorf("配置文件不能是符号链接")
	}
	changed, err := os.ReadFile(stagedConfig)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return atomicWrite(target, changed, 0o600)
}

func discoverPluginMCPs(userFile string, response map[string]any) ([]mcpServer, []string) {
	config, _ := nestedMap(response, "config", "plugins")
	if len(config) == 0 {
		return nil, nil
	}
	home := filepath.Dir(userFile)
	cache := filepath.Join(home, "plugins", "cache")
	manifests := []string{}
	_ = filepath.WalkDir(cache, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() && path != cache {
			relative, relErr := filepath.Rel(cache, path)
			if relErr == nil && strings.Count(filepath.ToSlash(relative), "/") > 6 {
				return filepath.SkipDir
			}
		}
		if !entry.IsDir() && entry.Name() == "plugin.json" && filepath.Base(filepath.Dir(path)) == ".codex-plugin" && isRegular(path) && len(manifests) < 500 {
			manifests = append(manifests, path)
		}
		return nil
	})
	servers := []mcpServer{}
	warnings := []string{}
	for id, rawSettings := range config {
		settings, ok := rawSettings.(map[string]any)
		if !ok {
			continue
		}
		name, source := id, ""
		if index := strings.IndexByte(id, '@'); index >= 0 {
			name, source = id[:index], id[index+1:]
		}
		var selected string
		var selectedManifest map[string]any
		var selectedMTime time.Time
		for _, path := range manifests {
			rel, err := filepath.Rel(cache, path)
			if err != nil {
				continue
			}
			if source != "" && !strings.EqualFold(strings.Split(rel, string(os.PathSeparator))[0], source) {
				continue
			}
			data, err := readBounded(path, maxPluginBytes)
			if err != nil {
				continue
			}
			var manifest map[string]any
			if json.Unmarshal(data, &manifest) != nil || firstString(manifest, "name") != name {
				continue
			}
			info, _ := os.Stat(path)
			mtime := time.Time{}
			if info != nil {
				mtime = info.ModTime()
			}
			if selected == "" || mtime.After(selectedMTime) {
				selected, selectedManifest, selectedMTime = path, manifest, mtime
			}
		}
		if selected == "" {
			continue
		}
		packageRoot := filepath.Dir(filepath.Dir(selected))
		mcpDeclaration := selectedManifest["mcpServers"]
		var entries map[string]any
		if declared, ok := mcpDeclaration.(map[string]any); ok {
			entries = declared
		} else {
			fileName := ".mcp.json"
			if declared, ok := mcpDeclaration.(string); ok && declared != "" {
				fileName = declared
			}
			file := filepath.Clean(filepath.Join(packageRoot, fileName))
			if within(packageRoot, file) && isRegular(file) {
				data, readErr := readBounded(file, maxPluginBytes)
				if readErr != nil {
					warnings = append(warnings, "插件 "+name+" 的 MCP 清单读取失败")
				} else if decodeErr := json.Unmarshal(data, &entries); decodeErr != nil {
					warnings = append(warnings, "插件 "+name+" 的 MCP 清单读取失败")
				}
			}
		}
		if nested, ok := entries["mcpServers"].(map[string]any); ok {
			entries = nested
		}
		for serverName, serverRaw := range entries {
			raw, ok := serverRaw.(map[string]any)
			if !ok {
				continue
			}
			override, _ := nestedMap(settings, "mcp_servers", serverName)
			combined := cloneMap(raw)
			for key, value := range override {
				combined[key] = value
			}
			enabled, _ := settings["enabled"].(bool)
			note := "启动配置由插件提供"
			if !enabled {
				note = "插件已停用"
			} else if serverEnabled, ok := combined["enabled"].(bool); ok && !serverEnabled {
				enabled = false
				note = "MCP 已停用"
			}
			servers = append(servers, mcpServer{Name: serverName, Scope: "PLUGIN", File: userFile, Raw: combined, Active: enabled, PluginID: id, Note: note})
		}
	}
	return servers, warnings
}

func nestedMap(object map[string]any, keys ...string) (map[string]any, bool) {
	current := object
	for index, key := range keys {
		value, ok := current[key].(map[string]any)
		if !ok {
			return nil, false
		}
		if index == len(keys)-1 {
			return value, true
		}
		current = value
	}
	return current, true
}
func cloneMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
func mcpEdit(key string, value any) map[string]any {
	return map[string]any{"keyPath": key, "value": value, "mergeStrategy": "replace"}
}
func quotedKey(value string) string { encoded, _ := json.Marshal(value); return string(encoded) }
func validateMCPConfig(config map[string]any) error {
	urlValue, hasURL := config["url"].(string)
	command, hasCommand := config["command"].(string)
	if hasURL {
		parsed, err := url.Parse(urlValue)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
			return fmt.Errorf("请输入有效的 HTTP/HTTPS 地址；认证请使用环境变量或认证入口")
		}
	} else if !hasCommand || strings.TrimSpace(command) == "" {
		return fmt.Errorf("请填写启动命令")
	}
	if hasURL && hasCommand {
		return fmt.Errorf("启动命令和 HTTP 地址只能选择一种")
	}
	for _, key := range []string{"startup_timeout_sec", "tool_timeout_sec"} {
		if value, exists := config[key]; exists {
			number, ok := value.(float64)
			if !ok || number <= 0 || number > 3600 {
				return fmt.Errorf("超时必须为 0 到 3600 之间的正数")
			}
		}
	}
	if value, exists := config["args"]; exists {
		args, ok := value.([]any)
		if !ok {
			return fmt.Errorf("命令参数必须是字符串数组")
		}
		for _, arg := range args {
			if _, ok := arg.(string); !ok {
				return fmt.Errorf("命令参数必须是字符串数组")
			}
		}
	}
	for _, key := range []string{"env", "http_headers"} {
		if value, exists := config[key]; exists {
			fields, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("环境变量和请求头必须是文本键值")
			}
			for _, field := range fields {
				if _, ok := field.(string); !ok {
					return fmt.Errorf("环境变量和请求头必须是文本键值")
				}
			}
		}
	}
	return nil
}

func decodeMCPMap(payload map[string]any, field string) (map[string]any, error) {
	value, ok := payload[field]
	if !ok {
		return nil, fmt.Errorf("缺少 %s", field)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("%s 必须是对象", field)
	}
	return result, nil
}
func readConfigFile(path string) ([]byte, error) {
	if !isRegular(path) {
		return nil, fmt.Errorf("Codex 配置不是普通文件")
	}
	return readBounded(path, 4_000_000)
}
func sha256Hex(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func sortMCPServers(values []mcpServer) {
	scopeRank := map[string]int{"GLOBAL": 0, "TASK": 1, "PLUGIN": 2, "MANAGED": 3}
	sort.SliceStable(values, func(i, j int) bool {
		if scopeRank[values[i].Scope] == scopeRank[values[j].Scope] {
			return values[i].Name < values[j].Name
		}
		return scopeRank[values[i].Scope] < scopeRank[values[j].Scope]
	})
}
func categorizeCodexError(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "version") || strings.Contains(lower, "modified") || strings.Contains(lower, "conflict"):
		return "配置已被其他程序修改，请刷新后重试"
	case strings.Contains(lower, "trust"):
		return "该任务目录尚未受 Codex 信任，请先在 Codex 中打开并确认目录"
	case strings.Contains(lower, "method"):
		return "当前 Codex 不支持此接口，请更新 Codex 后重试"
	default:
		return "Codex 未完成此操作，请检查配置后重试"
	}
}

var mcpNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
