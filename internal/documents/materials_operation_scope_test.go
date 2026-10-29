package documents

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

type localOperationFixture struct {
	service             *Service
	public, physical    MaterialsScope
	aliasRoot, realRoot string
}

func localOperationLink(t *testing.T, alias, target string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", alias, target).CombinedOutput()
		if err != nil {
			t.Fatalf("create directory junction: %v: %s", err, output)
		}
		return
	}
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
}

func newLocalOperationFixture(t *testing.T) localOperationFixture {
	t.Helper()
	base := t.TempDir()
	realRoot, aliasRoot := filepath.Join(base, "actual"), filepath.Join(base, "configured")
	if err := os.MkdirAll(filepath.Join(realRoot, "task"), 0755); err != nil {
		t.Fatal(err)
	}
	localOperationLink(t, aliasRoot, realRoot)
	public, err := PrepareLocalTaskMaterials(filepath.Join(aliasRoot, "task"))
	if err != nil {
		t.Fatal(err)
	}
	return localOperationFixture{service: NewService(filepath.Join(base, "cache")), public: public, physical: LocalTaskMaterials(filepath.Join(realRoot, "task")), aliasRoot: aliasRoot, realRoot: realRoot}
}

func localOperationFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func localOperationBytes(t *testing.T, path string, expected []byte) {
	t.Helper()
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, expected) {
		t.Fatalf("unexpected file at %s: %q want=%q error=%v", path, actual, expected, err)
	}
}

func TestLocalMaterialsOperationListReadCreateAndSnapshots(t *testing.T) {
	f := newLocalOperationFixture(t)
	textPath := filepath.Join(f.public.Owned, "source.txt")
	raw := []byte("\xef\xbb\xbfknown needle\r\n中文\r\n")
	localOperationFile(t, filepath.Join(f.physical.Owned, "source.txt"), raw)
	localOperationFile(t, filepath.Join(f.physical.Owned, "reading.md"), []byte("# known needle\r\n"))
	entries, err := f.service.List(f.public.Root)
	if err != nil || len(entries) != 2 {
		t.Fatalf("list after preparation: %+v %v", entries, err)
	}
	var assertPublicEntries func([]Entry)
	assertPublicEntries = func(entries []Entry) {
		for _, entry := range entries {
			if !pathWithin(f.public.Root, entry.Path) || pathWithin(f.physical.Root, entry.Path) {
				t.Fatalf("list exposed physical namespace: %+v", entry)
			}
			assertPublicEntries(entry.Children)
		}
	}
	assertPublicEntries(entries)
	for _, path := range []string{textPath, "docs/source.txt"} {
		content, err := f.service.ReadMaterialsPreview(f.public.Root, path)
		if err != nil || content.Path != textPath || content.Hash != Hash(raw) || content.Text != "known needle\r\n中文\r\n" {
			t.Fatalf("read alias or relative path: %+v %v", content, err)
		}
	}
	text, err := f.service.MaterialTextSnapshot(f.public.Root, textPath)
	if err != nil || text.Path != textPath || !text.HasUTF8BOM {
		t.Fatalf("text snapshot: %+v %v", text, err)
	}
	saved, err := f.service.SaveMaterialText(context.Background(), f.public.Root, textPath, "changed needle\r\n", text.Hash, true)
	if err != nil || saved.Path != textPath {
		t.Fatalf("text save: %+v %v", saved, err)
	}
	localOperationBytes(t, filepath.Join(f.physical.Owned, "source.txt"), []byte("\xef\xbb\xbfchanged needle\r\n"))
	if err := f.service.CacheDraft(textPath, "legacy draft"); err != nil {
		t.Fatal(err)
	}
	legacy, err := f.service.Save(f.public.Root, textPath, "legacy saved\r\n", saved.Hash, false)
	if err != nil || legacy.Path != textPath {
		t.Fatalf("legacy save path changed: %+v %v", legacy, err)
	}
	if _, err := f.service.Draft(textPath); !os.IsNotExist(err) {
		t.Fatalf("legacy draft was cleared using a different namespace: %v", err)
	}
	markdownPath := filepath.Join(f.public.Owned, "reading.md")
	markdown, err := f.service.MarkdownSnapshot(f.public.Root, markdownPath)
	if err != nil || markdown.Path != markdownPath {
		t.Fatalf("markdown snapshot: %+v %v", markdown, err)
	}
	markdown, err = f.service.SaveMarkdown(context.Background(), f.public.Root, "docs/reading.md", "# updated\r\n", markdown.Hash)
	if err != nil || markdown.Path != markdownPath {
		t.Fatalf("markdown save: %+v %v", markdown, err)
	}
	localOperationBytes(t, filepath.Join(f.physical.Owned, "reading.md"), []byte("# updated\r\n"))
	created, err := f.service.Create(f.public.Root, "docs/created.txt", []byte("created"), false)
	if err != nil || created != filepath.Join(f.public.Owned, "created.txt") {
		t.Fatalf("create: %s %v", created, err)
	}
	localOperationBytes(t, filepath.Join(f.physical.Owned, "created.txt"), []byte("created"))
}

type localOperationTrash struct{ paths []string }

func (t *localOperationTrash) moveToTrash(_ context.Context, path string) error {
	t.paths = append(t.paths, path)
	return os.Remove(path)
}

func TestLocalMaterialsOperationConflictsSearchCopyTrashAndDrafts(t *testing.T) {
	f := newLocalOperationFixture(t)
	ctx := context.Background()
	file := filepath.Join(f.public.Owned, "source.txt")
	localOperationFile(t, filepath.Join(f.physical.Owned, "source.txt"), []byte("original needle\n"))
	request := FileOperationRequest{Kind: FileOperationCreateFile, Directory: f.public.Owned, Name: "source.txt", Content: []byte("replacement needle\n")}
	conflict, err := f.service.ExecuteFileOperation(ctx, f.public.Root, request)
	if err != nil || conflict.Status != FileOperationConflicted || conflict.Conflict == nil || conflict.Conflict.Request.Directory != f.public.Owned {
		t.Fatalf("conflict lost public request: %+v %v", conflict, err)
	}
	resolved, err := f.service.ResolveFileOperationConflict(ctx, f.public.Root, *conflict.Conflict, FileConflictReplace, "")
	if err != nil || resolved.Status != FileOperationCompleted || resolved.RelativePath != "docs/source.txt" {
		t.Fatalf("replace conflict: %+v %v", resolved, err)
	}
	localOperationBytes(t, filepath.Join(f.physical.Owned, "source.txt"), request.Content)
	search, err := f.service.SearchMaterials(ctx, f.public.Root, "needle")
	if err != nil || len(search.Hits) != 1 || search.Hits[0].RelativePath != "docs/source.txt" {
		t.Fatalf("search: %+v %v", search, err)
	}
	for _, resolve := range []func() ([]string, error){
		func() ([]string, error) { return f.service.ResolveCopyPaths(ctx, f.public.Root, []string{file}) },
		func() ([]string, error) { return f.service.ResolveCopyFiles(ctx, f.public.Root, []string{file}) },
		func() ([]string, error) { return f.service.ResolveDeleteItems(f.public.Root, []string{file}) },
	} {
		paths, err := resolve()
		if err != nil || len(paths) != 1 || paths[0] != file {
			t.Fatalf("selection lost public namespace: %v %v", paths, err)
		}
	}
	copyPath := filepath.Join(f.public.Owned, "copy.txt")
	if result, err := f.service.Copy(f.public.Root, file, copyPath); err != nil || result != copyPath {
		t.Fatalf("copy: %s %v", result, err)
	}
	importPath := filepath.Join(f.public.Temporary, "imported.txt")
	external := filepath.Join(t.TempDir(), "source.txt")
	localOperationFile(t, external, []byte("external"))
	if result, err := f.service.Import(f.public.Root, external, importPath); err != nil || result != importPath {
		t.Fatalf("import: %s %v", result, err)
	}
	identity, err := NewMarkdownDraftIdentity("task", f.public.Base, f.public.Root, file)
	if err != nil {
		t.Fatal(err)
	}
	beforeKey := identity.key()
	draft := MaterialTextDraft{Identity: identity, Content: "unsaved text", BaselineHash: Hash(request.Content), Newline: "\n"}
	if err := f.service.WriteMaterialTextDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	loaded, err := f.service.ReadMaterialTextDraft(ctx, identity)
	if err != nil || loaded == nil || loaded.Content != draft.Content || loaded.Identity.MaterialsRoot != f.public.Root || loaded.Identity.key() != beforeKey {
		t.Fatalf("draft identity changed: %+v %v", loaded, err)
	}
	backend := &localOperationTrash{}
	trashed, err := (&TrashService{backend: backend}).MoveToTrash(ctx, f.public.Root, []string{copyPath})
	if err != nil || len(trashed) != 1 || trashed[0].Path != copyPath || trashed[0].Error != "" || len(backend.paths) != 1 || backend.paths[0] != filepath.Join(f.physical.Owned, "copy.txt") {
		t.Fatalf("trash did not use pinned physical path and public result: %+v %v %v", trashed, backend.paths, err)
	}
}

func TestLocalMaterialsOperationRejectsOtherNamespacesAndReadOnlyWrites(t *testing.T) {
	f := newLocalOperationFixture(t)
	localOperationFile(t, filepath.Join(f.physical.Owned, "source.txt"), []byte("keep"))
	localOperationFile(t, filepath.Join(f.physical.Owned, ".git", "config"), []byte("keep git"))
	localOperationFile(t, filepath.Join(f.physical.Owned, ".silverwing-private"), []byte("keep managed"))
	peer := filepath.Join(f.public.Root, "peer")
	localOperationFile(t, filepath.Join(f.physical.Root, "peer", "reference.txt"), []byte("reference"))
	otherAlias := filepath.Join(filepath.Dir(f.aliasRoot), "other-alias")
	localOperationLink(t, otherAlias, f.realRoot)
	for name, path := range map[string]string{
		"parent":           "../outside.txt",
		"within traversal": "docs/../temp/.gitignore",
		"physical":         filepath.Join(f.physical.Owned, "source.txt"),
		"different alias":  filepath.Join(otherAlias, "task", ".workspace", "materials", "docs", "source.txt"),
		"git":              "docs/.git/config",
		"managed":          "docs/.silverwing-private",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := f.service.ReadMaterialsPreview(f.public.Root, path); err == nil {
				t.Fatalf("read accepted forbidden path: %s", path)
			}
			if _, err := f.service.Create(f.public.Root, path, []byte("bad"), false); err == nil {
				t.Fatalf("write accepted forbidden path: %s", path)
			}
		})
	}
	for _, path := range []string{filepath.Join(peer, "new.txt"), filepath.Join(f.public.Temporary, ".gitignore"), f.public.Owned, f.public.Temporary} {
		if _, err := f.service.Create(f.public.Root, path, []byte("bad"), false); err == nil {
			t.Fatalf("read-only or root write accepted: %s", path)
		}
	}
	if content, err := f.service.ReadMaterialsPreview(f.public.Root, filepath.Join(peer, "reference.txt")); err != nil || content.Text != "reference" {
		t.Fatalf("peer became unreadable: %+v %v", content, err)
	}
	localOperationBytes(t, filepath.Join(f.physical.Owned, "source.txt"), []byte("keep"))
	// A generic root under a junction still follows the original strict policy.
	if _, err := f.service.List(f.aliasRoot); err == nil {
		t.Fatal("generic materials root inherited the local ancestor exception")
	}
}

func TestLocalMaterialsOperationRejectsInternalDirectoryLinks(t *testing.T) {
	for _, part := range []string{".workspace", ".workspace/materials", ".workspace/materials/docs", ".workspace/materials/temp"} {
		t.Run(part, func(t *testing.T) {
			f := newLocalOperationFixture(t)
			target := filepath.Join(f.physical.Base, filepath.FromSlash(part))
			moved := target + "-original"
			if err := os.Rename(target, moved); err != nil {
				t.Fatal(err)
			}
			localOperationLink(t, target, moved)
			sentinel := filepath.Join(moved, "keep.txt")
			localOperationFile(t, sentinel, []byte("keep"))
			if _, err := f.service.List(f.public.Root); err == nil {
				t.Fatal("linked scope directory listed")
			}
			if _, err := f.service.ExecuteFileOperation(context.Background(), f.public.Root, FileOperationRequest{Kind: FileOperationCreateFile, Directory: f.public.Owned, Name: "bad.txt"}); err == nil {
				t.Fatal("linked scope directory became writable")
			}
			localOperationBytes(t, sentinel, []byte("keep"))
		})
	}
	f := newLocalOperationFixture(t)
	external := t.TempDir()
	localOperationFile(t, filepath.Join(external, "keep.txt"), []byte("keep"))
	localOperationLink(t, filepath.Join(f.physical.Owned, "nested"), external)
	if _, err := f.service.ReadMaterialsPreview(f.public.Root, "docs/nested/keep.txt"); err == nil {
		t.Fatal("nested link read accepted")
	}
	if _, err := f.service.Create(f.public.Root, "docs/nested/bad.txt", []byte("bad"), false); err == nil {
		t.Fatal("nested link write accepted")
	}
	localOperationBytes(t, filepath.Join(external, "keep.txt"), []byte("keep"))
}

func TestLocalMaterialsOperationPinsDirectoryIdentitiesUntilCommit(t *testing.T) {
	for _, part := range []string{"task", ".workspace", ".workspace/materials", ".workspace/materials/docs", ".workspace/materials/temp", "alias"} {
		t.Run(part, func(t *testing.T) {
			f := newLocalOperationFixture(t)
			sentinels := map[string][]byte{
				filepath.Join(f.physical.Base, "task-sentinel.txt"):                    []byte("retained task"),
				filepath.Join(f.physical.Base, ".workspace", "workspace-sentinel.txt"): []byte("retained workspace"),
				filepath.Join(f.physical.Root, "materials-sentinel.txt"):               []byte("retained materials"),
				filepath.Join(f.physical.Owned, "keep.txt"):                            []byte("retained source"),
				filepath.Join(f.physical.Temporary, "temp-sentinel.txt"):               []byte("retained temporary file"),
				filepath.Join(f.physical.Temporary, ".gitignore"):                      []byte("# Silverwing local temporary files\n*\n"),
			}
			for path, data := range sentinels {
				localOperationFile(t, path, data)
			}
			assertSentinels := func() {
				t.Helper()
				for path, data := range sentinels {
					localOperationBytes(t, path, data)
					relative, err := filepath.Rel(f.physical.Base, path)
					if err != nil {
						t.Fatal(err)
					}
					localOperationBytes(t, filepath.Join(f.public.Base, relative), data)
				}
				for _, owned := range []string{f.physical.Owned, f.public.Owned} {
					if _, err := os.Stat(filepath.Join(owned, "must-not-commit.txt")); !os.IsNotExist(err) {
						t.Fatalf("replacement attempt wrote a destination: %v", err)
					}
					entries, err := os.ReadDir(owned)
					if err != nil {
						t.Fatal(err)
					}
					for _, entry := range entries {
						if strings.HasPrefix(entry.Name(), ".silverwing-") {
							t.Fatal("replacement attempt leaked staging files")
						}
					}
				}
			}
			scope, err := newMaterialsOperationScope(f.public.Root)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.close()
			if part == "alias" {
				newRoot := filepath.Join(filepath.Dir(f.realRoot), "replacement-root")
				if err := os.CopyFS(newRoot, os.DirFS(f.realRoot)); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(f.aliasRoot); err != nil {
					t.Fatal(err)
				}
				localOperationLink(t, f.aliasRoot, newRoot)
			} else {
				target := f.physical.Base
				if part != "task" {
					target = filepath.Join(target, filepath.FromSlash(part))
				}
				moved := target + "-old-identity"
				original, err := os.Lstat(target)
				if err != nil || !os.SameFile(original, original) {
					t.Fatalf("capture original directory identity: %v", err)
				}
				if err := os.Rename(target, moved); err != nil {
					// Live descendant handles can make Windows reject an ancestor
					// rename even when every handle allows delete sharing. Prove
					// that the pins, rather than a broken fixture, prevented it.
					const windowsAccessDenied = syscall.Errno(5)
					if runtime.GOOS != "windows" || !errors.Is(err, windowsAccessDenied) {
						t.Fatal(err)
					}
					current, statErr := os.Lstat(target)
					if statErr != nil || !os.SameFile(original, current) {
						t.Fatalf("blocked rename changed the original directory: %v", statErr)
					}
					if _, err := os.Lstat(moved); !os.IsNotExist(err) {
						t.Fatalf("blocked rename created its destination: %v", err)
					}
					if err := scope.revalidate(); err != nil {
						t.Fatalf("blocked rename invalidated the untouched scope: %v", err)
					}
					assertSentinels()
					scope.close()
					if err := os.Rename(target, moved); err != nil {
						t.Fatalf("same rename remains blocked after releasing scope pins: %v", err)
					}
				}
				if err := os.CopyFS(target, os.DirFS(moved)); err != nil {
					t.Fatal(err)
				}
			}
			// The new paths contain a complete valid layout. Rejection must
			// come from the captured identity, not from a missing fixture file.
			if _, err := validatedMaterialsRoot(f.physical.Root); err != nil {
				t.Fatal("replacement layout is not valid", err)
			}
			if err := scope.revalidate(); err == nil {
				t.Fatal("directory replacement retained operation authority")
			}
			destination := filepath.Join(f.physical.Owned, "must-not-commit.txt")
			err = writeNewFileStaged(context.Background(), f.physical.Root, "docs", f.physical.Owned, destination, []byte("bad"), 0600, scope)
			if err == nil {
				t.Fatal("staged operation ignored the pinned scope identity")
			}
			assertSentinels()
		})
	}
}
