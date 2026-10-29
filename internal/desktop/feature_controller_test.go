package desktop

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"silverwing-go/internal/editor"
)

type featureSelectionInputProbe struct {
	events    []string
	textError error
}

func (p *featureSelectionInputProbe) captureMaterialTextInput() error {
	p.events = append(p.events, "text")
	return p.textError
}
func (p *featureSelectionInputProbe) captureMarkdownEditorInput(*markdownEditorState) error {
	p.events = append(p.events, "markdown")
	return nil
}
func (p *featureSelectionInputProbe) syncMarkdownEditor() { p.events = append(p.events, "sync") }

func TestFeatureControllerSelectionCaptureFailurePreservesPresentation(t *testing.T) {
	blocked := errors.New("composition still active")
	unit := materialsController{state: materialsState{documentPath: "old.md", documentContent: "draft", documentDirty: true}}
	probe := &featureSelectionInputProbe{textError: blocked}
	err := unit.prepareTaskSelection(TaskTransition{origin: taskTransitionUser, indexChanged: true}, probe)
	if !errors.Is(err, blocked) || !reflect.DeepEqual(probe.events, []string{"text"}) {
		t.Fatalf("capture crossed failed input boundary: events=%v err=%v", probe.events, err)
	}
	if unit.state.documentPath != "old.md" || unit.state.documentContent != "draft" || !unit.state.documentDirty {
		t.Fatal("rejected capture changed the displayed draft")
	}
}

func TestFeatureControllerSnapshotSelectionSyncsOldEditor(t *testing.T) {
	var unit materialsController
	probe := &featureSelectionInputProbe{}
	if err := unit.prepareTaskSelection(TaskTransition{origin: taskTransitionSnapshot}, probe); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(probe.events, []string{"sync"}) {
		t.Fatalf("snapshot input protocol=%v", probe.events)
	}
}

func TestFeatureControllerPresentationResetPreservesSaveAndWorkerOwnership(t *testing.T) {
	engine := &editor.Autosave{}
	worker := &materialsFileJobState{}
	textEditor := &materialTextEditorState{}
	unit := materialsController{state: materialsState{
		documentPath: "old.md", documentContent: "draft", documentDirty: true, documentsLoaded: true,
		documentTabs: []string{"old.md"}, documentDrafts: map[string]string{"old.md": "draft"},
		documentDirtyMap: map[string]bool{"old.md": true}, documentSaveRefs: map[string]documentSaveRef{"lease": {path: "old.md"}},
		markdownAutosave: engine, materialsFileJobs: worker, materialTextActive: textEditor,
	}}
	unit.resetPresentedMaterials()
	if unit.state.documentPath != "" || unit.state.documentContent != "" || unit.state.documentDirty || unit.state.documentsLoaded || len(unit.state.documentTabs) != 0 {
		t.Fatal("selected-task presentation was retained")
	}
	if unit.state.markdownAutosave != engine || unit.state.materialsFileJobs != worker || unit.state.materialTextActive != textEditor || unit.state.documentSaveRefs["lease"].path != "old.md" {
		t.Fatal("presentation reset took ownership of an independent save or worker")
	}
	unit.state.documentDrafts["next.md"] = "next"
	unit.state.documentDirtyMap["next.md"] = true
}

func TestFeatureControllerSnapshotSelectionTracksIncarnationBeforeIndex(t *testing.T) {
	a, b, c := task{id: "a", createdAt: "1"}, task{id: "b", createdAt: "1"}, task{id: "c", createdAt: "1"}
	unit := taskController{state: taskState{tasks: []task{a, b, c}, selected: 1}}
	if index := unit.snapshotSelectionIndex([]task{c, a, b}, false, []int{0, 1, 2}, []int{0, 1, 2}); index != 2 {
		t.Fatalf("stable incarnation moved to %d, want 2", index)
	}
	replacement := b
	replacement.createdAt = "2"
	if index := unit.snapshotSelectionIndex([]task{c, a, replacement}, false, []int{0, 1, 2}, []int{0, 1, 2}); index != 0 {
		t.Fatalf("replacement inherited selection: %d", index)
	}
	unit.selectIndex(-1)
	if unit.currentTask().id != "" || unit.restoredTaskPresent("missing") {
		t.Fatal("zero/empty selection did not remain empty")
	}
}

func TestFeatureCleanupDrainsEveryOwnerAfterFirstFailure(t *testing.T) {
	failure := errors.New("first owner failed")
	var previous context.Context
	events := []string{}
	err := runFeatureCleanup(
		featureCleanupStep{"first", time.Second, func(ctx context.Context) error { previous = ctx; events = append(events, "first"); return failure }},
		featureCleanupStep{"second", time.Second, func(ctx context.Context) error {
			if previous.Err() != context.Canceled || ctx.Err() != nil {
				t.Fatal("owners shared a canceled cleanup context")
			}
			events = append(events, "second")
			return nil
		}},
	)
	if !errors.Is(err, failure) || !reflect.DeepEqual(events, []string{"first", "second"}) {
		t.Fatalf("cleanup events=%v err=%v", events, err)
	}
}
