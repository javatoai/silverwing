package releasenotice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type observedUserGateway struct {
	*userGateway
	identityCalls int
	afterSend     func()
}

func (g *observedUserGateway) Identity(ctx context.Context) (Identity, error) {
	g.identityCalls++
	return g.userGateway.Identity(ctx)
}

func (g *observedUserGateway) Send(ctx context.Context, to, card, key string) SendResult {
	answer := g.userGateway.Send(ctx, to, card, key)
	if g.afterSend != nil {
		g.afterSend()
	}
	return answer
}

func TestUserTagChecksRefreshOncePerRPCAndNeverReuseAcrossRPCs(t *testing.T) {
	store, saved, gateway := userNoticeFixture(t)
	g := &observedUserGateway{userGateway: gateway}
	clock := time.Now()
	refreshes, guards := 0, 0
	g.afterSend = func() { clock = clock.Add(10 * time.Minute) }
	checks := TagSendChecks{
		Refresh: func(context.Context, Session) (Identity, error) {
			refreshes++
			return saved.Identity, nil
		},
		BeforeSend: func(context.Context, Session) error { guards++; return nil },
		now:        func() time.Time { return clock },
	}
	if _, err := store.SendTag(context.Background(), saved.ID, saved.RetryChoice(), g, checks); err != nil {
		t.Fatal(err)
	}
	if refreshes != 2 || guards != 2 || g.identityCalls != 0 || len(g.to) != 2 {
		t.Fatal("redundant verification or evidence reused across RPC", refreshes, guards, g.identityCalls, g.to)
	}
	// A new call, including CLI skip-success, must obtain fresh verification.
	if _, err := store.SendTag(context.Background(), saved.ID, saved.RetryChoice(), g, checks); err != nil {
		t.Fatal(err)
	}
	if refreshes != 3 || guards != 3 || len(g.to) != 2 {
		t.Fatal("verification persisted across requests", refreshes, guards, g.to)
	}
}

func TestUserTagFirstCheckExpiresBeforeRPCAndKeepsReservedKeys(t *testing.T) {
	for _, elapsed := range []time.Duration{5 * time.Second, -time.Second} {
		t.Run(elapsed.String(), func(t *testing.T) {
			store, saved, g := userNoticeFixture(t)
			clock := time.Now()
			checks := TagSendChecks{
				Refresh: func(context.Context, Session) (Identity, error) { return saved.Identity, nil },
				BeforeSend: func(context.Context, Session) error {
					clock = clock.Add(elapsed)
					return nil
				},
				now: func() time.Time { return clock },
			}
			out, err := store.SendTag(context.Background(), saved.ID, saved.RetryChoice(), g, checks)
			if err == nil || !strings.Contains(err.Error(), "过期") || len(g.to) != 0 || len(out.Receipts) != 2 || out.Receipts[0].State != "queued" {
				t.Fatal(out, err, g.to)
			}
			originalKey := out.Receipts[0].Key
			checks.BeforeSend = validateOK
			if _, err = store.SendTag(context.Background(), saved.ID, saved.RetryChoice(), g, checks); err != nil || len(g.to) != 2 || g.keys[0] != originalKey {
				t.Fatal("retry lost reserved keys", err, g.keys)
			}
		})
	}
}

func TestUserTagMandatoryChecksAndFinalGuardCannotBeBypassed(t *testing.T) {
	for _, mode := range []string{"no-refresh", "no-guard", "local-only", "guard-error", "guard-cancel", "refresh-cancel", "account-between-recipients"} {
		t.Run(mode, func(t *testing.T) {
			store, saved, g := userNoticeFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			refreshes := 0
			checks := TagSendChecks{
				Refresh: func(context.Context, Session) (Identity, error) {
					refreshes++
					identity := saved.Identity
					if mode == "local-only" {
						identity.UserVerified = false
					}
					if mode == "refresh-cancel" {
						cancel()
					}
					if mode == "account-between-recipients" && refreshes == 2 {
						identity.OpenID = "ou_changed"
					}
					return identity, nil
				},
				BeforeSend: func(context.Context, Session) error {
					if mode == "guard-cancel" {
						cancel()
					}
					if mode == "guard-error" {
						return errors.New("config changed")
					}
					return nil
				},
			}
			if mode == "no-refresh" {
				checks.Refresh = nil
			}
			if mode == "no-guard" {
				checks.BeforeSend = nil
			}
			out, err := store.SendTag(ctx, saved.ID, saved.RetryChoice(), g, checks)
			want := 0
			if mode == "account-between-recipients" {
				want = 1
				if out.Deliveries()[1].State != "queued" {
					t.Fatal("unstarted recipient lost its reservation", out)
				}
			}
			if err == nil || len(g.to) != want {
				t.Fatal(out, err, g.to)
			}
		})
	}
}

func TestUserTagBatchLockIsHeldAcrossProcesses(t *testing.T) {
	store, saved, _ := userNoticeFixture(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	err = store.lockTag(saved, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		child := exec.CommandContext(ctx, executable, "-test.run=^TestUserTagLockProcessHelper$")
		child.Env = append(os.Environ(), "SW_NOTICE_LOCK_DIRECTORY="+store.Directory, "SW_NOTICE_LOCK_ID="+saved.ID)
		output, err := child.CombinedOutput()
		if err != nil || !strings.Contains(string(output), "LOCK_REJECTED") {
			return fmt.Errorf("another process bypassed batch lock: %v: %s", err, output)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUserTagLockProcessHelper(t *testing.T) {
	directory := os.Getenv("SW_NOTICE_LOCK_DIRECTORY")
	if directory == "" {
		t.Skip("isolated child only")
	}
	store := Store{Directory: directory}
	saved, err := store.load(os.Getenv("SW_NOTICE_LOCK_ID"))
	if err != nil {
		t.Fatal(err)
	}
	// Changing roster/session ID must still contend for the same batch lock.
	saved.Recipients = append(saved.Recipients, Recipient{Name: "新增", OpenID: "ou_extra"})
	saved.BindTagOwner(saved.Owner, saved.Profile)
	if _, err := store.Prepare(saved); err == nil {
		t.Fatal("recipient change bypassed another process's batch lock")
	}
	fmt.Println("LOCK_REJECTED")
}
