package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/media"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func TestMarkdownImageClickSelectsOriginalOnlyForComments(t *testing.T) {
	for _, comment := range []bool{false, true} {
		t.Run(fmt.Sprintf("comment-%t", comment), func(t *testing.T) {
			const target = "https://example.invalid/synthetic-image"
			const source = "![本地示例](" + target + ")"
			path := filepath.Join(t.TempDir(), "中文 原图.png")
			owner := newReadonlyMarkdownState("synthetic-comment", source, map[string]string{target: path})
			owner.commentReading = comment
			t.Cleanup(func() { closeMarkdownPreviewState(owner) })
			bitmap := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 240, 100)))
			native := ensureFrameworkMarkdown(owner)
			for index := 1; index < native.document.Len(); index++ {
				for _, node := range native.document.Inlines(index) {
					if node.Kind != parser.Image {
						continue
					}
					part := native.partAt(owner, node.Start)
					start, end := native.projection.OriginalOffset(node.Start), native.projection.OriginalOffset(node.End)
					key := fmt.Sprintf("%d:%d:%d", part.ID, start, end)
					owner.inlineImages[key] = &markdownInlineImageState{
						key: strings.Join([]string{owner.taskID, owner.path, target, path}, "\x00"), bitmap: bitmap,
					}
					if comment {
						owner.inlineImages[key].sourcePath = path
					}
				}
			}
			state := &app{page: "tasks", tab: 2}
			tester := ui.NewTester(func(context *ui.Context) {
				theme := setTheme(context, false)
				state.renderMarkdownPreview(context, theme, owner)
				state.renderMarkdownBitmapViewer(context, theme)
			}, 640, 520)
			if err := tester.Click("Markdown 图片：本地示例"); err != nil {
				t.Fatal(err)
			}
			viewer := state.materialsUnit.state.markdownBitmapViewer
			if viewer == nil || viewer.owner != owner {
				t.Fatal("image click did not attach the viewer to its Markdown owner")
			}
			if comment {
				if viewer.sourcePath != path || viewer.media.imageExpandedBitmap != nil || viewer.media.imageExpandedError == "" {
					t.Fatal("comment image did not request the original or silently reused its thumbnail without a native window")
				}
			} else if viewer.sourcePath != "" || viewer.media.imageExpandedBitmap != bitmap {
				t.Fatal("ordinary Markdown no longer uses its existing bitmap viewer")
			}
			ctx, _ := beginMarkdownLocalImageViewerRequest(viewer)
			viewer.media.imageExpandedBitmap = bitmap
			updateReadonlyMarkdownState(owner, source+"\n\n评论更新", map[string]string{target: path})
			if state.materialsUnit.state.markdownBitmapViewer != nil || ctx.Err() == nil || viewer.media.imageExpandedBitmap != nil {
				t.Fatal("updating the comment did not immediately cancel loading and release the expanded bitmap")
			}
			state.openMarkdownBitmapViewer(owner, bitmap)
			viewer = state.materialsUnit.state.markdownBitmapViewer
			closeMarkdownPreviewState(owner)
			if state.materialsUnit.state.markdownBitmapViewer != nil || viewer.media.imageExpandedBitmap != nil {
				t.Fatal("closing the reader retained its expanded bitmap")
			}
		})
	}
}

func TestMarkdownLocalImageViewerLoadsOriginalResolution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "comment image.png")
	writeMarkdownImageFixture(t, path, 2400, 1600)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeHash := sha256.Sum256(before)

	inline, err := decodeNativeImagePreview(context.Background(), path, 1200)
	if err != nil {
		t.Fatal(err)
	}
	inlineWidth, inlineHeight := inline.Size()
	if inlineWidth != 1200 || inlineHeight != 800 {
		t.Fatalf("inline preview dimensions = %d × %d, want 1200 × 800", inlineWidth, inlineHeight)
	}

	original, info, err := loadMarkdownLocalImageBitmap(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	originalWidth, originalHeight := original.Size()
	if originalWidth != 2400 || originalHeight != 1600 || info.Width != 2400 || info.Height != 1600 {
		t.Fatalf("expanded image dimensions = %d × %d, info %#v; want 2400 × 1600", originalWidth, originalHeight, info)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(after) != beforeHash {
		t.Fatal("opening the original image changed the cached source bytes")
	}
}

func TestMarkdownLocalImageViewerHonorsMediaLimits(t *testing.T) {
	t.Run("bytes", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "oversized.png")
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(media.DefaultMaxImageBytes + 1); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if _, _, err := loadMarkdownLocalImageBitmap(context.Background(), path); err == nil {
			t.Fatal("image larger than 32 MiB was accepted")
		}
	})

	t.Run("pixels", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "too-many-pixels.png")
		if err := os.WriteFile(path, pngConfigOnly(5001, 4000), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := loadMarkdownLocalImageBitmap(context.Background(), path); err == nil {
			t.Fatal("image larger than 20 MP was accepted")
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cancelled.png")
		writeMarkdownImageFixture(t, path, 64, 32)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := loadMarkdownLocalImageBitmap(ctx, path); err == nil {
			t.Fatal("cancelled image request returned successfully")
		}
	})
}

func TestMarkdownLocalImageViewerRetryAndLateResultGuard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "retry.png")
	if err := os.WriteFile(path, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadMarkdownLocalImageBitmap(context.Background(), path); err == nil {
		t.Fatal("corrupt image fixture unexpectedly loaded")
	}
	writeMarkdownImageFixture(t, path, 96, 64)
	if _, info, err := loadMarkdownLocalImageBitmap(context.Background(), path); err != nil || info.Width != 96 || info.Height != 64 {
		t.Fatalf("valid retry failed: info %#v, error %v", info, err)
	}

	owner := newReadonlyMarkdownState("comment", "comment image", nil)
	owner.commentReading = true
	viewer := &markdownBitmapViewerState{
		owner: owner, document: owner.doc, source: owner.doc.Source(), sourcePath: path,
		page: "tasks", settingsPage: "", tab: 2,
		media: &nativeMediaState{kind: "image", imageExpanded: true},
	}
	state := &app{
		materialsUnit: materialsController{state: materialsState{markdownBitmapViewer: viewer}},
		page:          "tasks",
		tab:           2,
	}
	firstCtx, firstGeneration := beginMarkdownLocalImageViewerRequest(viewer)
	viewer.media.imageExpandedLoading = false
	viewer.media.imageExpandedError = "暂时无法读取"
	secondCtx, secondGeneration := beginMarkdownLocalImageViewerRequest(viewer)
	if firstCtx.Err() == nil || secondCtx.Err() != nil || secondGeneration != firstGeneration+1 || viewer.media.imageExpandedError != "" {
		t.Fatal("retry did not cancel the previous request and reset the error state")
	}
	if markdownLocalImageViewerResultCurrent(state, viewer, firstGeneration) {
		t.Fatal("an earlier request generation was accepted")
	}
	if !markdownLocalImageViewerResultCurrent(state, viewer, secondGeneration) {
		t.Fatal("the current request was rejected")
	}

	state.page = "settings"
	if markdownLocalImageViewerResultCurrent(state, viewer, secondGeneration) {
		t.Fatal("result for a different page was accepted")
	}
	state.page = "tasks"
	state.tab++
	if markdownLocalImageViewerResultCurrent(state, viewer, secondGeneration) {
		t.Fatal("result for a different tab was accepted")
	}
	state.tab--
	owner.doc.SyncExternal("new comment source")
	if markdownLocalImageViewerResultCurrent(state, viewer, secondGeneration) {
		t.Fatal("result for an updated comment document was accepted")
	}
	closeMarkdownBitmapViewer(viewer)
	if secondCtx.Err() == nil || viewer.media.imageExpandedBitmap != nil || viewer.media.imageExpandedLoading || viewer.media.imageExpandedError != "" {
		t.Fatal("closing the viewer did not cancel and clear its large-image state")
	}
	closeMarkdownPreviewState(owner)
}

func TestMarkdownLocalImageViewerRejectsReplacedAndClosedOwners(t *testing.T) {
	owner := newReadonlyMarkdownState("comment", "comment image", nil)
	owner.commentReading = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	viewer := &markdownBitmapViewerState{
		owner: owner, document: owner.doc, source: owner.doc.Source(), sourcePath: "fixture.png",
		page: "tasks", tab: 1, generation: 7, requestCtx: ctx,
		media: &nativeMediaState{kind: "image", imageExpanded: true},
	}
	state := &app{
		materialsUnit: materialsController{state: materialsState{markdownBitmapViewer: viewer}},
		page:          "tasks",
		tab:           1,
	}
	if !markdownLocalImageViewerResultCurrent(state, viewer, 7) {
		t.Fatal("valid current viewer result was rejected")
	}
	state.materialsUnit.state.markdownBitmapViewer = &markdownBitmapViewerState{}
	if markdownLocalImageViewerResultCurrent(state, viewer, 7) {
		t.Fatal("result for a replaced viewer was accepted")
	}
	state.materialsUnit.state.markdownBitmapViewer = viewer
	owner.closed = true
	if markdownLocalImageViewerResultCurrent(state, viewer, 7) {
		t.Fatal("result for a closed owner was accepted")
	}
	closeMarkdownPreviewState(owner)
}

func writeMarkdownImageFixture(t *testing.T, path string, width, height int) {
	t.Helper()
	pixels := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			pixels.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: uint8(x ^ y), A: 255})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, pixels); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func pngConfigOnly(width, height int) []byte {
	data := []byte{137, 80, 78, 71, 13, 10, 26, 10}
	chunk := func(kind string, payload []byte) {
		length := make([]byte, 4)
		binary.BigEndian.PutUint32(length, uint32(len(payload)))
		data = append(data, length...)
		data = append(data, kind...)
		data = append(data, payload...)
		checksum := crc32.ChecksumIEEE(append([]byte(kind), payload...))
		encodedChecksum := make([]byte, 4)
		binary.BigEndian.PutUint32(encodedChecksum, checksum)
		data = append(data, encodedChecksum...)
	}
	header := make([]byte, 13)
	binary.BigEndian.PutUint32(header[:4], uint32(width))
	binary.BigEndian.PutUint32(header[4:8], uint32(height))
	header[8], header[9] = 8, 2
	chunk("IHDR", header)
	chunk("IEND", nil)
	return data
}
