package integrations

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/releasenotice"
	"silverwing-go/internal/storage"
)

func TestNoticeAccountContextWatchesConfigAndCredentialsWithoutPersistingSecrets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv("MEEGLE_HOST", "")
	t.Setenv("MEEGLE_USER_ACCESS_TOKEN", "")
	dir := filepath.Join(home, ".meegle")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"current":"isolated-notice-account","profiles":{"isolated-notice-account":{"host":"project.feishu.cn","user_access_token":"private-sentinel"}}}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	cli := "fake-meegle"
	cfg := domain.AppConfig{MeegleExecutablePath: &cli}
	s := &Service{}
	one := s.noticeLocalAccount(context.Background(), cfg)
	if !one.bound || one.fingerprint == "" || strings.Contains(one.fingerprint, "private-sentinel") {
		t.Fatal(one)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(strings.ReplaceAll(string(data), "private-sentinel", "other-private-sentinel")), 0600); err != nil {
		t.Fatal(err)
	}
	two := s.noticeLocalAccount(context.Background(), cfg)
	if one.fingerprint == two.fingerprint {
		t.Fatal("account credential switch was invisible")
	}
	encoded, _ := json.Marshal(map[string]string{"localContext": one.fingerprint})
	if strings.Contains(string(encoded), "private-sentinel") {
		t.Fatal("raw credential escaped")
	}
}

func TestNoticeAuthorizationTimeIsRequestLocalAndIndependentOfPermanentData(t *testing.T) {
	paths := storage.NewPaths(t.TempDir())
	store := storage.NewConfigStore(paths)
	cfg := domain.DefaultAppConfig()
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	cfg, _ = store.Load()
	s := NewService(paths, store, nil)
	p := &TagNoticePreparation{service: s, config: cfg, Identity: releasenotice.Identity{UserVerified: true}, verifiedAt: time.Now().Add(-time.Hour)}
	// Authorization expires before any account read or CLI can run, regardless
	// of the lifetime of the recipient data already on disk.
	if err := p.Commit(context.Background()); err == nil || !strings.Contains(err.Error(), "在线验证已过期") {
		t.Fatal(err)
	}
}
