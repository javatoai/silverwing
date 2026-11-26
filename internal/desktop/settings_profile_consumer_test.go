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
	if !about.HasText("版本发布") || silverwingReleasesURL != want.releases {
		t.Fatalf("About release link was lost or uses another channel: %q", silverwingReleasesURL)
	}
	if len(backend.actions) != 0 || len(about.OpenedURLs()) != 0 {
		t.Fatal("copying the repository link performed an external action")
	}
}
