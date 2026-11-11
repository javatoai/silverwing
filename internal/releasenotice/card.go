package releasenotice

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

func SessionCard(s Session) (string, error) {
	switch s.Kind {
	case "":
		return Card(s.Title, s.URL, s.Rows)
	case Branches:
		return BranchCard(s.Title, s.URL, s.Rows)
	default:
		return "", errors.New("通知类型无法识别")
	}
}
func Card(title, link string, rows []Row) (string, error) {
	content, err := noticeLink(title, link)
	if err != nil {
		return "", err
	}
	return renderUATElements(title, "申请发布UAT环境版本", content, rows)
}

// applicantInBody reproduces the saved payload of an earlier notification.
func renderUATCard(title, link, applicant string, rows []Row, applicantInBody bool) (string, error) {
	content, e := noticeLink(title, link)
	if e != nil {
		return "", e
	}
	header := "申请发布UAT环境版本"
	if name := strings.Join(strings.Fields(applicant), " "); name != "" {
		if applicantInBody {
			content += "\n申请人：" + escapeNoticeText.Replace(name)
		} else {
			header = name + "：" + header
		}
	}
	return renderUATElements(title, header, content, rows)
}

func renderUATElements(title, header, content string, rows []Row) (string, error) {
	if len(rows) == 0 {
		return "", errors.New("本批次没有可通知的 Tag")
	}
	cells := make([]map[string]string, 0, len(rows))
	for _, r := range rows {
		if r.Service == "" || r.Version == "" {
			return "", errors.New("服务或版本为空")
		}
		cells = append(cells, map[string]string{"service": r.Service, "version": r.Version})
	}
	return marshalNoticeCard(header, header+"｜"+title, "default", []any{
		map[string]any{"tag": "markdown", "content": content, "text_size": "normal"},
		noticeTable("version", "UAT版本", cells),
		map[string]any{"tag": "markdown", "element_id": "genbu_link", "content": "[打开Genbu进行发布](https://genbu.snowballtech.com/)", "text_size": "normal"},
	})
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
	return marshalNoticeCard("上线分支通知", "上线分支通知｜"+title, "fill", []any{
		map[string]any{"tag": "markdown", "content": content, "text_size": "normal"},
		noticeTable("branch", "服务分支", cells),
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
func noticeTable(valueKey, title string, rows []map[string]string) map[string]any {
	serviceWidth, valueWidth := "52%", "48%"
	if valueKey == "branch" {
		serviceWidth, valueWidth = "240px", "auto"
	}
	return map[string]any{"tag": "table", "page_size": min(10, len(rows)), "row_height": "middle",
		"header_style": map[string]any{"background_style": "grey", "bold": true, "text_size": "normal", "lines": 1},
		"columns":      []any{map[string]string{"name": "service", "display_name": "服务", "data_type": "text", "width": serviceWidth}, map[string]string{"name": valueKey, "display_name": title, "data_type": "text", "width": valueWidth}}, "rows": rows}
}
func marshalNoticeCard(header, summary, widthMode string, elements []any) (string, error) {
	card := map[string]any{"schema": "2.0",
		"config": map[string]any{"width_mode": widthMode, "summary": map[string]string{"content": summary}},
		"header": map[string]any{"title": map[string]string{"tag": "plain_text", "content": header}, "template": "blue", "icon": map[string]string{"tag": "standard_icon", "token": "bell_outlined"}},
		"body":   map[string]any{"direction": "vertical", "padding": "12px", "vertical_spacing": "12px", "elements": elements}}
	b, e := json.Marshal(card)
	return string(b), e
}
