package desktop

import (
	"context"
	"fmt"
	"path/filepath"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	markdownui "silverwing-go/internal/ui/markdown"

	"github.com/egoist/mygo/ui"
	md "github.com/egoist/mygo/ui/markdown"
)

type taskFilesPreview struct {
	relative                 string
	loading                  bool
	error                    string
	read                     application.TaskFilesReadResult
	reader                   *markdownui.ReaderState
	code                     nativeCodePreviewState
	native                   md.Options
	images                   map[string]*taskFilesInlineImage
	media                    *taskFilesMedia
	revealLine, revealColumn int
	revealFragment           string
	viewer                   string
	viewerZoom               int
	viewerScroll             ui.ScrollState
}

func (view *taskFilesView) retirePreview(jobs *taskFilesJobs) {
	preview := view.preview
	if preview == nil {
		return
	}
	jobs.cancelPreview()
	position := view.position(preview.relative)
	if preview.reader != nil {
		position.reader = preview.reader.GetViewState()
		preview.reader.Close()
		preview.reader = nil
	}
	if preview.code.view != nil {
		position.code = preview.code.view.ScrollPosition()
		preview.code = nativeCodePreviewState{}
	}
	if preview.media != nil {
		preview.media.close(jobs)
	}
	preview.images = nil
	preview.native = md.Options{}
	preview.read = application.TaskFilesReadResult{}
	view.preview = nil
}

func (s *app) openTaskFilePreview(view *taskFilesView, relative string, refresh bool) {
	if !s.taskFilesCurrent(view) {
		return
	}
	if _, ok := taskFilesRelative(relative); !ok || relative == "" {
		return
	}
	entry, known := view.entry(relative)
	if known && (entry.Directory || entry.RestrictedReason != "") {
		if entry.RestrictedReason != "" {
			view.operationError = entry.RestrictedReason
		}
		return
	}
	view.selected, view.opened = relative, relative
	if known && entry.Kind == "external" {
		// Re-entering an existing preview must also clear messages left by a
		// different, rejected tree selection, without reloading the preview.
		view.operationError, view.operationMessage = "", ""
	}
	if view.preview != nil && view.preview.relative == relative && !refresh {
		return
	}
	view.retirePreview(&s.taskFilesUnit.jobs)
	preview := &taskFilesPreview{relative: relative, loading: true, images: make(map[string]*taskFilesInlineImage)}
	view.preview = preview
	if known && entry.Kind == "external" {
		preview.loading = false
		preview.read = application.TaskFilesReadResult{Root: view.root, RelativePath: relative,
			Content: documents.Content{Path: taskFilesAbsolute(view.root, relative), Kind: "external", ReadOnly: true}}
		return
	}
	backend, identity := s.backend, view.identity
	s.runTaskFilesJob(view, "preview:read", func(ctx context.Context) (any, error) {
		return taskFilesRead(ctx, backend, identity, relative)
	}, func(value any, err error) {
		preview.loading = false
		if err == nil {
			read := value.(application.TaskFilesReadResult)
			err = view.acceptRoot(read.Root)
			if err == nil {
				preview.read = read
				s.bindTaskFilesPreview(view, preview)
				return
			}
		}
		preview.error = err.Error()
	}, nil)
}

func (s *app) bindTaskFilesPreview(view *taskFilesView, preview *taskFilesPreview) {
	position := view.position(preview.relative)
	if preview.read.FallbackReason != "" {
		return
	}
	switch preview.read.Content.Kind {
	case "markdown":
		preview.reader = markdownui.NewReaderState(markdownui.Document{ID: view.identity.documentID(preview.relative),
			Path: preview.read.Content.Path, Source: preview.read.Content.Text})
		if position.reader.DocumentID != "" {
			if err := preview.reader.RestoreViewState(position.reader); err != nil {
				preview.error = err.Error()
			}
		}
		preview.reader.RevealLocation(preview.revealLine, preview.revealFragment)
	case "image", "pdf":
		s.requestTaskFilesMedia(view, preview)
	default:
		if taskFilesTextKind(preview.read.Content.Kind) {
			state := preview.code.bind(view.identity.documentID(preview.relative), preview.read.Content.Path, preview.read.Content.Text)
			state.SetScrollPosition(position.code)
			if preview.revealLine > 0 {
				at := markdownLinkLocationOffset(state.Buffer.Snapshot().String(), preview.revealLine, preview.revealColumn)
				state.Select(at, at)
			}
		}
	}
}

func taskFilesTextKind(kind string) bool {
	switch kind {
	case "text", "code", "json", "table", "csv", "yaml", "xml":
		return true
	}
	return false
}

func (s *app) renderTaskFilePreview(c *ui.Context, p palette, view *taskFilesView) {
	preview := view.preview
	if preview == nil {
		return
	}
	if preview.loading {
		label(c, "正在读取文件…", 13, p.muted).Padding(20)
		return
	}
	if preview.error != "" {
		ui.Column(c).FillWidth().Padding(20).Gap(10).Children(func() {
			label(c, preview.error, 13, p.amber).Wrap()
			if ui.Button(c, "重试读取文件").Clicked() {
				s.openTaskFilePreview(view, preview.relative, true)
			}
		})
		return
	}
	if preview.read.Content.Kind == "external" {
		s.renderTaskFilesFallback(c, p, view, preview, "")
		return
	}
	if preview.read.FallbackReason != "" {
		s.renderTaskFilesFallback(c, p, view, preview, preview.read.FallbackReason)
		return
	}
	if preview.reader != nil {
		preview.native.Image = func(ctx *ui.Context, block, start, end int, destination, alt string) {
			s.renderTaskFilesMarkdownImage(ctx.Key(fmt.Sprintf("task-image:%d:%d:%d", block, start, end)), p, view, preview, destination, alt)
		}
		ui.Column(c.Key("task-file-markdown")).Grow(1).FillWidth().MinHeight(0).MinWidth(0).Padding(12, 18).Children(func() {
			markdownui.MarkdownReader(c, preview.reader, markdownui.ReaderOptions{Theme: transientMarkdownTheme(p), Native: &preview.native,
				OnNavigate: func(target markdownui.LinkTarget) { s.navigateTaskFilesLink(view, preview, target) },
				OnError: func(err error) {
					if s.taskFilesCurrent(view) && view.preview == preview {
						view.operationError = err.Error()
					}
				},
			}).Grow(1).FillWidth().MinHeight(0).MinWidth(0)
		})
		s.renderTaskFilesInlineViewer(c, p, view, preview)
		return
	}
	if preview.code.view != nil {
		renderNativeCodePreview(c, p, &preview.code, view.identity.documentID(preview.relative), preview.read.Content.Path,
			preview.read.Content.Text, "task-file-code").Grow(1).FillWidth().MinWidth(0).MinHeight(0)
		return
	}
	if preview.media != nil {
		s.renderTaskFilesMedia(c, p, view, preview)
		return
	}
	s.renderTaskFilesFallback(c, p, view, preview, "此文件类型不支持内置预览。")
}

func (s *app) renderTaskFilesFallback(c *ui.Context, p palette, view *taskFilesView, preview *taskFilesPreview, reason string) {
	ui.Column(c.Key("task-file-fallback")).Grow(1).FillWidth().Padding(20).Gap(10).Children(func() {
		if reason != "" {
			label(c, reason, 13, p.muted).Wrap()
		}
		if ui.Button(c, "使用系统应用打开").Clicked() {
			s.taskFilesExternalAction(view, preview.relative, "task.files.open")
		}
	})
}

func (s *app) navigateTaskFilesLink(view *taskFilesView, preview *taskFilesPreview, target markdownui.LinkTarget) {
	if !s.taskFilesCurrent(view) || view.preview != preview {
		return
	}
	if target.URL != "" {
		s.act("system.open", "", "", "", target.URL, "", nil)
		return
	}
	relative, err := filepath.Rel(view.root, target.Path)
	relative = filepath.ToSlash(relative)
	if _, ok := taskFilesRelative(relative); err != nil || !ok || relative == "" {
		view.operationError = "链接超出当前任务工作目录"
		return
	}
	if relative == preview.relative {
		preview.reader.RevealLocation(target.Line, target.Fragment)
		return
	}
	s.openTaskFilePreview(view, relative, false)
	if next := view.preview; next != nil && next.relative == relative {
		next.revealLine, next.revealColumn, next.revealFragment = target.Line, target.Column, target.Fragment
		if next.reader != nil {
			next.reader.RevealLocation(target.Line, target.Fragment)
		}
	}
	view.revealPath = relative
	parent := filepath.ToSlash(filepath.Dir(relative))
	for parent != "." && parent != "" {
		view.expanded[parent] = true
		parent = filepath.ToSlash(filepath.Dir(parent))
	}
	s.continueTaskFilesReveal(view)
}
