package domain

import (
	"strings"
	"testing"
)

func TestCommentInlineImagesKeepPositionsAndUploadEachAssetOnce(t *testing.T) {
	ref := CommentImageMarkdownRef("one")
	deleted := CommentImageMarkdownRef("deleted")
	source := "前 **文**\r\n\r\n![一](<" + ref + "> \"保留标题\")\r\n\r\n中间 ![再一次][same]\r\n\r\n[same]: " + ref + "\r\n\r\n`![代码](" + ref + ")`\r\n\r\n![手写路径](./private.png)"
	d := RequirementCommentDraft{Content: source, Images: []CommentDraftAsset{
		{ID: "one", Name: "图.png", MarkdownRef: ref},
		{ID: "deleted", Name: "已删.png", MarkdownRef: deleted},
	}}
	if got := d.ActiveImages(); len(got) != 1 || got[0].ID != "one" {
		t.Fatalf("active images = %#v", got)
	}
	calls := map[string]int{}
	body := d.Markdown(func(a CommentDraftAsset) (string, string) {
		calls[a.ID]++
		return "https://example.com/" + a.ID, "token-" + a.ID
	})
	if calls["one"] != 1 || len(calls) != 1 || strings.Count(body, "<!--token-one-->") != 2 || strings.Contains(body, "已删.png") {
		t.Fatalf("wrong asset serialization: calls %#v, body %q", calls, body)
	}
	if !strings.Contains(body, "![一](<https://example.com/one> \"保留标题\")") || !strings.Contains(body, "中间 ![再一次](https://example.com/one)") || !strings.Contains(body, "`![代码]("+ref+")`") || !strings.HasSuffix(body, "![手写路径](./private.png)") {
		t.Fatalf("image positions or unrelated Markdown changed: %q", body)
	}
	if d.Content != source || d.Images[1].MarkdownRef != deleted {
		t.Fatal("serialization changed the undo-restorable draft")
	}
	d.Content = "正文"
	if len(d.ActiveImages()) != 0 || d.Markdown(func(CommentDraftAsset) (string, string) { t.Fatal("deleted reference uploaded"); return "", "" }) != "正文" {
		t.Fatal("deleted references were appended or remained active")
	}
	d.Content = "![撤销恢复](" + ref + ")"
	if len(d.ActiveImages()) != 1 {
		t.Fatal("restored source cannot reactivate retained asset metadata")
	}
}

func TestCommentLegacyImageNormalizationPreservesPendingSnapshot(t *testing.T) {
	for _, state := range []string{"", "failed", "publishing", "unknown"} {
		t.Run(state, func(t *testing.T) {
			d := RequirementCommentDraft{Content: "保留正文", Images: []CommentDraftAsset{{ID: "legacy", Name: "旧图.png"}}}
			if state != "" {
				d.Attempt = &CommentPublishAttempt{State: state, Body: "待核对的原始快照"}
			}
			changed := d.NormalizeImageReferences()
			if state == "publishing" || state == "unknown" {
				if changed || d.Content != "保留正文" || d.Images[0].MarkdownRef != "" || d.Attempt.Body != "待核对的原始快照" {
					t.Fatal("pending draft was migrated")
				}
				return
			}
			want := "保留正文\n\n![旧图.png](#sw-comment-image/legacy)"
			if !changed || d.Content != want || d.Images[0].MarkdownRef != CommentImageMarkdownRef("legacy") || d.NormalizeImageReferences() {
				t.Fatalf("legacy migration not atomic/idempotent: %#v", d)
			}
		})
	}
}
