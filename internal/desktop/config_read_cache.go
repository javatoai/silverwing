package desktop

import (
	"encoding/json"

	"silverwing-go/internal/domain"
)

// Configuration readers run on the window thread. Retain only the current
// source, so direct assignments, snapshots and detected settings all invalidate
// the cache without depending on a particular update path.
type desktopConfigReadCache struct {
	settings            configSourceReadCache[map[string]any]
	currentApp          configSourceReadCache[domain.AppConfig]
	snapshotApp         configSourceReadCache[domain.AppConfig]
	tagIdentity         configSourceReadCache[string]
	requirementProjects configSourceReadCache[string]
}

// Keep invalidation independent of the projection. Errors are cached as well,
// and a failed replacement cannot expose the last good value or partial data.
type configSourceReadCache[T any] struct {
	source      string
	initialized bool
	value       T
	err         error
}

func (cache *configSourceReadCache[T]) read(source string, load func(string) (T, error)) (T, error) {
	if !cache.initialized || cache.source != source {
		cache.source, cache.initialized = source, true
		cache.value, cache.err = load(source)
		if cache.err != nil {
			var empty T
			cache.value = empty
		}
	}
	return cache.value, cache.err
}

func decodeAppConfig(source string) (domain.AppConfig, error) {
	value := domain.DefaultAppConfig()
	err := json.Unmarshal([]byte(source), &value)
	return value, err
}

// Typed projections are read-only too. Keep current settings separate from the
// backend snapshot; editable task forms continue to decode their own copies.
func (s *app) readCurrentAppConfig() (domain.AppConfig, error) {
	return s.settingsUnit.state.configReads.currentApp.read(s.settingsUnit.state.configJSON, decodeAppConfig)
}

func (s *app) readSnapshotAppConfig() (domain.AppConfig, error) {
	return s.settingsUnit.state.configReads.snapshotApp.read(s.snapshot.ConfigJSON, decodeAppConfig)
}

// The returned map is read-only. Editing continues to use settingsConfigMap,
// which decodes a separate tree and cannot mutate these saved values.
func (s *app) readSettingsConfig() (map[string]any, error) {
	return s.settingsUnit.state.configReads.settings.read(s.settingsUnit.state.configJSON, decodeSettingsConfig)
}

// Only use this borrowed array for read-only queries. Forms and callers that
// retain or modify values use configArray, which owns an independent subtree.
func (s *app) readConfigArray(key string) []any {
	config, err := s.readSettingsConfig()
	if err != nil {
		return nil
	}
	values, _ := config[key].([]any)
	return values
}

// Array callers also build editable drafts. Copy just the requested subtree,
// preserving that ownership without decoding unrelated services and settings.
func cloneConfigArray(values []any) []any {
	if values == nil {
		return nil
	}
	copy := make([]any, len(values))
	for i, value := range values {
		copy[i] = cloneConfigValue(value)
	}
	return copy
}

func cloneConfigValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		copy := make(map[string]any, len(value))
		for key, item := range value {
			copy[key] = cloneConfigValue(item)
		}
		return copy
	case []any:
		return cloneConfigArray(value)
	default:
		return value
	}
}
