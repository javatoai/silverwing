package desktop

import "testing"

func TestTaskTagNavigationPlacement(t *testing.T) {
	if taskTagTab != len(tabs)-1 || tabs[taskTagTab] != "Tag" || tabs[taskTagTab-1] != "需求说明" {
		t.Fatalf("Tag must follow requirement notes: %v", tabs)
	}
	for _, config := range []string{
		`{"tagEnabled":true,"groups":[{"tagEnabled":true}]}`,
		`{"tagEnabled":false,"groups":[{"tagEnabled":false}]}`,
	} {
		s := &app{
			settingsUnit: settingsController{state: settingsState{configJSON: config}},
		}
		for _, item := range s.sidebarNavigationItems() {
			if item.page == "tags" {
				t.Fatal("Tag remains in the sidebar")
			}
		}
	}
	if validPage("tags") || !validPage("tasks") || !validPage("archive") {
		t.Fatal("Tag must use the existing task host")
	}
	s := &app{page: "tags"}
	s.reconcileSidebarNavigation()
	if s.page != "tasks" || s.tab != taskTagTab {
		t.Fatalf("invalid Tag route reconciliation: %s/%d", s.page, s.tab)
	}
}

func TestTaskTagNavigationReadingState(t *testing.T) {
	view := readingViewForTab(taskTagTab)
	if view != "TAG" {
		t.Fatalf("Tag view saved as %q", view)
	}
	tab, ok := readingTabForView(view)
	if !ok || tab != taskTagTab {
		t.Fatalf("Tag view restored as %d, %v", tab, ok)
	}
	bounded := (ReadingSnapshot{Schema: 1, View: view}).Bounded()
	if bounded.View != view {
		t.Fatalf("Tag view rejected by reading state: %q", bounded.View)
	}
}
