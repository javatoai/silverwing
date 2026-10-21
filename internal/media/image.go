package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"math"
	"strings"
	"sync"

	"golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

var supportedImageExtensions = map[string]struct{}{
	"png": {}, "jpg": {}, "jpeg": {}, "webp": {}, "gif": {}, "bmp": {}, "ico": {},
}

// ImageDocument owns one decoded, orientation-corrected still image. Animated
// formats intentionally preview only their first frame.
type ImageDocument struct {
	mu      sync.Mutex
	closed  bool
	info    ImageInfo
	image   image.Image
	options Options
	cache   *pngCache
}

func OpenImage(ctx context.Context, path string, options Options) (*ImageDocument, error) {
	options, err := options.normalized()
	if err != nil {
		return nil, err
	}
	if extension := extensionOf(path); extension != "" {
		if _, ok := supportedImageExtensions[extension]; !ok {
			return nil, fmt.Errorf("%w：%s", ErrUnsupportedType, extension)
		}
	} else {
		return nil, ErrUnsupportedType
	}
	data, err := readRegularFile(ctx, path, options.MaxImageBytes, "图片")
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("图片文件为空")
	}
	decoded, format, err := decodeImage(data, options.MaxImagePixels)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(format, "jpeg") || strings.EqualFold(format, "jpg") {
		if orientation := jpegOrientation(data); orientation != 0 && orientation != 1 {
			decoded, err = applyOrientation(ctx, decoded, orientation)
			if err != nil {
				return nil, err
			}
		}
	}
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	bounds := decoded.Bounds()
	return &ImageDocument{
		info:  ImageInfo{Width: bounds.Dx(), Height: bounds.Dy(), Format: format},
		image: decoded, options: options, cache: newPNGCache(options.CacheBytes),
	}, nil
}

func (d *ImageDocument) Info() ImageInfo {
	if d == nil {
		return ImageInfo{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.info
}

// RenderPNG encodes a still-image PNG. A positive width requests a target
// width; the source image is never upscaled because the UI applies display
// zoom independently of source resolution.
func (d *ImageDocument) RenderPNG(ctx context.Context, widthPx int) (RenderedPage, error) {
	if d == nil {
		return RenderedPage{}, ErrClosed
	}
	if err := contextError(ctx); err != nil {
		return RenderedPage{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.image == nil {
		return RenderedPage{}, ErrClosed
	}
	if widthPx < 0 {
		return RenderedPage{}, errors.New("图片预览宽度不能为负数")
	}
	if widthPx > 0 && widthPx > d.info.Width {
		widthPx = d.info.Width
	}
	if widthPx == 0 {
		widthPx = d.info.Width
	}
	key := fmt.Sprintf("image:%d", widthPx)
	if png, ok := d.cache.get(key); ok {
		width, height := d.outputSize(widthPx)
		return RenderedPage{PageIndex: -1, PageCount: 1, Width: width, Height: height,
			AspectRatio: float64(d.info.Width) / float64(d.info.Height), PNG: png}, nil
	}
	if err := contextError(ctx); err != nil {
		return RenderedPage{}, err
	}
	img := d.image
	width, height := d.outputSize(widthPx)
	if width != d.info.Width {
		resized := image.NewNRGBA(image.Rect(0, 0, width, height))
		xdraw.CatmullRom.Scale(resized, resized.Bounds(), img, img.Bounds(), xdraw.Src, nil)
		img = resized
	}
	var output bytes.Buffer
	if err := encodePNG(&output, img); err != nil {
		return RenderedPage{}, fmt.Errorf("编码图片预览失败：%w", err)
	}
	if err := contextError(ctx); err != nil {
		return RenderedPage{}, err
	}
	png := output.Bytes()
	d.cache.put(key, png)
	return RenderedPage{PageIndex: -1, PageCount: 1, Width: width, Height: height,
		AspectRatio: float64(d.info.Width) / float64(d.info.Height), PNG: append([]byte(nil), png...)}, nil
}

func (d *ImageDocument) ClearCache() {
	if d == nil {
		return
	}
	d.cache.clear()
}

func (d *ImageDocument) outputSize(widthPx int) (int, int) {
	width, height := d.info.Width, d.info.Height
	if widthPx > 0 && widthPx < width {
		width = widthPx
		height = maxInt(1, int(math.Round(float64(d.info.Height)*float64(width)/float64(d.info.Width))))
	}
	return width, height
}

func (d *ImageDocument) Close() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	d.image = nil
	d.cache.clear()
	return nil
}

func decodeImage(data []byte, maxPixels int64) (image.Image, string, error) {
	encoded := data
	format := ""
	if isICO(data) {
		var err error
		encoded, err = bestICOImage(data)
		if err != nil {
			return nil, "", fmt.Errorf("无法解码图片，请使用外部应用打开：%w", err)
		}
		format = "ico"
	}
	config, decodedFormat, err := image.DecodeConfig(bytes.NewReader(encoded))
	if err != nil {
		return nil, "", fmt.Errorf("无法解码图片，请使用外部应用打开：%w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width) > maxPixels/int64(config.Height) {
		return nil, "", fmt.Errorf("图片超过 %s 像素预览限制，请使用外部应用打开", formatPixelLimit(maxPixels))
	}
	if format == "" {
		format = decodedFormat
	}
	img, actualFormat, err := image.Decode(bytes.NewReader(encoded))
	if err != nil {
		return nil, "", fmt.Errorf("无法解码图片，请使用外部应用打开：%w", err)
	}
	if format != "ico" {
		format = actualFormat
	}
	bounds := img.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 || int64(bounds.Dx()) > maxPixels/int64(bounds.Dy()) {
		return nil, "", fmt.Errorf("图片超过 %s 像素预览限制，请使用外部应用打开", formatPixelLimit(maxPixels))
	}
	return img, format, nil
}

func isICO(data []byte) bool {
	return len(data) >= 6 && binary.LittleEndian.Uint16(data[0:2]) == 0 && binary.LittleEndian.Uint16(data[2:4]) == 1
}

func bestICOImage(data []byte) ([]byte, error) {
	if len(data) < 6 {
		return nil, errors.New("ICO 文件头不完整")
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count < 1 || count > 1024 || len(data) < 6+count*16 {
		return nil, errors.New("ICO 图片目录无效")
	}
	type entry struct {
		area int64
		data []byte
	}
	entries := make([]entry, 0, count)
	for index := 0; index < count; index++ {
		row := data[6+index*16 : 6+(index+1)*16]
		width, height := int(row[0]), int(row[1])
		if width == 0 {
			width = 256
		}
		if height == 0 {
			height = 256
		}
		size := int(binary.LittleEndian.Uint32(row[8:12]))
		offset := int(binary.LittleEndian.Uint32(row[12:16]))
		if size <= 0 || offset < 0 || offset > len(data) || size > len(data)-offset {
			continue
		}
		entries = append(entries, entry{area: int64(width) * int64(height), data: data[offset : offset+size]})
	}
	if len(entries) == 0 {
		return nil, errors.New("ICO 中没有有效图片")
	}
	best := entries[0]
	for _, candidate := range entries[1:] {
		if candidate.area > best.area {
			best = candidate
		}
	}
	if len(best.data) >= 8 && bytes.Equal(best.data[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		return best.data, nil
	}
	return icoDIBToBMP(best.data)
}

func icoDIBToBMP(dib []byte) ([]byte, error) {
	if len(dib) < 40 {
		return nil, errors.New("ICO 位图头格式暂不支持")
	}
	headerSize := int(binary.LittleEndian.Uint32(dib[:4]))
	if headerSize < 40 || headerSize > len(dib) {
		return nil, errors.New("ICO 位图头无效")
	}
	width := int32(binary.LittleEndian.Uint32(dib[4:8]))
	doubleHeight := int32(binary.LittleEndian.Uint32(dib[8:12]))
	bpp := binary.LittleEndian.Uint16(dib[14:16])
	if width <= 0 || doubleHeight == 0 || doubleHeight == -1<<31 || bpp == 0 {
		return nil, errors.New("ICO 位图尺寸无效")
	}
	height := doubleHeight
	if height < 0 {
		height = -height
	}
	if height%2 != 0 {
		return nil, errors.New("ICO 位图高度无效")
	}
	height /= 2
	binary.LittleEndian.PutUint32(dib[8:12], uint32(height))
	paletteEntries := uint32(binary.LittleEndian.Uint32(dib[32:36]))
	if paletteEntries == 0 && bpp <= 8 {
		paletteEntries = 1 << bpp
	}
	if paletteEntries > uint32(len(dib)/4) {
		return nil, errors.New("ICO 调色板数据不完整")
	}
	compression := binary.LittleEndian.Uint32(dib[16:20])
	extraMasks := 0
	if compression == 3 && headerSize == 40 {
		extraMasks = 12
	}
	dataOffset := 14 + headerSize + extraMasks + int(paletteEntries)*4
	if dataOffset > 14+len(dib) {
		return nil, errors.New("ICO 调色板数据不完整")
	}
	bmpData := make([]byte, 14+len(dib))
	bmpData[0], bmpData[1] = 'B', 'M'
	binary.LittleEndian.PutUint32(bmpData[2:6], uint32(len(bmpData)))
	binary.LittleEndian.PutUint32(bmpData[10:14], uint32(dataOffset))
	copy(bmpData[14:], dib)
	if _, err := bmp.DecodeConfig(bytes.NewReader(bmpData)); err != nil {
		return nil, fmt.Errorf("ICO 位图格式不受支持：%w", err)
	}
	return bmpData, nil
}

func jpegOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return 0
	}
	for offset := 2; offset+4 <= len(data); {
		if data[offset] != 0xff {
			return 0
		}
		marker := data[offset+1]
		offset += 2
		if marker == 0xd9 || marker == 0xda {
			return 0
		}
		if marker >= 0xd0 && marker <= 0xd7 || marker == 0x01 {
			continue
		}
		if offset+2 > len(data) {
			return 0
		}
		length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if length < 2 || offset+length > len(data) {
			return 0
		}
		segment := data[offset+2 : offset+length]
		if marker == 0xe1 && len(segment) >= 14 && string(segment[:6]) == "Exif\x00\x00" {
			if value := tiffOrientation(segment[6:]); value >= 1 && value <= 8 {
				return value
			}
		}
		offset += length
	}
	return 0
}

func tiffOrientation(data []byte) int {
	if len(data) < 8 {
		return 0
	}
	var order binary.ByteOrder
	switch string(data[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0
	}
	if order.Uint16(data[2:4]) != 42 {
		return 0
	}
	ifdOffset := uint64(order.Uint32(data[4:8]))
	if ifdOffset+2 > uint64(len(data)) {
		return 0
	}
	count := int(order.Uint16(data[ifdOffset : ifdOffset+2]))
	entryOffset := int(ifdOffset) + 2
	for index := 0; index < count; index++ {
		position := entryOffset + index*12
		if position < 0 || position+12 > len(data) {
			return 0
		}
		entry := data[position : position+12]
		if order.Uint16(entry[0:2]) == 0x0112 && order.Uint16(entry[2:4]) == 3 && order.Uint32(entry[4:8]) >= 1 {
			return int(order.Uint16(entry[8:10]))
		}
	}
	return 0
}

func applyOrientation(ctx context.Context, src image.Image, orientation int) (image.Image, error) {
	source := src.Bounds()
	sourceWidth, sourceHeight := source.Dx(), source.Dy()
	width, height := sourceWidth, sourceHeight
	if orientation >= 5 && orientation <= 8 {
		width, height = sourceHeight, sourceWidth
	}
	destination := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < sourceHeight; y++ {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		for x := 0; x < sourceWidth; x++ {
			var dx, dy int
			switch orientation {
			case 2:
				dx, dy = sourceWidth-1-x, y
			case 3:
				dx, dy = sourceWidth-1-x, sourceHeight-1-y
			case 4:
				dx, dy = x, sourceHeight-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = sourceHeight-1-y, x
			case 7:
				dx, dy = sourceHeight-1-y, sourceWidth-1-x
			case 8:
				dx, dy = y, sourceWidth-1-x
			default:
				dx, dy = x, y
			}
			destination.Set(dx, dy, color.NRGBAModel.Convert(src.At(source.Min.X+x, source.Min.Y+y)))
		}
	}
	return destination, nil
}

func formatPixelLimit(maxPixels int64) string {
	return fmt.Sprintf("%d", maxPixels)
}

func encodePNG(w io.Writer, img image.Image) error {
	return png.Encode(w, img)
}

func extensionOf(path string) string {
	name := path
	if index := strings.LastIndexAny(name, `/\`); index >= 0 {
		name = name[index+1:]
	}
	if index := strings.LastIndexByte(name, '.'); index >= 0 {
		return strings.ToLower(name[index+1:])
	}
	return ""
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
