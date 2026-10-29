package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/productprofile"
)

// Exercise the rendered views and copy actions against the pre-refactor values,
// rather than comparing profile constants with other aliases of those constants.
func TestSettingsProfileConsumersPreserveNamesAndReleaseLinks(t *testing.T) {
	want, ok := map[string]struct{ name, releases string }{
		"":        {"SilverWing", "https://github.com/javatoai/silverwing/releases"},
		"qa":      {"silverwing-qa", "https://github.com/javatoai/silverwing/releases?q=qa-v&expanded=true"},
		"product": {"silverwing-product", "https://github.com/javatoai/silverwing/releases?q=product-v&expanded=true"},
	}[productprofile.Edition]
	if !ok {
		t.Fatalf("unexpected build edition %q", productprofile.Edition)
	}
	const repository = "https://github.com/javatoai/silverwing"
	backend := &settingsSystemBackend{actions: make(chan application.Action, 2)}
	state := &app{backend: backend}
	about := settingsSystemPageTester(state, 640, false, func(context *ui.Context, theme palette) {
		state.settingsAbout(context, theme)
	})
	if !about.HasText(want.name) || !about.HasText(repository) {
		t.Fatalf("About changed its displayed identity: %v", about.Texts())
	}
	if err := about.Click("复制地址"); err != nil {
		t.Fatal(err)
	}
	if backend.clipboard != repository {
		t.Fatalf("repository copy = %q, want %q", backend.clipboard, repository)
	}
	updates := settingsSystemPageTester(state, 640, false, func(context *ui.Context, theme palette) {
		state.settingsUpdates(context, theme)
	})
	if !updates.HasText(want.releases) {
		t.Fatalf("Updates showed another channel: %v", updates.Texts())
	}
	if err := updates.Click("复制链接"); err != nil {
		t.Fatal(err)
	}
	if backend.clipboard != want.releases {
		t.Fatalf("release link copy = %q, want %q", backend.clipboard, want.releases)
	}
	if len(backend.actions) != 0 || len(about.OpenedURLs()) != 0 || len(updates.OpenedURLs()) != 0 {
		t.Fatal("copying profile links performed an external action")
	}
}
