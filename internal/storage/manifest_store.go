package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"silverwing-go/internal/domain"
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
	return decodeManifest(path, data)
}

func decodeManifest(path string, data []byte) (domain.TaskManifest, error) {
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
		"folderName", "taskDirectoryName", "featureBranch", "createdAt", "updatedAt", "services",
	); err != nil {
		return domain.TaskManifest{}, fmt.Errorf("读取 %s: %w", path, err)
	}
	return manifest, nil
}

// LoadTask reads one first-level task without enumerating or decoding its
// neighbours. OpenRoot pins each directory while the manifest is read; named
// paths are checked again before returning so a replaced root/task is stale.
// The configured root itself may be an alias, but task/manifest links are not.
func (s *ManifestStore) LoadTask(taskRoot, taskID string) (ManifestEntry, error) {
	if taskID == "" || taskID == "." || taskID == ".." || strings.ContainsAny(taskID, "/\\\x00") || filepath.VolumeName(taskID) != "" {
		return ManifestEntry{}, errors.New("任务标识必须是任务根目录第一层的目录名")
	}
	root, err := os.OpenRoot(taskRoot)
	if err != nil {
		return ManifestEntry{}, err
	}
	defer root.Close()
	rootInfo, err := root.Stat(".")
	if err != nil {
		return ManifestEntry{}, err
	}
	if !pinManifestIdentity(rootInfo) {
		return ManifestEntry{}, errors.New("无法确认任务根目录身份")
	}
	taskInfo, err := root.Lstat(taskID)
	if err != nil {
		return ManifestEntry{}, err
	}
	if !taskInfo.IsDir() || manifestPathReparse(taskInfo) || !pinManifestIdentity(taskInfo) {
		return ManifestEntry{}, errors.New("任务目录不是安全的普通目录")
	}
	actualName, err := manifestTaskDirectoryName(filepath.Join(taskRoot, taskID))
	if err != nil {
		return ManifestEntry{}, err
	}
	// Windows permits case, short-name and trailing-dot aliases. The old Scan
	// contract matched the directory's actual name exactly; retain that boundary
	// while allowing aliases in the configured root and its ancestors.
	if actualName != taskID {
		return ManifestEntry{}, errors.New("任务标识与实际目录名不一致，请刷新后重试")
	}
	task, err := root.OpenRoot(taskID)
	if err != nil {
		return ManifestEntry{}, err
	}
	defer task.Close()
	openedTask, err := task.Stat(".")
	if err != nil || !os.SameFile(taskInfo, openedTask) {
		return ManifestEntry{}, errors.New("任务目录身份已变化，请刷新后重试")
	}
	manifestInfo, err := task.Lstat(TaskManifestFileName)
	if err != nil {
		return ManifestEntry{}, err
	}
	if !manifestInfo.Mode().IsRegular() || manifestPathReparse(manifestInfo) || !pinManifestIdentity(manifestInfo) {
		return ManifestEntry{}, errors.New("任务清单不是安全的普通文件")
	}
	file, err := task.Open(TaskManifestFileName)
	if err != nil {
		return ManifestEntry{}, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(manifestInfo, opened) {
		return ManifestEntry{}, errors.New("任务清单身份已变化，请刷新后重试")
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return ManifestEntry{}, err
	}
	path := filepath.Join(taskRoot, taskID)
	manifest, err := decodeManifest(filepath.Join(path, TaskManifestFileName), data)
	if err != nil {
		return ManifestEntry{}, err
	}
	if manifest.TaskDirectoryName != taskID {
		return ManifestEntry{}, errors.New("任务清单的目录标识与实际路径不一致，请修复后重试")
	}
	guard := manifestLookupGuard{taskRoot, taskID, root, task, rootInfo, taskInfo, manifestInfo}
	if !guard.unchanged() {
		return ManifestEntry{}, errors.New("任务根目录或清单身份已变化，请刷新后重试")
	}
	return ManifestEntry{Path: path, Manifest: manifest}, nil
}

func pinManifestIdentity(info os.FileInfo) bool { return info != nil && os.SameFile(info, info) }

// The named paths must still identify the directories and file pinned for this
// read. Keeping this check together also covers replacement through a configured
// root alias without rejecting aliases themselves.
type manifestLookupGuard struct {
	taskRoot, taskID                 string
	root, task                       *os.Root
	rootInfo, taskInfo, manifestInfo os.FileInfo
}

func (g manifestLookupGuard) unchanged() bool {
	currentRoot, rootErr := os.Stat(g.taskRoot)
	currentTask, taskErr := g.root.Lstat(g.taskID)
	currentManifest, manifestErr := g.task.Lstat(TaskManifestFileName)
	return rootErr == nil && taskErr == nil && manifestErr == nil && os.SameFile(g.rootInfo, currentRoot) &&
		!manifestPathReparse(currentTask) && os.SameFile(g.taskInfo, currentTask) &&
		!manifestPathReparse(currentManifest) && os.SameFile(g.manifestInfo, currentManifest) &&
		currentManifest.Size() == g.manifestInfo.Size() && currentManifest.ModTime().Equal(g.manifestInfo.ModTime())
}

func (s *ManifestStore) Save(taskDir string, manifest domain.TaskManifest) error {
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
		"folderName", "taskDirectoryName", "featureBranch", "createdAt", "updatedAt", "services",
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
