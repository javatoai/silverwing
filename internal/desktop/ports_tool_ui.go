package desktop

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"silverwing-go/internal/tools"

	"github.com/egoist/mygo/ui"
)

func (s *app) renderPortsTool(c *ui.Context, p palette, state *toolPageState) {
	visible := state.visiblePortProcesses()
	ui.Column(c).Grow(1).MinHeight(0).Gap(11).Children(func() {
		ui.Column(c).Shrink(0).Padding(15).Gap(10).Background(p.surface).Border(1, p.line).Radius(12).Children(func() {
			ui.Row(c).Height(34).Gap(8).Children(func() {
				label(c, "本机端口进程", 14, p.text).FontWeight(600)
				ui.Spacer(c)
				if toolButtonWithEnabled(c, "ports-refresh", "重新查询", p, false, s.toolOperationEnabled(state, "tool.ports")) {
					s.queryPorts()
				}
			})
			ui.Row(c).Height(40).Gap(8).Children(func() {
				accepted := state.portFilter
				ui.TextInput(c.Key("tool-port-filter"), &state.portFilter).Width(260).Height(38).Placeholder("请输入端口号（1–65535）").Label("端口号").OnChange(func() {
					// Match the source's five UTF-16 Char.isDigit candidate rule:
					// an invalid edit preserves the prior input and query state.
					count := 0
					for _, value := range state.portFilter {
						count++
						if count > 5 || value > 0xffff || !unicode.IsDigit(value) {
							state.portFilter = accepted
							return
						}
					}
					if state.portFilter == accepted {
						return
					}
					accepted = state.portFilter
					s.cancelDeveloperToolJob("tool.ports")
					state.portFilterRevision++
					state.portItems = []tools.PortProcess{}
					state.portSelected = -1
					state.portConfirm = false
					state.portHasQuery = false
					state.portLastStatus = ""
					state.portLastMessage = ""
					state.portInputError = ""
				}).OnSubmit(func() { s.runDeveloperToolPrimaryAction(state, "端口") })
				if toolButtonWithEnabled(c, "ports-query", "查询", p, true, s.toolPrimaryActionEnabled(state, "端口")) {
					s.runDeveloperToolPrimaryAction(state, "端口")
				}
				if toolToggle(c, "ports-show-tcp", "TCP", state.portShowTCP, p) {
					state.setPortProtocolVisible("TCP", !state.portShowTCP)
				}
				if toolToggle(c, "ports-show-udp", "UDP", state.portShowUDP, p) {
					state.setPortProtocolVisible("UDP", !state.portShowUDP)
				}
			})
			label(c, "结束操作会重新核对查询返回的完整进程身份。", 11, p.muted)
			if state.portInputError != "" {
				label(c, state.portInputError, 11, p.amber)
			}
		})
		if state.portLastMessage != "" {
			status := portStatusLabel(state.portLastStatus)
			bg, fg := portStatusColors(state.portLastStatus, p)
			ui.Row(c).Padding(9, 12).Gap(8).Background(bg).Radius(8).Children(func() {
				if status != "" {
					label(c, status, 11, fg).FontWeight(600)
				}
				label(c, state.portLastMessage, 12, fg)
			})
		}
		ui.Row(c).Height(30).Shrink(0).Padding(0, 9).Gap(12).Children(func() {
			label(c, fmt.Sprintf("显示 %d / %d 个进程 · %d 个端点", len(visible), len(state.portItems), countVisiblePortEndpoints(visible)), 11, p.muted)
			if state.portSelected >= 0 && state.portSelected < len(state.portItems) {
				selected := state.portItems[state.portSelected]
				label(c, fmt.Sprintf("已选择 %s · PID %d", firstNonEmpty(selected.Name, "未知进程"), selected.PID), 11, p.blue)
			}
		})
		ui.Column(c).Grow(1).MinHeight(0).Background(p.surface).Border(1, p.line).Radius(12).Children(func() {
			ui.Row(c).Height(34).Shrink(0).Padding(0, 10).Gap(12).Background(p.soft).Children(func() {
				label(c, "协议 / 状态", 10, p.muted).Width(105)
				label(c, "主要端点", 10, p.muted).Width(185)
				label(c, "进程 · 端点数", 10, p.muted).Grow(1)
				label(c, "PID", 10, p.muted).Width(76)
				label(c, "启动时间", 10, p.muted).Width(170)
				label(c, "操作", 10, p.muted).Width(190)
			})
			ui.Divider(c)
			if len(visible) == 0 {
				ui.Column(c).Grow(1).Center().Children(func() {
					if len(state.portItems) > 0 {
						label(c, "当前协议筛选下没有进程。", 12, p.muted)
					} else if state.portHasQuery {
						label(c, "该端口当前没有监听进程。", 12, p.muted)
					} else {
						label(c, "输入一个 1–65535 的端口号并查询。", 12, p.muted)
					}
				})
			} else {
				ui.Scroll(c).Grow(1).MinHeight(0).Padding(6).Gap(3).Children(func() {
					for _, row := range visible {
						s.renderPortRow(c, p, state, row.index, row.process)
					}
				})
			}
		})
		if item, ok := state.visibleSelectedPort(); ok {
			s.renderPortDetails(c, p, state, item)
		}
		if state.portConfirm && state.portSelected >= 0 && state.portSelected < len(state.portItems) {
			s.renderPortConfirmation(c, p, state)
		}
	})
}

// Filter copies keep row selection bound to the complete query result. Protocol
// controls change presentation without changing the process identity or jobs.
type visiblePortProcess struct {
	index   int
	process tools.PortProcess
}

func (s *toolPageState) visiblePortProcesses() []visiblePortProcess {
	rows := make([]visiblePortProcess, 0, len(s.portItems))
	for index, item := range s.portItems {
		endpoints := make([]tools.PortEndpoint, 0, len(portEndpoints(item)))
		for _, endpoint := range portEndpoints(item) {
			if endpoint.Protocol == "TCP" && s.portShowTCP || endpoint.Protocol == "UDP" && s.portShowUDP {
				endpoints = append(endpoints, endpoint)
			}
		}
		if len(endpoints) == 0 {
			continue
		}
		item.Endpoints = endpoints
		item.Protocol, item.Address, item.Port, item.State = endpoints[0].Protocol, endpoints[0].Address, endpoints[0].Port, endpoints[0].State
		rows = append(rows, visiblePortProcess{index: index, process: item})
	}
	return rows
}

func (s *toolPageState) visibleSelectedPort() (tools.PortProcess, bool) {
	for _, row := range s.visiblePortProcesses() {
		if row.index == s.portSelected {
			return row.process, true
		}
	}
	return tools.PortProcess{}, false
}

func (s *toolPageState) setPortProtocolVisible(protocol string, visible bool) {
	switch protocol {
	case "TCP":
		s.portShowTCP = visible
	case "UDP":
		s.portShowUDP = visible
	default:
		return
	}
	if _, ok := s.visibleSelectedPort(); !ok {
		s.portSelected = -1
		s.portConfirm = false
	}
}

func countVisiblePortEndpoints(rows []visiblePortProcess) int {
	count := 0
	for _, row := range rows {
		count += effectiveEndpointCount(row.process)
	}
	return count
}

func (s *app) renderPortRow(c *ui.Context, p palette, state *toolPageState, index int, item tools.PortProcess) {
	selected := index == state.portSelected
	bg := p.surface
	if selected {
		bg = p.selected
	}
	row := ui.Row(c.Key(fmt.Sprintf("port-row-%d-%s", item.PID, item.StartedAt))).FillWidth().Height(48).Padding(0, 10).Gap(12).Background(bg).Radius(6)
	row.Children(func() {
		label(c, portProtocolAndState(item), 10, p.blue).Width(105).FontWeight(600).NoWrap()
		label(c, portPrimaryEndpoint(item), 10, p.text).Width(185).Font("Consolas").SingleLine()
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			label(c, firstNonEmpty(item.Name, "未知进程"), 11, p.text).SingleLine()
			label(c, fmt.Sprintf("%d 个监听端点", effectiveEndpointCount(item)), 9, p.muted).SingleLine()
		})
		label(c, strconv.Itoa(item.PID), 10, p.text).Width(76).Font("Consolas")
		label(c, firstNonEmpty(item.StartTimeDisplay, "无法确认"), 10, p.muted).Width(170).SingleLine()
		if iconButton(c, fmt.Sprintf("port-copy-info-%d-%s", item.PID, item.StartedAt), "copy", "复制进程信息", p).Clicked() {
			s.copyTextToClipboard(c.Services(), portProcessSummary(item), "")
		}
		if selected && item.CanTerminate {
			if toolButton(c, fmt.Sprintf("port-select-end-%d-%s", item.PID, item.StartedAt), "结束…", p, false) {
				state.portConfirm = true
			}
		} else if selected {
			label(c, "已选择 · 不可结束", 10, p.muted).Width(104)
		} else {
			if toolButton(c, fmt.Sprintf("port-select-process-%d-%s", item.PID, item.StartedAt), "选择", p, false) {
				state.portSelected = index
				state.portConfirm = false
			}
		}
	})
}

func (s *app) renderPortDetails(c *ui.Context, p palette, state *toolPageState, item tools.PortProcess) {
	ui.Column(c).Shrink(0).Padding(11, 13).Gap(7).Background(p.surface).Border(1, p.line).Radius(10).Children(func() {
		ui.Row(c).Height(26).Gap(8).Children(func() {
			label(c, "进程详情", 12, p.text).FontWeight(600)
			label(c, fmt.Sprintf("PID %d · %s", item.PID, firstNonEmpty(item.Name, "未知进程")), 11, p.blue).Grow(1)
			if iconButton(c, fmt.Sprintf("port-detail-copy-%d", item.PID), "copy", "复制进程详情", p).Clicked() {
				s.copyTextToClipboard(c.Services(), portProcessSummary(item), "")
			}
		})
		ui.Row(c).Gap(14).Children(func() {
			label(c, "用户："+firstNonEmpty(item.User, "未知"), 10, p.muted).Grow(1).SingleLine()
			label(c, "状态："+firstNonEmpty(item.State, "未知"), 10, p.muted).Grow(1).SingleLine()
			label(c, "启动时间："+firstNonEmpty(item.StartTimeDisplay, "无法确认"), 10, p.muted).Grow(1).SingleLine()
		})
		label(c, "路径："+firstNonEmpty(item.Path, "未知"), 10, p.text).SingleLine()
		if item.CommandLine != "" {
			label(c, "命令行："+item.CommandLine, 10, p.muted).SingleLine()
		}
		ui.Row(c).Height(21).Gap(8).Children(func() {
			label(c, fmt.Sprintf("监听端点（%d）", effectiveEndpointCount(item)), 10, p.muted).FontWeight(600)
			ui.Spacer(c)
			if item.CanTerminate {
				label(c, "可在身份复核后结束", 9, p.muted)
			} else {
				label(c, "缺少可信身份，不能结束", 9, p.amber)
			}
		})
		ui.Scroll(c).Height(76).Gap(2).Children(func() {
			for _, endpoint := range portEndpoints(item) {
				ui.Row(c).Height(21).Gap(10).Children(func() {
					label(c, fmt.Sprintf("%s  %s:%d", endpoint.Protocol, endpoint.Address, endpoint.Port), 10, p.text).Font("Consolas").Grow(1).SingleLine()
					label(c, firstNonEmpty(endpoint.State, "未知状态"), 9, p.muted).Width(112).SingleLine()
				})
			}
		})
	})
}

func (s *app) renderPortConfirmation(c *ui.Context, p palette, state *toolPageState) {
	item := state.portItems[state.portSelected]
	ui.Row(c).Padding(12).Gap(12).Background(p.amberBg).Border(1, p.amber).Radius(10).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			label(c, "确认结束此进程？", 13, p.amber).FontWeight(600)
			label(c, fmt.Sprintf("%s · %s · %s · PID %d · 启动于 %s", item.Name, item.Protocol, item.Address, item.PID, item.StartTimeDisplay), 11, p.text).SingleLine()
			label(c, "身份会在执行前按进程启动时间再次核对。", 10, p.muted)
		})
		if toolButton(c, "port-cancel-end", "取消", p, false) {
			state.portConfirm = false
		}
		if toolButtonWithEnabled(c, "port-confirm-end", "确认结束", p, true, state.portStoppingPID == 0) {
			s.terminateSelectedPort()
		}
	})
}

func (s *app) queryPorts() {
	state := s.ensureToolState()
	s.cancelDeveloperToolJob("tool.ports")
	filter := strings.TrimSpace(state.portFilter)
	if filter == "" {
		state.portInputError = "请输入要查询的端口号（1–65535）。"
		return
	}
	port, err := strconv.Atoi(filter)
	if err != nil || port < 1 || port > 65535 {
		state.portInputError = "端口必须是 1 到 65535 的数字。"
		return
	}
	state.portInputError = ""
	state.portRequest++
	state.portSelected = -1
	state.portConfirm = false
	state.portHasQuery = false
	state.portItems = []tools.PortProcess{}
	state.portLastStatus = ""
	state.portLastMessage = ""
	s.act("tool.ports", "", "", "", "", "", map[string]any{"port": port, "request": state.portRequest, "filterRevision": state.portFilterRevision})
}

func (s *app) terminateSelectedPort() {
	state := s.ensureToolState()
	if state.portStoppingPID != 0 {
		state.portInputError = "已有端口结束操作正在等待真实结果。"
		state.portConfirm = false
		return
	}
	if state.portSelected < 0 || state.portSelected >= len(state.portItems) {
		state.portInputError = "请先从查询结果中选择进程。"
		state.portConfirm = false
		return
	}
	item := state.portItems[state.portSelected]
	if !item.CanTerminate {
		state.portInputError = "此进程缺少可信的启动时间，不能结束。"
		state.portConfirm = false
		return
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		state.portInputError = "进程身份无法编码：" + err.Error()
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		state.portInputError = "进程身份无法编码：" + err.Error()
		return
	}
	payload["confirmed"] = true
	payload["request"] = state.portRequest
	payload["filterRevision"] = state.portFilterRevision
	state.portStopSequence++
	if state.portStopSequence == 0 {
		state.portStopSequence = 1
	}
	state.portStoppingSequence = state.portStopSequence
	payload["stopRequest"] = state.portStoppingSequence
	state.portStoppingPID = item.PID
	state.portInputError = ""
	state.portConfirm = false
	s.act("tool.port-stop", "", "", "", "", "", payload)
}

func portProcessSummary(item tools.PortProcess) string {
	var details strings.Builder
	fmt.Fprintf(&details, "进程：%s\nPID：%d\n用户：%s\n状态：%s\n启动时间：%s\n路径：%s\n命令行：%s\n监听端点：\n",
		firstNonEmpty(item.Name, "未知进程"), item.PID, firstNonEmpty(item.User, "未知"), firstNonEmpty(item.State, "未知"),
		firstNonEmpty(item.StartTimeDisplay, "无法确认"), firstNonEmpty(item.Path, "未知"), firstNonEmpty(item.CommandLine, "未知"))
	for _, endpoint := range portEndpoints(item) {
		fmt.Fprintf(&details, "%s %s:%d · %s\n", endpoint.Protocol, endpoint.Address, endpoint.Port, firstNonEmpty(endpoint.State, "未知状态"))
	}
	return strings.TrimRight(details.String(), "\n")
}

func portEndpoints(item tools.PortProcess) []tools.PortEndpoint {
	if len(item.Endpoints) != 0 {
		return item.Endpoints
	}
	if item.Protocol == "" && item.Address == "" && item.Port == 0 && item.State == "" {
		return nil
	}
	return []tools.PortEndpoint{{Protocol: item.Protocol, Address: item.Address, Port: item.Port, State: item.State}}
}

func effectiveEndpointCount(item tools.PortProcess) int { return len(portEndpoints(item)) }

func countPortEndpoints(items []tools.PortProcess) int {
	count := 0
	for _, item := range items {
		count += effectiveEndpointCount(item)
	}
	return count
}

func portProtocolAndState(item tools.PortProcess) string {
	protocol := item.Protocol
	state := item.State
	if len(item.Endpoints) > 0 {
		if protocol == "" {
			protocol = item.Endpoints[0].Protocol
		}
		if state == "" {
			state = item.Endpoints[0].State
		}
	}
	return firstNonEmpty(protocol, "未知协议") + " · " + firstNonEmpty(state, "未知状态")
}

func portPrimaryEndpoint(item tools.PortProcess) string {
	endpoints := portEndpoints(item)
	if len(endpoints) == 0 {
		return "无端点"
	}
	endpoint := endpoints[0]
	return fmt.Sprintf("%s:%d", endpoint.Address, endpoint.Port)
}

func portStatusLabel(status string) string {
	switch status {
	case "EXITED":
		return "进程已结束"
	case "PORT_REOCCUPIED":
		return "端口再次被占用"
	case "TIMED_OUT":
		return "等待超时"
	case "STILL_RUNNING":
		return "进程仍在运行"
	case "NOT_OWNED":
		return "进程已不占用端口"
	case "MISSING_IDENTITY":
		return "身份无法确认"
	case "IDENTITY_CHANGED":
		return "进程身份已变化"
	case "SYSTEM_PROCESS":
		return "系统进程"
	case "CURRENT_APPLICATION":
		return "当前应用进程"
	case "CONFIRMATION_REQUIRED":
		return "需要确认"
	case "FAILED":
		return "操作失败"
	case "UNKNOWN":
		return "结果待确认"
	default:
		return status
	}
}

func portStatusColors(status string, p palette) (ui.Color, ui.Color) {
	if status == "EXITED" {
		return p.greenBg, p.green
	}
	return p.amberBg, p.amber
}
