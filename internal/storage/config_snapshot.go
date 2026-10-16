package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// FileSnapshot returns the current config path and its readable raw content.
// For a directory, regular files are sorted and joined with Kotlin-compatible
// section headers. Staging files are omitted just as they are from reads.
func (s *ConfigStore) FileSnapshot() ConfigFileSnapshot {
	release, err := s.acquireReadLock(context.Background())
	if err != nil {
		message := err.Error()
		return ConfigFileSnapshot{Path: s.paths.Config, Exists: true, ReadError: &message}
	}
	defer release()
	path, err := filepath.Abs(s.paths.Config)
	if err != nil {
		message := err.Error()
		return ConfigFileSnapshot{Path: s.paths.Config, Exists: true, ReadError: &message}
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return ConfigFileSnapshot{Path: path, Exists: false}
	}
	if err != nil {
		message := err.Error()
		return ConfigFileSnapshot{Path: path, Exists: true, ReadError: &message}
	}
	if !info.IsDir() {
		data, err := readConfigSnapshotText(path)
		if err != nil {
			message := err.Error()
			return ConfigFileSnapshot{Path: path, Exists: true, ReadError: &message}
		}
		content := data
		return ConfigFileSnapshot{Path: path, Exists: true, Content: &content}
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		message := err.Error()
		return ConfigFileSnapshot{Path: path, Exists: true, ReadError: &message}
	}
	sections := []string{}
	for _, entry := range entries {
		if isShardStagingFile(entry.Name()) {
			continue
		}
		filePath := filepath.Join(path, entry.Name())
		fileInfo, statErr := os.Stat(filePath)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			message := statErr.Error()
			return ConfigFileSnapshot{Path: path, Exists: true, ReadError: &message}
		}
		if !fileInfo.Mode().IsRegular() {
			continue
		}
		data, readErr := readConfigSnapshotText(filePath)
		if readErr != nil {
			message := readErr.Error()
			return ConfigFileSnapshot{Path: path, Exists: true, ReadError: &message}
		}
		sections = append(sections, "["+entry.Name()+"]\n"+data)
	}
	content := strings.Join(sections, "\n\n")
	return ConfigFileSnapshot{Path: path, Exists: true, Content: &content}
}

func readConfigSnapshotText(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("配置文件不是有效的 UTF-8：%s", path)
	}
	return string(data), nil
}
