package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

const serviceCatalogDirectActionsMinWidth = 960

func catalogRuntimeDisabledReason(service map[string]any) string {
	switch {
	case !mapBool(service, "enabled", true):
		return "此服务已停用"
	case strings.TrimSpace(mapString(service, "genbuServiceName")) == "":
		return "请在仓库配置填写 Genbu 服务名"
	}
	return ""
}

const serviceCatalogActionHeight = 32

func serviceCatalogButton(c *ui.Context, p palette, key string) ui.Element {
	return ui.ButtonBase(c.Key(key)).Height(serviceCatalogActionHeight).Shrink(0).Padding(0, 10).Radius(8).Center().
		FocusRing(true).Background(p.surface).Border(1, p.line)
}

func serviceCatalogTextAction(c *ui.Context, p palette, key, text, accessibleLabel, reason string, busy bool) bool {
	disabled := busy || reason != ""
	button := serviceCatalogButton(c, p, key).Label(accessibleLabel).Description(reason).
		Tooltip(firstNonEmpty(reason, accessibleLabel)).Disabled(disabled)
	color := p.text
	if disabled {
		color = p.muted
	} else if button.Hovered() {
		button.Background(p.soft)
	}
	button.Children(func() { label(c, text, 12, color).NoWrap() })
	return button.Clicked()
}

func (s *app) renderServiceCatalogActions(c *ui.Context, p palette, service application.Service, runtimeVisible, compact bool) {
	config := s.readServiceConfig(service.ID)
	runtimeVisible = runtimeVisible && mapBool(config, "genbuProbeEnabled", false)
	reason := ""
	if runtimeVisible {
		reason = catalogRuntimeDisabledReason(config)
	}
	environment := s.catalogRuntimeEnvironment()
	uat := environment == "uat"
	showUAT := runtimeVisible && reason == "" && uat
	uatBusy := s.busy || s.taskServicesUnit.uatWriteRunning()
	target := service.Name + " " + strings.ToUpper(environment)
	type runtimeAction struct {
		key, label string
		kind       taskServiceDetailKind
	}
	actions := []runtimeAction{
		{"pods", "Pod 明细", taskServicePodsDetail},
		{"config", "查看配置", taskServiceConfigDetail},
	}
	if uat {
		actions = append(actions, runtimeAction{"edit", "修改配置", taskServiceConfigEditDetail})
	}
	ui.Row(c.Key("catalog-row-actions-" + service.ID)).Shrink(0).Gap(6).AlignItems(ui.Center).Children(func() {
		if showUAT && uatReleaseButton(c, p, "catalog-uat-release-"+service.ID, service.Name, serviceCatalogActionHeight, uatBusy) {
			s.openCatalogUATPodAction(service.ID, uatRelease)
		}
		if runtimeVisible && !compact {
			for _, action := range actions {
				if serviceCatalogTextAction(c, p, "catalog-runtime-"+action.key+"-"+service.ID, action.label,
					target+" "+action.label, reason, s.busy) {
					s.openCatalogRuntimeDialog(service.ID, action.kind)
				}
			}
		}
		if serviceCatalogTextAction(c, p, "service-config-"+service.ID, "仓库配置", "配置仓库 "+service.Name, "", s.busy) {
			s.openServiceEditor(service.ID, "basic")
		}
		key := "catalog-row-menu-" + service.ID
		trigger := serviceCatalogButton(c, p, key).Width(serviceCatalogActionHeight).Padding(0)
		s.renderOverflowMenu(c, p, trigger, key, "查看更多 "+service.Name, s.busy,
			ui.AnchorBottomRight, ui.AnchorTopRight, func(menu *overflowMenuBuilder) {
				if runtimeVisible && (compact || uat) {
					if compact {
						for _, action := range actions {
							if menu.Item(action.key, action.label).DisabledReason(reason).Chosen() {
								s.openCatalogRuntimeDialog(service.ID, action.kind)
							}
						}
					}
					if uat && menu.Item("approvals", "查看配置审批").DisabledReason(reason).Chosen() {
						s.openCatalogRuntimeApprovals(service.ID)
					}
					if showUAT && menu.Item("uat-restart", uatRestartLabel).Icon("refresh").Disabled(uatBusy).Chosen() {
						s.openCatalogUATPodAction(service.ID, uatRestart)
					}
					menu.Separator()
				}
				if menu.Item("remove", "移除").Icon("delete").Dangerous().Chosen() {
					s.confirmNative("移除仓库？", "这不会删除本地 Git 仓库，也不会改动已有任务；如果仍被任务引用，后端会阻止移除。", service.Name, func() {
						s.act("service.remove", "", service.ID, "", "", "", nil)
					})
				}
			})
	})
}
