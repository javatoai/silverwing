package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/productprofile"
)

const TaskManifestFileName = "silverwing.json"

type ManifestEntry struct {
	Path     string
	Manifest domain.TaskManifest
}

type ManifestScanResult struct {
	Current            []ManifestEntry
	UnsupportedPaths   []string
	Failures           map[string]string
	UnsupportedReasons map[string]string
}

type UnsupportedManifestVersionError struct {
	Actual string
}

func (e *UnsupportedManifestVersionError) Error() string {
	actual := e.Actual
	if actual == "" {
		actual = "缺少 schemaVersion"
	}
	return fmt.Sprintf("任务 JSON 版本不受支持：%s，当前版本为 %s", actual, domain.CurrentTaskManifestSchemaVersion)
}

type ManifestStore struct{}

func NewManifestStore() *ManifestStore { return &ManifestStore{} }

func (s *ManifestStore) Load(taskDir string) (domain.TaskManifest, error) {
	path := filepath.Join(taskDir, TaskManifestFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.TaskManifest{}, err
	}
	if !utf8.Valid(data) {
		return domain.TaskManifest{}, fmt.Errorf("读取 %s：任务 JSON 不是有效的 UTF-8", path)
	}
	version, err := manifestVersion(data)
	if err != nil {
		return domain.TaskManifest{}, fmt.Errorf("读取 %s: %w", path, err)
	}
	if !domain.ValidateSchemaVersion(version, domain.CurrentTaskManifestSchemaVersion) {
		return domain.TaskManifest{}, &UnsupportedManifestVersionError{Actual: version}
	}
	var manifest domain.TaskManifest
	if err := domain.DecodeStrict(data, &manifest,
		"edition", "folderName", "taskDirectoryName", "createdAt", "updatedAt",
	); err != nil {
		return domain.TaskManifest{}, fmt.Errorf("读取 %s: %w", path, err)
	}
	if !productprofile.AcceptsEdition(manifest.Edition) {
		return domain.TaskManifest{}, fmt.Errorf("%s", productprofile.ManifestEditionError())
	}
	return manifest, nil
}

func (s *ManifestStore) Save(taskDir string, manifest domain.TaskManifest) error {
	if !productprofile.AcceptsEdition(manifest.Edition) {
		return fmt.Errorf("%s", productprofile.ManifestWriteEditionError())
	}
	if !domain.ValidateSchemaVersion(manifest.SchemaVersion, domain.CurrentTaskManifestSchemaVersion) {
		return fmt.Errorf("不能写入任务 JSON 版本 %s，当前版本为 %s", manifest.SchemaVersion, domain.CurrentTaskManifestSchemaVersion)
	}
	manifest.SchemaVersion = domain.CurrentTaskManifestSchemaVersion
	manifest = domain.NormalizeTaskManifest(manifest)
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	var roundTrip domain.TaskManifest
	if err := domain.DecodeStrict(data, &roundTrip,
		"edition", "folderName", "taskDirectoryName", "createdAt", "updatedAt",
	); err != nil {
		return fmt.Errorf("任务清单无法按当前 schema 读取：%w", err)
	}
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(taskDir, "."+TaskManifestFileName+"-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return replaceFile(tmpPath, filepath.Join(taskDir, TaskManifestFileName))
}

// List matches the source repository's list contract: return compatible,
// decodable manifests and leave unsupported or malformed manifests untouched.
func (s *ManifestStore) List(taskRoot string) ([]ManifestEntry, error) {
	scan, err := s.Scan(taskRoot)
	if err != nil {
		return nil, err
	}
	return scan.Current, nil
}

func (s *ManifestStore) Scan(taskRoot string) (ManifestScanResult, error) {
	entries, err := os.ReadDir(taskRoot)
	if errors.Is(err, os.ErrNotExist) {
		return ManifestScanResult{Current: []ManifestEntry{}, UnsupportedPaths: []string{}, Failures: map[string]string{}, UnsupportedReasons: map[string]string{}}, nil
	}
	if err != nil {
		return ManifestScanResult{}, err
	}
	result := ManifestScanResult{
		Current:            []ManifestEntry{},
		UnsupportedPaths:   []string{},
		Failures:           map[string]string{},
		UnsupportedReasons: map[string]string{},
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		directory := filepath.Join(taskRoot, entry.Name())
		manifestPath := filepath.Join(directory, TaskManifestFileName)
		if _, err := os.Stat(manifestPath); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			result.Failures[directory] = err.Error()
			continue
		}
		manifest, err := s.Load(directory)
		if err == nil {
			result.Current = append(result.Current, ManifestEntry{Path: directory, Manifest: manifest})
			continue
		}
		var unsupported *UnsupportedManifestVersionError
		if errors.As(err, &unsupported) {
			result.UnsupportedPaths = append(result.UnsupportedPaths, directory)
			result.UnsupportedReasons[directory] = unsupported.Error()
			continue
		}
		result.Failures[directory] = err.Error()
	}
	return result, nil
}

func manifestVersion(data []byte) (string, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return "", err
	}
	if object == nil {
		return "", fmt.Errorf("expected a JSON object")
	}
	raw, ok := object["schemaVersion"]
	if !ok {
		return "", nil
	}
	trimmed := bytes.TrimSpace(raw)
	if bytes.HasPrefix(trimmed, []byte("{")) || bytes.HasPrefix(trimmed, []byte("[")) {
		return "", fmt.Errorf("schemaVersion must be a JSON primitive")
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return "", nil
	}
	var version string
	if err := json.Unmarshal(raw, &version); err != nil {
		return string(trimmed), nil
	}
	return version, nil
}
