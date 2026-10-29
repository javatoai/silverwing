package desktop

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/editor"
)

type skillPreviewFileCall struct {
	ctx            context.Context
	root, relative string
}

type fakeSkillPreviewFiles struct {
	calls        []skillPreviewFileCall
	path         string
	err          error
	beforeReturn func()
}

func (f *fakeSkillPreviewFiles) ResolveLocalFile(ctx context.Context, root, relative string) (string, error) {
	f.calls = append(f.calls, skillPreviewFileCall{ctx: ctx, root: root, relative: relative})
	if f.beforeReturn != nil {
		f.beforeReturn()
	}
	return f.path, f.err
}

// Build each preview through its actual result handler rather than binding a
// synthetic markdown state directly. No resource files or native decoder are used.
func skillPreviewResolverFixture(t *testing.T, kind string, files localPreviewFileResolver) (*app, *markdownEditorState, string) {
	t.Helper()
	s := &app{page: "settings"}
	s.snapshot.Home = t.TempDir()
	s.settingsUnit.previewFiles = files
	container := t.TempDir()
	root := filepath.Join(container, "example")
	source := "![preview](images/picture%20one.png)"
	var markdown *markdownEditorState
	if kind == "local browser" {
		s.settingsUnit.state.settingsPage = "skill-local"
		browser := s.ensureSkillsBrowser()
		browser.root, browser.selectedDirectory, browser.selectedPath = container, "example", "SKILL.md"
		browser.previewMarkdown, browser.pendingPreview = true, "preview-request"
		browser.handleResult(application.Action{Kind: "skills.files/preview", Payload: map[string]any{
			"directoryName": "example", "relativePath": "SKILL.md", "browserRequestID": "preview-request",
		}}, application.ActionResult{Text: source})
		markdown = browser.markdown
	} else {
		s.settingsUnit.state.settingsPage = "skill-sources"
		s.settingsUnit.state.settingsSkillRoot = container
		action := application.Action{Kind: kind, Payload: map[string]any{
			"sourceId": "source", "name": "example", "skillName": "example", "directoryName": "example", "relativePath": "SKILL.md",
		}}
		s.prepareSettingsExtensionRead(&action)
		s.applySettingsExtensionPreview(action, application.ActionResult{Data: map[string]any{
			"name": "SKILL.md", "text": source, "path": filepath.Join(root, "SKILL.md"),
		}})
		markdown = s.settingsUnit.state.settingsExtensionRequests.previewMarkdown
	}
	if markdown == nil || !s.skillMarkdownPreviewCurrent(markdown) {
		t.Fatalf("%s did not create a current Skill preview", kind)
	}
	t.Cleanup(func() { closeMarkdownPreviewState(markdown) })
	return s, markdown, root
}

func TestSkillPreviewProductionBindersConsumeInjectedFileResolver(t *testing.T) {
	for _, kind := range []string{"local browser", "skills.preview", "skills.files/preview", "codex.extensions/plugin/skill-preview"} {
		t.Run(kind, func(t *testing.T) {
			failure := errors.New("injected local resource rejection")
			files := &fakeSkillPreviewFiles{err: failure}
			s, markdown, root := skillPreviewResolverFixture(t, kind, files)
			if markdown.localPreviewFiles != files {
				t.Fatal("production binder ignored the supplied resolver")
			}
			image, err := s.ensureMarkdownImage(markdown, editor.Part{ID: 1}, editor.Node{
				Destination: "images/picture%20one.png", Range: editor.SourceRange{Start: 0, End: 1},
			})
			if err != nil || image == nil || image.loading || image.error != failure.Error() {
				t.Fatalf("image consumer lost the injected resolver error: %#v, %v", image, err)
			}
			if len(files.calls) != 1 {
				t.Fatalf("image consumer resolved %d times, want once", len(files.calls))
			}
			call := files.calls[0]
			if call.ctx == nil || call.ctx.Err() != nil || call.root != root || call.relative != filepath.Join("images", "picture one.png") {
				t.Fatalf("resolver did not receive the captured root and decoded relative path: %#v", call)
			}
		})
	}
}

func TestSkillPreviewResolverResultAndFailureRemainAuthoritative(t *testing.T) {
	files := &fakeSkillPreviewFiles{}
	s, markdown, root := skillPreviewResolverFixture(t, "local browser", files)
	// This path deliberately does not exist. File authorization is the port's
	// responsibility; the consumer must not construct a second filesystem service.
	files.path = filepath.Join(root, "authorized-by-injected-resolver.png")
	path, remote, err := s.resolveSkillMarkdownImage(markdown, "images/picture.png")
	if err != nil || remote || path != files.path || len(files.calls) != 1 {
		t.Fatalf("consumer bypassed the supplied resolver result: %q, %t, %v", path, remote, err)
	}
	failure := errors.New("preserve this resolver failure")
	files.err = failure
	if path, remote, err := s.resolveSkillMarkdownImage(markdown, "images/picture.png"); path != "" || remote || !errors.Is(err, failure) {
		t.Fatalf("consumer replaced or ignored a resolver failure: %q, %t, %v", path, remote, err)
	}
	markdown.localPreviewFiles = nil
	before := len(files.calls)
	if path, remote, err := s.resolveSkillMarkdownImage(markdown, "images/picture.png"); path != "" || remote || err == nil || len(files.calls) != before {
		t.Fatal("a missing bound port fell back to an unrelated filesystem resolver")
	}
}

func TestSkillPreviewFiltersURLsAndPathsBeforeLocalResolution(t *testing.T) {
	for _, test := range []struct {
		destination string
		remote      bool
	}{
		{destination: "https://example.test/image.png", remote: true},
		{destination: "HTTPS://example.test/image.png", remote: true},
		{destination: "https://user:password@example.test/image.png"},
		{destination: "http://"},
		{destination: "%"},
		{destination: "file:///outside.png"},
		{destination: "C:/outside.png"},
		{destination: "/outside.png"},
		{destination: `\outside.png`},
		{destination: "//other-host/outside.png"},
		{destination: "../outside.png"},
		{destination: "%2e%2e/outside.png"},
	} {
		t.Run(test.destination, func(t *testing.T) {
			files := &fakeSkillPreviewFiles{}
			s, markdown, _ := skillPreviewResolverFixture(t, "local browser", files)
			path, remote, err := s.resolveSkillMarkdownImage(markdown, test.destination)
			if len(files.calls) != 0 || path != "" || remote != test.remote || (err == nil) != test.remote {
				t.Fatalf("URL/path validation was bypassed: path=%q remote=%t error=%v calls=%d", path, remote, err, len(files.calls))
			}
		})
	}
}

func TestSkillPreviewRejectsInvalidOwnerBeforeAndAfterResolution(t *testing.T) {
	for _, kind := range []string{"local browser", "skills.preview"} {
		for _, when := range []string{"before", "after", "closed"} {
			t.Run(kind+"/"+when, func(t *testing.T) {
				files := &fakeSkillPreviewFiles{}
				s, markdown, root := skillPreviewResolverFixture(t, kind, files)
				files.path = filepath.Join(root, "image.png")
				wantCalls := 0
				switch when {
				case "before":
					s.page = "tasks"
				case "after":
					files.beforeReturn = func() { s.page = "tasks" }
					wantCalls = 1
				case "closed":
					markdown.closed = true
				}
				path, remote, err := s.resolveSkillMarkdownImage(markdown, "image.png")
				if path != "" || remote || err == nil || len(files.calls) != wantCalls {
					t.Fatalf("invalid preview owner accepted a resource: path=%q remote=%t error=%v calls=%d", path, remote, err, len(files.calls))
				}
			})
		}
	}
}

func TestLocalPreviewFileAdapterRetainsFilesystemBoundary(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "skill")
	image := filepath.Join(root, "images", "picture.png")
	if err := os.MkdirAll(filepath.Dir(image), 0755); err != nil {
		t.Fatal(err)
	}
	original := []byte("regular file bytes\r\n ")
	if err := os.WriteFile(image, original, 0600); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(base, "outside.png")
	if err := os.WriteFile(sentinel, original, 0600); err != nil {
		t.Fatal(err)
	}
	var controller settingsController
	files := controller.previewResourceResolver()
	if files != controller.previewResourceResolver() {
		t.Fatal("the composition root recreated a resolver for every resource")
	}
	if path, err := files.ResolveLocalFile(context.Background(), root, filepath.Join("images", "picture.png")); err != nil || path != image {
		t.Fatalf("ordinary root-relative image was rejected: %q, %v", path, err)
	}
	for _, relative := range []string{"images", "missing.png", "../outside.png"} {
		if path, err := files.ResolveLocalFile(context.Background(), root, relative); err == nil || path != "" {
			t.Errorf("adapter accepted unsafe or non-regular resource %q: %q, %v", relative, path, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := files.ResolveLocalFile(ctx, root, filepath.Join("images", "picture.png")); !errors.Is(err, context.Canceled) {
		t.Fatalf("adapter lost cancellation: %v", err)
	}
	for _, path := range []string{image, sentinel} {
		if data, err := os.ReadFile(path); err != nil || string(data) != string(original) {
			t.Fatalf("preview resolution modified a file: %s, %q, %v", path, data, err)
		}
	}
}

func TestLocalPreviewFileAdapterRejectsInternalReparseAndPreservesSentinel(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	sentinel := filepath.Join(outside, "sentinel.png")
	original := []byte("outside file must remain unchanged\r\n ")
	if err := os.WriteFile(sentinel, original, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "internal-link")
	if runtime.GOOS == "windows" {
		output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", link, outside).CombinedOutput()
		if err != nil {
			t.Skipf("create internal junction fixture: %v: %s", err, output)
		}
	} else if err := os.Symlink(outside, link); err != nil {
		t.Skipf("create internal symlink fixture: %v", err)
	}
	files := newDocumentLocalPreviewFileResolver()
	if path, err := files.ResolveLocalFile(context.Background(), root, filepath.Join("internal-link", "sentinel.png")); path != "" || err == nil {
		t.Fatalf("adapter resolved an internal reparse path: %q, %v", path, err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != string(original) {
		t.Fatalf("preview resolution changed the external sentinel: %q, %v", data, err)
	}
}
