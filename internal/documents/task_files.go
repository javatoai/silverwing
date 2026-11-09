package documents

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const TaskFilesSearchLimit = 500

// Extra text formats are scoped to the read-only task browser. Do not broaden
// the editable materials service's type or permission policy.
func taskFileKind(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jsonl", ".iml", ".log":
		return "text"
	default:
		return Kind(name)
	}
}

type TaskFileEntry struct {
	Path             string `json:"path"`
	Name             string `json:"name"`
	Kind             string `json:"kind"`
	Directory        bool   `json:"directory"`
	RestrictedReason string `json:"restrictedReason,omitempty"`
	Size             int64  `json:"size"`
	Modified         string `json:"modified"`
}
type TaskFilesDirectory struct {
	Root    string          `json:"root"`
	Path    string          `json:"path"`
	Entries []TaskFileEntry `json:"entries"`
}
type TaskFilesIssue struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}
type TaskFilesSearchResult struct {
	Root      string           `json:"root"`
	Query     string           `json:"query"`
	Entries   []TaskFileEntry  `json:"entries"`
	Truncated bool             `json:"truncated"`
	Issues    []TaskFilesIssue `json:"issues,omitempty"`
}
type TaskFilesReadResult struct {
	Root           string  `json:"root"`
	RelativePath   string  `json:"relativePath"`
	Content        Content `json:"content"`
	FallbackReason string  `json:"fallbackReason,omitempty"`
	Identity       string  `json:"identity,omitempty"`
	MediaBytes     []byte  `json:"mediaBytes,omitempty"`
	MediaHash      string  `json:"mediaHash,omitempty"`
}

// TaskFilesScope is a read-only, operation-lifetime capability for one task.
// It neither constructs a materials scope nor opens a draft/cache store.
type TaskFilesScope struct {
	public, real string
	root         *os.Root
	identity     os.FileInfo
}

func OpenTaskFilesScope(taskPath string) (*TaskFilesScope, error) {
	public, err := filepath.Abs(taskPath)
	if err != nil {
		return nil, err
	}
	real, err := validatedLocalTaskPath(public)
	if err != nil {
		return nil, err
	}
	if hasGitSegment(public) || hasGitSegment(real) {
		return nil, errors.New("Git 内部目录不支持任务文件浏览")
	}
	info, err := os.Lstat(real)
	if err != nil || !os.SameFile(info, info) {
		return nil, errors.New("无法确认任务文件根目录身份")
	}
	root, err := os.OpenRoot(real)
	if err != nil {
		return nil, err
	}
	s := &TaskFilesScope{public: filepath.Clean(public), real: real, root: root, identity: info}
	if err := s.Check(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *TaskFilesScope) Close() {
	if s != nil && s.root != nil {
		_ = s.root.Close()
		s.root = nil
	}
}
func (s *TaskFilesScope) Check() error {
	if s == nil || s.root == nil {
		return errors.New("任务文件读取已结束")
	}
	real, err := validatedLocalTaskPath(s.public)
	if err != nil || !samePathName(real, s.real) {
		return errors.New("任务文件根目录已变化，请刷新后重试")
	}
	info, err := s.root.Stat(".")
	current, namedErr := os.Lstat(s.real)
	if err != nil || namedErr != nil || !os.SameFile(s.identity, info) || !os.SameFile(info, current) {
		return errors.New("任务文件根目录身份已变化，请刷新后重试")
	}
	return nil
}

func taskFilesRelative(path string) (string, error) {
	return taskFilesRelativeForOperation(path, false)
}

// System launch may target the .git leaf itself, never anything beneath it.
// Directory enumeration and content reads keep the stricter validator.
func taskFilesRelativeForOperation(path string, allowGitLeaf bool) (string, error) {
	path = strings.ReplaceAll(path, `\`, "/")
	if path == "" || path == "." {
		return "", nil
	}
	if strings.HasPrefix(path, "/") || strings.ContainsAny(path, ":\x00") || filepath.IsAbs(path) || filepath.VolumeName(path) != "" {
		return "", errors.New("任务文件必须使用任务内相对路径")
	}
	parts := strings.Split(path, "/")
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("任务文件路径不能包含上级目录或空路径段")
		}
		if strings.EqualFold(strings.TrimRight(part, " ."), ".git") && !(allowGitLeaf && index == len(parts)-1 && strings.EqualFold(part, ".git")) {
			return "", errors.New("Git 内部目录只展示，不支持访问")
		}
		if runtime.GOOS == "windows" && strings.TrimRight(part, " .") != part {
			return "", errors.New("任务文件路径不能使用目录别名")
		}
	}
	return path, nil
}

type taskFilesAncestor struct {
	relative string
	info     os.FileInfo
}
type taskFilesDirectoryHandle struct {
	scope     *TaskFilesScope
	root      *os.Root
	file      *os.File
	ancestors []taskFilesAncestor
}

func (d *taskFilesDirectoryHandle) close() {
	if d.file != nil {
		_ = d.file.Close()
	}
	if d.root != nil {
		_ = d.root.Close()
	}
}
func (d *taskFilesDirectoryHandle) check() error {
	if err := d.scope.Check(); err != nil {
		return err
	}
	for _, expected := range d.ancestors {
		name := filepath.FromSlash(expected.relative)
		path := filepath.Join(d.scope.real, name)
		if err := checkTaskFilesDirectory(d.scope.root, name, path, expected.info); err != nil {
			return err
		}
	}
	return nil
}

func checkTaskFilesDirectory(parent *os.Root, name, path string, expected os.FileInfo) error {
	info, err := parent.Lstat(name)
	if err != nil || !info.IsDir() || isUnsafeDirectory(info, path) || !os.SameFile(expected, info) {
		return errors.New("任务文件目录已变化或包含链接")
	}
	canonical, err := canonicalLocalTaskPath(path)
	if err != nil || !samePathName(canonical, path) {
		return errors.New("任务文件目录存在重定向或别名")
	}
	return nil
}

func (s *TaskFilesScope) directory(ctx context.Context, relative string) (*taskFilesDirectoryHandle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.Check(); err != nil {
		return nil, err
	}
	relative, err := taskFilesRelative(relative)
	if err != nil {
		return nil, err
	}
	d := &taskFilesDirectoryHandle{scope: s}
	prefix := ""
	if relative != "" {
		for _, part := range strings.Split(relative, "/") {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if prefix != "" {
				prefix += "/"
			}
			prefix += part
			info, err := s.root.Lstat(filepath.FromSlash(prefix))
			path := filepath.Join(s.real, filepath.FromSlash(prefix))
			if err != nil {
				return nil, err
			}
			if !info.IsDir() || isUnsafeDirectory(info, path) || !os.SameFile(info, info) {
				return nil, errors.New("任务文件目录包含链接或不是普通目录")
			}
			d.ancestors = append(d.ancestors, taskFilesAncestor{prefix, info})
		}
	}
	name := filepath.FromSlash(relative)
	if name == "" {
		name = "."
	}
	expected := s.identity
	if len(d.ancestors) > 0 {
		expected = d.ancestors[len(d.ancestors)-1].info
	}
	if err := d.open(s.root, name, expected); err != nil {
		return nil, err
	}
	if err := d.check(); err != nil {
		d.close()
		return nil, err
	}
	return d, nil
}

func (d *taskFilesDirectoryHandle) open(parent *os.Root, name string, expected os.FileInfo) error {
	var err error
	d.root, err = parent.OpenRoot(name)
	if err != nil {
		return err
	}
	d.file, err = d.root.Open(".")
	if err != nil {
		d.close()
		return err
	}
	opened, err := d.file.Stat()
	if err != nil || !os.SameFile(expected, opened) {
		d.close()
		return errors.New("打开任务文件目录时身份发生变化")
	}
	return nil
}

func taskFilesChildPath(parent, name string) string {
	if parent != "" {
		return parent + "/" + name
	}
	return name
}

func (s *TaskFilesScope) entry(d *taskFilesDirectoryHandle, relative string, child os.DirEntry) (TaskFileEntry, os.FileInfo) {
	entry := TaskFileEntry{Path: relative, Name: child.Name(), Kind: taskFileKind(child.Name()), Directory: child.IsDir()}
	info, err := d.root.Lstat(child.Name())
	if err != nil {
		entry.RestrictedReason = "无法读取文件信息：" + err.Error()
		return entry, nil
	}
	entry.Directory, entry.Size, entry.Modified = info.IsDir(), info.Size(), info.ModTime().Format(time.RFC3339Nano)
	if entry.Directory {
		entry.Kind = "directory"
	}
	if isUnsafeDirectory(info, filepath.Join(s.real, filepath.FromSlash(relative))) {
		entry.RestrictedReason = "链接或重解析点，不支持访问"
	} else if !info.IsDir() && !info.Mode().IsRegular() {
		entry.RestrictedReason = "特殊文件，不支持访问"
	} else if strings.EqualFold(child.Name(), ".git") {
		// The tree exposes Git metadata as an opaque system-open item, whether
		// it is a repository directory or a worktree pointer file.
		entry.Kind, entry.Directory = "external", false
	}
	return entry, info
}

func (s *TaskFilesScope) List(ctx context.Context, relative string) (TaskFilesDirectory, error) {
	relative, err := taskFilesRelative(relative)
	if err != nil {
		return TaskFilesDirectory{}, err
	}
	d, err := s.directory(ctx, relative)
	if err != nil {
		return TaskFilesDirectory{}, err
	}
	defer d.close()
	result := TaskFilesDirectory{Root: s.public, Path: relative, Entries: []TaskFileEntry{}}
	for {
		children, readErr := d.file.ReadDir(128)
		for _, child := range children {
			if err := ctx.Err(); err != nil {
				return TaskFilesDirectory{}, err
			}
			entry, _ := s.entry(d, taskFilesChildPath(relative, child.Name()), child)
			result.Entries = append(result.Entries, entry)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return TaskFilesDirectory{}, readErr
		}
	}
	sort.Slice(result.Entries, func(i, j int) bool {
		a, b := result.Entries[i], result.Entries[j]
		if a.Directory != b.Directory {
			return a.Directory
		}
		left, right := strings.ToLower(a.Name), strings.ToLower(b.Name)
		if left == right {
			return a.Name < b.Name
		}
		return left < right
	})
	if err := ctx.Err(); err != nil {
		return TaskFilesDirectory{}, err
	}
	if err := d.check(); err != nil {
		return TaskFilesDirectory{}, err
	}
	return result, nil
}

var errTaskFilesSearchLimit = errors.New("task file search result limit")

func (s *TaskFilesScope) Search(ctx context.Context, query string, limit int) (TaskFilesSearchResult, error) {
	result := TaskFilesSearchResult{Root: s.public, Query: query, Entries: []TaskFileEntry{}}
	if query == "" {
		return result, errors.New("请输入文件名或相对路径关键词")
	}
	if limit <= 0 || limit > TaskFilesSearchLimit {
		limit = TaskFilesSearchLimit
	}
	if err := ctx.Err(); err != nil {
		return TaskFilesSearchResult{}, err
	}
	// All directory opens are relative to the same pinned root. Validate its
	// configured path once here and again before publishing the results.
	if err := s.Check(); err != nil {
		return TaskFilesSearchResult{}, err
	}
	needle := strings.ToLower(query)
	addIssue := func(relative string, err error) error {
		if relative == "" {
			return err
		}
		if len(result.Issues) < 32 {
			result.Issues = append(result.Issues, TaskFilesIssue{relative, err.Error()})
		}
		return nil
	}
	var visit func(*os.Root, string, string, os.FileInfo) error
	visit = func(parent *os.Root, name, relative string, expected os.FileInfo) (visitErr error) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if relative != "" {
			validated, err := taskFilesRelative(relative)
			if err != nil {
				return addIssue(relative, err)
			}
			if validated != relative {
				return addIssue(relative, errors.New("任务文件目录存在重定向或别名"))
			}
		}
		d := &taskFilesDirectoryHandle{scope: s}
		if err := d.open(parent, name, expected); err != nil {
			if canceled := ctx.Err(); canceled != nil {
				return canceled
			}
			return addIssue(relative, err)
		}
		defer d.close()
		if relative != "" {
			path := filepath.Join(s.real, filepath.FromSlash(relative))
			if err := checkTaskFilesDirectory(parent, name, path, expected); err != nil {
				return addIssue(relative, err)
			}
			// A child is checked against its pinned parent. Each frame checks
			// its own link in the chain on unwind, even when the limit is hit.
			defer func() {
				if err := checkTaskFilesDirectory(parent, name, path, expected); err != nil {
					visitErr = err
				}
			}()
		}
		for {
			children, readErr := d.file.ReadDir(128)
			for _, child := range children {
				if err := ctx.Err(); err != nil {
					return err
				}
				path := taskFilesChildPath(relative, child.Name())
				matches := strings.Contains(strings.ToLower(path), needle)
				if !matches && !child.IsDir() {
					continue
				}
				// Only results and candidate directories need a fresh no-follow stat.
				entry, info := s.entry(d, path, child)
				if matches {
					if len(result.Entries) == limit {
						result.Truncated = true
						return errTaskFilesSearchLimit
					}
					result.Entries = append(result.Entries, entry)
				}
				if entry.Directory && entry.RestrictedReason == "" {
					if err := visit(d.root, entry.Name, entry.Path, info); err != nil {
						return err
					}
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return addIssue(relative, readErr)
			}
		}
		return nil
	}
	err := visit(s.root, ".", "", s.identity)
	if err != nil && err != errTaskFilesSearchLimit {
		return TaskFilesSearchResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return TaskFilesSearchResult{}, err
	}
	if err := s.Check(); err != nil {
		return TaskFilesSearchResult{}, err
	}
	return result, nil
}

type taskFilesContextReader struct {
	context context.Context
	reader  io.Reader
}

func (r taskFilesContextReader) Read(b []byte) (int, error) {
	if err := r.context.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(b)
}

func (s *TaskFilesScope) Read(ctx context.Context, relative string) (TaskFilesReadResult, error) {
	return s.read(ctx, relative, 0)
}

// ReadMedia captures immutable media bytes from the same pinned, no-follow
// handle used for metadata validation. The application supplies its fixed
// image/PDF budget; no client-provided byte limit is accepted at that boundary.
func (s *TaskFilesScope) ReadMedia(ctx context.Context, relative string, maxBytes int64) (TaskFilesReadResult, error) {
	if maxBytes <= 0 || maxBytes == 1<<63-1 {
		return TaskFilesReadResult{}, errors.New("媒体预览字节上限必须为有效正数")
	}
	return s.read(ctx, relative, maxBytes)
}

func (s *TaskFilesScope) read(ctx context.Context, relative string, mediaMaxBytes int64) (TaskFilesReadResult, error) {
	relative, err := taskFilesRelative(relative)
	if err != nil || relative == "" {
		return TaskFilesReadResult{}, errors.New("请选择任务内普通文件")
	}
	parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(relative)))
	if parent == "." {
		parent = ""
	}
	d, err := s.directory(ctx, parent)
	if err != nil {
		return TaskFilesReadResult{}, err
	}
	defer d.close()
	name := filepath.Base(filepath.FromSlash(relative))
	info, err := d.root.Lstat(name)
	if err != nil {
		return TaskFilesReadResult{}, err
	}
	physical := filepath.Join(s.real, filepath.FromSlash(relative))
	if !info.Mode().IsRegular() || isUnsafeDirectory(info, physical) {
		return TaskFilesReadResult{}, errors.New("任务文件是链接、重解析点或非普通文件")
	}
	file, err := openMaterialFileNoFollow(physical, info)
	if err != nil {
		return TaskFilesReadResult{}, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return TaskFilesReadResult{}, err
	}
	if opened.Size() != info.Size() || !opened.ModTime().Equal(info.ModTime()) {
		return TaskFilesReadResult{}, errors.New("任务文件在读取期间已变化，请重试")
	}
	identity, err := taskFilesFileIdentity(file)
	if err != nil {
		return TaskFilesReadResult{}, err
	}
	content := Content{Path: filepath.Join(s.public, filepath.FromSlash(relative)), Kind: taskFileKind(relative), Size: info.Size(), Modified: info.ModTime().Format(time.RFC3339Nano), ReadOnly: true}
	result := TaskFilesReadResult{Root: s.public, RelativePath: relative, Content: content, Identity: identity}
	if mediaMaxBytes > 0 {
		if content.Kind != "image" && content.Kind != "pdf" {
			return TaskFilesReadResult{}, errors.New("媒体字节快照只支持图片或 PDF")
		}
		if info.Size() > mediaMaxBytes {
			result.FallbackReason = "媒体文件超过预览字节上限，请用系统应用打开"
		} else {
			data, readErr := io.ReadAll(io.LimitReader(taskFilesContextReader{ctx, file}, mediaMaxBytes+1))
			if readErr != nil {
				return TaskFilesReadResult{}, readErr
			}
			if int64(len(data)) > mediaMaxBytes {
				result.FallbackReason = "媒体文件超过预览字节上限，请用系统应用打开"
			} else {
				if int64(len(data)) != opened.Size() {
					return TaskFilesReadResult{}, errors.New("任务文件在读取期间已变化，请重试")
				}
				result.MediaBytes, result.MediaHash = data, Hash(data)
			}
		}
	} else if content.Kind == "unsupported" {
		// An Office/unknown extension stays unsupported even when its contents
		// happen to be valid text. Do not turn an external-open format into code.
		result.FallbackReason = "文件格式暂不支持内置预览，请用系统应用打开"
	} else if content.Kind != "image" && content.Kind != "pdf" {
		if info.Size() > MaxMaterialsPreview {
			result.FallbackReason = "文件超过 3 MiB 预览上限，请用系统应用打开"
		} else {
			data, err := readTextPreview(taskFilesContextReader{ctx, file}, MaxMaterialsPreview)
			if errors.Is(err, errTextPreviewTooLarge) {
				result.FallbackReason = "文件超过 3 MiB 预览上限，请用系统应用打开"
			} else if err != nil {
				return TaskFilesReadResult{}, err
			} else {
				result.Content.Size = int64(len(data))
				result.Content.Hash = Hash(data)
				if text, ok := detectedMaterialsText(data); ok {
					result.Content.Text = text
					result.Content.HasUTF8BOM = len(data) >= 3 && string(data[:3]) == "\xef\xbb\xbf"
					if at := strings.IndexAny(text, "\r\n"); at >= 0 {
						result.Content.Newline = text[at : at+1]
						if text[at] == '\r' && at+1 < len(text) && text[at+1] == '\n' {
							result.Content.Newline = "\r\n"
						}
					}
					if content.Kind == "table" {
						reader := csv.NewReader(strings.NewReader(text))
						reader.FieldsPerRecord = -1
						if rows, err := reader.ReadAll(); err == nil {
							result.Content.Rows = rows
						}
					}
				} else {
					result.FallbackReason = "文件格式不适合内置文本预览，请用系统应用打开"
				}
			}
		}
	}
	current, err := d.root.Lstat(name)
	after, statErr := file.Stat()
	if err != nil || statErr != nil || isUnsafeDirectory(current, physical) || !os.SameFile(opened, current) || !os.SameFile(opened, after) || opened.Size() != current.Size() || !opened.ModTime().Equal(current.ModTime()) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
		return TaskFilesReadResult{}, errors.New("任务文件在读取期间已变化，请重试")
	}
	if err := ctx.Err(); err != nil {
		return TaskFilesReadResult{}, err
	}
	if err := d.check(); err != nil {
		return TaskFilesReadResult{}, err
	}
	return result, nil
}

// WithPath validates an explicit external action without loading file contents.
// Its callback receives only the server-resolved physical path, never a UI path.
func (s *TaskFilesScope) WithPath(ctx context.Context, relative string, operation func(string, bool) error) error {
	relative, err := taskFilesRelativeForOperation(relative, true)
	if err != nil {
		return err
	}
	parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(relative)))
	if parent == "." {
		parent = ""
	}
	if relative == "" {
		parent = ""
	}
	d, err := s.directory(ctx, parent)
	if err != nil {
		return err
	}
	defer d.close()
	name := filepath.Base(filepath.FromSlash(relative))
	if relative == "" {
		name = "."
	}
	info, err := d.root.Lstat(name)
	if err != nil {
		return err
	}
	physical := filepath.Join(s.real, filepath.FromSlash(relative))
	if isUnsafeDirectory(info, physical) || (!info.IsDir() && !info.Mode().IsRegular()) {
		return errors.New("不能系统打开任务内链接或特殊文件")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.check(); err != nil {
		return err
	}
	if err := operation(physical, info.IsDir()); err != nil {
		return err
	}
	if err := d.check(); err != nil {
		return err
	}
	current, err := d.root.Lstat(name)
	if err != nil || isUnsafeDirectory(current, physical) || !os.SameFile(info, current) {
		return errors.New("系统操作期间任务文件身份已变化")
	}
	return nil
}
