package desktop

import (
	"context"
	"errors"
	"fmt"
	"math"

	"silverwing-go/internal/application"
	"silverwing-go/internal/media"

	"github.com/egoist/mygo/ui"
)

type taskFilesMediaAsset struct {
	read   application.TaskFilesReadResult
	pdf    *media.PDFDocument
	info   media.DocumentInfo
	bitmap *ui.Bitmap
}

func (asset *taskFilesMediaAsset) dispose() {
	if asset != nil && asset.pdf != nil {
		_ = asset.pdf.Close()
	}
}

type taskFilesMedia struct {
	asset    *taskFilesMediaAsset
	loading  bool
	error    string
	width    int
	viewport ui.Handle
}

func (state *taskFilesMedia) close(jobs *taskFilesJobs) {
	asset := state.asset
	state.asset = nil
	if asset != nil {
		asset.bitmap = nil // Native bitmaps are released by dropping their owner.
		jobs.dispose(asset.dispose)
	}
}

func checkTaskFilesMediaSource(ctx context.Context, backend application.Backend, identity taskFilesIdentity, expected application.TaskFilesReadResult) error {
	actual, err := taskFilesRead(ctx, backend, identity, expected.RelativePath)
	if err != nil {
		return err
	}
	if !taskFilesReadMatches(expected, actual) {
		return errors.New("文件或任务目录已变化，请重新读取文件")
	}
	return ctx.Err()
}

// The decoder consumes an immutable snapshot from the task backend's scoped
// handle. It never reopens the ambient path between the identity checks.
func loadTaskFilesMedia(ctx context.Context, backend application.Backend, identity taskFilesIdentity,
	expected application.TaskFilesReadResult, page, width int) (*taskFilesMediaAsset, error) {
	snapshot, err := taskFilesReadMedia(ctx, backend, identity, expected)
	if err != nil {
		return nil, err
	}
	asset := &taskFilesMediaAsset{read: expected}
	switch expected.Content.Kind {
	case "image":
		asset.bitmap, err = decodeTaskFilesImage(ctx, expected.Content.Path, snapshot.MediaBytes, 2048, 8_000_000)
	case "pdf":
		asset.pdf, err = media.OpenPDFBytes(ctx, snapshot.MediaBytes, media.Options{})
		if err == nil {
			asset.info = asset.pdf.Info()
			page = clampInt(page, 0, max(0, asset.info.PageCount-1))
			var pixels media.RenderedRGBA
			pixels, err = asset.pdf.RenderPageRGBA(ctx, page, width)
			if err == nil {
				asset.bitmap = ui.NewBitmap(pixels.RGBA)
			}
		}
	default:
		err = errors.New("该文件不是图片或 PDF")
	}
	if err == nil {
		err = checkTaskFilesMediaSource(ctx, backend, identity, expected)
	}
	if err != nil {
		asset.dispose()
		return nil, err
	}
	return asset, nil
}

func taskFilesImageWidth(info media.ImageInfo, maxWidth int, pixelBudget int64) int {
	width := max(1, min(info.Width, maxWidth))
	if info.Width > 0 && info.Height > 0 {
		bounded := int(math.Sqrt(float64(pixelBudget) * float64(info.Width) / float64(info.Height)))
		width = max(1, min(width, bounded))
	}
	return width
}

func decodeTaskFilesImage(ctx context.Context, nameHint string, snapshot []byte, maxWidth int, pixelBudget int64) (*ui.Bitmap, error) {
	document, err := media.OpenImageBytes(ctx, nameHint, snapshot, media.Options{})
	if err != nil {
		return nil, err
	}
	defer document.Close()
	pixels, err := document.RenderRGBA(ctx, taskFilesImageWidth(document.Info(), maxWidth, pixelBudget))
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return ui.NewBitmap(pixels.RGBA), nil
}

func (s *app) requestTaskFilesMedia(view *taskFilesView, preview *taskFilesPreview) {
	state := &taskFilesMedia{loading: true, width: 1200}
	preview.media = state
	backend, identity, expected := s.backend, view.identity, preview.read
	page := view.position(preview.relative).page
	jobs := &s.taskFilesUnit.jobs
	s.runTaskFilesJob(view, "preview:media", func(ctx context.Context) (any, error) {
		release, err := jobs.acquireMedia(ctx)
		if err != nil {
			return nil, err
		}
		defer release()
		return loadTaskFilesMedia(ctx, backend, identity, expected, page, 1200)
	}, func(value any, err error) {
		state.loading = false
		if err != nil {
			state.error = err.Error()
			return
		}
		state.asset = value.(*taskFilesMediaAsset)
		if state.asset.pdf != nil {
			position := view.position(preview.relative)
			position.page = clampInt(position.page, 0, max(0, state.asset.info.PageCount-1))
		}
	}, func(value any) {
		if asset, ok := value.(*taskFilesMediaAsset); ok {
			asset.dispose()
		}
	})
}

func (s *app) renderTaskFilesPDFPage(view *taskFilesView, preview *taskFilesPreview, width int) {
	state := preview.media
	if !s.taskFilesCurrent(view) || state == nil || state.asset == nil || state.asset.pdf == nil {
		return
	}
	backend, identity, expected := s.backend, view.identity, state.asset.read
	document, page := state.asset.pdf, view.position(preview.relative).page
	state.loading, state.error, state.width, state.asset.bitmap = true, "", width, nil
	jobs := &s.taskFilesUnit.jobs
	s.runTaskFilesJob(view, "preview:pdf-page", func(ctx context.Context) (any, error) {
		release, err := jobs.acquireMedia(ctx)
		if err != nil {
			return nil, err
		}
		defer release()
		if err := checkTaskFilesMediaSource(ctx, backend, identity, expected); err != nil {
			return nil, err
		}
		pixels, err := document.RenderPageRGBA(ctx, page, width)
		if err != nil {
			return nil, err
		}
		if err := checkTaskFilesMediaSource(ctx, backend, identity, expected); err != nil {
			return nil, err
		}
		return ui.NewBitmap(pixels.RGBA), nil
	}, func(value any, err error) {
		state.loading = false
		if err != nil {
			state.error = err.Error()
			return
		}
		state.asset.bitmap = value.(*ui.Bitmap)
	}, nil)
}

func (s *app) renderTaskFilesMedia(c *ui.Context, p palette, view *taskFilesView, preview *taskFilesPreview) {
	state := preview.media
	position := view.position(preview.relative)
	if state.asset == nil {
		if state.error != "" {
			s.renderTaskFilesFallback(c, p, view, preview, state.error)
		} else {
			label(c, "正在读取媒体…", 13, p.muted).Padding(20)
		}
		return
	}
	pdf := state.asset.pdf != nil
	viewportWidth := state.viewport.Bounds(c).W
	if viewportWidth <= 0 {
		viewportWidth = 600
	}
	displayWidth := max(float32(64), viewportWidth-32) * float32(position.zoom) / 100
	if !pdf {
		if bitmap := state.asset.bitmap; bitmap != nil {
			width, _ := bitmap.Size()
			displayWidth = min(float32(width), max(float32(64), viewportWidth-32)) * float32(position.zoom) / 100
		}
	}
	width := clampInt(int(displayWidth), 64, 2048)
	ui.Row(c.Key("task-file-media-controls")).Height(34).FillWidth().Padding(0, 8).Gap(5).AlignItems(ui.Center).Children(func() {
		if pdf {
			if miniButton(c, p, "task-file-pdf-prev", "chevron-left", "上一页", false).Disabled(position.page <= 0).Clicked() {
				position.page--
				position.scroll = ui.ScrollState{}
				s.renderTaskFilesPDFPage(view, preview, width)
			}
			label(c, fmt.Sprintf("%d / %d", position.page+1, state.asset.info.PageCount), 11, p.muted).NoWrap()
			if miniButton(c, p, "task-file-pdf-next", "chevron", "下一页", false).Disabled(position.page+1 >= state.asset.info.PageCount).Clicked() {
				position.page++
				position.scroll = ui.ScrollState{}
				s.renderTaskFilesPDFPage(view, preview, width)
			}
		}
		if miniButton(c, p, "task-file-media-out", "minus", "缩小预览", false).Disabled(position.zoom <= 25).Clicked() {
			position.zoom = max(25, position.zoom-25)
		}
		label(c, fmt.Sprintf("%d%%", position.zoom), 11, p.muted).NoWrap()
		if miniButton(c, p, "task-file-media-in", "plus", "放大预览", false).Disabled(position.zoom >= 200).Clicked() {
			position.zoom = min(200, position.zoom+25)
		}
		if ui.Button(c.Key("task-file-media-fit"), "适合宽度").Clicked() {
			position.zoom = 100
			position.scroll = ui.ScrollState{}
		}
	})
	if pdf && !state.loading && state.error == "" && state.width != width {
		s.renderTaskFilesPDFPage(view, preview, width)
	}
	if state.error != "" {
		s.renderTaskFilesFallback(c, p, view, preview, state.error)
		return
	}
	ui.ScrollBoth(c.Key("task-file-media-scroll")).Bind(&state.viewport).TrackScroll(&position.scroll).Grow(1).FillWidth().MinHeight(0).MinWidth(0).Padding(16).Children(func() {
		if state.loading {
			label(c, "正在渲染…", 12, p.muted)
			return
		}
		if bitmap := state.asset.bitmap; bitmap != nil {
			w, h := bitmap.Size()
			if w > 0 && h > 0 {
				ui.Image(c.Key("task-file-native-media"), bitmap).Width(displayWidth).Height(displayWidth * float32(h) / float32(w)).Label("任务文件媒体预览")
			}
		}
	})
}
