package desktop

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	markdownui "silverwing-go/internal/ui/markdown"
)

func TestFullReviewMaterialsCaptureKeepsPendingPropertyDraft(t *testing.T) {
	const source = "---\ntags: [original]\n---\n\nbody\n"
	s, _, _, view := markdownLiveAcceptanceFixture(t, source, false, 920)
	owner := s.ensureMarkdownEditor()
	if err := view.Click("标签：original"); err != nil {
		t.Fatal(err)
	}
	if err := view.Click("标签名称"); err != nil {
		t.Fatal(err)
	}
	view.Command("selectAll")
	view.Type("pending-material")
	if err := s.captureMarkdownEditorInput(owner); err == nil {
		t.Fatal("materials capture allowed an unconfirmed property draft to be discarded")
	}
	view.Frame()
	if owner.componentEditor.Snapshot().Source != source {
		t.Fatal("capture rewrote source")
	}
	if _, ok := view.Find("标签名称"); !ok {
		t.Fatal("pending editor disappeared")
	}
	if err := view.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if err := s.captureMarkdownEditorInput(owner); err != nil {
		t.Fatal(err)
	}
}

func TestFullReviewLocalFileLinksCaptureTheirActualHost(t *testing.T) {
	for _, host := range []string{"global", "task", "comment"} {
		t.Run(host, func(t *testing.T) {
			var s *app
			var owner *markdownEditorState
			switch host {
			case "global":
				s, _, _ = settingsInstructionFixture(t, "body\n")
				owner = s.ensureTaskNotesUI().activeMarkdown
			case "task":
				var draft *taskLocalNoteDraft
				s, draft = taskLocalNoteFixture(t)
				loadTaskLocalNote(s, draft, "body\n", true)
				owner = draft.markdown
			case "comment":
				var state *requirementCommentComposer
				s, _, _, state = commentComposerFixture(t)
				owner = s.ensureCommentMarkdownEditor(state)
				t.Cleanup(func() { closeMarkdownPreviewState(owner) })
			}
			target := markdownui.LinkTarget{Path: filepath.Join(t.TempDir(), "local.txt")}
			s.openMarkdownFileLink(owner, target)
			viewer := s.materialsUnit.state.markdownLinkFile
			if viewer == nil || viewer.owner != owner || viewer.target.Path != target.Path {
				t.Fatalf("valid %s owner rejected by materials-only capture: %q", host, s.lastError)
			}
			s.closeMarkdownLinkFile()
		})
	}
}

func TestFullReviewCommentFilePreviewInvalidatesWithItsOwner(t *testing.T) {
	for _, change := range []string{"account", "config", "navigation", "closed", "active-composer", "draft", "comment"} {
		t.Run(change, func(t *testing.T) {
			s, _, _, composer := commentComposerFixture(t)
			owner := s.ensureCommentMarkdownEditor(composer)
			body := owner.doc.Source()
			s.openMarkdownFileLink(owner, markdownui.LinkTarget{Path: filepath.Join(t.TempDir(), "local.txt")})
			viewer := s.materialsUnit.state.markdownLinkFile
			if viewer == nil || !s.markdownLinkFileCurrent(viewer) {
				t.Fatal("valid comment preview rejected")
			}
			switch change {
			case "account":
				s.requirementUnit.state.requirementIdentityJobs = &requirementIdentityJobState{confirmed: true, identity: "account-2", state: application.RequirementIdentityAuthenticated}
			case "config":
				s.settingsUnit.state.configJSON = `{"meegleExecutablePath":"other-cli"}`
			case "navigation":
				s.requirementUnit.state.requirementContentJobs = &requirementContentJobState{visible: "another requirement"}
			case "closed":
				composer.open = false
			case "active-composer":
				s.requirementUnit.state.commentComposers.active = nil
			case "draft":
				composer.draft.ID = "different-draft"
			case "comment":
				composer.editCommentID = "different-comment"
			}
			s.observeRequirementCommentComposers()
			if s.markdownLinkFileCurrent(viewer) {
				t.Fatal("stale owner still accepts late file/media results")
			}
			tester := ui.NewTester(func(c *ui.Context) {
				p := setTheme(c, false)
				s.renderMarkdownLinkFile(c, p)
				s.renderRequirementCommentLayers(c, p)
			}, 960, 800)
			tester.Frame()
			if s.materialsUnit.state.markdownLinkFile != nil || viewer.ctx.Err() == nil {
				t.Fatal("stale preview was not dismissed and cancelled")
			}
			if owner.doc.Source() != body {
				t.Fatal("invalidating preview rewrote the comment")
			}
		})
	}
}

func TestFullReviewCommentFilePreviewIsAboveComposer(t *testing.T) {
	for _, closeWith := range []string{"escape", "return-button"} {
		t.Run(closeWith, func(t *testing.T) {
			s, _, _, composer := commentComposerFixture(t)
			owner := s.ensureCommentMarkdownEditor(composer)
			buffer, body, mode := owner.live.Buffer(), owner.doc.Source(), owner.componentEditor.Mode()
			s.openMarkdownFileLink(owner, markdownui.LinkTarget{Path: filepath.Join(t.TempDir(), "local.txt")})
			viewer := s.materialsUnit.state.markdownLinkFile
			if viewer == nil {
				t.Fatal("missing file preview")
			}
			viewer.loading, viewer.errorText = false, ""
			viewer.content = documents.Content{Kind: "text", Path: viewer.target.Path, Text: "local file content"}
			tester := ui.NewTester(func(c *ui.Context) {
				p := setTheme(c, false)
				ui.Column(c).Fill()
				s.renderMarkdownLinkFile(c, p)
				s.renderRequirementCommentLayers(c, p)
			}, 960, 800)
			tester.Frame()
			if closeWith == "escape" {
				tester.Key(0, ui.KeyEscape)
			} else if err := tester.Click("返回原文"); err != nil {
				t.Fatal(err)
			}
			tester.Frame()
			if !composer.open || s.materialsUnit.state.markdownLinkFile != nil || viewer.ctx.Err() == nil {
				t.Fatalf("close %s did not return from file to composer: composer=%v preview=%v", closeWith, composer.open, s.materialsUnit.state.markdownLinkFile != nil)
			}
			if owner.live.Buffer() != buffer || owner.doc.Source() != body || owner.componentEditor.Mode() != mode {
				t.Fatal("returning from preview changed comment source, Buffer or mode")
			}
		})
	}
}
