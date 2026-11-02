package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func splitGitNUL(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool { return r == 0 })
}

// Inspect every existing path component, including Windows junctions. For a
// deletion, missing tail components are valid; no existing ancestor may redirect.
func materialsGitSafePath(root, path string, rejectNested bool) error {
	root, path = filepath.Clean(root), filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("资料路径超出 Git 同步目录")
	}
	underRoot := true
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil {
			reparse, e := bootstrapPathIsReparsePoint(current, info)
			if e != nil {
				return e
			}
			if info.Mode()&os.ModeSymlink != 0 || reparse {
				return fmt.Errorf("资料同步不跨越链接目录或文件：%s", current)
			}
			if rejectNested && underRoot && current != path && current != root {
				if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
					return fmt.Errorf("资料同步不跨越嵌套仓库：%s", current)
				} else if !os.IsNotExist(err) {
					return err
				}
			}
		}
		if current == root {
			underRoot = false
		}
		if current == filepath.VolumeName(current)+string(filepath.Separator) || filepath.Dir(current) == current {
			break
		}
	}
	return nil
}
