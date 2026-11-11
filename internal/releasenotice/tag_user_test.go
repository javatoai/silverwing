package releasenotice

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type userGateway struct {
	fakeGateway
	sender string
}

func (g *userGateway) SendingIdentity() string { return g.sender }

func userTagChecks(g UserGateway, validate func(context.Context, Session) error) TagSendChecks {
	return TagSendChecks{
		Refresh: func(ctx context.Context, saved Session) (Identity, error) {
			if err := validate(ctx, saved); err != nil {
				return Identity{}, err
			}
			return g.Identity(ctx)
		},
		BeforeSend: validate,
	}
}
func userNoticeFixture(t *testing.T) (Store, Session, *userGateway) {
	t.Helper()
	store, session, _, _ := noticeFixture(t, "")
	session.Identity.UserVerified = true
	session.Identity.BotReady = false
	session.Recipients = []Recipient{{Name: "测试甲", OpenID: "ou_a"}, {Name: "测试乙", OpenID: "ou_b"}}
	session.Testers = []string{"测试甲", "测试乙"}
	session.BindTagOwner(session.Owner, "profile")
	if _, err := store.Prepare(session); err != nil {
		t.Fatal(err)
	}
	return store, session, &userGateway{fakeGateway: fakeGateway{identity: session.Identity}, sender: "user"}
}
func TestUserTagPartialRetryAndExplicitResend(t *testing.T) {
	store, s, g := userNoticeFixture(t)
	g.answers = []SendResult{{MessageID: "om_a"}, {Error: "timeout", Unknown: true}}
	sent, err := store.SendTag(context.Background(), s.ID, s.RetryChoice(), g, userTagChecks(g, validateOK))
	if err == nil || sent.Deliveries()[0].State != "sent" || sent.Deliveries()[1].State != "unknown" {
		t.Fatal(sent, err)
	}
	if strings.Join(g.to, ",") != "ou_a,ou_b" {
		t.Fatal(g.to)
	}
	originalKey, originalCard := g.keys[1], g.cards[1]
	sent, err = store.SendTag(context.Background(), s.ID, s.RetryChoice(), g, userTagChecks(g, validateOK))
	if err != nil || len(g.to) != 3 || g.to[2] != "ou_b" || g.keys[2] != originalKey || g.cards[2] != originalCard {
		t.Fatal(sent, err, g)
	}
	_, err = store.SendTag(context.Background(), s.ID, s.RetryChoice(), g, userTagChecks(g, validateOK))
	if err != nil || len(g.to) != 3 {
		t.Fatal(err, g.to)
	}
	choice, err := sent.SendChoice()
	if err != nil || !choice.Resend {
		t.Fatal(choice, err)
	}
	_, err = store.SendTag(context.Background(), s.ID, choice, g, userTagChecks(g, validateOK))
	if err != nil || len(g.to) != 5 || g.keys[4] == originalKey {
		t.Fatal(err, g.to)
	}
	_, err = store.SendTag(context.Background(), s.ID, choice, g, userTagChecks(g, validateOK))
	if err == nil || len(g.to) != 5 {
		t.Fatal("duplicate desktop click sent again", err, g.to)
	}
}

func TestUserTagRetriesTesterNamedCardThenResendsFixedTitle(t *testing.T) {
	store, session, g := userNoticeFixture(t)
	content, err := noticeLink(session.Title, session.URL)
	if err != nil {
		t.Fatal(err)
	}
	oldCard, err := renderUATElements(session.Title, strings.Join(session.Testers, "、")+", 申请发布UAT环境版本", content, session.Rows)
	if err != nil {
		t.Fatal(err)
	}
	currentCard, err := SessionCard(session)
	if err != nil || oldCard == currentCard {
		t.Fatal("fixture must preserve a previous tester-named card", err)
	}
	session.Receipts = []Receipt{
		{OpenID: "ou_a", Name: "测试甲", State: "sent", Key: "uat-card-previous-success", Card: oldCard, MessageID: "om_previous", StartedAt: time.Now().UTC()},
		{OpenID: "ou_b", Name: "测试乙", State: "unknown", Key: "uat-card-previous-unknown", Card: oldCard, StartedAt: time.Now().UTC()},
	}
	if err := store.save(session); err != nil {
		t.Fatal(err)
	}
	prepared, err := store.Prepare(session)
	if err != nil {
		t.Fatal(err)
	}
	sent, err := store.SendTag(context.Background(), prepared.ID, prepared.RetryChoice(), g, userTagChecks(g, validateOK))
	if err != nil || len(g.to) != 1 || g.to[0] != "ou_b" || g.cards[0] != oldCard || g.keys[0] != "uat-card-previous-unknown" || len(sent.Receipts) != 2 || sent.Receipts[0] != session.Receipts[0] || sent.Receipts[1].State != "sent" {
		t.Fatal("unknown retry changed the old card/key or repeated a success", sent, err, g)
	}
	choice, err := sent.SendChoice()
	if err != nil || !choice.Resend {
		t.Fatal(choice, err)
	}
	resent, err := store.SendTag(context.Background(), sent.ID, choice, g, userTagChecks(g, validateOK))
	if err != nil || len(g.to) != 3 || len(resent.Receipts) != 4 || strings.Join(resent.Testers, "、") != strings.Join(session.Testers, "、") {
		t.Fatal(resent, err, g)
	}
	for i := 1; i < len(g.cards); i++ {
		if g.cards[i] != currentCard || g.keys[i] == session.Receipts[0].Key || g.keys[i] == session.Receipts[1].Key || resent.Receipts[i-1].Card != oldCard {
			t.Fatal("explicit resend did not use a new card/key or altered history", resent, g)
		}
	}
}

func TestUserTagRejectsInvalidIdentityAndStaleSnapshot(t *testing.T) {
	for _, mode := range []string{"bot", "unverified", "account", "app", "cancel", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			store, s, g := userNoticeFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			check := validateOK
			switch mode {
			case "bot":
				g.sender = "bot"
			case "unverified":
				g.identity.UserVerified = false
			case "account":
				g.identity.OpenID = "ou_other"
			case "app":
				g.identity.AppID = "other"
			case "cancel":
				cancel()
			case "snapshot":
				check = func(context.Context, Session) error { return errors.New("changed") }
			}
			if _, err := store.SendTag(ctx, s.ID, s.RetryChoice(), g, userTagChecks(g, check)); err == nil || len(g.to) != 0 {
				t.Fatal(err, g.to)
			}
		})
	}
}
func TestUserTagStopsBetweenRecipientsAndKeepsReservation(t *testing.T) {
	store, s, g := userNoticeFixture(t)
	checks := 0
	sent, err := store.SendTag(context.Background(), s.ID, s.RetryChoice(), g, userTagChecks(g, func(context.Context, Session) error {
		checks++
		if checks == 3 {
			return errors.New("account changed")
		}
		return nil
	}))
	if err == nil || len(g.to) != 1 || sent.Deliveries()[1].State != "queued" {
		t.Fatal(sent, err, g.to)
	}
	queued := sent.Receipts[1].Key
	_, err = store.SendTag(context.Background(), s.ID, s.RetryChoice(), g, userTagChecks(g, validateOK))
	if err != nil || len(g.to) != 2 || g.keys[1] != queued {
		t.Fatal(err, g.to)
	}
}
func TestUserTagHistoryAndRetryBoundary(t *testing.T) {
	store, s, g := userNoticeFixture(t)
	g.answers = []SendResult{{Error: "timeout", Unknown: true}, {MessageID: "om_b"}}
	sent, err := store.SendTag(context.Background(), s.ID, s.RetryChoice(), g, userTagChecks(g, validateOK))
	if err == nil {
		t.Fatal("expected partial")
	}
	changed := s
	changed.Recipients = []Recipient{{Name: "另一人", OpenID: "ou_other"}}
	changed.BindTagOwner(s.Owner, s.Profile)
	if _, err = store.Prepare(changed); err == nil {
		t.Fatal("pending recipient change accepted")
	}
	changed = s
	changed.Title = "different content"
	if _, err = store.Prepare(changed); err == nil {
		t.Fatal("pending content replaced")
	}
	sent.Receipts[0].StartedAt = time.Now().Add(-time.Hour)
	if err = store.save(sent); err != nil {
		t.Fatal(err)
	}
	if _, err = store.SendTag(context.Background(), s.ID, s.RetryChoice(), g, userTagChecks(g, validateOK)); err == nil || len(g.to) != 2 {
		t.Fatal("expired key retried", err, g.to)
	}
	// Bot history has another identity and cannot satisfy this user notice.
	if s.Audience != UserTesters || s.Sender != "user" || len(s.Receipts) != 0 {
		t.Fatal(s)
	}
}

func TestUserTagPreparedSnapshotAndBotHistoryAreIsolated(t *testing.T) {
	store, legacy, bot, choice := noticeFixture(t, "")
	if _, err := store.Send(context.Background(), legacy.ID, choice, bot, validateOK); err != nil {
		t.Fatal(err)
	}
	s := legacy
	s.Identity.UserVerified = true
	s.Recipients = []Recipient{{Name: "测试", OpenID: "ou_self"}}
	s.BindTagOwner(legacy.Owner, "profile")
	prepared, err := store.Prepare(s)
	if err != nil || len(prepared.Receipts) != 0 || prepared.ID == legacy.ID {
		t.Fatal(prepared, err)
	}
	oldChoice := prepared.RetryChoice()
	s.Title = "更新需求标题"
	if _, err = store.Prepare(s); err != nil {
		t.Fatal(err)
	}
	g := &userGateway{fakeGateway: fakeGateway{identity: s.Identity}, sender: "user"}
	if _, err = store.SendTag(context.Background(), s.ID, oldChoice, g, userTagChecks(g, validateOK)); err == nil || len(g.to) != 0 {
		t.Fatal("stale prepared click accepted", err, g.to)
	}
	if _, err = store.SendTag(context.Background(), s.ID, s.RetryChoice(), g, userTagChecks(g, validateOK)); err != nil || len(g.to) != 1 {
		t.Fatal(err, g.to)
	}
	old, err := store.load(legacy.ID)
	if err != nil || len(old.Receipts) != 1 || old.Receipts[0].State != "sent" {
		t.Fatal("legacy history changed", old, err)
	}
}

func TestUserTagRosterOrderAndAddedRecipientDoNotDuplicateSuccess(t *testing.T) {
	store, s, g := userNoticeFixture(t)
	if _, err := store.SendTag(context.Background(), s.ID, s.RetryChoice(), g, userTagChecks(g, validateOK)); err != nil {
		t.Fatal(err)
	}
	changed := s
	changed.Recipients = []Recipient{s.Recipients[1], s.Recipients[0]}
	changed.BindTagOwner(s.Owner, s.Profile)
	if changed.ID != s.ID {
		t.Fatal("ordering changed receipt identity")
	}
	changed.Recipients = append(changed.Recipients, Recipient{Name: "新增", OpenID: "ou_c"})
	changed.BindTagOwner(s.Owner, s.Profile)
	prepared, err := store.Prepare(changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SendTag(context.Background(), prepared.ID, prepared.RetryChoice(), g, userTagChecks(g, validateOK)); err != nil || len(g.to) != 3 || g.to[2] != "ou_c" {
		t.Fatal(err, g.to)
	}
}
