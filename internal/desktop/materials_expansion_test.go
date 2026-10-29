package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func materialsExpansionFixture(t *testing.T) *app {
	s := readingObservationFixture(t)
	s.desktopPreferences = &desktopPreferencesState{enabled: true, value: DefaultDesktopPreferences(), store: NewDesktopPreferencesStore(filepath.Join(t.TempDir(), "prefs.json"))}
	root := s.taskUnit.currentTask().materialsPath
	s.materialsUnit.state.documents = []application.Document{{Name: "one", Path: filepath.Join(root, "one"), Kind: "directory", Children: []application.Document{{Name: "two", Path: filepath.Join(root, "one", "two"), Kind: "directory", Children: []application.Document{{Name: "three", Path: filepath.Join(root, "one", "two", "three"), Kind: "directory"}}}}}}
	return s
}

func TestMaterialsExpansionLegacyDefaultsAndPersistence(t *testing.T) {
	for _, old := range []string{`{"expandRequirementMaterialsSecondLevelFolders":true,"unknown":42}`, `{"expandRequirementMaterialsSecondLevelFolders":false,"unknown":42}`, `{}`} {
		path := filepath.Join(t.TempDir(), "prefs.json")
		if err := os.WriteFile(path, []byte(old), 0600); err != nil {
			t.Fatal(err)
		}
		store := NewDesktopPreferencesStore(path)
		value, err := store.Load()
		if err != nil || value.MaterialsExpansionDepth != 2 || value.MaterialsExpansionRevision != 1 {
			t.Fatalf("%+v %v", value, err)
		}
		data, _ := os.ReadFile(path)
		var raw map[string]any
		_ = json.Unmarshal(data, &raw)
		if raw["materialsExpansionDepth"] != float64(2) || raw["expandRequirementMaterialsSecondLevelFolders"] != nil {
			t.Fatal(string(data))
		}
	}
}

func TestMaterialsExpansionChangeResetsAndManualStateSurvivesRefresh(t *testing.T) {
	s := materialsExpansionFixture(t)
	s.initializeMaterialsTreeExpansion()
	if got := s.currentReadingMaterials(false).ExpandedDirectories; !slices.Equal(got, []string{"one", "one/two"}) {
		t.Fatal(got)
	}
	s.setReadingDirectoryExpanded("one", false)
	s.initializeMaterialsTreeExpansion()
	if s.readingExpandedDirectories()["one"] {
		t.Fatal("refresh replaced manual collapse")
	}
	path, content, dirty := s.materialsUnit.state.documentPath, s.materialsUnit.state.documentContent, s.materialsUnit.state.documentDirty
	s.saveMaterialsExpansionDepth("3")
	material := s.currentReadingMaterials(false)
	if !slices.Equal(material.ExpandedDirectories, []string{"one", "one/two", "one/two/three"}) || material.ExpansionRevision != 2 {
		t.Fatalf("%+v", material)
	}
	if s.materialsUnit.state.documentPath != path || s.materialsUnit.state.documentContent != content || s.materialsUnit.state.documentDirty != dirty {
		t.Fatal("expansion changed the document")
	}
	s.saveMaterialsExpansionDepth("0")
	if len(s.currentReadingMaterials(false).ExpandedDirectories) != 0 {
		t.Fatal("zero depth did not collapse all")
	}
	encoded, err := json.Marshal(*s.currentReadingMaterials(false))
	if err != nil {
		t.Fatal(err)
	}
	var restored MaterialsSnapshot
	if err = json.Unmarshal(encoded, &restored); err != nil || restored.ExpansionRevision != 3 {
		t.Fatalf("reading revision not persisted: %+v %v", restored, err)
	}
	preferences, err := s.desktopPreferences.store.Load()
	if err != nil || preferences.MaterialsExpansionDepth != 0 || preferences.MaterialsExpansionRevision != 3 {
		t.Fatalf("%+v %v", preferences, err)
	}
}

func TestMaterialsExpansionInvalidInputAndSaveFailureRetainState(t *testing.T) {
	s := materialsExpansionFixture(t)
	s.initializeMaterialsTreeExpansion()
	before := slices.Clone(s.currentReadingMaterials(false).ExpandedDirectories)
	for _, text := range []string{"", "-1", "1.5", "abc", "9999999999999999999999999999"} {
		s.saveMaterialsExpansionDepth(text)
		if s.lastError == "" || s.materialsExpansionDepth() != 2 || !slices.Equal(before, s.currentReadingMaterials(false).ExpandedDirectories) {
			t.Fatal(text)
		}
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	s.desktopPreferences.store = NewDesktopPreferencesStore(filepath.Join(blocker, "prefs.json"))
	s.saveMaterialsExpansionDepth("1")
	if s.materialsExpansionDepth() != 2 || s.materialsExpansionRevision() != 1 || !slices.Equal(before, s.currentReadingMaterials(false).ExpandedDirectories) {
		t.Fatal("save failure changed expansion")
	}
}

func TestMaterialsExpansionChangedSettingAppliesToOtherTaskOnReturn(t *testing.T) {
	s := materialsExpansionFixture(t)
	s.initializeMaterialsTreeExpansion()
	first := s.materialsUnit.state.documents
	s.taskUnit.state.selected = 1
	s.taskUnit.state.tasks[1].materialsPath = filepath.Join(s.taskUnit.state.tasks[1].path, "materials")
	other := []application.Document{{Name: "other", Kind: "directory", Path: filepath.Join(s.taskUnit.state.tasks[1].materialsPath, "other")}}
	s.materialsUnit.state.documents = other
	s.initializeMaterialsTreeExpansion()
	s.setReadingDirectoryExpanded("other", false)
	s.taskUnit.state.selected = 0
	s.materialsUnit.state.documents = first
	s.saveMaterialsExpansionDepth("1")
	s.taskUnit.state.selected = 1
	s.materialsUnit.state.documents = other
	s.initializeMaterialsTreeExpansion()
	if !s.readingExpandedDirectories()["other"] {
		t.Fatal("previous task expansion revision was retained")
	}
	s.setReadingDirectoryExpanded("other", false)
	s.initializeMaterialsTreeExpansion()
	if s.readingExpandedDirectories()["other"] {
		t.Fatal("manual state did not survive same-record refresh")
	}
}

func TestMaterialsSettingsRefinementLayout(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, _, _ := materialsGitDesktopFixture(t)
				s.materialsUnit.state.materialsGitCapture = true
				s.desktopPreferences = &desktopPreferencesState{value: DefaultDesktopPreferences()}
				cfg, _ := s.materialsGitConfig()
				cfg.MaterialsGitSync.Branch = "docs/" + strings.Repeat("long-branch-", 5) + "team"
				data, _ := json.Marshal(cfg)
				s.settingsUnit.state.configJSON = string(data)
				scope := application.MaterialsGitScope(s.snapshot.Home, cfg)
				s.materialsUnit.state.materialsGit.scope = scope
				s.materialsUnit.state.materialsGitBranches = &materialsGitBranchesState{scope: scope, loaded: true, branches: []string{cfg.MaterialsGitSync.Branch, "master"}}
				tt := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					ui.Scroll(c).Fill().Padding(24).Gap(14).Background(p.bg).Children(func() { s.settingsPaths(c, p) })
				}, width, 1000)
				for _, text := range []string{"Git 自动同步", "Git 仓库地址", "选择同步分支", "同步周期（分钟）", "默认展开目录层级"} {
					assertSettingsVisibleBounds(t, tt, text, width, 1000)
				}
				for _, text := range []string{"飞书同步目标", "默认展开二级目录文件夹", "Lark CLI"} {
					if tt.HasText(text) {
						t.Fatalf("retired setting visible: %s", text)
					}
				}
				captureDesktopTestFrame(t, tt, fmt.Sprintf("materials-refinement-%d-dark-%t", width, dark))
				if err := tt.Click("选择同步分支"); err != nil {
					t.Fatal(err)
				}
				if !tt.HasText("master") {
					t.Fatal("native branch popup did not open")
				}
				captureDesktopTestFrame(t, tt, fmt.Sprintf("materials-branches-popup-%d-dark-%t", width, dark))
				tt.Key(0, ui.KeyEscape)
				if tt.HasText("master") {
					t.Fatal("Esc did not dismiss the branch popup")
				}
			})
		}
	}
}
