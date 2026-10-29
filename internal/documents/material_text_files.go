package documents

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

type MaterialTextSnapshot = MarkdownFileSnapshot

func EditableMaterialText(path string) bool {
	switch Kind(path) {
	case "text", "json", "table":
		return true
	}
	return false
}

// MaterialTextSnapshot keeps the exact source bytes separate from preview
// projections. UTF-16 and detected unknown formats remain read-only previews.
func (s *Service) MaterialTextSnapshot(root, path string) (snapshot MaterialTextSnapshot, resultErr error) {
	scope, err := newMaterialsOperationScope(root)
	if err != nil {
		return MaterialTextSnapshot{}, err
	}
	defer scope.finishSnapshot(&snapshot, &resultErr)
	root = scope.root
	path, err = scope.resolve(path, false, false, false)
	if err != nil {
		return MaterialTextSnapshot{}, err
	}
	if !EditableMaterialText(path) {
		return MaterialTextSnapshot{}, errors.New("该资料类型仅支持只读预览")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return MaterialTextSnapshot{}, err
	}
	if err = validateMaterialPath(root, path, info); err != nil {
		return MaterialTextSnapshot{}, err
	}
	if !info.Mode().IsRegular() {
		return MaterialTextSnapshot{}, errors.New("只能编辑普通资料文件")
	}
	if info.Size() > MaxMaterialsText {
		return MaterialTextSnapshot{}, errMaterialsPreviewTooLarge
	}
	file, err := openMaterialFileNoFollow(path, info)
	if err != nil {
		return MaterialTextSnapshot{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxMaterialsText+1))
	if err != nil {
		return MaterialTextSnapshot{}, err
	}
	return materialTextSnapshotFromBytes(path, info, data)
}

func materialTextSnapshotFromBytes(path string, info os.FileInfo, data []byte) (MaterialTextSnapshot, error) {
	if len(data) > MaxMaterialsText {
		return MaterialTextSnapshot{}, errMaterialsPreviewTooLarge
	}
	bom := len(data) >= 3 && string(data[:3]) == "\xef\xbb\xbf"
	source := data
	if bom {
		source = source[3:]
	}
	if !utf8.Valid(source) || strings.IndexByte(string(source), 0) >= 0 {
		return MaterialTextSnapshot{}, errors.New("可编辑资料必须是有效 UTF-8 文本")
	}
	text, modified := string(source), ""
	newline := materialTextFirstNewline(text)
	if info != nil {
		modified = info.ModTime().Format(time.RFC3339Nano)
	}
	return MaterialTextSnapshot{Path: path, Text: text, Hash: Hash(data), Size: int64(len(data)), Modified: modified, HasUTF8BOM: bom, Newline: newline}, nil
}

func materialTextFirstNewline(text string) string {
	if first := strings.IndexAny(text, "\r\n"); first >= 0 && text[first] == '\r' {
		if first+1 < len(text) && text[first+1] == '\n' {
			return "\r\n"
		}
		return "\r"
	}
	return "\n"
}

func materialTextBytes(text string, hasBOM bool) []byte {
	if hasBOM {
		return append([]byte{0xef, 0xbb, 0xbf}, []byte(text)...)
	}
	return []byte(text)
}

// SaveMaterialText writes the editor's original source verbatim. In particular,
// neither syntax highlighting nor JSON/CSV preview output is serialized.
func (s *Service) SaveMaterialText(ctx context.Context, root, path, text, baselineHash string, hasBOM bool) (snapshot MaterialTextSnapshot, resultErr error) {
	if err := ctx.Err(); err != nil {
		return MaterialTextSnapshot{}, err
	}
	if _, err := materialTextSnapshotFromBytes(path, nil, materialTextBytes(text, hasBOM)); err != nil {
		return MaterialTextSnapshot{}, err
	}
	scope, err := newMaterialsOperationScope(root)
	if err != nil {
		return MaterialTextSnapshot{}, err
	}
	defer scope.finishSnapshot(&snapshot, &resultErr)
	path, err = scope.resolve(path, false, true, false)
	if err != nil {
		return MaterialTextSnapshot{}, err
	}
	return s.saveMaterialText(ctx, scope.root, path, text, baselineHash, hasBOM, scope)
}

func (s *Service) saveMaterialText(ctx context.Context, root, path, text, baselineHash string, hasBOM bool, scope *materialsOperationScope) (MaterialTextSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return MaterialTextSnapshot{}, err
	}
	data := materialTextBytes(text, hasBOM)
	if _, err := materialTextSnapshotFromBytes(path, nil, data); err != nil {
		return MaterialTextSnapshot{}, err
	}
	root, err := validatedMaterialsRoot(root)
	if err != nil {
		return MaterialTextSnapshot{}, err
	}
	current, err := s.MaterialTextSnapshot(root, path)
	if err != nil {
		return MaterialTextSnapshot{}, err
	}
	if baselineHash == "" || current.Hash != baselineHash {
		return current, ErrConflict
	}
	path = current.Path
	if current.Hash == Hash(data) {
		return current, nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		return MaterialTextSnapshot{}, err
	}
	parent := filepath.Dir(path)
	if err = scope.revalidateDestination(root, relativeMaterials(root, parent), parent); err != nil {
		return MaterialTextSnapshot{}, err
	}
	staging, err := stagingPath(parent, "import", ".tmp")
	if err != nil {
		return MaterialTextSnapshot{}, err
	}
	if err = writeNewFile(ctx, staging, data, info.Mode().Perm()); err != nil {
		return MaterialTextSnapshot{}, err
	}
	defer os.Remove(staging)
	if err = ctx.Err(); err != nil {
		return MaterialTextSnapshot{}, err
	}
	latest, err := s.MaterialTextSnapshot(root, path)
	if err != nil {
		return MaterialTextSnapshot{}, err
	}
	if latest.Hash != baselineHash {
		return latest, ErrConflict
	}
	if err = scope.revalidateDestination(root, relativeMaterials(root, parent), parent); err != nil {
		return MaterialTextSnapshot{}, err
	}
	if err = ctx.Err(); err != nil {
		return MaterialTextSnapshot{}, err
	}
	if err = os.Rename(staging, path); err != nil {
		return MaterialTextSnapshot{}, err
	}
	return materialTextSnapshotFromBytes(path, nil, data)
}
