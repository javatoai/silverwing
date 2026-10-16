package media

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

type pdfiumAPI struct {
	// Keep the native handle alive for the process lifetime: registered function
	// pointers remain callable after OpenPDF returns.
	library             loadedLibrary
	initLibrary         func()
	loadMemDocument     func(unsafe.Pointer, int32, string) uintptr
	getLastError        func() uint32
	getPageCount        func(uintptr) int32
	loadPage            func(uintptr, int32) uintptr
	getPageWidth        func(uintptr) float32
	getPageHeight       func(uintptr) float32
	getPageRotation     func(uintptr) int32
	getCropBox          func(uintptr, *float32, *float32, *float32, *float32) int32
	getMediaBox         func(uintptr, *float32, *float32, *float32, *float32) int32
	closePage           func(uintptr)
	closeDocument       func(uintptr)
	createBitmap        func(int32, int32, int32) uintptr
	fillRect            func(uintptr, int32, int32, int32, int32, uint32) int32
	renderPageBitmap    func(uintptr, uintptr, int32, int32, int32, int32, int32, int32)
	getBitmapBuffer     func(uintptr) unsafe.Pointer
	getBitmapWidth      func(uintptr) int32
	getBitmapHeight     func(uintptr) int32
	getBitmapStride     func(uintptr) int32
	getBitmapFormat     func(uintptr) int32
	destroyBitmap       func(uintptr)
	textLoadPage        func(uintptr) uintptr
	textClosePage       func(uintptr)
	textCountChars      func(uintptr) int32
	textGetUnicode      func(uintptr, int32) uint32
	textGetCharBox      func(uintptr, int32, *float64, *float64, *float64, *float64) int32
	bookmarkFirstChild  func(uintptr, uintptr) uintptr
	bookmarkNextSibling func(uintptr, uintptr) uintptr
	bookmarkGetTitle    func(uintptr, unsafe.Pointer, uint32) uint32
	bookmarkGetDest     func(uintptr, uintptr) uintptr
	bookmarkGetAction   func(uintptr) uintptr
	actionGetDest       func(uintptr, uintptr) uintptr
	destGetPageIndex    func(uintptr, uintptr) int32
}

type loadedLibrary struct {
	addr  func(string) (uintptr, error)
	close func() error
}

var (
	globalPDFiumMu  sync.Mutex
	globalPDFium    *pdfiumAPI
	globalPDFiumAt  string
	globalPDFiumSem = make(chan struct{}, 1)
)

func loadPDFium(explicitPath string) (*pdfiumAPI, error) {
	globalPDFiumMu.Lock()
	defer globalPDFiumMu.Unlock()
	if globalPDFium != nil {
		if explicitPath != "" {
			requested, err := locatePDFium(explicitPath)
			if err != nil {
				return nil, err
			}
			loaded := filepath.Clean(globalPDFiumAt)
			requested = filepath.Clean(requested)
			matches := requested == loaded
			if os.PathSeparator == '\\' {
				matches = strings.EqualFold(requested, loaded)
			}
			if !matches {
				return nil, fmt.Errorf("PDFium 已从 %q 加载，当前进程不能切换到 %q", globalPDFiumAt, requested)
			}
		}
		return globalPDFium, nil
	}
	path, err := locatePDFium(explicitPath)
	if err != nil {
		return nil, err
	}
	library, err := openNativeLibrary(path)
	if err != nil {
		return nil, fmt.Errorf("加载 PDFium 失败：%s：%w", path, err)
	}
	api, err := bindPDFium(library)
	if err != nil {
		_ = library.close()
		return nil, err
	}
	api.library = library
	api.initLibrary()
	globalPDFium = api
	globalPDFiumAt = path
	return api, nil
}

func bindPDFium(library loadedLibrary) (*pdfiumAPI, error) {
	api := &pdfiumAPI{}
	bind := func(name string, fn any) error {
		address, err := library.addr(name)
		if err != nil {
			return fmt.Errorf("PDFium 运行库缺少 API %s：%w", name, err)
		}
		purego.RegisterFunc(fn, address)
		return nil
	}
	bindings := []struct {
		name string
		fn   any
	}{
		{"FPDF_InitLibrary", &api.initLibrary},
		{"FPDF_LoadMemDocument", &api.loadMemDocument},
		{"FPDF_GetLastError", &api.getLastError},
		{"FPDF_GetPageCount", &api.getPageCount},
		{"FPDF_LoadPage", &api.loadPage},
		{"FPDF_GetPageWidthF", &api.getPageWidth},
		{"FPDF_GetPageHeightF", &api.getPageHeight},
		{"FPDFPage_GetRotation", &api.getPageRotation},
		{"FPDFPage_GetCropBox", &api.getCropBox},
		{"FPDFPage_GetMediaBox", &api.getMediaBox},
		{"FPDF_ClosePage", &api.closePage},
		{"FPDF_CloseDocument", &api.closeDocument},
		{"FPDFBitmap_Create", &api.createBitmap},
		{"FPDFBitmap_FillRect", &api.fillRect},
		{"FPDF_RenderPageBitmap", &api.renderPageBitmap},
		{"FPDFBitmap_GetBuffer", &api.getBitmapBuffer},
		{"FPDFBitmap_GetWidth", &api.getBitmapWidth},
		{"FPDFBitmap_GetHeight", &api.getBitmapHeight},
		{"FPDFBitmap_GetStride", &api.getBitmapStride},
		{"FPDFBitmap_GetFormat", &api.getBitmapFormat},
		{"FPDFBitmap_Destroy", &api.destroyBitmap},
		{"FPDFText_LoadPage", &api.textLoadPage},
		{"FPDFText_ClosePage", &api.textClosePage},
		{"FPDFText_CountChars", &api.textCountChars},
		{"FPDFText_GetUnicode", &api.textGetUnicode},
		{"FPDFText_GetCharBox", &api.textGetCharBox},
		{"FPDFBookmark_GetFirstChild", &api.bookmarkFirstChild},
		{"FPDFBookmark_GetNextSibling", &api.bookmarkNextSibling},
		{"FPDFBookmark_GetTitle", &api.bookmarkGetTitle},
		{"FPDFBookmark_GetDest", &api.bookmarkGetDest},
		{"FPDFBookmark_GetAction", &api.bookmarkGetAction},
		{"FPDFAction_GetDest", &api.actionGetDest},
		{"FPDFDest_GetDestPageIndex", &api.destGetPageIndex},
	}
	for _, binding := range bindings {
		if err := bind(binding.name, binding.fn); err != nil {
			return nil, err
		}
	}
	return api, nil
}

func locatePDFium(explicitPath string) (string, error) {
	name := pdfiumLibraryName()
	var candidates []string
	if explicitPath != "" {
		candidates = append(candidates, explicitPath)
	} else if env := strings.TrimSpace(os.Getenv("SILVERWING_PDFIUM_PATH")); env != "" {
		candidates = append(candidates, env)
	} else if executable, err := os.Executable(); err == nil {
		appDir := filepath.Dir(executable)
		candidates = append(candidates, filepath.Join(appDir, name), filepath.Join(appDir, "native", name))
		if runtime.GOOS == "darwin" {
			candidates = append(candidates, filepath.Join(appDir, "..", "Resources", "native", name))
		} else {
			candidates = append(candidates, filepath.Join(appDir, "resources", "native", name))
		}
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			candidate = filepath.Join(candidate, name)
			info, err = os.Stat(candidate)
		}
		if err == nil && info.Mode().IsRegular() {
			absolute, absErr := filepath.Abs(candidate)
			if absErr != nil {
				return "", fmt.Errorf("解析 PDFium 路径失败：%w", absErr)
			}
			return filepath.Clean(absolute), nil
		}
	}
	if explicitPath != "" {
		return "", fmt.Errorf("PDF 预览不可用：找不到 PDFium 运行库 %q；请提供 %s 路径", explicitPath, name)
	}
	if env := strings.TrimSpace(os.Getenv("SILVERWING_PDFIUM_PATH")); env != "" {
		return "", fmt.Errorf("PDF 预览不可用：SILVERWING_PDFIUM_PATH 指向的文件不存在（%s）；请部署 %s", env, name)
	}
	return "", fmt.Errorf("PDF 预览不可用：未找到 %s；请将匹配当前系统和架构的 PDFium 运行库放在程序目录，或设置 SILVERWING_PDFIUM_PATH。说明见 docs/NATIVE-MEDIA.md", name)
}

func acquirePDFium(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case globalPDFiumSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w: %v", ErrCancelled, ctx.Err())
	}
}

func releasePDFium() { <-globalPDFiumSem }
