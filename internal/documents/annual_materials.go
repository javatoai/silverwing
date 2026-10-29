package documents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"silverwing-go/internal/storage"
)

const TemporaryMaterialsDirectory = "temp"

type materialsCreationTimeKey struct{}

// Keeps the preparation year tied to the task's captured creation timestamp,
// including retries across New Year. This is internal context, never stored.
func WithMaterialsCreationTime(ctx context.Context, created time.Time) context.Context {
	return context.WithValue(ctx, materialsCreationTimeKey{}, created)
}

func materialsCreationYear(ctx context.Context) int {
	if created, ok := ctx.Value(materialsCreationTimeKey{}).(time.Time); ok {
		return created.In(time.Local).Year()
	}
	return time.Now().Year()
}

var materialsYearPattern = regexp.MustCompile(`^[1-9][0-9]{3}$`)
var materialsRequirementPattern = regexp.MustCompile(`^[0-9]+-.+$`)

// Derived from the existing write root; sibling directories are read-only.
type MaterialsScope struct{ Base, Root, Owned, Temporary string }

// AnnualMaterialsScope retains the existing API while supported materials use
// one permission model.
type AnnualMaterialsScope = MaterialsScope

func (s MaterialsScope) Readable(path string) bool {
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.Root, filepath.FromSlash(path))
	}
	rel, err := filepath.Rel(s.Root, filepath.Clean(path))
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if strings.EqualFold(part, ".git") || strings.HasPrefix(strings.ToLower(part), ".silverwing-") {
			return false
		}
	}
	return true
}

func MaterialsYear(name string) bool            { return materialsYearPattern.MatchString(name) }
func MaterialsRequirementName(name string) bool { return materialsRequirementPattern.MatchString(name) }
func AnnualScope(owned string) (AnnualMaterialsScope, bool) {
	owned = filepath.Clean(owned)
	root := filepath.Dir(owned)
	year := filepath.Dir(root)
	if !filepath.IsAbs(owned) || !MaterialsYear(filepath.Base(year)) || !MaterialsRequirementName(filepath.Base(root)) || strings.EqualFold(filepath.Base(owned), TemporaryMaterialsDirectory) {
		return AnnualMaterialsScope{}, false
	}
	return AnnualMaterialsScope{Base: filepath.Dir(year), Root: root, Owned: owned, Temporary: filepath.Join(root, TemporaryMaterialsDirectory)}, true
}
func (s MaterialsScope) Writable(path string, allowRoot bool) bool {
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.Root, filepath.FromSlash(path))
	}
	path = filepath.Clean(path)
	for _, root := range []string{s.Owned, s.Temporary} {
		rel, err := filepath.Rel(root, path)
		if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if rel == "." {
			return allowRoot
		}
		for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
			if strings.EqualFold(part, ".git") || strings.HasPrefix(strings.ToLower(part), ".silverwing-") {
				return false
			}
		}
		return !(strings.EqualFold(root, s.Temporary) && strings.EqualFold(rel, ".gitignore"))
	}
	return false
}

// Caller holds the existing materials lock. No Git query or initialization.
func (s MaterialsScope) EnsureLocal() error {
	for _, path := range []string{s.Owned, s.Temporary} {
		if err := ensureMaterialsDirectory(s.Base, path); err != nil {
			return err
		}
	}
	path := filepath.Join(s.Temporary, ".gitignore")
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || isUnsafeDirectory(info, path)) {
		return errors.New("temp 忽略文件不是普通文件")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	const rule = "# Silverwing local temporary files\n*\n"
	if strings.HasSuffix(strings.ReplaceAll(string(data), "\r\n", "\n"), rule) {
		return nil
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	return storage.WriteFileAtomic(path, append(data, []byte(rule)...), 0o600)
}

func findAnnualRequirementDirectory(root, id, folder string, year int) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	var matches []string
	for _, entry := range entries {
		if !MaterialsYear(entry.Name()) {
			continue
		}
		yearPath := filepath.Join(root, entry.Name())
		info, err := os.Lstat(yearPath)
		if err != nil {
			return "", err
		}
		if !info.IsDir() || isUnsafeDirectory(info, yearPath) {
			return "", fmt.Errorf("年份路径不是安全目录：%s", yearPath)
		}
		children, err := os.ReadDir(yearPath)
		if err != nil {
			return "", err
		}
		for _, child := range children {
			if !strings.HasPrefix(child.Name(), id+"-") {
				continue
			}
			path := filepath.Join(yearPath, child.Name())
			info, err := os.Lstat(path)
			if err != nil {
				return "", err
			}
			if !info.IsDir() || isUnsafeDirectory(info, path) {
				return "", fmt.Errorf("需求路径不是安全目录：%s", path)
			}
			matches = append(matches, path)
		}
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("需求 %s 存在多个资料目录，请先处理：%s", id, strings.Join(matches, "；"))
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return filepath.Join(root, fmt.Sprint(year), id+"-"+folder), nil
}
func annualRequirementDirectory(root, id, folder string) (string, error) {
	return findAnnualRequirementDirectory(root, id, folder, time.Now().Year())
}

func (s MaterialsScope) DirectoryInstructions(planned bool) (string, error) {
	for _, path := range []string{s.Base, filepath.Dir(s.Root), s.Root} {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) && planned {
			break
		}
		if err != nil {
			return "", err
		}
		if !info.IsDir() || isUnsafeDirectory(info, path) || !sameResolvedPath(path) {
			return "", fmt.Errorf("资料目录包含不安全的链接：%s", path)
		}
	}
	entries, err := os.ReadDir(s.Root)
	if err != nil && !(planned && os.IsNotExist(err)) {
		return "", err
	}
	var b strings.Builder
	b.WriteString("## 资料目录清单\n\n")
	if planned {
		b.WriteString("以下为资料准备目标；仅在准备成功后使用，不能据此宣称目录已经创建。\n\n")
	}
	for _, area := range []struct{ path, label string }{{s.Owned, "正式资料目录（可读写，按配置参与 Git 同步）"}, {s.Temporary, "本地临时目录（可读写，禁止提交、推送或通过 Git 覆盖）"}} {
		info, err := os.Lstat(area.path)
		if os.IsNotExist(err) && !planned {
			fmt.Fprintf(&b, "- 尚未准备的%s：%s（不表示目录已存在）\n", area.label, materialsCodePath(area.path))
			continue
		}
		if err != nil && !(planned && os.IsNotExist(err)) {
			return "", err
		}
		if err == nil && (!info.IsDir() || isUnsafeDirectory(info, area.path)) {
			return "", fmt.Errorf("资料目录不安全：%s", area.path)
		}
		fmt.Fprintf(&b, "- %s：%s\n", area.label, materialsCodePath(area.path))
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.EqualFold(entry.Name(), ".git") || strings.HasPrefix(strings.ToLower(entry.Name()), ".silverwing-") || strings.EqualFold(entry.Name(), filepath.Base(s.Owned)) || strings.EqualFold(entry.Name(), TemporaryMaterialsDirectory) {
			continue
		}
		path := filepath.Join(s.Root, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if isUnsafeDirectory(info, path) {
			continue
		}
		fmt.Fprintf(&b, "- 同事资料目录（只读，可浏览和复制，禁止修改及上传本地变化）：%s\n", materialsCodePath(path))
	}
	b.WriteString("\n需求分析、设计、测试、验收等正式交付资料写入正式资料目录；过程日志、临时脚本、中间结果和临时导出写入 temp。不得修改忽略规则或强制添加 temp。其他同级文件同样只读。产品源代码仍只写入本任务授权服务工作区。\n")
	return b.String(), nil
}

func materialsCodePath(path string) string {
	fence := strings.Repeat("`", strings.Count(path, "`")+1)
	return fence + path + fence
}
