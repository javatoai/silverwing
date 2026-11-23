package genbuapi

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestApprovalDetailFieldsPreservesConfigFileBodies(t *testing.T) {
	const yaml = "feature: enabled\r\ncredentials:\r\n  password: sample-password\r\n"
	const configJSON = `{"port":8080,"token":"sample-token"}`
	const properties = "webhook.url=https://example.test/send?access_token=sample-webhook-token\n"
	row := map[string]any{
		"approve_type":        json.Number("7"),
		"reason":              "更新开关，token=reason-value",
		"message":             "saved using actual-session-token",
		"external_id":         json.Number("1234"),
		"ding_approve_number": "DING-2345",
		"hidden":              "hidden-row-value",
		"content": map[string]any{
			"name":      "service-runtime",
			"namespace": "service",
			"version":   "release-3",
			"env":       "DEV",
			"hidden":    "hidden-content-value",
			"encoded_data": []any{
				map[string]any{"name": "app.yaml", "content": yaml},
				map[string]any{"name": "app.json", "content": configJSON},
				map[string]any{"name": "app.properties", "content": properties},
				map[string]any{"name": "api-key", "content": "sample-key"},
			},
		},
	}
	fields := approvalDetailFields(row, "actual-session-token")
	want := map[string]string{
		"审批备注":                    "更新开关，token=<redacted>",
		"执行消息":                    "saved using <redacted>",
		"工单号":                     "1234",
		"钉钉审批号":                   "DING-2345",
		"配置文件 1 (app.yaml)":       yaml,
		"配置文件 2 (app.json)":       configJSON,
		"配置文件 3 (app.properties)": properties,
		"配置文件 4 (api-key)":        "sample-key",
	}
	if !reflect.DeepEqual(fields, want) {
		t.Errorf("detail fields = %v, want %v", fields, want)
	}
}

func TestRedactApprovalTextMasksAmbiguousYAMLStructures(t *testing.T) {
	for _, value := range []string{
		"feature: { auth: {\n  nested: secret-value\n}}",
		"env:\n  - name: DB_PASSWORD\n    value: secret-value\n",
	} {
		if got := redactApprovalText(value); got != "<redacted>" {
			t.Errorf("redacted text = %q, want the ambiguous value hidden", got)
		}
	}
}

func TestRedactApprovalTextMasksCredentialsInAdditionalFormats(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{
			name: "Java properties continuation",
			text: "spring.datasource.password=first\\\nsecond\nfeature.enabled=true",
			want: "<redacted>",
		},
		{
			name: "Java properties whitespace separator",
			text: "db.password secret-value\nfeature.enabled true",
			want: "<redacted>",
		},
		{
			name: "YAML indentless sequence",
			text: "password: # from the old release\n# override\n- second\nfeature: true",
			want: "<redacted>",
		},
		{
			name: "YAML folded plain scalar",
			text: "password: first\n  second\nfeature: true",
			want: "<redacted>",
		},
		{
			name: "YAML tagged block",
			text: "password: !!binary |\n  c2VjcmV0\nfeature: true",
			want: "<redacted>",
		},
		{
			name: "inline Basic authorization",
			text: "Legacy request used Authorization: Basic dXNlcjpzZWNyZXQ= and failed",
			want: "Legacy request used Authorization: <redacted> and failed",
		},
		{
			name: "XML properties",
			text: "<?xml version=\"1.0\"?>\n<properties><entry key=\"db.pass&#x77;ord\">secret-value</entry></properties>",
			want: "<redacted>",
		},
		{
			name: "ordinary Java properties",
			text: "spring.application.name=demo\nspring.datasource.password=secret-value\nfeature.enabled true",
			want: "spring.application.name=demo\nspring.datasource.password=<redacted>\nfeature.enabled true",
		},
		{
			name: "ordinary XML properties",
			text: "<properties><entry key=\"feature.enabled\">true</entry></properties>",
			want: "<properties><entry key=\"feature.enabled\">true</entry></properties>",
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := redactApprovalText(item.text); got != item.want {
				t.Errorf("redacted text = %q, want %q", got, item.want)
			}
		})
	}
}

func TestApprovalDetailFieldsAllowListsProposedSettings(t *testing.T) {
	cases := []struct {
		name    string
		kind    int
		content map[string]any
		want    map[string]string
	}{
		{
			name: "Service",
			kind: 2,
			content: map[string]any{
				"name": "test-service",
				"port_data": []any{map[string]any{
					"service_port": 80, "container_port": 8080, "agreement": "TCP", "auth": "hidden-auth",
				}},
			},
			want: map[string]string{"Service 名称": "test-service", "服务端口 1": "80", "容器端口 1": "8080", "端口协议 1": "TCP"},
		},
		{
			name:    "Ingress",
			kind:    3,
			content: map[string]any{"name": "private-ingress", "rule_data": []any{map[string]any{"host_name": "example.test", "context_path": "/api"}}},
			want:    map[string]string{"Ingress 名称": "private-ingress", "域名 1": "example.test", "上下文路径 1": "/api"},
		},
		{
			name:    "Group",
			kind:    6,
			content: map[string]any{"pod_group_name": "backend", "k8s_namespace": "services", "image_version": "v3", "pod_num": 2, "hidden": "hidden"},
			want:    map[string]string{"Group 名称": "backend", "镜像版本": "v3", "Pod 数": "2"},
		},
		{
			name:    "unrecognized type",
			kind:    99,
			content: map[string]any{"name": "unrelated", "password": "secret"},
			want:    map[string]string{},
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			fields := approvalDetailFields(map[string]any{"approve_type": item.kind, "content": item.content})
			if !reflect.DeepEqual(fields, item.want) {
				t.Errorf("detail fields = %v, want %v", fields, item.want)
			}
		})
	}
}
