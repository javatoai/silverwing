package markdownui

import "testing"

func TestCaptureInputKeepsUnconfirmedPropertyTag(t *testing.T) {
	state, _ := dailyPublicEditor("---\ntags: [original]\n---\n\nbody\n")
	defer state.Close()
	dialog := &propertyTagDialog{open: true, value: "edited", originalValue: "original", change: TagChange{Action: RenameTag, Index: 0, DocumentID: state.document.ID, Version: state.Snapshot().Version}}
	state.reader.properties.dialog = dialog
	before := state.Snapshot().Source
	if _, err := state.CaptureInput(); err == nil {
		t.Fatal("unconfirmed property edit escaped capture guard")
	}
	if state.Snapshot().Source != before || state.reader.properties.dialog != dialog || dialog.value != "edited" {
		t.Fatal("capture discarded property draft")
	}
	if err := state.SetMode(Preview); err == nil || state.Mode() != Live {
		t.Fatal("mode switch discarded property draft")
	}
	if err := state.Reload("replacement", state.Snapshot().Version); err == nil || state.Snapshot().Source != before {
		t.Fatal("external read discarded property draft")
	}
	dialog.value = dialog.originalValue
	if _, err := state.CaptureInput(); err != nil {
		t.Fatalf("unchanged property popup blocked capture: %v", err)
	}
}

func TestPendingPropertyTagCannotBeOrphanedByOuterDelete(t *testing.T) {
	for _, route := range []string{"x", "menu"} {
		t.Run(route, func(t *testing.T) {
			state, view := dailyPublicEditor("---\ntags: [original]\n---\n\nbody\n")
			defer state.Close()
			before := state.Snapshot().Source
			if err := view.Click("标签：original"); err != nil {
				t.Fatal(err)
			}
			if err := view.Click("标签名称"); err != nil {
				t.Fatal(err)
			}
			view.Command("selectAll")
			view.Type("edited")
			if route == "x" {
				if err := view.Click("删除标签：original"); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := view.RightClick("标签：original"); err != nil {
					t.Fatal(err)
				}
				if err := view.ChooseMenuItem("删除标签"); err != nil {
					t.Fatal(err)
				}
			}
			if state.Snapshot().Source != before || state.reader.properties.dialog == nil || state.reader.properties.dialog.value != "edited" {
				t.Fatal("outer delete orphaned or discarded pending property input")
			}
			if _, ok := view.Find("标签名称"); !ok {
				t.Fatal("pending property popup became unreachable")
			}
			if err := view.Click("取消"); err != nil {
				t.Fatal(err)
			}
			if _, err := state.CaptureInput(); err != nil {
				t.Fatalf("explicit cancel did not unblock capture: %v", err)
			}
		})
	}
}
