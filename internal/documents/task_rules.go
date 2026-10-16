package documents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// TaskRulesSnapshot distinguishes an absent file from an existing empty file.
// Text is the complete UTF-8 source, including its original trailing newlines.
type TaskRulesSnapshot struct {
	MarkdownFileSnapshot
	Exists bool `json:"exists"`
}

func taskRulesTarget(root, path string) (string, string, error) {
	root, err := validatedMaterialsRoot(root)
	if err != nil {
		return "", "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || !pathWithin(root, path) || hasGitSegment(path) {
		return "", "", errors.New("任务说明路径超出任务目录")
	}
	current := root
	for _, segment := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return root, path, nil
		}
		if err != nil {
			return "", "", err
		}
		if err := validateMaterialPath(root, current, info); err != nil {
			return "", "", err
		}
		if current != path && !info.IsDir() {
			return "", "", errors.New("任务说明的上级不是目录")
		}
	}
	return root, path, nil
}

func (s *Service) ReadTaskRules(root, path string) (TaskRulesSnapshot, error) {
	root, path, err := taskRulesTarget(root, path)
	if err != nil {
		return TaskRulesSnapshot{}, err
	}
	snapshot, err := s.MarkdownSnapshot(root, path)
	if os.IsNotExist(err) {
		return TaskRulesSnapshot{MarkdownFileSnapshot: MarkdownFileSnapshot{Path: path}}, nil
	}
	return TaskRulesSnapshot{MarkdownFileSnapshot: snapshot, Exists: err == nil}, err
}

// SaveTaskRules reuses the file service's staging, path checks and atomic
// replacement, but keeps source line endings instead of normalizing Markdown.
func (s *Service) SaveTaskRules(ctx context.Context, root, path, text, expectedHash string, expectedExists bool) (TaskRulesSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return TaskRulesSnapshot{}, err
	}
	if !utf8.ValidString(text) || len(text) > MaxEditableMarkdown {
		return TaskRulesSnapshot{}, errors.New("任务说明必须是 3 MiB 以内的有效 UTF-8 文本")
	}
	root, path, err := taskRulesTarget(root, path)
	if err != nil {
		return TaskRulesSnapshot{}, err
	}
	current, err := s.ReadTaskRules(root, path)
	if err != nil {
		return TaskRulesSnapshot{}, err
	}
	matches := func(value TaskRulesSnapshot) bool {
		return value.Exists == expectedExists && (!expectedExists || expectedHash != "" && value.Hash == expectedHash)
	}
	if !matches(current) {
		return current, ErrConflict
	}
	data := []byte(text)
	if current.HasUTF8BOM {
		data = append([]byte{0xef, 0xbb, 0xbf}, data...)
	}
	if len(data) > MaxEditableMarkdown {
		return TaskRulesSnapshot{}, errors.New("任务说明超过 3 MiB")
	}
	parent := filepath.Dir(path)
	if err := ensureMaterialsDirectory(root, parent); err != nil {
		return TaskRulesSnapshot{}, err
	}
	mode := os.FileMode(0o644)
	if current.Exists {
		info, err := os.Lstat(path)
		if err != nil {
			return TaskRulesSnapshot{}, err
		}
		mode = info.Mode().Perm()
	}
	staging, err := stagingPath(parent, "import", ".tmp")
	if err != nil {
		return TaskRulesSnapshot{}, err
	}
	if err := writeNewFile(ctx, staging, data, mode); err != nil {
		return TaskRulesSnapshot{}, err
	}
	defer os.Remove(staging)
	latest, err := s.ReadTaskRules(root, path)
	if err != nil {
		return TaskRulesSnapshot{}, err
	}
	if !matches(latest) {
		return latest, ErrConflict
	}
	if err := revalidateMaterialsDestination(root, relativeMaterials(root, parent), parent); err != nil {
		return TaskRulesSnapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return TaskRulesSnapshot{}, err
	}
	if expectedExists {
		err = os.Rename(staging, path)
	} else {
		err = renameNoReplace(staging, path)
		if errors.Is(err, os.ErrExist) {
			return TaskRulesSnapshot{}, ErrConflict
		}
	}
	if err != nil {
		return TaskRulesSnapshot{}, err
	}
	snapshot, err := markdownSnapshotFromBytes(path, nil, data)
	return TaskRulesSnapshot{MarkdownFileSnapshot: snapshot, Exists: err == nil}, err
}
