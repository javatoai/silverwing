package application

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

type OptionalCLIPhase string

const (
	OptionalCLIDisabled    OptionalCLIPhase = "disabled"
	OptionalCLIProbing     OptionalCLIPhase = "probing"
	OptionalCLIAvailable   OptionalCLIPhase = "available"
	OptionalCLIUnavailable OptionalCLIPhase = "unavailable"
)

type OptionalCLIStatus struct {
	Tool, ConfiguredPath, Command, Source, Version, Error string
	Phase                                                 OptionalCLIPhase
}

func (s OptionalCLIStatus) Ready() bool { return s.Phase == OptionalCLIAvailable }

func (s OptionalCLIStatus) statusFields() map[string]any {
	command := s.Command
	if command == "" {
		command = s.ConfiguredPath
	}
	return map[string]any{
		"tool": s.Tool, "command": command, "source": s.Source, "version": s.Version,
		"enabled": s.Phase != OptionalCLIDisabled, "installed": s.Ready(), "error": s.Error,
		"authenticationState": "NOT_CHECKED",
	}
}

type OptionalCLIBackend interface {
	ProbeOptionalCLI(context.Context, string, bool) (OptionalCLIStatus, error)
	OptionalCLIStatus(string) OptionalCLIStatus
}

type OptionalCLIFeature uint8

const (
	OptionalCLIFeatureBranchNotice OptionalCLIFeature = iota
	OptionalCLIFeatureGenbu
	OptionalCLIFeatureDragonQA
)

var optionalCLIFeatureTools = [...][]string{
	OptionalCLIFeatureBranchNotice: {"lark"},
	OptionalCLIFeatureGenbu:        {"genbu"},
	OptionalCLIFeatureDragonQA:     {"dragon"},
}

func (f OptionalCLIFeature) Available(ready func(string) bool) bool {
	if int(f) >= len(optionalCLIFeatureTools) {
		return false
	}
	for _, tool := range optionalCLIFeatureTools[f] {
		if !ready(tool) {
			return false
		}
	}
	return true
}

type optionalCLIRegistry struct {
	mu      sync.Mutex
	records map[string]*optionalCLIRecord
}

// A record is also the identity of one verified configuration in this process.
// Replacement invalidates consumers even when a tool returns to the same path.
type optionalCLIRecord struct {
	status OptionalCLIStatus
	scope  context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

var errOptionalCLIScopeChanged = errors.New("CLI 配置或探测状态已变化，请重新打开操作")

func optionalCLIName(tool string) string {
	switch tool {
	case "lark":
		return "Lark"
	case "genbu":
		return "Genbu"
	case "dragon":
		return "Dragon"
	default:
		return ""
	}
}

func optionalCLIConfigStatus(cfg domain.AppConfig, tool string) OptionalCLIStatus {
	status := OptionalCLIStatus{Tool: tool, ConfiguredPath: cfg.OptionalCLIPath(tool), Phase: OptionalCLIDisabled}
	if cfg.OptionalCLIEnabled(tool) {
		status.Phase = OptionalCLIUnavailable
	}
	return status
}

func (r *optionalCLIRegistry) matching(cfg domain.AppConfig, tool string) *optionalCLIRecord {
	record := r.records[tool]
	if record != nil && cfg.OptionalCLIEnabled(tool) && record.status.ConfiguredPath == cfg.OptionalCLIPath(tool) {
		return record
	}
	return nil
}

func (a *App) OptionalCLIStatus(tool string) OptionalCLIStatus {
	if nativePlatform(tool) {
		status, err := a.embeddedPlatformStatus(context.Background(), tool)
		if err != nil {
			status.Error = err.Error()
		}
		return status
	}
	r := &a.optionalCLIs
	r.mu.Lock()
	defer r.mu.Unlock()
	cfg, err := a.Config.LoadForPreview(context.Background())
	if err != nil {
		return OptionalCLIStatus{Tool: tool, Phase: OptionalCLIUnavailable, Error: err.Error()}
	}
	if record := r.matching(cfg, tool); record != nil {
		return record.status
	}
	return optionalCLIConfigStatus(cfg, tool)
}

// ProbeOptionalCLI checks the executable only. Account and permissions are
// checked by the feature that uses it. Concurrent requests share the probe.
func (a *App) ProbeOptionalCLI(ctx context.Context, tool string, force bool) (OptionalCLIStatus, error) {
	if nativePlatform(tool) {
		return a.embeddedPlatformStatus(ctx, tool)
	}
	if optionalCLIName(tool) == "" {
		return OptionalCLIStatus{}, fmt.Errorf("不支持的选装 CLI：%s", tool)
	}
	r := &a.optionalCLIs
	r.mu.Lock()
	cfg, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		r.mu.Unlock()
		return OptionalCLIStatus{}, err
	}
	status := optionalCLIConfigStatus(cfg, tool)
	if status.Phase == OptionalCLIDisabled {
		r.mu.Unlock()
		return status, nil
	}
	if record := r.matching(cfg, tool); record != nil {
		if record.status.Phase == OptionalCLIProbing {
			r.mu.Unlock()
			select {
			case <-ctx.Done():
				return OptionalCLIStatus{}, ctx.Err()
			case <-record.scope.Done():
				return OptionalCLIStatus{}, errOptionalCLIScopeChanged
			case <-record.done:
				r.mu.Lock()
				defer r.mu.Unlock()
				if r.records[tool] != record {
					return OptionalCLIStatus{}, errOptionalCLIScopeChanged
				}
				return record.status, nil
			}
		}
		if !force {
			status = record.status
			r.mu.Unlock()
			return status, nil
		}
	}
	if previous := r.records[tool]; previous != nil {
		previous.cancel()
	}
	if r.records == nil {
		r.records = make(map[string]*optionalCLIRecord)
	}
	scope, cancel := context.WithCancel(context.Background())
	probeCtx, cancelProbe := context.WithCancel(ctx)
	status.Phase = OptionalCLIProbing
	record := &optionalCLIRecord{status: status, scope: scope, cancel: func() { cancel(); cancelProbe() }, done: make(chan struct{})}
	r.records[tool] = record
	r.mu.Unlock()
	defer cancelProbe()
	data, probeErr := a.Platform.Execute(probeCtx, "cli.status", map[string]any{
		"tool": tool, "force": force, "versionOnly": true, "deferDiscovery": true,
	})
	status.Phase = OptionalCLIUnavailable
	if probeErr != nil {
		status.Error = probeErr.Error()
	} else {
		response, _ := data.(map[string]any)
		status.Command, _ = response["command"].(string)
		status.Source, _ = response["source"].(string)
		status.Version, _ = response["version"].(string)
		status.Error, _ = response["error"].(string)
		if installed, _ := response["installed"].(bool); installed && status.Command != "" {
			status.Phase = OptionalCLIAvailable
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	defer close(record.done)
	if r.records[tool] != record {
		if process.IsCleanupFailure(probeErr) {
			return OptionalCLIStatus{}, errors.Join(errOptionalCLIScopeChanged, probeErr)
		}
		return OptionalCLIStatus{}, errOptionalCLIScopeChanged
	}
	current, err := a.Config.LoadForPreview(probeCtx)
	if err != nil || !current.OptionalCLIEnabled(tool) {
		delete(r.records, tool)
		cancel()
		if process.IsCleanupFailure(probeErr) {
			return OptionalCLIStatus{}, errors.Join(err, probeErr)
		}
		if err != nil {
			return OptionalCLIStatus{}, err
		}
		return optionalCLIConfigStatus(current, tool), nil
	}
	if current.OptionalCLIPath(tool) != status.ConfiguredPath {
		delete(r.records, tool)
		cancel()
		return OptionalCLIStatus{}, errOptionalCLIScopeChanged
	}
	if status.Ready() && status.ConfiguredPath == "" {
		updated, saveErr := a.Platform.SaveOptionalCLIDiscovery(probeCtx, current, tool, status.Command, status.Source)
		if saveErr != nil {
			delete(r.records, tool)
			cancel()
			return OptionalCLIStatus{}, saveErr
		}
		status.ConfiguredPath = updated.OptionalCLIPath(tool)
	}
	record.status = status
	if process.IsCleanupFailure(probeErr) {
		return status, probeErr
	}
	return status, nil
}

func (a *App) RequireOptionalCLI(ctx context.Context, tool string) error {
	if nativePlatform(tool) {
		_, err := a.platformAdmission(ctx, tool)
		return err
	}
	if a.optionalCLIRequire != nil {
		return a.optionalCLIRequire(ctx, tool)
	}
	status, err := a.ProbeOptionalCLI(ctx, tool, false)
	if err != nil {
		return err
	}
	switch {
	case status.Ready():
		return nil
	case status.Phase == OptionalCLIDisabled:
		return fmt.Errorf("%s CLI 未启用，请在「工具与连接 → 命令行与账号」中启用", optionalCLIName(tool))
	case status.Error != "":
		return fmt.Errorf("%s CLI 不可用：%s", optionalCLIName(tool), status.Error)
	default:
		return fmt.Errorf("%s CLI 不可用，请检查命令路径", optionalCLIName(tool))
	}
}

// optionalCLIScope admits an operation once and lets later steps verify the
// same executable/configuration without probing a newer scope.
func (a *App) optionalCLIScope(ctx context.Context, tool string) (string, func(context.Context) error, error) {
	if nativePlatform(tool) {
		check, err := a.platformAdmission(ctx, tool)
		return "", check, err
	}
	if err := a.RequireOptionalCLI(ctx, tool); err != nil {
		return "", nil, err
	}
	r := &a.optionalCLIs
	r.mu.Lock()
	defer r.mu.Unlock()
	cfg, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return "", nil, err
	}
	if testRequire := a.optionalCLIRequire; testRequire != nil {
		path, enabled := cfg.OptionalCLIPath(tool), cfg.OptionalCLIEnabled(tool)
		return path, func(ctx context.Context) error {
			current, err := a.Config.LoadForPreview(ctx)
			if err != nil {
				return err
			}
			if current.OptionalCLIPath(tool) != path || current.OptionalCLIEnabled(tool) != enabled {
				return errOptionalCLIScopeChanged
			}
			return testRequire(ctx, tool)
		}, nil
	}
	record := r.matching(cfg, tool)
	if record == nil || !record.status.Ready() {
		return "", nil, errOptionalCLIScopeChanged
	}
	return record.status.Command, func(ctx context.Context) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		current, err := a.Config.LoadForPreview(ctx)
		if err != nil {
			return err
		}
		if r.matching(current, tool) != record {
			return errOptionalCLIScopeChanged
		}
		return ctx.Err()
	}, nil
}

// Serialize in-process configuration changes with admission and retirement.
// Probe subprocesses never hold this lock, and their path writes use the store's
// own compare-and-update check.
func (a *App) updateOptionalCLIConfig(replace bool, apply func() (ActionResult, error)) (ActionResult, error) {
	r := &a.optionalCLIs
	p := &a.connections
	r.mu.Lock()
	p.mu.Lock()
	result, err := apply()
	if err != nil {
		p.mu.Unlock()
		r.mu.Unlock()
		return result, err
	}
	cfg, readErr := a.Config.LoadForPreview(context.Background())
	if readErr == nil {
		a.refreshPlatformConnections(cfg)
	}
	if replace || readErr != nil {
		p.retire("genbu")
		p.retire("dragon")
		p.revision++
	}
	for tool, record := range r.records {
		if replace || readErr != nil || r.matching(cfg, tool) == nil {
			record.cancel()
			delete(r.records, tool)
		}
	}
	p.mu.Unlock()
	r.mu.Unlock()
	return result, nil
}
