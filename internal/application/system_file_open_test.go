package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type fallbackFileOpener struct {
	opened, chosen int
	path           string
	outcome        systemFileOpenOutcome
	choice         systemFileOpenOutcome
	err            error
}

func (f *fallbackFileOpener) status(string) (FileOpeningStatus, error) {
	return FileOpeningStatus{}, nil
}
func (f *fallbackFileOpener) open(_ context.Context, path string) (systemFileOpenOutcome, error) {
	f.opened++
	f.path = path
	return f.outcome, f.err
}
func (f *fallbackFileOpener) choose(path string) (systemFileOpenOutcome, error) {
	f.chosen++
	f.path = path
	return f.choice, nil
}

func TestSystemFileOpenDefaultAndChooserFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "中文 资料.unknown")
	if err := os.WriteFile(path, []byte{0, 1}, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		outcome systemFileOpenOutcome
		failure error
		choose  int
		status  string
		choice  systemFileOpenOutcome
	}{
		{"default", systemFileOpenSubmitted, nil, 0, "SUBMITTED", 0},
		{"no-association-choose", systemFileOpenNeedsChooser, nil, 1, "SUBMITTED", systemFileOpenSubmitted},
		{"no-association-cancel", systemFileOpenNeedsChooser, nil, 1, "CANCELLED", systemFileOpenCancelled},
		{"cancel", systemFileOpenCancelled, nil, 0, "CANCELLED", 0},
		{"failure", 0, errors.New("launcher unavailable"), 0, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fallbackFileOpener{outcome: tc.outcome, err: tc.failure, choice: tc.choice}
			result, err := runSystemFileOpening(context.Background(), "system.open", path, fake)
			if !errors.Is(err, tc.failure) || fake.opened != 1 || fake.chosen != tc.choose || fake.path != path {
				t.Fatalf("result=%+v err=%v fake=%+v", result, err, fake)
			}
			if err == nil && result.Data.(FileOpeningResult).Status != tc.status {
				t.Fatalf("unexpected outcome: %+v", result)
			}
		})
	}
	fake := &fallbackFileOpener{}
	if _, err := runSystemFileOpening(context.Background(), "system.open", path+"-deleted", fake); err == nil || fake.opened != 0 {
		t.Fatal("missing file reached launcher")
	}
}

func TestMaterialsExternalOpenRejectsStaleAndOutsideTargets(t *testing.T) {
	a, root, taskPath := materialTextApplicationFixture(t)
	inside := filepath.Join(root, "资料.unknown")
	outside := filepath.Join(t.TempDir(), "资料.unknown")
	for _, path := range []string{inside, outside} {
		if err := os.WriteFile(path, []byte{0, 1}, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, path string
		payload    map[string]any
	}{
		{"outside", outside, nil},
		{"deleted", inside + "-deleted", nil},
		{"task", inside, map[string]any{"draftTaskPath": taskPath + "-replaced"}},
		{"root", inside, map[string]any{"draftMaterialsRoot": root + "-changed"}},
		{"home", inside, map[string]any{"materialsHome": a.Paths.Home + "-changed"}},
		{"identity", inside, map[string]any{"desktopRenameTaskCreatedAt": "replaced"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := a.documentAction(context.Background(), Action{Kind: "document.open", TaskID: "task", Path: tc.path, Payload: tc.payload}); err == nil {
				t.Fatal("unsafe target reached system opener")
			}
		})
	}
}
