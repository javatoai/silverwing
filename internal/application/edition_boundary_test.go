package application

import (
	"context"
	"reflect"
	"testing"
)

func TestPublicEditionRejectsInternalActionsWithoutOpeningData(t *testing.T) {
	// A nil backend/config store makes any accidental dispatch into an internal
	// execution chain fail; unsupported actions must stop at the router.
	app := &App{}
	for _, kind := range []string{"tag.build", "tag.history", "tag.history-delete", "tag.probe", "production.inspect", "production.create", "production.delete"} {
		if _, err := app.Dispatch(context.Background(), Action{Kind: kind}); err == nil {
			t.Fatalf("internal action accepted: %s", kind)
		}
	}
}

func TestPublicSnapshotsExcludeInternalCapabilities(t *testing.T) {
	for _, target := range []any{Snapshot{}, Task{}, Workspace{}, Service{}, Module{}, Group{}} {
		schema := reflect.TypeOf(target)
		for i := 0; i < schema.NumField(); i++ {
			field := schema.Field(i).Name
			for _, prefix := range []string{"Tag", "Genbu", "Production"} {
				if len(field) >= len(prefix) && field[:len(prefix)] == prefix {
					t.Fatalf("%s exposes %s", schema.Name(), field)
				}
			}
		}
	}
}
