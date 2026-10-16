package editor

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"silverwing-go/internal/process"
)

const (
	MaxMermaidSourceBytes     = 512 * 1024
	MaxMermaidSVGBytes        = 8 * 1024 * 1024
	MaxMermaidPNGBytes        = 32 * 1024 * 1024
	MaxMermaidErrorBytes      = 64 * 1024
	MaxMermaidDimension       = 4096
	MaxMermaidPixels          = 8_000_000
	MaxMermaidCacheItems      = 32
	MaxMermaidCacheBytes      = 32 * 1024 * 1024
	MaxMermaidExecutableBytes = 24 * 1024 * 1024
	MermaidRendererVersion    = "0.3.1"
)

var mermaidInstallMu sync.Mutex

var (
	svgOpenRE   = regexp.MustCompile(`(?i)<svg(\s|>)`)
	svgViewRE   = regexp.MustCompile(`(?i)\bviewBox\s*=\s*["']([^"']+)["']`)
	svgWidthRE  = regexp.MustCompile(`(?i)\bwidth\s*=\s*["']\s*([0-9.]+)`)
	svgHeightRE = regexp.MustCompile(`(?i)\bheight\s*=\s*["']\s*([0-9.]+)`)
)

type MermaidPlatform struct {
	Key            string `json:"key"`
	ExecutableName string `json:"executableName"`
}

func MermaidPlatformFor(osName, architecture string) (MermaidPlatform, error) {
	arch := ""
	switch strings.ToLower(architecture) {
	case "amd64", "x86_64", "x64":
		arch = "x64"
	case "aarch64", "arm64":
		arch = "arm64"
	default:
		return MermaidPlatform{}, fmt.Errorf("离线 Mermaid 暂不支持当前处理器架构：%s", architecture)
	}
	osName = strings.ToLower(osName)
	osKey := ""
	switch {
	case strings.Contains(osName, "windows"):
		osKey = "windows"
	case strings.Contains(osName, "mac"), strings.Contains(osName, "darwin"):
		osKey = "macos"
	case strings.Contains(osName, "linux"):
		osKey = "linux"
	default:
		return MermaidPlatform{}, fmt.Errorf("离线 Mermaid 暂不支持当前操作系统：%s", osName)
	}
	executable := "mmdr"
	if osKey == "windows" {
		executable += ".exe"
	}
	return MermaidPlatform{Key: osKey + "-" + arch, ExecutableName: executable}, nil
}

type DiagramSize struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

func (s DiagramSize) AspectRatio() float64 { return s.Width / s.Height }

type PixelSize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (s PixelSize) AspectRatio() float64 {
	if s.Height == 0 {
		return 0
	}
	return float64(s.Width) / float64(s.Height)
}

type MermaidResult struct {
	SVG         string      `json:"svg,omitempty"`
	PNG         []byte      `json:"png,omitempty"`
	PixelSize   PixelSize   `json:"pixelSize,omitempty"`
	DiagramSize DiagramSize `json:"diagramSize,omitempty"`
	Error       string      `json:"error,omitempty"`
}

func (r MermaidResult) Success() bool { return r.Error == "" && len(r.PNG) != 0 }

type MermaidDisplayState struct {
	Diagram *MermaidResult `json:"diagram,omitempty"`
	Error   string         `json:"error,omitempty"`
}

func (s MermaidDisplayState) Receive(result MermaidResult) MermaidDisplayState {
	if result.Success() {
		copy := cloneMermaid(result)
		return MermaidDisplayState{Diagram: &copy}
	}
	s.Error = result.Error
	return s
}

type mermaidCacheEntry struct {
	key    string
	result MermaidResult
	size   int
}

// MermaidRenderer invokes the pinned packaged mmdr binary. It renders to SVG,
// computes a bounded native-display PNG size, and asks mmdr for PNG directly;
// no browser or WebView is involved. The caller owns platform resource
// extraction and supplies the executable's local path.
type MermaidRenderer struct {
	Executable string
	Runner     *process.Runner
	slots      chan struct{}
	mu         sync.Mutex
	cache      map[string]*list.Element
	lru        list.List
	cacheBytes int
}

func NewMermaidRenderer(executable string, runner *process.Runner) *MermaidRenderer {
	if runner == nil {
		runner = process.NewRunner()
	}
	return &MermaidRenderer{Executable: executable, Runner: runner, slots: make(chan struct{}, 2), cache: make(map[string]*list.Element)}
}

func (r *MermaidRenderer) RenderDebounced(ctx context.Context, source string, darkTheme bool) MermaidResult {
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return r.Render(ctx, source, darkTheme)
	case <-ctx.Done():
		return MermaidResult{Error: "Mermaid 渲染已取消。"}
	}
}

func NativeMermaidPlatform() (MermaidPlatform, error) {
	return MermaidPlatformFor(runtime.GOOS, runtime.GOARCH)
}

// InstallMermaidExecutable copies a build-time verified embedded renderer into
// the user's versioned cache. The caller supplies the packaged platform
// resource; this function never downloads code or consults the network.
func InstallMermaidExecutable(resource io.Reader, cacheRoot string, platform MermaidPlatform) (string, error) {
	if resource == nil {
		return "", errors.New("缺少平台对应的离线 Mermaid 程序")
	}
	if strings.TrimSpace(cacheRoot) == "" {
		return "", errors.New("Mermaid 缓存目录未配置")
	}
	allowed, err := MermaidPlatformForRuntimeKey(platform.Key)
	if err != nil || allowed != platform {
		return "", errors.New("Mermaid 平台资源不合法")
	}
	targetDir := filepath.Join(cacheRoot, "cache", "native-mermaid", MermaidRendererVersion, platform.Key)
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(targetDir, platform.ExecutableName)
	mermaidInstallMu.Lock()
	defer mermaidInstallMu.Unlock()
	if info, err := os.Stat(target); err == nil && info.Mode().IsRegular() && info.Size() > 0 && info.Size() <= MaxMermaidExecutableBytes {
		return target, nil
	}
	data, err := io.ReadAll(io.LimitReader(resource, MaxMermaidExecutableBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) == 0 || len(data) > MaxMermaidExecutableBytes {
		return "", errors.New("离线 Mermaid 程序资源大小无效")
	}
	temporary, err := os.CreateTemp(targetDir, "mmdr-*.tmp")
	if err != nil {
		return "", err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if !strings.HasPrefix(platform.Key, "windows-") {
		if err := os.Chmod(temporaryName, 0o700); err != nil {
			return "", err
		}
	}
	if err := os.Rename(temporaryName, target); err != nil {
		return "", err
	}
	return target, nil
}

func MermaidPlatformForRuntimeKey(key string) (MermaidPlatform, error) {
	parts := strings.Split(key, "-")
	if len(parts) != 2 {
		return MermaidPlatform{}, errors.New("Mermaid 平台标识不合法")
	}
	osName := map[string]string{"windows": "Windows", "macos": "Mac OS X", "linux": "Linux"}[parts[0]]
	architecture := map[string]string{"x64": "amd64", "arm64": "aarch64"}[parts[1]]
	if osName == "" || architecture == "" {
		return MermaidPlatform{}, errors.New("Mermaid 平台标识不支持")
	}
	return MermaidPlatformFor(osName, architecture)
}

func (r *MermaidRenderer) Render(ctx context.Context, source string, darkTheme bool) MermaidResult {
	r.ensureInitialized()
	if strings.TrimSpace(source) == "" {
		return MermaidResult{Error: "Mermaid 源码为空。"}
	}
	sourceBytes := []byte(source)
	if len(sourceBytes) > MaxMermaidSourceBytes {
		return MermaidResult{Error: "Mermaid 源码超过 512 KB，已跳过渲染。"}
	}
	if strings.TrimSpace(r.Executable) == "" {
		return MermaidResult{Error: "缺少平台对应的离线 Mermaid 程序。请重新构建应用。"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	theme := "default"
	if darkTheme {
		theme = "dark"
	}
	key := mermaidCacheKey(sourceBytes, theme)
	if cached, ok := r.getCached(key); ok {
		return cached
	}
	select {
	case r.slots <- struct{}{}:
	case <-ctx.Done():
		return MermaidResult{Error: "Mermaid 渲染已取消。"}
	}
	defer func() { <-r.slots }()
	if cached, ok := r.getCached(key); ok {
		return cached
	}
	renderCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result := r.render(renderCtx, sourceBytes, theme)
	if !result.Success() {
		return result
	}
	if err := renderCtx.Err(); err != nil {
		return MermaidResult{Error: "Mermaid 渲染超过 5 秒，已停止本次渲染。"}
	}
	r.cacheResult(key, result)
	return cloneMermaid(result)
}

func (r *MermaidRenderer) ensureInitialized() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Runner == nil {
		r.Runner = process.NewRunner()
	}
	if r.slots == nil {
		r.slots = make(chan struct{}, 2)
	}
	if r.cache == nil {
		r.cache = make(map[string]*list.Element)
	}
}

func (r *MermaidRenderer) render(ctx context.Context, source []byte, theme string) MermaidResult {
	svgRun, err := r.Runner.Run(ctx, process.Spec{
		Executable: r.Executable, Arguments: []string{"--input", "-", "--outputFormat", "svg", "--theme", theme},
		Stdin: source, Timeout: 5 * time.Second, MaxStdoutBytes: MaxMermaidSVGBytes, MaxStderrBytes: MaxMermaidErrorBytes, KillOnOutputLimit: true,
	})
	if err != nil {
		return MermaidResult{Error: mermaidProcessError(err)}
	}
	if svgRun.StdoutTruncated {
		return MermaidResult{Error: "Mermaid SVG 输出超过 8 MB，已停止显示。"}
	}
	if svgRun.StderrTruncated {
		return MermaidResult{Error: "Mermaid 渲染器错误输出超过 64 KB，已停止本次渲染。"}
	}
	if svgRun.ExitCode != 0 {
		return MermaidResult{Error: mermaidExitMessage(svgRun.Stderr, svgRun.ExitCode)}
	}
	svg := strings.TrimLeft(svgRun.Stdout, "\uFEFF\u0000\r\n \t")
	if !svgOpenRE.MatchString(svg) {
		return MermaidResult{Error: "离线渲染器没有返回有效的 SVG 图表。"}
	}
	diagramSize := SVGSize(svg)
	pixelSize := OutputPixelSize(diagramSize)
	temporary, err := os.CreateTemp("", "silverwing-mermaid-*.png")
	if err != nil {
		return MermaidResult{Error: "无法创建 Mermaid 临时图像文件：" + err.Error()}
	}
	temporaryPath := temporary.Name()
	if err = temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return MermaidResult{Error: "无法创建 Mermaid 临时图像文件：" + err.Error()}
	}
	_ = os.Remove(temporaryPath)
	defer os.Remove(temporaryPath)
	pngRun, err := r.Runner.Run(ctx, process.Spec{
		Executable: r.Executable, Arguments: []string{"--input", "-", "--outputFormat", "png", "--output", temporaryPath,
			"--theme", theme, "--width", fmt.Sprint(pixelSize.Width), "--height", fmt.Sprint(pixelSize.Height)},
		Stdin: source, Timeout: 5 * time.Second, MaxStdoutBytes: MaxMermaidSVGBytes, MaxStderrBytes: MaxMermaidErrorBytes, KillOnOutputLimit: true,
	})
	if err != nil {
		return MermaidResult{Error: mermaidProcessError(err)}
	}
	if pngRun.StderrTruncated {
		return MermaidResult{Error: "Mermaid 渲染器错误输出超过 64 KB，已停止本次渲染。"}
	}
	if pngRun.ExitCode != 0 {
		return MermaidResult{Error: mermaidExitMessage(pngRun.Stderr, pngRun.ExitCode)}
	}
	png, err := readBounded(temporaryPath, MaxMermaidPNGBytes)
	if err != nil {
		return MermaidResult{Error: "Mermaid PNG 输出超过 32 MB 或文件未生成。"}
	}
	actualSize, ok := PNGSize(png)
	if !ok {
		return MermaidResult{Error: "离线渲染器没有返回有效的 PNG 图表。"}
	}
	return MermaidResult{SVG: svg, PNG: png, PixelSize: actualSize, DiagramSize: diagramSize}
}

func SVGSize(svg string) DiagramSize {
	if match := svgViewRE.FindStringSubmatch(svg); len(match) == 2 {
		fields := strings.FieldsFunc(match[1], func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\r' || r == '\n' })
		if len(fields) == 4 {
			width, wErr := parseFloat(fields[2])
			height, hErr := parseFloat(fields[3])
			if wErr == nil && hErr == nil && finitePositive(width) && finitePositive(height) {
				return DiagramSize{width, height}
			}
		}
	}
	width, wErr := svgDimension(svgWidthRE, svg)
	height, hErr := svgDimension(svgHeightRE, svg)
	if wErr == nil && hErr == nil && finitePositive(width) && finitePositive(height) {
		return DiagramSize{width, height}
	}
	return DiagramSize{16, 9}
}

func OutputPixelSize(diagram DiagramSize) PixelSize {
	width, height := diagram.Width, diagram.Height
	if !finitePositive(width) {
		width = 16
	}
	if !finitePositive(height) {
		height = 9
	}
	scale := math.Min(2, math.Min(float64(MaxMermaidDimension)/width,
		math.Min(float64(MaxMermaidDimension)/height, math.Sqrt(float64(MaxMermaidPixels)/(width*height)))))
	maxDimension := math.Max(width, height)
	if scale < 1/maxDimension {
		scale = 1 / maxDimension
	}
	pixelWidth := clamp(int(width*scale), 1, MaxMermaidDimension)
	pixelHeight := clamp(int(height*scale), 1, MaxMermaidDimension)
	for pixelWidth*pixelHeight > MaxMermaidPixels {
		if pixelWidth >= pixelHeight {
			pixelWidth--
		} else {
			pixelHeight--
		}
	}
	return PixelSize{pixelWidth, pixelHeight}
}

func PNGSize(data []byte) (PixelSize, bool) {
	if len(data) < 24 || !bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) || string(data[12:16]) != "IHDR" {
		return PixelSize{}, false
	}
	width, height := binary.BigEndian.Uint32(data[16:20]), binary.BigEndian.Uint32(data[20:24])
	if width == 0 || height == 0 || width > MaxMermaidDimension || height > MaxMermaidDimension || uint64(width)*uint64(height) > MaxMermaidPixels {
		return PixelSize{}, false
	}
	return PixelSize{int(width), int(height)}, true
}

func (r *MermaidRenderer) getCached(key string) (MermaidResult, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.cache[key]
	if entry == nil {
		return MermaidResult{}, false
	}
	r.lru.MoveToFront(entry)
	return cloneMermaid(entry.Value.(*mermaidCacheEntry).result), true
}

func (r *MermaidRenderer) cacheResult(key string, result MermaidResult) {
	bytes := len(result.SVG) + len(result.PNG) + result.PixelSize.Width*result.PixelSize.Height*4
	if bytes > MaxMermaidCacheBytes {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing := r.cache[key]; existing != nil {
		r.cacheBytes -= existing.Value.(*mermaidCacheEntry).size
		r.lru.Remove(existing)
	}
	entry := &mermaidCacheEntry{key: key, result: cloneMermaid(result), size: bytes}
	r.cache[key] = r.lru.PushFront(entry)
	r.cacheBytes += bytes
	for r.lru.Len() > MaxMermaidCacheItems || r.cacheBytes > MaxMermaidCacheBytes {
		oldest := r.lru.Back()
		if oldest == nil {
			break
		}
		value := oldest.Value.(*mermaidCacheEntry)
		delete(r.cache, value.key)
		r.cacheBytes -= value.size
		r.lru.Remove(oldest)
	}
}

func cloneMermaid(result MermaidResult) MermaidResult {
	result.PNG = append([]byte(nil), result.PNG...)
	return result
}

func mermaidCacheKey(source []byte, theme string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(theme))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(source)
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func mermaidProcessError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "Mermaid 渲染超过 5 秒，已停止本次渲染。"
	}
	if errors.Is(err, context.Canceled) {
		return "Mermaid 渲染已取消。"
	}
	if strings.Contains(strings.ToLower(err.Error()), "超限") || strings.Contains(err.Error(), "输出超过允许") {
		return "Mermaid 渲染输出超过大小限制，已停止本次渲染。"
	}
	if strings.TrimSpace(err.Error()) != "" {
		return err.Error()
	}
	return "无法启动离线 Mermaid 渲染器。"
}

func mermaidExitMessage(stderr string, exitCode int) string {
	detail := strings.TrimSpace(stderr)
	if detail != "" {
		if len(detail) > 500 {
			detail = detail[:500]
		}
		return detail
	}
	return fmt.Sprintf("Mermaid 语法有误（退出码 %d）。", exitCode)
}

func readBounded(path string, maximum int) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > int64(maximum) {
		return nil, errors.New("output file size invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	if err != nil || len(data) > maximum {
		return nil, errors.New("output file exceeds limit")
	}
	return data, nil
}

func svgDimension(pattern *regexp.Regexp, source string) (float64, error) {
	match := pattern.FindStringSubmatch(source)
	if len(match) < 2 {
		return 0, errors.New("missing dimension")
	}
	return parseFloat(match[1])
}

func parseFloat(value string) (float64, error) {
	var parsed float64
	_, err := fmt.Sscan(value, &parsed)
	return parsed, err
}

func finitePositive(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0
}
