package documents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func taskFilesWrite(t *testing.T, root, path string, data []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
func taskFilesScope(t *testing.T, root string) *TaskFilesScope {
	t.Helper()
	scope, err := OpenTaskFilesScope(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(scope.Close)
	return scope
}

type taskFilesCancelAfter struct {
	context.Context
	remaining int
}

func (c *taskFilesCancelAfter) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func TestTaskFilesSearchCanceledDuringWalk(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 60; i++ {
		taskFilesWrite(t, root, fmt.Sprintf("nested/file-%03d.txt", i), []byte("synthetic"))
	}
	scope := taskFilesScope(t, root)
	ctx := &taskFilesCancelAfter{Context: context.Background(), remaining: 20}
	result, err := scope.Search(ctx, "file", 500)
	if !errors.Is(err, context.Canceled) || len(result.Entries) != 0 {
		t.Fatal("cancellation during traversal published partial results", result, err)
	}
}

func TestTaskFilesLazyMetadataIgnoredAndRestrictedGit(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"AGENTS.md", "silverwing.json", ".hidden", ".gitignore", ".workspace/agent/TASK-RULES.md", "ignored/needle.txt", "build/needle.out", ".git/objects/needle"} {
		taskFilesWrite(t, root, name, []byte("synthetic\n"))
	}
	scope := taskFilesScope(t, root)
	result, err := scope.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]TaskFileEntry{}
	filesStarted := false
	for index, entry := range result.Entries {
		if index > 0 && result.Entries[index-1].Directory == entry.Directory && strings.ToLower(result.Entries[index-1].Name) > strings.ToLower(entry.Name) {
			t.Fatal("names were not sorted case-insensitively", result.Entries)
		}
		seen[entry.Name] = entry
		if entry.Directory && filesStarted {
			t.Fatal("folders were not sorted first")
		}
		filesStarted = filesStarted || !entry.Directory
		if strings.Contains(entry.Path, "/") {
			t.Fatal("initial list scanned descendants", entry)
		}
	}
	for _, name := range []string{".workspace", ".hidden", ".gitignore", "AGENTS.md", "silverwing.json", "ignored", "build"} {
		if _, ok := seen[name]; !ok {
			t.Fatal("task metadata/ignored/hidden file filtered", name)
		}
	}
	if git := seen[".git"]; git.RestrictedReason != "" || git.Kind != "external" || git.Directory {
		t.Fatal("Git metadata was not an opaque system-open leaf", git)
	}
	for _, path := range []string{".git", ".git/objects", ".GIT/objects", ".git./objects"} {
		if _, err := scope.List(context.Background(), path); err == nil {
			t.Fatal("Git metadata traversed", path)
		}
		if _, err := scope.Read(context.Background(), path); err == nil {
			t.Fatal("Git metadata read", path)
		}
	}
	search, err := scope.Search(context.Background(), "NEEDLE", 500)
	if err != nil || len(search.Entries) != 2 {
		t.Fatal("ignored/build paths lost or Git searched", search, err)
	}
	workspace, err := scope.List(context.Background(), ".workspace/agent")
	if err != nil || len(workspace.Entries) != 1 || workspace.Entries[0].Path != ".workspace/agent/TASK-RULES.md" {
		t.Fatal(workspace, err)
	}
}

func TestTaskFilesReadonlyFormatsLimitsAndIdentity(t *testing.T) {
	root := t.TempDir()
	scope := taskFilesScope(t, root)
	for path, data := range map[string][]byte{"note.md": []byte("\xef\xbb\xbf# 文本\r\n尾部  \r\n"), "code.go": []byte("package example\n"), "data.json": []byte("{\"value\":1}\n"), "data.csv": []byte("a,b\n1,2\n"), "plain.unknown": []byte("plain\n"), "utf16.txt": {0xff, 0xfe, 'A', 0, '\n', 0}, "image.png": {137, 80, 78, 71}, "document.pdf": []byte("%PDF-synthetic"), "office.docx": {0, 255, 0, 1}, "large.txt": []byte(strings.Repeat("x", MaxMaterialsPreview+1))} {
		taskFilesWrite(t, root, path, data)
	}
	for _, path := range []string{"note.md", "code.go", "data.json", "data.csv", "plain.unknown", "utf16.txt", "image.png", "document.pdf", "office.docx", "large.txt"} {
		result, err := scope.Read(context.Background(), path)
		if err != nil {
			t.Fatal(path, err)
		}
		content := result.Content
		if !content.ReadOnly || content.Draft != nil || content.TextDraft != nil || content.MarkdownEditContext != "" || result.Identity == "" {
			t.Fatal("read acquired editor/draft or omitted identity", path, result)
		}
		if path == "note.md" && (content.Text != "# 文本\r\n尾部  \r\n" || !content.HasUTF8BOM || content.Hash == "" || content.Newline != "\r\n") {
			t.Fatal("source fidelity lost", result)
		}
		if path == "utf16.txt" && content.Text != "A\n" {
			t.Fatal("bounded UTF16 preview changed", result)
		}
		if path == "data.csv" && len(content.Rows) != 2 {
			t.Fatal("CSV readonly preview missing", result)
		}
		if (path == "large.txt" || path == "office.docx" || path == "plain.unknown") && result.FallbackReason == "" {
			t.Fatal("oversize/unsupported lacked explicit fallback", result)
		}
		if (path == "image.png" || path == "document.pdf") && (content.Text != "" || content.Size == 0) {
			t.Fatal("media read should return safe file information", result)
		}
	}
	first, err := scope.Read(context.Background(), "image.png")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "image.png"), filepath.Join(root, "old.png")); err != nil {
		t.Fatal(err)
	}
	taskFilesWrite(t, root, "image.png", []byte{137, 80, 78, 71})
	second, err := scope.Read(context.Background(), "image.png")
	if err != nil || first.Identity == second.Identity {
		t.Fatal("file replacement has same native identity", first, second, err)
	}
}

func TestTaskFilesGitMetadataSystemOpenLeafOnly(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(fmt.Sprintf("directory-%t", directory), func(t *testing.T) {
			root := t.TempDir()
			if directory {
				taskFilesWrite(t, root, "repo/.git/secret-needle", []byte("private metadata"))
			} else {
				taskFilesWrite(t, root, "repo/.git", []byte("gitdir: outside-pointer-must-not-be-read"))
			}
			scope := taskFilesScope(t, root)
			listing, err := scope.List(context.Background(), "repo")
			if err != nil || len(listing.Entries) != 1 {
				t.Fatal(listing, err)
			}
			entry := listing.Entries[0]
			if entry.Kind != "external" || entry.Directory || entry.RestrictedReason != "" {
				t.Fatal("Git entry is not an opaque external-open leaf", entry)
			}
			if _, err := scope.Read(context.Background(), "repo/.git"); err == nil {
				t.Fatal("Git metadata acquired a content reader")
			}
			result, err := scope.Search(context.Background(), "secret-needle", 500)
			if err != nil || len(result.Entries) != 0 || len(result.Issues) != 0 {
				t.Fatal("search traversed Git metadata", result, err)
			}
			calls := 0
			launch := func(p string, isDir bool) error {
				calls++
				if p != filepath.Join(root, "repo", ".git") || isDir != directory {
					t.Fatal("system launcher received a different target/type", p, isDir)
				}
				return nil
			}
			if err := scope.WithPath(context.Background(), "repo/.git", launch); err != nil || calls != 1 {
				t.Fatal("explicit Git system open was rejected", err, calls)
			}
			for _, invalid := range []string{"repo/.git/secret-needle", "repo/.git/../file", "repo/.git./secret", "../.git"} {
				if err := scope.WithPath(context.Background(), invalid, launch); err == nil || calls != 1 {
					t.Fatal("system-open exception escaped the Git leaf", invalid, err, calls)
				}
			}
		})
	}
}

func TestTaskFilesAdditionalTextFormatsAreBoundedReadonly(t *testing.T) {
	root := t.TempDir()
	scope := taskFilesScope(t, root)
	cases := []struct {
		name string
		data []byte
		text string
	}{
		{"events.jsonl", []byte("{\"message\":\"中文\"}\n{\"ok\":true}\n"), "{\"message\":\"中文\"}\n{\"ok\":true}\n"},
		{"module.IML", []byte("\xef\xbb\xbf<module name=\"中文\"/>\r\n"), "<module name=\"中文\"/>\r\n"},
		{"server.log", []byte("INFO 中文\r\nERROR next\n"), "INFO 中文\r\nERROR next\n"},
		{"utf16.LOG", []byte{0xff, 0xfe, 'O', 0, 'K', 0, '\n', 0}, "OK\n"},
		{"binary.log", []byte{0, 1, 0, 2, 0xff}, ""},
		{"large.jsonl", []byte(strings.Repeat("x", MaxMaterialsPreview+1)), ""},
		{"certificate.p12", []byte("even-text-with-a-p12-extension-stays-external"), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			taskFilesWrite(t, root, c.name, c.data)
			result, err := scope.Read(context.Background(), c.name)
			if err != nil || !result.Content.ReadOnly || result.Content.Draft != nil || result.Content.TextDraft != nil {
				t.Fatal("new format failed or acquired editing state", result, err)
			}
			if c.text != "" {
				if result.Content.Kind != "text" || result.Content.Text != c.text || result.FallbackReason != "" {
					t.Fatal("new text format did not retain decoded source", result)
				}
			} else if result.FallbackReason == "" || result.Content.Text != "" {
				t.Fatal("binary/oversized/certificate acquired plaintext preview", result)
			}
			after, err := os.ReadFile(filepath.Join(root, c.name))
			if err != nil || !bytes.Equal(after, c.data) {
				t.Fatal("preview changed the original file", err)
			}
		})
	}
	for _, extension := range []string{"jsonl", "iml", "log"} {
		if Kind("sample."+extension) != "unsupported" {
			t.Fatal("task browser changed the shared materials type policy", extension)
		}
	}
}

func TestTaskFilesUnsupportedFormatsKeepSystemOpenFallback(t *testing.T) {
	root := t.TempDir()
	scope := taskFilesScope(t, root)
	for _, name := range []string{"office.docx", "legacy.DOC", "sheet.xlsx", "slides.pptx", "plain.unknown", ".hidden-note"} {
		t.Run(name, func(t *testing.T) {
			// Plain text is deliberate: rejection must follow the format contract,
			// rather than depending on a valid Office ZIP/binary failing decoding.
			original := []byte("synthetic unsupported Office fixture; never launch\n")
			taskFilesWrite(t, root, name, original)
			result, err := scope.Read(context.Background(), name)
			if err != nil {
				t.Fatal(err)
			}
			c := result.Content
			if result.FallbackReason == "" || c.Kind != "unsupported" || c.Text != "" || c.Hash != "" || len(c.Rows) != 0 || len(result.MediaBytes) != 0 {
				t.Fatal("unsupported file acquired an internal content preview", result)
			}
			if !c.ReadOnly || c.Draft != nil || c.TextDraft != nil || c.MarkdownEditContext != "" || c.Size != int64(len(original)) || result.Identity == "" {
				t.Fatal("fallback changed readonly metadata or acquired editing state", result)
			}
			after, err := os.ReadFile(filepath.Join(root, name))
			if err != nil || !bytes.Equal(after, original) {
				t.Fatal("fallback modified the source file", err)
			}
		})
	}
}

func TestTaskFilesSearchBoundedLiteralAndCancellation(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 520; i++ {
		taskFilesWrite(t, root, fmt.Sprintf("level/part/file-%03d[abc].txt", i), []byte("not content search"))
	}
	scope := taskFilesScope(t, root)
	result, err := scope.Search(context.Background(), "[AbC]", 500)
	if err != nil || len(result.Entries) != 500 || !result.Truncated {
		t.Fatal(result, err)
	}
	result, err = scope.Search(context.Background(), "not content search", 500)
	if err != nil || len(result.Entries) != 0 {
		t.Fatal("searched file contents", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := scope.List(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := scope.Read(ctx, "level/part/file-001[abc].txt"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := scope.Search(ctx, "file", 500); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, path := range []string{"../escape", "level/../../escape", "/absolute", "C:/absolute", "level//part", "level/./part", "level/part/../x", "file:stream"} {
		if _, err := scope.List(context.Background(), path); err == nil {
			t.Fatal("range escape accepted", path)
		}
	}
}

func TestTaskFilesRejectsRedirectsAndAllowsConfiguredRootAlias(t *testing.T) {
	container := t.TempDir()
	physical := filepath.Join(container, "physical")
	task := filepath.Join(physical, "task")
	taskFilesWrite(t, task, "safe.txt", []byte("inside"))
	outside := t.TempDir()
	taskFilesWrite(t, outside, "secret.txt", []byte("outside"))
	alias := filepath.Join(container, "alias")
	if err := os.Symlink(physical, alias); err != nil {
		t.Skip("directory symlink privilege unavailable:", err)
	}
	scope := taskFilesScope(t, filepath.Join(alias, "task"))
	if _, err := scope.Read(context.Background(), "safe.txt"); err != nil {
		t.Fatal("configured ancestor alias rejected", err)
	}
	if err := os.Symlink(outside, filepath.Join(task, "linked")); err != nil {
		t.Fatal(err)
	}
	list, err := scope.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range list.Entries {
		if entry.Name == "linked" {
			found = entry.RestrictedReason != ""
		}
	}
	if !found {
		t.Fatal("link was hidden instead of restricted")
	}
	if _, err := scope.List(context.Background(), "linked"); err == nil {
		t.Fatal("linked directory followed")
	}
	if _, err := scope.Read(context.Background(), "linked/secret.txt"); err == nil {
		t.Fatal("linked file read")
	}
	result, err := scope.Search(context.Background(), "secret", 500)
	if err != nil || len(result.Entries) != 0 {
		t.Fatal("search traversed redirect", result, err)
	}
	called := false
	if err := scope.WithPath(context.Background(), "linked/secret.txt", func(string, bool) error { called = true; return nil }); err == nil || called {
		t.Fatal("unsafe system path reached launcher", err)
	}
}

func TestTaskFilesRootAndDirectoryReplacementInvalidateScope(t *testing.T) {
	container := t.TempDir()
	root := filepath.Join(container, "task")
	taskFilesWrite(t, root, "child/safe.txt", []byte("before"))
	taskFilesWrite(t, root, "root.txt", []byte("original root"))
	scope := taskFilesScope(t, root)
	d, err := scope.directory(context.Background(), "child")
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	if err := os.Rename(filepath.Join(root, "child"), filepath.Join(root, "old-child")); err != nil {
		t.Fatal(err)
	}
	taskFilesWrite(t, root, "child/safe.txt", []byte("replacement"))
	if err := d.check(); err == nil {
		t.Fatal("replaced child identity accepted")
	}
	renameErr := os.Rename(root, filepath.Join(container, "old-task"))
	if runtime.GOOS == "windows" {
		// OpenRoot's named-root handle deliberately lacks FILE_SHARE_DELETE.
		// Keep the already-executed child replacement assertion above; root
		// protection here is native refusal rather than a reachable replacement.
		if !errors.Is(renameErr, syscall.Errno(32)) {
			t.Fatal("pinned Windows root did not reject rename with sharing violation", renameErr)
		}
		if _, err := os.Lstat(filepath.Join(container, "old-task")); !os.IsNotExist(err) {
			t.Fatal("denied root rename changed the destination", err)
		}
		if _, err := scope.List(context.Background(), ""); err != nil {
			t.Fatal("native root protection left original root unreadable", err)
		}
		read, err := scope.Read(context.Background(), "root.txt")
		if err != nil || read.Content.Text != "original root" {
			t.Fatal("original root bytes were not retained", read, err)
		}
		return
	}
	if renameErr != nil {
		t.Fatal(renameErr)
	}
	taskFilesWrite(t, root, "safe.txt", []byte("new task"))
	if _, err := scope.List(context.Background(), ""); err == nil {
		t.Fatal("replaced task root accepted")
	}
}

func TestTaskFilesUnreadableDirectoryIsLocalToSearch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix mode permissions fixture")
	}
	root := t.TempDir()
	taskFilesWrite(t, root, "blocked/needle.txt", []byte("blocked"))
	taskFilesWrite(t, root, "readable/needle.txt", []byte("readable"))
	blocked := filepath.Join(root, "blocked")
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(blocked, 0o700)
	if file, err := os.Open(blocked); err == nil {
		_, readErr := file.ReadDir(1)
		file.Close()
		if readErr == nil {
			t.Skip("current user bypasses mode restrictions")
		}
	}
	scope := taskFilesScope(t, root)
	if _, err := scope.List(context.Background(), ""); err != nil {
		t.Fatal("one denied child broke parent", err)
	}
	result, err := scope.Search(context.Background(), "needle", 500)
	if err != nil || len(result.Entries) != 1 || len(result.Issues) == 0 {
		t.Fatal("permission failure discarded readable siblings", result, err)
	}
}

// Hooks run at existing cancellation checkpoints, so the fixture can replace a
// directory while its pinned handle is live without goroutine/sleep races.
type taskFilesCheckpointContext struct {
	context.Context
	calls int
	hook  func(int)
}

func (c *taskFilesCheckpointContext) Err() error {
	c.calls++
	if c.hook != nil {
		c.hook(c.calls)
	}
	return c.Context.Err()
}

func TestTaskFilesSearchLimitRevalidatesChangedChildBeforePublishing(t *testing.T) {
	for _, limit := range []int{1, 2} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			root := t.TempDir()
			for i := 0; i < 3; i++ {
				taskFilesWrite(t, root, fmt.Sprintf("child/needle-old-%d.txt", i), []byte("old"))
			}
			scope := taskFilesScope(t, root)
			control, err := scope.Search(context.Background(), "needle", limit)
			if err != nil || !control.Truncated || len(control.Entries) != limit {
				t.Fatal("ordinary limit control failed", control, err)
			}
			replaced := false
			ctx := &taskFilesCheckpointContext{Context: context.Background()}
			ctx.hook = func(checkpoint int) {
				// Six checks open root/child. Each following check precedes a
				// matching child entry: replace just before the overflow match.
				if checkpoint != 7+limit {
					return
				}
				if err := os.Rename(filepath.Join(root, "child"), filepath.Join(root, "old-child")); err != nil {
					t.Fatal(err)
				}
				taskFilesWrite(t, root, "child/needle-new.txt", []byte("replacement"))
				replaced = true
			}
			result, err := scope.Search(ctx, "needle", limit)
			if !replaced {
				t.Fatal("controlled child replacement was not reached", ctx.calls)
			}
			if err == nil || errors.Is(err, errTaskFilesSearchLimit) || len(result.Entries) != 0 || result.Truncated {
				t.Fatal("truncation published stale child entries or swallowed identity failure", result, err)
			}
		})
	}
}

func TestTaskFilesMediaSnapshotsExactBoundedReadonly(t *testing.T) {
	root := t.TempDir()
	fixtures := map[string][]byte{"picture.png": {0x89, 'P', 'N', 'G', 0, 1}, "document.pdf": []byte("%PDF-test\n"), "empty.png": {}}
	for name, data := range fixtures {
		taskFilesWrite(t, root, name, data)
	}
	taskFilesWrite(t, root, "notes.md", []byte("# read only"))
	scope := taskFilesScope(t, root)
	for name, expected := range fixtures {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, name)
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := scope.Read(context.Background(), name)
			if err != nil || len(metadata.MediaBytes) != 0 || metadata.MediaHash != "" || metadata.Content.Hash != "" {
				t.Fatal("metadata-only media contract changed", metadata, err)
			}
			// A nonempty file exactly at the cap must succeed, including the
			// extra EOF probe; empty files still need a positive server budget.
			budget := int64(len(expected))
			if budget == 0 {
				budget = 1
			}
			result, err := scope.ReadMedia(context.Background(), name, budget)
			if err != nil || !bytes.Equal(result.MediaBytes, expected) || result.MediaHash != Hash(expected) || result.Content.Size != int64(len(result.MediaBytes)) || result.FallbackReason != "" {
				t.Fatal("media snapshot did not preserve exact bytes", result, err)
			}
			if !result.Content.ReadOnly || result.Content.Draft != nil || result.Content.TextDraft != nil || result.Content.MarkdownEditContext != "" || result.Content.Hash != metadata.Content.Hash || result.Identity != metadata.Identity || result.Content.Modified != metadata.Content.Modified {
				t.Fatal("media snapshot changed readonly/metadata ownership", result)
			}
			after, err := os.Stat(path)
			data, readErr := os.ReadFile(path)
			if err != nil || readErr != nil || !bytes.Equal(data, expected) || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
				t.Fatal("media read mutated source file", err, readErr)
			}
			if len(expected) > 0 {
				result, err := scope.ReadMedia(context.Background(), name, int64(len(expected)-1))
				if err != nil || result.FallbackReason == "" || len(result.MediaBytes) != 0 || result.MediaHash != "" || result.Content.Size != int64(len(expected)) {
					t.Fatal("over-budget snapshot returned bytes", result, err)
				}
			}
		})
	}
	for _, maxBytes := range []int64{-1, 0, 1<<63 - 1} {
		if result, err := scope.ReadMedia(context.Background(), "picture.png", maxBytes); err == nil || len(result.MediaBytes) != 0 {
			t.Fatal("invalid media budget accepted", maxBytes, result, err)
		}
	}
	if _, err := scope.ReadMedia(context.Background(), "notes.md", 32); err == nil {
		t.Fatal("media byte mode widened the text preview contract")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if result, err := scope.ReadMedia(canceled, "picture.png", 32); !errors.Is(err, context.Canceled) || len(result.MediaBytes) != 0 {
		t.Fatal("canceled media snapshot published data", result, err)
	}
}

func TestTaskFilesMediaSnapshotsRejectLinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	taskFilesWrite(t, outside, "secret.png", []byte("outside"))
	if err := os.Symlink(filepath.Join(outside, "secret.png"), filepath.Join(root, "leaf.png")); err != nil {
		t.Skip("symlink fixture unavailable", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	scope := taskFilesScope(t, root)
	for _, path := range []string{"leaf.png", "linked/secret.png"} {
		if result, err := scope.ReadMedia(context.Background(), path, 64); err == nil || len(result.MediaBytes) != 0 || result.MediaHash != "" {
			t.Fatal("redirected media was returned", path, result, err)
		}
	}
}

func TestTaskFilesMediaSnapshotsPinBytesAndRejectParentReplacement(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(fmt.Sprintf("restore-%t", restore), func(t *testing.T) {
			root := t.TempDir()
			original := []byte("original pinned media")
			taskFilesWrite(t, root, "sub/image.png", original)
			scope := taskFilesScope(t, root)
			swapped, restored := false, false
			var renameBlocked error
			ctx := &taskFilesCheckpointContext{Context: context.Background()}
			ctx.hook = func(checkpoint int) {
				switch checkpoint {
				case 3:
					// directory/ancestor checks have completed; the file is now
					// pinned, immediately before the first actual file read.
					if err := os.Rename(filepath.Join(root, "sub"), filepath.Join(root, "old-sub")); err != nil {
						// Windows can protect an ancestor of an open file with
						// ACCESS_DENIED/SHARING_VIOLATION instead of allowing ABA.
						if runtime.GOOS == "windows" && (errors.Is(err, syscall.Errno(5)) || errors.Is(err, syscall.Errno(32))) {
							renameBlocked = err
							return
						}
						t.Fatal(err)
					}
					taskFilesWrite(t, root, "sub/image.png", []byte("foreign path bytes"))
					swapped = true
				case 4:
					if !restore || !swapped {
						return
					}
					// Restore before the EOF probe/post-read path validation.
					// A path reopen in the intervening read would capture foreign
					// bytes despite the same metadata being visible at both ends.
					if err := os.Remove(filepath.Join(root, "sub", "image.png")); err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(filepath.Join(root, "sub")); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(filepath.Join(root, "old-sub"), filepath.Join(root, "sub")); err != nil {
						t.Fatal(err)
					}
					restored = true
				}
			}
			result, err := scope.ReadMedia(ctx, "sub/image.png", 128)
			if renameBlocked != nil {
				if swapped || restored || err != nil || !bytes.Equal(result.MediaBytes, original) || result.MediaHash != Hash(original) {
					t.Fatal("Windows blocked rename but changed the pinned snapshot", result, err, renameBlocked)
				}
				if _, statErr := os.Stat(filepath.Join(root, "old-sub")); !os.IsNotExist(statErr) {
					t.Fatal("blocked ancestor rename created its destination", statErr)
				}
				current, readErr := os.ReadFile(filepath.Join(root, "sub", "image.png"))
				if readErr != nil || !bytes.Equal(current, original) {
					t.Fatal("blocked ancestor rename changed the original file", readErr)
				}
				t.Log("Windows pinned file prevented ancestor replacement:", renameBlocked)
				return
			}
			if !swapped || restored != restore {
				t.Fatal("controlled pinned-handle replacement did not execute", swapped, restored, ctx.calls)
			}
			if !restore {
				if err == nil || len(result.MediaBytes) != 0 || result.MediaHash != "" {
					t.Fatal("changed media parent published bytes", result, err)
				}
				return
			}
			if err != nil || !bytes.Equal(result.MediaBytes, original) || result.MediaHash != Hash(original) {
				t.Fatal("ancestor ABA escaped the pinned media bytes", result, err)
			}
			taskFilesWrite(t, root, "sub/image.png", []byte("later edit"))
			if !bytes.Equal(result.MediaBytes, original) || result.MediaHash != Hash(original) {
				t.Fatal("returned media snapshot changed after source replacement")
			}
		})
	}
}
