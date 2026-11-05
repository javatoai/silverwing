package releasenotice

import (
	"encoding/json"
	"silverwing-go/internal/domain"
	"strings"
	"testing"
)

func TestBranchCardDynamicLinkedHeadlineAndServiceTable(t *testing.T) {
	service, branch := "android-transit-service", "feature/zhen.wang_7123064844_blacklist_rules_v2"
	raw, e := BranchCard("需求 [A]", "https://project.feishu.cn/obt/userstory/detail/123", []Row{{Service: service, Branch: branch}, {Service: "worker", Branch: "release/b"}})
	if e != nil {
		t.Fatal(e)
	}
	var card struct {
		Schema string
		Config struct {
			WidthMode string `json:"width_mode"`
			Summary   struct{ Content string }
		}
		Header struct{ Title struct{ Content string } }
		Body   struct{ Elements []map[string]any }
	}
	if e = json.Unmarshal([]byte(raw), &card); e != nil {
		t.Fatal(e)
	}
	if card.Schema != "2.0" || card.Config.WidthMode != "fill" || card.Header.Title.Content != "上线分支通知" || card.Config.Summary.Content != "上线分支通知｜需求 [A]" || len(card.Body.Elements) != 2 {
		t.Fatal(raw)
	}
	if card.Body.Elements[0]["content"] != "**[需求 &#91;A&#93;](https://project.feishu.cn/obt/userstory/detail/123)**" {
		t.Fatal(raw)
	}
	table := card.Body.Elements[1]
	if table["tag"] != "table" || len(table["rows"].([]any)) != 2 || table["columns"].([]any)[1].(map[string]any)["display_name"] != "服务分支" {
		t.Fatal(raw)
	}
	columns := table["columns"].([]any)
	if columns[0].(map[string]any)["width"] != "240px" || columns[1].(map[string]any)["width"] != "auto" {
		t.Fatal(raw)
	}
	first := table["rows"].([]any)[0].(map[string]any)
	if first["service"] != service || first["branch"] != branch {
		t.Fatal("long service or branch changed", raw)
	}
	if table["rows"].([]any)[1].(map[string]any)["branch"] != "release/b" || strings.Contains(raw, "UAT") || strings.Contains(raw, "genbu") {
		t.Fatal(raw)
	}
}
func TestBranchRowsPreserveTaskOrderAndDistinctBranches(t *testing.T) {
	rows, e := BranchRows([]domain.ServiceWorkspace{{ServiceName: "B", Branch: "feature/b"}, {ServiceName: "A", Branch: "feature/a"}, {ServiceName: "B", Branch: "feature/b"}, {ServiceName: "B", Branch: "release/b"}})
	if e != nil || len(rows) != 3 || rows[0].Service != "B" || rows[1].Service != "A" || rows[2].Branch != "release/b" {
		t.Fatal(rows, e)
	}
	for _, services := range [][]domain.ServiceWorkspace{nil, {{ServiceName: "api"}}, {{Branch: "feature/a"}}} {
		if _, e = BranchRows(services); e == nil {
			t.Fatal("incomplete branches accepted")
		}
	}
}
