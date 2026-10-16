package desktop

import (
	"errors"
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/storage"
)

func settingsBackupDeleteFixture(t *testing.T) *app {
	t.Helper()
	s := &app{page: "settings", settingsPage: "config-backup", settingsTab: "存储", selected: -1,
		inputs: map[string]*string{}, snapshot: application.Snapshot{Home: t.TempDir()}, configJSON: "fixture-config"}
	s.applySettingsConfigBackups([]storage.Backup{
		{ID: "a", Path: filepath.Join(s.snapshot.Home, "backups", "silverwing-config-1791790393123.zip")},
		{ID: "b", Path: filepath.Join(s.snapshot.Home, "backups", "silverwing-config-"+strings.Repeat("very-long-name-", 10)+".zip")},
	})
	return s
}

func settingsBackupDeleteTester(s *app, width int, dark bool) *ui.Tester {
	s.dark = dark
	return ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, dark)
		ui.Column(c).Fill().Padding(12).Background(p.bg).Children(func() { s.settingsConfigBackup(c, p) })
	}, width, 650)
}

func TestSettingsBackupDeleteCompactSelectionLayout(t *testing.T) {
	for _, width := range []int{960, 480, 360} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s := settingsBackupDeleteFixture(t)
				tester := settingsBackupDeleteTester(s, width, dark)
				if err := tester.Click("删除所选"); err != nil {
					t.Fatal(err)
				}
				if len(s.ensureSettingsConfigRecovery().jobs.current) != 0 {
					t.Fatal("empty selection dispatched deletion")
				}
				name := filepath.Base(mapString(s.settingsBackups[0], "path"))
				if err := tester.Click("选择备份 " + name); err != nil {
					t.Fatal(err)
				}
				if !s.settingsConfigRecovery.backupSelected["a"] || !tester.HasText("已选 1 个") {
					t.Fatal("single selection did not update count")
				}
				if err := tester.Click("恢复"); err != nil {
					t.Fatal(err)
				}
				if len(s.settingsConfigRecovery.backupSelected) != 1 || len(s.settingsConfigRecovery.jobs.current) != 0 {
					t.Fatal("restore changed selection or dispatched deletion")
				}
				if err := tester.Click("全选"); err != nil {
					t.Fatal(err)
				}
				if len(s.settingsConfigRecovery.backupSelected) != 2 || !tester.HasText("已选 2 个") {
					t.Fatal("select all did not update count")
				}
				check, _ := tester.Find("全选")
				count, _ := tester.Find("已选 2 个")
				remove, _ := tester.Find("删除所选")
				if math.Abs(float64(check.Y-count.Y)) > 8 || math.Abs(float64(remove.Y-check.Y)) > 8 || remove.X+remove.W > float32(width-12) {
					t.Fatalf("selection toolbar clipped or split: %+v %+v %+v", check, count, remove)
				}
				for _, backup := range s.settingsBackups {
					name := filepath.Base(mapString(backup, "path"))
					box, ok := tester.Find(name)
					if !ok || box.X+box.W > float32(width-70) || box.H > 24 {
						t.Fatalf("backup name overflow: %+v", box)
					}
				}
				if directory := os.Getenv("SILVERWING_BACKUP_EVIDENCE_DIR"); directory != "" {
					if err := os.MkdirAll(directory, 0o700); err != nil {
						t.Fatal(err)
					}
					file, err := os.Create(filepath.Join(directory, fmt.Sprintf("backups-%d-dark-%t.png", width, dark)))
					if err != nil {
						t.Fatal(err)
					}
					encodeErr := png.Encode(file, tester.Image())
					closeErr := file.Close()
					if encodeErr != nil || closeErr != nil {
						t.Fatalf("capture: %v / %v", encodeErr, closeErr)
					}
				}
				if err := tester.Click("全选"); err != nil {
					t.Fatal(err)
				}
				if len(s.settingsConfigRecovery.backupSelected) != 0 {
					t.Fatal("clear all retained selected IDs")
				}
			})
		}
	}
}

func TestSettingsBackupDeleteImmutablePlanBusyAndLateResults(t *testing.T) {
	s := settingsBackupDeleteFixture(t)
	state := s.settingsConfigRecovery
	state.backupSelected = map[string]bool{"a": true}
	plan, valid := s.settingsSelectedBackupPlan()
	if !valid || !reflect.DeepEqual(plan.ids, []string{"a"}) {
		t.Fatal("selection snapshot missing")
	}
	state.backupSelected = map[string]bool{"b": true}
	if !reflect.DeepEqual(plan.ids, []string{"a"}) {
		t.Fatal("selection snapshot followed live selection")
	}
	// A canceled confirmation has no continuation and keeps the current selection.
	before := append([]map[string]any(nil), s.settingsBackups...)
	s.finishSettingsBackupDeleteConfirmation(plan, 1)
	if !reflect.DeepEqual(before, s.settingsBackups) || !state.backupSelected["b"] || len(state.jobs.current) != 0 {
		t.Fatal("unconfirmed delete changed data")
	}
	request, err := s.beginSettingsConfigRead(application.Action{Kind: "config.delete-backups", Payload: map[string]any{"confirmed": true, "ids": plan.ids}})
	if err != nil || !state.backupDeleteLoading {
		t.Fatal("delete request did not start", err)
	}
	plan.ids[0] = "mutated"
	if !reflect.DeepEqual(request.action.Payload["ids"], []string{"a"}) {
		t.Fatal("worker payload shares mutable confirmation slice")
	}
	if _, err := s.beginSettingsConfigRead(application.Action{Kind: "config.delete-backups"}); err == nil {
		t.Fatal("duplicate delete was accepted")
	}
	if _, err := s.beginSettingsConfigRead(application.Action{Kind: "config.backups"}); err == nil {
		t.Fatal("list raced active deletion")
	}
	tester := settingsBackupDeleteTester(s, 480, false)
	if err := tester.Click("正在删除…"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("全选"); err != nil {
		t.Fatal(err)
	}
	if len(state.jobs.current) != 1 || !state.backupSelected["b"] || state.backupSelected["a"] {
		t.Fatal("busy controls changed selection or repeated deletion")
	}
	s.snapshot.Home = t.TempDir()
	s.observeSettingsConfigRecovery()
	s.applySettingsConfigBackups([]storage.Backup{{ID: "new", Path: filepath.Join(s.snapshot.Home, "backups", "silverwing-config-new.zip")}})
	s.finishSettingsConfigRead(request, application.ActionResult{Data: storage.BackupDeleteResult{DeletedIDs: []string{"new"}}}, nil)
	s.completeSettingsExtensionRead(request)
	if request.ctx.Err() == nil || len(s.settingsBackups) != 1 || mapString(s.settingsBackups[0], "id") != "new" || state.backupDeleteLoading {
		t.Fatal("late result changed a new context")
	}
	old := settingsBackupDeletePlan{scope: plan.scope, ids: []string{"new"}}
	s.confirmSettingsBackupDelete(old)
	if len(state.jobs.current) != 0 {
		t.Fatal("old confirmation started work in new home")
	}
}

func TestSettingsBackupDeleteConfirmationCancelAndBoundedLayout(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark-%t", dark), func(t *testing.T) {
			s := settingsBackupDeleteFixture(t)
			state := s.settingsConfigRecovery
			state.backupSelected = map[string]bool{"a": true, "b": true}
			plan, ok := s.settingsSelectedBackupPlan()
			if !ok {
				t.Fatal("missing plan")
			}
			for i := 2; i < 100; i++ {
				plan.names = append(plan.names, fmt.Sprintf("silverwing-config-fixture-%03d.zip", i))
			}
			s.confirmationDialog = &confirmationDialogState{Title: "删除配置备份", Message: "将永久删除所选的 100 个备份文件，此操作不可撤销。", Detail: strings.Join(plan.names, "\n"),
				Buttons: []confirmationDialogButton{{label: "删除所选"}, {label: "取消"}}, DefaultIndex: 1, CancelIndex: 1, OwnerKey: s.confirmationOwnerKey(), Open: true,
				Result: func(index int) { s.finishSettingsBackupDeleteConfirmation(plan, index) }}
			s.settingsPickerBusy = true
			tester := ui.NewTester(func(c *ui.Context) {
				p := setTheme(c, dark)
				ui.Column(c).Fill().Background(p.bg)
				s.renderConfirmationDialog(c, p)
			}, 360, 420)
			cancel, ok := tester.Find("取消")
			if !ok || cancel.Y+cancel.H > 420 || cancel.X+cancel.W > 360 || !tester.Focused("取消") {
				t.Fatalf("cancel is clipped or not focused: %+v", cancel)
			}
			tester.Key(0, ui.KeyEscape)
			if s.confirmationDialog != nil || s.settingsPickerBusy || len(state.backupSelected) != 2 || len(state.jobs.current) != 0 || len(s.settingsBackups) != 2 {
				t.Fatal("cancel altered selection or data")
			}
		})
	}
}

func TestSettingsBackupDeletePartialFailureAndEmpty(t *testing.T) {
	s := settingsBackupDeleteFixture(t)
	state := s.settingsConfigRecovery
	state.backupSelected = map[string]bool{"a": true, "b": true}
	bPath := mapString(s.settingsBackups[1], "path")
	s.applySettingsBackupDelete(application.ActionResult{Data: storage.BackupDeleteResult{
		DeletedIDs: []string{"a"}, Failures: []storage.BackupDeleteFailure{{ID: "b", Name: filepath.Base(bPath), Reason: "文件正被占用"}},
		Backups: []storage.Backup{{ID: "replacement-b", Path: bPath}},
	}}, nil)
	if len(s.settingsBackups) != 1 || mapString(s.settingsBackups[0], "id") != "b" || !state.backupSelected["b"] || state.backupSelected["a"] || state.backupFailures["b"] == "" {
		t.Fatal("partial failure lost selection, removed wrong row or duplicated replacement")
	}
	tester := settingsBackupDeleteTester(s, 480, true)
	if !tester.HasText("文件正被占用") || !tester.HasText("已选 1 个") {
		t.Fatal("partial failure feedback not visible")
	}
	s.applySettingsBackupDelete(application.ActionResult{Data: storage.BackupDeleteResult{DeletedIDs: []string{"b"}, Backups: []storage.Backup{}}}, nil)
	tester.Frame()
	if len(s.settingsBackups) != 0 || len(state.backupSelected) != 0 || !tester.HasText("暂无配置备份") || !tester.Focused("读取备份") {
		t.Fatal("all deleted did not show empty state")
	}
	s.applySettingsBackupDelete(application.ActionResult{}, errors.New("fixture failure"))
	if state.backupDeleteError != "fixture failure" {
		t.Fatal("failed deletion did not retain useful error")
	}
	tester.Frame()
	if !tester.HasText("fixture failure") {
		t.Fatal("empty-list error was hidden")
	}
}
