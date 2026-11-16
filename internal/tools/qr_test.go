package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateQRPreservesUTF8AndHasOpaqueQuietZone(t *testing.T) {
	const input = "  中文🙂 e\u0301\r\n"
	data, err := GenerateQR(input)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	width, height := img.Bounds().Dx(), img.Bounds().Dy()
	if width != height || width%QRModulePixels != 0 {
		t.Fatalf("generated PNG bounds = %v", img.Bounds())
	}
	const margin = 4 * QRModulePixels
	for y := range height {
		for x := range width {
			if x >= margin && y >= margin && x < width-margin && y < height-margin {
				continue
			}
			r, g, b, a := img.At(x, y).RGBA()
			if r != 0xffff || g != 0xffff || b != 0xffff || a != 0xffff {
				t.Fatalf("quiet zone at (%d,%d) is not opaque white", x, y)
			}
		}
	}
	if r, _, _, a := img.At(margin, margin).RGBA(); r != 0 || a != 0xffff {
		t.Fatal("finder pattern does not start after four quiet modules")
	}
	if got, err := DecodeQR(context.Background(), "", data); err != nil || got != input {
		t.Fatalf("generated QR decodes to %q, %v", got, err)
	}
}

func TestDecodeIndependentQRFromWebPAndJPEG(t *testing.T) {
	// Produced with Segno 1.6.6 (UTF-8 ECI, four quiet modules) and saved as
	// lossless WebP with Pillow, independently of the application's writer.
	const fixture = "UklGRp4AAABXRUJQVlA4TJEAAAAvg8AgAA8w//M///MfeHDUNpIghUHxZxkGAdC7e/eM6H9UVVVV7Q8A3CzAM8t0WofoaIq30DqP5m3JTOsOUFXgrkqcVWczZ8YOWQNDDwUSGvNsM1xpp6TmuAtnTDlkuhXb7lqDNllnBGyC86wKyrQzwNEUPbOcZF6ibYHrdE0mt86SeVeiKNwBxjbnXVVVVX8AAA=="
	const want = "  独立测试🙂\n"
	data, err := base64.StdEncoding.DecodeString(fixture)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if got, err := DecodeQR(ctx, "", data); err != nil || got != want {
		t.Fatalf("WebP QR decodes to %q, %v", got, err)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, img, nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sample.jpg")
	if err := os.WriteFile(path, jpg.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := DecodeQR(ctx, path, nil); err != nil || got != want {
		t.Fatalf("JPEG QR file decodes to %q, %v", got, err)
	}
}

func TestQRRejectsUnsupportedOrOversizedInputs(t *testing.T) {
	for _, invalid := range []string{"", "\xff", "has\x00null", strings.Repeat("a", 3000), strings.Repeat("1", maxQRContentBytes+1)} {
		if data, err := GenerateQR(invalid); err == nil || data != nil {
			t.Errorf("generated QR with invalid content of %d bytes", len(invalid))
		}
	}
	img := image.NewGray(image.Rect(0, 0, 2, 2))
	var validGIF, blankPNG bytes.Buffer
	if err := gif.Encode(&validGIF, img, nil); err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(&blankPNG, img); err != nil {
		t.Fatal(err)
	}
	// Announce an oversized IHDR without allocating its declared pixel buffer.
	largePNG := bytes.Clone(blankPNG.Bytes())
	binary.BigEndian.PutUint32(largePNG[16:20], 4001)
	binary.BigEndian.PutUint32(largePNG[20:24], 4000)
	binary.BigEndian.PutUint32(largePNG[29:33], crc32.ChecksumIEEE(largePNG[12:29]))
	ctx := context.Background()
	tests := []struct {
		data        []byte
		wantMessage string
	}{
		{[]byte("not an image"), "PNG、JPEG 或 WebP"},
		{validGIF.Bytes(), "PNG、JPEG 或 WebP"},
		{blankPNG.Bytes(), "未能识别二维码"},
		{largePNG, "像素"},
		{make([]byte, MaxQRImageBytes+1), "字节"},
	}
	for _, test := range tests {
		if _, err := DecodeQR(ctx, "", test.data); err == nil || !strings.Contains(err.Error(), test.wantMessage) {
			t.Errorf("expected error containing %q, got %v", test.wantMessage, err)
		}
	}
	path := filepath.Join(t.TempDir(), "oversized.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	truncateErr := file.Truncate(MaxQRImageBytes + 1)
	if err := file.Close(); err != nil || truncateErr != nil {
		t.Fatal(err, truncateErr)
	}
	if _, err := DecodeQR(ctx, path, nil); err == nil || !strings.Contains(err.Error(), "大小限制") {
		t.Fatalf("oversized file error = %v", err)
	}
	if _, err := DecodeQR(ctx, path, blankPNG.Bytes()); err == nil || !strings.Contains(err.Error(), "一个图片来源") {
		t.Fatalf("ambiguous source error = %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := DecodeQR(canceled, "", blankPNG.Bytes()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled QR decode returned %v", err)
	}
}
