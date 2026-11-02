package storage

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const legacyMaterialsLock = ".silverwing-requirement-materials.lock"

// MaterialsRepositoryLockPath is independent of the application home and stays
// outside the repository. Existing aliases resolve to the same filesystem root.
func MaterialsRepositoryLockPath(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil || strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("无效的资料根目录：%s", root)
	}
	absolute = filepath.Clean(absolute)
	existing := absolute
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", fmt.Errorf("资料根目录没有可解析的父目录：%s", root)
		}
		existing = parent
	}
	canonical, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	tail, err := filepath.Rel(existing, absolute)
	if err != nil {
		return "", err
	}
	canonical = filepath.Clean(filepath.Join(canonical, tail))
	if runtime.GOOS == "windows" {
		canonical = strings.ToLower(canonical)
	}
	digest := sha256.Sum256([]byte(canonical))
	return filepath.Join(os.TempDir(), "silverwing-materials-locks", fmt.Sprintf("%x.lock", digest)), nil
}

// AcquireMaterialsRepositoryLock is shared by preparation, document mutations
// and Git, including processes using different application homes.
func AcquireMaterialsRepositoryLock(root string) (func(), error) {
	path, err := MaterialsRepositoryLockPath(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	release, err := AcquireLock(path)
	if err != nil {
		return nil, err
	}
	if err := RetireLegacyMaterialsLock(root); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

// RetireLegacyMaterialsLock removes only an identifiable empty old runtime
// file. A running older process remains authoritative: never remove its lock.
// The caller owns the new repository lock (also rechecked after a Git merge).
func RetireLegacyMaterialsLock(root string) error {
	path := filepath.Join(root, legacyMaterialsLock)
	before, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() {
		return fmt.Errorf("旧资料运行锁不是普通文件，请检查：%s", path)
	}
	return retireUnusedMaterialsLock(path, before)
}

func MigrateMaterialsTemporaryIgnore(path string) error {
	return migrateMaterialsIgnore(path, "# Silverwing local temporary files", []string{"*"}, false)
}

func MigrateMaterialsGitExclude(path string) error {
	return migrateMaterialsIgnore(path, "# Silverwing local runtime and temporary files", []string{".silverwing-*", "/[0-9][0-9][0-9][0-9]/*/[tT][eE][mM][pP]/"}, true)
}

// No rule is identified by its pattern alone. Preserve every byte outside
// complete, explicitly marked default blocks, including identical user rules.
func migrateMaterialsIgnore(path, marker string, rules []string, leadingSeparator bool) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("旧资料忽略规则不是普通文件：%s", path)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	after := stripMaterialsIgnoreBlocks(before, marker, rules)
	if bytes.Equal(before, after) {
		return nil
	}
	// info/exclude used one leading separator even when SW created the file.
	// Only that exact wholly generated shape may lose the separator too.
	if leadingSeparator && (bytes.Equal(after, []byte("\n")) || bytes.Equal(after, []byte("\r\n"))) && bytes.HasPrefix(before, after) {
		if len(stripMaterialsIgnoreBlocks(before[len(after):], marker, rules)) == 0 {
			after = nil
		}
	}
	currentInfo, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, currentInfo) {
		return fmt.Errorf("迁移前忽略文件身份已变化，请重试：%s", path)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(before, current) {
		return fmt.Errorf("忽略文件正在被编辑，请保存后重试：%s", path)
	}
	if len(after) == 0 {
		return os.Remove(path)
	}
	return WriteFileAtomic(path, after, info.Mode().Perm())
}

func stripMaterialsIgnoreBlocks(data []byte, marker string, rules []string) []byte {
	var result []byte
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		result = append(result, data[:3]...)
		data = data[3:]
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	text := func(line []byte) string {
		return string(bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r")))
	}
	for i := 0; i < len(lines); {
		if text(lines[i]) == marker {
			end, last := i+1, -1
			for end < len(lines) {
				index := -1
				for j, rule := range rules {
					if text(lines[end]) == rule {
						index = j
						break
					}
				}
				if index <= last {
					break
				}
				last, end = index, end+1
			}
			if end > i+1 {
				i = end
				continue
			}
		}
		result = append(result, lines[i]...)
		i++
	}
	return result
}
