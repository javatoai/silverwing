package application

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/integrations"
	"silverwing-go/internal/releasenotice"
)

func TestTagRecipientsWholeRosterOrSelfFallback(t *testing.T) {
	self := releasenotice.Identity{OpenID: "ou_self", Name: "当前用户"}
	for _, mode := range []string{"multiple", "duplicate", "ambiguous", "empty", "failed", "stale", "blank", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			metadata := integrations.RequirementMetadataReadResult{ReleaseTesters: []domain.RequirementPerson{{Name: "甲"}, {Name: "乙"}}}
			var readErr error
			switch mode {
			case "empty":
				metadata.ReleaseTesters = []domain.RequirementPerson{}
			case "failed":
				readErr = errors.New("offline")
			case "stale":
				metadata.Stale = true
			case "blank":
				metadata.ReleaseTesters[1].Name = " "
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls atomic.Int32
			resolve := func(_ context.Context, name string) (releasenotice.Recipient, error) {
				calls.Add(1)
				id := "ou_a"
				if name == "乙" && mode != "duplicate" {
					id = "ou_b"
				}
				if name == "乙" && mode == "ambiguous" {
					return releasenotice.Recipient{}, errors.New("同名歧义")
				}
				if mode == "cancel" {
					cancel()
				}
				return releasenotice.Recipient{Name: name, OpenID: id}, nil
			}
			got, warning, err := tagRecipients(ctx, self, metadata, readErr, resolve)
			if mode == "cancel" {
				if !errors.Is(err, context.Canceled) || len(got) > 0 {
					t.Fatal(got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "multiple" {
				if len(got) != 2 || warning != "" {
					t.Fatal(got, warning)
				}
				return
			}
			if mode == "duplicate" {
				if len(got) != 1 || got[0].OpenID != "ou_a" || warning != "" {
					t.Fatal(got, warning)
				}
				return
			}
			if len(got) != 1 || got[0].OpenID != self.OpenID || !strings.Contains(warning, "自行转发") {
				t.Fatal(got, warning)
			}
			if mode != "ambiguous" && calls.Load() != 0 {
				t.Fatal("unreliable roster queried", calls.Load())
			}
		})
	}
}
