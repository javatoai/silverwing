package desktop

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func captureDesktopTestFrame(t *testing.T, tester *ui.Tester, name string) {
	t.Helper()
	dir := os.Getenv("SILVERWING_DESKTOP_EVIDENCE_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	encodeErr := png.Encode(file, tester.Image())
	if closeErr := file.Close(); encodeErr != nil || closeErr != nil {
		t.Fatalf("capture: %v / %v", encodeErr, closeErr)
	}
}
