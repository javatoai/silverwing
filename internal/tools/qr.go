package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"strings"
	"unicode/utf8"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"

	"silverwing-go/internal/media"
)

const (
	QRModulePixels   = 8
	MaxQRImageBytes  = 10 << 20
	MaxQRImagePixels = 16_000_000

	// A version 40 code can hold at most 7089 numeric digits. Other
	// encodings have lower capacities, checked by the writer.
	maxQRContentBytes = 7089
)

// GenerateQR renders an opaque black-on-white PNG with four quiet modules on
// every side and UTF-8 ECI for byte-mode content.
func GenerateQR(input string) ([]byte, error) {
	if input == "" {
		return nil, errors.New("请输入二维码内容")
	}
	if len(input) > maxQRContentBytes {
		return nil, errors.New("内容超出二维码容量，请缩短后重试")
	}
	if !utf8.ValidString(input) || strings.IndexByte(input, 0) >= 0 {
		return nil, errors.New("请输入不含空字符（NUL）的 UTF-8 文本")
	}
	modules, err := qrcode.NewQRCodeWriter().Encode(input, gozxing.BarcodeFormat_QR_CODE, 0, 0, map[gozxing.EncodeHintType]any{
		gozxing.EncodeHintType_CHARACTER_SET: "UTF-8",
		gozxing.EncodeHintType_MARGIN:        4,
	})
	if err != nil {
		return nil, fmt.Errorf("无法生成二维码，请检查内容长度：%w", err)
	}
	width := modules.GetWidth() * QRModulePixels
	height := modules.GetHeight() * QRModulePixels
	img := image.NewGray(image.Rect(0, 0, width, height))
	for y := range height {
		row := img.Pix[y*img.Stride : y*img.Stride+width]
		for x := range row {
			if !modules.Get(x/QRModulePixels, y/QRModulePixels) {
				row[x] = 255
			}
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, img); err != nil {
		return nil, fmt.Errorf("无法编码二维码图片：%w", err)
	}
	return output.Bytes(), nil
}

// DecodeQR reads a single QR code from a PNG, JPEG or WebP file or byte snapshot.
func DecodeQR(ctx context.Context, path string, imageBytes []byte) (string, error) {
	img, err := media.DecodeStillImage(ctx, path, imageBytes, MaxQRImageBytes, MaxQRImagePixels, "png", "jpeg", "webp")
	if ctx != nil && ctx.Err() != nil {
		return "", ctx.Err()
	}
	if errors.Is(err, media.ErrUnsupportedType) || errors.Is(err, image.ErrFormat) {
		return "", errors.New("请选择有效的 PNG、JPEG 或 WebP 图片")
	}
	if err != nil {
		return "", err
	}
	bitmap, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", fmt.Errorf("无法读取二维码图片：%w", err)
	}
	result, err := qrcode.NewQRCodeReader().Decode(bitmap, map[gozxing.DecodeHintType]any{
		gozxing.DecodeHintType_TRY_HARDER: true,
	})
	if ctx != nil && ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", errors.New("未能识别二维码，请确认图片清晰且二维码完整")
	}
	decoded := result.GetText()
	if !utf8.ValidString(decoded) || strings.IndexByte(decoded, 0) >= 0 {
		return "", errors.New("二维码内容无法显示为 UTF-8 文本")
	}
	return decoded, nil
}
