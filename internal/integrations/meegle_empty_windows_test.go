//go:build windows

package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

// Exercise the actual command, pagination and field-reading chains without
// talking to Meegle or changing the real application's data directory.
func meegleResponseFixture(t *testing.T) (*Service, domain.AppConfig, func(...string), func() [][]string) {
	t.Helper()
	root := t.TempDir()
	fixture, calls := filepath.Join(root, "responses.json"), filepath.Join(root, "calls.jsonl")
	t.Setenv("SILVERWING_MEEGLE_EMPTY_FIXTURE", fixture)
	cli := filepath.Join(root, "meegle.ps1")
	const script = `$ErrorActionPreference = 'Stop'
$OutputEncoding = [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
if ($args[0] -eq '--version') { Write-Output 'fixture 1.0.0'; exit 0 }
$fixture = Get-Content -Raw -LiteralPath $env:SILVERWING_MEEGLE_EMPTY_FIXTURE -Encoding UTF8 | ConvertFrom-Json
$calls = $env:SILVERWING_MEEGLE_EMPTY_FIXTURE + '.calls'
$index = 0
if (Test-Path -LiteralPath $calls) { $index = @(Get-Content -LiteralPath $calls).Count }
ConvertTo-Json -InputObject @($args) -Compress | Add-Content -LiteralPath $calls -Encoding UTF8
if ($index -ge $fixture.responses.Count) { [Console]::Error.WriteLine('unexpected extra command'); exit 3 }
[Console]::Out.WriteLine($fixture.responses[$index])
`
	if err := os.WriteFile(cli, append([]byte{0xef, 0xbb, 0xbf}, []byte(script)...), 0600); err != nil {
		t.Fatal(err)
	}
	calls = fixture + ".calls"
	config := domain.DefaultAppConfig()
	config.MeegleExecutablePath = &cli
	config.CommandProxyTargets = nil
	service := NewService(storage.NewPaths(filepath.Join(root, "home")), nil, nil)
	set := func(responses ...string) {
		t.Helper()
		data, err := json.Marshal(map[string]any{"responses": responses})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixture, data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(calls); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	readCalls := func() [][]string {
		t.Helper()
		data, err := os.ReadFile(calls)
		if err != nil {
			t.Fatal(err)
		}
		var result [][]string
		for _, line := range strings.Split(strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff")), "\n") {
			var args []string
			if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &args); err != nil {
				t.Fatal(err)
			}
			result = append(result, args)
		}
		return result
	}
	return service, config, set, readCalls
}

func TestMeegleMQLValidEmptyAndPaginationGuards(t *testing.T) {
	service, config, set, calls := meegleResponseFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, tc := range []struct {
		name, response string
		valid          bool
	}{
		{"empty null list", `{"session_id":"session","data":{},"list":null,"search_status_info":null}`, true},
		{"empty array list", `{"session_id":"session","data":{},"list":[]}`, true},
		{"explicit zero", `{"session_id":"session","data":{},"list":[{"count":0}]}`, true},
		{"missing list", `{"session_id":"session","data":{}}`, false},
		{"missing data", `{"session_id":"session","list":null}`, false},
		{"wrong data", `{"session_id":"session","data":[],"list":null}`, false},
		{"positive count without rows", `{"session_id":"session","data":{},"list":[{"count":1}]}`, false},
		{"rows without count", `{"session_id":"session","data":{"1":[{"work_item_id":"1"}]},"list":null}`, false},
		{"error payload", `{"session_id":"session","data":{},"list":null,"error":{"message":"unavailable"}}`, false},
		{"failed status", `{"session_id":"session","data":{},"list":null,"ok":false}`, false},
		{"error without session", `{"data":{},"list":null,"error":{"message":"unavailable"}}`, false},
		{"nonzero code", `{"data":{},"list":null,"code":3003}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set(tc.response)
			value, err := service.executeMQL(ctx, config, "fixture-project", "SELECT work_item_id FROM fixture", 10*time.Second)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t, value=%v, err=%v", tc.valid, value, err)
			}
			if tc.valid && len(responseRows(value, nil)) != 0 {
				t.Fatal("empty query produced rows")
			}
			if len(calls()) != 1 {
				t.Fatal("empty or invalid result unexpectedly paginated")
			}
		})
	}
	page := func(session string, count int, ids ...int) string {
		rows := []any{}
		for _, id := range ids {
			rows = append(rows, map[string]any{"work_item_id": fmt.Sprint(id)})
		}
		data, _ := json.Marshal(map[string]any{"session_id": session, "list": []any{map[string]any{"count": count}}, "data": map[string]any{"1": rows}})
		return string(data)
	}
	ids := make([]int, 50)
	for i := range ids {
		ids[i] = i + 1
	}
	for _, tc := range []struct {
		name, second string
		valid        bool
	}{
		{"complete", page("session", 51, 51), true},
		{"duplicate ID", page("session", 51, 1), false},
		{"changed session", page("other", 51, 51), false},
		{"changed count", page("session", 52, 51), false},
		{"missing next row", page("session", 51), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set(page("session", 51, ids...), tc.second)
			value, err := service.executeMQL(ctx, config, "fixture-project", "SELECT work_item_id FROM fixture", 10*time.Second)
			if (err == nil) != tc.valid || tc.valid && len(responseRows(value, nil)) != 51 {
				t.Fatalf("valid=%t, value=%v, err=%v", tc.valid, value, err)
			}
			if got := calls(); len(got) != 2 || !strings.Contains(strings.Join(got[1], " "), "--session-id session") {
				t.Fatalf("wrong pagination commands: %v", got)
			}
		})
	}
}

func TestMeegleMetadataEmptyPageGuards(t *testing.T) {
	service, config, set, _ := meegleResponseFixture(t)
	for _, tc := range []struct {
		name, response string
		valid          bool
	}{
		{"confirmed null", `{"list":null,"pagination":{"page_num":1,"total":0,"has_more":false}}`, true},
		{"normal empty", `{"list":[],"pagination":{"page_num":1,"has_more":false}}`, true},
		{"missing list", `{"pagination":{"page_num":1,"total":0,"has_more":false}}`, false},
		{"missing total", `{"list":null,"pagination":{"page_num":1,"has_more":false}}`, false},
		{"positive total", `{"list":null,"pagination":{"page_num":1,"total":1,"has_more":false}}`, false},
		{"more pages", `{"list":null,"pagination":{"page_num":1,"total":0,"has_more":true}}`, false},
		{"wrong page", `{"list":null,"pagination":{"page_num":2,"total":0,"has_more":false}}`, false},
		{"error with empty list", `{"ok":false,"list":[],"pagination":{"page_num":1,"total":0,"has_more":false}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set(tc.response)
			rows, err := service.readPages(context.Background(), config, []string{"workitem", "meta-fields"}, 10*time.Second, "fixture fields")
			if (err == nil) != tc.valid || len(rows) != 0 {
				t.Fatalf("valid=%t, rows=%v, err=%v", tc.valid, rows, err)
			}
		})
	}
	set(`{"list":[{"field_key":"one"}],"pagination":{"page_num":1,"has_more":true}}`, `{"list":null,"pagination":{"page_num":2,"total":0,"has_more":false}}`)
	if _, err := service.readPages(context.Background(), config, []string{"workitem", "meta-fields"}, 10*time.Second, "fixture fields"); err == nil {
		t.Fatal("null later page was accepted as a complete list")
	}
}

func TestMeegleRequirementBodyEnglishFallbackAndFieldDeduplication(t *testing.T) {
	service, config, set, calls := meegleResponseFixture(t)
	const empty = `{"list":null,"pagination":{"page_num":1,"total":0,"has_more":false}}`
	const description = `{"list":[{"field_name":"Description","field_key":"description-v2"}],"pagination":{"page_num":1,"has_more":false}}`
	summary := map[string]any{"work_item_attribute": map[string]any{"work_item_type": map[string]any{"key": "real-type"}}}
	set(empty, empty, empty, empty, description, `{"work_item_fields":[{"key":"description-v2","value":"正文 **原始 Markdown**"}]}`)
	body, err := service.requirementBody(context.Background(), config, summary, "project", "userstory", "101")
	if err != nil || body != "正文 **原始 Markdown**" {
		t.Fatalf("English description lookup failed: %q, %v", body, err)
	}
	got := calls()
	if len(got) != 6 || !strings.Contains(strings.Join(got[4], " "), "--field-query Description") || !strings.Contains(strings.Join(got[5], " "), "--fields description-v2") {
		t.Fatalf("did not read the discovered field key: %v", got)
	}
	set(description, `{"work_item_fields":[]}`, description, empty, empty, description, `{"list":[{"field_name":"Content","field_key":"content-v2"}],"pagination":{"page_num":1,"has_more":false}}`, `{"work_item_fields":[{"key":"content-v2","value":"备用正文"}]}`)
	body, err = service.requirementBody(context.Background(), config, summary, "project", "userstory", "101")
	if err != nil || body != "备用正文" || len(calls()) != 8 {
		t.Fatalf("empty duplicate field was read repeatedly: body=%q err=%v calls=%v", body, err, calls())
	}
	set(empty, empty, empty, empty, empty, empty, empty)
	if body, err := service.requirementBody(context.Background(), config, summary, "project", "userstory", "101"); err != nil || body != "" {
		t.Fatalf("genuinely empty body rejected: %q %v", body, err)
	}
	set(description, `not JSON`)
	if _, err := service.requirementBody(context.Background(), config, summary, "project", "userstory", "101"); err == nil {
		t.Fatal("failed body read was treated as empty text")
	}
}
