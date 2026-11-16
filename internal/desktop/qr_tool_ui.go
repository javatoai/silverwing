package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
	"silverwing-go/internal/asyncjob"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/tools"
)

const (
	qrToolTab         = "二维码"
	qrSourceByteLimit = 8 * 1024
	qrGenerateMode    = 0
	qrDecodeMode      = 1
)

type qrToolOperation uint8

const (
	qrGenerate qrToolOperation = iota
	qrDecode
	qrSave
)

type qrToolImage struct {
	png     []byte
	preview *ui.Bitmap
}

type qrToolReply struct {
	image *qrToolImage
	text  string
	err   error
}

type qrToolRequest struct {
	operation qrToolOperation
	revision  uint64
	lease     *asyncjob.Lease
	reply     chan qrToolReply
}

// The view owns this state. The worker receives immutable inputs and returns
// one reply, and only the view accepts the result or changes editor state.
type qrToolState struct {
	mode          int
	input, output codeeditor.State
	inputVersion  uint64
	revision      uint64
	work          *qrToolRequest
	image         *qrToolImage
	decoded       bool
	message       string
	err           string
}

func (q *qrToolState) invalidate() {
	q.revision++
	if q.work != nil {
		q.work.lease.Cancel()
	}
	q.image = nil
	q.message, q.err = "", ""
	if q.decoded {
		q.output.Buffer = codeeditor.NewBufferPreservingLineEndings("")
		q.decoded = false
	}
}

func (q *qrToolState) syncInput() {
	if q.input.Buffer == nil {
		return
	}
	if version := q.input.Buffer.Version(); q.inputVersion != version {
		q.inputVersion = version
		q.invalidate()
	}
}

func (q *qrToolState) poll(visible bool) {
	request := q.work
	if request == nil {
		return
	}
	select {
	case result := <-request.reply:
		q.work = nil
		if !visible || request.revision != q.revision || errors.Is(result.err, context.Canceled) {
			return
		}
		if result.err != nil {
			q.err = result.err.Error()
			return
		}
		switch request.operation {
		case qrGenerate:
			q.image = result.image
		case qrDecode:
			q.output.Buffer = codeeditor.NewBufferPreservingLineEndings(result.text)
			q.output.Select(0, 0)
			q.decoded = true
		case qrSave:
			q.message = "二维码图片已保存"
		}
	default:
	}
}

func (s *app) startQRToolWork(state *toolPageState, services ui.Services, operation qrToolOperation, compute func(context.Context) qrToolReply) {
	q := &state.qrTool
	if q.work != nil || !s.utilityToolVisible(state, qrToolTab) {
		return
	}
	jobs := s.ensureDeveloperToolJobs()
	lease, err := jobs.lifecycle.Begin(context.Background(), asyncjob.Options{CancelOnClose: true})
	if err != nil {
		return
	}
	request := &qrToolRequest{operation: operation, revision: q.revision, lease: lease, reply: make(chan qrToolReply, 1)}
	q.work = request
	q.message, q.err = "", ""
	go func() {
		defer lease.Complete(nil)
		defer lease.Cancel()
		ctx := lease.Context()
		var result qrToolReply
		if ctx.Err() == nil {
			result = compute(ctx)
		}
		if err := ctx.Err(); err != nil {
			result = qrToolReply{err: err}
		}
		request.reply <- result
		services.Invalidate()
	}()
	services.Invalidate()
}

func (s *app) qrCanAct(state *toolPageState, mode int) bool {
	q := &state.qrTool
	if !s.utilityToolVisible(state, qrToolTab) || q.mode != mode || s.win != nil && s.win.IsDestroyed() {
		return false
	}
	q.syncInput()
	return q.work == nil
}

func (s *app) generateQRToolImage(state *toolPageState, services ui.Services) {
	q := &state.qrTool
	if !s.qrCanAct(state, qrGenerateMode) || q.input.Buffer == nil || q.input.Buffer.Len() == 0 {
		return
	}
	source := q.input.Buffer.OriginalText()
	q.invalidate()
	s.startQRToolWork(state, services, qrGenerate, func(ctx context.Context) qrToolReply {
		data, err := tools.GenerateQR(source)
		if err != nil {
			return qrToolReply{err: err}
		}
		if err := ctx.Err(); err != nil {
			return qrToolReply{err: err}
		}
		preview, err := qrPreviewBitmap(data)
		if err != nil {
			return qrToolReply{err: fmt.Errorf("无法预览二维码图片：%w", err)}
		}
		return qrToolReply{image: &qrToolImage{png: data, preview: preview}}
	})
}

func qrPreviewBitmap(data []byte) (*ui.Bitmap, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	// Keep one pixel for each QR module, including the quiet zone. MyGO's
	// pixelated sampling then enlarges the preview without smoothing modules.
	size := tools.QRModulePixels
	bounds := img.Bounds()
	modules := image.NewGray(image.Rect(0, 0, bounds.Dx()/size, bounds.Dy()/size))
	for y := range modules.Bounds().Dy() {
		for x := range modules.Bounds().Dx() {
			modules.Set(x, y, img.At(bounds.Min.X+x*size, bounds.Min.Y+y*size))
		}
	}
	return ui.NewBitmap(modules).Pixelated(), nil
}

func (s *app) chooseQRToolImage(state *toolPageState, services ui.Services) {
	q := &state.qrTool
	if !s.qrCanAct(state, qrDecodeMode) || s.settingsUnit.state.settingsPickerBusy {
		return
	}
	revision := q.revision
	s.pickNativePath("选择二维码图片", s.snapshot.Home, false, []mygo.FileFilter{
		{Name: "二维码图片", Extensions: []string{"png", "jpg", "jpeg", "webp"}},
	}, func(path string) {
		if s.qrCanAct(state, qrDecodeMode) && q.revision == revision {
			q.invalidate()
			s.decodeQRToolImage(state, services, path, nil)
		}
	})
}

func (s *app) decodeQRToolImage(state *toolPageState, services ui.Services, path string, data []byte) {
	s.startQRToolWork(state, services, qrDecode, func(ctx context.Context) qrToolReply {
		text, err := tools.DecodeQR(ctx, path, data)
		return qrToolReply{text: text, err: err}
	})
}

func (s *app) decodeQRToolClipboard(state *toolPageState, services ui.Services) {
	q := &state.qrTool
	if !s.qrCanAct(state, qrDecodeMode) || s.settingsUnit.state.settingsPickerBusy {
		return
	}
	q.invalidate()
	revision := q.revision
	var data []byte
	var err error
	if s.win != nil {
		data, err = mygo.Clipboard.ReadFormat(transfer.PNG)
	} else {
		data, err = services.ReadClipboardData(transfer.PNG).Read(transfer.PNG)
	}
	// Some native clipboard providers pump nested events during a read.
	if !s.qrCanAct(state, qrDecodeMode) || q.revision != revision {
		return
	}
	switch {
	case errors.Is(err, transfer.ErrFormat) || err == nil && len(data) == 0:
		q.err = "剪贴板中没有图片，请先截图或复制图片。"
	case err != nil:
		q.err = "读取剪贴板失败：" + err.Error()
	default:
		s.decodeQRToolImage(state, services, "", data)
	}
}

func (s *app) copyQRToolData(state *toolPageState, services ui.Services, data transfer.Data, message string) {
	q := &state.qrTool
	revision, mode := q.revision, q.mode
	var err error
	if s.win != nil {
		err = mygo.Clipboard.Write(data)
	} else {
		services.WriteClipboardData(data)
	}
	if !s.qrCanAct(state, mode) || q.revision != revision {
		return
	}
	q.message, q.err = message, ""
	if err != nil {
		q.message, q.err = "", "复制失败："+err.Error()
	}
}

func (s *app) saveQRToolImage(state *toolPageState, services ui.Services) {
	q := &state.qrTool
	if !s.qrCanAct(state, qrGenerateMode) || q.image == nil || s.settingsUnit.state.settingsPickerBusy {
		return
	}
	revision := q.revision
	s.chooseSavePath("保存二维码图片", filepath.Join(s.snapshot.Home, "qrcode.png"), []mygo.FileFilter{
		{Name: "PNG 图片", Extensions: []string{"png"}},
	}, func(path string) {
		if !s.qrCanAct(state, qrGenerateMode) || q.revision != revision || q.image == nil {
			return
		}
		data := q.image.png
		s.startQRToolWork(state, services, qrSave, func(ctx context.Context) qrToolReply {
			if err := ctx.Err(); err != nil {
				return qrToolReply{err: err}
			}
			if err := storage.WriteFileAtomic(path, data, 0o600); err != nil {
				return qrToolReply{err: fmt.Errorf("保存二维码图片失败：%w", err)}
			}
			return qrToolReply{}
		})
	})
}

func (s *app) renderQRTool(c *ui.Context, p palette, state *toolPageState) {
	q := &state.qrTool
	services := c.Services()
	if q.input.Buffer == nil {
		q.input = newUtilityTextEditor("二维码输入", "输入或粘贴文字、链接", qrSourceByteLimit, false)
		q.output = newUtilityTextEditor("二维码识别结果", "识别结果将在这里显示", -1, true)
		q.input.ClipboardWrite = s.utilityClipboardWriter(services)
		q.output.ClipboardWrite = s.utilityClipboardWriter(services)
		q.input.OnLimitExceeded = func() {
			q.invalidate()
			q.err = "二维码输入不能超过 8 KiB。"
		}
	}
	q.syncInput()
	q.poll(s.utilityToolVisible(state, qrToolTab))
	ui.Column(c.Key("qr-tool")).Grow(1).MinWidth(0).MinHeight(0).Gap(10).Children(func() {
		ui.Row(c.Key("qr-toolbar")).FillWidth().Wrap().Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Tabs(c.Key("qr-modes"), &q.mode, "生成二维码", "识别二维码").OnChange(q.invalidate)
			if q.mode == qrDecodeMode {
				canChoose := q.work == nil && !s.settingsUnit.state.settingsPickerBusy
				if toolButtonWithEnabled(c, "qr-choose-image", "选择图片", p, true, canChoose) {
					s.chooseQRToolImage(state, services)
				}
				if toolButtonWithEnabled(c, "qr-from-clipboard", "从剪贴板识别", p, false, canChoose) {
					s.decodeQRToolClipboard(state, services)
				}
				if toolButtonWithEnabled(c, "qr-copy-text", "复制文本", p, false, q.decoded && q.work == nil) && s.qrCanAct(state, qrDecodeMode) && q.decoded {
					s.copyQRToolData(state, services, transfer.TextData(q.output.Buffer.OriginalText()), "识别结果已复制")
				}
			} else {
				if toolButtonWithEnabled(c, "qr-generate", "生成", p, true, q.input.Buffer.Len() != 0 && q.work == nil) {
					s.generateQRToolImage(state, services)
				}
				if toolButtonWithEnabled(c, "qr-save-image", "保存 PNG", p, false, q.image != nil && q.work == nil && !s.settingsUnit.state.settingsPickerBusy) {
					s.saveQRToolImage(state, services)
				}
			}
		})
		q.renderStatus(c, p)
		if q.mode == qrDecodeMode {
			renderUtilityEditor(c, p, "qr-decoded-text", "识别结果", &q.output)
		} else {
			s.renderQRGenerator(c, p, state)
		}
	})
}

func (q *qrToolState) renderStatus(c *ui.Context, p palette) {
	switch {
	case q.err != "":
		label(c, q.err, 12, p.danger).Wrap()
	case q.work != nil:
		label(c, q.pendingLabel(), 12, p.muted)
	case q.message != "":
		label(c, q.message, 12, p.green)
	}
}

func (q *qrToolState) pendingLabel() string {
	if q.work.revision != q.revision {
		return "正在取消…"
	}
	switch q.work.operation {
	case qrDecode:
		return "正在识别…"
	case qrSave:
		return "正在保存…"
	default:
		return "正在生成…"
	}
}

func (s *app) renderQRGenerator(c *ui.Context, p palette, state *toolPageState) {
	q := &state.qrTool
	width, _ := c.Size()
	var panes ui.Element
	if width >= 720 {
		panes = ui.Row(c.Key("qr-panes-side-by-side"))
	} else {
		panes = ui.Column(c.Key("qr-panes-stacked"))
	}
	panes.FillWidth().Grow(1).MinWidth(0).MinHeight(0).Gap(10).AlignItems(ui.Stretch).Children(func() {
		renderUtilityEditor(c, p, "qr-input", "输入", &q.input)
		ui.Column(c.Key("qr-preview-panel")).Grow(1).MinWidth(0).MinHeight(0).Padding(12).Gap(8).
			Background(p.surface).Border(1, p.line).Radius(10).Children(func() {
			label(c, "预览", 13, p.text).FontWeight(600)
			if q.image == nil {
				ui.Column(c).Grow(1).MinHeight(0).Center().Children(func() {
					label(c, "生成的二维码将在这里显示", 12, p.muted).Wrap()
				})
				return
			}
			ui.Column(c.Key("qr-preview-content")).Grow(1).FillWidth().MinHeight(0).Center().Gap(8).Children(func() {
				ui.Image(c.Key("qr-preview"), q.image.preview).Width(320).MaxWidthPercent(100).AspectRatio(1).
					Shrink(1).MinHeight(0).Fit(ui.Contain).Label("二维码预览")
				if toolButtonWithEnabled(c, "qr-copy-image", "复制图片", p, false, q.work == nil) && s.qrCanAct(state, qrGenerateMode) && q.image != nil {
					data := transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, q.image.png)))
					s.copyQRToolData(state, c.Services(), data, "二维码图片已复制")
				}
			})
		})
	})
}
