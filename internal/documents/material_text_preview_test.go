package documents

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func utf16PreviewFixture(text string, order binary.ByteOrder) []byte {
	units := utf16.Encode([]rune(text))
	data := make([]byte, 2+len(units)*2)
	order.PutUint16(data, 0xfeff)
	for i, unit := range units {
		order.PutUint16(data[2+i*2:], unit)
	}
	return data
}

func TestMaterialsUnknownExtensionTextPreview(t *testing.T) {
	const source = "2026-10-15 14:30:00 INFO 中文日志\r\n\t保留缩进  \n# 不是 Markdown 标题\n<script>只显示原文</script>\r\n"
	for _, fixture := range []struct {
		name string
		data []byte
		want string
	}{
		{"ATS-人工补发测试.log", []byte(source), source},
		{"readme.unknown", []byte(source), source},
		{"extensionless", []byte(source), source},
		{"empty.log", nil, ""},
		{"utf8-bom.dat", append([]byte{0xef, 0xbb, 0xbf}, []byte(source)...), source},
		{"utf16-le.log", utf16PreviewFixture(source, binary.LittleEndian), source},
		{"utf16-be.blob", utf16PreviewFixture(source, binary.BigEndian), source},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, fixture.name)
			if err := os.WriteFile(path, fixture.data, 0600); err != nil {
				t.Fatal(err)
			}
			content, err := (&Service{}).ReadMaterialsPreview(root, fixture.name)
			if err != nil {
				t.Fatal(err)
			}
			if content.Kind != "text" || !content.ReadOnly || content.Text != fixture.want || content.Hash != Hash(fixture.data) {
				t.Fatalf("content = %#v", content)
			}
			if Kind(path) != "unsupported" {
				t.Fatal("content sniffing changed extension routing")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(fixture.data) {
				t.Fatal("read changed fixture bytes")
			}
			if _, err := (&Service{}).Save(root, fixture.name, "changed", content.Hash, true); err == nil {
				t.Fatal("detected text became writable")
			}
		})
	}
}

func TestMaterialsUnknownExtensionBinaryFallback(t *testing.T) {
	for name, data := range map[string][]byte{
		"fake.log":          {'a', 0, 'b'},
		"invalid-utf8.log":  {0xff, 0x80, 0x81},
		"binary.blob":       {'P', 'K', 3, 4, 'x'},
		"invalid-utf16.log": {0xff, 0xfe, 0, 0xd8},
		"odd-utf16.log":     {0xff, 0xfe, 0},
		"utf16-nul.blob":    utf16PreviewFixture("a\x00b", binary.BigEndian),
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
				t.Fatal(err)
			}
			content, err := (&Service{}).ReadMaterialsPreview(root, name)
			if err != nil {
				t.Fatal(err)
			}
			if content.Kind != "unsupported" || !content.ReadOnly || content.Text != "" {
				t.Fatalf("binary entered text preview: %#v", content)
			}
		})
	}
}

func TestMaterialsUnknownExtensionReadBoundaries(t *testing.T) {
	root := t.TempDir()
	service := &Service{}
	path := filepath.Join(root, "large.log")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(MaxMaterialsPreview + 1); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if _, err := service.ReadMaterialsPreview(root, path); !errors.Is(err, errMaterialsPreviewTooLarge) {
		t.Fatalf("size boundary = %v", err)
	}
	if _, err := service.ReadMaterialsPreview(root, "../outside.log"); err == nil {
		t.Fatal("path escape allowed")
	}
	if _, err := service.ReadMaterialsPreview(root, "missing.log"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read failure was hidden as binary: %v", err)
	}
	const markdown = "# 正常 Markdown\r\n\r\n- item\r\n"
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(markdown), 0600); err != nil {
		t.Fatal(err)
	}
	content, err := service.ReadMaterialsPreview(root, "README.md")
	if err != nil || content.Kind != "markdown" || content.Text != markdown {
		t.Fatalf("Markdown route changed: %#v / %v", content, err)
	}
	for _, name := range []string{"image.png", "document.pdf"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("literal text"), 0600); err != nil {
			t.Fatal(err)
		}
		content, err := service.ReadMaterialsPreview(root, name)
		if err != nil || content.Kind != Kind(name) || content.Text != "" {
			t.Fatalf("explicit media route changed: %#v / %v", content, err)
		}
	}
	if data, err := readTextPreview(strings.NewReader(strings.Repeat("x", 1025)), 1024); !errors.Is(err, errTextPreviewTooLarge) || data != nil {
		t.Fatalf("bounded read = %d / %v", len(data), err)
	}
}
