package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"testing"
)

func TestImageBytesDecodeSnapshotWithoutOpeningPath(t *testing.T) {
	input := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	input.SetNRGBA(0, 0, color.NRGBA{R: 240, G: 30, A: 255})
	var data bytes.Buffer
	if err := png.Encode(&data, input); err != nil {
		t.Fatal(err)
	}
	document, err := OpenImageBytes(context.Background(), filepath.Join(t.TempDir(), "missing", "snapshot.png"), data.Bytes(), Options{})
	if err != nil {
		t.Fatal("immutable snapshot attempted to open its nonexistent path hint", err)
	}
	defer document.Close()
	for i := range data.Bytes() {
		data.Bytes()[i] = 0
	}
	pixels, err := document.RenderRGBA(context.Background(), 0)
	if err != nil || document.Info().Width != 3 || document.Info().Height != 2 {
		t.Fatal("decoded document retained caller-owned input", document.Info(), err)
	}
	r, g, _, _ := pixels.RGBA.At(0, 0).RGBA()
	if r <= g {
		t.Fatal("decoded snapshot pixels changed")
	}
}

func TestImageBytesDIBIconKeepsSnapshotForRepeatedDecoding(t *testing.T) {
	// One 2x2, 32bpp DIB-backed icon with a two-row AND mask. ICO stores
	// twice the pixel height in its DIB header; conversion must not edit it.
	const dibSize = 40 + 2*2*4 + 2*4
	data := make([]byte, 22+dibSize)
	binary.LittleEndian.PutUint16(data[2:4], 1)
	binary.LittleEndian.PutUint16(data[4:6], 1)
	data[6], data[7] = 2, 2
	binary.LittleEndian.PutUint16(data[10:12], 1)
	binary.LittleEndian.PutUint16(data[12:14], 32)
	binary.LittleEndian.PutUint32(data[14:18], dibSize)
	binary.LittleEndian.PutUint32(data[18:22], 22)
	dib := data[22:]
	binary.LittleEndian.PutUint32(dib[0:4], 40)
	binary.LittleEndian.PutUint32(dib[4:8], 2)
	binary.LittleEndian.PutUint32(dib[8:12], 4)
	binary.LittleEndian.PutUint16(dib[12:14], 1)
	binary.LittleEndian.PutUint16(dib[14:16], 32)
	binary.LittleEndian.PutUint32(dib[20:24], 16)
	for offset := 40; offset < 56; offset += 4 {
		dib[offset+2], dib[offset+3] = 240, 255
	}
	original := append([]byte(nil), data...)
	hash := sha256.Sum256(data)
	for attempt := 0; attempt < 2; attempt++ {
		doc, err := OpenImageBytes(context.Background(), "snapshot.ico", data, Options{})
		if err != nil {
			t.Fatalf("decode %d: %v", attempt, err)
		}
		info := doc.Info()
		pixels, renderErr := doc.RenderRGBA(context.Background(), 0)
		doc.Close()
		if renderErr != nil || info.Width != 2 || info.Height != 2 || pixels.RGBA == nil {
			t.Fatalf("decode %d changed dimensions: %+v, %v", attempt, info, renderErr)
		}
		if !bytes.Equal(data, original) || sha256.Sum256(data) != hash {
			t.Fatalf("decode %d mutated the caller's icon snapshot", attempt)
		}
	}
}

func TestMediaBytesRejectBoundsAndCancellationBeforeDecoding(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := OpenImageBytes(ctx, "snapshot.png", []byte("not an image"), Options{}); err == nil {
		t.Fatal("cancelled image snapshot decoded")
	}
	if _, err := OpenPDFBytes(ctx, []byte("not a PDF"), Options{}); err == nil {
		t.Fatal("cancelled PDF snapshot reached the native loader")
	}
	if _, err := OpenImageBytes(context.Background(), "snapshot.png", []byte{1, 2}, Options{MaxImageBytes: 1}); err == nil {
		t.Fatal("image snapshot exceeded its byte budget")
	}
	if _, err := OpenPDFBytes(context.Background(), []byte{1, 2}, Options{MaxPDFBytes: 1}); err == nil {
		t.Fatal("PDF snapshot exceeded its byte budget")
	}
	if _, err := OpenImageBytes(context.Background(), "snapshot.exe", []byte{1}, Options{}); err == nil {
		t.Fatal("unsupported image hint accepted")
	}
	if _, err := OpenPDFBytes(context.Background(), nil, Options{}); err == nil {
		t.Fatal("empty PDF snapshot accepted")
	}
}
