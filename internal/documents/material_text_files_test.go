package documents

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterialTextSourceRoundTripAndConflict(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"notes.txt", "query.sql", "main.go", "main.rs", "data.json", "data.csv", "fix.patch"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, name)
			original := []byte("\xef\xbb\xbf中文\r\nsecond\nthird\rfourth  \r\n")
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			service := NewService(filepath.Join(root, "cache"))
			snapshot, err := service.MaterialTextSnapshot(root, path)
			if err != nil || !snapshot.HasUTF8BOM || snapshot.Newline != "\r\n" || snapshot.Text != string(original[3:]) {
				t.Fatalf("snapshot=%#v error=%v", snapshot, err)
			}
			unchanged, err := service.SaveMaterialText(ctx, root, path, snapshot.Text, snapshot.Hash, snapshot.HasUTF8BOM)
			if err != nil || unchanged.Hash != snapshot.Hash {
				t.Fatalf("unchanged save: %#v %v", unchanged, err)
			}
			source := strings.Replace(snapshot.Text, "second", "第二行", 1)
			saved, err := service.SaveMaterialText(ctx, root, path, source, snapshot.Hash, snapshot.HasUTF8BOM)
			bytes, _ := os.ReadFile(path)
			if err != nil || string(bytes) != "\xef\xbb\xbf"+source || saved.Hash != Hash(bytes) {
				t.Fatalf("source changed: %q %v", bytes, err)
			}
			external := []byte("external\r\n")
			if err := os.WriteFile(path, external, 0600); err != nil {
				t.Fatal(err)
			}
			current, err := service.SaveMaterialText(ctx, root, path, source, saved.Hash, true)
			if !errors.Is(err, ErrConflict) || current.Hash != Hash(external) {
				t.Fatalf("conflict: %#v %v", current, err)
			}
			bytes, _ = os.ReadFile(path)
			if string(bytes) != string(external) {
				t.Fatal("external content overwritten")
			}
			if _, err := service.SaveMaterialText(ctx, root, path, source, "", true); !errors.Is(err, ErrConflict) {
				t.Fatal("missing baseline allowed")
			}
		})
	}
	for source, want := range map[string]string{"a\rb\nc": "\r", "a\nb\r\nc": "\n", "a\r\nb\nc": "\r\n", "no newline": "\n"} {
		snapshot, err := materialTextSnapshotFromBytes("x.txt", nil, []byte(source))
		if err != nil || snapshot.Newline != want {
			t.Fatalf("newline %q = %q, %v", source, snapshot.Newline, err)
		}
	}
}

func TestMaterialTextThreeMiBBoundaryIncludesBOM(t *testing.T) {
	ctx, root := context.Background(), t.TempDir()
	service := NewService(filepath.Join(root, "cache"))
	path := filepath.Join(root, "large.txt")
	source := strings.Repeat("x", MaxMaterialsText-3)
	data := materialTextBytes(source, true)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.MaterialTextSnapshot(root, path)
	if err != nil || snapshot.Size != MaxMaterialsText {
		t.Fatalf("exact limit: %#v %v", snapshot, err)
	}
	if _, err := service.ReadMaterialsPreview(root, path); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Read(root, path); !errors.Is(err, errTextPreviewTooLarge) {
		t.Fatalf("tool 2MiB limit changed: %v", err)
	}
	if _, err := service.SaveMaterialText(ctx, root, path, source+"x", snapshot.Hash, true); !errors.Is(err, errMaterialsPreviewTooLarge) {
		t.Fatalf("BOM overflow: %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(data) {
		t.Fatal("oversize save changed file")
	}
	identity, err := NewMarkdownDraftIdentity("t", root, root, path)
	if err != nil {
		t.Fatal(err)
	}
	draft := MaterialTextDraft{Identity: identity, Content: source, BaselineHash: snapshot.Hash, HasUTF8BOM: true}
	if err := service.WriteMaterialTextDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	draft.Content += "x"
	if err := service.WriteMaterialTextDraft(ctx, draft); err == nil {
		t.Fatal("draft exceeds final encoded byte limit")
	}
	if err := os.WriteFile(path, append(data, 'x'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.MaterialTextSnapshot(root, path); !errors.Is(err, errMaterialsPreviewTooLarge) {
		t.Fatalf("read overflow: %v", err)
	}
	if _, err := service.ReadMaterialsPreview(root, path); !errors.Is(err, errMaterialsPreviewTooLarge) {
		t.Fatalf("preview overflow: %v", err)
	}
}

func TestMaterialTextReadonlyEncodingAndPathBoundaries(t *testing.T) {
	root := t.TempDir()
	service := NewService(filepath.Join(root, "cache"))
	for _, fixture := range []struct {
		name string
		data []byte
	}{
		{"utf16.txt", utf16PreviewFixture("只读\r\n", binary.LittleEndian)},
		{"unknown.log", []byte("unknown text\n")},
	} {
		path := filepath.Join(root, fixture.name)
		if err := os.WriteFile(path, fixture.data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := service.MaterialTextSnapshot(root, path); err == nil {
			t.Fatalf("%s became writable", fixture.name)
		}
		preview, err := service.ReadMaterialsPreview(root, path)
		if err != nil || !preview.ReadOnly || preview.Text == "" {
			t.Fatalf("readonly preview lost: %#v %v", preview, err)
		}
		if _, err := service.Save(root, path, "new", Hash(fixture.data), true); err == nil {
			t.Fatalf("%s saved", fixture.name)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "bad.txt"), []byte{0xff, 0x80}, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"bad.txt", "../outside.txt", ".git/config.txt", "missing.txt", root} {
		if _, err := service.MaterialTextSnapshot(root, path); err == nil {
			t.Fatalf("unsafe source %s accepted", path)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Logf("symlink permission unavailable: %v", err)
		return
	}
	if _, err := service.MaterialTextSnapshot(root, link); err == nil {
		t.Fatal("symlink opened for editing")
	}
}

func TestMaterialTextDraftJournalIdentityRenameAndDeletion(t *testing.T) {
	ctx, root := context.Background(), t.TempDir()
	service := NewService(filepath.Join(root, "cache"))
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("disk"), 0600); err != nil {
		t.Fatal(err)
	}
	identity, _ := NewMarkdownDraftIdentity("t", filepath.Join(root, "task"), root, path)
	draft := MaterialTextDraft{Identity: identity, Content: "draft\r\n原文", BaselineHash: Hash([]byte("disk")), HasUTF8BOM: true, Newline: "\r\n"}
	if err := service.WriteMaterialTextDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "cache", "materials-text-drafts.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "cache", "materials-markdown-drafts.json")); !os.IsNotExist(err) {
		t.Fatal("text draft used markdown cache")
	}
	draft.Content += " newer"
	if err := service.WriteMaterialTextDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	nextHash := Hash([]byte("first saved"))
	if err := service.CompleteMaterialTextDraftSave(ctx, identity, "draft\r\n原文", draft.BaselineHash, nextHash); err != nil {
		t.Fatal(err)
	}
	restored, err := service.ReadMaterialTextDraft(ctx, identity)
	if err != nil || restored.Content != draft.Content || restored.BaselineHash != nextHash || !restored.HasUTF8BOM {
		t.Fatalf("newer draft lost: %#v %v", restored, err)
	}
	if err := service.MoveMaterialTextDrafts(ctx, identity, "a.txt", "renamed.txt"); err != nil {
		t.Fatal(err)
	}
	next, _ := NewMarkdownDraftIdentity(identity.TaskID, identity.TaskPath, root, "renamed.txt")
	if old, _ := service.ReadMaterialTextDraft(ctx, identity); old != nil {
		t.Fatal("rename retained old identity")
	}
	if moved, err := service.ReadMaterialTextDraft(ctx, next); err != nil || moved == nil || moved.Content != draft.Content {
		t.Fatalf("renamed draft lost: %#v %v", moved, err)
	}
	if err := service.RemoveMaterialTextDrafts(ctx, next, []string{filepath.Join(root, "renamed.txt")}); err != nil {
		t.Fatal(err)
	}
	if removed, _ := service.ReadMaterialTextDraft(ctx, next); removed != nil {
		t.Fatal("deleted identity retained")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := service.WriteMaterialTextDraft(ctx, draft); err == nil {
		t.Fatal("late draft recreated a deleted file identity")
	}
}

func TestMaterialTextMarkdownSavePreservesUntouchedMixedBytes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "notes.md")
	original := "\xef\xbb\xbf---\r\nmeta: raw\n---\r\n\r\n# 标题\n\r\n<!-- untouched -->\r尾行  \r\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	service := NewService(filepath.Join(root, "cache"))
	snapshot, err := service.MarkdownSnapshot(root, path)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(snapshot.Text, "# 标题", "# 已修改标题", 1)
	if _, err := service.SaveMarkdown(context.Background(), root, path, changed, snapshot.Hash); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "\xef\xbb\xbf"+changed {
		t.Fatalf("untouched bytes changed: %q", data)
	}
}

func TestMaterialTextAllDraftCacheDeletion(t *testing.T) {
	ctx, root := context.Background(), t.TempDir()
	service := NewService(filepath.Join(root, "cache"))
	textPath, mdPath := filepath.Join(root, "note.txt"), filepath.Join(root, "note.md")
	for _, path := range []string{textPath, mdPath} {
		if err := os.WriteFile(path, []byte("disk"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	textID, _ := NewMarkdownDraftIdentity("task", root, root, textPath)
	mdID, _ := NewMarkdownDraftIdentity("task", root, root, mdPath)
	if err := service.WriteMaterialTextDraft(ctx, MaterialTextDraft{Identity: textID, Content: "text draft", BaselineHash: Hash([]byte("disk"))}); err != nil {
		t.Fatal(err)
	}
	if err := service.WriteMarkdownDraft(ctx, MarkdownDraft{Identity: mdID, Content: "# markdown draft", BaselineHash: Hash([]byte("disk"))}); err != nil {
		t.Fatal(err)
	}
	if err := service.RemoveMaterialTextDrafts(ctx, textID, []string{root}); err != nil {
		t.Fatal(err)
	}
	if err := service.RemoveMarkdownDrafts(ctx, mdID, []string{root}); err != nil {
		t.Fatal(err)
	}
	if draft, _ := service.ReadMaterialTextDraft(ctx, textID); draft != nil {
		t.Fatal("text cache retained deleted subtree")
	}
	if draft, _ := service.ReadMarkdownDraft(ctx, mdID); draft != nil {
		t.Fatal("Markdown cache retained deleted subtree")
	}
}
