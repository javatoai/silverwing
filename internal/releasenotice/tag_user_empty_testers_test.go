package releasenotice

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func emptyTesterNoticeFixture(t *testing.T, testers []string) (Store, Session, *userGateway) {
	t.Helper()
	session := Session{
		Scope: "empty-testers-scope", Title: "隔离测试需求", URL: "https://example.invalid/requirement",
		Identity:   Identity{AppID: "cli_fixture", Name: "本人", OpenID: "ou_self", UserVerified: true},
		Rows:       []Row{{OperationID: "empty-testers-operation", Service: "synthetic", Version: "1.beta-1", Branch: "feature/task"}},
		Testers:    testers,
		Recipients: []Recipient{{Name: "本人", OpenID: "ou_self"}},
		Warning:    "需求未配置测试负责人",
	}
	session.BindTagOwner("empty-testers-task", "profile")
	return Store{Directory: t.TempDir()}, session, &userGateway{fakeGateway: fakeGateway{identity: session.Identity}, sender: "user"}
}

func TestUserTagEmptyTesterSelfFallbackSurvivesPrepareLoad(t *testing.T) {
	for _, tt := range []struct {
		name    string
		testers []string
	}{{name: "nil"}, {name: "empty", testers: []string{}}} {
		t.Run(tt.name, func(t *testing.T) {
			store, session, g := emptyTesterNoticeFixture(t, tt.testers)
			prepared, err := store.Prepare(session)
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := store.load(prepared.ID)
			if err != nil || loaded.Testers != nil {
				t.Fatal("persisted empty roster must exercise omitted testers", loaded.Testers, err)
			}
			sent, err := store.SendTag(context.Background(), prepared.ID, prepared.RetryChoice(), g, userTagChecks(g, validateOK))
			if err != nil || len(g.to) != 1 || g.to[0] != session.Identity.OpenID || len(sent.Receipts) != 1 || sent.Receipts[0].State != "sent" {
				t.Fatalf("self fallback failed after Prepare/Load (prepared testers nil=%t, loaded nil=%t): %v; sends=%v; receipts=%v", prepared.Testers == nil, loaded.Testers == nil, err, g.to, sent.Receipts)
			}
			reopened, err := store.Prepare(session)
			if err != nil {
				t.Fatal("repeated preparation failed", err)
			}
			repeated, err := store.SendTag(context.Background(), reopened.ID, reopened.RetryChoice(), g, userTagChecks(g, validateOK))
			if err != nil || len(g.to) != 1 || !slices.Equal(repeated.Receipts, sent.Receipts) {
				t.Fatal("repeated preparation duplicated or changed a successful receipt", repeated, err, g.to)
			}
		})
	}
}

func TestUserTagEmptyTesterRetryPreservesOriginalReceipt(t *testing.T) {
	for _, state := range []string{"unknown", "queued"} {
		for _, stored := range []string{"omitted", "explicit-empty"} {
			t.Run(state+"/"+stored, func(t *testing.T) {
				testers := []string{}
				if stored == "explicit-empty" {
					testers = nil
				}
				store, session, g := emptyTesterNoticeFixture(t, testers)
				originalCard, err := renderUATCard(session.Title, session.URL, "旧申请人", session.Rows, true)
				if err != nil {
					t.Fatal(err)
				}
				success := Receipt{OpenID: "ou_self", Name: "本人", Key: "uat-card-previous-success", Card: originalCard, State: "sent", MessageID: "om_previous", StartedAt: time.Now().Add(-10 * time.Minute).UTC()}
				original := Receipt{OpenID: "ou_self", Name: "本人", Key: "uat-card-original", Card: originalCard, State: state, StartedAt: time.Now().Add(-5 * time.Minute).UTC()}
				session.Receipts = []Receipt{success, original}
				if stored == "explicit-empty" {
					// Exercise older records that explicitly persisted [], as well as
					// current omitempty records that reload the same roster as nil.
					path, _ := store.path(session.ID)
					data, err := json.Marshal(struct {
						Session
						Testers []string `json:"testers"`
					}{Session: session, Testers: []string{}})
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, data, 0600); err != nil {
						t.Fatal(err)
					}
				} else if err := store.save(session); err != nil {
					t.Fatal(err)
				}
				loaded, err := store.load(session.ID)
				if err != nil || (loaded.Testers != nil) != (stored == "explicit-empty") {
					t.Fatal("wrong persisted roster fixture", loaded.Testers, err)
				}
				prepared, err := store.Prepare(session)
				if err != nil {
					t.Fatal("equivalent empty roster blocked pending preparation", err)
				}
				prepared, err = store.Prepare(prepared)
				if err != nil || !slices.Equal(prepared.Receipts, session.Receipts) {
					t.Fatal("repeated preparation changed existing receipts", prepared.Receipts, err)
				}
				sent, err := store.SendTag(context.Background(), prepared.ID, prepared.RetryChoice(), g, userTagChecks(g, validateOK))
				if err != nil || len(g.to) != 1 || g.to[0] != "ou_self" || g.keys[0] != original.Key || g.cards[0] != original.Card || len(sent.Receipts) != 2 || sent.Receipts[0] != success {
					t.Fatal("retry changed the original card/key or successful history", sent, err, g.to, g.keys, g.cards)
				}
				want := original
				want.State, want.MessageID = "sent", "om_fixture"
				if sent.Receipts[1] != want {
					t.Fatal("retry replaced the original receipt", sent.Receipts[1], want)
				}
			})
		}
	}
}

func TestUserTagEmptyTesterRetryWindowIsUnchanged(t *testing.T) {
	for _, state := range []string{"unknown", "queued"} {
		for _, tt := range []struct {
			name   string
			offset time.Duration
		}{{name: "one-hour", offset: -time.Hour}, {name: "future", offset: time.Minute}} {
			t.Run(state+"/"+tt.name, func(t *testing.T) {
				store, session, g := emptyTesterNoticeFixture(t, []string{})
				card, err := SessionCard(session)
				if err != nil {
					t.Fatal(err)
				}
				session.Receipts = []Receipt{{OpenID: "ou_self", Name: "本人", Key: "uat-card-original", Card: card, State: state, StartedAt: time.Now().Add(tt.offset).UTC()}}
				if err := store.save(session); err != nil {
					t.Fatal(err)
				}
				prepared, err := store.Prepare(session)
				if err != nil {
					t.Fatal("empty roster must not fail before retry-window validation", err)
				}
				out, err := store.SendTag(context.Background(), prepared.ID, prepared.RetryChoice(), g, userTagChecks(g, validateOK))
				if err == nil || !strings.Contains(err.Error(), "一小时去重窗口") || len(g.to) != 0 || !slices.Equal(out.Receipts, session.Receipts) {
					t.Fatal("empty-roster normalization bypassed retry boundary or changed history", out.Receipts, err, g.to)
				}
			})
		}
	}
}

func TestUserTagTesterSnapshotRetainsLegacyNilAndNonemptyHashes(t *testing.T) {
	// Frozen hashes of nil and nonempty rosters before empty-list normalization.
	for _, tt := range []struct {
		name, account, digest string
		testers               []string
	}{
		{name: "nil-legacy", digest: "6c484b0d2b91e2c95325fcdd7a0efff7c51bf34c9bb5ae78e8dcf254589bdb5d"},
		{name: "nil-bound", account: "fixture-meegle-account", digest: "5b4daacd599ec006c4181e8f6510f422d0aaf42ad50d6bbe9863d2cdc082c17e"},
		{name: "nonempty-legacy", testers: []string{"测试甲", "测试乙"}, digest: "1696c179ce7b756a0c9f09d5acea7f18770945225ad8ab5ef325d9912d93f3f9"},
		{name: "nonempty-bound", testers: []string{"测试甲", "测试乙"}, account: "fixture-meegle-account", digest: "47a0043f7dcc12fab8a400fcfccff02498074ee1cc61275aa28cf7db6d1ab11f"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, session, _ := emptyTesterNoticeFixture(t, tt.testers)
			session.RequirementIdentity = tt.account
			if got := session.RetryChoice().Snapshot; got != tt.digest {
				t.Fatalf("existing roster snapshot changed: got %s, want %s", got, tt.digest)
			}
			if tt.testers == nil {
				session.Testers = []string{}
				if got := session.RetryChoice().Snapshot; got != tt.digest {
					t.Fatalf("empty and omitted rosters have different snapshots: got %s, want %s", got, tt.digest)
				}
			}
		})
	}
}

func TestUserTagPendingTesterNormalizationRejectsRealChanges(t *testing.T) {
	changes := []struct {
		name   string
		mutate func(*Session)
	}{
		{name: "title", mutate: func(s *Session) { s.Title += " changed" }},
		{name: "testers", mutate: func(s *Session) { s.Testers = []string{"新测试负责人"} }},
		{name: "recipient-name", mutate: func(s *Session) { s.Recipients = []Recipient{{Name: "新姓名", OpenID: "ou_self"}} }},
		{name: "recipient-id", mutate: func(s *Session) {
			s.Recipients = []Recipient{{Name: "另一人", OpenID: "ou_other"}}
			s.BindTagOwner(s.Owner, s.Profile)
		}},
		{name: "warning", mutate: func(s *Session) { s.Warning += " changed" }},
		{name: "requirement-account", mutate: func(s *Session) { s.RequirementIdentity += " changed" }},
	}
	for _, state := range []string{"unknown", "queued"} {
		for _, testers := range [][]string{{}, {"原测试负责人"}} {
			for _, change := range changes {
				name := state + "/empty/" + change.name
				if len(testers) != 0 {
					name = state + "/nonempty/" + change.name
				}
				t.Run(name, func(t *testing.T) {
					store, session, _ := emptyTesterNoticeFixture(t, testers)
					session.RequirementIdentity = "original-meegle-account"
					session.Receipts = []Receipt{{OpenID: "ou_self", Key: "uat-card-original", Card: "original card", State: state, StartedAt: time.Now().UTC()}}
					if err := store.save(session); err != nil {
						t.Fatal(err)
					}
					path, _ := store.path(session.ID)
					before, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					changed := session
					change.mutate(&changed)
					if _, err := store.Prepare(changed); err == nil {
						t.Fatal("pending content or roster change accepted")
					}
					after, err := os.ReadFile(path)
					if err != nil || string(before) != string(after) {
						t.Fatal("rejected preparation changed original history", err)
					}
				})
			}
		}
	}
}
