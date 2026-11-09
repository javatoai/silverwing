package codexprotocol

import (
	"context"
	"errors"
	"testing"
)

func TestActivationUsesMatchingUserContextWithoutReplay(t *testing.T) {
	for _, mode := range []string{"ordinary", "administrator", "desktop-failure", "unknown-result", "permission-failure", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			nativeCalls, desktopCalls, permissionCalls := 0, 0, 0
			if mode == "canceled" {
				cancel()
			}
			uri := "codex://threads/new?path=D%3A%5Ctasks%5C%E9%80%80%E5%8D%A1%E5%9C%BA%E6%99%AF%E5%AF%B9%E8%B4%A6"
			elevation := func() (bool, error) {
				permissionCalls++
				if mode == "permission-failure" {
					return false, errors.New("token denied")
				}
				return mode != "ordinary", nil
			}
			direct := func(context.Context, string) error { nativeCalls++; return nil }
			desktop := func(_ context.Context, got string) error {
				desktopCalls++
				if got != uri {
					t.Fatal("URI changed during desktop handoff", got)
				}
				if mode == "desktop-failure" {
					return errors.New("desktop unavailable")
				}
				if mode == "unknown-result" {
					return context.DeadlineExceeded
				}
				return nil
			}
			err := activateInUserContext(ctx, uri, elevation, direct, desktop)
			wantFailure := mode != "ordinary" && mode != "administrator"
			if (err != nil) != wantFailure {
				t.Fatal("activation result lost", err)
			}
			wantNative, wantDesktop := 0, 0
			if mode == "ordinary" {
				wantNative = 1
			}
			if mode == "administrator" || mode == "desktop-failure" || mode == "unknown-result" {
				wantDesktop = 1
			}
			if nativeCalls != wantNative || desktopCalls != wantDesktop || mode == "canceled" && permissionCalls != 0 {
				t.Fatal("wrong route or replay", nativeCalls, desktopCalls, permissionCalls)
			}
		})
	}
}
