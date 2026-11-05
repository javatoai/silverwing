package larkcli

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

type fakeRunner struct {
	last   process.Spec
	result process.Result
	err    error
}

func (r *fakeRunner) Run(_ context.Context, s process.Spec) (process.Result, error) {
	r.last = s
	return r.result, r.err
}
func fakeClient(r *fakeRunner) Client {
	cfg := domain.DefaultAppConfig()
	path := "C:/synthetic/lark-cli.exe"
	cfg.LarkExecutablePath = &path
	proxy := "http://proxy.invalid:9999"
	cfg.CommandProxyURL = &proxy
	cfg.CommandProxyTargets = []domain.CommandProxyTarget{domain.ProxyTargetGenbu, domain.ProxyTargetMeegle}
	return Client{Runner: r, Config: cfg}
}
func TestIdentityReadsCachedNameDespiteExpiredPersonalToken(t *testing.T) {
	r := &fakeRunner{result: process.Result{Stdout: `{"appId":"cli_a95dcece863a9cce","identities":{"user":{"status":"missing","tokenStatus":"expired","userName":"动态用户","openId":"ou_dynamic"},"bot":{"status":"ready"}}}`}}
	c := fakeClient(r)
	id, e := c.Identity(context.Background())
	if e != nil || id.UserState != "expired" || id.Name != "动态用户" || id.OpenID != "ou_dynamic" || !id.BotReady {
		t.Fatal(id, e)
	}
	if !reflect.DeepEqual(r.last.Arguments, []string{"auth", "status", "--profile", "cli_a95dcece863a9cce", "--json"}) {
		t.Fatal(r.last.Arguments)
	}
	if len(r.last.RemoveEnv) != 0 {
		t.Fatal("SW proxy modified inherited environment", r.last.RemoveEnv)
	}
	for k, v := range r.last.Env {
		if strings.Contains(strings.ToLower(k), "proxy") || strings.Contains(v, "proxy.invalid") {
			t.Fatal(k, v)
		}
	}
	r.result.Stdout = strings.ReplaceAll(r.result.Stdout, "cli_a95dcece863a9cce", "wrong_app")
	if _, e = c.Identity(context.Background()); e == nil {
		t.Fatal("wrong application accepted")
	}
}
func TestSendStrictReceiptAndBotArguments(t *testing.T) {
	r := &fakeRunner{result: process.Result{Stdout: `{"ok":true,"data":{"message_id":"om_fake"}}`}}
	c := fakeClient(r)
	card := `{"schema":"2.0","text":"中文 \"quoted\" $literal"}`
	answer := c.Send(context.Background(), "ou_receiver", card, "uat-card-fixture")
	if answer.MessageID != "om_fake" || answer.Error != "" || answer.Unknown {
		t.Fatal(answer)
	}
	want := []string{"im", "+messages-send", "--profile", "cli_a95dcece863a9cce", "--as", "bot", "--user-id", "ou_receiver", "--msg-type", "interactive", "--content", card, "--idempotency-key", "uat-card-fixture"}
	if !reflect.DeepEqual(r.last.Arguments, want) || r.last.Timeout != time.Minute {
		t.Fatal(r.last)
	}
	for _, raw := range []string{`{"ok":false,"data":{"message_id":"om_fake"}}`, `{"ok":true,"data":{}}`, "not-json"} {
		r.result = process.Result{Stdout: raw}
		if a := c.Send(context.Background(), "ou_receiver", card, "key"); a.MessageID != "" || a.Error == "" {
			t.Fatal(a)
		}
	}
	r.result = process.Result{ExitCode: 1, Stderr: `{"ok":false,"error":{"code":230013,"message":"Bot has NO availability to this user"}}`}
	if a := c.Send(context.Background(), "ou_receiver", card, "key"); a.Unknown || !strings.Contains(a.Error, "可用范围") || strings.Contains(a.Error, "OAuth") {
		t.Fatal(a)
	}
	r.err = errors.New("timeout")
	if a := c.Send(context.Background(), "ou_receiver", card, "key"); !a.Unknown {
		t.Fatal(a)
	}
}

func TestSendRejectsGroupAndInvalidTargets(t *testing.T) {
	r := &fakeRunner{}
	c := fakeClient(r)
	for _, to := range []string{"", "oc_group", "not-an-open-id"} {
		if out := c.Send(context.Background(), to, "{}", "key"); out.Error == "" || out.Unknown || len(r.last.Arguments) != 0 {
			t.Fatal(to, out, r.last)
		}
	}
}
