package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"silverwing-go/internal/documents"
)

func taskTabsPreferencesFixture(t *testing.T, s *app) string {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, desktopPreferencesFile)
	store := NewDesktopPreferencesStore(path)
	if err := store.Save(DefaultDesktopPreferences()); err != nil {
		t.Fatal(err)
	}
	s.desktopPreferences = &desktopPreferencesState{enabled: true, home: home, store: store, value: DefaultDesktopPreferences()}
	return path
}

func TestTaskTabsDefaultAndLastEnabledPersistence(t *testing.T) {
	s := newApp(nil, Options{Page: "settings"})
	path := taskTabsPreferencesFixture(t, s)
	if s.enabledTaskContentTabCount() != 8 || !slices.Equal(s.visibleTaskContentTabs(), []int{0, 1, 2, 3, 4, 5, 6, 7}) {
		t.Fatal("old preferences must show all tabs in fixed order")
	}
	for index := 0; index < taskTerminalTab; index++ {
		if !s.setTaskContentTabEnabled(index, false) {
			t.Fatalf("hide %d: %s", index, s.lastError)
		}
	}
	before, _ := os.ReadFile(path)
	if s.tab != taskTerminalTab || s.setTaskContentTabEnabled(taskTerminalTab, false) || s.enabledTaskContentTabCount() != 1 {
		t.Fatal("last enabled tab was closed", s.lastError)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("rejected close changed preferences")
	}
	if s.taskTerminalUnit.current != nil || len(s.taskTerminalUnit.owners) != 0 || s.materialsUnit.state.documentsRequested {
		t.Fatal("settings navigation started task work")
	}
	restarted := newApp(nil, Options{Page: "tasks"})
	restarted.restoreDesktopPreferences(filepath.Dir(path))
	if restarted.tab != taskTerminalTab || restarted.enabledTaskContentTabCount() != 1 {
		t.Fatal("restart did not restore visibility and fallback", restarted.tab)
	}
	if !restarted.setTaskContentTabEnabled(taskTagTab, true) || restarted.tab != taskTerminalTab || !slices.Equal(restarted.visibleTaskContentTabs(), []int{4, 7}) {
		t.Fatal("enabling a tab reordered tabs or changed selection")
	}
}

func TestTaskTabsTemporaryNavigationAndRestore(t *testing.T) {
	s := newApp(nil, Options{Page: "tasks"})
	path := taskTabsPreferencesFixture(t, s)
	if !s.setTaskContentTabEnabled(taskTagTab, false) || !s.selectTaskContentTab(taskTagTab) || !slices.Contains(s.visibleTaskContentTabs(), taskTagTab) || s.taskContentTabEnabled(taskTagTab) {
		t.Fatal("explicit route did not reveal hidden tab temporarily")
	}
	if !s.selectTaskContentTab(0) || slices.Contains(s.visibleTaskContentTabs(), taskTagTab) {
		t.Fatal("leaving temporary tab changed its preference")
	}
	s.selectTaskContentTab(taskTagTab)
	s.page = "settings"
	s.reconcileTaskContentTabs()
	if s.tab != 0 || slices.Contains(s.visibleTaskContentTabs(), taskTagTab) {
		t.Fatal("leaving task page retained hidden entry")
	}
	s.page = "tasks"
	s.readingSession = newMemoryReadingSession(filepath.Dir(path))
	s.readingSession.loaded, s.readingSession.restoreLastTask = true, true
	s.readingSession.cache.View = "TAG"
	s.readingSession.startupIdentity = s.readingUIIdentity()
	s.loading = false
	s.restoreReadingSessionOnSnapshot()
	if s.tab != 0 {
		t.Fatal("startup restored hidden page")
	}
	s.afterReadingTaskSelection()
	if s.tab != 0 {
		t.Fatal("task selection restored hidden page")
	}
	for _, index := range []int{0, taskTagTab} {
		if !s.setTaskContentTabEnabled(index, false) { // TAG is already hidden.
			t.Fatal(s.lastError)
		}
	}
	explicit := newApp(nil, Options{Page: "tasks", Tab: 0, TabExplicit: true})
	explicit.restoreDesktopPreferences(filepath.Dir(path))
	if explicit.tab != 0 || !slices.Contains(explicit.visibleTaskContentTabs(), 0) || explicit.taskContentTabEnabled(0) {
		t.Fatal("explicit CLI tab zero was treated as a default route")
	}
}

func TestTaskTabsCaptureAndSaveFailureKeepSelectionAndContent(t *testing.T) {
	s, _, files := materialTextEditorFixture(t, map[string]string{"draft.txt": "draft"})
	state := bindMaterialTextTest(s, files["draft.txt"])
	s.tab, s.page = 1, "tasks"
	path := taskTabsPreferencesFixture(t, s)
	before, _ := os.ReadFile(path)
	state.view.Buffer.SetMaxSourceBytes(-1)
	source := strings.Repeat("x", documents.MaxMaterialsText+1)
	state.view.Buffer.Set(source)
	if s.setTaskContentTabEnabled(1, false) || s.tab != 1 || !s.taskContentTabEnabled(1) || state.view.Buffer.OriginalText() != source || s.lastError == "" {
		t.Fatal("capture failure hid page or discarded source")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("capture failure modified preferences")
	}
	s.tab = 0
	s.desktopPreferences.store = NewDesktopPreferencesStore(filepath.Join(path, "invalid-child"))
	if s.setTaskContentTabEnabled(0, false) || s.tab != 0 || !s.taskContentTabEnabled(0) {
		t.Fatal("preference failure committed a partial navigation")
	}
}

func TestTaskTabsHideTerminalRetainsSessionAndOutputOwner(t *testing.T) {
	s, launches := taskTerminalFixture(t)
	taskTabsPreferencesFixture(t, s)
	owner := s.ensureTaskTerminalsCurrent()
	awaitTaskTerminal(t, s, func() bool { return !owner.sessions[0].loading })
	process := (<-launches).process
	session := owner.sessions[0]
	if !s.setTaskContentTabEnabled(taskTerminalTab, false) || s.tab != 0 || process.closed.Load() != 0 || owner.sessions[0] != session {
		t.Fatal("hiding terminal closed its process")
	}
	if !s.setTaskContentTabEnabled(taskTerminalTab, true) || !s.selectTaskContentTab(taskTerminalTab) || s.ensureTaskTerminalsCurrent() != owner || owner.sessions[0] != session || len(launches) != 0 {
		t.Fatal("reenabling terminal recreated the session")
	}
}

func TestTaskTabsPreferenceNormalizationAndUnrelatedWrites(t *testing.T) {
	all := make([]string, len(tabs))
	for index := range tabs {
		all[index] = readingViewForTab(index)
	}
	for _, test := range []struct {
		name, raw string
		want      []string
	}{
		{"old", `{}`, nil},
		{"invalid", `{"hiddenTaskTabs":true}`, nil},
		{"mixed", `{"hiddenTaskTabs":["TAG","unknown","TAG","FILES"]}`, []string{"TAG", "FILES"}},
		{"all", `{"hiddenTaskTabs":["DETAIL","MATERIALS","REQUIREMENT","NOTES","TAG","FILES","SERVICES","TERMINAL"]}`, all[1:]},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), desktopPreferencesFile)
			if err := os.WriteFile(path, []byte(test.raw), 0600); err != nil {
				t.Fatal(err)
			}
			value, err := NewDesktopPreferencesStore(path).Load()
			if err != nil || !slices.Equal(value.HiddenTaskTabs, test.want) {
				t.Fatal(value.HiddenTaskTabs, err)
			}
		})
	}
	s := newApp(nil, Options{Page: "settings"})
	path := taskTabsPreferencesFixture(t, s)
	data, _ := os.ReadFile(path)
	var raw map[string]any
	json.Unmarshal(data, &raw)
	raw["futurePreference"] = map[string]any{"keep": true}
	data, _ = json.Marshal(raw)
	os.WriteFile(path, data, 0600)
	if !s.setTaskContentTabEnabled(taskTagTab, false) {
		t.Fatal(s.lastError)
	}
	if err := s.persistDesktopLayout(); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	json.Unmarshal(data, &raw)
	if raw["futurePreference"] == nil {
		t.Fatal("tab update removed another preference")
	}
	value, err := s.desktopPreferences.store.Load()
	if err != nil || !slices.Equal(value.HiddenTaskTabs, []string{"TAG"}) {
		t.Fatal("layout write overwrote hidden tabs", value, err)
	}
	section, ok := settingsSectionForPage("task-tabs")
	if !ok || section.name != "外观与交互" || !s.navigateSettingsPage("task-tabs", "") {
		t.Fatal("settings entry is unreachable")
	}
}
