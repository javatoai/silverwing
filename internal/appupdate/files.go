package appupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const MetadataName = "bundle.json"

func Within(root, path string) bool {
	root, e1 := filepath.Abs(root)
	path, e2 := filepath.Abs(path)
	if e1 != nil || e2 != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		root, path = strings.ToLower(root), strings.ToLower(path)
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Resolve rejects symlinks/junctions at every existing path component.
func Resolve(root, relative string) (string, error) {
	if relative == "" || strings.HasPrefix(relative, "/") || strings.Contains(relative, "\\") || filepath.IsAbs(relative) || strings.Contains(relative, ":") {
		return "", errors.New("无效的更新相对路径")
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("更新路径越界")
	}
	path := filepath.Join(root, clean)
	if !Within(root, path) {
		return "", errors.New("更新路径越界")
	}
	for current := path; Within(root, current); current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if err == nil && (info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeIrregular != 0) {
			return "", errors.New("更新路径包含链接或特殊文件")
		}
		if filepath.Clean(current) == filepath.Clean(root) {
			break
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return path, nil
}

func Digest(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("更新文件不是普通文件")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func ReadJSON(path string, target any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 8<<20 {
		return errors.New("更新描述文件无效或过大")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(content, target)
}

func WriteJSON(path string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = file.Write(append(content, '\n'))
	if err == nil {
		err = file.Sync()
	}
	if e := file.Close(); err == nil {
		err = e
	}
	return err
}

func VerifyLayout(root string, layout Layout) error {
	if layout.Schema != 1 || len(layout.Files) == 0 || len(layout.Files) > 20000 {
		return errors.New("程序文件清单无效")
	}
	seen := map[string]bool{}
	for _, item := range layout.Files {
		path, err := Resolve(root, item.Path)
		if err != nil {
			return err
		}
		key := strings.ToLower(filepath.Clean(path))
		if seen[key] {
			return errors.New("程序文件清单包含重复路径")
		}
		seen[key] = true
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() != item.Size {
			return fmt.Errorf("程序文件大小已变化：%s", item.Path)
		}
		hash, err := Digest(path)
		if err != nil {
			return err
		}
		if hash != item.SHA256 {
			return fmt.Errorf("程序文件校验失败：%s", item.Path)
		}
	}
	for _, required := range []string{layout.Executable, layout.Helper} {
		path, err := Resolve(root, required)
		if err != nil {
			return err
		}
		if !seen[strings.ToLower(filepath.Clean(path))] {
			return errors.New("程序清单缺少主程序或更新助手")
		}
	}
	return nil
}

// Inventory is shared by packaging and the replacement adapters.
func Inventory(root, exclude string) ([]File, error) {
	var files []File
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == exclude {
			return nil
		}
		if _, err := Resolve(root, rel); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		hash, err := Digest(path)
		if err != nil {
			return err
		}
		files = append(files, File{Path: rel, SHA256: hash, Size: info.Size(), Mode: uint32(info.Mode().Perm())})
		return nil
	})
	return files, err
}

func CopyFile(source, destination string, mode fs.FileMode) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("不能复制特殊更新文件")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(output, input)
	if err == nil {
		err = output.Sync()
	}
	if e := output.Close(); err == nil {
		err = e
	}
	return err
}
