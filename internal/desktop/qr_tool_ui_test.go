package desktop

import (
	"bytes"
	"context"
	"image/color"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

func TestQRToolClipboardRoundTripClearsStaleResults(t *testing.T) {
	s, state, tester := qrToolTester(t)
	q := &state.qrTool
	click := func(label string) {
		t.Helper()
		if err := tester.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	const source = "https://example.test/你好?value=🙂\r\n  下一行  "
	click("二维码输入")
	tester.SetClipboard(source)
	tester.Key(ui.Cmd, ui.KeyV)
	if got := q.input.Buffer.OriginalText(); got != source {
		input, _ := tester.Find("二维码输入")
		t.Fatalf("pasted source = %q, focused = %v, editor = %+v, labels = %q", got, tester.Focused("二维码输入"), input, tester.Texts())
	}
	click("生成")
	waitForQRTool(t, s, tester)
	if q.image == nil {
		t.Fatalf("generation did not produce a preview: error %q, visible %v, mode %d, revision %d", q.err, s.utilityToolVisible(state, qrToolTab), q.mode, q.revision)
	}
	preview, ok := tester.Find("二维码预览")
	if !ok || preview.W <= 0 || preview.H <= 0 {
		t.Fatalf("preview bounds = %+v, visible = %v", preview, ok)
	}
	side := min(320, preview.W, preview.H)
	cornerX, cornerY := preview.X+(preview.W-side)/2+1, preview.Y+(preview.H-side)/2+1
	if got := tester.Image().RGBAAt(int(cornerX), int(cornerY)); got != (color.RGBA{255, 255, 255, 255}) {
		t.Fatalf("quiet zone pixel in the dark theme = %v", got)
	}
	for _, size := range [][2]int{{900, 480}, {1120, 900}, {420, 600}} {
		tester.SetSize(size[0], size[1])
		preview, previewVisible := tester.Find("二维码预览")
		button, buttonVisible := tester.Find("复制图片")
		gap := button.Y - (preview.Y + preview.H)
		if !previewVisible || !buttonVisible || button.W <= 0 || button.H <= 0 || gap < 0 || gap > 12 ||
			button.Y+button.H > float32(size[1]) || button.X < preview.X || button.X+button.W > preview.X+preview.W {
			t.Fatalf("at %dx%d, copy button %+v (visible %v) should sit just below preview %+v (visible %v)", size[0], size[1], button, buttonVisible, preview, previewVisible)
		}
	}

	click("复制图片")
	copied := tester.ClipboardData()
	data, err := copied.Read(transfer.PNG)
	if err != nil || !bytes.Equal(data, q.image.png) {
		t.Fatalf("copied image differs from generated PNG: %v", err)
	}

	click("二维码输入")
	tester.Type("x")
	if q.image != nil {
		t.Fatal("an input edit retained the previous QR result")
	}
	if _, visible := tester.Find("复制图片"); visible {
		t.Fatal("the copy button remained visible after the result was cleared")
	}

	click("识别二维码")
	tester.SetClipboardData(copied)
	click("从剪贴板识别")
	waitForQRTool(t, s, tester)
	if got := q.output.Buffer.OriginalText(); !q.decoded || got != source {
		t.Fatalf("decoded text = %q, decoded = %v, error = %q", got, q.decoded, q.err)
	}
	click("二维码识别结果")
	tester.Type("x")
	click("复制文本")
	if got := tester.Clipboard(); got != source {
		t.Fatalf("clipboard text = %q", got)
	}
	if len(tester.OpenedURLs()) != 0 {
		t.Fatal("QR text opened a URL")
	}

	click("从剪贴板识别")
	if q.decoded || q.output.Buffer.Len() != 0 || !tester.HasText("剪贴板中没有图片") {
		t.Fatalf("missing-image failure did not clear the decoded result: %q", q.err)
	}
}

func TestQRToolRejectsCompletionAfterModeChange(t *testing.T) {
	s, state, tester := qrToolTester(t)
	q := &state.qrTool
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	stale := &qrToolImage{}
	s.startQRToolWork(state, ui.Services{}, qrGenerate, func(ctx context.Context) qrToolReply {
		started <- ctx
		<-release
		return qrToolReply{image: stale}
	})
	request := q.work
	if request == nil {
		t.Fatal("first QR operation did not start")
	}
	ctx := <-started
	if err := tester.Click("识别二维码"); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != context.Canceled || q.work != request {
		t.Fatal("mode change must cancel the worker and retain its occupied slot until completion")
	}
	s.startQRToolWork(state, ui.Services{}, qrDecode, func(context.Context) qrToolReply {
		return qrToolReply{text: "overlapping worker"}
	})
	if q.work != request {
		t.Fatal("a second QR operation started before its predecessor drained")
	}

	close(release)
	waitForQRTool(t, s, tester)
	if q.work != nil || q.image != nil || q.decoded {
		t.Fatal("a stale successful completion became usable in the new mode")
	}
}

func TestQRToolDropsQueuedSuccessOnInputEdit(t *testing.T) {
	s, state, tester := qrToolTester(t)
	q := &state.qrTool
	s.startQRToolWork(state, ui.Services{}, qrGenerate, func(context.Context) qrToolReply {
		return qrToolReply{image: &qrToolImage{}}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := s.ensureDeveloperToolJobs().lifecycle.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if q.work == nil {
		t.Fatal("QR operation did not start")
	}
	q.input.Buffer.Set("updated input")
	tester.Frame()
	if q.work != nil || q.image != nil {
		t.Fatal("a queued success overwrote an edit")
	}
}

func qrToolTester(t *testing.T) (*app, *toolPageState, *ui.Tester) {
	t.Helper()
	s := &app{page: "tools"}
	state := s.ensureToolState()
	state.activeTab = qrToolTab
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, true)
		ui.Column(c).Fill().Padding(12).Children(func() { s.renderQRTool(c, p, state) })
	}, 900, 480)
	t.Cleanup(func() {
		if err := s.toolsUnit.closeDeveloperToolJobs(context.Background()); err != nil {
			t.Errorf("close QR tool jobs: %v", err)
		}
	})
	return s, state, tester
}

func waitForQRTool(t *testing.T, s *app, tester *ui.Tester) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := s.ensureDeveloperToolJobs().lifecycle.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
}
