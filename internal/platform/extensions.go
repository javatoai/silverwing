package platform

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

const (
	extensionsStateName = "extensions-state.json"
	skillSourcesName    = "skill-sources"
	skillBackupsName    = "skill-backups"
	skillFileName       = "SKILL.md"
	maxSkillBytes       = 512 * 1024
	maxPluginBytes      = 512 * 1024
)

var skillNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var skillFrontMatter = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---(?:\r?\n|$)`)
var localSkillFrontMatter = regexp.MustCompile(`(?s)\A---\s*\r?\n(.*?)\r?\n---(?:\r?\n|\z)`)

type PluginCatalogItem struct {
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Version       *string  `json:"version,omitempty"`
	Installed     bool     `json:"installed"`
	SourcePath    *string  `json:"sourcePath,omitempty"`
	BundledSkills []string `json:"bundledSkills"`
}

type MarketplaceSnapshot struct {
	SourceID        string              `json:"sourceId"`
	MarketplaceName *string             `json:"marketplaceName,omitempty"`
	CacheDirectory  *string             `json:"cacheDirectory,omitempty"`
	Plugins         []PluginCatalogItem `json:"plugins"`
	UpdatedAt       *string             `json:"updatedAt,omitempty"`
	Error           *string             `json:"error,omitempty"`
}

type ExternalSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SourcePath  string `json:"sourcePath"`
}

type SkillSourceSnapshot struct {
	SourceID        string          `json:"sourceId"`
	Skills          []ExternalSkill `json:"skills"`
	DiscoveredRoots []string        `json:"discoveredRoots"`
	UpdatedAt       *string         `json:"updatedAt,omitempty"`
	Error           *string         `json:"error,omitempty"`
}

type extensionState struct {
	Schema         int                            `json:"schema"`
	Marketplaces   map[string]MarketplaceSnapshot `json:"marketplaces"`
	SkillSources   map[string]SkillSourceSnapshot `json:"skillSources"`
	ManagedPlugins []managedPlugin                `json:"managedPlugins"`
	ManagedSkills  []managedSkill                 `json:"managedSkills"`
}
type managedPlugin struct {
	SourceID  string `json:"sourceId"`
	Name      string `json:"pluginName"`
	ManagedAt string `json:"managedAt"`
}
type managedSkill struct {
	SourceID  string `json:"sourceId"`
	Name      string `json:"skillName"`
	ManagedAt string `json:"managedAt"`
}
type marketplaceRecord struct {
	Name, Source, Cache string
}

func (s *Service) executeSkills(ctx context.Context, kind string, payload map[string]any) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	release, err := acquireExtensionOperation(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	cfg, err := s.loadConfig()
	if err != nil {
		return nil, err
	}
	state, err := s.loadExtensionState()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch kind {
	case "skills.sources/catalog":
		if id := payloadString(payload, "sourceId"); id != "" {
			source, ok := findSkillSource(cfg.SkillSources, id)
			if !ok {
				return nil, fmt.Errorf("找不到 Skill 来源：%s", id)
			}
			status, err := s.refreshSkillSource(ctx, cfg, source, state)
			if err != nil {
				return nil, err
			}
			return s.skillSourceStatusView(status, state), nil
		}
		return s.skillSourcesSnapshot(cfg, state), nil
	case "skills.sources/save":
		if err := requireConfirmed(payload, "保存并读取外部 Skill 来源"); err != nil {
			return nil, err
		}
		source, err := decodePayload[domain.SkillSource](payload, "source")
		if err != nil {
			return nil, err
		}
		if err := source.Validate(); err != nil {
			return nil, err
		}
		updated, err := s.config.Update(func(current domain.AppConfig) (domain.AppConfig, error) {
			if err := ctx.Err(); err != nil {
				return current, err
			}
			for _, configured := range current.SkillSources {
				if strings.EqualFold(configured.ID, source.ID) {
					return current, fmt.Errorf("Skill 来源 ID 已存在")
				}
			}
			current.SkillSources = append(current.SkillSources, source)
			return current, nil
		})
		if err != nil {
			return nil, err
		}
		status, err := s.refreshSkillSource(ctx, updated, source, state)
		if err != nil {
			return nil, err
		}
		return s.skillSourceStatusView(status, state), nil
	case "skills.sources/remove":
		if err := requireConfirmed(payload, "移除 Skill 来源及其 SilverWing 缓存"); err != nil {
			return nil, err
		}
		id := payloadString(payload, "sourceId")
		source, ok := findSkillSource(cfg.SkillSources, id)
		if !ok {
			return nil, fmt.Errorf("找不到 Skill 来源：%s", id)
		}
		root := filepath.Join(s.paths.Codex, skillSourcesName)
		checkout := filepath.Join(root, source.ID)
		if err := removeWithin(root, checkout); err != nil {
			return nil, err
		}
		state.SkillSources = removeSkillSnapshot(state.SkillSources, id)
		if err := s.saveExtensionState(state); err != nil {
			return nil, err
		}
		_, err = s.config.Update(func(current domain.AppConfig) (domain.AppConfig, error) {
			current.SkillSources = filterSkillSources(current.SkillSources, id)
			return current, nil
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"removed": true, "sourceId": id, "installedSkillsKept": true}, nil
	case "skills.catalog":
		return s.localSkillsCatalog(state)
	case "skills.files/list":
		return s.localSkillFiles(payloadString(payload, "directoryName"))
	case "skills.files/preview":
		return s.previewLocalSkillFile(payloadString(payload, "directoryName"), payloadRawString(payload, "relativePath"))
	case "skills.files/copy-path":
		path, err := s.extensionSkillCopyFilePath(cfg, state, payload)
		if err != nil {
			return nil, err
		}
		return map[string]any{"path": path}, nil
	case "skills.preview":
		file, err := s.externalSkillPreviewFile(cfg, state, payloadString(payload, "sourceId"), payloadString(payload, "name"))
		if err != nil {
			return nil, err
		}
		content, err := readExtensionText(file, maxSkillBytes)
		if err != nil {
			return nil, err
		}
		return map[string]any{"name": payloadString(payload, "name"), "text": string(content), "path": file}, nil
	case "skills.preview/path":
		file, err := s.externalSkillPreviewFile(cfg, state, payloadString(payload, "sourceId"), payloadString(payload, "name"))
		if err != nil {
			return nil, err
		}
		return file, nil
	case "skills.install", "skills.update":
		if err := requireConfirmed(payload, "安装 Skill"); err != nil {
			return nil, err
		}
		sourceID, skillName := payloadString(payload, "sourceId"), payloadString(payload, "name")
		source, ok := findSkillSource(cfg.SkillSources, sourceID)
		if !ok {
			return nil, fmt.Errorf("找不到 Skill 来源：%s", sourceID)
		}
		takeOver := payloadBool(payload, "takeOver")
		if kind == "skills.update" {
			if takeOver {
				return nil, fmt.Errorf("更新 Skill 不能接管外部安装")
			}
			takeOver = false
		}
		return s.installExternalSkill(ctx, source, skillName, takeOver, state)
	case "skills.remove":
		if err := requireConfirmed(payload, "卸载 Skill"); err != nil {
			return nil, err
		}
		name := payloadString(payload, "directoryName")
		if name == "" {
			name = payloadString(payload, "name")
		}
		if sourceID := payloadString(payload, "sourceId"); sourceID != "" {
			source, ok := findSkillSource(cfg.SkillSources, sourceID)
			if !ok {
				return nil, fmt.Errorf("找不到 Skill 来源：%s", sourceID)
			}
			return s.uninstallExternalSkill(ctx, source, name, state)
		}
		return s.uninstallLocalSkill(ctx, name, state)
	default:
		return nil, fmt.Errorf("未知 Skill 操作：%s", kind)
	}
}

func (s *Service) executeCodexExtensions(ctx context.Context, kind string, payload map[string]any) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.HasPrefix(kind, "codex.extensions/mcp/") {
		return s.executeMCP(ctx, strings.TrimPrefix(kind, "codex.extensions/mcp/"), payload)
	}
	if kind == "codex.extensions/branches/list" || kind == "skills.sources/branches/list" {
		return s.loadExtensionBranches(ctx, payload)
	}
	release, err := acquireExtensionOperation(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	cfg, err := s.loadConfig()
	if err != nil {
		return nil, err
	}
	state, err := s.loadExtensionState()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch kind {
	case "codex.extensions/snapshot", "codex.extensions/marketplaces/list":
		return s.extensionsSnapshot(cfg, state), nil
	case "codex.extensions/marketplaces/add-source":
		if err := requireConfirmed(payload, "添加并注册 Codex Marketplace"); err != nil {
			return nil, err
		}
		source, err := decodePayload[domain.CodexPluginMarketplaceSource](payload, "source")
		if err != nil {
			return nil, err
		}
		if err := source.Validate(); err != nil {
			return nil, err
		}
		updated, err := s.config.Update(func(current domain.AppConfig) (domain.AppConfig, error) {
			if err := ctx.Err(); err != nil {
				return current, err
			}
			for _, configured := range current.CodexPluginMarketplaceSources {
				if strings.EqualFold(configured.ID, source.ID) {
					return current, fmt.Errorf("插件来源 ID 已存在：%s", source.ID)
				}
			}
			current.CodexPluginMarketplaceSources = append(current.CodexPluginMarketplaceSources, source)
			return current, nil
		})
		if err != nil {
			return nil, err
		}
		status, err := s.refreshMarketplace(ctx, updated, source, state, true, false)
		if err != nil {
			return nil, err
		}
		return marketplaceStatusView(status, state), nil
	case "codex.extensions/marketplaces/refresh-source":
		source, ok := findMarketplace(cfg.CodexPluginMarketplaceSources, payloadString(payload, "sourceId"))
		if !ok {
			return nil, fmt.Errorf("找不到插件来源：%s", payloadString(payload, "sourceId"))
		}
		status, err := s.refreshMarketplace(ctx, cfg, source, state, true, true)
		if err != nil {
			return nil, err
		}
		return marketplaceStatusView(status, state), nil
	case "codex.extensions/marketplaces/remove-source":
		if err := requireConfirmed(payload, "移除 Codex Marketplace 来源"); err != nil {
			return nil, err
		}
		id := payloadString(payload, "sourceId")
		source, ok := findMarketplace(cfg.CodexPluginMarketplaceSources, id)
		if !ok {
			return nil, fmt.Errorf("找不到插件来源：%s", id)
		}
		if status, ok := state.Marketplaces[id]; ok && status.MarketplaceName != nil && strings.TrimSpace(*status.MarketplaceName) != "" {
			if _, err := s.runCodex(ctx, cfg, "plugin", "marketplace", "remove", *status.MarketplaceName, "--json"); err != nil {
				return nil, err
			}
		}
		delete(state.Marketplaces, source.ID)
		plugins := state.ManagedPlugins[:0]
		for _, managed := range state.ManagedPlugins {
			if managed.SourceID != source.ID {
				plugins = append(plugins, managed)
			}
		}
		state.ManagedPlugins = plugins
		if err := s.saveExtensionState(state); err != nil {
			return nil, err
		}
		_, err = s.config.Update(func(current domain.AppConfig) (domain.AppConfig, error) {
			current.CodexPluginMarketplaceSources = filterMarketplaces(current.CodexPluginMarketplaceSources, id)
			return current, nil
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"removed": true, "sourceId": id, "installedPluginsKept": true}, nil
	case "codex.extensions/plugin/install", "codex.extensions/plugins/install":
		if err := requireConfirmed(payload, "安装 Codex 插件"); err != nil {
			return nil, err
		}
		sourceID, pluginName := payloadString(payload, "sourceId"), payloadString(payload, "name")
		source, ok := findMarketplace(cfg.CodexPluginMarketplaceSources, sourceID)
		if !ok {
			return nil, fmt.Errorf("找不到插件来源：%s", sourceID)
		}
		status, ok := state.Marketplaces[sourceID]
		if !ok || status.MarketplaceName == nil {
			return nil, fmt.Errorf("请先加载插件来源“%s”", source.Name)
		}
		var plugin *PluginCatalogItem
		for i := range status.Plugins {
			if status.Plugins[i].Name == pluginName {
				plugin = &status.Plugins[i]
				break
			}
		}
		if plugin == nil {
			return nil, fmt.Errorf("插件来源“%s”中找不到 %s", source.Name, pluginName)
		}
		managed := managedPluginOwned(state, sourceID, pluginName)
		if plugin.Installed && !managed && !payloadBool(payload, "takeOver") {
			return nil, fmt.Errorf("%s 已由外部方式安装；确认接管后才能由 SilverWing 卸载它", pluginName)
		}
		if !plugin.Installed {
			if _, err := s.runCodex(ctx, cfg, "plugin", "add", pluginName, "--marketplace", *status.MarketplaceName, "--json"); err != nil {
				return nil, err
			}
		}
		for i := range status.Plugins {
			if status.Plugins[i].Name == pluginName {
				status.Plugins[i].Installed = true
			}
		}
		status.Error = nil
		state.Marketplaces[sourceID] = status
		state.ManagedPlugins = append(filterManagedPlugins(state.ManagedPlugins, sourceID, pluginName), managedPlugin{SourceID: sourceID, Name: pluginName, ManagedAt: time.Now().UTC().Format(time.RFC3339Nano)})
		if err := s.saveExtensionState(state); err != nil {
			return nil, err
		}
		return marketplaceStatusView(status, state), nil
	case "codex.extensions/plugin/uninstall", "codex.extensions/plugins/uninstall":
		if err := requireConfirmed(payload, "卸载 Codex 插件"); err != nil {
			return nil, err
		}
		sourceID, pluginName := payloadString(payload, "sourceId"), payloadString(payload, "name")
		source, ok := findMarketplace(cfg.CodexPluginMarketplaceSources, sourceID)
		if !ok {
			return nil, fmt.Errorf("找不到插件来源：%s", sourceID)
		}
		status, ok := state.Marketplaces[sourceID]
		if !ok || status.MarketplaceName == nil {
			return nil, fmt.Errorf("请先加载插件来源“%s”", source.Name)
		}
		if !managedPluginOwned(state, sourceID, pluginName) {
			return nil, fmt.Errorf("%s 不是 SilverWing 管理的插件；请先确认接管", pluginName)
		}
		if _, err := s.runCodex(ctx, cfg, "plugin", "remove", pluginName, "--marketplace", *status.MarketplaceName, "--json"); err != nil {
			return nil, err
		}
		for i := range status.Plugins {
			if status.Plugins[i].Name == pluginName {
				status.Plugins[i].Installed = false
			}
		}
		status.Error = nil
		state.Marketplaces[sourceID] = status
		state.ManagedPlugins = filterManagedPlugins(state.ManagedPlugins, sourceID, pluginName)
		if err := s.saveExtensionState(state); err != nil {
			return nil, err
		}
		return marketplaceStatusView(status, state), nil
	case "codex.extensions/plugin/preview":
		return s.previewPlugin(cfg, state, payloadString(payload, "sourceId"), payloadString(payload, "name"))
	case "codex.extensions/plugin/skill-preview":
		return s.previewPluginSkill(cfg, state, payloadString(payload, "sourceId"), payloadString(payload, "pluginName"), payloadString(payload, "skillName"))
	default:
		return nil, fmt.Errorf("未知 Codex 扩展操作：%s", kind)
	}
}

func (s *Service) refreshMarketplace(ctx context.Context, cfg domain.AppConfig, source domain.CodexPluginMarketplaceSource, state extensionState, register, upgrade bool) (MarketplaceSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return MarketplaceSnapshot{}, err
	}
	old, exists := state.Marketplaces[source.ID]
	if !exists {
		old = MarketplaceSnapshot{SourceID: source.ID, Plugins: []PluginCatalogItem{}}
	}
	if source.RequiresRootMarketplaceMigration() {
		old.Error = stringPtr(fmt.Sprintf("插件来源“%s”使用旧版子目录 Marketplace（%s）。请移除后按仓库根目录重新添加。", source.Name, source.MarketplaceDirectory))
		state.Marketplaces[source.ID] = old
		if err := s.saveExtensionState(state); err != nil {
			return MarketplaceSnapshot{}, err
		}
		return old, nil
	}
	marketplaceName := old.MarketplaceName
	var opErr error
	if marketplaceName == nil || strings.TrimSpace(*marketplaceName) == "" {
		if register {
			marketplaceName, opErr = s.registerMarketplace(ctx, cfg, source)
		}
	}
	if opErr == nil && marketplaceName != nil {
		if upgrade {
			_, opErr = s.runCodex(ctx, cfg, "plugin", "marketplace", "upgrade", *marketplaceName, "--json")
		}
	}
	if opErr == nil && marketplaceName != nil {
		var plugins []PluginCatalogItem
		var raw string
		raw, opErr = s.runCodexOutput(ctx, cfg, "plugin", "list", "--marketplace", *marketplaceName, "--available", "--json")
		if opErr == nil {
			plugins, opErr = parsePluginCatalog(raw)
		}
		if opErr == nil {
			var records []marketplaceRecord
			list, listErr := s.runCodexOutput(ctx, cfg, "plugin", "marketplace", "list", "--json")
			if process.IsCleanupFailure(listErr) {
				return MarketplaceSnapshot{}, listErr
			}
			opErr = listErr
			if opErr == nil {
				records, opErr = parseMarketplaceCatalog(list)
			}
			if opErr == nil {
				var cache *string
				for _, record := range records {
					if record.Name == *marketplaceName && record.Cache != "" {
						cache = stringPtr(record.Cache)
						break
					}
				}
				if cache == nil {
					cache = old.CacheDirectory
				}
				if err := ctx.Err(); err != nil {
					return MarketplaceSnapshot{}, err
				}
				status := MarketplaceSnapshot{SourceID: source.ID, MarketplaceName: marketplaceName, CacheDirectory: cache, Plugins: plugins, UpdatedAt: stringPtr(time.Now().UTC().Format(time.RFC3339Nano))}
				state.Marketplaces[source.ID] = status
				if err := s.saveExtensionState(state); err != nil {
					return MarketplaceSnapshot{}, err
				}
				return status, nil
			}
		}
	}
	if process.IsCleanupFailure(opErr) {
		return MarketplaceSnapshot{}, opErr
	}
	if err := ctx.Err(); err != nil {
		return MarketplaceSnapshot{}, err
	}
	if opErr == nil {
		opErr = fmt.Errorf("无法识别 Codex Marketplace 名称")
	}
	old.Error = stringPtr(redactProxy(cfg, truncate(opErr.Error(), 4000)))
	state.Marketplaces[source.ID] = old
	if err := s.saveExtensionState(state); err != nil {
		return MarketplaceSnapshot{}, err
	}
	return old, nil
}

func (s *Service) registerMarketplace(ctx context.Context, cfg domain.AppConfig, source domain.CodexPluginMarketplaceSource) (*string, error) {
	beforeRaw, err := s.runCodexOutput(ctx, cfg, "plugin", "marketplace", "list", "--json")
	if err != nil {
		return nil, err
	}
	before, err := parseMarketplaceCatalog(beforeRaw)
	if err != nil {
		return nil, err
	}
	args := []string{"plugin", "marketplace", "add", source.RepositoryURL}
	if source.Ref != nil {
		args = append(args, "--ref", *source.Ref)
	}
	args = append(args, "--json")
	added, err := s.runCodexOutput(ctx, cfg, args...)
	if err != nil {
		return nil, err
	}
	afterRaw, err := s.runCodexOutput(ctx, cfg, "plugin", "marketplace", "list", "--json")
	if err != nil {
		return nil, err
	}
	after, err := parseMarketplaceCatalog(afterRaw)
	if err != nil {
		return nil, err
	}
	normalized := normalizeRemote(source.RepositoryURL)
	for _, record := range after {
		if record.Source != "" && normalizeRemote(record.Source) == normalized {
			return stringPtr(record.Name), nil
		}
	}
	newNames := []string{}
	beforeNames := map[string]bool{}
	for _, record := range before {
		beforeNames[record.Name] = true
	}
	for _, record := range after {
		if !beforeNames[record.Name] {
			newNames = append(newNames, record.Name)
		}
	}
	if len(newNames) == 1 {
		return stringPtr(newNames[0]), nil
	}
	addedRecords, _ := parseMarketplaceCatalog(added)
	if len(addedRecords) > 0 {
		return stringPtr(addedRecords[0].Name), nil
	}
	return nil, fmt.Errorf("Codex 已执行 Marketplace 添加，但未返回可识别的名称")
}

func (s *Service) previewPlugin(cfg domain.AppConfig, state extensionState, sourceID, name string) (map[string]any, error) {
	source, ok := findMarketplace(cfg.CodexPluginMarketplaceSources, sourceID)
	if !ok {
		return nil, fmt.Errorf("找不到插件来源：%s", sourceID)
	}
	_ = source
	status, ok := state.Marketplaces[sourceID]
	if !ok {
		return nil, fmt.Errorf("请先加载插件来源")
	}
	var plugin *PluginCatalogItem
	for i := range status.Plugins {
		if status.Plugins[i].Name == name {
			plugin = &status.Plugins[i]
			break
		}
	}
	if plugin == nil {
		return nil, fmt.Errorf("插件来源中找不到 %s", name)
	}
	if status.CacheDirectory == nil || plugin.SourcePath == nil {
		return nil, fmt.Errorf("Codex 未提供插件本地缓存目录，请先刷新来源")
	}
	root, err := filepath.Abs(*status.CacheDirectory)
	if err != nil {
		return nil, err
	}
	dir, err := filepath.Abs(*plugin.SourcePath)
	if err != nil {
		return nil, err
	}
	manifest, err := readPluginPreviewManifest(root, dir)
	if err != nil {
		return nil, err
	}
	interfaceInfo, _ := manifest["interface"].(map[string]any)
	previewName := firstString(manifest, "name")
	if previewName == "" {
		previewName = plugin.Name
	}
	version := plugin.Version
	if manifestVersion := firstString(manifest, "version"); manifestVersion != "" {
		version = stringPtr(manifestVersion)
	}
	description := firstString(manifest, "description")
	if description == "" {
		description = firstString(interfaceInfo, "longDescription", "shortDescription")
	}
	if description == "" {
		description = plugin.Description
	}
	author, _ := manifest["author"].(map[string]any)
	previewAuthor := firstString(author, "name")
	if previewAuthor == "" {
		previewAuthor = firstString(interfaceInfo, "developerName")
	}
	capabilities := []string{}
	if values, ok := interfaceInfo["capabilities"].([]any); ok {
		for _, value := range values {
			if text, ok := value.(string); ok {
				capabilities = append(capabilities, text)
			}
		}
	}
	var skills []map[string]string
	if raw, ok := manifest["skills"].(string); ok && strings.TrimSpace(raw) != "" {
		relative := strings.TrimSuffix(strings.TrimPrefix(raw, "./"), "/")
		skillsRoot, err := safeChild(dir, relative)
		if err != nil {
			return nil, fmt.Errorf("插件 Skill 目录不安全：%w", err)
		}
		skills, err = discoverPluginSkills(skillsRoot)
		if err != nil {
			return nil, err
		}
	}
	readme, err := readOptional(filepath.Join(dir, "README.md"))
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"name": previewName, "version": version, "description": description,
		"author":       optionalString(map[string]any{"author": previewAuthor}, "author"),
		"category":     optionalString(map[string]any{"category": firstString(interfaceInfo, "category")}, "category"),
		"capabilities": capabilities, "readme": readme, "skills": skills,
		"plugin": plugin,
	}, nil
}

func discoverPluginSkills(root string) ([]map[string]string, error) {
	if !isDir(root) || isSymlink(root) {
		return nil, fmt.Errorf("插件 Skill 目录不存在或不是普通目录：%s", root)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	items := []map[string]string{}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		directory := filepath.Join(root, entry.Name())
		if err := validateSkillTree(directory); err != nil {
			return nil, err
		}
		content, err := readExtensionText(filepath.Join(directory, skillFileName), maxSkillBytes)
		if err != nil {
			return nil, err
		}
		front := skillFrontMatter.FindSubmatch(content)
		if len(front) < 2 {
			return nil, fmt.Errorf("Skill 缺少 YAML frontmatter：%s", directory)
		}
		name := frontMatterValue(string(front[1]), "name")
		if name == "" {
			name = entry.Name()
		}
		items = append(items, map[string]string{
			"name":        name,
			"description": frontMatterValue(string(front[1]), "description"),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["name"] < items[j]["name"] })
	return items, nil
}

func (s *Service) previewPluginSkill(cfg domain.AppConfig, state extensionState, sourceID, pluginName, skillName string) (map[string]any, error) {
	preview, err := s.previewPlugin(cfg, state, sourceID, pluginName)
	if err != nil {
		return nil, err
	}
	discovered, _ := preview["skills"].([]map[string]string)
	found := false
	for _, skill := range discovered {
		if skill["name"] == skillName {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("插件中找不到内置 Skill：%s", skillName)
	}
	status := state.Marketplaces[sourceID]
	var plugin *PluginCatalogItem
	for i := range status.Plugins {
		if status.Plugins[i].Name == pluginName {
			plugin = &status.Plugins[i]
			break
		}
	}
	if plugin == nil || plugin.SourcePath == nil || status.CacheDirectory == nil {
		return nil, fmt.Errorf("插件缓存路径不可用")
	}
	dir, err := filepath.Abs(*plugin.SourcePath)
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(*status.CacheDirectory)
	if err != nil {
		return nil, err
	}
	if !within(root, dir) {
		return nil, fmt.Errorf("插件路径越界")
	}
	manifest, err := readPluginPreviewManifest(root, dir)
	if err != nil {
		return nil, err
	}
	relative, _ := manifest["skills"].(string)
	if relative == "" {
		return nil, fmt.Errorf("插件没有配置内置 Skill 目录")
	}
	skillsRoot, err := safeChild(dir, strings.TrimPrefix(strings.TrimSuffix(relative, "/"), "./"))
	if err != nil {
		return nil, err
	}
	if err := ensureNoSymlinksBetween(root, skillsRoot); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(skillsRoot)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() != skillName || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if err := validateNamedSkillTree(filepath.Join(skillsRoot, skillName), skillName); err != nil {
			return nil, err
		}
		content, err := readExtensionText(filepath.Join(skillsRoot, skillName, skillFileName), maxSkillBytes)
		if err != nil {
			return nil, err
		}
		return map[string]any{"name": skillName, "text": string(content), "path": filepath.Join(skillsRoot, skillName, skillFileName)}, nil
	}
	return nil, fmt.Errorf("插件中找不到内置 Skill：%s", skillName)
}

func parseMarketplaceCatalog(output string) ([]marketplaceRecord, error) {
	var root any
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &root); err != nil {
		return nil, fmt.Errorf("Codex 未返回可识别的 JSON：%w", err)
	}
	items := selectObjectList(root, "marketplaces", "items", "sources")
	records := []marketplaceRecord{}
	seen := map[string]bool{}
	for _, item := range items {
		name := firstString(item, "name", "marketplaceName", "marketplace", "id")
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		source := firstString(item, "source", "url", "repositoryUrl")
		if nested, ok := item["source"].(map[string]any); ok && source == "" {
			source = firstString(nested, "source", "url", "repositoryUrl", "path")
		}
		if nested, ok := item["marketplaceSource"].(map[string]any); ok && source == "" {
			source = firstString(nested, "source", "url", "repositoryUrl", "path")
		}
		cache := firstString(item, "cacheDirectory", "cache_path", "cachePath", "resolvedPath", "installedRoot", "root", "directory", "path")
		records = append(records, marketplaceRecord{name, source, cache})
	}
	return records, nil
}

func parsePluginCatalog(output string) ([]PluginCatalogItem, error) {
	var root any
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &root); err != nil {
		return nil, fmt.Errorf("Codex 未返回可识别的 JSON：%w", err)
	}
	items := selectObjectList(root, "plugins", "items", "available", "installed")
	plugins := []PluginCatalogItem{}
	seen := map[string]bool{}
	for _, item := range items {
		nested, ok := item["plugin"].(map[string]any)
		if !ok {
			nested = item
		}
		name := firstString(nested, "name", "id")
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		description := firstString(nested, "description", "summary")
		if description == "" {
			if ui, ok := nested["interface"].(map[string]any); ok {
				description = firstString(ui, "description", "shortDescription")
			}
		}
		version := optionalString(nested, "version", "latestVersion")
		installed, hasInstalled := optionalBool(nested, "installed", "isInstalled")
		if !hasInstalled {
			status := strings.ToLower(firstString(nested, "status"))
			installed = status == "installed" || status == "enabled"
			if _, hasEnabled := optionalBool(nested, "enabled"); hasEnabled {
				installed, _ = optionalBool(nested, "enabled")
			}
		}
		var skills []string
		if values, ok := nested["skills"].([]any); ok {
			for _, raw := range values {
				switch value := raw.(type) {
				case string:
					skills = append(skills, value)
				case map[string]any:
					if name := firstString(value, "name", "id"); name != "" {
						skills = append(skills, name)
					}
				}
			}
		}
		sourcePath := optionalString(nested, "sourcePath", "path")
		if source, ok := nested["source"].(map[string]any); ok {
			if candidate := firstString(source, "path", "directory"); candidate != "" {
				sourcePath = stringPtr(candidate)
			}
		}
		plugins = append(plugins, PluginCatalogItem{Name: name, Description: description, Version: version, Installed: installed, SourcePath: sourcePath, BundledSkills: uniqueStrings(skills)})
	}
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Name < plugins[j].Name })
	return plugins, nil
}

func selectObjectList(root any, names ...string) []map[string]any {
	if object, ok := root.(map[string]any); ok {
		for _, name := range names {
			if values, ok := object[name]; ok {
				if result := objectList(values); len(result) > 0 {
					return result
				}
			}
		}
	}
	return objectList(root)
}
func objectList(value any) []map[string]any {
	switch typed := value.(type) {
	case []any:
		out := []map[string]any{}
		for _, item := range typed {
			if object, ok := item.(map[string]any); ok {
				out = append(out, object)
			}
		}
		return out
	case map[string]any:
		return []map[string]any{typed}
	default:
		return nil
	}
}
func firstString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := object[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
func optionalString(object map[string]any, keys ...string) *string {
	if value := firstString(object, keys...); value != "" {
		return stringPtr(value)
	}
	return nil
}
func optionalBool(object map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		if value, ok := object[key].(bool); ok {
			return value, true
		}
	}
	return false, false
}
func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func findMarketplace(values []domain.CodexPluginMarketplaceSource, id string) (domain.CodexPluginMarketplaceSource, bool) {
	for _, value := range values {
		if value.ID == id {
			return value, true
		}
	}
	return domain.CodexPluginMarketplaceSource{}, false
}
func filterMarketplaces(values []domain.CodexPluginMarketplaceSource, id string) []domain.CodexPluginMarketplaceSource {
	out := values[:0]
	for _, value := range values {
		if value.ID != id {
			out = append(out, value)
		}
	}
	return out
}
func managedPluginOwned(state extensionState, sourceID, name string) bool {
	for _, entry := range state.ManagedPlugins {
		if entry.SourceID == sourceID && entry.Name == name {
			return true
		}
	}
	return false
}
func filterManagedPlugins(values []managedPlugin, sourceID, name string) []managedPlugin {
	out := values[:0]
	for _, entry := range values {
		if entry.SourceID != sourceID || entry.Name != name {
			out = append(out, entry)
		}
	}
	return out
}
func normalizeRemote(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err == nil && parsed.Host != "" {
		parsed.User = nil
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		return strings.TrimSuffix(parsed.String(), "/")
	}
	return strings.TrimSuffix(strings.TrimSpace(value), ".git")
}
func readOptional(path string) (*string, error) {
	if !isRegular(path) || isSymlink(path) {
		return nil, nil
	}
	data, err := readExtensionText(path, maxPluginBytes)
	if err != nil {
		return nil, err
	}
	value := string(data)
	return &value, nil
}

var _ = sha256.Size
var _ = hashBytes

func (s *Service) skillSourcesSnapshot(cfg domain.AppConfig, state extensionState) map[string]any {
	items := make([]map[string]any, 0, len(cfg.SkillSources))
	for _, source := range cfg.SkillSources {
		status, ok := state.SkillSources[source.ID]
		if !ok {
			status = SkillSourceSnapshot{SourceID: source.ID, Skills: []ExternalSkill{}, DiscoveredRoots: []string{}}
		}
		items = append(items, map[string]any{"source": source, "status": s.skillSourceStatusView(status, state)})
	}
	return map[string]any{"sources": items}
}

func (s *Service) extensionsSnapshot(cfg domain.AppConfig, state extensionState) map[string]any {
	marketplaces := make([]map[string]any, 0, len(cfg.CodexPluginMarketplaceSources))
	for _, source := range cfg.CodexPluginMarketplaceSources {
		status, ok := state.Marketplaces[source.ID]
		if !ok {
			status = MarketplaceSnapshot{SourceID: source.ID, Plugins: []PluginCatalogItem{}}
		}
		marketplaces = append(marketplaces, map[string]any{"source": source, "status": marketplaceStatusView(status, state)})
	}
	skillSources := make([]map[string]any, 0, len(cfg.SkillSources))
	for _, source := range cfg.SkillSources {
		status, ok := state.SkillSources[source.ID]
		if !ok {
			status = SkillSourceSnapshot{SourceID: source.ID, Skills: []ExternalSkill{}, DiscoveredRoots: []string{}}
		}
		skillSources = append(skillSources, map[string]any{"source": source, "status": s.skillSourceStatusView(status, state)})
	}
	return map[string]any{"marketplaces": marketplaces, "skillSources": skillSources}
}

const (
	extensionOwnershipManaged      = "MANAGED"
	extensionOwnershipExternal     = "EXTERNAL"
	extensionOwnershipNotInstalled = "NOT_INSTALLED"
)

func marketplaceStatusView(status MarketplaceSnapshot, state extensionState) map[string]any {
	result := jsonObject(status)
	plugins := make([]map[string]any, 0, len(status.Plugins))
	for _, plugin := range status.Plugins {
		item := jsonObject(plugin)
		switch {
		case managedPluginOwned(state, status.SourceID, plugin.Name):
			item["ownership"] = extensionOwnershipManaged
		case plugin.Installed:
			item["ownership"] = extensionOwnershipExternal
		default:
			item["ownership"] = extensionOwnershipNotInstalled
		}
		plugins = append(plugins, item)
	}
	result["plugins"] = plugins
	return result
}

func (s *Service) skillSourceStatusView(status SkillSourceSnapshot, state extensionState) map[string]any {
	result := jsonObject(status)
	skills := make([]map[string]any, 0, len(status.Skills))
	for _, skill := range status.Skills {
		item := jsonObject(skill)
		switch {
		case !s.localSkillDirectoryExists(skill.Name):
			item["ownership"] = extensionOwnershipNotInstalled
		case managedSkillOwned(state, status.SourceID, skill.Name):
			item["ownership"] = extensionOwnershipManaged
		default:
			item["ownership"] = extensionOwnershipExternal
		}
		skills = append(skills, item)
	}
	result["skills"] = skills
	return result
}

func jsonObject(value any) map[string]any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil || result == nil {
		return map[string]any{}
	}
	return result
}

func (s *Service) localSkillDirectoryExists(name string) bool {
	name, err := safeLocalSkillDirectoryName(name)
	if err != nil {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	root := filepath.Join(home, ".agents", "skills")
	directory := filepath.Join(root, name)
	if !within(root, directory) {
		return false
	}
	_, err = os.Lstat(directory)
	return err == nil
}

// Resolve the preview's source identity again before copying its file reference.
// A path supplied by the desktop is never a copy target.
func (s *Service) extensionSkillCopyFilePath(cfg domain.AppConfig, state extensionState, payload map[string]any) (string, error) {
	if pluginName := payloadString(payload, "pluginName"); pluginName != "" {
		preview, err := s.previewPluginSkill(cfg, state, payloadString(payload, "sourceId"), pluginName, payloadString(payload, "skillName"))
		if err != nil {
			return "", err
		}
		return preview["path"].(string), nil
	}
	if sourceID := payloadString(payload, "sourceId"); sourceID != "" {
		return s.externalSkillPreviewFile(cfg, state, sourceID, payloadString(payload, "name"))
	}
	return s.resolveLocalSkillFilePath(payloadString(payload, "directoryName"), payloadRawString(payload, "relativePath"))
}

func (s *Service) refreshSkillSource(ctx context.Context, cfg domain.AppConfig, source domain.SkillSource, state extensionState) (SkillSourceSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return SkillSourceSnapshot{}, err
	}
	if err := source.Validate(); err != nil {
		return SkillSourceSnapshot{}, err
	}
	previous := state.SkillSources[source.ID]
	checkout, err := s.ensureSkillCheckout(ctx, cfg, source)
	if err == nil {
		var skills []ExternalSkill
		var roots []string
		skills, roots, err = discoverSkills(source, checkout)
		if err == nil {
			if err := ctx.Err(); err != nil {
				return SkillSourceSnapshot{}, err
			}
			updated := SkillSourceSnapshot{SourceID: source.ID, Skills: skills, DiscoveredRoots: roots, UpdatedAt: stringPtr(time.Now().UTC().Format(time.RFC3339Nano))}
			state.SkillSources[source.ID] = updated
			if saveErr := s.saveExtensionState(state); saveErr != nil {
				return SkillSourceSnapshot{}, saveErr
			}
			return updated, nil
		}
	}
	if process.IsCleanupFailure(err) {
		return SkillSourceSnapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return SkillSourceSnapshot{}, err
	}
	previous.SourceID = source.ID
	if previous.Skills == nil {
		previous.Skills = []ExternalSkill{}
	}
	if previous.DiscoveredRoots == nil {
		previous.DiscoveredRoots = []string{}
	}
	previous.Error = stringPtr(redactProxy(cfg, err.Error()))
	state.SkillSources[source.ID] = previous
	if saveErr := s.saveExtensionState(state); saveErr != nil {
		return SkillSourceSnapshot{}, saveErr
	}
	return previous, nil
}

func (s *Service) ensureSkillCheckout(ctx context.Context, cfg domain.AppConfig, source domain.SkillSource) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	root := filepath.Join(s.paths.Codex, skillSourcesName)
	checkout := filepath.Join(root, source.ID)
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	checkoutAbs, err := filepath.Abs(checkout)
	if err != nil {
		return "", err
	}
	if !within(rootAbs, checkoutAbs) || samePath(rootAbs, checkoutAbs) {
		return "", fmt.Errorf("Skill 缓存路径不安全")
	}
	if err := ensureNoExistingSymlinks(rootAbs); err != nil {
		return "", err
	}
	if err := os.MkdirAll(rootAbs, 0o755); err != nil {
		return "", err
	}
	if err := ensureNoExistingSymlinks(rootAbs); err != nil {
		return "", err
	}
	_, git, _, err := s.resolveCLIExecutable(ctx, cfg, "git")
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	gitArgs := []string{"-c", "core.symlinks=false"}
	gitDirectory := filepath.Join(checkoutAbs, ".git")
	if !isDir(gitDirectory) || isSymlink(gitDirectory) {
		if exists(checkoutAbs) {
			if err := removeWithin(rootAbs, checkoutAbs); err != nil {
				return "", err
			}
		}
		gitArgs = append(gitArgs, "clone", "--depth", "1")
		if source.Ref != nil && strings.TrimSpace(*source.Ref) != "" {
			gitArgs = append(gitArgs, "--branch", *source.Ref)
		}
		gitArgs = append(gitArgs, source.RepositoryURL, checkoutAbs)
	} else if err := ensureNoSymlinks(checkoutAbs); err != nil {
		return "", err
	} else if source.Ref == nil || strings.TrimSpace(*source.Ref) == "" {
		gitArgs = append(gitArgs, "-C", checkoutAbs, "pull", "--ff-only")
	} else {
		fetch := []string{"-c", "core.symlinks=false", "-C", checkoutAbs, "fetch", "--depth", "1", "origin", *source.Ref}
		if _, err := s.runTool(ctx, cfg, domain.ProxyTargetGit, git, fetch, checkoutAbs, 5*time.Minute); err != nil {
			return "", err
		}
		gitArgs = append(gitArgs, "-C", checkoutAbs, "checkout", "--force", "FETCH_HEAD")
	}
	if _, err := s.runTool(ctx, cfg, domain.ProxyTargetGit, git, gitArgs, rootAbs, 5*time.Minute); err != nil {
		return "", err
	}
	if err := ensureNoSymlinks(checkoutAbs); err != nil {
		return "", err
	}
	return checkoutAbs, nil
}

func discoverSkills(source domain.SkillSource, checkout string) ([]ExternalSkill, []string, error) {
	root := filepath.Clean(checkout)
	var candidates []string
	if source.SkillRoot != nil && strings.TrimSpace(*source.SkillRoot) != "" {
		resolved, err := safeChild(root, *source.SkillRoot)
		if err != nil {
			return nil, nil, err
		}
		candidates = []string{resolved}
	} else {
		candidates = []string{filepath.Join(root, ".agents", "skills"), filepath.Join(root, "skills")}
	}
	var skills []ExternalSkill
	var discoveredRoots []string
	for _, candidate := range candidates {
		if !isDir(candidate) || isSymlink(candidate) {
			continue
		}
		dirs := []string{}
		if isRegular(filepath.Join(candidate, skillFileName)) {
			dirs = append(dirs, candidate)
		} else {
			entries, err := os.ReadDir(candidate)
			if err != nil {
				return nil, nil, err
			}
			for _, entry := range entries {
				if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 && isRegular(filepath.Join(candidate, entry.Name(), skillFileName)) {
					dirs = append(dirs, filepath.Join(candidate, entry.Name()))
				}
			}
		}
		if len(dirs) == 0 {
			continue
		}
		relRoot, err := filepath.Rel(root, candidate)
		if err != nil {
			return nil, nil, err
		}
		discoveredRoots = append(discoveredRoots, filepath.ToSlash(relRoot))
		for _, dir := range dirs {
			item, err := parseSkillDirectory(dir, root)
			if err != nil {
				return nil, nil, err
			}
			skills = append(skills, item)
		}
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	seen := map[string]bool{}
	for _, skill := range skills {
		if seen[skill.Name] {
			return nil, nil, fmt.Errorf("Skill 来源包含重复名称：%s", skill.Name)
		}
		seen[skill.Name] = true
	}
	return skills, discoveredRoots, nil
}

func parseSkillDirectory(directory, checkout string) (ExternalSkill, error) {
	if err := validateSkillTree(directory); err != nil {
		return ExternalSkill{}, err
	}
	data, err := readExtensionText(filepath.Join(directory, skillFileName), maxSkillBytes)
	if err != nil {
		return ExternalSkill{}, err
	}
	front := skillFrontMatter.FindSubmatch(data)
	if len(front) < 2 {
		return ExternalSkill{}, fmt.Errorf("Skill 缺少 YAML frontmatter：%s", directory)
	}
	name, description := frontMatterValue(string(front[1]), "name"), frontMatterValue(string(front[1]), "description")
	if name == "" {
		return ExternalSkill{}, fmt.Errorf("Skill 缺少 name：%s", directory)
	}
	if !skillNamePattern.MatchString(name) {
		return ExternalSkill{}, fmt.Errorf("Skill name 不合法：%s", name)
	}
	if description == "" {
		return ExternalSkill{}, fmt.Errorf("Skill description 不能为空：%s", name)
	}
	rel, err := filepath.Rel(checkout, directory)
	if err != nil {
		return ExternalSkill{}, err
	}
	return ExternalSkill{Name: name, Description: description, SourcePath: filepath.ToSlash(rel)}, nil
}

func (s *Service) externalSkillPreviewFile(cfg domain.AppConfig, state extensionState, sourceID, skillName string) (string, error) {
	source, ok := findSkillSource(cfg.SkillSources, sourceID)
	if !ok {
		return "", fmt.Errorf("找不到 Skill 来源：%s", sourceID)
	}
	status, ok := state.SkillSources[source.ID]
	if !ok {
		return "", fmt.Errorf("请先加载 Skill 来源“%s”", source.Name)
	}
	var skill *ExternalSkill
	for i := range status.Skills {
		if status.Skills[i].Name == skillName {
			skill = &status.Skills[i]
			break
		}
	}
	if skill == nil {
		return "", fmt.Errorf("Skill 来源“%s”中找不到 %s", source.Name, skillName)
	}
	checkout := filepath.Join(s.paths.Codex, skillSourcesName, source.ID)
	directory, err := safeChild(checkout, skill.SourcePath)
	if err != nil {
		return "", err
	}
	if err := validateNamedSkillTree(directory, skillName); err != nil {
		return "", err
	}
	file := filepath.Join(directory, skillFileName)
	if !isRegular(file) {
		return "", fmt.Errorf("Skill 文档不存在或不是普通文件：%s", directory)
	}
	return file, nil
}

func (s *Service) installExternalSkill(ctx context.Context, source domain.SkillSource, name string, takeover bool, state extensionState) (SkillSourceSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return SkillSourceSnapshot{}, err
	}
	status, ok := state.SkillSources[source.ID]
	if !ok {
		return SkillSourceSnapshot{}, fmt.Errorf("请先加载 Skill 来源“%s”", source.Name)
	}
	var item *ExternalSkill
	for i := range status.Skills {
		if status.Skills[i].Name == name {
			item = &status.Skills[i]
			break
		}
	}
	if item == nil {
		return SkillSourceSnapshot{}, fmt.Errorf("Skill 来源“%s”中找不到 %s", source.Name, name)
	}
	root := filepath.Join(s.paths.Codex, skillSourcesName)
	checkout, err := filepath.Abs(filepath.Join(root, source.ID))
	if err != nil {
		return SkillSourceSnapshot{}, err
	}
	sourceDir, err := safeChild(checkout, item.SourcePath)
	if err != nil {
		return SkillSourceSnapshot{}, err
	}
	if err := validateNamedSkillTreeWithContext(ctx, sourceDir, name); err != nil {
		return SkillSourceSnapshot{}, err
	}
	destinationRoot, err := os.UserHomeDir()
	if err != nil {
		return SkillSourceSnapshot{}, err
	}
	destinationRoot = filepath.Join(destinationRoot, ".agents", "skills")
	destination := filepath.Join(destinationRoot, name)
	if err := ensureNoExistingSymlinks(destinationRoot); err != nil {
		return SkillSourceSnapshot{}, err
	}
	if exists(destination) && !managedSkillOwned(state, source.ID, name) && !takeover {
		return SkillSourceSnapshot{}, fmt.Errorf("%s 已由外部方式安装；确认接管后才能覆盖它", name)
	}
	backup := ""
	if exists(destination) {
		backup, err = s.backupSkill(ctx, destination, name)
		if err != nil {
			return SkillSourceSnapshot{}, err
		}
	}
	if err := os.MkdirAll(destinationRoot, 0o755); err != nil {
		return SkillSourceSnapshot{}, err
	}
	if err := ensureNoExistingSymlinks(destinationRoot); err != nil {
		return SkillSourceSnapshot{}, err
	}
	staging := filepath.Join(destinationRoot, "."+name+"-silverwing-staging-"+randomSuffix())
	defer func() { _ = removeAny(staging) }()
	if err := copySkillDirectoryWithContext(ctx, sourceDir, staging); err != nil {
		return SkillSourceSnapshot{}, err
	}
	if err := validateNamedSkillTreeWithContext(ctx, staging, name); err != nil {
		return SkillSourceSnapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return SkillSourceSnapshot{}, err
	}
	// Once replacement begins, finish publishing its ownership without canceling
	// between removal and rename. All potentially long copying happens above.
	if exists(destination) {
		if err := removeSkillExact(destinationRoot, destination); err != nil {
			return SkillSourceSnapshot{}, err
		}
	}
	if err := os.Rename(staging, destination); err != nil {
		if backup != "" && !exists(destination) {
			_ = copyBackup(backup, destination)
		}
		return SkillSourceSnapshot{}, err
	}
	state.ManagedSkills = append(filterManagedSkillsByName(state.ManagedSkills, name), managedSkill{SourceID: source.ID, Name: name, ManagedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	if err := s.saveExtensionState(state); err != nil {
		return SkillSourceSnapshot{}, err
	}
	return status, nil
}

func (s *Service) uninstallExternalSkill(ctx context.Context, source domain.SkillSource, name string, state extensionState) (SkillSourceSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return SkillSourceSnapshot{}, err
	}
	if !skillNamePattern.MatchString(name) {
		return SkillSourceSnapshot{}, fmt.Errorf("Skill 名称不安全")
	}
	if !managedSkillOwned(state, source.ID, name) {
		return SkillSourceSnapshot{}, fmt.Errorf("%s 不是 SilverWing 管理的 Skill", name)
	}
	root, err := os.UserHomeDir()
	if err != nil {
		return SkillSourceSnapshot{}, err
	}
	root = filepath.Join(root, ".agents", "skills")
	destination := filepath.Join(root, name)
	if err := ensureNoExistingSymlinks(root); err != nil {
		return SkillSourceSnapshot{}, err
	}
	if exists(destination) {
		if _, err := s.backupSkill(ctx, destination, name); err != nil {
			return SkillSourceSnapshot{}, err
		}
		if err := ctx.Err(); err != nil {
			return SkillSourceSnapshot{}, err
		}
		if err := removeSkillExact(root, destination); err != nil {
			return SkillSourceSnapshot{}, err
		}
	}
	state.ManagedSkills = filterManagedSkills(state.ManagedSkills, source.ID, name)
	if err := s.saveExtensionState(state); err != nil {
		return SkillSourceSnapshot{}, err
	}
	return state.SkillSources[source.ID], nil
}

func safeLocalSkillDirectoryName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\\`) || filepath.Base(name) != name || !filepath.IsLocal(name) {
		return "", fmt.Errorf("Skill 目录名必须是单级目录名")
	}
	return name, nil
}

func payloadRawString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}

func (s *Service) uninstallLocalSkill(ctx context.Context, name string, state extensionState) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name, err := safeLocalSkillDirectoryName(name)
	if err != nil {
		return nil, err
	}
	root, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root = filepath.Join(root, ".agents", "skills")
	destination := filepath.Join(root, name)
	if err := ensureNoExistingSymlinks(root); err != nil {
		return nil, err
	}
	if !isDir(destination) || isSymlink(destination) {
		return nil, fmt.Errorf("Skill 目录不存在或不是普通目录：%s", destination)
	}
	if !isRegular(filepath.Join(destination, skillFileName)) {
		return nil, fmt.Errorf("Skill 缺少 SKILL.md：%s", destination)
	}
	if _, err := s.backupSkill(ctx, destination, "local-skill"); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := removeSkillExact(root, destination); err != nil {
		return nil, err
	}
	retained := state.ManagedSkills[:0]
	for _, entry := range state.ManagedSkills {
		if entry.Name != name {
			retained = append(retained, entry)
		}
	}
	state.ManagedSkills = retained
	if err := s.saveExtensionState(state); err != nil {
		return nil, err
	}
	return map[string]any{"removed": true, "directoryName": name}, nil
}

func (s *Service) localSkillsCatalog(state extensionState) (map[string]any, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(home, ".agents", "skills")
	if !exists(root) {
		return map[string]any{"root": root, "skills": []any{}}, nil
	}
	if !isDir(root) || isSymlink(root) {
		return nil, fmt.Errorf("本机 Skill 目录不是普通目录：%s", root)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	type localItem struct {
		DirectoryName string `json:"directoryName"`
		Name          string `json:"name"`
		Description   string `json:"description"`
		Ownership     string `json:"ownership"`
	}
	items := []localItem{}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !isRegular(filepath.Join(root, entry.Name(), skillFileName)) {
			continue
		}
		data, readErr := readBounded(filepath.Join(root, entry.Name(), skillFileName), maxSkillBytes)
		if readErr != nil {
			items = append(items, localItem{entry.Name(), entry.Name(), "无法读取 SKILL.md：" + readErr.Error(), localSkillOwnership(state, entry.Name())})
			continue
		}
		name, description := localSkillMetadata(entry.Name(), data)
		ownership := localSkillOwnership(state, entry.Name())
		items = append(items, localItem{entry.Name(), name, description, ownership})
	}
	sort.Slice(items, func(i, j int) bool {
		if strings.ToLower(items[i].Name) == strings.ToLower(items[j].Name) {
			return items[i].DirectoryName < items[j].DirectoryName
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	return map[string]any{"root": root, "skills": items}, nil
}

func localSkillMetadata(directoryName string, content []byte) (name, description string) {
	if front := localSkillFrontMatter.FindSubmatch(content); len(front) >= 2 {
		name = frontMatterValue(string(front[1]), "name")
		description = frontMatterValue(string(front[1]), "description")
	}
	if name == "" {
		name = directoryName
	}
	if description == "" {
		description = "未提供说明"
	}
	return name, description
}

func localSkillOwnership(state extensionState, directoryName string) string {
	for _, managed := range state.ManagedSkills {
		if managed.Name == directoryName {
			return extensionOwnershipManaged
		}
	}
	return extensionOwnershipExternal
}

func (s *Service) localSkillFiles(directoryName string) (map[string]any, error) {
	directoryName, err := safeLocalSkillDirectoryName(directoryName)
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(home, ".agents", "skills")
	if err := ensureNoExistingSymlinks(root); err != nil {
		return nil, err
	}
	directory := filepath.Join(root, directoryName)
	if !isDir(directory) || isSymlink(directory) || !isRegular(filepath.Join(directory, skillFileName)) {
		return nil, fmt.Errorf("Skill 目录不存在或缺少 SKILL.md：%s", directory)
	}
	type localFile struct {
		RelativePath string `json:"relativePath"`
		SizeBytes    int64  `json:"sizeBytes"`
		Markdown     bool   `json:"markdown"`
	}
	files := []localFile{}
	truncated := false
	err = filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if path != directory && entry.IsDir() && (entry.Type()&os.ModeSymlink != 0 || entry.Name() == ".git") {
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}
		if len(files) >= 2000 {
			truncated = true
			return filepath.SkipAll
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return nil
		}
		relative = filepath.ToSlash(relative)
		files = append(files, localFile{RelativePath: relative, SizeBytes: info.Size(), Markdown: strings.HasSuffix(strings.ToLower(relative), ".md")})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool {
		if (files[i].RelativePath == skillFileName) != (files[j].RelativePath == skillFileName) {
			return files[i].RelativePath == skillFileName
		}
		return strings.ToLower(files[i].RelativePath) < strings.ToLower(files[j].RelativePath)
	})
	return map[string]any{"directoryName": directoryName, "files": files, "truncated": truncated}, nil
}

func (s *Service) previewLocalSkillFile(directoryName, relativePath string) (string, error) {
	file, err := s.resolveLocalSkillFilePath(directoryName, relativePath)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(file)
	if err != nil {
		return "", err
	}
	if info.Size() > maxSkillBytes {
		return "", fmt.Errorf("文件过大，无法安全预览：%s", relativePath)
	}
	data, err := readBounded(file, maxSkillBytes)
	if err != nil {
		return "", err
	}
	sample := data
	if len(sample) > 8*1024 {
		sample = sample[:8*1024]
	}
	if strings.IndexByte(string(sample), 0) >= 0 {
		return "", fmt.Errorf("该文件不是可安全预览的文本文件")
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("文件不是有效的 UTF-8 文本：%s", relativePath)
	}
	return string(data), nil
}

func (s *Service) resolveLocalSkillFilePath(directoryName, relativePath string) (string, error) {
	directoryName, err := safeLocalSkillDirectoryName(directoryName)
	if err != nil {
		return "", err
	}
	native := filepath.FromSlash(relativePath)
	portable := filepath.ToSlash(native)
	segments := strings.Split(portable, "/")
	if portable == "" {
		return "", fmt.Errorf("Skill 文件路径不能为空")
	}
	if !filepath.IsLocal(native) {
		return "", fmt.Errorf("Skill 文件路径不安全")
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("Skill 文件路径不安全")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(home, ".agents", "skills")
	if err := ensureNoExistingSymlinks(root); err != nil {
		return "", err
	}
	directory := filepath.Join(root, directoryName)
	if !isDir(directory) || isSymlink(directory) || !isRegular(filepath.Join(directory, skillFileName)) {
		return "", fmt.Errorf("Skill 目录不存在或不是普通目录")
	}
	file := directory
	for _, segment := range segments {
		file = filepath.Join(file, segment)
		if isSymlink(file) {
			return "", fmt.Errorf("Skill 文件路径不能包含符号链接")
		}
	}
	if !within(directory, file) || !isRegular(file) {
		return "", fmt.Errorf("Skill 文件不存在或不是普通文件")
	}
	return file, nil
}

func managedSkillOwned(state extensionState, sourceID, name string) bool {
	for _, entry := range state.ManagedSkills {
		if entry.SourceID == sourceID && entry.Name == name {
			return true
		}
	}
	return false
}

func (s *Service) backupSkill(ctx context.Context, source, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	root := filepath.Join(s.paths.Codex, skillBackupsName)
	if err := ensureNoExistingSymlinks(root); err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	backup := filepath.Join(root, name+"-"+fmt.Sprintf("%d", time.Now().UnixMilli())+"-"+randomSuffix())
	if isSymlink(source) {
		target, err := os.Readlink(source)
		if err != nil {
			return "", err
		}
		if err := os.Symlink(target, backup); err != nil {
			return "", err
		}
		return backup, nil
	}
	if !isDir(source) {
		if err := copyOneWithContext(ctx, source, backup); err != nil {
			return "", err
		}
		return backup, nil
	}
	if err := walkCopyWithContext(ctx, source, backup, false); err != nil {
		_ = removeAny(backup)
		return "", err
	}
	return backup, nil
}

func copyBackup(source, target string) error {
	if exists(target) {
		return fmt.Errorf("恢复目标已存在：%s", target)
	}
	if isSymlink(source) {
		linkTarget, err := os.Readlink(source)
		if err != nil {
			return err
		}
		return os.Symlink(linkTarget, target)
	}
	if isDir(source) {
		return copyTree(source, target)
	}
	return copyOne(source, target)
}

func copySkillDirectory(source, target string) error {
	return copySkillDirectoryWithContext(context.Background(), source, target)
}

func walkCopyWithContext(ctx context.Context, source, target string, skipGit bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if exists(target) {
		return fmt.Errorf("目标已存在：%s", target)
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("不允许复制符号链接：%s", path)
		}
		if skipGit && entry.IsDir() && path != source && strings.EqualFold(entry.Name(), ".git") {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(target, rel)
		if !within(target, dest) {
			return fmt.Errorf("复制路径越界：%s", path)
		}
		if entry.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("不支持复制特殊文件：%s", path)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return copyOneWithContext(ctx, path, dest)
	})
}

func copyTree(source, target string) error {
	return walkCopyWithContext(context.Background(), source, target, false)
}

func copyOne(source, target string) error {
	return copyOneWithContext(context.Background(), source, target)
}

func copyOneWithContext(ctx context.Context, source, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("不允许复制符号链接：%s", source)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("不支持复制特殊文件：%s", source)
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, extensionContextReader{ctx: ctx, reader: in})
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(target)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(target)
		return closeErr
	}
	return nil
}

func validateSkillTree(root string) error {
	return validateSkillTreeWithContext(context.Background(), root)
}

func validateSkillTreeWithContext(ctx context.Context, root string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !isDir(root) || isSymlink(root) {
		return fmt.Errorf("Skill 目录不存在或不是普通目录：%s", root)
	}
	if !isRegular(filepath.Join(root, skillFileName)) {
		return fmt.Errorf("Skill 缺少 SKILL.md：%s", root)
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("Skill 不允许包含符号链接：%s", path)
		}
		if entry.IsDir() && path != root && strings.EqualFold(entry.Name(), ".git") {
			return filepath.SkipDir
		}
		return nil
	})
}

func validateNamedSkillTree(root, expectedName string) error {
	return validateNamedSkillTreeWithContext(context.Background(), root, expectedName)
}

func validateNamedSkillTreeWithContext(ctx context.Context, root, expectedName string) error {
	if !skillNamePattern.MatchString(expectedName) {
		return fmt.Errorf("Skill 名称不安全")
	}
	if err := validateSkillTreeWithContext(ctx, root); err != nil {
		return err
	}
	content, err := readExtensionText(filepath.Join(root, skillFileName), maxSkillBytes)
	if err != nil {
		return err
	}
	front := skillFrontMatter.FindSubmatch(content)
	if len(front) < 2 {
		return fmt.Errorf("Skill 缺少 YAML frontmatter：%s", root)
	}
	name := frontMatterValue(string(front[1]), "name")
	if name == "" {
		return fmt.Errorf("Skill 缺少 name：%s", root)
	}
	if name != expectedName {
		return fmt.Errorf("Skill 名称不匹配：%s", root)
	}
	return nil
}

func (s *Service) loadExtensionState() (extensionState, error) {
	path := filepath.Join(s.paths.Codex, extensionsStateName)
	if err := ensureNoExistingSymlinks(path); err != nil {
		return extensionState{}, err
	}
	if !exists(path) {
		return emptyExtensionState(), nil
	}
	if !isRegular(path) {
		return extensionState{}, fmt.Errorf("Codex 扩展状态不是普通文件")
	}
	data, err := readBounded(path, 16*1024*1024)
	if err != nil {
		return extensionState{}, err
	}
	if len(data) > 16*1024*1024 {
		return extensionState{}, fmt.Errorf("Codex 扩展状态文件过大")
	}
	state := emptyExtensionState()
	if err := json.Unmarshal(data, &state); err != nil {
		return extensionState{}, fmt.Errorf("Codex 扩展状态文件无法读取：%w", err)
	}
	state.normalize()
	return state, nil
}

func (s *Service) saveExtensionState(state extensionState) error {
	state.normalize()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.paths.Codex, extensionsStateName)
	if err := atomicWrite(path, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return nil
}

func emptyExtensionState() extensionState {
	return extensionState{Schema: 1, Marketplaces: map[string]MarketplaceSnapshot{}, SkillSources: map[string]SkillSourceSnapshot{}, ManagedPlugins: []managedPlugin{}, ManagedSkills: []managedSkill{}}
}
func (s *extensionState) normalize() {
	if s.Schema == 0 {
		s.Schema = 1
	}
	if s.Marketplaces == nil {
		s.Marketplaces = map[string]MarketplaceSnapshot{}
	}
	if s.SkillSources == nil {
		s.SkillSources = map[string]SkillSourceSnapshot{}
	}
	if s.ManagedPlugins == nil {
		s.ManagedPlugins = []managedPlugin{}
	}
	if s.ManagedSkills == nil {
		s.ManagedSkills = []managedSkill{}
	}
}

func findSkillSource(sources []domain.SkillSource, id string) (domain.SkillSource, bool) {
	for _, source := range sources {
		if source.ID == id {
			return source, true
		}
	}
	return domain.SkillSource{}, false
}
func filterSkillSources(values []domain.SkillSource, id string) []domain.SkillSource {
	out := values[:0]
	for _, value := range values {
		if value.ID != id {
			out = append(out, value)
		}
	}
	return out
}
func removeSkillSnapshot(values map[string]SkillSourceSnapshot, id string) map[string]SkillSourceSnapshot {
	delete(values, id)
	return values
}
func filterManagedSkills(values []managedSkill, sourceID, name string) []managedSkill {
	out := values[:0]
	for _, entry := range values {
		if entry.SourceID != sourceID || entry.Name != name {
			out = append(out, entry)
		}
	}
	return out
}

func filterManagedSkillsByName(values []managedSkill, name string) []managedSkill {
	out := values[:0]
	for _, entry := range values {
		if entry.Name != name {
			out = append(out, entry)
		}
	}
	return out
}

func safeChild(root, relative string) (string, error) {
	portable := strings.ReplaceAll(relative, `\`, "/")
	if portable == "" || strings.HasPrefix(portable, "/") || filepath.IsAbs(relative) {
		return "", fmt.Errorf("Skill 来源路径不安全")
	}
	if portable != "." {
		for _, segment := range strings.Split(portable, "/") {
			if segment == "" || segment == "." || segment == ".." {
				return "", fmt.Errorf("Skill 来源路径不安全")
			}
		}
	}
	base, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(base, filepath.FromSlash(portable)))
	if err != nil {
		return "", err
	}
	if !within(base, target) {
		return "", fmt.Errorf("Skill 路径超出仓库范围")
	}
	if err := ensureNoSymlinksBetween(base, target); err != nil {
		return "", err
	}
	return target, nil
}

func ensureNoSymlinks(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("Skill 缓存不允许符号链接：%s", path)
		}
		return nil
	})
}
func ensureNoSymlinksBetween(root, target string) error {
	if !within(root, target) {
		return fmt.Errorf("路径超出根目录")
	}
	if err := ensureNoExistingSymlinks(root); err != nil {
		return err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	current := root
	if isSymlink(current) {
		return fmt.Errorf("路径根不能是符号链接：%s", current)
	}
	for _, segment := range strings.Split(rel, string(os.PathSeparator)) {
		if segment == "." || segment == "" {
			continue
		}
		current = filepath.Join(current, segment)
		if isSymlink(current) {
			return fmt.Errorf("路径不能包含符号链接：%s", current)
		}
	}
	return nil
}

func removeWithin(root, target string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if !within(rootAbs, targetAbs) || samePath(rootAbs, targetAbs) {
		return fmt.Errorf("删除目标超出 SilverWing 缓存目录：%s", target)
	}
	if err := ensureSafeDeleteAncestors(rootAbs, targetAbs); err != nil {
		return err
	}
	if !exists(targetAbs) {
		return nil
	}
	return removeAny(targetAbs)
}
func removeSkillExact(root, target string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if filepath.Dir(targetAbs) != rootAbs {
		return fmt.Errorf("只能删除用户 Skill 根目录下的单个 Skill：%s", target)
	}
	if err := ensureSafeDeleteAncestors(rootAbs, targetAbs); err != nil {
		return err
	}
	return removeAny(targetAbs)
}

func ensureNoSymlinksBeforeLeaf(root, target string) error {
	if !within(root, target) {
		return fmt.Errorf("路径超出根目录")
	}
	if err := ensureNoExistingSymlinks(root); err != nil {
		return err
	}
	parent := filepath.Dir(target)
	if samePath(parent, root) {
		return nil
	}
	return ensureNoSymlinksBetween(root, parent)
}

func ensureNoExistingSymlinks(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(absolute)
	root := string(os.PathSeparator)
	if volume != "" {
		root = volume + string(os.PathSeparator)
	}
	current := root
	rel, err := filepath.Rel(root, absolute)
	if err != nil {
		return err
	}
	for _, segment := range strings.Split(rel, string(os.PathSeparator)) {
		if segment == "" || segment == "." {
			continue
		}
		current = filepath.Join(current, segment)
		if isSymlink(current) {
			return fmt.Errorf("路径不能包含符号链接：%s", current)
		}
	}
	return nil
}
func removeAny(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return os.Remove(path)
	}
	return os.RemoveAll(path)
}

func frontMatterValue(frontMatter, key string) string {
	for _, line := range strings.Split(frontMatter, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		before, value, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(before) == key {
			return strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	return ""
}
func readBounded(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("文件不是普通文件：%s", path)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("文件过大，无法安全读取：%s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("文件在读取期间发生改变：%s", path)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("文件过大，无法安全读取：%s", path)
	}
	return data, nil
}
func isDir(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}
func isRegular(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}
func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}
func exists(path string) bool { _, err := os.Lstat(path); return err == nil }
func within(root, child string) bool {
	rel, err := filepath.Rel(root, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}
func samePath(a, b string) bool {
	left, right := filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
func stringPtr(v string) *string { return &v }
func randomSuffix() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
func hashBytes(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
