package releasenotice

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

type fakeGateway struct {
	identity        Identity
	answers         []SendResult
	keys, cards, to []string
}

func (f *fakeGateway) Identity(context.Context) (Identity, error) { return f.identity, nil }
func (f *fakeGateway) Send(_ context.Context, to, card, key string) SendResult {
	f.to = append(f.to, to)
	f.cards = append(f.cards, card)
	f.keys = append(f.keys, key)
	answer := SendResult{MessageID: "om_fixture"}
	if len(f.answers) > 0 {
		answer = f.answers[0]
		f.answers = f.answers[1:]
	}
	return answer
}
func noticeFixture(t *testing.T, kind string) (Store, Session, *fakeGateway, Selection) {
	t.Helper()
	identity := Identity{AppID: "cli_fixture", Name: "本地用户名", OpenID: "ou_self", BotReady: true, UserState: "expired"}
	session := Session{Scope: "scope", Kind: kind, Identity: identity, Title: "测试需求", URL: "https://example.invalid/requirement", Rows: []Row{{OperationID: "one", Service: "synthetic", Version: "1.beta-1", Branch: "feature/task"}}}
	session.BindOwner("task-one")
	store := Store{Directory: t.TempDir()}
	if _, e := store.Prepare(session); e != nil {
		t.Fatal(e)
	}
	return store, session, &fakeGateway{identity: identity}, Selection{Retry: true}
}
func validateOK(context.Context, Session) error { return nil }

func TestCardPreservesCard20StructureAndTagTester(t *testing.T) {
	for _, kind := range []string{"", Branches} {
		t.Run(kind, func(t *testing.T) {
			_, session, _, _ := noticeFixture(t, kind)
			session.Title = "需求 [A]"
			raw, e := SessionCard(session)
			if e != nil {
				t.Fatal(e)
			}
			var card struct {
				Schema string
				Config struct {
					WidthMode string `json:"width_mode"`
					Summary   struct{ Content string }
				}
				Header struct {
					Template string
					Title    struct{ Content string }
					Subtitle any
					Icon     struct{ Token string }
				}
				Body struct {
					Padding  string
					Spacing  string `json:"vertical_spacing"`
					Elements []map[string]any
				}
			}
			if e = json.Unmarshal([]byte(raw), &card); e != nil {
				t.Fatal(e)
			}
			if card.Schema != "2.0" || card.Header.Template != "blue" || card.Header.Icon.Token != "bell_outlined" || card.Header.Subtitle != nil || card.Body.Padding != "12px" || card.Body.Spacing != "12px" {
				t.Fatal(raw)
			}
			content := "**[需求 &#91;A&#93;](https://example.invalid/requirement)**"
			if card.Body.Elements[0]["content"] != content {
				t.Fatal(raw)
			}
			if kind == Branches {
				if card.Config.WidthMode != "fill" || card.Header.Title.Content != "上线分支通知" || len(card.Body.Elements) != 2 || card.Config.Summary.Content != "上线分支通知｜需求 [A]" {
					t.Fatal(raw)
				}
			} else {
				header := "测试负责人待确认, 申请发布UAT环境版本"
				if card.Config.WidthMode != "default" || card.Header.Title.Content != header || len(card.Body.Elements) != 3 || card.Body.Elements[2]["element_id"] != "genbu_link" || card.Config.Summary.Content != header+"｜需求 [A]" {
					t.Fatal(raw)
				}
			}
			if card.Body.Elements[1]["tag"] != "table" {
				t.Fatal(raw)
			}
		})
	}
}
func TestBothNoticesSendOnlyToSelfAndExplicitlyResend(t *testing.T) {
	for _, kind := range []string{"", Branches} {
		t.Run(kind, func(t *testing.T) {
			store, session, g, choice := noticeFixture(t, kind)
			g.identity.Name = "" // A cached ID with no name and expired token is sufficient.
			first, e := store.Send(context.Background(), session.ID, choice, g, validateOK)
			if e != nil || len(g.to) != 1 || g.to[0] != "ou_self" || len(first.Receipts) != 1 || first.Receipts[0].State != "sent" || first.Receipts[0].ChatID != "" || strings.Contains(g.cards[0], session.Identity.Name) {
				t.Fatal(first, e, g.to)
			}
			if _, e = store.Send(context.Background(), session.ID, choice, g, validateOK); e != nil || len(g.to) != 1 {
				t.Fatal("duplicate initial request", e)
			}
			next, e := first.SendChoice()
			if e != nil || !next.Resend || next.ResendAfter != first.Receipts[0].Key {
				t.Fatal(next, e)
			}
			second, e := store.Send(context.Background(), session.ID, next, g, validateOK)
			if e != nil || len(g.to) != 2 || len(second.Receipts) != 2 || g.keys[0] == g.keys[1] {
				t.Fatal(second, e)
			}
			if _, e = store.Send(context.Background(), session.ID, next, g, validateOK); e != nil || len(g.to) != 2 {
				t.Fatal("replayed resend", e)
			}
			next, _ = second.SendChoice()
			third, e := store.Send(context.Background(), session.ID, next, g, validateOK)
			if e != nil || len(g.to) != 3 || len(third.Receipts) != 3 || g.keys[1] == g.keys[2] {
				t.Fatal(third, e)
			}
		})
	}
}
func TestBothNoticesRetryUncertainResultWithoutNewMessage(t *testing.T) {
	for _, kind := range []string{"", Branches} {
		t.Run(kind, func(t *testing.T) {
			store, session, g, choice := noticeFixture(t, kind)
			g.answers = []SendResult{{Error: "timeout", Unknown: true}}
			unknown, e := store.Send(context.Background(), session.ID, choice, g, validateOK)
			if e != nil || len(unknown.Receipts) != 1 || unknown.Receipts[0].State != "unknown" {
				t.Fatal(unknown, e)
			}
			reopened, e := (Store{Directory: store.Directory}).Prepare(session)
			if e != nil {
				t.Fatal(e)
			}
			next, _ := reopened.SendChoice()
			if next.Resend || !next.Retry {
				t.Fatal(next)
			}
			sent, e := store.Send(context.Background(), session.ID, next, g, validateOK)
			if e != nil || len(sent.Receipts) != 1 || sent.Receipts[0].State != "sent" || len(g.to) != 2 || g.keys[0] != g.keys[1] || g.cards[0] != g.cards[1] {
				t.Fatal(sent, e)
			}
			next, _ = sent.SendChoice()
			g.answers = []SendResult{{Error: "timeout", Unknown: true}}
			resent, e := store.Send(context.Background(), session.ID, next, g, validateOK)
			if e != nil || len(resent.Receipts) != 2 || resent.Receipts[1].State != "unknown" {
				t.Fatal(resent, e)
			}
			if _, e = store.Send(context.Background(), session.ID, next, g, validateOK); e != nil || len(g.to) != 3 {
				t.Fatal("resend replay duplicated uncertain result", e)
			}
			retry, _ := resent.SendChoice()
			done, e := store.Send(context.Background(), session.ID, retry, g, validateOK)
			if e != nil || len(g.to) != 4 || g.keys[2] != g.keys[3] || len(done.Receipts) != 2 {
				t.Fatal(done, e)
			}
		})
	}
}

func TestCLINoticeDeduplicatesAndReusesUncertainSelfReceipt(t *testing.T) {
	store, session, g, _ := noticeFixture(t, "")
	g.answers = []SendResult{{Error: "timeout", Unknown: true}}
	results, err := store.SendCLI(context.Background(), session.ID, g, validateOK)
	if err == nil || len(results) != 1 || !results[0].Self || results[0].State != "unknown" || len(g.to) != 1 || g.to[0] != session.Identity.OpenID {
		t.Fatal(results, err, g.to)
	}
	session.Title = "Refreshed requirement title"
	if _, err := store.Prepare(session); err != nil {
		t.Fatal(err)
	}
	results, err = store.SendCLI(context.Background(), session.ID, g, validateOK)
	if err != nil || len(results) != 1 || results[0].Skipped || results[0].State != "sent" ||
		len(g.to) != 2 || g.to[1] != session.Identity.OpenID || g.keys[0] != g.keys[1] || g.cards[0] != g.cards[1] {
		t.Fatal(results, err, g.to)
	}
	results, err = store.SendCLI(context.Background(), session.ID, g, validateOK)
	if err != nil || len(results) != 1 || !results[0].Skipped || len(g.to) != 2 {
		t.Fatal("successful self receipt was sent again", results, err, g.to)
	}
}
func TestUnsafeIdentityOrLegacyRecipientsNeverReachSend(t *testing.T) {
	for _, mode := range []string{"account", "app", "bot", "missing-id", "legacy-policy", "legacy-group", "legacy-friends", "scope"} {
		t.Run(mode, func(t *testing.T) {
			store, session, g, choice := noticeFixture(t, Branches)
			validate := validateOK
			switch mode {
			case "account":
				g.identity.OpenID = "ou_other"
			case "app":
				g.identity.AppID = "other-app"
			case "bot":
				g.identity.BotReady = false
			case "missing-id":
				g.identity.OpenID = ""
			case "legacy-policy":
				session.Audience = ""
			case "legacy-group":
				session.People = []Person{{ChatID: "oc_old_group"}}
			case "legacy-friends":
				session.People = []Person{{OpenID: "ou_other"}, {OpenID: "ou_self"}}
			case "scope":
				validate = func(context.Context, Session) error { return errors.New("stale task") }
			}
			if e := store.save(session); e != nil {
				t.Fatal(e)
			}
			if _, e := store.Send(context.Background(), session.ID, choice, g, validate); e == nil || len(g.to) != 0 {
				t.Fatal("unsafe notification reached CLI", mode, e, g.to)
			}
		})
	}
}
func TestUnknownReceiptCannotBeBypassedOrRewritten(t *testing.T) {
	for _, mode := range []string{"expired", "future", "changed-card", "resend-unknown", "missing-resend-key", "missing-receipt"} {
		t.Run(mode, func(t *testing.T) {
			store, session, g, choice := noticeFixture(t, "")
			card, _ := SessionCard(session)
			session.Receipts = []Receipt{{OpenID: "ou_self", Key: "uat-card-original", Card: card, State: "unknown", StartedAt: time.Now()}}
			switch mode {
			case "expired":
				session.Receipts[0].StartedAt = time.Now().Add(-2 * time.Hour)
			case "future":
				session.Receipts[0].StartedAt = time.Now().Add(time.Hour)
			case "changed-card":
				session.Receipts[0].Card = "legacy named card"
			case "resend-unknown":
				choice.Resend = true
				choice.ResendAfter = "uat-card-original"
			case "missing-resend-key":
				session.Receipts[0].State = "sent"
				choice.Resend = true
			case "missing-receipt":
				session.Receipts = nil
				choice.Resend = true
				choice.ResendAfter = "absent"
			}
			if e := store.save(session); e != nil {
				t.Fatal(e)
			}
			if _, e := store.Send(context.Background(), session.ID, choice, g, validateOK); e == nil || len(g.to) != 0 {
				t.Fatal(mode, e)
			}
		})
	}
}
func TestConcurrentClickAndCancellationDoNotSend(t *testing.T) {
	store, session, g, choice := noticeFixture(t, "")
	if e := store.Locked(session.ID, func() error {
		_, e := store.Send(context.Background(), session.ID, choice, g, validateOK)
		if e == nil {
			t.Fatal("concurrent writer")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if _, e := store.Send(ctx, session.ID, choice, g, func(context.Context, Session) error { cancel(); return nil }); !errors.Is(e, context.Canceled) || len(g.to) != 0 {
		t.Fatal(e, g.to)
	}
}
func TestLegacyHistoryOnlyImportsSelfAndPreservesOldFiles(t *testing.T) {
	for _, kind := range []string{"", Branches} {
		t.Run(kind, func(t *testing.T) {
			_, legacy, _, _ := noticeFixture(t, kind)
			legacy.ID = Digest("legacy-" + kind)
			legacy.Audience = ""
			legacy.Identity.Name = "旧姓名"
			legacy.People = []Person{{OpenID: "ou_friend"}, {ChatID: "oc_group"}, {OpenID: "ou_self"}}
			legacy.Receipts = []Receipt{{OpenID: "ou_friend", Key: "friend", State: "sent", StartedAt: time.Now()}, {ChatID: "oc_group", Key: "group", State: "sent", StartedAt: time.Now()}, {OpenID: "ou_self", Key: "self", State: "unknown", Card: "old named card", StartedAt: time.Now()}}
			store := Store{Directory: t.TempDir()}
			if e := store.Locked(legacy.ID, func() error { return store.save(legacy) }); e != nil {
				t.Fatal(e)
			}
			path, _ := store.path(legacy.ID)
			before, _ := os.ReadFile(path)
			current := legacy
			current.People = nil
			current.Receipts = nil
			current.Identity.Name = ""
			current.BindOwner("task-one")
			saved, e := store.Prepare(current)
			if e != nil || len(saved.Receipts) != 1 || saved.Receipts[0].Key != "self" {
				t.Fatal(saved, e)
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("old receipt file changed")
			}
			current.Identity.Name = "新姓名"
			current.BindOwner("task-one")
			if current.ID != saved.ID {
				t.Fatal("name affected identity")
			}
			current.Scope = "refreshed-config"
			refreshed, e := store.Prepare(current)
			if e != nil || len(refreshed.Receipts) != 1 || refreshed.Scope != current.Scope {
				t.Fatal(refreshed, e)
			}
			current.BindOwner("different-task")
			other, e := store.Prepare(current)
			if e != nil || len(other.Receipts) != 0 {
				t.Fatal("cross-task receipt", other, e)
			}
			g := &fakeGateway{identity: saved.Identity}
			if _, e = store.Send(context.Background(), saved.ID, Selection{Retry: true}, g, validateOK); e == nil || len(g.to) != 0 {
				t.Fatal("old unknown card silently rewritten", e)
			}
		})
	}
}

func TestLegacySelfSuccessResendsUsingCurrentLayout(t *testing.T) {
	for _, kind := range []string{"", Branches} {
		t.Run(kind, func(t *testing.T) {
			store, session, g, _ := noticeFixture(t, kind)
			session.Receipts = []Receipt{{OpenID: "ou_other", State: "sent", Key: "friend"}, {ChatID: "oc_group", State: "sent", Key: "group"}, {OpenID: "ou_self", Key: "old-self", State: "sent", MessageID: "om_old", Card: "old named card"}}
			if e := store.save(session); e != nil {
				t.Fatal(e)
			}
			choice, e := session.SendChoice()
			if e != nil || !choice.Resend || choice.ResendAfter != "old-self" {
				t.Fatal(choice, e)
			}
			card, e := SessionCard(session)
			if e != nil {
				t.Fatal(e)
			}
			out, e := store.Send(context.Background(), session.ID, choice, g, validateOK)
			if e != nil || len(g.to) != 1 || g.to[0] != "ou_self" || g.keys[0] == "old-self" || g.cards[0] != card || len(out.Receipts) != 4 {
				t.Fatal(out, e, g.to)
			}
		})
	}
}

func TestTagTitleNamesTestersWithoutApplicantOrBrackets(t *testing.T) {
	_, session, _, _ := noticeFixture(t, "")
	session.Testers = []string{"靳保新", "梁聪"}
	card, err := SessionCard(session)
	if err != nil || !strings.Contains(card, "靳保新、梁聪, 申请发布UAT环境版本") || strings.Contains(card, session.Identity.Name) || strings.ContainsAny(card, "《》") || strings.Contains(card, "请转发") {
		t.Fatal(card, err)
	}
}

func TestUATNoticeRetriesPreviousLayoutsWithOriginalPayload(t *testing.T) {
	for _, layout := range []string{"anonymous", "applicant-in-body", "applicant-in-header"} {
		t.Run(layout, func(t *testing.T) {
			store, session, g, choice := noticeFixture(t, "")
			applicant := ""
			if layout != "anonymous" {
				applicant = session.Identity.Name
			}
			oldCard, err := renderUATCard(session.Title, session.URL, applicant, session.Rows, layout != "applicant-in-header")
			if err != nil {
				t.Fatal(err)
			}
			session.Receipts = []Receipt{{OpenID: session.Identity.OpenID, State: "unknown", Key: "uat-card-original", Card: oldCard, StartedAt: time.Now()}}
			if err := store.save(session); err != nil {
				t.Fatal(err)
			}
			out, err := store.Send(context.Background(), session.ID, choice, g, validateOK)
			if err != nil || len(g.cards) != 1 || g.cards[0] != oldCard || g.keys[0] != "uat-card-original" || len(out.Receipts) != 1 || out.Receipts[0].State != "sent" {
				t.Fatal(out, err, g.cards, g.keys)
			}
		})
	}
}
