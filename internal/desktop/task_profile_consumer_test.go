package desktop

import (
	"testing"

	"silverwing-go/internal/productprofile"
)

func TestTaskCreationProfileConsumerPreservesLegacyTitle(t *testing.T) {
	want, ok := map[string]string{"qa": "创建测试任务", "product": "创建产品任务"}[productprofile.Edition]
	if !ok {
		t.Fatalf("restricted task creation consumer used in edition %q", productprofile.Edition)
	}
	_, _, tester := releaseReviewTemplateFixture(t)
	if !tester.HasText(want) {
		t.Fatalf("task creation title changed: want %q, got %v", want, tester.Texts())
	}
	if _, found := tester.Find(want); !found {
		t.Fatalf("task creation accessibility label lost %q", want)
	}
	if !tester.HasText("创建任务") {
		t.Fatal("the shared submit action label changed")
	}
}
