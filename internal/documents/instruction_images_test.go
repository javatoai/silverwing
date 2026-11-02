package documents

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeInstructionImageFixture(t *testing.T, directory, name, content string) string {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func instructionClipboardPNG(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestInstructionImageImportsCopyKeepBothAndLeaveBodyUntouched(t *testing.T) {
	root, sources := t.TempDir(), t.TempDir()
	path := filepath.Join(root, ".workspace", "agent", "TASK-RULES.md")
	first := writeInstructionImageFixture(t, filepath.Join(sources, "a"), "中文 #100%.png", "first image")
	second := writeInstructionImageFixture(t, filepath.Join(sources, "b"), "中文 #100%.png", "second image")
	service := NewService("")
	result, err := service.ImportInstructionImages(context.Background(), root, path, ClipboardPayload{Files: []string{first, first, second}})
	if err != nil || len(result.Resources) != 2 {
		t.Fatalf("image batch failed: %+v %v", result, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("image import created the missing instruction body")
	}
	for index, want := range []string{"first image", "second image"} {
		resource := result.Resources[index]
		if filepath.Dir(filepath.FromSlash(resource.RelativePath)) != "TASK-RULES.assets" {
			t.Fatalf("wrong assets directory: %q", resource.RelativePath)
		}
		data, err := os.ReadFile(filepath.Join(filepath.Dir(path), filepath.FromSlash(resource.RelativePath)))
		if err != nil || string(data) != want {
			t.Fatalf("wrong copied image: %q %v", data, err)
		}
	}
	if result.Resources[0].RelativePath == result.Resources[1].RelativePath {
		t.Fatal("duplicate names overwrote the first file")
	}
	for _, source := range []string{first, second} {
		if _, err := os.Stat(source); err != nil {
			t.Fatalf("image source was moved: %v", err)
		}
	}
	body := "完整原文\r\n\r\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ImportInstructionImages(context.Background(), root, path, ClipboardPayload{Files: []string{first}}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != body {
		t.Fatal("image import changed the instruction body")
	}
}

func TestInstructionImagesUseCapturedPNGAndExistingPixelLimit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "AGENTS.md")
	payload := instructionClipboardPNG(t)
	result, err := NewService("").ImportInstructionImages(context.Background(), root, path, ClipboardPayload{ImagePNG: payload})
	if err != nil || len(result.Resources) != 1 {
		t.Fatalf("captured PNG import failed: %+v %v", result, err)
	}
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(result.Resources[0].RelativePath)))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatal("captured PNG bytes changed")
	}
	oversized := append([]byte(nil), payload...)
	binary.BigEndian.PutUint32(oversized[16:20], 8001)
	binary.BigEndian.PutUint32(oversized[20:24], 4000)
	binary.BigEndian.PutUint32(oversized[29:33], crc32.ChecksumIEEE(oversized[12:29]))
	for _, data := range [][]byte{[]byte("invalid PNG"), oversized} {
		result, err := NewService("").ImportInstructionImages(context.Background(), root, path, ClipboardPayload{ImagePNG: data})
		if err == nil || len(result.Resources) != 0 {
			t.Fatalf("invalid/oversized screenshot accepted: %+v %v", result, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "AGENTS.assets"))
	if err != nil || len(entries) != 1 {
		t.Fatal("failed screenshot left an imported image")
	}
}

func TestInstructionImagesRejectEscapesNonImagesAndDirectories(t *testing.T) {
	for _, scenario := range []string{"escape", "non-image", "directory", "body-directory", "assets-file"} {
		t.Run(scenario, func(t *testing.T) {
			root, sources := t.TempDir(), t.TempDir()
			path := filepath.Join(root, "AGENTS.md")
			source := writeInstructionImageFixture(t, sources, "image.png", "image")
			switch scenario {
			case "escape":
				path = filepath.Join(root, "..", "outside", "AGENTS.md")
			case "non-image":
				source = writeInstructionImageFixture(t, sources, "script.svg", "<svg/>")
			case "directory":
				source = filepath.Join(sources, "folder.png")
				if err := os.Mkdir(source, 0o700); err != nil {
					t.Fatal(err)
				}
			case "body-directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "assets-file":
				writeInstructionImageFixture(t, root, "AGENTS.assets", "existing file")
			}
			result, err := NewService("").ImportInstructionImages(context.Background(), root, path, ClipboardPayload{Files: []string{source}})
			if err == nil || len(result.Resources) != 0 {
				t.Fatalf("unsafe import accepted: %+v %v", result, err)
			}
			if _, err := os.Stat(filepath.Join(root, "AGENTS.assets")); !os.IsNotExist(err) && scenario != "assets-file" {
				t.Fatal("rejected import created an image directory")
			}
			if scenario == "assets-file" {
				if data, err := os.ReadFile(filepath.Join(root, "AGENTS.assets")); err != nil || string(data) != "existing file" {
					t.Fatal("import replaced an existing assets file")
				}
			}
		})
	}
}

func TestInstructionImagesRejectSymbolicRootsParentsSourcesAndAssets(t *testing.T) {
	for _, scenario := range []string{"root", "parent", "source", "assets", "body"} {
		t.Run(scenario, func(t *testing.T) {
			root, outside, sources := t.TempDir(), t.TempDir(), t.TempDir()
			path := filepath.Join(root, "nested", "AGENTS.md")
			source := writeInstructionImageFixture(t, sources, "image.png", "image")
			link, target := "", outside
			switch scenario {
			case "root":
				link, root = filepath.Join(root, "linked-root"), filepath.Join(root, "linked-root")
				path = filepath.Join(root, "AGENTS.md")
			case "parent":
				link = filepath.Dir(path)
			case "source":
				link, target = filepath.Join(sources, "linked.png"), source
				source = link
			case "assets":
				if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(filepath.Dir(path), "AGENTS.assets")
			case "body":
				if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				target = writeInstructionImageFixture(t, outside, "outside.md", "outside body")
				link = path
			}
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symbolic links unavailable: %v", err)
			}
			result, err := NewService("").ImportInstructionImages(context.Background(), root, path, ClipboardPayload{Files: []string{source}})
			if err == nil || len(result.Resources) != 0 {
				t.Fatalf("linked scope accepted: %+v %v", result, err)
			}
			entries, err := os.ReadDir(outside)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".assets") || entry.Name() == "image.png" {
					t.Fatal("image import escaped through a link")
				}
			}
		})
	}
}

type instructionImageCancellationContext struct {
	context.Context
	when string
	stop context.CancelFunc
}

func (c instructionImageCancellationContext) Err() error {
	if _, err := os.Stat(c.when); err == nil {
		c.stop()
	}
	return c.Context.Err()
}

func TestInstructionImageCancellationReturnsNoPartialReferences(t *testing.T) {
	root, sources := t.TempDir(), t.TempDir()
	path := writeInstructionImageFixture(t, root, "AGENTS.md", "body stays unchanged\r\n")
	first := writeInstructionImageFixture(t, sources, "one.png", "first")
	second := writeInstructionImageFixture(t, sources, "two.png", "second")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	context := instructionImageCancellationContext{Context: ctx, when: filepath.Join(root, "AGENTS.assets", "one.png"), stop: cancel}
	result, err := NewService("").ImportInstructionImages(context, root, path, ClipboardPayload{Files: []string{first, second}})
	if !errors.Is(err, ctx.Err()) || ctx.Err() == nil || len(result.Resources) != 0 {
		t.Fatalf("cancelled import returned partial references: %+v %v", result, err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "body stays unchanged\r\n" {
		t.Fatal("cancelled import changed the body")
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatal("cancelled import moved its source")
	}
	if _, err := os.Stat(filepath.Join(root, "AGENTS.assets", "two.png")); !os.IsNotExist(err) {
		t.Fatal("cancelled import continued copying images")
	}
}

type instructionImageFailureContext struct {
	context.Context
	check func()
}

func (c instructionImageFailureContext) Err() error {
	c.check()
	return c.Context.Err()
}

func TestInstructionImagePartialFailureReturnsNoReferences(t *testing.T) {
	root, sources := t.TempDir(), t.TempDir()
	path := writeInstructionImageFixture(t, root, "AGENTS.md", "original body")
	first := writeInstructionImageFixture(t, sources, "one.png", "first")
	second := writeInstructionImageFixture(t, sources, "two.png", "second")
	removed := false
	ctx := instructionImageFailureContext{Context: context.Background(), check: func() {
		if _, err := os.Stat(filepath.Join(root, "AGENTS.assets", "one.png")); err == nil && !removed {
			removed = true
			if err := os.Remove(second); err != nil {
				t.Fatal(err)
			}
		}
	}}
	result, err := NewService("").ImportInstructionImages(ctx, root, path, ClipboardPayload{Files: []string{first, second}})
	if !removed || err == nil || len(result.Resources) != 0 {
		t.Fatalf("partial failure returned insertable references: %+v %v", result, err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "original body" {
		t.Fatal("partial import changed the instruction body")
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatal("partial import moved its copied source")
	}
}
