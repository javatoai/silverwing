package desktop

import "testing"

func TestPublicEditionLegacyNavigationFallsBackToTaskDetails(t *testing.T) {
	for _, page := range []string{"tags", "production"} {
		s := newApp(nil, Options{CapturePath: "public-boundary"})
		s.page, s.loading, s.tab = page, false, 4
		s.reconcileSidebarNavigation()
		if s.page != "tasks" || s.tab != 0 {
			t.Fatalf("%s did not fall back: %s/%d", page, s.page, s.tab)
		}
		for _, item := range s.sidebarNavigationItems() {
			if item.page == "tags" || item.page == "production" {
				t.Fatalf("internal sidebar entry: %s", item.page)
			}
		}
	}
	if tab, ok := readingTabForView("TAG"); !ok || tab != 0 {
		t.Fatalf("legacy reading view did not fall back: %d/%t", tab, ok)
	}
}
