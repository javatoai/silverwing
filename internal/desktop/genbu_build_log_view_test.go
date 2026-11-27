package desktop

import (
	"fmt"
	"strings"
	"testing"

	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

func TestGenbuBuildLogFollowAndReadingPosition(t *testing.T) {
	s, productionView, batch := productionFixture(t)
	batch.Target.GenbuService = "build-service"
	tag := production.Tag{Name: "2.3.44", Commit: batch.ReleaseSHA, Object: "original-tag-object"}
	batch.Tags = []production.Tag{tag}
	s.releaseUnit.state.production.batches[0] = batch
	s.openProductionBuildLog(batch, tag, func() *production.Batch { return s.productionBatch(productionView) })
	d := s.genbuBuildLog
	if d == nil || !d.followTail {
		t.Fatal("build log should follow its last line by default")
	}
	d.loaded, d.text, d.logError = true, genbuBuildLogViewSample(0, 160), ""
	tester := ui.NewTester(func(c *ui.Context) {
		s.renderGenbuReadDialogs(c, setTheme(c, false))
	}, 980, 650)
	tester.Frame()
	view := d.preview.view
	if view == nil {
		t.Fatal("build log preview is missing")
	}
	buffer := view.Buffer
	assertLastLineVisible := func() {
		t.Helper()
		// The synthetic output ends in CRLF, so the final editor row is empty.
		line := buffer.LineCount() - 2
		top, height, visible := view.LineGeometry(line)
		start, end := view.ScrollY(), view.ScrollY()+view.ViewportHeight()
		if !visible || top < start-1 || top+float64(height) > end+1 {
			t.Fatalf("last log line %d at %.1f–%.1f is outside %.1f–%.1f", line, top, top+float64(height), start, end)
		}
	}
	assertLastLineVisible()
	d.text = genbuBuildLogViewSample(0, 180)
	tester.Frame()
	assertLastLineVisible()

	const followLabel = "跟随最后一行"
	if err := tester.Click(followLabel); err != nil {
		t.Fatal(err)
	}
	if d.followTail {
		t.Fatal("unchecking did not stop following the log tail")
	}
	if err := tester.Click("查找"); err != nil {
		t.Fatal(err)
	}
	const query = "build step 0075"
	tester.Type(query)
	if !tester.HasText("Find") || view.SelectedText() != query {
		t.Fatalf("find bar did not select %q", query)
	}
	view.SetScrollY(view.LineTop(65) + 3)
	tester.Frame()
	readingLine := buffer.Line(view.LineAt(view.ScrollY()))

	d.text = genbuBuildLogViewSample(7, 187)
	tester.Frame()
	if d.preview.view != view || d.preview.view.Buffer != buffer {
		t.Fatal("refresh discarded the live editor or its buffer")
	}
	if got := buffer.OriginalText(); got != d.text {
		t.Fatal("rolling refresh changed or omitted build output")
	}
	if got := buffer.Line(view.LineAt(view.ScrollY())); got != readingLine {
		t.Fatalf("rolling refresh moved the reading position from %q to %q", readingLine, got)
	}
	if !tester.HasText("Find") || view.SelectedText() != query {
		t.Fatal("refresh discarded the open finder or its selected match")
	}

	tester.Key(0, ui.KeyEscape)
	if err := tester.Click(followLabel); err != nil {
		t.Fatal(err)
	}
	if !d.followTail {
		t.Fatal("checking did not resume following the log tail")
	}
	assertLastLineVisible()
}

func genbuBuildLogViewSample(start, end int) string {
	var log strings.Builder
	for line := start; line < end; line++ {
		fmt.Fprintf(&log, "build step %04d\r\n", line)
	}
	return log.String()
}
