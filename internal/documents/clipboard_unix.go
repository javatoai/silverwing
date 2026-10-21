//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package documents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"silverwing-go/internal/executable"
	"strings"
)

func newPlatformClipboardBackend() clipboardBackend { return unixClipboardBackend{} }

type unixClipboardBackend struct{}

func (unixClipboardBackend) read(ctx context.Context) (ClipboardPayload, error) {
	if err := ctx.Err(); err != nil {
		return ClipboardPayload{}, err
	}
	if runtime.GOOS == "darwin" {
		return readMacClipboard(ctx)
	}
	if executable, err := exec.LookPath("wl-paste"); err == nil {
		return readWaylandClipboard(ctx, executable)
	}
	if executable, err := exec.LookPath("xclip"); err == nil {
		return readXClipboard(ctx, executable)
	}
	return ClipboardPayload{}, errors.New("未找到可用的系统剪贴板命令（需要 wl-paste/wl-copy 或 xclip）")
}

func (unixClipboardBackend) copyFiles(ctx context.Context, paths []string) error {
	if runtime.GOOS == "darwin" {
		return copyMacClipboardFiles(ctx, paths)
	}
	content := []string{"copy"}
	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		content = append(content, (&url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}).String())
	}
	data := []byte(strings.Join(content, "\n") + "\n")
	if executable, err := exec.LookPath("wl-copy"); err == nil {
		return runClipboardCommand(ctx, executable, []string{"--type", "x-special/gnome-copied-files"}, data)
	}
	if executable, err := exec.LookPath("xclip"); err == nil {
		return runClipboardCommand(ctx, executable, []string{"-selection", "clipboard", "-i", "-t", "x-special/gnome-copied-files"}, data)
	}
	return errors.New("未找到可用的系统剪贴板命令（需要 wl-copy 或 xclip）")
}

func readWaylandClipboard(ctx context.Context, executable string) (ClipboardPayload, error) {
	types, typeErr := commandClipboardOutput(ctx, executable, "--list-types")
	if typeErr != nil {
		return ClipboardPayload{}, typeErr
	}
	available := strings.Fields(string(types))
	if containsClipboardType(available, "x-special/gnome-copied-files") {
		data, err := commandClipboardOutput(ctx, executable, "--type", "x-special/gnome-copied-files")
		if err == nil {
			files := parseGNOMEFileList(data)
			if len(files) > 0 {
				return ClipboardPayload{Files: files}, nil
			}
		}
	}
	if containsClipboardType(available, "text/uri-list") {
		data, err := commandClipboardOutput(ctx, executable, "--type", "text/uri-list")
		if err == nil {
			files := parseFileURIList(data)
			if len(files) > 0 {
				return ClipboardPayload{Files: files}, nil
			}
		}
	}
	if containsClipboardType(available, "image/png") {
		data, err := commandClipboardOutput(ctx, executable, "--type", "image/png")
		if err == nil && len(data) > 0 {
			return ClipboardPayload{ImagePNG: data}, nil
		}
	}
	return ClipboardPayload{}, nil
}

func readXClipboard(ctx context.Context, executable string) (ClipboardPayload, error) {
	targets, err := commandClipboardOutput(ctx, executable, "-selection", "clipboard", "-t", "TARGETS", "-o")
	if err != nil {
		return ClipboardPayload{}, err
	}
	available := strings.Fields(string(targets))
	for _, mime := range []string{"x-special/gnome-copied-files", "text/uri-list"} {
		if !containsClipboardType(available, mime) {
			continue
		}
		data, readErr := commandClipboardOutput(ctx, executable, "-selection", "clipboard", "-t", mime, "-o")
		if readErr == nil {
			files := parseFileURIList(data)
			if mime == "x-special/gnome-copied-files" {
				files = parseGNOMEFileList(data)
			}
			if len(files) > 0 {
				return ClipboardPayload{Files: files}, nil
			}
		}
	}
	if containsClipboardType(available, "image/png") {
		data, readErr := commandClipboardOutput(ctx, executable, "-selection", "clipboard", "-t", "image/png", "-o")
		if readErr == nil && len(data) > 0 {
			return ClipboardPayload{ImagePNG: data}, nil
		}
	}
	return ClipboardPayload{}, nil
}

func readMacClipboard(ctx context.Context) (ClipboardPayload, error) {
	if executable, err := exec.LookPath(executable.SystemCommand("osascript")); err == nil {
		// Finder exposes copied files as file URLs to AppleScript. Text copied from
		// an editor will fail this coercion and proceeds to the image fallback.
		data, readErr := commandClipboardOutput(ctx, executable, "-e", "the clipboard as «class furl»")
		if readErr == nil {
			files := parseFileURIList(data)
			if len(files) > 0 {
				return ClipboardPayload{Files: files}, nil
			}
		}
	}
	if executable, err := exec.LookPath(executable.SystemCommand("pbpaste")); err == nil {
		data, readErr := commandClipboardOutput(ctx, executable, "-Prefer", "png")
		if readErr == nil && validPNG(data) {
			return ClipboardPayload{ImagePNG: data}, nil
		}
	}
	if data, err := readMacImagePNG(ctx); err == nil && len(data) > 0 {
		return ClipboardPayload{ImagePNG: data}, nil
	}
	return ClipboardPayload{}, nil
}

func copyMacClipboardFiles(ctx context.Context, paths []string) error {
	executable, err := exec.LookPath(executable.SystemCommand("osascript"))
	if err != nil {
		return errors.New("macOS 文件剪贴板需要 osascript")
	}
	arguments := []string{"-e", `on run argv
set fileList to {}
repeat with filePath in argv
set end of fileList to (POSIX file (contents of filePath))
end repeat
set the clipboard to fileList
end run`}
	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		arguments = append(arguments, absolute)
	}
	_, err = commandClipboardOutput(ctx, executable, arguments...)
	return err
}

func readMacImagePNG(ctx context.Context) ([]byte, error) {
	osascript, err := exec.LookPath(executable.SystemCommand("osascript"))
	if err != nil {
		return nil, err
	}
	sips, err := exec.LookPath(executable.SystemCommand("sips"))
	if err != nil {
		return nil, err
	}
	tiffFile, err := os.CreateTemp("", "silverwing-clipboard-*.tiff")
	if err != nil {
		return nil, err
	}
	tiffPath := tiffFile.Name()
	if err := tiffFile.Close(); err != nil {
		_ = os.Remove(tiffPath)
		return nil, err
	}
	defer os.Remove(tiffPath)
	pngFile, err := os.CreateTemp("", "silverwing-clipboard-*.png")
	if err != nil {
		return nil, err
	}
	pngPath := pngFile.Name()
	if err := pngFile.Close(); err != nil {
		_ = os.Remove(pngPath)
		return nil, err
	}
	if err := os.Remove(pngPath); err != nil {
		return nil, err
	}
	defer os.Remove(pngPath)
	script := `on run argv
set imageData to the clipboard as «class TIFF»
set imageFile to open for access (POSIX file (item 1 of argv)) with write permission
set eof imageFile to 0
write imageData to imageFile
close access imageFile
end run`
	if _, err := commandClipboardOutput(ctx, osascript, "-e", script, tiffPath); err != nil {
		return nil, err
	}
	if _, err := commandClipboardOutput(ctx, sips, "-s", "format", "png", tiffPath, "--out", pngPath); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(pngPath)
	if err != nil {
		return nil, err
	}
	if !validPNG(data) {
		return nil, errors.New("剪贴板图像无法转换为 PNG")
	}
	return data, nil
}

func validPNG(data []byte) bool {
	_, err := png.DecodeConfig(bytes.NewReader(data))
	return err == nil
}

func commandClipboardOutput(ctx context.Context, executable string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, executable, arguments...)
	output, err := command.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("剪贴板命令失败：%s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, err
	}
	return output, nil
}

func runClipboardCommand(ctx context.Context, executable string, arguments []string, data []byte) error {
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Stdin = bytes.NewReader(data)
	output, err := command.CombinedOutput()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if message := strings.TrimSpace(string(output)); message != "" {
		return fmt.Errorf("剪贴板写入失败：%s", message)
	}
	return fmt.Errorf("剪贴板写入失败：%w", err)
}

func parseGNOMEFileList(data []byte) []string {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) > 0 && (lines[0] == "copy" || lines[0] == "cut") {
		lines = lines[1:]
	}
	return parseFileURILines(lines)
}

func parseFileURIList(data []byte) []string {
	return parseFileURILines(strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n"))
}

func parseFileURILines(lines []string) []string {
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parsed, err := url.Parse(line)
		if err != nil || !strings.EqualFold(parsed.Scheme, "file") || (parsed.Host != "" && !strings.EqualFold(parsed.Host, "localhost")) {
			continue
		}
		path := parsed.Path // net/url has already decoded URL escapes here.
		if filepath.IsAbs(filepath.FromSlash(path)) {
			files = append(files, filepath.FromSlash(path))
		}
	}
	return files
}

func containsClipboardType(types []string, target string) bool {
	for _, value := range types {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}
