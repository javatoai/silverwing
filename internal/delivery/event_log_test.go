package delivery

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"silverwing-go/internal/storage"
)

func TestTagEventLogBusyLockDoesNotBlockDelivery(t *testing.T) {
	logs := t.TempDir()
	path := filepath.Join(logs, "application-"+formatSilverwingTime(time.Now())[:10]+".jsonl")
	if err := os.WriteFile(path, []byte("existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	release, err := storage.AcquireLock(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{paths: storage.Paths{Logs: logs}}
	done := make(chan struct{})
	go func() { s.recordApplicationEvent("INFO", "test", "bounded log", nil); close(done) }()
	defer func() { release(); <-done }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("best-effort logging blocked delivery behind a held log lock")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "existing\n" {
		t.Fatalf("busy log changed: %q, %v", data, err)
	}
}
