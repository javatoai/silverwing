package desktop

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/media"
)

func taskFilesSamePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func (view *taskFilesView) acceptRoot(root string) error {
	if !filepath.IsAbs(root) || !taskFilesSamePath(root, view.identity.taskPath) ||
		(view.root != "" && !taskFilesSamePath(view.root, root)) {
		return errors.New("任务工作目录已变化，请重新打开任务文件")
	}
	view.root = root
	return nil
}

func validateTaskFilesDirectory(result application.TaskFilesDirectory, relative string) error {
	if result.Path != relative {
		return errors.New("目录读取位置不匹配，请重试")
	}
	seen := make(map[string]bool, len(result.Entries))
	for _, entry := range result.Entries {
		parent := path.Dir(entry.Path)
		if parent == "." {
			parent = ""
		}
		if _, ok := taskFilesRelative(entry.Path); !ok || entry.Path == "" || parent != relative ||
			entry.Name != path.Base(entry.Path) || seen[entry.Path] {
			return errors.New("目录返回了无效路径，请刷新")
		}
		seen[entry.Path] = true
	}
	return nil
}

func (s *app) requestTaskFilesDirectory(view *taskFilesView, relative string, refresh bool) {
	if !s.taskFilesCurrent(view) {
		return
	}
	if _, ok := taskFilesRelative(relative); !ok {
		return
	}
	dir := view.directory(relative)
	if dir.loading || (dir.loaded && !refresh) {
		return
	}
	dir.loading, dir.error = true, ""
	backend, action := s.backend, view.identity.action("task.files.list", relative)
	s.runTaskFilesJob(view, "list:"+relative, func(ctx context.Context) (any, error) {
		result, err := backend.Dispatch(ctx, action)
		if err != nil {
			return nil, err
		}
		return decodeTaskFilesResult[application.TaskFilesDirectory](result)
	}, func(value any, err error) {
		dir.loading = false
		if err == nil {
			result := value.(application.TaskFilesDirectory)
			err = view.acceptRoot(result.Root)
			if err == nil {
				err = validateTaskFilesDirectory(result, relative)
			}
			if err == nil {
				dir.entries, dir.loaded, dir.error = result.Entries, true, ""
				s.continueTaskFilesReveal(view)
				return
			}
		}
		dir.loaded, dir.error = false, err.Error()
	}, nil)
}

func (s *app) refreshTaskFiles(view *taskFilesView) {
	if !s.taskFilesCurrent(view) {
		return
	}
	s.taskFilesUnit.jobs.cancelView(view)
	view.epoch++
	view.retirePreview(&s.taskFilesUnit.jobs)
	view.operationError, view.operationMessage = "", ""
	view.searching = false
	view.searchDone, view.searchError = false, ""
	for _, dir := range view.directories {
		dir.loaded, dir.loading, dir.error = false, false, ""
	}
	s.requestTaskFilesDirectory(view, "", true)
	if view.opened != "" {
		s.openTaskFilePreview(view, view.opened, true)
	}
}

func (s *app) changeTaskFilesSearchQuery(view *taskFilesView) {
	if !s.taskFilesCurrent(view) || strings.TrimSpace(view.query) != "" {
		return
	}
	// Clearing the field is also an exit from search, including an in-flight
	// request. Its eventual receipt must not bring the old results back.
	s.cancelTaskFilesSearch(view)
	view.searchQuery = ""
	view.search = application.TaskFilesSearchResult{}
}

func (s *app) requestTaskFilesSearch(view *taskFilesView) {
	if !s.taskFilesCurrent(view) {
		return
	}
	query := view.query
	if strings.TrimSpace(query) == "" {
		s.cancelTaskFilesSearch(view)
		return
	}
	view.searching, view.searchDone, view.searchError = true, false, ""
	view.searchQuery = query
	view.search = application.TaskFilesSearchResult{}
	backend := s.backend
	action := view.identity.action("task.files.search", "")
	action.Payload["query"] = query
	s.runTaskFilesJob(view, "search", func(ctx context.Context) (any, error) {
		result, err := backend.Dispatch(ctx, action)
		if err != nil {
			return nil, err
		}
		return decodeTaskFilesResult[application.TaskFilesSearchResult](result)
	}, func(value any, err error) {
		view.searching = false
		if err == nil {
			result := value.(application.TaskFilesSearchResult)
			err = view.acceptRoot(result.Root)
			if err == nil && result.Query != query {
				err = errors.New("搜索返回了旧查询结果，请重试")
			}
			if err == nil {
				for _, entry := range result.Entries {
					if _, ok := taskFilesRelative(entry.Path); !ok || entry.Path == "" {
						err = errors.New("搜索返回了无效路径，请重试")
						break
					}
				}
			}
			if err == nil {
				view.search, view.searchDone = result, true
				return
			}
		}
		view.searchError = err.Error()
	}, nil)
}

func (s *app) cancelTaskFilesSearch(view *taskFilesView) {
	s.taskFilesUnit.jobs.cancel("search")
	view.searching, view.searchDone, view.searchError = false, false, ""
}

func (s *app) locateTaskFilesEntry(view *taskFilesView, entry application.TaskFileEntry) {
	if !s.taskFilesCurrent(view) {
		return
	}
	if entry.RestrictedReason != "" {
		view.operationError = entry.RestrictedReason
		return
	}
	if _, ok := taskFilesRelative(entry.Path); !ok || entry.Path == "" {
		return
	}
	// Keep the results available for a double click. The ancestor chain is
	// loaded now and revealed when the user returns to the directory tree.
	view.operationMessage = "已定位：" + entry.Path
	view.revealPath = entry.Path
	view.selected = entry.Path
	parent := path.Dir(entry.Path)
	if entry.Directory {
		parent = entry.Path
	}
	for parent != "." && parent != "" {
		view.expanded[parent] = true
		parent = path.Dir(parent)
	}
	s.continueTaskFilesReveal(view)
	if !entry.Directory {
		s.openTaskFilePreview(view, entry.Path, false)
	}
}

// Only materialize the ancestor chain of an explicitly selected search result.
// Each directory is still fetched independently, with a local retry state.
func (s *app) continueTaskFilesReveal(view *taskFilesView) {
	if view.revealPath == "" {
		return
	}
	parts := strings.Split(view.revealPath, "/")
	for i := 0; i < len(parts); i++ {
		relative := strings.Join(parts[:i], "/")
		dir := view.directory(relative)
		if !dir.loaded {
			if !dir.loading && dir.error == "" {
				s.requestTaskFilesDirectory(view, relative, false)
			}
			return
		}
		child := strings.Join(parts[:i+1], "/")
		var found *application.TaskFileEntry
		for index := range dir.entries {
			if dir.entries[index].Path == child {
				found = &dir.entries[index]
				break
			}
		}
		if found == nil || found.RestrictedReason != "" {
			view.operationError = "文件已移除或路径受限，请重新搜索"
			view.revealPath = ""
			return
		}
		if i < len(parts)-1 && !found.Directory {
			view.revealPath = ""
			return
		}
		if i == len(parts)-1 && found.Directory && view.expanded[child] {
			s.requestTaskFilesDirectory(view, child, false)
		}
	}
}

func (s *app) taskFilesExternalAction(view *taskFilesView, relative, kind string) {
	if !s.taskFilesCurrent(view) || (kind != "task.files.open" && kind != "task.files.reveal") {
		return
	}
	if _, ok := taskFilesRelative(relative); !ok {
		return
	}
	view.operationError, view.operationMessage = "", ""
	backend, action := s.backend, view.identity.action(kind, relative)
	s.runTaskFilesJob(view, "external", func(ctx context.Context) (any, error) {
		return backend.Dispatch(ctx, action)
	}, func(value any, err error) {
		if err != nil {
			view.operationError = err.Error()
			return
		}
		result := value.(application.ActionResult)
		view.operationMessage = result.Message
	}, nil)
}

func taskFilesReadMatches(expected, actual application.TaskFilesReadResult) bool {
	return expected.Identity != "" && expected.Identity == actual.Identity && expected.RelativePath == actual.RelativePath &&
		taskFilesSamePath(expected.Root, actual.Root) && taskFilesSamePath(expected.Content.Path, actual.Content.Path) &&
		expected.Content.Kind == actual.Content.Kind && expected.Content.Size == actual.Content.Size &&
		expected.Content.Modified == actual.Content.Modified && expected.Content.Hash == actual.Content.Hash && actual.Content.ReadOnly
}

func taskFilesRead(ctx context.Context, backend application.Backend, identity taskFilesIdentity, relative string) (application.TaskFilesReadResult, error) {
	result, err := backend.Dispatch(ctx, identity.action("task.files.read", relative))
	if err != nil {
		return application.TaskFilesReadResult{}, err
	}
	return taskFilesValidateRead(identity, relative, result)
}

func taskFilesValidateRead(identity taskFilesIdentity, relative string, result application.ActionResult) (application.TaskFilesReadResult, error) {
	read, err := decodeTaskFilesResult[application.TaskFilesReadResult](result)
	if err != nil {
		return read, err
	}
	if read.RelativePath != relative || !taskFilesSamePath(read.Root, identity.taskPath) ||
		!taskFilesSamePath(read.Content.Path, taskFilesAbsolute(read.Root, relative)) || !read.Content.ReadOnly ||
		read.Content.Draft != nil || read.Content.TextDraft != nil || read.Content.MarkdownEditContext != "" {
		return read, fmt.Errorf("文件读取身份不匹配，请刷新任务文件")
	}
	return read, nil
}

func taskFilesReadMedia(ctx context.Context, backend application.Backend, identity taskFilesIdentity, expected application.TaskFilesReadResult) (application.TaskFilesReadResult, error) {
	action := identity.action("task.files.read", expected.RelativePath)
	action.Payload["media"] = true
	result, err := backend.Dispatch(ctx, action)
	if err != nil {
		return application.TaskFilesReadResult{}, err
	}
	read, err := taskFilesValidateRead(identity, expected.RelativePath, result)
	if err != nil {
		return read, err
	}
	if !taskFilesReadMatches(expected, read) {
		return read, errors.New("文件或任务目录已变化，请重新读取文件")
	}
	if read.FallbackReason != "" {
		return read, errors.New(read.FallbackReason)
	}
	var limit int64
	switch read.Content.Kind {
	case "image":
		limit = media.DefaultMaxImageBytes
	case "pdf":
		limit = media.DefaultMaxPDFBytes
	default:
		return read, errors.New("该文件不是图片或 PDF")
	}
	if len(read.MediaBytes) == 0 || int64(len(read.MediaBytes)) > limit || int64(len(read.MediaBytes)) != read.Content.Size ||
		read.MediaHash == "" || documents.Hash(read.MediaBytes) != read.MediaHash {
		return read, errors.New("媒体快照不完整或超过预览限制，请重新读取")
	}
	return read, ctx.Err()
}
