package application

import (
	"archive/zip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/snowball/auth"
	"silverwing-go/internal/snowball/genbuapi"
)

type platformFixtureTransport func(*http.Request) (*http.Response, error)

func (f platformFixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func platformFixtureResponse(body string) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
}

func platformFixtureJWT() string {
	return "eyJhbGciOiJIUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, `{"exp":%d}`, time.Now().Add(time.Hour).Unix())) + ".fixture"
}

func isolatePlatformUserHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func saveFixturePlatformAccount(t *testing.T) {
	t.Helper()
	home := isolatePlatformUserHome(t)
	store := auth.Store{Directory: filepath.Join(home, ".snowball-config")}
	if _, err := store.Save(auth.Credentials{Username: "fixture", Password: "fixture-password"}); err != nil {
		t.Fatal(err)
	}
}

// installFixtureGenbuHTTP replaces the transport, not the application adapter.
// No synthetic request can connect to any real service.
func installFixtureGenbuHTTP(t *testing.T, a *App, services []string, step int, onVersion func() error) {
	t.Helper()
	saveFixturePlatformAccount(t)
	token := platformFixtureJWT()
	apps := make([]map[string]any, 0, len(services))
	for i, name := range services {
		apps = append(apps, map[string]any{"app_name": name, "app_id": i + 1})
	}
	a.connections.transport = platformFixtureTransport(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case genbuapi.LoginPath:
			return platformFixtureResponse(`{"code":"000000","data":{"token":"beast-fixture"}}`)
		case genbuapi.DragonUserPath:
			return platformFixtureResponse(fmt.Sprintf(`{"code":20000,"token":%q}`, token))
		case genbuapi.ApplicationsPath:
			data, err := json.Marshal(map[string]any{"code": 20000, "response": apps})
			if err != nil {
				return nil, err
			}
			return platformFixtureResponse(string(data))
		case genbuapi.VersionPath:
			if onVersion != nil {
				if err := onVersion(); err != nil {
					return nil, err
				}
			}
			var request struct {
				Conditions struct {
					Tag string `json:"key_word"`
				}
			}
			if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
				return nil, err
			}
			return platformFixtureResponse(fmt.Sprintf(`{"code":20000,"response":[{"version_id":1,"version_name":%q}]}`, request.Conditions.Tag))
		case genbuapi.BaseInfoPath:
			return platformFixtureResponse(fmt.Sprintf(`{"code":20000,"response":[{"step":1,"status":%d},{"step":3,"status":0}]}`, step))
		default:
			return nil, fmt.Errorf("unexpected fixture request %s %s", req.Method, req.URL.Path)
		}
	})
}

func TestEmbeddedPlatformSettingsAndBothLoginsIgnoreLegacyExecutables(t *testing.T) {
	ctx := context.Background()
	home := isolatePlatformUserHome(t)
	a := newOptionalCLIApp(t)
	// An obsolete executable path must neither enable the integrations nor be
	// resolved by a version query. Platform package tests cover direct CLI calls.
	missing := filepath.Join(t.TempDir(), "old-embedded-command.exe")
	cfg := domain.DefaultAppConfig()
	cfg.GenbuExecutablePath, cfg.DragonExecutablePath = &missing, &missing
	if err := a.Config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	legacyAccount := filepath.Join(a.Paths.Home, "genbu.json")
	if err := os.WriteFile(legacyAccount, []byte(`{"version":1,"username":"obsolete","password":"obsolete fixture"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	initial, err := a.PlatformConnections(ctx)
	if err != nil || initial.Configured || initial.GenbuEnabled || initial.DragonEnabled {
		t.Fatalf("legacy paths enabled platforms: %+v %v", initial, err)
	}
	var logins []string
	a.connections.transport = platformFixtureTransport(func(req *http.Request) (*http.Response, error) {
		logins = append(logins, req.URL.Path)
		switch req.URL.Path {
		case genbuapi.LoginPath:
			var form map[string]string
			if err := json.NewDecoder(req.Body).Decode(&form); err != nil || form["username"] != "fixture" || form["password"] != "sensitive fixture password" {
				return nil, errors.New("shared credentials mismatch")
			}
			return platformFixtureResponse(`{"code":"000000","data":{"token":"beast-fixture"}}`)
		case genbuapi.DragonUserPath:
			return platformFixtureResponse(fmt.Sprintf(`{"code":20000,"token":%q}`, platformFixtureJWT()))
		case "/dragon/api/admin/user/beastUser":
			return platformFixtureResponse(fmt.Sprintf(`{"code":"000000","data":{"token":%q,"user":{"userId":5}}}`, platformFixtureJWT()))
		default:
			return nil, errors.New("unexpected request")
		}
	})
	result, err := a.Dispatch(ctx, Action{Kind: "platform.credentials.save", Payload: map[string]any{"username": " fixture ", "password": "sensitive fixture password"}})
	if err != nil || !result.Data.(PlatformConnections).Configured || len(logins) != 0 {
		t.Fatalf("account save did not remain local: %+v %v", result, err)
	}
	credentials, err := loadPlatformAccount()
	if err != nil || credentials.Password != "sensitive fixture password" {
		t.Fatal("password was not stored in the shared Snowball account file", err)
	}
	if data, err := json.Marshal(result); err != nil || strings.Contains(string(data), credentials.Password) {
		t.Fatal("saving credentials exposed a password in the action result", err)
	}
	genbuOpt, err := a.ProbeOptionalCLI(ctx, "genbu", true)
	if err != nil || !genbuOpt.Ready() || genbuOpt.Command != "" || genbuOpt.Version != "" {
		t.Fatalf("embedded readiness depended on old path: %+v %v", genbuOpt, err)
	}
	for _, platform := range []string{"genbu", "dragon"} {
		if _, err := a.Dispatch(ctx, Action{Kind: "platform.login.test", ServiceID: platform}); err != nil {
			t.Fatal(platform, err)
		}
	}
	if len(logins) != 4 || logins[1] != genbuapi.DragonUserPath || logins[3] != "/dragon/api/admin/user/beastUser" {
		t.Fatalf("wrong platform session exchanges: %v", logins)
	}
	genbuScope, err := a.platformAdmission(ctx, "genbu")
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		if _, err := a.Dispatch(ctx, Action{Kind: "platform.connection.set", ServiceID: "dragon", Payload: map[string]any{"enabled": enabled}}); err != nil {
			t.Fatal(err)
		}
		if err := genbuScope(ctx); err != nil {
			t.Fatal("Dragon switch retired Genbu", err)
		}
	}
	if _, err := a.Dispatch(ctx, Action{Kind: "platform.connection.set", ServiceID: "genbu", Payload: map[string]any{"enabled": false}}); err != nil {
		t.Fatal(err)
	}
	if err := genbuScope(ctx); !errors.Is(err, errPlatformScopeChanged) {
		t.Fatal("disabled Genbu scope retained admission", err)
	}
	if _, err := a.Dispatch(ctx, Action{Kind: "platform.login.test", ServiceID: "genbu"}); err == nil || len(logins) != 4 {
		t.Fatal("disabled platform sent a network request", err)
	}
	other := newOptionalCLIApp(t)
	if status, err := other.PlatformConnections(ctx); err != nil || !status.Configured || status.Username != "fixture" || !status.GenbuEnabled || !status.DragonEnabled {
		t.Fatal("shared account or this profile's independent default switches are incorrect", status, err)
	}
	backup := filepath.Join(t.TempDir(), "settings.zip")
	if _, err := a.Config.ExportTo(backup); err != nil {
		t.Fatal(err)
	}
	zipped, err := zip.OpenReader(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer zipped.Close()
	for _, file := range zipped.File {
		if filepath.Base(file.Name) == auth.FileName {
			t.Fatal("account file was exported")
		}
		r, openErr := file.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		data, readErr := io.ReadAll(r)
		_ = r.Close()
		if readErr != nil || strings.Contains(string(data), credentials.Password) {
			t.Fatal("configuration export contained a platform secret", readErr)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".snowball-config", "credentials.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(a.Paths.Home, auth.FileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("account file was written to the profile", err)
	}
	if data, err := os.ReadFile(legacyAccount); err != nil || !strings.Contains(string(data), "obsolete fixture") {
		t.Fatal("the previous profile account file changed", err)
	}
}

func TestPlatformPasswordRequiresCurrentAccountRevision(t *testing.T) {
	ctx := context.Background()
	home := isolatePlatformUserHome(t)
	a := newOptionalCLIApp(t)
	var reader PlatformPasswordBackend = a
	initial, err := a.PlatformConnections(ctx)
	if err != nil || initial.Configured {
		t.Fatal("expected an unconfigured account", err)
	}
	if password, err := reader.PlatformPassword(ctx, initial.Revision); password != "" || !errors.Is(err, errPlatformScopeChanged) {
		t.Fatal("reading an unconfigured account did not fail", err)
	}
	store := auth.Store{Directory: filepath.Join(home, ".snowball-config")}
	account := auth.Credentials{Username: "fixture", Password: " first fixture password "}
	if _, err := store.Save(account); err != nil {
		t.Fatal(err)
	}
	result, err := a.Dispatch(ctx, Action{Kind: "platform.connections"})
	if err != nil {
		t.Fatal(err)
	}
	saved := result.Data.(PlatformConnections)
	if !saved.Configured || saved.Username != account.Username {
		t.Fatal("shared account did not appear in platform metadata")
	}
	if data, err := json.Marshal(result); err != nil || strings.Contains(string(data), account.Password) {
		t.Fatal("platform metadata exposed a password", err)
	}
	if password, err := reader.PlatformPassword(ctx, saved.Revision); password != account.Password || err != nil {
		t.Fatal("reader did not return the exact saved password", err)
	}

	// An external edit must invalidate the old revision even before another metadata read.
	account.Password = "second fixture password"
	if _, err := store.Save(account); err != nil {
		t.Fatal(err)
	}
	if password, err := reader.PlatformPassword(ctx, saved.Revision); password != "" || !errors.Is(err, errPlatformScopeChanged) {
		t.Fatal("stale account revision did not fail", err)
	}
	current, err := a.PlatformConnections(ctx)
	if err != nil || current.Revision == saved.Revision {
		t.Fatal("updated account did not receive a new revision", err)
	}
	if password, err := reader.PlatformPassword(ctx, current.Revision); password != account.Password || err != nil {
		t.Fatal("reader did not return the updated password", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if password, err := reader.PlatformPassword(cancelled, current.Revision); password != "" || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled password read did not fail", err)
	}
}

func TestEmbeddedPlatformAccountEditRetiresBothScopes(t *testing.T) {
	ctx := context.Background()
	a := newOptionalCLIApp(t)
	saveFixturePlatformAccount(t)
	before, err := a.PlatformConnections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	genbu, err := a.platformAdmission(ctx, "genbu")
	if err != nil {
		t.Fatal(err)
	}
	dragon, err := a.platformAdmission(ctx, "dragon")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Dispatch(ctx, Action{Kind: "platform.credentials.save", Payload: map[string]any{"username": "fixture", "password": ""}}); err != nil {
		t.Fatal("saving the unchanged username with a blank password failed", err)
	}
	if genbu(ctx) != nil || dragon(ctx) != nil {
		t.Fatal("saving an unchanged account retired an active scope")
	}
	other := newOptionalCLIApp(t)
	if _, err := other.Dispatch(ctx, Action{Kind: "platform.credentials.save", Payload: map[string]any{"username": "fixture", "password": "new fixture password"}}); err != nil {
		t.Fatal(err)
	}
	if status, err := a.PlatformConnections(ctx); err != nil || status.Revision == before.Revision ||
		!errors.Is(genbu(ctx), errPlatformScopeChanged) || !errors.Is(dragon(ctx), errPlatformScopeChanged) {
		t.Fatal("credential change in another instance left an old scope active", err)
	}
	reopened, err := newApp(a.Paths.Home)
	if err != nil {
		t.Fatal(err)
	}
	status, err := reopened.PlatformConnections(ctx)
	if err != nil || !status.Configured || status.Username != "fixture" {
		t.Fatal("edited account did not survive reopening", err)
	}
}

func TestEmbeddedPlatformOlderConfigReadCannotRetireCurrentScope(t *testing.T) {
	ctx := context.Background()
	a := newOptionalCLIApp(t)
	saveFixturePlatformAccount(t)
	disabled := setTestGenbuEnabled(t, a, false)
	if _, err := a.Dispatch(ctx, Action{Kind: "platform.connection.set", ServiceID: "genbu", Payload: map[string]any{"enabled": true}}); err != nil {
		t.Fatal(err)
	}
	check, err := a.platformAdmission(ctx, "genbu")
	if err != nil {
		t.Fatal(err)
	}
	if a.platformReadyFor(disabled, "genbu") || check(ctx) != nil {
		t.Fatal("reading an old disabled configuration retired the current Genbu scope")
	}
}
