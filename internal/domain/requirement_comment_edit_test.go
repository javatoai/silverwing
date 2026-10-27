package domain

import (
	"strings"
	"testing"
)

func TestCommentEditSourceAndResources(t *testing.T) {
	old := "https://project.feishu.cn/goapi/v5/platform/file/stream/download/old-token"
	content := "前 **文**\r\n\r\n![" + old + "](<" + old + "> \"标题 " + old + "\")<!--old-token-->\r\n\r\n中间\r\n\r\n![第二张][img]\r\n\r\n[img]: " + old + "\r\n\r\n`![示例](" + old + ")`\r\n\r\n```md\n![代码](" + old + ")\n```"
	original := RequirementComment{ID: "id", Content: content}
	d := RequirementCommentDraft{Content: content, Edit: &CommentEditTarget{CommentID: "id", Original: &original}, Images: []CommentDraftAsset{{ID: "existing", OriginalURL: old}, {ID: "new", Name: "新增"}}}
	body := d.Markdown(func(a CommentDraftAsset) (string, string) {
		return "https://project.feishu.cn/files/" + a.ID, "fresh-" + a.ID
	})
	if !strings.Contains(body, "中间\r\n\r\n![第二张](https://project.feishu.cn/files/existing)") || !strings.Contains(body, "标题 "+old) || strings.Count(body, "<!--fresh-existing-->") != 2 || strings.Contains(body, "<!--old-token-->") {
		t.Fatalf("image positions, titles, reference or tokens corrupted: %q", body)
	}
	if !strings.Contains(body, "`![示例]("+old+")`") || !strings.Contains(body, "![代码]("+old+")") || !strings.HasSuffix(body, "![新增](https://project.feishu.cn/files/new)<!--fresh-new-->") {
		t.Fatal("code was rewritten or new image was not appended")
	}
	if d.Content != content || d.Edit.Original.Content != content {
		t.Fatal("render rewrote draft or baseline")
	}
	d.Images = d.Images[:1]
	if d.HasChanges() {
		t.Fatal("unchanged edit is saveable")
	}
	d.Content = ""
	if d.HasContent() || len(d.ActiveImages()) != 0 {
		t.Fatal("removed inline images count as content")
	}
}
