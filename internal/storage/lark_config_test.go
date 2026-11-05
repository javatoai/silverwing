package storage

import (
	"silverwing-go/internal/domain"
	"testing"
)

func TestLarkNotificationConfigRoundTripHasNoFixedSelf(t *testing.T) {
	store := NewConfigStore(NewPaths(t.TempDir()))
	cfg, e := store.Load()
	if e != nil {
		t.Fatal(e)
	}
	defaults := cfg.EffectiveLarkNotice()
	if len(defaults.Contacts) != 0 {
		t.Fatal(defaults)
	}
	cfg.LarkNotice = &domain.LarkNoticeConfig{Profile: defaults.Profile, AppID: defaults.AppID, BranchChatID: "oc_synthetic", Contacts: []domain.LarkNoticeContact{{Name: "其他测试", OpenID: "ou_fixture"}}}
	path := "C:/isolated/lark-cli.cmd"
	cfg.LarkExecutablePath = &path
	if e = store.Save(cfg); e != nil {
		t.Fatal(e)
	}
	got, e := NewConfigStore(store.Paths()).Load()
	if e != nil || got.LarkExecutablePath == nil || *got.LarkExecutablePath != path || got.EffectiveLarkNotice().Contacts[0].Name != "其他测试" || got.EffectiveLarkNotice().BranchChatID != "oc_synthetic" {
		t.Fatal(got.LarkNotice, e)
	}
}

func TestRetiredLarkRecipientsDoNotBlockSavingOrGetDiscarded(t *testing.T) {
	store := NewConfigStore(NewPaths(t.TempDir()))
	cfg, e := store.Load()
	if e != nil {
		t.Fatal(e)
	}
	notice := cfg.EffectiveLarkNotice()
	notice.BranchChatID = "retired invalid group"
	notice.Contacts = []domain.LarkNoticeContact{{Name: "", OpenID: "retired invalid id"}}
	cfg.LarkNotice = &notice
	if e = store.Save(cfg); e != nil {
		t.Fatal(e)
	}
	saved, e := store.Load()
	if e != nil || saved.EffectiveLarkNotice().BranchChatID != notice.BranchChatID || len(saved.EffectiveLarkNotice().Contacts) != 1 {
		t.Fatal(saved.LarkNotice, e)
	}
}
