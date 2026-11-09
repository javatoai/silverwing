package releasenotice

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestSendCLILeavesLegacyOtherRecipientsUntouched(t *testing.T) {
	for _, tt := range []struct {
		name, key string
		age       time.Duration
	}{
		{name: "expired", key: "uat-card-original", age: 2 * time.Hour},
		{name: "missing key", age: time.Minute},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, session, g, _ := noticeFixture(t, "")
			blocked := Receipt{OpenID: "ou_pending", Key: tt.key, Card: "original payload", State: "unknown",
				Error: "timeout", StartedAt: time.Now().Add(-tt.age).UTC()}
			session.Receipts = []Receipt{
				{OpenID: session.Identity.OpenID, State: "sent", MessageID: "om_self"},
				blocked,
				{OpenID: "ou_known", State: "sent", MessageID: "om_known"},
			}
			if err := store.save(session); err != nil {
				t.Fatal(err)
			}
			want := []RecipientDelivery{
				{Name: session.Identity.Name, Self: true, State: "sent", Skipped: true, MessageID: "om_self"},
			}
			results, err := store.SendCLI(context.Background(), session.ID, g, validateOK)
			if err != nil || !slices.Equal(results, want) || len(g.to) != 0 {
				t.Fatal(results, err, g.to)
			}
			saved, err := store.load(session.ID)
			if err != nil || !slices.Equal(saved.Receipts, session.Receipts) {
				t.Fatal(saved.Receipts, err)
			}
		})
	}
}

func TestSendCLIDoesNotBypassBlockedSelfRetry(t *testing.T) {
	store, session, g, _ := noticeFixture(t, "")
	session.Receipts = []Receipt{{OpenID: session.Identity.OpenID, Key: "uat-card-original", Card: "original payload",
		State: "unknown", StartedAt: time.Now().Add(-2 * time.Hour)}}
	if err := store.save(session); err != nil {
		t.Fatal(err)
	}
	results, err := store.SendCLI(context.Background(), session.ID, g, validateOK)
	if err == nil || len(results) != 1 || !results[0].Self || results[0].State != "unknown" || len(g.to) != 0 {
		t.Fatal(results, err, g.to)
	}
}

func TestSendCLIStopsOnContextCancellationBeforeSend(t *testing.T) {
	store, session, g, _ := noticeFixture(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	validate := func(context.Context, Session) error {
		cancel()
		return ctx.Err()
	}
	results, err := store.SendCLI(ctx, session.ID, g, validate)
	if !errors.Is(err, context.Canceled) || len(results) != 0 || len(g.to) != 0 {
		t.Fatal(results, err, g.to)
	}
}
