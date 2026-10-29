package storage

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/productprofile"
)

const configShardSchema = 6
const configLockFile = "config.lock"
const backupPrefix = "silverwing-config-"
const shardStagePrefix = ".silverwing-shard-"

var shardNames = []string{
	"layout.json", "workspace.json",
	"tools.json", "git.json", "integrations.json", "appearance.json",
}

type UnsupportedConfigVersionError struct{ Actual string }

func (e *UnsupportedConfigVersionError) Error() string {
	if e.Actual == "" {
		return "配置 schema 不受支持：缺少 schema；当前分片 schema 为 6。silverwing 不读取或改写旧配置。"
	}
	return fmt.Sprintf("配置 schema 不受支持：%s；当前分片 schema 为 6。silverwing 不读取或改写旧配置。", e.Actual)
}

type DefaultTaskRootInitializationError struct {
	TaskRoot string
	Cause    error
}

func (e *DefaultTaskRootInitializationError) Error() string {
	return fmt.Sprintf("无法创建默认任务根目录：%s；请在设置中选择可写目录: %v", e.TaskRoot, e.Cause)
}
func (e *DefaultTaskRootInitializationError) Unwrap() error { return e.Cause }

type Backup struct {
	ID         string
	Path       string
	ModifiedAt time.Time
}

// ConfigFileSnapshot mirrors the source ConfigStore's read-only raw-file view.
// Content and ReadError are nil when absent; file contents are never parsed or
// repaired by this API.
type ConfigFileSnapshot struct {
	Path      string  `json:"path"`
	Exists    bool    `json:"exists"`
	Content   *string `json:"content"`
	ReadError *string `json:"readError"`
}

type ConfigStore struct {
	paths         Paths
	backupMu      sync.Mutex
	listedBackups map[string]listedConfigBackup
}

var configMutationLocks sync.Map

func NewConfigStore(paths Paths) *ConfigStore { return &ConfigStore{paths: paths} }
func (s *ConfigStore) Paths() Paths           { return s.paths }

func (s *ConfigStore) Exists() bool {
	for _, name := range shardNames {
		info, err := os.Lstat(filepath.Join(s.paths.Config, name))
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}

// LoadForPreview shares compatible settings reading with Load, but an empty
// home is compared with in-memory defaults and never initialized on disk.
func (s *ConfigStore) LoadForPreview(ctx context.Context) (domain.AppConfig, error) {
	release, err := s.acquireReadLock(ctx)
	if err != nil {
		return domain.AppConfig{}, err
	}
	defer release()
	present := s.hasConfigurationData()
	if err := ctx.Err(); err != nil {
		return domain.AppConfig{}, err
	}
	if !present {
		return domain.DefaultAppConfig(), nil
	}
	return s.readCurrentContext(ctx)
}

func (s *ConfigStore) Load() (domain.AppConfig, error) {
	release, err := s.acquireMutationLock()
	if err != nil {
		return domain.AppConfig{}, err
	}
	defer release()
	if s.hasConfigurationData() {
		return s.readCurrent()
	}
	config := domain.DefaultAppConfig()
	taskRoot := s.paths.Tasks
	if err := os.MkdirAll(taskRoot, 0o755); err != nil {
		if writeErr := s.writeShards(shardsFor(domain.DefaultAppConfig()), shardNames); writeErr != nil {
			return domain.AppConfig{}, writeErr
		}
		return domain.AppConfig{}, &DefaultTaskRootInitializationError{TaskRoot: taskRoot, Cause: err}
	}
	config.TaskRoot = &taskRoot
	config.RequirementMaterialsRoot = defaultProfileMaterialsRoot(s.paths.Home)
	if err := s.writeShards(shardsFor(config), shardNames); err != nil {
		return domain.AppConfig{}, err
	}
	return config, nil
}

func (s *ConfigStore) Save(config domain.AppConfig) error {
	release, err := s.acquireMutationLock()
	if err != nil {
		return err
	}
	defer release()
	return s.saveUnlocked(config)
}

func (s *ConfigStore) Update(transform func(domain.AppConfig) (domain.AppConfig, error)) (domain.AppConfig, error) {
	if transform == nil {
		return domain.AppConfig{}, fmt.Errorf("configuration transform is nil")
	}
	release, err := s.acquireMutationLock()
	if err != nil {
		return domain.AppConfig{}, err
	}
	defer release()
	current, err := s.loadUnlocked()
	if err != nil {
		return domain.AppConfig{}, err
	}
	updated, err := transform(current)
	if err != nil {
		return domain.AppConfig{}, err
	}
	if err := s.saveUnlocked(updated); err != nil {
		return domain.AppConfig{}, err
	}
	return s.loadUnlocked()
}

func (s *ConfigStore) loadUnlocked() (domain.AppConfig, error) {
	if !s.hasConfigurationData() {
		config := domain.DefaultAppConfig()
		taskRoot := s.paths.Tasks
		if err := os.MkdirAll(taskRoot, 0o755); err != nil {
			return domain.AppConfig{}, &DefaultTaskRootInitializationError{TaskRoot: taskRoot, Cause: err}
		}
		config.TaskRoot = &taskRoot
		config.RequirementMaterialsRoot = defaultProfileMaterialsRoot(s.paths.Home)
		if err := s.writeShards(shardsFor(config), shardNames); err != nil {
			return domain.AppConfig{}, err
		}
		return config, nil
	}
	return s.readCurrent()
}

func (s *ConfigStore) saveUnlocked(config domain.AppConfig) error {
	config = normalizeConfigCollections(config)
	if config.SchemaVersion != domain.CurrentAppConfigSchemaVersion {
		return &UnsupportedConfigVersionError{Actual: config.SchemaVersion}
	}
	if err := config.Validate(); err != nil {
		return err
	}
	target := shardsFor(config)
	encoded := make(map[string][]byte, len(shardNames))
	for _, name := range shardNames {
		data, err := encodeShard(name, target)
		if err != nil {
			return err
		}
		encoded[name] = data
	}
	if _, err := decodeShards(encoded); err != nil {
		return fmt.Errorf("配置序列化结果无法按当前 schema 读取：%w", err)
	}
	if !s.hasConfigurationData() {
		return s.writeShardFiles(encoded, shardNames)
	}
	currentFiles, err := s.readShardFiles()
	if err != nil {
		return err
	}
	current, err := decodeShards(currentFiles)
	if err != nil {
		return err
	}
	changed := changedShardNames(current.Shards, target)
	if len(changed) == 0 {
		return nil
	}
	// Merge against the latest files while holding the existing mutation lock.
	// Validate every result before backing up or replacing any shard.
	for _, name := range changed {
		value, err := configShardValue(name, target)
		if err != nil {
			return err
		}
		encoded[name], err = mergeConfigJSON(currentFiles[name], encoded[name], reflect.TypeOf(value), name)
		if err != nil {
			return err
		}
	}
	if _, err := decodeShards(encoded); err != nil {
		return fmt.Errorf("兼容配置合并校验失败：%w", err)
	}
	if err := s.createBackup(currentFiles); err != nil {
		return err
	}
	return s.writeShardFiles(encoded, changed)
}

func normalizeConfigCollections(config domain.AppConfig) domain.AppConfig {
	config.Groups = cloneSlice(config.Groups)
	config.DefaultWorkspaceToolIDs = cloneSlice(config.DefaultWorkspaceToolIDs)
	config.MeegleProjects = cloneSlice(config.MeegleProjects)
	config.CommandProxyTargets = cloneSlice(config.CommandProxyTargets)
	config.CodexPluginMarketplaceSources = cloneSlice(config.CodexPluginMarketplaceSources)
	config.SkillSources = cloneSlice(config.SkillSources)
	if config.MaterialsGitSync != nil {
		copy := *config.MaterialsGitSync
		config.MaterialsGitSync = &copy
	}
	return config
}

func cloneSlice[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	cloned := make([]T, len(values))
	copy(cloned, values)
	return cloned
}

func (s *ConfigStore) Backups() ([]Backup, error) {
	return s.BackupsContext(context.Background())
}

func (s *ConfigStore) BackupsContext(ctx context.Context) ([]Backup, error) {
	return s.listConfigBackups(ctx)
}

// PreviewImport strictly validates a configuration ZIP without changing the
// current configuration or creating a backup. The caller can show the decoded
// configuration before an explicit ImportFrom call.
func (s *ConfigStore) PreviewImport(source string) (domain.AppConfig, error) {
	return s.PreviewImportContext(context.Background(), source)
}

func (s *ConfigStore) PreviewImportContext(ctx context.Context, source string) (domain.AppConfig, error) {
	if err := ctx.Err(); err != nil {
		return domain.AppConfig{}, err
	}
	normalized, err := filepath.Abs(source)
	if err != nil {
		return domain.AppConfig{}, err
	}
	if !strings.EqualFold(filepath.Ext(normalized), ".zip") {
		return domain.AppConfig{}, fmt.Errorf("导入配置必须是 ZIP 文件")
	}
	decoded, err := readArchiveContext(ctx, normalized)
	if err != nil {
		return domain.AppConfig{}, err
	}
	return decoded.Config, nil
}

// PreviewRestore applies the same directory and strict archive validation as
// Restore, but performs no mutation and does not create a backup.
func (s *ConfigStore) PreviewRestore(backup string) (domain.AppConfig, error) {
	return s.PreviewRestoreContext(context.Background(), backup)
}

func (s *ConfigStore) PreviewRestoreContext(ctx context.Context, backup string) (domain.AppConfig, error) {
	if err := ctx.Err(); err != nil {
		return domain.AppConfig{}, err
	}
	normalized, err := filepath.Abs(backup)
	if err != nil {
		return domain.AppConfig{}, err
	}
	backups, err := filepath.Abs(s.paths.Backups)
	if err != nil {
		return domain.AppConfig{}, err
	}
	if filepath.Dir(normalized) != backups || !strings.HasPrefix(filepath.Base(normalized), backupPrefix) || !strings.HasSuffix(filepath.Base(normalized), ".zip") {
		return domain.AppConfig{}, fmt.Errorf("只能恢复 silverwing 配置备份目录中的 ZIP 文件")
	}
	decoded, err := readArchiveContext(ctx, normalized)
	if err != nil {
		return domain.AppConfig{}, err
	}
	return decoded.Config, nil
}

func (s *ConfigStore) Restore(backup string) (domain.AppConfig, error) {
	normalized, err := filepath.Abs(backup)
	if err != nil {
		return domain.AppConfig{}, err
	}
	backups, err := filepath.Abs(s.paths.Backups)
	if err != nil {
		return domain.AppConfig{}, err
	}
	if filepath.Dir(normalized) != backups || !strings.HasPrefix(filepath.Base(normalized), backupPrefix) || !strings.HasSuffix(filepath.Base(normalized), ".zip") {
		return domain.AppConfig{}, fmt.Errorf("只能恢复 silverwing 配置备份目录中的 ZIP 文件")
	}
	release, err := s.acquireMutationLock()
	if err != nil {
		return domain.AppConfig{}, err
	}
	defer release()
	imported, err := readArchive(normalized)
	if err != nil {
		return domain.AppConfig{}, err
	}
	if err := s.replaceCurrentUnlocked(imported); err != nil {
		return domain.AppConfig{}, err
	}
	return s.readCurrent()
}

func (s *ConfigStore) ExportTo(target string) (string, error) {
	normalized, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(filepath.Ext(normalized), ".zip") {
		return "", fmt.Errorf("导出目标必须是 ZIP 文件")
	}
	release, err := s.acquireMutationLock()
	if err != nil {
		return "", err
	}
	files, readErr := s.readShardFiles()
	if readErr == nil {
		_, readErr = decodeShards(files)
	}
	release()
	if readErr != nil {
		return "", readErr
	}
	if err := writeZip(normalized, files); err != nil {
		return "", err
	}
	return normalized, nil
}

func (s *ConfigStore) ImportFrom(source string) (domain.AppConfig, error) {
	normalized, err := filepath.Abs(source)
	if err != nil {
		return domain.AppConfig{}, err
	}
	if !strings.EqualFold(filepath.Ext(normalized), ".zip") {
		return domain.AppConfig{}, fmt.Errorf("导入配置必须是 ZIP 文件")
	}
	release, err := s.acquireMutationLock()
	if err != nil {
		return domain.AppConfig{}, err
	}
	defer release()
	imported, err := readArchive(normalized)
	if err != nil {
		return domain.AppConfig{}, err
	}
	if err := s.replaceCurrentUnlocked(imported); err != nil {
		return domain.AppConfig{}, err
	}
	return s.readCurrent()
}

func (s *ConfigStore) replaceCurrentUnlocked(imported decodedConfig) error {
	files, err := importedConfigFiles(imported)
	if err != nil {
		return err
	}
	if !s.hasConfigurationData() {
		return s.writeShardFiles(files, shardNames)
	}
	currentFiles, err := s.readShardFiles()
	if err != nil {
		return err
	}
	changed := shardNames
	if current, decodeErr := decodeShards(currentFiles); decodeErr == nil {
		changed, err = changedImportedShards(current, imported)
		if err != nil {
			return err
		}
	}
	if len(changed) == 0 {
		return nil
	}
	if err := s.createBackup(currentFiles); err != nil {
		return err
	}
	return s.writeShardFiles(files, changed)
}

func (s *ConfigStore) acquireMutationLock() (func(), error) {
	releaseProcess, err := s.acquireProcessLock(context.Background())
	if err != nil {
		return nil, err
	}
	releaseFile, err := AcquireLock(filepath.Join(s.paths.Locks, configLockFile))
	if err != nil {
		releaseProcess()
		return nil, err
	}
	var done sync.Once
	return func() { done.Do(func() { releaseFile(); releaseProcess() }) }, nil
}

// Readers share the writer gate so a multi-shard commit is observed whole.
// Preview/raw reads never initialize an empty home or create a lock file.
func (s *ConfigStore) acquireReadLock(ctx context.Context) (func(), error) {
	releaseProcess, err := s.acquireProcessLock(ctx)
	if err != nil {
		return nil, err
	}
	lockPath := filepath.Join(s.paths.Locks, configLockFile)
	if _, err := os.Stat(lockPath); errors.Is(err, os.ErrNotExist) {
		return releaseProcess, nil
	} else if err != nil {
		releaseProcess()
		return nil, err
	}
	releaseFile, err := AcquireLock(lockPath)
	if err != nil {
		releaseProcess()
		return nil, err
	}
	var done sync.Once
	return func() { done.Do(func() { releaseFile(); releaseProcess() }) }, nil
}

func (s *ConfigStore) acquireProcessLock(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key, err := filepath.Abs(s.paths.Config)
	if err != nil {
		return nil, err
	}
	key = strings.ToLower(filepath.Clean(key))
	value, _ := configMutationLocks.LoadOrStore(key, make(chan struct{}, 1))
	gate := value.(chan struct{})
	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	var done sync.Once
	return func() { done.Do(func() { <-gate }) }, nil
}

func (s *ConfigStore) hasConfigurationData() bool {
	info, err := os.Stat(s.paths.Config)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		return true
	}
	if !info.IsDir() {
		return true
	}
	entries, err := os.ReadDir(s.paths.Config)
	if err != nil {
		return true
	}
	for _, entry := range entries {
		if !isShardStagingFile(entry.Name()) {
			return true
		}
	}
	return false
}

func (s *ConfigStore) readCurrent() (domain.AppConfig, error) {
	return s.readCurrentContext(context.Background())
}

func (s *ConfigStore) readCurrentContext(ctx context.Context) (domain.AppConfig, error) {
	files, err := s.readShardFilesContext(ctx)
	if err != nil {
		return domain.AppConfig{}, err
	}
	decoded, err := decodeShardsContext(ctx, files)
	if err != nil {
		return domain.AppConfig{}, err
	}
	return decoded.Config, nil
}

func (s *ConfigStore) readShardFiles() (map[string][]byte, error) {
	return s.readShardFilesContext(context.Background())
}

func (s *ConfigStore) readShardFilesContext(ctx context.Context) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Stat(s.paths.Config)
	if cancelled := ctx.Err(); cancelled != nil {
		return nil, cancelled
	}
	if err != nil {
		return nil, fmt.Errorf("配置目录不存在或无法读取：%w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("配置目录不是目录：%s", s.paths.Config)
	}
	entries, err := os.ReadDir(s.paths.Config)
	if cancelled := ctx.Err(); cancelled != nil {
		return nil, cancelled
	}
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, name := range shardNames {
		known[name] = true
	}
	present := map[string]bool{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if isShardStagingFile(entry.Name()) {
			continue
		}
		if !known[entry.Name()] {
			return nil, fmt.Errorf("配置目录包含未知文件：%s", entry.Name())
		}
		fileInfo, statErr := os.Lstat(filepath.Join(s.paths.Config, entry.Name()))
		if cancelled := ctx.Err(); cancelled != nil {
			return nil, cancelled
		}
		if statErr != nil {
			return nil, statErr
		}
		if !fileInfo.Mode().IsRegular() {
			return nil, fmt.Errorf("配置目录只能包含配置分片文件")
		}
		present[entry.Name()] = true
	}
	out := map[string][]byte{}
	for _, name := range shardNames {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !present[name] {
			return nil, fmt.Errorf("配置分片缺失：%s", name)
		}
		data, err := readConfigurationFileContext(ctx, filepath.Join(s.paths.Config, name))
		if err != nil {
			return nil, err
		}
		out[name] = data
	}
	return out, nil
}

func decodeShards(files map[string][]byte) (decodedConfig, error) {
	return decodeShardsContext(context.Background(), files)
}

func decodeShardsContext(ctx context.Context, files map[string][]byte) (decodedConfig, error) {
	defaults := shardsFor(domain.DefaultAppConfig())
	var shards configShards
	for _, name := range shardNames {
		if err := ctx.Err(); err != nil {
			return decodedConfig{}, err
		}
		var target, baseline any
		switch name {
		case "layout.json":
			target, baseline = &shards.Layout, defaults.Layout
		case "workspace.json":
			target, baseline = &shards.Workspace, defaults.Workspace
		case "tools.json":
			target, baseline = &shards.Tools, defaults.Tools
		case "git.json":
			target, baseline = &shards.Git, defaults.Git
		case "integrations.json":
			target, baseline = &shards.Integrations, defaults.Integrations
		case "appearance.json":
			target, baseline = &shards.Appearance, defaults.Appearance
		}
		required := []string{}
		if name == "layout.json" {
			required = append(required, "edition")
		}
		if err := decodeConfigShard(files[name], target, baseline, name, required...); err != nil {
			return decodedConfig{}, err
		}
	}
	if !productprofile.AcceptsEdition(shards.Layout.Edition) {
		return decodedConfig{}, fmt.Errorf("%s", productprofile.ConfigurationEditionError())
	}
	config := domain.DefaultAppConfig()
	config.Edition = shards.Layout.Edition
	config.TaskRoot = shards.Workspace.TaskRoot
	config.Groups = []domain.GroupConfig{}
	for _, group := range shards.Layout.Groups {
		config.Groups = append(config.Groups, domain.GroupConfig{ID: group.ID, Name: group.Name})
	}
	config.TerminalExecutable = shards.Tools.TerminalExecutable
	config.DefaultWorkspaceToolIDs = shards.Tools.DefaultWorkspaceToolIDs
	config.GitExecutablePath = shards.Git.GitExecutablePath
	var rawIntegration map[string]json.RawMessage
	if err := json.Unmarshal(files["integrations.json"], &rawIntegration); err != nil {
		return decodedConfig{}, err
	}
	if _, present := rawIntegration["commandProxyTargets"]; !present {
		shards.Integrations.CommandProxyTargets = domain.DefaultCommandProxyTargets(shards.Integrations.CommandProxyURL)
	}
	data, err := json.Marshal(shards.Integrations)
	if err != nil {
		return decodedConfig{}, err
	}
	var integrationFields map[string]json.RawMessage
	if err = json.Unmarshal(data, &integrationFields); err != nil {
		return decodedConfig{}, err
	}
	delete(integrationFields, "schema")
	full, err := json.Marshal(config)
	if err != nil {
		return decodedConfig{}, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(full, &fields); err != nil {
		return decodedConfig{}, err
	}
	for key, value := range integrationFields {
		fields[key] = value
	}
	full, err = json.Marshal(fields)
	if err != nil {
		return decodedConfig{}, err
	}
	if err = json.Unmarshal(full, &config); err != nil {
		return decodedConfig{}, err
	}
	config.Theme = shards.Appearance.Theme
	config.ToolVisibility = shards.Appearance.ToolVisibility
	config.SidebarVisibility = shards.Appearance.SidebarVisibility
	if err := config.Validate(); err != nil {
		return decodedConfig{}, fmt.Errorf("配置校验失败：%w", err)
	}
	return decodedConfig{Config: config, Shards: shards, Files: files}, nil
}

func decodeConfigShard(data []byte, target any, defaults any, name string, required ...string) error {
	if len(data) == 0 {
		return fmt.Errorf("%s 内容为空", name)
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(data, &meta); err != nil {
		return fmt.Errorf("读取 %s: %w", name, err)
	}
	raw, ok := meta["schema"]
	if !ok {
		return &UnsupportedConfigVersionError{}
	}
	var version int
	if err := json.Unmarshal(raw, &version); err != nil {
		return &UnsupportedConfigVersionError{Actual: string(raw)}
	}
	if version != configShardSchema {
		return &UnsupportedConfigVersionError{Actual: fmt.Sprint(version)}
	}
	targetValue := reflect.ValueOf(target)
	defaultsValue := reflect.ValueOf(defaults)
	if targetValue.Kind() != reflect.Pointer || targetValue.IsNil() || defaultsValue.Type() != targetValue.Elem().Type() {
		return fmt.Errorf("%s default type does not match decode target", name)
	}
	targetValue.Elem().Set(defaultsValue)
	known, err := knownConfigJSON(data, targetValue.Elem().Type())
	if err != nil {
		return fmt.Errorf("读取 %s: %w", name, err)
	}
	if err := domain.DecodeStrict(known, target, required...); err != nil {
		return fmt.Errorf("读取 %s: %w", name, err)
	}
	return nil
}

func shardsFor(config domain.AppConfig) configShards {
	groups := []groupLayoutShard{}
	for _, g := range config.Groups {
		groups = append(groups, groupLayoutShard{ID: g.ID, Name: g.Name})
	}
	return configShards{
		Layout:       layoutShard{Edition: productprofile.Edition, Schema: configShardSchema, Groups: groups},
		Workspace:    workspaceShard{Schema: configShardSchema, TaskRoot: config.TaskRoot},
		Tools:        toolsShard{Schema: configShardSchema, TerminalExecutable: config.TerminalExecutable, DefaultWorkspaceToolIDs: config.DefaultWorkspaceToolIDs},
		Git:          gitShard{Schema: configShardSchema, GitExecutablePath: config.GitExecutablePath},
		Integrations: integrationsShard{Schema: configShardSchema, AIRequirementNamingEnabled: config.AIRequirementNamingEnabled, AIRequirementNamingPrewarmEnabled: config.AIRequirementNamingPrewarmEnabled, AIRequirementNamingModel: config.AIRequirementNamingModel, MeegleProjects: config.MeegleProjects, MeegleDefaultSprintProjectKey: config.MeegleDefaultSprintProjectKey, MeegleExecutablePath: config.MeegleExecutablePath, CodexExecutablePath: config.CodexExecutablePath, CommandProxyURL: config.CommandProxyURL, CommandProxyNoProxy: config.CommandProxyNoProxy, CommandProxyUsername: config.CommandProxyUsername, CommandProxyPassword: config.CommandProxyPassword, CommandProxyTargets: config.CommandProxyTargets, RequirementMaterialsRoot: config.RequirementMaterialsRoot, RequirementMaterialsSubdirectory: config.RequirementMaterialsSubdirectory, MaterialsGitSync: config.MaterialsGitSync, CodexPluginMarketplaceSources: config.CodexPluginMarketplaceSources, SkillSources: config.SkillSources},
		Appearance:   appearanceShard{Schema: configShardSchema, Theme: config.Theme, ToolVisibility: config.ToolVisibility, SidebarVisibility: config.SidebarVisibility},
	}
}

func changedShardNames(current, target configShards) []string {
	names := []string{}
	if !reflect.DeepEqual(current.Layout, target.Layout) {
		names = append(names, "layout.json")
	}
	if !reflect.DeepEqual(current.Workspace, target.Workspace) {
		names = append(names, "workspace.json")
	}

	if !reflect.DeepEqual(current.Tools, target.Tools) {
		names = append(names, "tools.json")
	}
	if !reflect.DeepEqual(current.Git, target.Git) {
		names = append(names, "git.json")
	}
	if !equalIntegrationsShard(current.Integrations, target.Integrations) {
		names = append(names, "integrations.json")
	}
	if !reflect.DeepEqual(current.Appearance, target.Appearance) {
		names = append(names, "appearance.json")
	}
	return names
}

func equalIntegrationsShard(left, right integrationsShard) bool {
	leftTargets, rightTargets := left.CommandProxyTargets, right.CommandProxyTargets
	left.CommandProxyTargets, right.CommandProxyTargets = nil, nil
	return reflect.DeepEqual(left, right) && domain.EqualCommandProxyTargetSets(leftTargets, rightTargets)
}

func (s *ConfigStore) writeShards(shards configShards, names []string) error {
	files := make(map[string][]byte, len(names))
	for _, name := range names {
		data, err := encodeShard(name, shards)
		if err != nil {
			return err
		}
		files[name] = data
	}
	return s.writeShardFiles(files, names)
}

// Typed defaults and compatible updates share the same staging and rollback
// transaction; callers supply the final bytes, including retained extensions.
func (s *ConfigStore) writeShardFiles(files map[string][]byte, names []string) error {
	if err := os.MkdirAll(s.paths.Config, 0o755); err != nil {
		return err
	}
	selected := uniqueNames(names)
	originals := map[string][]byte{}
	for _, name := range selected {
		data, err := os.ReadFile(filepath.Join(s.paths.Config, name))
		if err == nil {
			originals[name] = data
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	staged := map[string]string{}
	cleanup := func() {
		for _, p := range staged {
			_ = os.Remove(p)
		}
	}
	for _, name := range selected {
		data, ok := files[name]
		if !ok {
			cleanup()
			return fmt.Errorf("缺少待保存配置分片：%s", name)
		}
		file, err := os.CreateTemp(s.paths.Config, shardStagePrefix+strings.TrimSuffix(name, ".json")+"-*.json.tmp")
		if err != nil {
			cleanup()
			return err
		}
		if _, err = file.Write(data); err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(file.Name())
			cleanup()
			return err
		}
		staged[name] = file.Name()
	}
	replaced := []string{}
	for _, name := range selected {
		replaced = append(replaced, name)
		if err := replaceFile(staged[name], filepath.Join(s.paths.Config, name)); err != nil {
			rollbackErrors := []error{}
			for i := len(replaced) - 1; i >= 0; i-- {
				n := replaced[i]
				target := filepath.Join(s.paths.Config, n)
				old, exists := originals[n]
				if !exists {
					if removeErr := os.Remove(target); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
						rollbackErrors = append(rollbackErrors, fmt.Errorf("remove newly created shard %s: %w", n, removeErr))
					}
					continue
				}
				restore, createErr := os.CreateTemp(s.paths.Config, shardStagePrefix+"rollback-*.json.tmp")
				if createErr != nil {
					rollbackErrors = append(rollbackErrors, fmt.Errorf("create rollback staging file for %s: %w", n, createErr))
					continue
				}
				_, writeErr := restore.Write(old)
				if writeErr == nil {
					writeErr = restore.Sync()
				}
				if closeErr := restore.Close(); writeErr == nil {
					writeErr = closeErr
				}
				if writeErr == nil {
					writeErr = replaceFile(restore.Name(), target)
				}
				if writeErr != nil {
					_ = os.Remove(restore.Name())
					rollbackErrors = append(rollbackErrors, fmt.Errorf("restore shard %s: %w", n, writeErr))
					continue
				}
				actual, readErr := os.ReadFile(target)
				if readErr != nil {
					rollbackErrors = append(rollbackErrors, fmt.Errorf("verify restored shard %s: %w", n, readErr))
				} else if !bytes.Equal(actual, old) {
					rollbackErrors = append(rollbackErrors, fmt.Errorf("verify restored shard %s: content differs from pre-transaction bytes", n))
				}
			}
			cleanup()
			if len(rollbackErrors) > 0 {
				return errors.Join(append([]error{fmt.Errorf("replace config shard %s: %w", name, err)}, rollbackErrors...)...)
			}
			return err
		}
	}
	cleanup()
	return nil
}

func (s *ConfigStore) createBackup(files map[string][]byte) error {
	if err := os.MkdirAll(s.paths.Backups, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.paths.Backups, backupPrefix+"*.zip.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	target := strings.TrimSuffix(tmpPath, ".tmp")
	if err := writeZipUsingTemp(target, files, tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	backups, err := s.Backups()
	if err != nil {
		return err
	}
	if len(backups) > 10 {
		for _, old := range backups[10:] {
			if err := os.Remove(old.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func writeZip(target string, files map[string][]byte) error {
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(parent, ".config-*.zip.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	if err := writeZipUsingTemp(target, files, tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
func writeZipUsingTemp(target string, files map[string][]byte, tmpPath string) error {
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	for _, name := range shardNames {
		data, ok := files[name]
		if !ok {
			_ = zw.Close()
			_ = f.Close()
			return fmt.Errorf("缺少配置分片：%s", name)
		}
		w, e := zw.Create(name)
		if e != nil {
			_ = zw.Close()
			_ = f.Close()
			return e
		}
		if _, e = w.Write(data); e != nil {
			_ = zw.Close()
			_ = f.Close()
			return e
		}
	}
	if err := zw.Close(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return replaceFile(tmpPath, target)
}

func readArchive(filename string) (decodedConfig, error) {
	return readArchiveContext(context.Background(), filename)
}

func readArchiveContext(ctx context.Context, filename string) (decodedConfig, error) {
	if err := ctx.Err(); err != nil {
		return decodedConfig{}, err
	}
	info, err := os.Stat(filename)
	if cancelled := ctx.Err(); cancelled != nil {
		return decodedConfig{}, cancelled
	}
	if err != nil {
		return decodedConfig{}, fmt.Errorf("配置 ZIP 不存在或无法读取：%w", err)
	}
	if !info.Mode().IsRegular() {
		return decodedConfig{}, fmt.Errorf("配置 ZIP 不是普通文件：%s", filename)
	}
	file, err := openConfigurationContext(ctx, filename)
	if err != nil {
		return decodedConfig{}, err
	}
	defer file.Close()
	info, err = file.Stat()
	if cancelled := ctx.Err(); cancelled != nil {
		return decodedConfig{}, cancelled
	}
	if err != nil {
		return decodedConfig{}, err
	}
	reader, err := zip.NewReader(configContextReaderAt{ctx: ctx, reader: file}, info.Size())
	if cancelled := ctx.Err(); cancelled != nil {
		return decodedConfig{}, cancelled
	}
	if err != nil {
		return decodedConfig{}, err
	}
	files := map[string][]byte{}
	allowed := map[string]bool{}
	for _, name := range shardNames {
		allowed[name] = true
	}
	for _, entry := range reader.File {
		if err := ctx.Err(); err != nil {
			return decodedConfig{}, err
		}
		name := entry.Name
		if !safeZipEntry(name) {
			return decodedConfig{}, fmt.Errorf("配置 ZIP 包含不安全条目：%s", name)
		}
		if entry.FileInfo().IsDir() {
			return decodedConfig{}, fmt.Errorf("配置 ZIP 不允许目录条目：%s", name)
		}
		if !entry.Mode().IsRegular() {
			return decodedConfig{}, fmt.Errorf("配置 ZIP 只能包含普通文件：%s", name)
		}
		if !allowed[name] {
			return decodedConfig{}, fmt.Errorf("配置 ZIP 包含未知条目：%s", name)
		}
		if _, exists := files[name]; exists {
			return decodedConfig{}, fmt.Errorf("配置 ZIP 包含重复条目：%s", name)
		}
		rc, e := entry.Open()
		if e != nil {
			return decodedConfig{}, e
		}
		data, e := io.ReadAll(configContextReader{ctx: ctx, reader: rc})
		closeErr := rc.Close()
		if cancelled := ctx.Err(); cancelled != nil {
			return decodedConfig{}, cancelled
		}
		if e == nil {
			e = closeErr
		}
		if e != nil {
			return decodedConfig{}, e
		}
		files[name] = data
	}
	for _, name := range shardNames {
		if err := ctx.Err(); err != nil {
			return decodedConfig{}, err
		}
		if _, ok := files[name]; !ok {
			return decodedConfig{}, fmt.Errorf("配置 ZIP 缺少条目：%s", name)
		}
	}
	return decodeShardsContext(ctx, files)
}

func encodeShard(name string, shards configShards) ([]byte, error) {
	value, err := configShardValue(name, shards)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(value, "", "  ")
}

func configShardValue(name string, shards configShards) (any, error) {
	var value any
	switch name {
	case "layout.json":
		value = shards.Layout
	case "workspace.json":
		value = shards.Workspace
	case "tools.json":
		value = shards.Tools
	case "git.json":
		value = shards.Git
	case "integrations.json":
		value = shards.Integrations
	case "appearance.json":
		value = shards.Appearance
	default:
		return nil, fmt.Errorf("未知配置分片：%s", name)
	}
	return value, nil
}

func safeZipEntry(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\\\") || strings.Contains(name, "\\\\") || strings.Contains(name, ":") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return !filepath.IsAbs(name)
}
func isShardStagingFile(name string) bool {
	return strings.HasPrefix(name, shardStagePrefix) && strings.HasSuffix(name, ".json.tmp")
}
func uniqueNames(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

type decodedConfig struct {
	Config domain.AppConfig
	Shards configShards
	Files  map[string][]byte
}
type configShards struct {
	Layout       layoutShard
	Workspace    workspaceShard
	Tools        toolsShard
	Git          gitShard
	Integrations integrationsShard
	Appearance   appearanceShard
}
type layoutShard struct {
	Edition string             `json:"edition"`
	Schema  int                "json:\"schema\""
	Groups  []groupLayoutShard "json:\"groups\""
}
type groupLayoutShard struct {
	ID   string "json:\"id\""
	Name string "json:\"name\""
}
type workspaceShard struct {
	Schema   int     "json:\"schema\""
	TaskRoot *string "json:\"taskRoot\""
}

type toolsShard struct {
	Schema             int     "json:\"schema\""
	TerminalExecutable *string "json:\"terminalExecutable\""

	DefaultWorkspaceToolIDs []string "json:\"defaultWorkspaceToolIds\""
}
type gitShard struct {
	Schema int "json:\"schema\""

	GitExecutablePath *string "json:\"gitExecutablePath\""
}
type integrationsShard struct {
	Schema                            int                          "json:\"schema\""
	AIRequirementNamingEnabled        bool                         "json:\"aiRequirementNamingEnabled\""
	AIRequirementNamingPrewarmEnabled bool                         "json:\"aiRequirementNamingPrewarmEnabled\""
	AIRequirementNamingModel          string                       "json:\"aiRequirementNamingModel\""
	MeegleProjects                    []domain.MeegleProjectConfig "json:\"meegleProjects\""
	MeegleDefaultSprintProjectKey     *string                      "json:\"meegleDefaultSprintProjectKey\""
	MeegleExecutablePath              *string                      "json:\"meegleExecutablePath\""
	CodexExecutablePath               *string                      "json:\"codexExecutablePath\""
	CommandProxyURL                   *string                      "json:\"commandProxyUrl\""
	CommandProxyNoProxy               *string                      "json:\"commandProxyNoProxy\""
	CommandProxyUsername              *string                      "json:\"commandProxyUsername\""
	CommandProxyPassword              *string                      "json:\"commandProxyPassword\""
	CommandProxyTargets               []domain.CommandProxyTarget  "json:\"commandProxyTargets\""

	RequirementMaterialsRoot         *string                               "json:\"requirementMaterialsRoot\""
	RequirementMaterialsSubdirectory *string                               "json:\"requirementMaterialsSubdirectory\""
	MaterialsGitSync                 *domain.MaterialsGitSyncSettings      `json:"materialsGitSync"`
	CodexPluginMarketplaceSources    []domain.CodexPluginMarketplaceSource "json:\"codexPluginMarketplaceSources\""
	SkillSources                     []domain.SkillSource                  "json:\"skillSources\""
}
type appearanceShard struct {
	Schema            int                              "json:\"schema\""
	Theme             domain.ThemePreference           "json:\"theme\""
	ToolVisibility    domain.ToolVisibilitySettings    "json:\"toolVisibility\""
	SidebarVisibility domain.SidebarVisibilitySettings "json:\"sidebarVisibility\""
}

// Decode rows from an empty value so a sparse row cannot inherit the previous
// default row ID through encoding/json's slice element reuse.
func (g *groupLayoutShard) UnmarshalJSON(data []byte) error {
	type plain groupLayoutShard
	var next plain
	if err := domain.DecodeStrict(data, &next, "id", "name"); err != nil {
		return err
	}
	*g = groupLayoutShard(next)
	return nil
}
