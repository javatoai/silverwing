package application

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"silverwing-go/internal/documents"
	"testing"
)

func TestDocumentCapturedClipboardUsesCapturedPNGAndPreservesDocument(t *testing.T) {
	a, root, taskPath := materialTextApplicationFixture(t)
	_ = taskPath
	folder := filepath.Join(root, "doc.assets")
	if err := os.MkdirAll(folder, 0755); err != nil {
		t.Fatal(err)
	}
	bitmap := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	bitmap.Set(0, 0, color.NRGBA{R: 123, A: 255})
	var output bytes.Buffer
	if err := png.Encode(&output, bitmap); err != nil {
		t.Fatal(err)
	}
	result, err := a.Dispatch(context.Background(), Action{Kind: "document.paste", TaskID: "task", Payload: map[string]any{"directory": folder, "capturedClipboard": true, "clipboard": documents.ClipboardPayload{ImagePNG: output.Bytes()}}})
	if err != nil {
		t.Fatal(err)
	}
	batch, ok := result.Data.(documents.FileOperationBatchResult)
	if !ok || len(batch.Completed) != 1 {
		t.Fatalf("unexpected import: %#v", result.Data)
	}
	data, err := os.ReadFile(filepath.Join(result.Path, filepath.FromSlash(batch.Completed[0].RelativePath)))
	if err != nil || !bytes.Equal(data, output.Bytes()) {
		t.Fatal("captured screenshot bytes were not used")
	}
}
