package larkcli

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

func TestUserSendVerifiesLoginAndNeverFallsBackToBot(t *testing.T) {
	r := &fakeRunner{result: process.Result{Stdout: `{"appId":"cli_a95dcece863a9cce","identities":{"user":{"verified":true,"openId":"ou_self"},"bot":{"status":"missing"}}}`}}
	c := fakeClient(r)
	c.AsUser = true
	id, err := c.Identity(context.Background())
	if err != nil || id.ValidateUser() != nil || id.Name != "" {
		t.Fatal(id, err)
	}
	if !reflect.DeepEqual(r.last.Arguments, []string{"auth", "status", "--profile", "cli_a95dcece863a9cce", "--json", "--verify"}) {
		t.Fatal(r.last)
	}
	r.result = process.Result{Stdout: `{"ok":true,"data":{"message_id":"om_user"}}`}
	if got := c.Send(context.Background(), "ou_tester", "{}", "key"); got.MessageID != "om_user" {
		t.Fatal(got)
	}
	if strings.Contains(strings.Join(r.last.Arguments, " "), "--as bot") || !strings.Contains(strings.Join(r.last.Arguments, " "), "--as user") {
		t.Fatal(r.last.Arguments)
	}
	r.result = process.Result{ExitCode: 1, Stderr: `{"ok":false,"error":{"code":99991679,"message":"missing scope im:message.send_as_user"}}`}
	if got := c.Send(context.Background(), "ou_tester", "{}", "key"); got.Unknown || !strings.Contains(got.Error, "补充用户授权") {
		t.Fatal(got)
	}
	r.result = process.Result{ExitCode: 1, Stderr: `{"ok":false,"error":{"type":"authorization","subtype":"missing_scope","message":"missing required scope(s): im:message.send_as_user"}}`}
	if got := c.Send(context.Background(), "ou_tester", "{}", "key"); got.Unknown || !strings.Contains(got.Error, "补充用户授权") {
		t.Fatal(got)
	}
	r.result = process.Result{Stdout: `{"appId":"cli_a95dcece863a9cce","identities":{"user":{"verified":true,"openId":"ou_self","scope":"im:message"}}}`}
	if _, err = c.Identity(context.Background()); err == nil || !strings.Contains(err.Error(), "im:message.send_as_user") {
		t.Fatal(err)
	}
	r.result = process.Result{Stdout: `{"appId":"cli_a95dcece863a9cce","identities":{"user":{"verified":false,"openId":"ou_self","tokenStatus":"expired"}}}`}
	if _, err = c.Identity(context.Background()); err == nil {
		t.Fatal("expired accepted")
	}
}

func TestLocalUserIdentityChecksAccountAndPermissionsWithoutClaimingVerification(t *testing.T) {
	r := &fakeRunner{result: process.Result{Stdout: `{"appId":"cli_a95dcece863a9cce","identities":{"user":{"verified":true,"openId":"ou_self","scope":"im:message im:message.send_as_user","tokenStatus":"valid"}}}`}}
	c := fakeClient(r)
	c.AsUser, c.VerifyUser = true, true
	id, err := c.LocalIdentity(context.Background())
	if err != nil || id.OpenID != "ou_self" || id.UserVerified || id.ValidateUser() == nil {
		t.Fatal("local status became online evidence", id, err)
	}
	if !reflect.DeepEqual(r.last.Arguments, []string{"auth", "status", "--profile", "cli_a95dcece863a9cce", "--json"}) {
		t.Fatal(r.last.Arguments)
	}
	for _, raw := range []string{
		`{"appId":"other","identities":{"user":{"openId":"ou_self"}}}`,
		`{"appId":"cli_a95dcece863a9cce","identities":{"user":{"openId":""}}}`,
		`{"appId":"cli_a95dcece863a9cce","identities":{"user":{"openId":"ou_self","tokenStatus":"expired"}}}`,
		`{"appId":"cli_a95dcece863a9cce","identities":{"user":{"openId":"ou_self","scope":"im:message"}}}`,
	} {
		r.result.Stdout = raw
		if _, err := c.LocalIdentity(context.Background()); err == nil {
			t.Fatal("invalid local account accepted", raw)
		}
	}
}
func TestTesterResolutionExactUniqueAndConfiguredPriority(t *testing.T) {
	cases := []struct {
		name, body string
		ok         bool
	}{
		{"exact", `{"ok":true,"data":{"users":[{"open_id":"ou_a","localized_name":"测试甲","is_activated":true}]}}`, true},
		{"alias", `{"ok":true,"data":{"users":[{"open_id":"ou_a","localized_name":"Tester","match_segments":["测试甲"],"is_activated":true}]}}`, true},
		{"ambiguous", `{"ok":true,"data":{"users":[{"open_id":"ou_a","localized_name":"测试甲","is_activated":true},{"open_id":"ou_b","localized_name":"测试甲","is_activated":true}]}}`, false},
		{"partial", `{"ok":true,"data":{"has_more":true,"users":[{"open_id":"ou_a","localized_name":"测试甲","is_activated":true}]}}`, false},
		{"substring", `{"ok":true,"data":{"users":[{"open_id":"ou_a","localized_name":"测试甲乙","is_activated":true}]}}`, false},
		{"inactive", `{"ok":true,"data":{"users":[{"open_id":"ou_a","localized_name":"测试甲","is_activated":false}]}}`, false},
		{"external", `{"ok":true,"data":{"users":[{"open_id":"ou_a","localized_name":"测试甲","is_activated":true,"is_cross_tenant":true}]}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeRunner{result: process.Result{Stdout: tc.body}}
			c := fakeClient(r)
			c.AsUser = true
			got, err := c.ResolveTester(context.Background(), "测试甲")
			if (err == nil) != tc.ok || tc.ok && got.OpenID != "ou_a" {
				t.Fatal(got, err)
			}
		})
	}
	r := &fakeRunner{}
	c := fakeClient(r)
	c.AsUser = true
	cfg := c.Config.EffectiveLarkNotice()
	cfg.Contacts = []domain.LarkNoticeContact{{Name: "测试甲", OpenID: "ou_configured"}}
	c.Config.LarkNotice = &cfg
	got, err := c.ResolveTester(context.Background(), "测试甲")
	if err != nil || got.OpenID != "ou_configured" || len(r.last.Arguments) > 0 {
		t.Fatal(got, err, r.last)
	}
	cfg.Contacts = append(cfg.Contacts, domain.LarkNoticeContact{Name: "测试甲", OpenID: "ou_other"})
	if _, err = c.ResolveTester(context.Background(), "测试甲"); err == nil {
		t.Fatal("ambiguous configuration accepted")
	}
}
