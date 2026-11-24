package releasenotice

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

func SessionCard(s Session) (string, error) {
	if s.Kind != Branches {
		return "", errors.New("通知类型无法识别")
	}
	return BranchCard(s.Title, s.URL, s.Rows)
}

func BranchCard(title, link string, rows []Row) (string, error) {
	content, e := noticeLink(title, link)
	if e != nil {
		return "", e
	}
	if len(rows) == 0 {
		return "", errors.New("任务没有可发送的服务分支")
	}
	cells := make([]map[string]string, 0, len(rows))
	for _, r := range rows {
		if r.Service == "" || r.Branch == "" {
			return "", errors.New("服务名称或分支为空")
		}
		cells = append(cells, map[string]string{"service": r.Service, "branch": r.Branch})
	}
	return marshalBranchNoticeCard(title, []any{
		map[string]any{"tag": "markdown", "content": content, "text_size": "normal"},
		branchNoticeTable(cells),
	})
}

var escapeNoticeText = strings.NewReplacer("&", "&amp;", "<", "&#60;", ">", "&#62;", "[", "&#91;", "]", "&#93;", "*", "&#42;", "_", "&#95;", "~", "&#126;", "`", "&#96;", "\\", "&#92;")

func noticeLink(title, link string) (string, error) {
	if strings.TrimSpace(title) == "" {
		return "", errors.New("需求标题为空")
	}
	u, e := url.Parse(link)
	if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", errors.New("任务缺少有效的需求链接")
	}
	return "**[" + escapeNoticeText.Replace(title) + "](" + strings.NewReplacer("(", "%28", ")", "%29").Replace(u.String()) + ")**", nil
}
func branchNoticeTable(rows []map[string]string) map[string]any {
	return map[string]any{"tag": "table", "page_size": min(10, len(rows)), "row_height": "middle",
		"header_style": map[string]any{"background_style": "grey", "bold": true, "text_size": "normal", "lines": 1},
		"columns": []any{
			map[string]string{"name": "service", "display_name": "服务", "data_type": "text", "width": "240px"},
			map[string]string{"name": "branch", "display_name": "服务分支", "data_type": "text", "width": "auto"},
		}, "rows": rows}
}
func marshalBranchNoticeCard(title string, elements []any) (string, error) {
	const header = "上线分支通知"
	card := map[string]any{"schema": "2.0",
		"config": map[string]any{"width_mode": "fill", "summary": map[string]string{"content": header + "｜" + title}},
		"header": map[string]any{"title": map[string]string{"tag": "plain_text", "content": header}, "template": "blue", "icon": map[string]string{"tag": "standard_icon", "token": "bell_outlined"}},
		"body":   map[string]any{"direction": "vertical", "padding": "12px", "vertical_spacing": "12px", "elements": elements}}
	b, e := json.Marshal(card)
	return string(b), e
}
