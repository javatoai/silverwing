package documents

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"
)

type MarkdownFileSnapshot struct {
	Path       string `json:"path"`
	Text       string `json:"text"`
	Hash       string `json:"hash"`
	Size       int64  `json:"size"`
	Modified   string `json:"modified"`
	HasUTF8BOM bool   `json:"hasUtf8Bom"`
	Newline    string `json:"newline"`
}

// MarkdownSnapshot reads a bounded, strict UTF-8 snapshot and records the
// source byte hash, BOM and dominant newline style for lossless saves.
func (s *Service) MarkdownSnapshot(root, relativePath string) (MarkdownFileSnapshot, error) {
	root, err := validatedMaterialsRoot(root)
	if err != nil {
		return MarkdownFileSnapshot{}, err
	}
	path, err := safePath(root, relativePath, false)
	if err != nil {
		return MarkdownFileSnapshot{}, err
	}
	if Kind(path) != "markdown" {
		return MarkdownFileSnapshot{}, errors.New("只有 Markdown 文件支持编辑")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return MarkdownFileSnapshot{}, err
	}
	if err := validateMaterialPath(root, path, info); err != nil {
		return MarkdownFileSnapshot{}, err
	}
	if !info.Mode().IsRegular() {
		return MarkdownFileSnapshot{}, errors.New("Markdown 文件不存在或不是普通文件")
	}
	if info.Size() > MaxEditableMarkdown {
		return MarkdownFileSnapshot{}, errMaterialsPreviewTooLarge
	}
	file, err := openMaterialFileNoFollow(path, info)
	if err != nil {
		return MarkdownFileSnapshot{}, err
	}
	defer file.Close()
	bytes, err := io.ReadAll(io.LimitReader(file, MaxEditableMarkdown+1))
	if err != nil {
		return MarkdownFileSnapshot{}, err
	}
	if int64(len(bytes)) > MaxEditableMarkdown {
		return MarkdownFileSnapshot{}, errMaterialsPreviewTooLarge
	}
	return markdownSnapshotFromBytes(path, info, bytes)
}

// SaveMarkdown checks the exact byte baseline twice before replacing the file
// and retains UTF-8 BOM and LF/CRLF style. A changed baseline is returned with
// ErrConflict so the editor can show the current external version.
func (s *Service) SaveMarkdown(ctx context.Context, root, relativePath, content, baselineHash string) (MarkdownFileSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return MarkdownFileSnapshot{}, err
	}
	if int64(len([]byte(content))) > MaxEditableMarkdown {
		return MarkdownFileSnapshot{}, errMaterialsPreviewTooLarge
	}
	root, err := validatedMaterialsRoot(root)
	if err != nil {
		return MarkdownFileSnapshot{}, err
	}
	path, err := safePath(root, relativePath, false)
	if err != nil {
		return MarkdownFileSnapshot{}, err
	}
	current, err := s.MarkdownSnapshot(root, relativePath)
	if err != nil {
		return MarkdownFileSnapshot{}, err
	}
	if baselineHash == "" || current.Hash != baselineHash {
		return current, ErrConflict
	}
	encoded := materialTextBytes(content, current.HasUTF8BOM)
	if int64(len(encoded)) > MaxEditableMarkdown {
		return MarkdownFileSnapshot{}, errMaterialsPreviewTooLarge
	}
	info, err := os.Lstat(path)
	if err != nil {
		return MarkdownFileSnapshot{}, err
	}
	staging, err := stagingPath(filepath.Dir(path), "import", ".tmp")
	if err != nil {
		return MarkdownFileSnapshot{}, err
	}
	parent := filepath.Dir(path)
	if err := revalidateMaterialsDestination(root, relativeMaterials(root, parent), parent); err != nil {
		return MarkdownFileSnapshot{}, err
	}
	if err := writeNewFile(ctx, staging, encoded, info.Mode().Perm()); err != nil {
		return MarkdownFileSnapshot{}, err
	}
	defer os.Remove(staging)
	if err := ctx.Err(); err != nil {
		return MarkdownFileSnapshot{}, err
	}
	latest, err := s.MarkdownSnapshot(root, relativePath)
	if err != nil {
		return MarkdownFileSnapshot{}, err
	}
	if latest.Hash != baselineHash {
		return latest, ErrConflict
	}
	if err := revalidateMaterialsDestination(root, relativeMaterials(root, parent), parent); err != nil {
		return MarkdownFileSnapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return MarkdownFileSnapshot{}, err
	}
	if err := os.Rename(staging, path); err != nil {
		return MarkdownFileSnapshot{}, err
	}
	return markdownSnapshotFromBytes(path, nil, encoded)
}

func markdownSnapshotFromBytes(path string, info os.FileInfo, data []byte) (MarkdownFileSnapshot, error) {
	bom := len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF
	textBytes := data
	if bom {
		textBytes = data[3:]
	}
	if !utf8.Valid(textBytes) {
		return MarkdownFileSnapshot{}, errors.New("Markdown 必须是有效 UTF-8 文本")
	}
	text := string(textBytes)
	newline := materialTextFirstNewline(text)
	modified := ""
	if info != nil {
		modified = info.ModTime().Format(time.RFC3339Nano)
	}
	return MarkdownFileSnapshot{Path: path, Text: text, Hash: Hash(data), Size: int64(len(data)), Modified: modified, HasUTF8BOM: bom, Newline: newline}, nil
}
