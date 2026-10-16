package integrations

import (
	"context"
	"silverwing-go/internal/domain"
	"strings"
	"testing"
)

func TestMaterialsHeaderCommentUsesActualRootAndStableMarker(t *testing.T) {
	payload := map[string]any{"targetUrl": "https://example.feishu.cn/wiki/ConfiguredTarget123", "directoryUrl": "https://example.feishu.cn/wiki/ActualTaskRoot123", "writeRoot": t.TempDir(), "requirementId": "1001", "requirementTitle": "1001-fixture", "subdirectory": "materials", "item": map[string]any{"projectKey": "fixture-project", "projectName": "fixture", "type": "userstory", "id": "1001", "url": "https://project.feishu.cn/fixture/userstory/detail/1001"}}
	source, err := materialsCommentContent(context.Background(), &Service{}, domain.AppConfig{}, payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(source, "飞书资料根目录：[https://example.feishu.cn/wiki/ActualTaskRoot123]") || strings.Contains(source, "ConfiguredTarget123") {
		t.Fatalf("wrong root link %q", source)
	}
	repeated, err := materialsCommentContent(context.Background(), &Service{}, domain.AppConfig{}, payload)
	if err != nil || source != repeated {
		t.Fatal("comment retry changed stable content/marker")
	}
	payload["directoryUrl"] = "https://another.feishu.cn/wiki/OtherRoot123"
	if _, err := materialsCommentContent(context.Background(), &Service{}, domain.AppConfig{}, payload); err == nil {
		t.Fatal("cross target root accepted")
	}
}
