package desktop

import (
	"net/url"
	"path/filepath"
	markdownui "silverwing-go/internal/ui/markdown"
	"testing"
)

func TestMarkdownResourceReferencesRoundTripSpecialFileNames(t *testing.T) {
	root := t.TempDir()
	doc := filepath.Join(root, "docs", "日常#100%.md")
	for _, name := range []string{"photo#1.png", "100%.png", "中文 空格.png", "a&copy;.png"} {
		relative := filepath.ToSlash(filepath.Join("docs", "日常#100%.assets", name))
		target, err := markdownResourceReference(doc, root, relative)
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(target)
		if err != nil || u.Fragment != "" || u.RawQuery != "" {
			t.Fatalf("file characters became URI syntax: %q %v", target, err)
		}
		resolved, err := markdownui.ResolveLink(doc, target)
		want := filepath.Join(root, filepath.FromSlash(relative))
		if err != nil || resolved.Path != want {
			t.Fatalf("reference does not identify copied file: %q -> %+v %v; want %q", target, resolved, err, want)
		}
	}
}
