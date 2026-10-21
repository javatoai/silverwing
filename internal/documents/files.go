// Package documents manages native material files without a browser runtime.
package documents

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"silverwing-go/internal/storage"
)

const MaxMaterialsText = 3 * 1024 * 1024
const MaxEditableMarkdown = MaxMaterialsText
const MaxTextPreview = 2 * 1024 * 1024
const MaxMaterialsPreview = MaxMaterialsText

var ErrConflict = errors.New("文件已被外部修改，当前草稿尚未覆盖外部内容")
var errTextPreviewTooLarge = errors.New("文本预览不能超过 2 MiB，请使用外部编辑器")
var errMaterialsPreviewTooLarge = errors.New("资料文本不能超过 3 MiB（含 BOM）")
var stagingName = regexp.MustCompile(`(?i)^\.silverwing-(?:import-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.tmp|(?:rename|paste)-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)

type Entry struct {
	Path     string  `json:"path"`
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	Size     int64   `json:"size"`
	Modified string  `json:"modified"`
	Children []Entry `json:"children,omitempty"`
}
type Content struct {
	Path       string             `json:"path"`
	Kind       string             `json:"kind"`
	Text       string             `json:"text"`
	Hash       string             `json:"hash"`
	Size       int64              `json:"size"`
	Modified   string             `json:"modified"`
	Rows       [][]string         `json:"rows,omitempty"`
	ReadOnly   bool               `json:"readOnly"`
	Draft      *MarkdownDraft     `json:"draft,omitempty"`
	TextDraft  *MaterialTextDraft `json:"textDraft,omitempty"`
	DraftError string             `json:"draftError,omitempty"`
	HasUTF8BOM bool               `json:"hasUtf8Bom,omitempty"`
	Newline    string             `json:"newline,omitempty"`
}
type Service struct {
	CacheRoot string
	draftMu   sync.Mutex
}

func NewService(cacheRoot string) *Service { return &Service{CacheRoot: cacheRoot} }

func Kind(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return "markdown"
	case ".json":
		return "json"
	case ".csv":
		return "table"
	case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".bmp", ".ico":
		return "image"
	case ".pdf":
		return "pdf"
	case ".txt",
		".java", ".kt", ".kts", ".gradle",
		".properties", ".ini", ".conf",
		".html", ".htm", ".xml", ".svg",
		".css", ".scss", ".sass", ".less",
		".js", ".jsx", ".ts", ".tsx",
		".yaml", ".yml", ".sql",
		".go", ".rs", ".c", ".h", ".cc", ".cpp", ".hpp", ".cs", ".swift", ".rb", ".php", ".lua", ".toml", ".diff", ".patch",
		".py", ".pyw", ".pyi",
		".ps1", ".psm1", ".psd1",
		".bat", ".cmd", ".sh", ".bash", ".zsh":
		return "text"
	default:
		return "unsupported"
	}
}

func (s *Service) List(root string) ([]Entry, error) {
	root, err := validatedMaterialsRoot(root)
	if err != nil {
		return nil, err
	}
	return list(root, root, 0)
}
func list(root, directory string, depth int) ([]Entry, error) {
	if depth > 64 {
		return nil, errors.New("资料目录嵌套超过 64 层")
	}
	children, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(children))
	for _, child := range children {
		if stagingName.MatchString(child.Name()) || strings.EqualFold(child.Name(), ".git") ||
			!child.IsDir() && hiddenMaterialsMetadataName(child.Name()) {
			continue
		}
		path := filepath.Join(directory, child.Name())
		info, e := os.Lstat(path)
		if e != nil {
			return nil, e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if !sameResolvedPath(path) {
			continue
		}
		entry := Entry{Path: path, Name: child.Name(), Kind: Kind(path), Size: info.Size(), Modified: info.ModTime().Format(time.RFC3339Nano)}
		if info.IsDir() {
			entry.Kind = "directory"
			entry.Children, e = list(root, path, depth+1)
			if e != nil {
				return nil, e
			}
		}
		entries = append(entries, entry)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if (entries[i].Kind == "directory") != (entries[j].Kind == "directory") {
			return entries[i].Kind == "directory"
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return entries, nil
}

func hiddenMaterialsMetadataName(name string) bool {
	switch strings.ToLower(name) {
	case ".ds_store", "thumbs.db", "ehthumbs.db", "desktop.ini":
		return true
	default:
		return len(name) > 2 && strings.HasPrefix(name, "._")
	}
}

func (s *Service) Read(root, path string) (Content, error) {
	return s.read(root, path, MaxTextPreview, decodeText, false)
}

// ReadMaterialsPreview follows the materials preview contract: a 3 MiB
// limit and strict UTF-8 for declared text formats. Unknown formats are checked
// as read-only text, also accepting the existing BOM-declared UTF-16 decoder.
func (s *Service) ReadMaterialsPreview(root, path string) (Content, error) {
	content, err := s.read(root, path, MaxMaterialsPreview, decodeMaterialsPreviewText, true)
	if errors.Is(err, errTextPreviewTooLarge) {
		return content, errMaterialsPreviewTooLarge
	}
	return content, err
}

func (s *Service) read(root, path string, maxBytes int64, decoder func([]byte) (string, error), detectUnknownText bool) (Content, error) {
	root, err := validatedMaterialsRoot(root)
	if err != nil {
		return Content{}, err
	}
	path, err = safePath(root, path, false)
	if err != nil {
		return Content{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return Content{}, err
	}
	if err := validateMaterialPath(root, path, info); err != nil {
		return Content{}, err
	}
	if !info.Mode().IsRegular() {
		return Content{}, errors.New("只能读取普通文件")
	}
	kind := Kind(path)
	content := Content{Path: path, Kind: kind, Size: info.Size(), Modified: info.ModTime().Format(time.RFC3339Nano)}
	unknown := kind == "unsupported" && detectUnknownText
	if kind == "image" || kind == "pdf" || kind == "unsupported" && !unknown {
		content.ReadOnly = true
		return content, nil
	}
	if info.Size() > maxBytes {
		return content, errTextPreviewTooLarge
	}
	file, err := openMaterialFileNoFollow(path, info)
	if err != nil {
		return content, err
	}
	data, readErr := readTextPreview(file, maxBytes)
	closeErr := file.Close()
	if readErr != nil {
		return content, readErr
	}
	if closeErr != nil {
		return content, closeErr
	}
	content.Size = int64(len(data))
	if unknown {
		content.ReadOnly = true
		if text, ok := detectedMaterialsText(data); ok {
			content.Kind, content.Text, content.Hash = "text", text, Hash(data)
		}
		return content, nil
	}
	content.Hash = Hash(data)
	if detectUnknownText && len(data) >= 2 && (string(data[:2]) == "\xff\xfe" || string(data[:2]) == "\xfe\xff") {
		content.ReadOnly = true
		if text, ok := detectedMaterialsText(data); ok {
			content.Text = text
			return content, nil
		}
	}
	content.Text, err = decoder(data)
	if err != nil {
		return content, err
	}
	content.ReadOnly = kind == "markdown" && content.Size > MaxEditableMarkdown
	if kind == "table" {
		reader := csv.NewReader(strings.NewReader(content.Text))
		reader.FieldsPerRecord = -1
		// CSV parsing belongs to the preview. A malformed table is still a
		// readable UTF-8 document, so its original text must reach the UI's
		// parse-error fallback instead of failing document.read.
		if rows, parseErr := reader.ReadAll(); parseErr == nil {
			content.Rows = rows
		}
	}
	return content, nil
}

func readTextPreview(reader io.Reader, maxBytes int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, errTextPreviewTooLarge
	}
	return data, nil
}

func (s *Service) Save(root, path, text, expectedHash string, overwrite bool) (Content, error) {
	root, err := validatedMaterialsRoot(root)
	if err != nil {
		return Content{}, err
	}
	path, err = safePath(root, path, false)
	if err != nil {
		return Content{}, err
	}
	kind := Kind(path)
	if EditableMaterialText(path) {
		current, err := s.MaterialTextSnapshot(root, path)
		if err != nil {
			return Content{}, err
		}
		if overwrite {
			expectedHash = current.Hash
		}
		snapshot, err := s.SaveMaterialText(context.Background(), root, path, text, expectedHash, current.HasUTF8BOM)
		if err == nil {
			_ = os.Remove(s.draftPath(path))
		}
		return Content{Path: snapshot.Path, Kind: kind, Text: snapshot.Text, Hash: snapshot.Hash, Size: snapshot.Size, Modified: snapshot.Modified, HasUTF8BOM: snapshot.HasUTF8BOM, Newline: snapshot.Newline}, err
	}
	if kind != "markdown" {
		return Content{}, errors.New("该文件类型不支持文本编辑")
	}
	if kind == "markdown" && len([]byte(text)) > MaxEditableMarkdown {
		return Content{}, errors.New("单个 Markdown 编辑支持到 3 MiB（含 BOM）")
	}
	if len([]byte(text)) > MaxMaterialsText {
		return Content{}, errMaterialsPreviewTooLarge
	}
	if kind == "markdown" {
		if !overwrite {
			_, err = s.SaveMarkdown(context.Background(), root, relativeMaterials(root, path), text, expectedHash)
		} else {
			current, snapErr := s.MarkdownSnapshot(root, relativeMaterials(root, path))
			if snapErr != nil {
				return Content{}, snapErr
			}
			_, err = s.SaveMarkdown(context.Background(), root, relativeMaterials(root, path), text, current.Hash)
		}
		if err != nil {
			return Content{}, err
		}
		_ = os.Remove(s.draftPath(path))
		return s.ReadMaterialsPreview(root, path)
	}
	return Content{}, errors.New("该文件类型不支持文本编辑")
}

func (s *Service) Create(root, path string, data []byte, directory bool) (string, error) {
	root, err := validatedMaterialsRoot(root)
	if err != nil {
		return "", err
	}
	path, err = safePath(root, path, true)
	if err != nil {
		return "", err
	}
	if path == filepath.Clean(root) {
		return "", errors.New("不能替换资料根目录")
	}
	if err = validateName(filepath.Base(path)); err != nil {
		return "", err
	}
	if directory {
		err = os.Mkdir(path, 0o755)
	} else {
		var file *os.File
		file, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			_, err = file.Write(data)
			if err == nil {
				err = file.Sync()
			}
			closeErr := file.Close()
			if err == nil {
				err = closeErr
			}
		}
	}
	if err != nil {
		return "", err
	}
	return path, nil
}

func (s *Service) Rename(root, path, newName string) (string, error) {
	root, err := validatedMaterialsRoot(root)
	if err != nil {
		return "", err
	}
	path, err = safePath(root, path, false)
	if err != nil {
		return "", err
	}
	if err = validateName(newName); err != nil {
		return "", err
	}
	if path == filepath.Clean(root) {
		return "", errors.New("不能重命名资料根目录")
	}
	target, err := safePath(root, filepath.Join(filepath.Dir(path), newName), true)
	if err != nil {
		return "", err
	}
	if _, e := os.Lstat(target); e == nil {
		return "", errors.New("同名文件已存在，请选择新的名称")
	} else if !os.IsNotExist(e) {
		return "", e
	}
	if err = os.Rename(path, target); err != nil {
		return "", err
	}
	return target, nil
}

func (s *Service) Copy(root, source, destination string) (string, error) {
	root, err := validatedMaterialsRoot(root)
	if err != nil {
		return "", err
	}
	source, err = safePath(root, source, false)
	if err != nil {
		return "", err
	}
	destination, err = safePath(root, destination, true)
	if err != nil {
		return "", err
	}
	rel, _ := filepath.Rel(source, destination)
	if rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("不能复制到自身或其子目录")
	}
	if err = copyNew(source, destination); err != nil {
		return "", err
	}
	return destination, nil
}
func (s *Service) Import(root, source, destination string) (string, error) {
	root, err := validatedMaterialsRoot(root)
	if err != nil {
		return "", err
	}
	source, _, err = validateImportSource(source)
	if err != nil {
		return "", err
	}
	destination, err = safePath(root, destination, true)
	if err != nil {
		return "", err
	}
	if err = copyNew(source, destination); err != nil {
		return "", err
	}
	return destination, nil
}
func copyNew(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("不能复制符号链接")
	}
	if info.IsDir() {
		if err = os.Mkdir(destination, 0o755); err != nil {
			return err
		}
		children, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, child := range children {
			if err = copyNew(filepath.Join(source, child.Name()), filepath.Join(destination, child.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return errors.New("只能复制普通文件或目录")
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	return err
}

func (s *Service) CacheDraft(path, text string) error {
	return atomicWrite(s.draftPath(path), []byte(text), 0o600)
}
func (s *Service) Draft(path string) (string, error) {
	data, err := os.ReadFile(s.draftPath(path))
	return string(data), err
}
func (s *Service) draftPath(path string) string {
	return filepath.Join(s.CacheRoot, "markdown-drafts", Hash([]byte(filepath.Clean(path)))+".md")
}
func Hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func safePath(root, path string, missing bool) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	root = filepath.Clean(root)
	if hasGitSegment(root) {
		return "", errors.New("不能操作 Git 内部目录")
	}
	if err := validateRealAncestors(root, false); err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || hasGitSegment(path) {
		return "", errors.New("文件路径超出资料目录")
	}
	// Every ancestor must be a real directory; do not follow links/reparse paths.
	for cursor := path; ; cursor = filepath.Dir(cursor) {
		info, e := os.Lstat(cursor)
		if e != nil {
			if !missing || !os.IsNotExist(e) || cursor != path {
				return "", e
			}
		} else {
			if info.Mode()&os.ModeSymlink != 0 {
				return "", errors.New("不能操作符号链接资料路径")
			}
			if !sameResolvedPath(cursor) {
				return "", errors.New("资料路径不能包含目录链接或 junction")
			}
			if cursor != path && !info.IsDir() {
				return "", errors.New("资料路径的上级必须是目录")
			}
		}
		if cursor == root {
			break
		}
		if filepath.Dir(cursor) == cursor {
			return "", errors.New("资料根目录不合法")
		}
	}
	return path, nil
}

// ValidateFileName applies the same filename rules before a desktop operation
// captures a draft lease and when the file service performs the operation.
func ValidateFileName(name string) error { return validateName(name) }

func validateName(name string) error {
	if strings.TrimSpace(name) == "" || len(utf16.Encode([]rune(name))) > 255 || !utf8.ValidString(name) || name == "." || name == ".." || strings.EqualFold(name, ".git") || stagingName.MatchString(name) {
		return errors.New("请输入有效名称，不能使用 .、.. 或 .git。")
	}
	if strings.ContainsAny(name, `<>:"/\|?*`+"\x00\r\n") || strings.IndexFunc(name, func(r rune) bool { return r < 32 }) >= 0 || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return errors.New("名称不能含路径分隔符或 <>:\"|?*，也不能以空格或句点结尾。")
	}
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	reserved := regexp.MustCompile(`^(?:CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])$`)
	if reserved.MatchString(base) {
		return errors.New("该名称是系统保留名称，请换一个名称。")
	}
	return nil
}
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return storage.WriteFileAtomic(path, data, mode)
}
func decodeText(data []byte) (string, error) {
	if len(data) >= 3 && string(data[:3]) == "\xef\xbb\xbf" {
		data = data[3:]
	}
	if len(data) >= 2 && (string(data[:2]) == "\xff\xfe" || string(data[:2]) == "\xfe\xff") {
		little := data[0] == 0xff
		data = data[2:]
		if len(data)%2 != 0 {
			return "", errors.New("UTF-16 文件长度不合法")
		}
		units := make([]uint16, len(data)/2)
		for i := range units {
			if little {
				units[i] = uint16(data[2*i]) | uint16(data[2*i+1])<<8
			} else {
				units[i] = uint16(data[2*i])<<8 | uint16(data[2*i+1])
			}
		}
		return string(utf16.Decode(units)), nil
	}
	if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
		return "", errors.New("文件不是有效 UTF-8 / UTF-16 文本")
	}
	return string(data), nil
}

func decodeMaterialsPreviewText(data []byte) (string, error) {
	if len(data) >= 3 && string(data[:3]) == "\xef\xbb\xbf" {
		data = data[3:]
	}
	if !utf8.Valid(data) {
		return "", errors.New("文件不是有效的 UTF-8 文本")
	}
	return string(data), nil
}
func EncodeBinary(data []byte) string { return base64.StdEncoding.EncodeToString(data) }
