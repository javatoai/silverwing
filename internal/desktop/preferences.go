package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"silverwing-go/internal/storage"
)

const (
	defaultDesktopWidth  = 1600
	defaultDesktopHeight = 980
)

// DesktopPreferences contains display state only. Task identity and content view
// are stored with the reading cache so they cannot drift into a second source.
type DesktopPreferences struct {
	Width                        int    `json:"width"`
	Height                       int    `json:"height"`
	Maximized                    bool   `json:"maximized"`
	SettingsSection              string `json:"settingsSection"`
	TaskListPaneWidth            int    `json:"taskListPaneWidth,omitempty"`
	TaskListPaneCollapsed        bool   `json:"taskListPaneCollapsed"`
	MaterialsDirectoryPaneWidth  int    `json:"materialsDirectoryPaneWidth,omitempty"`
	MaterialsExpansionDepth      int    `json:"materialsExpansionDepth"`
	MaterialsExpansionRevision   uint64 `json:"materialsExpansionRevision"`
	WorkspaceNavigationCollapsed bool   `json:"workspaceNavigationCollapsed"`
}

// DefaultDesktopPreferences supplies the initial Go desktop layout.
func DefaultDesktopPreferences() DesktopPreferences {
	return DesktopPreferences{
		Width: defaultDesktopWidth, Height: defaultDesktopHeight, Maximized: true,
		SettingsSection:              "basic",
		WorkspaceNavigationCollapsed: true,
		MaterialsExpansionDepth:      2, MaterialsExpansionRevision: 1,
	}
}

// DesktopPreferencesStore reads and writes desktop-preferences.json. Unknown
// fields are retained when the Go desktop updates individual display choices.
type DesktopPreferencesStore struct {
	path string
	mu   sync.Mutex
}

func NewDesktopPreferencesStore(path string) *DesktopPreferencesStore {
	return &DesktopPreferencesStore{path: path}
}

// Load supplies and persists the directory-expansion defaults when upgrading an
// old preference file. Invalid JSON is reported without overwriting the file.
func (s *DesktopPreferencesStore) Load() (DesktopPreferences, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

// Save replaces known preference keys while preserving every unknown key.
// Callers that update one field should prefer Update, which starts from the
// latest on-disk values and therefore retains the existing navigation choice.
func (s *DesktopPreferencesStore) Save(value DesktopPreferences) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, raw, err := s.readLocked()
	if err != nil {
		return err
	}
	return s.saveLocked(raw, value)
}

// Update applies a partial preference change to the latest on-disk snapshot.
func (s *DesktopPreferencesStore) Update(change func(*DesktopPreferences)) (DesktopPreferences, error) {
	if change == nil {
		return DesktopPreferences{}, errors.New("desktop preference update is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, raw, err := s.readLocked()
	if err != nil {
		return DesktopPreferences{}, err
	}
	change(&value)
	if err := s.saveLocked(raw, value); err != nil {
		return DesktopPreferences{}, err
	}
	// Return the normalized, persisted snapshot.
	return s.loadLocked()
}

func (s *DesktopPreferencesStore) loadLocked() (DesktopPreferences, error) {
	value, raw, err := s.readLocked()
	if err == nil {
		_, hasDepth := raw["materialsExpansionDepth"]
		_, hasRevision := raw["materialsExpansionRevision"]
		_, hasRetiredSwitch := raw["expandRequirementMaterialsSecondLevelFolders"]
		if !hasDepth || !hasRevision || hasRetiredSwitch {
			if err := s.saveLocked(raw, value); err != nil {
				return value, fmt.Errorf("save directory expansion defaults: %w", err)
			}
		}
	}
	return value, err
}

func (s *DesktopPreferencesStore) readLocked() (DesktopPreferences, map[string]json.RawMessage, error) {
	value := DefaultDesktopPreferences()
	if stringsTrimSpace(s.path) == "" {
		return value, nil, errors.New("desktop preference path is empty")
	}
	content, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return value, map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return value, nil, fmt.Errorf("read desktop preferences: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(content, &raw); err != nil || raw == nil {
		if err == nil {
			err = errors.New("expected a JSON object")
		}
		return value, nil, fmt.Errorf("decode desktop preferences: %w", err)
	}
	readInt(raw, "width", &value.Width)
	readInt(raw, "height", &value.Height)
	readInt(raw, "taskListPaneWidth", &value.TaskListPaneWidth)
	readInt(raw, "materialsDirectoryPaneWidth", &value.MaterialsDirectoryPaneWidth)
	readBool(raw, "maximized", &value.Maximized)
	readBool(raw, "taskListPaneCollapsed", &value.TaskListPaneCollapsed)
	readInt(raw, "materialsExpansionDepth", &value.MaterialsExpansionDepth)
	if rawRevision, exists := raw["materialsExpansionRevision"]; exists {
		_ = json.Unmarshal(rawRevision, &value.MaterialsExpansionRevision)
	}
	if value.MaterialsExpansionDepth < 0 {
		value.MaterialsExpansionDepth = 2
	}
	if value.MaterialsExpansionRevision == 0 {
		value.MaterialsExpansionRevision = 1
	}
	readBool(raw, "workspaceNavigationCollapsed", &value.WorkspaceNavigationCollapsed)
	readString(raw, "settingsSection", &value.SettingsSection)
	if value.Width <= 0 {
		value.Width = defaultDesktopWidth
	}
	if value.Height <= 0 {
		value.Height = defaultDesktopHeight
	}
	if value.SettingsSection == "" {
		value.SettingsSection = "basic"
	}
	if value.TaskListPaneWidth <= 0 {
		value.TaskListPaneWidth = 0
	}
	if value.MaterialsDirectoryPaneWidth <= 0 {
		value.MaterialsDirectoryPaneWidth = 0
	}
	return value, raw, nil
}

func (s *DesktopPreferencesStore) saveLocked(raw map[string]json.RawMessage, value DesktopPreferences) error {
	if value.MaterialsExpansionDepth < 0 {
		return errors.New("目录展开层级不能为负数")
	}
	if value.MaterialsExpansionRevision == 0 {
		value.MaterialsExpansionRevision = 1
	}
	if stringsTrimSpace(s.path) == "" {
		return errors.New("desktop preference path is empty")
	}
	if raw == nil {
		raw = make(map[string]json.RawMessage)
	}
	if value.SettingsSection == "" {
		value.SettingsSection = "basic"
	}
	if !value.Maximized {
		if value.Width <= 0 {
			value.Width = defaultDesktopWidth
		}
		if value.Height <= 0 {
			value.Height = defaultDesktopHeight
		}
		if err := putPreference(raw, "width", value.Width); err != nil {
			return err
		}
		if err := putPreference(raw, "height", value.Height); err != nil {
			return err
		}
	}
	for key, item := range map[string]any{
		"maximized":                    value.Maximized,
		"settingsSection":              value.SettingsSection,
		"taskListPaneCollapsed":        value.TaskListPaneCollapsed,
		"materialsExpansionDepth":      value.MaterialsExpansionDepth,
		"materialsExpansionRevision":   value.MaterialsExpansionRevision,
		"workspaceNavigationCollapsed": value.WorkspaceNavigationCollapsed,
	} {
		if err := putPreference(raw, key, item); err != nil {
			return err
		}
	}
	delete(raw, "expandRequirementMaterialsSecondLevelFolders")
	if value.TaskListPaneWidth > 0 {
		if err := putPreference(raw, "taskListPaneWidth", value.TaskListPaneWidth); err != nil {
			return err
		}
	} else {
		delete(raw, "taskListPaneWidth")
	}
	if value.MaterialsDirectoryPaneWidth > 0 {
		if err := putPreference(raw, "materialsDirectoryPaneWidth", value.MaterialsDirectoryPaneWidth); err != nil {
			return err
		}
	} else {
		delete(raw, "materialsDirectoryPaneWidth")
	}
	content, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("encode desktop preferences: %w", err)
	}
	if err := atomicWriteFile(s.path, append(content, '\n'), 0o600); err != nil {
		return fmt.Errorf("save desktop preferences: %w", err)
	}
	return nil
}

func readInt(raw map[string]json.RawMessage, key string, destination *int) {
	if value, ok := raw[key]; ok {
		_ = json.Unmarshal(value, destination)
	}
}

func readBool(raw map[string]json.RawMessage, key string, destination *bool) {
	if value, ok := raw[key]; ok {
		_ = json.Unmarshal(value, destination)
	}
}

func readString(raw map[string]json.RawMessage, key string, destination *string) {
	if value, ok := raw[key]; ok {
		_ = json.Unmarshal(value, destination)
	}
}

func putPreference(raw map[string]json.RawMessage, key string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode desktop preference %s: %w", key, err)
	}
	raw[key] = encoded
	return nil
}

func atomicWriteFile(path string, content []byte, mode os.FileMode) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return err
	}
	return storage.WriteFileAtomic(absolute, content, mode)
}

func stringsTrimSpace(value string) string {
	return strings.TrimSpace(value)
}
