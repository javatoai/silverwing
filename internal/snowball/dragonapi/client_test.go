package dragonapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testToken() string {
	return "eyJhbGciOiJIUzI1NiJ9." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, time.Now().Add(time.Hour).Unix()))) + ".test"
}

func reply(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": "000000", "data": data})
}

func testTemplate() map[string]any {
	return map[string]any{
		"process":                    map[string]any{"processTemplateId": 24, "name": "提测审批", "classifyId": 4},
		"processTemplateStructureId": 987,
		"nodes": []any{
			map[string]any{"id": "new-start", "clazz": "start", "label": "提交申请", "ccApprover": true},
			map[string]any{"id": "new-env-gateway", "clazz": "exclusiveGateway", "label": "环境区分"},
		},
		"edges": []any{map[string]any{"source": "new-start", "target": "new-env-gateway", "label": "提交"}},
		"tpls": []any{map[string]any{"formTemplateId": 86, "content": map[string]any{
			"list":   []any{map[string]any{"el": "test-approval", "model": "testApproval", "options": map[string]any{"defaultValue": map[string]any{"env": "PRE", "type": "其他", "businessLine": "CS"}}}},
			"config": map[string]any{"ui": "element"},
		}}},
	}
}

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(Config{BeastURL: server.URL, DragonURL: server.URL, Timeout: time.Second,
		Username: "tester", Password: "secret-password", Token: "cached-token", TokenExpiresAt: time.Now().Add(time.Hour).Unix(), UserID: 170})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestCreateUsesCurrentWorkflowAndEmbeddedApplications(t *testing.T) {
	var templateCalls, createCalls int
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-token") != "cached-token" || r.Header.Get("x-user-id") != "170" {
			t.Error("missing Dragon authentication headers")
		}
		switch r.URL.Path {
		case templatePath:
			templateCalls++
			if r.Method != http.MethodGet {
				t.Error("template must be read only")
			}
			reply(w, testTemplate())
		case approvalPath:
			createCalls++
			if r.Method != http.MethodPost {
				t.Error("create must POST")
			}
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
				return
			}
			if payload["processTemplateId"] != float64(24) || payload["processTemplateStructureId"] != float64(987) || payload["source"] != "new-start" {
				t.Errorf("workflow IDs were not taken from live template: %v", payload)
			}
			state := payload["state"].([]any)[0].(map[string]any)
			if state["id"] != "new-env-gateway" || state["label"] != "环境区分" {
				t.Errorf("incorrect initial state: %v", state)
			}
			tpls := payload["tpls"].(map[string]any)
			if tpls["content"].([]any)[0].(map[string]any)["id"] != float64(86) {
				t.Error("form ID must use current template")
			}
			form := tpls["formData"].([]any)[0].(map[string]any)["testApproval"].(map[string]any)
			for key, want := range map[string]string{"type": "Sprint 提测", "env": "UAT", "businessLine": "OBT", "meegoCode": "7118490426", "remark": "第一行\n第二行"} {
				if form[key] != want {
					t.Errorf("%s = %v, want %s", key, form[key], want)
				}
			}
			wantRows := []map[string]string{
				{"appId": "11020028", "appName": "bp-api-service", "appCnName": "运营外部接口", "version": "最新"},
				{"appId": "11020024", "appName": "bp-operate-app", "appCnName": "OBOS管理系统前端服务", "version": "最新"},
				{"appId": "11020023", "appName": "bp-operation-center", "appCnName": "雪球saas平台运营系统后端服务", "version": "最新"},
			}
			rows := form["overviewData"].([]any)
			if len(rows) != len(wantRows) {
				t.Errorf("unexpected rows: %v", rows)
				return
			}
			for i, want := range wantRows {
				for key, value := range want {
					if rows[i].(map[string]any)[key] != value {
						t.Errorf("row %d %s mismatch", i, key)
					}
				}
			}
			for _, key := range []string{"sqlData", "jiraCode", "codeUser"} {
				if len(form[key].([]any)) != 0 {
					t.Errorf("%s must be empty", key)
				}
			}
			if form["onlineStepData"] != onlineSteps || form["rollbackStepData"] != rollbackSteps {
				t.Error("release steps changed")
			}
			reply(w, map[string]any{"approvalId": 12345})
		default:
			t.Errorf("unexpected API call: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	result, err := client.Create(context.Background(), CreateInput{Meego: "7118490426", Remark: "第一行\n第二行", Apps: []string{"API", "app", "opc"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.ApprovalID != 12345 || !reflect.DeepEqual(result.Apps, []string{"api", "app", "opc"}) || templateCalls != 1 || createCalls != 1 {
		t.Fatalf("unexpected result/calls: %#v %d %d", result, templateCalls, createCalls)
	}
	if result.URL != client.config.DragonURL+"/#/workorder/detail/12345" {
		t.Fatalf("create returned incorrect detail URL: %s", result.URL)
	}
}

func TestInvalidCreateInputsNeverCallNetwork(t *testing.T) {
	client := testClient(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid input reached network") })
	for _, input := range []CreateInput{
		{Meego: "", Apps: []string{"api"}}, {Meego: "123", Apps: nil}, {Meego: "123", Apps: []string{"unknown"}},
		{Meego: "123", Apps: []string{"api", "bp-api-service"}}, {Meego: "123", Apps: []string{"api", ""}},
	} {
		var failure *CreateError
		if _, err := client.Create(context.Background(), input); !errors.As(err, &failure) || failure.PossiblyCreated {
			t.Errorf("invalid input %#v produced unexpected failure %v", input, err)
		}
	}
}

func TestChangedTemplateDoesNotSubmit(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != templatePath {
			t.Error("must not submit changed template")
			w.WriteHeader(400)
			return
		}
		data := testTemplate()
		data["tpls"] = []any{}
		reply(w, data)
	})
	var failure *CreateError
	if _, err := client.Create(context.Background(), CreateInput{Meego: "123", Apps: []string{"api"}}); !errors.As(err, &failure) || failure.PossiblyCreated {
		t.Fatalf("changed template produced unexpected failure %v", err)
	}
}

func TestAuthenticationRefreshOnlyRepeatsReadRequest(t *testing.T) {
	var reads, logins, writes int
	fresh := testToken()
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case loginPath:
			logins++
			reply(w, map[string]any{"token": "beast-token"})
		case exchangePath:
			if r.URL.Query().Get("token") != "beast-token" {
				t.Error("wrong exchange token")
			}
			reply(w, map[string]any{"token": fresh, "user": map[string]any{"userId": 170}})
		case templatePath:
			reads++
			if reads == 1 {
				fmt.Fprint(w, `{"code":"000401","message":"expired","data":{"reload":true}}`)
				return
			}
			if r.Header.Get("x-token") != fresh {
				t.Error("read did not use refreshed token")
			}
			reply(w, testTemplate())
		case approvalPath:
			writes++
			fmt.Fprint(w, `{"code":"000401","message":"rejected","data":{"reload":true}}`)
		}
	})
	_, err := client.Create(context.Background(), CreateInput{Meego: "123", Apps: []string{"api"}})
	var failure *CreateError
	if !errors.As(err, &failure) || failure.PossiblyCreated || reads != 2 || logins != 1 || writes != 1 {
		t.Fatalf("unexpected retry behavior: err=%v reads=%d logins=%d writes=%d", err, reads, logins, writes)
	}
	if client.SessionSnapshot().Token != fresh {
		t.Error("refreshed token not retained")
	}
}

func TestCreateResponseClassificationAndNoReplay(t *testing.T) {
	for _, test := range []struct {
		name     string
		status   int
		response string
		possible bool
		id       int64
	}{
		{name: "malformed response", status: 200, response: `not-json`, possible: true},
		{name: "missing approval ID", status: 200, response: `{"code":"000000","data":{}}`, possible: true},
		{name: "missing response code", status: 200, response: `{"data":{"approvalId":19001}}`, possible: true, id: 19001},
		{name: "explicit rejection", status: 200, response: `{"code":"000401","message":"rejected"}`},
		{name: "bad request", status: 400},
		{name: "redirect", status: 303, possible: true},
		{name: "request timeout", status: 408, possible: true},
		{name: "server failure", status: 500, possible: true},
		{name: "server failure with receipt", status: 500, response: `{"code":"000500","data":{"approvalId":19001}}`, possible: true, id: 19001},
	} {
		t.Run(test.name, func(t *testing.T) {
			var writes int
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == templatePath {
					reply(w, testTemplate())
					return
				}
				writes++
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.response)
			})
			result, err := client.Create(context.Background(), CreateInput{Meego: "123", Apps: []string{"api"}})
			var failure *CreateError
			if !errors.As(err, &failure) || failure.PossiblyCreated != test.possible || result.ApprovalID != test.id || writes != 1 {
				t.Fatalf("result=%+v err=%v writes=%d", result, err, writes)
			}
		})
	}
}

func TestCreateAdmissionRunsAgainAfterTemplateRead(t *testing.T) {
	var templateRead atomic.Bool
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != templatePath {
			t.Errorf("unexpected request after scope change: %s %s", r.Method, r.URL.Path)
			return
		}
		templateRead.Store(true)
		reply(w, testTemplate())
	})
	blocked := errors.New("approval scope changed")
	client.config.CheckAdmission = func(context.Context) error {
		if templateRead.Load() {
			return blocked
		}
		return nil
	}
	_, err := client.Create(context.Background(), CreateInput{Meego: "123", Apps: []string{"api"}})
	var failure *CreateError
	if !errors.As(err, &failure) || !errors.Is(err, blocked) || failure.PossiblyCreated || !templateRead.Load() {
		t.Fatalf("blocked submission returned %v", err)
	}
}

type blockedRequestError struct{ error }

func (*blockedRequestError) RequestNotSent() {}

type approvalTransportGuard struct {
	base http.RoundTripper
	err  error
}

func (g approvalTransportGuard) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodPost && req.URL.Path == approvalPath {
		return nil, g.err
	}
	return g.base.RoundTrip(req)
}

func TestCreateTransportPreflightRejectionIsDefinite(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != templatePath {
			t.Errorf("unexpected request after scope change: %s %s", r.Method, r.URL.Path)
			return
		}
		reply(w, testTemplate())
	})
	blocked := &blockedRequestError{errors.New("approval scope changed before send")}
	client.httpClient.Transport = approvalTransportGuard{base: http.DefaultTransport, err: blocked}
	_, err := client.Create(context.Background(), CreateInput{Meego: "123", Apps: []string{"api"}})
	var failure *CreateError
	if !errors.As(err, &failure) || !errors.Is(err, blocked) || failure.PossiblyCreated {
		t.Fatalf("blocked submission returned %v", err)
	}
}

func TestQueryFiltersAndCompactResults(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		want := map[string]string{"page": "3", "size": "5", "processTemplateId": "24", "no": "SNB001", "formData": "中文 & 内容", "isEnd": "0"}
		if r.Method != http.MethodGet || r.URL.Path != approvalPath+"/myCreate" {
			t.Error("incorrect query endpoint")
		}
		for key, value := range want {
			if r.URL.Query().Get(key) != value {
				t.Errorf("wrong query %s", key)
			}
		}
		reply(w, map[string]any{"page": 3, "size": 5, "total": 11, "data": []any{
			map[string]any{"approvalId": 91, "no": "SNB001", "title": "提测审批", "stateLabel": "测试审批", "currentHandlers": []any{map[string]any{"Name": "张三"}}, "createdUser": map[string]any{"realName": "用户", "phone": "private-phone"}, "env": "UAT", "processTemplateId": 24},
		}})
	})
	end := false
	result, err := client.Query(context.Background(), QueryInput{Page: 3, Size: 5, No: "SNB001", Keyword: "中文 & 内容", IsEnd: &end})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 11 || len(result.Items) != 1 || result.Items[0].Status != "测试审批" || result.Items[0].Handlers[0] != "张三" {
		t.Fatalf("result=%#v", result)
	}
	assertCompact(t, result)
}

func TestDetailReturnsThreeInputsAndHistoryWithoutFixedFields(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != approvalPath+"/91" {
			t.Error("wrong detail endpoint")
		}
		reply(w, map[string]any{
			"workOrder": map[string]any{"approvalId": 91, "no": "SNB001", "stateLabel": "处理中"},
			"tpls": []any{map[string]any{"formData": map[string]any{"testApproval": map[string]any{
				"meegoCode": "123", "remark": "备注", "type": "Sprint 提测", "env": "UAT", "businessLine": "OBT",
				"overviewData": []any{map[string]any{"appId": "11020028", "appName": "bp-api-service", "appCnName": "运营外部接口", "version": "最新"}},
			}}}},
			"circulationHistory": []any{map[string]any{"state": "提交申请", "circulation": "新建", "processorName": "用户", "remarks": "ok", "createdAt": "2026-10-30T10:00:00+08:00"}},
		})
	})
	result, err := client.Detail(context.Background(), 91)
	if err != nil {
		t.Fatal(err)
	}
	if result.Meego != "123" || result.Remark != "备注" || !reflect.DeepEqual(result.Apps, []string{"api"}) || len(result.History) != 1 {
		t.Fatalf("result=%#v", result)
	}
	if result.URL != client.config.DragonURL+"/#/workorder/detail/91" {
		t.Fatalf("detail returned incorrect URL: %s", result.URL)
	}
	assertCompact(t, result)
}

func TestDetailUsesWorkOrderStateAndCurrentNodeProcessors(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{
			"workOrder": map[string]any{
				"approvalId": 91, "no": "SNB001", "stateLabel": "", "currentHandlers": nil,
				"state": []any{map[string]any{"id": "test-node", "label": "测试审批", "processor": []int{156, 17}, "processMethod": "person"}},
			},
			"nodes": []any{
				map[string]any{"id": "old-node", "processor": []any{map[string]any{"userId": 35, "realName": "上一节点处理人"}}},
				map[string]any{"id": "test-node", "processor": []any{
					map[string]any{"userId": 156, "realName": "李向丹", "phone": "private-phone"},
					map[string]any{"userId": 17, "realName": "靳保新"},
					map[string]any{"userId": 159, "realName": "未选中处理人"},
				}},
			},
		})
	})
	result, err := client.Detail(context.Background(), 91)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "测试审批" || !reflect.DeepEqual(result.Handlers, []string{"李向丹", "靳保新"}) {
		t.Fatalf("detail status/handlers do not match active state: %#v", result.Approval)
	}
	assertCompact(t, result)
}

func assertCompact(t *testing.T, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"appId", "appName", "appCnName", "version", "businessLine", "\"env\"", "\"type\"", "processTemplateId", "private-phone", "phone"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("fixed/private field leaked: %s", forbidden)
		}
	}
}

func TestAuthenticationErrorsAreRedacted(t *testing.T) {
	const password = "secret password+"
	const rotatedToken = "rotated-token"
	encodedPassword := url.QueryEscape(password)
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("new-token", rotatedToken)
		fmt.Fprintf(w, `{"code":"cached-token","message":%q,"data":{}}`, password+" "+encodedPassword+" "+rotatedToken)
	})
	client.config.Password = password
	_, err := client.Query(context.Background(), QueryInput{})
	if err == nil {
		t.Fatalf("unsafe error: %v", err)
	}
	for _, secret := range []string{password, encodedPassword, "cached-token", rotatedToken} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("unsafe error: %v", err)
		}
	}
}

func TestRedirectCannotReceiveAuthenticationHeaders(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1); reply(w, map[string]any{}) }))
	defer target.Close()
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) })
	if _, err := client.Query(context.Background(), QueryInput{}); err == nil {
		t.Error("redirect accepted")
	}
	if redirected.Load() != 0 {
		t.Error("followed redirect with authentication")
	}
}

func TestEveryEmbeddedAliasIsUnambiguousAndCopiesAreIndependent(t *testing.T) {
	if registryError != nil {
		t.Fatal(registryError)
	}
	if len(registry) != 74 {
		t.Fatalf("application snapshot count = %d", len(registry))
	}
	for _, app := range registry {
		for _, alias := range append([]string{app.Alias, app.AppName}, app.Aliases...) {
			resolved, err := ResolveApplications([]string{strings.ToUpper(alias)})
			if err != nil || len(resolved) != 1 || resolved[0].AppID != app.AppID {
				t.Fatalf("alias %q did not resolve correctly: %v", alias, err)
			}
		}
	}
	first, _ := ResolveApplications([]string{"api"})
	first[0].Aliases[0] = "changed"
	second, _ := ResolveApplications([]string{"api"})
	if second[0].Aliases[0] == "changed" {
		t.Error("caller mutated embedded registry")
	}
}
