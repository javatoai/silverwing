package desktop

import (
	"fmt"
	"strings"
	"time"

	"silverwing-go/internal/tools"

	"github.com/egoist/mygo/ui"
)

func (s *app) renderTimeTool(c *ui.Context, p palette, state *toolPageState) {
	zone := state.timeZone
	if zone == "" {
		zone = "Asia/Shanghai"
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		location = time.Local
	}

	ui.Column(c).Grow(1).MinHeight(0).Gap(12).Children(func() {
		currentPanel := ui.Column(c.Key("time-current-panel")).Grow(0).Padding(16).Gap(10).Background(p.surface).Border(1, p.line).Radius(12)
		displayedNow := ui.Local(currentPanel, "current-time-snapshot", time.Now)
		now := time.Now()
		if now.Unix() != displayedNow.Unix() {
			*displayedNow = now
		}
		nextTick := time.Second - time.Duration(now.Nanosecond()/int(time.Millisecond))*time.Millisecond
		c.After(nextTick)
		localNow := displayedNow.In(location)
		currentPanel.Children(func() {
			ui.Row(c).Height(24).Gap(8).Children(func() {
				label(c, "当前时间", 14, p.text).FontWeight(600)
				ui.Spacer(c)
				label(c, "点击数值即可复制", 11, p.muted)
			})
			ui.Row(c).Gap(8).Children(func() {
				s.renderTimeCopyValue(c, p, "time-current-date", "日期", formatChineseTime(localNow.Format("2006-01-02 15:04:05.000")), "当前日期已复制", 1.4)
				s.renderTimeCopyValue(c, p, "time-current-seconds", "秒时间戳", fmt.Sprint(localNow.Unix()), "秒时间戳已复制", 1)
				s.renderTimeCopyValue(c, p, "time-current-milliseconds", "毫秒时间戳", fmt.Sprint(localNow.UnixMilli()), "毫秒时间戳已复制", 1)
			})
		})

		ui.Column(c).Grow(1).MinHeight(0).Padding(16).Gap(10).Background(p.surface).Border(1, p.line).Radius(12).Children(func() {
			ui.Row(c).Height(28).Gap(8).Children(func() {
				label(c, "智能转换", 14, p.text).FontWeight(600)
				ui.Spacer(c)
				if toolButtonWithEnabled(c, "time-clear", "清空", p, false, timeClearEnabled(state)) {
					s.cancelDeveloperToolJob("tool.time")
					clearTimeConversionInput(state)
				}
			})
			input := state.timeInput
			editor := ui.TextInput(c.Key("tool-time-input"), &input).FillWidth().Height(38).
				Placeholder("1791417600 或 2026-10-09 12:30:00").Label("日期或时间戳")
			editor.OnChange(func() {
				if s.acceptTimeToolInput(state, input) {
					s.queueTimeConversion()
				} else {
					input = state.timeInput
				}
			})
			editor.OnPaste(func(pasted string) bool {
				if !developerToolInputTooLarge(pasted) {
					return false
				}
				s.rejectTimeToolInput(state)
				return true
			})
			editor.OnSubmit(func() { s.runDeveloperToolPrimaryAction(state, "日期时间") })

			switch {
			case state.timeError != "":
				ui.Row(c).Padding(9, 11).Background(p.amberBg).Radius(8).Children(func() {
					label(c, state.timeError, 12, p.amber).Wrap()
				})
			case state.timeHasData:
				s.renderTimeResult(c, p, state.timeResult)
			case state.timePending || s.developerToolJobBusy("tool.time"):
				label(c, "正在识别…", 12, p.muted).Padding(4, 0)
			default:
				label(c, "支持日期、10 位秒时间戳和 13 位毫秒时间戳", 12, p.muted).Padding(4, 0)
			}
		})
	})
}

func (s *app) renderTimeCopyValue(c *ui.Context, p palette, key, title, value, feedback string, grow float32) {
	button := ui.ButtonBase(c.Key(key)).Column().Grow(grow).MinWidth(0).MinHeight(64).Padding(9, 10).Gap(5).Radius(8).
		Border(1, p.line).Label("复制" + title).Tooltip("点击数值即可复制")
	background := p.soft
	if button.Hovered() {
		background = p.selected
	}
	button.Background(background).Children(func() {
		label(c, title, 10, p.muted).Wrap()
		label(c, value, 13, p.blue).Font("Consolas").FontWeight(600).Wrap()
	})
	if button.Clicked() {
		s.copyTextToClipboard(c.Services(), value, feedback)
	}
}

func (s *app) renderTimeResult(c *ui.Context, p palette, result tools.TimeConversion) {
	kind := result.InputKind
	kindLabel := kind
	switch kind {
	case "DATE_TIME":
		kindLabel = "日期时间"
	case "SECONDS_TIMESTAMP":
		kindLabel = "秒时间戳"
	case "MILLISECONDS_TIMESTAMP":
		kindLabel = "毫秒时间戳"
	}
	label(c, "已识别："+kindLabel, 11, p.muted)

	if kind == "DATE_TIME" {
		ui.Row(c).Gap(8).Children(func() {
			s.renderTimeCopyValue(c, p, "time-result-seconds", "秒时间戳", fmt.Sprint(result.EpochSeconds), "转换秒时间戳已复制", 1)
			s.renderTimeCopyValue(c, p, "time-result-milliseconds", "毫秒时间戳", fmt.Sprint(result.EpochMilliseconds), "转换毫秒时间戳已复制", 1)
		})
		return
	}

	ui.Row(c).Children(func() {
		s.renderTimeCopyValue(c, p, "time-result-date", "日期时间", formatChineseTime(result.Formatted), "转换日期已复制", 1)
	})
}

func formatChineseTime(value string) string {
	// Use the same numeric local-date format as tools.TimeConversion.Formatted.
	return strings.TrimSpace(value)
}
