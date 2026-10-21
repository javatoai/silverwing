package documents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTaskRulesRawMissingAndEmpty(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".workspace", "agent", "TASK-RULES.md")
	service := NewService(filepath.Join(root, "cache"))
	missing, err := service.ReadTaskRules(root, path)
	if err != nil || missing.Exists || missing.Text != "" || missing.Hash != "" {
		t.Fatalf("missing file is not a normal empty state: %+v %v", missing, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".workspace")); !os.IsNotExist(err) {
		t.Fatal("opening a missing note created directories")
	}
	saved, err := service.SaveTaskRules(context.Background(), root, path, "", "", false)
	if err != nil || !saved.Exists || saved.Hash != Hash(nil) {
		t.Fatalf("explicit empty save failed: %+v %v", saved, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) != 0 {
		t.Fatal("explicit empty save did not create an empty file")
	}
	read, err := service.ReadTaskRules(root, path)
	if err != nil || !read.Exists || read.Hash == missing.Hash || read.Text != "" {
		t.Fatal("zero-byte and missing snapshots were conflated")
	}
}

func TestTaskRulesRawPreservesUnicodeAndLineEndings(t *testing.T) {
	for _, bom := range []bool{false, true} {
		root := t.TempDir()
		path := filepath.Join(root, "TASK-RULES.md")
		service := NewService("")
		source := "# 中文需求说明\r\nemoji 😀\n\r\n最后一行保留空格  \r\n\r\n"
		bytes := []byte(source)
		if bom {
			bytes = append([]byte{0xef, 0xbb, 0xbf}, bytes...)
		}
		if err := os.WriteFile(path, bytes, 0o600); err != nil {
			t.Fatal(err)
		}
		read, err := service.ReadTaskRules(root, path)
		if err != nil || read.Text != source || read.HasUTF8BOM != bom {
			t.Fatalf("read normalized the source: %q %v", read.Text, err)
		}
		next := source + "继续填写\n\r\n"
		saved, err := service.SaveTaskRules(context.Background(), root, path, next, read.Hash, true)
		if err != nil || saved.Text != next {
			t.Fatalf("save normalized the source: %q %v", saved.Text, err)
		}
		want := []byte(next)
		if bom {
			want = append([]byte{0xef, 0xbb, 0xbf}, want...)
		}
		got, _ := os.ReadFile(path)
		if string(got) != string(want) {
			t.Fatalf("file bytes differ: %q", got)
		}
		if !bom {
			if _, err := service.SaveTaskRules(context.Background(), root, path, "", saved.Hash, true); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(path)
			if len(data) != 0 {
				t.Fatal("clearing an existing file did not save zero bytes")
			}
		}
	}
}

func TestTaskRulesRawConflictsAndReadErrors(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "TASK-RULES.md")
	service := NewService("")
	if err := os.WriteFile(path, []byte("原文\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := service.ReadTaskRules(root, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("外部修改"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveTaskRules(context.Background(), root, path, "我的草稿", before.Hash, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("external edit was overwritten: %v", err)
	}
	if _, err := service.SaveTaskRules(context.Background(), root, path, "缺失时的草稿", "", false); !errors.Is(err, ErrConflict) {
		t.Fatalf("external creation was overwritten: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "外部修改" {
		t.Fatal("conflict changed external content")
	}
	bad := filepath.Join(root, "invalid.md")
	_ = os.WriteFile(bad, []byte{0xff}, 0o600)
	if _, err := service.ReadTaskRules(root, bad); err == nil {
		t.Fatal("invalid UTF-8 was treated as empty")
	}
	directory := filepath.Join(root, "directory.md")
	_ = os.Mkdir(directory, 0o700)
	if _, err := service.ReadTaskRules(root, directory); err == nil {
		t.Fatal("read failure was treated as an empty file")
	}
	if _, err := service.SaveTaskRules(context.Background(), root, directory, "覆盖", "", false); err == nil {
		t.Fatal("read failure was overwritten")
	}
	if _, err := service.ReadTaskRules(root, filepath.Join(root, "..", "escape.md")); err == nil {
		t.Fatal("escaped task note path accepted")
	}
}
