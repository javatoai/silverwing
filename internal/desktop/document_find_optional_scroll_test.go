package desktop

import "testing"

func TestDocumentFindNavigationWithoutReadingSession(t *testing.T) {
	state := &documentFindState{}
	state.set("fixture", "needle", []documentFindBlock{{key: "one", text: "needle", scrollY: 20}})
	s := &app{
		materialsUnit: materialsController{state: materialsState{documentFindOpen: true,
			documentFind: state}},
	}
	s.applyDocumentFindNavigation("markdown-native")
	if len(state.hits) != 1 {
		t.Fatal("missing optional persisted scroll state must not discard find results")
	}
}
