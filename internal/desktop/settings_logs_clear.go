package desktop

import (
	"fmt"
	"path/filepath"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func (s *app) settingsLogsActionCurrent(action application.Action) bool {
	return s.page == "settings" && s.settingsUnit.state.settingsPage == "logs" && s.snapshot.Home != "" && payloadString(action.Payload, "expectedHome") == s.snapshot.Home
}

func (s *app) settingsLogsClearButton(c *ui.Context, p palette) {
	text := "清空全部日志"
	if s.busy && s.operationKind == "logs.clear" {
		text = "正在清空…"
	}
	button := ui.ButtonBase(c.Key("logs-clear-all")).Row().Height(34).Padding(0, 9).Gap(5).Radius(7).AlignItems(ui.Center).
		Label(text).Disabled(s.busy || s.settingsUnit.state.settingsPickerBusy || s.snapshot.Home == "")
	if button.Hovered() {
		button.Background(p.soft)
	}
	button.Children(func() { icon(c, "delete", 15, p.danger); label(c, text, 12, p.danger).NoWrap() })
	if button.Clicked() {
		s.beginSettingsLogsClear()
	}
}

func (s *app) beginSettingsLogsClear() {
	action := application.Action{Kind: "logs.clear", Payload: map[string]any{"confirmed": true, "expectedHome": s.snapshot.Home, "settingsLogsAction": true}}
	if s.busy || !s.settingsLogsActionCurrent(action) {
		return
	}
	s.showConfirmationDialog("清空全部日志", "将清空此应用的当前日志和按日归档日志，无法撤销。清空后会继续记录新日志。",
		filepath.Join(s.snapshot.Home, "logs"), []confirmationDialogButton{{label: "清空全部日志"}, {label: "取消"}}, 1, 1,
		func(index int) { s.confirmSettingsLogsClear(action, index) })
}

func (s *app) confirmSettingsLogsClear(action application.Action, index int) {
	if index != 0 || s.busy || !s.settingsLogsActionCurrent(action) {
		return
	}
	s.dispatch(action)
}

// The ordinary mutation runner owns busy/cancellation. Only the captured logs
// page receives this operation's outcome; refresh reads the real post-clear
// contents, including any new events, instead of blindly clearing a live view.
func (s *app) finishSettingsLogsAction(action application.Action, result application.ActionResult, err error) bool {
	managed, _ := action.Payload["settingsLogsAction"].(bool)
	if !managed {
		return false
	}
	if !s.settingsLogsActionCurrent(action) {
		return true
	}
	if err != nil {
		s.lastError = err.Error()
		if action.Kind == "logs.list" {
			s.lastError = "清空结果已返回，但刷新日志失败：" + err.Error()
		}
		return true
	}
	if action.Kind == "logs.list" {
		s.applySettingsActionResult(action, result)
		s.lastError = payloadString(action.Payload, "settingsLogsClearFailure")
		if s.lastError == "" {
			s.lastMessage = payloadString(action.Payload, "settingsLogsClearSuccess")
		}
		return true
	}
	if action.Kind != "logs.clear" {
		return false
	}
	data := mapData(result.Data)
	if data == nil {
		s.lastError = "清空结果无效，请刷新日志确认。"
		return true
	}
	changed, failed := mapBool(data, "changed", false), dataInt64(data, "failedFiles")
	failure, success := "", "应用日志已清空。"
	if failed > 0 {
		failure = fmt.Sprintf("已清空 %d 个，%d 个未完成：%s", dataInt64(data, "clearedFiles"), failed, mapString(data, "error"))
		if !changed {
			s.lastError = failure
			return true
		}
	}
	refresh := application.Action{Kind: "logs.list", Payload: map[string]any{"expectedHome": s.snapshot.Home, "settingsLogsAction": true, "filter": "recent", "settingsLogsClearFailure": failure, "settingsLogsClearSuccess": success}}
	s.dispatch(refresh)
	return true
}
