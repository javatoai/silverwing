package documents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"unicode/utf16"

	"silverwing-go/internal/domain"
)

type RequirementDocumentationManifest struct {
	FormatVersion    int                 `json:"formatVersion"`
	Identity         RequirementIdentity `json:"identity"`
	RequirementTitle string              `json:"requirementTitle"`
	DirectoryName    string              `json:"directoryName"`
	CreatedAt        string              `json:"createdAt"`
	UpdatedAt        string              `json:"updatedAt"`
}

type RequirementDocumentationIndexEntry struct {
	FormatVersion        int                 `json:"formatVersion"`
	Identity             RequirementIdentity `json:"identity"`
	RequirementDirectory string              `json:"requirementDirectory"`
	UpdatedAt            string              `json:"updatedAt"`
}

type preparationFileStore struct{}

func (f *preparationFileStore) exists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func (f *preparationFileStore) isDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && !isUnsafeDirectory(info, path)
}

func (f *preparationFileStore) isRegularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && !isUnsafeDirectory(info, path)
}

func (f *preparationFileStore) isSymbolicLink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && isUnsafeDirectory(info, path)
}

func (f *preparationFileStore) write(path, content string) error {
	return atomicWrite(path, []byte(content), 0o644)
}

func (f *preparationFileStore) readRequirement(path string) (RequirementDocumentationManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RequirementDocumentationManifest{}, err
	}
	var manifest RequirementDocumentationManifest
	if err := domain.DecodeStrict(data, &manifest, "formatVersion", "identity", "requirementTitle", "directoryName", "createdAt", "updatedAt"); err != nil {
		return RequirementDocumentationManifest{}, fmt.Errorf("任务资料 manifest 损坏：%s: %w", path, err)
	}
	if manifest.FormatVersion != documentationFormatVersion {
		return RequirementDocumentationManifest{}, fmt.Errorf("不支持的任务资料 manifest 格式：%d", manifest.FormatVersion)
	}
	if err := manifest.Identity.Validate(); err != nil {
		return RequirementDocumentationManifest{}, fmt.Errorf("任务资料 manifest 身份无效：%s: %w", path, err)
	}
	return manifest, nil
}

func (f *preparationFileStore) readRequirementIdentity(path string) (RequirementIdentity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RequirementIdentity{}, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return RequirementIdentity{}, fmt.Errorf("manifest 根节点格式不正确")
	}
	var version int
	if err := json.Unmarshal(object["formatVersion"], &version); err != nil || version != documentationFormatVersion {
		return RequirementIdentity{}, fmt.Errorf("不支持的任务资料 manifest 格式：%v", version)
	}
	var identity RequirementIdentity
	if err := json.Unmarshal(object["identity"], &identity); err != nil {
		return RequirementIdentity{}, fmt.Errorf("manifest 缺少需求身份：%w", err)
	}
	identity.Space = strings.TrimSpace(identity.Space)
	identity.Kind = strings.TrimSpace(identity.Kind)
	identity.WorkItemID = strings.TrimSpace(identity.WorkItemID)
	if err := identity.Validate(); err != nil {
		return RequirementIdentity{}, fmt.Errorf("manifest 需求身份缺少字段或格式错误：%w", err)
	}
	return identity, nil
}

func (f *preparationFileStore) readManagedManifest(writeRoot string, identity RequirementIdentity) (*RequirementDocumentationManifest, error) {
	path := filepath.Join(writeRoot, requirementManifestName)
	exists, err := f.exists(path)
	if err != nil || !exists {
		return nil, err
	}
	if !f.isRegularFile(path) || f.isSymbolicLink(path) {
		return nil, fmt.Errorf("任务资料 manifest 不是普通文件：%s", path)
	}
	manifest, err := f.readRequirement(path)
	if err != nil {
		return nil, err
	}
	if manifest.Identity != identity {
		return nil, fmt.Errorf("任务资料 manifest 与当前需求不一致，已停止写入：%s", path)
	}
	return &manifest, nil
}

func (f *preparationFileStore) encodeRequirement(manifest RequirementDocumentationManifest) string {
	data, _ := json.MarshalIndent(manifest, "", "  ")
	return string(data)
}

func (f *preparationFileStore) readIndex(root string) ([]RequirementDocumentationIndexEntry, error) {
	path := filepath.Join(root, requirementIndexName)
	exists, err := f.exists(path)
	if err != nil || !exists {
		return []RequirementDocumentationIndexEntry{}, err
	}
	if f.isSymbolicLink(path) || !f.isRegularFile(path) {
		return nil, fmt.Errorf("任务资料索引不是普通文件：%s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	result := []RequirementDocumentationIndexEntry{}
	for lineNumber, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry RequirementDocumentationIndexEntry
		if err := domain.DecodeStrict([]byte(line), &entry, "formatVersion", "identity", "requirementDirectory", "updatedAt"); err != nil {
			return nil, fmt.Errorf("任务资料索引第 %d 行损坏：%w", lineNumber+1, err)
		}
		if entry.FormatVersion != documentationFormatVersion {
			return nil, fmt.Errorf("任务资料索引第 %d 行格式不受支持：%d", lineNumber+1, entry.FormatVersion)
		}
		if err := entry.Identity.Validate(); err != nil {
			return nil, fmt.Errorf("任务资料索引第 %d 行身份无效：%w", lineNumber+1, err)
		}
		result = append(result, entry)
	}
	return result, nil
}

func (f *preparationFileStore) updateIndex(root, directory string, manifest RequirementDocumentationManifest) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	directoryAbs, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	if !isWithin(rootAbs, directoryAbs) {
		return fmt.Errorf("需求目录超出任务资料根目录")
	}
	relative, err := filepath.Rel(rootAbs, directoryAbs)
	if err != nil {
		return err
	}
	entries, err := f.readIndex(rootAbs)
	if err != nil {
		return err
	}
	next := make([]RequirementDocumentationIndexEntry, 0, len(entries)+1)
	for _, entry := range entries {
		if entry.Identity != manifest.Identity {
			next = append(next, entry)
		}
	}
	next = append(next, RequirementDocumentationIndexEntry{
		FormatVersion:        documentationFormatVersion,
		Identity:             manifest.Identity,
		RequirementDirectory: relative,
		UpdatedAt:            manifest.UpdatedAt,
	})
	sort.Slice(next, func(i, j int) bool { return next[i].Identity.StableKey() < next[j].Identity.StableKey() })
	lines := make([]string, 0, len(next))
	for _, entry := range next {
		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		lines = append(lines, string(data))
	}
	return f.write(filepath.Join(rootAbs, requirementIndexName), strings.Join(lines, "\n")+"\n")
}

type requirementDirectoryContext struct {
	RequirementDirectory string
	WriteRoot            string
}

func requireMaterialsRoot(configured *string) (string, error) {
	if configured == nil || strings.TrimSpace(*configured) == "" {
		return "", fmt.Errorf("尚未配置任务资料根目录；请在 silverwing 设置 > 路径设置中配置")
	}
	value, err := filepath.Abs(strings.TrimSpace(*configured))
	if err != nil {
		return "", err
	}
	value = filepath.Clean(value)
	info, err := os.Lstat(value)
	if err != nil || !info.IsDir() || isUnsafeDirectory(info, value) {
		return "", fmt.Errorf("资料根路径必须是已存在的普通目录")
	}
	return value, nil
}

func requireMaterialsSubdirectory(configured *string) (string, error) {
	if configured == nil {
		return "", fmt.Errorf("尚未配置任务资料子目录；请在 silverwing 设置 > 路径设置中配置")
	}
	value := strings.TrimSpace(*configured)
	if err := validateMaterialsSegment(value, "资料子目录名", 100); err != nil {
		return "", err
	}
	return value, nil
}

func validateMaterialsSegment(value, label string, maxLength int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s 不能为空", label)
	}
	if value == "." || value == ".." {
		return fmt.Errorf("%s 不能是 . 或 ..", label)
	}
	if strings.ContainsAny(value, "<>:\"/\\|?*\x00\r\n") {
		return fmt.Errorf("%s 包含 Windows 非法文件名字符", label)
	}
	for _, r := range value {
		if r < 0x20 {
			return fmt.Errorf("%s 包含 Windows 非法文件名字符", label)
		}
	}
	if strings.HasSuffix(value, ".") || strings.HasSuffix(value, " ") {
		return fmt.Errorf("%s 不能以空格或句点结尾", label)
	}
	if len(utf16.Encode([]rune(value))) > maxLength {
		return fmt.Errorf("%s 超过允许长度", label)
	}
	stem := strings.ToUpper(strings.TrimRight(strings.SplitN(value, ".", 2)[0], ". "))
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return fmt.Errorf("%s 使用了 Windows 保留名称", label)
	}
	return nil
}

func buildRequirementDirectory(root, id, folder string) (string, error) {
	if !requirementIDPattern.MatchString(id) {
		return "", fmt.Errorf("需求编号不合法")
	}
	if err := validateMaterialsSegment(folder, "需求文件夹名称", 80); err != nil {
		return "", err
	}
	name := id + "-" + folder
	if err := validateMaterialsSegment(name, "需求目录名", 140); err != nil {
		return "", err
	}
	path := filepath.Clean(filepath.Join(root, name))
	if !isWithin(root, path) {
		return "", fmt.Errorf("任务资料目录超出资料根目录")
	}
	return path, nil
}

func resolveExistingRequirementDirectory(root, id, folder, subdirectory string, identity RequirementIdentity, files *preparationFileStore) (*requirementDirectoryContext, error) {
	path, err := buildRequirementDirectory(root, id, folder)
	if err != nil {
		return nil, err
	}
	exists, err := files.exists(path)
	if err != nil || !exists {
		return nil, err
	}
	if err := files.assertDirectory(path); err != nil {
		return nil, err
	}
	if err := validateExistingRequirementDirectory(path, subdirectory, identity, files); err != nil {
		return nil, err
	}
	return &requirementDirectoryContext{RequirementDirectory: path, WriteRoot: filepath.Join(path, subdirectory)}, nil
}

func validateExistingRequirementDirectory(path, subdirectory string, identity RequirementIdentity, files *preparationFileStore) error {
	return validateExistingRequirementDirectoryInput(path, subdirectory, identity.WorkItemID, &identity, files)
}

func validateExistingRequirementDirectoryInput(path, subdirectory, requirementID string, identity *RequirementIdentity, files *preparationFileStore) error {
	return filepath.WalkDir(path, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if isUnsafeDirectory(info, current) {
			return fmt.Errorf("需求目录包含链接或重解析点，拒绝使用：%s", current)
		}
		if !info.Mode().IsRegular() || filepath.Base(current) != requirementManifestName {
			return nil
		}
		if filepath.Dir(current) != filepath.Join(path, subdirectory) {
			return fmt.Errorf("任务资料 manifest 必须位于当前资料子目录：%s", current)
		}
		found, err := files.readRequirementIdentity(current)
		if err != nil {
			return fmt.Errorf("任务资料 manifest 身份损坏：%s: %w", current, err)
		}
		if found.WorkItemID != requirementID {
			return fmt.Errorf("任务资料目录中的 manifest 与当前需求编号不一致，已停止写入：%s", current)
		}
		if identity != nil {
			if !strings.EqualFold(found.Space, identity.Space) {
				return fmt.Errorf("任务资料目录中的 manifest 与当前需求空间不一致，已停止写入：%s", current)
			}
			if !strings.EqualFold(found.Kind, identity.Kind) {
				return fmt.Errorf("任务资料目录中的 manifest 与当前需求类型不一致，已停止写入：%s", current)
			}
		}
		return nil
	})
}

func ensureMaterialsDirectory(root, path string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if !isWithin(rootAbs, pathAbs) {
		return fmt.Errorf("路径不在资料根目录内")
	}
	if err := (&preparationFileStore{}).assertDirectory(rootAbs); err != nil {
		return err
	}
	relative, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return err
	}
	current := rootAbs
	if relative == "." {
		return nil
	}
	for _, segment := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0o755); err != nil {
				return err
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || isUnsafeDirectory(info, current) {
			return fmt.Errorf("目标是链接或重解析点，拒绝使用：%s", current)
		}
	}
	return nil
}

func (f *preparationFileStore) assertDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("需求目录不是目录：%s", path)
	}
	if isUnsafeDirectory(info, path) {
		return fmt.Errorf("需求目录不能是链接或重解析点：%s", path)
	}
	return nil
}

func absoluteClean(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func isWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func renderRequirementOverview(manifest RequirementDocumentationManifest) string {
	return fmt.Sprintf("# %s-%s\n\n- 需求：`%s`\n- 创建时间：%s\n\n## 过程文档\n\n过程文档、研发辅助资料、SQL 与脚本统一写入本需求的资料子目录。代码仓库自身的 README、ADR、API 文档仍留在对应 Worktree。\n",
		manifest.Identity.WorkItemID, manifest.RequirementTitle, manifest.Identity.StableKey(), manifest.CreatedAt)
}

var (
	requirementSpacePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	requirementIDPattern    = regexp.MustCompile(`^\d+$`)
)

func isWindowsHost() bool { return runtime.GOOS == "windows" }
