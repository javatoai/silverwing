package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"silverwing-go/internal/documents"
	"strings"
)

func splitGitNUL(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool { return r == 0 })
}

func materialsGitOwned(path, subdirectory string) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) < 4 || !documents.MaterialsYear(parts[0]) || !documents.MaterialsRequirementName(parts[1]) || parts[2] != subdirectory || strings.EqualFold(subdirectory, "temp") {
		return false
	}
	for _, part := range parts {
		if part == "." || part == ".." || strings.EqualFold(part, ".git") || strings.HasPrefix(strings.ToLower(part), ".silverwing-") {
			return false
		}
	}
	return true
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

func (s *MaterialsGitSyncService) materialPaths(ctx context.Context, scope MaterialsGitScope) ([]string, error) {
	// ls-files includes tracked deletions, honors ignore rules for untracked
	// files and never recurses into submodules. Literal arguments protect names
	// containing pathspec metacharacters such as brackets and a leading colon.
	r, err := s.run(ctx, scope, true, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var result []string
	for _, path := range splitGitNUL(r.Stdout) {
		if seen[path] || !materialsGitOwned(path, scope.Subdirectory) {
			continue
		}
		seen[path] = true
		absolute := filepath.Join(scope.Root, filepath.FromSlash(path))
		if err := materialsGitSafePath(scope.Root, absolute, true); err != nil {
			return nil, err
		}
		if info, err := os.Lstat(absolute); err == nil && !info.Mode().IsRegular() {
			return nil, fmt.Errorf("资料不是普通文件：%s", path)
		} else if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		result = append(result, path)
	}
	return result, nil
}
