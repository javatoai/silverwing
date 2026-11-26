package platform

import (
	"context"
	"testing"

	"silverwing-go/internal/storage"
)

func TestRetiredUpdateActionsAreUnavailable(t *testing.T) {
	service := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	for _, kind := range []string{"update.check", "update.prepare", "update.status", "update.cancel", "update.apply"} {
		data, err := service.Execute(context.Background(), kind, map[string]any{"confirmed": true})
		if data != nil || err == nil || err.Error() != "未知平台操作："+kind {
			t.Errorf("retired action %s returned data=%v, err=%v", kind, data, err)
		}
	}
}
