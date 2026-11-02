package desktop

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/egoist/mygo/ui"
)

const taskFilesInlineImageLimit = 8

type taskFilesInlineImage struct {
	bitmap  *ui.Bitmap
	loading bool
	error   string
}

// Markdown resources use the document's task-relative directory. URL escaping
// is decoded before rejecting traversal; absolute/network/file URLs cannot
// grant access to files outside the task. The backend also validates every hop.
func taskFilesImageTarget(document, raw string) (relative, remote string, err error) {
	u, err := url.Parse(strings.ReplaceAll(strings.TrimSpace(raw), " ", "%20"))
	if err != nil {
		return "", "", err
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		if _, err := parseNativeRemoteImageURL(u.String()); err != nil {
			return "", "", err
		}
		return "", u.String(), nil
	}
	if u.Scheme != "" || u.Host != "" || u.Path == "" || strings.HasPrefix(u.Path, "/") || strings.Contains(u.Path, `\`) || strings.ContainsAny(u.Path, "\x00\r\n:") {
		return "", "", fmt.Errorf("图片地址必须位于当前任务工作目录")
	}
	result := path.Clean(path.Join(path.Dir(document), u.Path))
	if _, ok := taskFilesRelative(result); !ok || result == "" {
		return "", "", fmt.Errorf("图片地址超出当前任务工作目录")
	}
	return result, "", nil
}

func (s *app) requestTaskFilesInlineImage(view *taskFilesView, preview *taskFilesPreview, raw string) *taskFilesInlineImage {
	if image := preview.images[raw]; image != nil {
		return image
	}
	image := &taskFilesInlineImage{loading: true}
	if len(preview.images) >= taskFilesInlineImageLimit {
		return &taskFilesInlineImage{error: "此文档图片较多，请使用系统应用查看其余图片"}
	}
	preview.images[raw] = image
	relative, remote, err := taskFilesImageTarget(preview.relative, raw)
	if err != nil {
		image.loading, image.error = false, err.Error()
		return image
	}
	backend, identity := s.backend, view.identity
	jobs := &s.taskFilesUnit.jobs
	s.runTaskFilesJob(view, "preview:image:"+raw, func(ctx context.Context) (any, error) {
		release, err := jobs.acquireMedia(ctx)
		if err != nil {
			return nil, err
		}
		defer release()
		if remote != "" {
			bitmap, err := fetchNativeRemoteImagePreview(ctx, remote, 1200)
			if err == nil {
				width, height := bitmap.Size()
				if int64(width)*int64(height) > 2_000_000 {
					return nil, fmt.Errorf("图片过大，请使用系统应用查看")
				}
			}
			return bitmap, err
		}
		read, err := taskFilesRead(ctx, backend, identity, relative)
		if err != nil {
			return nil, err
		}
		if read.Content.Kind != "image" || read.FallbackReason != "" {
			return nil, fmt.Errorf("图片不支持内置预览，请使用系统应用查看")
		}
		snapshot, err := taskFilesReadMedia(ctx, backend, identity, read)
		if err != nil {
			return nil, err
		}
		bitmap, err := decodeTaskFilesImage(ctx, read.Content.Path, snapshot.MediaBytes, 1200, 2_000_000)
		if err == nil {
			err = checkTaskFilesMediaSource(ctx, backend, identity, read)
		}
		return bitmap, err
	}, func(value any, err error) {
		image.loading = false
		if err != nil {
			image.error = err.Error()
			return
		}
		image.bitmap = value.(*ui.Bitmap)
	}, nil)
	return image
}

func (s *app) renderTaskFilesMarkdownImage(c *ui.Context, p palette, view *taskFilesView, preview *taskFilesPreview, raw, alt string) {
	if !s.taskFilesCurrent(view) || view.preview != preview {
		return
	}
	image := s.requestTaskFilesInlineImage(view, preview, raw)
	if image.bitmap != nil {
		width, height := image.bitmap.Size()
		ui.Image(c.Key("task-file-markdown-image:"+raw), image.bitmap).AspectRatio(float32(width) / float32(height)).MaxWidthPercent(100).MaxWidth(800).MaxHeight(480).Label("Markdown 图片：" + alt).OnClick(func() {
			if s.taskFilesCurrent(view) && view.preview == preview {
				preview.viewer, preview.viewerZoom = raw, 100
				preview.viewerScroll = ui.ScrollState{}
			}
		})
		return
	}
	text := alt
	if text == "" {
		text = "图片"
	}
	if image.loading {
		text += " · 正在读取…"
	} else if image.error != "" {
		text += " · " + image.error
	}
	label(c, text, 12, p.muted).Wrap().Tooltip(raw)
}

func (s *app) renderTaskFilesInlineViewer(c *ui.Context, p palette, view *taskFilesView, preview *taskFilesPreview) {
	image := preview.images[preview.viewer]
	if preview.viewer == "" || image == nil || image.bitmap == nil {
		return
	}
	open := true
	dialog := ui.DialogBase(c.Key("task-files-inline-image-viewer"), &open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .72))
		width, height := c.Size()
		panel.Column().Width(max(float32(1), width-32)).Height(max(float32(1), height-32)).Padding(12).Background(p.surface).Radius(8).Label("任务文件图片查看器")
		panel.OnShortcut(0, ui.KeyEscape, func() { preview.viewer = "" })
		panel.Children(func() {
			ui.Row(c).FillWidth().Height(34).Gap(6).AlignItems(ui.Center).Children(func() {
				label(c, "图片", 13, p.text).Grow(1)
				if miniButton(c, p, "task-file-inline-minus", "minus", "缩小图片", false).Clicked() {
					preview.viewerZoom = max(25, preview.viewerZoom-25)
				}
				label(c, fmt.Sprintf("%d%%", preview.viewerZoom), 11, p.muted)
				if miniButton(c, p, "task-file-inline-plus", "plus", "放大图片", false).Clicked() {
					preview.viewerZoom = min(300, preview.viewerZoom+25)
				}
				if miniButton(c, p, "task-file-inline-close", "close", "关闭图片", false).Clicked() {
					preview.viewer = ""
				}
			})
			ui.ScrollBoth(c.Key("task-file-inline-image-scroll")).TrackScroll(&preview.viewerScroll).Grow(1).FillWidth().MinHeight(0).Children(func() {
				w, h := image.bitmap.Size()
				fit := nativeImageFitScale(float32(w), float32(h), width-80, height-100)
				ui.Image(c, image.bitmap).Width(float32(w) * fit * float32(preview.viewerZoom) / 100).Height(float32(h) * fit * float32(preview.viewerZoom) / 100)
			})
		})
	})
	if dialog.Dismissed() || !open {
		preview.viewer = ""
	}
}
